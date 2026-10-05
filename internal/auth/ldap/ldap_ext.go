// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package ldap

// buropher 拡張（Redmine の AuthSourceLdap に無い機能）:
//
//   - CA 証明書（PEM の本文またはファイルのパス）をシステムの証明書に追加して TLS を検証する。
//   - 複数ホストのフェイルオーバ（接続できなければ FailoverHosts を順に試す。bind の失敗では切り替えない）。
//   - Active Directory プリセット（DirectoryType = active_directory）: ネストしたグループを
//     LDAP_MATCHING_RULE_IN_CHAIN で取得し、userAccountControl の ACCOUNTDISABLE（0x2）で無効なアカウントを判定する。
//   - グループの取得（GroupConfig）: ユーザーエントリの memberOf 属性、またはグループの検索
//     （member / uniqueMember は DN、memberUid はログイン名で照合）。
//   - Lookup: 定期同期用に、サービスアカウントでユーザーの属性・グループ・無効状態を引く。

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"

	goldap "github.com/go-ldap/ldap/v3"
)

// DirectoryType の値。
const (
	DirectoryGeneric         = ""
	DirectoryOpenLDAP        = "openldap"
	DirectoryActiveDirectory = "active_directory"
)

// GroupConfig.Mode の値。
const (
	GroupModeNone     = ""
	GroupModeMemberOf = "memberof"
	GroupModeSearch   = "search"
)

// InChainOID は LDAP_MATCHING_RULE_IN_CHAIN（Active Directory のネストしたグループの所属判定）。
const InChainOID = "1.2.840.113556.1.4.1941"

// uacAccountDisable は userAccountControl の ACCOUNTDISABLE ビット。
const uacAccountDisable = 0x2

// GroupConfig はグループの取得方法。
type GroupConfig struct {
	// Mode は "memberof"（ユーザーの memberOf 属性）/ "search"（グループを検索）/ ""（取得しない）。
	Mode string
	// BaseDN はグループの検索ベース（空なら Source.BaseDN）。
	BaseDN string
	// Filter はグループのフィルタ（空なら (objectClass=groupOfNames) / AD は (objectClass=group)）。
	Filter string
	// MemberAttr はグループ側のメンバー属性（member / uniqueMember / memberUid。空なら member）。
	MemberAttr string
	// MemberOfAttr はユーザー側の所属属性（空なら memberOf）。
	MemberOfAttr string
	// Nested はネストしたグループも含める（AD は LDAP_MATCHING_RULE_IN_CHAIN、それ以外は親グループを再帰的に検索）。
	Nested bool
}

// Enabled はグループを取得する設定か。
func (g GroupConfig) Enabled() bool { return g.Mode == GroupModeMemberOf || g.Mode == GroupModeSearch }

func (g GroupConfig) memberOfAttr() string {
	if g.MemberOfAttr != "" {
		return g.MemberOfAttr
	}
	return "memberOf"
}

func (g GroupConfig) memberAttr() string {
	if g.MemberAttr != "" {
		return g.MemberAttr
	}
	return "member"
}

// IsActiveDirectory は Active Directory プリセットか。
func (s *Source) IsActiveDirectory() bool { return s.DirectoryType == DirectoryActiveDirectory }

func (s *Source) groupBase() string {
	if s.Groups.BaseDN != "" {
		return s.Groups.BaseDN
	}
	return s.BaseDN
}

func (s *Source) groupFilter() string {
	if f, ok := normalizeFilter(s.Groups.Filter); ok && f != "" {
		return f
	}
	if s.IsActiveDirectory() {
		return "(objectClass=group)"
	}
	return "(objectClass=groupOfNames)"
}

type endpoint struct {
	host string
	port int
}

// endpoints は接続先（Host:Port、続いて FailoverHosts）。
func (s *Source) endpoints() []endpoint {
	out := []endpoint{{s.Host, s.Port}}
	for _, h := range s.FailoverHosts {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		port := s.Port
		if hh, p, err := net.SplitHostPort(h); err == nil {
			if n, err := strconv.Atoi(p); err == nil {
				h, port = hh, n
			}
		}
		out = append(out, endpoint{h, port})
	}
	return out
}

// ParseHostList は改行・カンマ・空白区切りのホスト一覧を分割する（フォームの入力用）。
func ParseHostList(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' || r == ' ' || r == '\t' }) {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// tlsConfig は TLS の設定（VerifyPeer が false なら検証しない。CACert があればシステムの証明書に追加する）。
func (s *Source) tlsConfig(host string) (*tls.Config, error) {
	tc := &tls.Config{InsecureSkipVerify: !s.VerifyPeer, ServerName: host}
	if strings.TrimSpace(s.CACert) == "" || !s.VerifyPeer {
		return tc, nil
	}
	pool, err := LoadCACert(s.CACert)
	if err != nil {
		return nil, err
	}
	tc.RootCAs = pool
	return tc, nil
}

// LoadCACert は CA 証明書（PEM の本文またはファイルのパス）をシステムの証明書に加えたプールを返す。
func LoadCACert(v string) (*x509.CertPool, error) {
	v = strings.TrimSpace(v)
	data := []byte(v)
	if !strings.Contains(v, "-----BEGIN") {
		b, err := os.ReadFile(v)
		if err != nil {
			return nil, newError("cannot read CA certificate: " + err.Error())
		}
		data = b
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(data) {
		return nil, newError("invalid CA certificate")
	}
	return pool, nil
}

// ValidCACert は CA 証明書の設定が読めるか（空は true）。
func ValidCACert(v string) bool {
	if strings.TrimSpace(v) == "" {
		return true
	}
	_, err := LoadCACert(v)
	return err == nil
}

// userGroups はユーザーが所属するグループの DN を返す（重複なし・取得順）。
func (ss *session) userGroups(conn *goldap.Conn, userDN, login string, entry *goldap.Entry) ([]string, error) {
	s := ss.s
	g := s.Groups
	var groups []string
	switch {
	case g.Nested && s.IsActiveDirectory():
		// AD: member:1.2.840.113556.1.4.1941: でネストを含めて検索する
		res, err := ss.searchGroups(conn, "(member:"+InChainOID+":="+goldap.EscapeFilter(userDN)+")")
		if err != nil {
			return nil, err
		}
		groups = res
	case g.Mode == GroupModeMemberOf:
		if entry == nil {
			return nil, nil
		}
		// グループの検索ベースが指定されていれば、その下のグループだけを使う（対応表は CN だけでも一致するため、
		// ディレクトリの別の場所に同じ CN のグループを作れるユーザーが対応するグループに入れてしまう）
		var base *goldap.DN
		if g.BaseDN != "" {
			b, err := goldap.ParseDN(g.BaseDN)
			if err != nil {
				return nil, err
			}
			base = b
		}
		for _, a := range entry.Attributes {
			if !strings.EqualFold(a.Name, g.memberOfAttr()) {
				continue
			}
			for _, v := range a.Values {
				if base != nil {
					if pd, err := goldap.ParseDN(v); err != nil || !(base.EqualFold(pd) || base.AncestorOfFold(pd)) {
						continue
					}
				}
				groups = append(groups, v)
			}
		}
	default:
		value := userDN
		if strings.EqualFold(g.memberAttr(), "memberUid") {
			value = login
		}
		res, err := ss.searchGroups(conn, eqFilter(g.memberAttr(), value))
		if err != nil {
			return nil, err
		}
		groups = res
	}
	if g.Nested && !s.IsActiveDirectory() && !strings.EqualFold(g.memberAttr(), "memberUid") {
		// 親グループを再帰的にたどる（深さ 10 まで）
		seen := map[string]bool{}
		for _, dn := range groups {
			seen[strings.ToLower(dn)] = true
		}
		frontier := groups
		for depth := 0; depth < 10 && len(frontier) > 0; depth++ {
			var next []string
			for _, dn := range frontier {
				parents, err := ss.searchGroups(conn, eqFilter(g.memberAttr(), dn))
				if err != nil {
					return nil, err
				}
				for _, p := range parents {
					if !seen[strings.ToLower(p)] {
						seen[strings.ToLower(p)] = true
						groups = append(groups, p)
						next = append(next, p)
					}
				}
			}
			frontier = next
		}
	}
	return uniqueFold(groups), nil
}

// searchGroups はグループの検索ベースで (&<group filter><cond>) に一致するエントリの DN を返す。
func (ss *session) searchGroups(conn *goldap.Conn, cond string) ([]string, error) {
	s := ss.s
	req := goldap.NewSearchRequest(s.groupBase(), goldap.ScopeWholeSubtree, goldap.NeverDerefAliases, 0, 0, false,
		"(&"+s.groupFilter()+cond+")", []string{"dn"}, nil)
	res, err := conn.Search(req)
	if err != nil {
		// 結果コードが成功以外（busy / insufficientAccessRights 等）でも「グループなし」とはしない
		// （同期で所属が削除されるため。呼び出し側はグループを取得しなかったものとして扱う）
		return nil, err
	}
	var out []string
	for _, e := range res.Entries {
		out = append(out, e.DN)
	}
	return out, nil
}

func uniqueFold(list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range list {
		k := strings.ToLower(v)
		if v == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, v)
	}
	return out
}

// GroupMatches は対応表の外部グループ名 external が LDAP グループ dn に一致するか
// （DN として同じ、または先頭の RDN の値（CN）が同じ。大文字小文字を区別しない）。
func GroupMatches(external, dn string) bool {
	external = strings.TrimSpace(external)
	if external == "" || dn == "" {
		return false
	}
	if strings.EqualFold(external, dn) {
		return true
	}
	pd, err := goldap.ParseDN(dn)
	if err != nil || len(pd.RDNs) == 0 {
		return false
	}
	if strings.Contains(external, "=") {
		pe, err := goldap.ParseDN(external)
		return err == nil && pe.EqualFold(pd)
	}
	for _, a := range pd.RDNs[0].Attributes {
		if strings.EqualFold(a.Value, external) {
			return true
		}
	}
	return false
}

// UserInfo は Lookup の結果（定期同期用）。
type UserInfo struct {
	DN        string
	Firstname string
	Lastname  string
	Mail      string
	// Disabled は AD の userAccountControl に ACCOUNTDISABLE が立っている。
	Disabled bool
	// Groups / GroupsFetched は Attrs と同じ。
	Groups        []string
	GroupsFetched bool
}

// ErrNotSearchable はサービスアカウントで検索できない設定（account に $login を含む）。
var ErrNotSearchable = errors.New("LDAP: the account must not contain $login to synchronize users")

// Lookup はサービスアカウントで login のユーザーを検索する（見つからなければ nil）。
func (s *Source) Lookup(login string) (*UserInfo, error) {
	if strings.Contains(s.Account, "$login") {
		return nil, ErrNotSearchable
	}
	var out *UserInfo
	err := s.run(func(ss *session) error {
		conn, err := ss.dial()
		if err != nil {
			return err
		}
		ok, err := bind(conn, s.Account, s.AccountPassword)
		if err != nil {
			return err
		}
		if !ok {
			return newError("invalid credentials for the account")
		}
		attrNames := compact([]string{"dn", s.AttrFirstname, s.AttrLastname, s.AttrMail})
		if s.IsActiveDirectory() {
			attrNames = append(attrNames, "userAccountControl")
		}
		if s.Groups.Mode == GroupModeMemberOf {
			attrNames = append(attrNames, s.Groups.memberOfAttr())
		}
		req := goldap.NewSearchRequest(s.BaseDN, goldap.ScopeWholeSubtree, goldap.NeverDerefAliases, 0, 0, false,
			s.searchFilter(eqFilter(s.AttrLogin, login)), attrNames, nil)
		res, err := conn.Search(req)
		if err != nil {
			// 認証（net-ldap 互換）と違い、検索の失敗を「見つからない」とはしない。
			// 一時的な障害（busy / unavailable）や ACL・検索ベースの誤りで定期同期が全員をロックしないように
			return err
		}
		if len(res.Entries) == 0 {
			return nil
		}
		e := res.Entries[len(res.Entries)-1]
		info := &UserInfo{DN: e.DN, Firstname: getAttr(e, s.AttrFirstname), Lastname: getAttr(e, s.AttrLastname),
			Mail: getAttr(e, s.AttrMail)}
		if s.IsActiveDirectory() {
			if n, err := strconv.ParseInt(e.GetEqualFoldAttributeValue("userAccountControl"), 10, 64); err == nil {
				info.Disabled = n&uacAccountDisable != 0
			}
		}
		if s.Groups.Enabled() {
			groups, err := ss.userGroups(conn, e.DN, login, e)
			if err != nil {
				return err
			}
			info.Groups, info.GroupsFetched = groups, true
		}
		out = info
		return nil
	})
	return out, err
}
