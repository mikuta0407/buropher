// Package ldap は Redmine の AuthSourceLdap（app/models/auth_source_ldap.rb）の認証・検索部分を
// github.com/go-ldap/ldap/v3 で移植したもの。
//
// Redmine（net-ldap）との対応:
//
//   - 接続方式 ldap_mode: ldap（平文）/ ldaps_verify_none（simple_tls・証明書検証なし）/
//     ldaps_verify_peer（simple_tls・証明書検証あり）。buropher 拡張として STARTTLS（StartTLS=true、
//     平文接続後に StartTLS。証明書検証は VerifyPeer に従う）を持つ。
//   - buropher 拡張（ldap_ext.go）: CA 証明書の指定、複数ホストのフェイルオーバ、Active Directory プリセット、
//     グループの取得（memberOf 属性 / グループ検索。ネストは LDAP_MATCHING_RULE_IN_CHAIN）、定期同期用の Lookup。
//   - bind: account / account_password の両方が空なら匿名、そうでなければ simple bind。
//     account に "$login" を含むと、ログイン名を DN エスケープして置換し、ユーザー自身のパスワードで bind する。
//   - 検索フィルタ: (&(&(objectClass=*)<filter>)(<attr_login>=<login>))。
//   - 検索時の bind 失敗は（net-ldap の search と同じく）例外にせず「該当なし」とする。
//   - 属性値は複数値なら先頭（get_attr）。属性名は大文字小文字を区別しない（net-ldap の Entry と同じ）。
//   - ネットワーク例外は *Error（AuthSourceException）、タイムアウト（既定 20 秒）は Timeout=true の *Error
//     （AuthSourceTimeoutException）。メッセージは "LDAP: <理由>"。
package ldap

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"

	goldap "github.com/go-ldap/ldap/v3"
)

// AuthMethodName は AuthSourceLdap#auth_method_name。
const AuthMethodName = "LDAP"

// DefaultTimeout は with_timeout の既定（timeout が未設定・0 以下のとき 20 秒）。
const DefaultTimeout = 20 * time.Second

// Source は LDAP 認証ソースの接続設定（auth_sources 行の config + 復号済みの account_password）。
type Source struct {
	ID               int64
	Name             string
	Host             string
	Port             int
	Account          string
	AccountPassword  string
	BaseDN           string
	Filter           string
	Timeout          int
	TLS              bool
	VerifyPeer       bool
	StartTLS         bool
	AttrLogin        string
	AttrFirstname    string
	AttrLastname     string
	AttrMail         string
	OntheflyRegister bool

	// 以下は buropher 拡張（ldap_ext.go）。

	// FailoverHosts は Host:Port に接続できないときに順に試す予備のホスト（"host" または "host:port"。
	// ポート省略時は Port）。
	FailoverHosts []string
	// CACert は TLS（LDAPS / STARTTLS）の検証に追加する CA 証明書（PEM の本文、またはファイルのパス）。
	CACert string
	// DirectoryType は "openldap" / "active_directory"（空は汎用）。
	DirectoryType string
	// Groups はグループの取得方法（Mode が空なら取得しない）。
	Groups GroupConfig
}

// Attrs は authenticate / search が返すユーザー属性（Redmine の attrs ハッシュ）。
type Attrs struct {
	DN           string
	Login        string
	Firstname    string
	Lastname     string
	Mail         string
	AuthSourceID int64
	// Groups はユーザーが所属する LDAP グループの DN（buropher 拡張。Source.Groups.Mode が空なら nil）。
	Groups []string
	// GroupsFetched はグループを取得したか（取得に失敗・未設定なら false。false のときグループは同期しない）。
	GroupsFetched bool
}

// Error は AuthSourceException / AuthSourceTimeoutException。
type Error struct {
	Message string
	Timeout bool
}

func (e *Error) Error() string { return e.Message }

// IsAuthSourceError は err が *Error なら true。
func IsAuthSourceError(err error) bool {
	var e *Error
	return errors.As(err, &e)
}

func newError(msg string) *Error { return &Error{Message: AuthMethodName + ": " + msg} }

// Searchable は searchable?（account に $login を含まず、4 属性がすべて設定されている）。
func (s *Source) Searchable() bool {
	return !strings.Contains(s.Account, "$login") && s.AttrLogin != "" && s.AttrFirstname != "" &&
		s.AttrLastname != "" && s.AttrMail != ""
}

func (s *Source) timeout() time.Duration {
	if s.Timeout > 0 {
		return time.Duration(s.Timeout) * time.Second
	}
	return DefaultTimeout
}

// session は with_timeout の 1 回分（期限と開いた接続）。期限を過ぎたら接続を閉じる。
type session struct {
	s        *Source
	deadline time.Time
	conns    []*goldap.Conn
}

func (s *Source) newSession() *session {
	return &session{s: s, deadline: time.Now().Add(s.timeout())}
}

func (ss *session) close() {
	for _, c := range ss.conns {
		c.Close()
	}
}

func (ss *session) remaining() time.Duration {
	d := time.Until(ss.deadline)
	if d < time.Millisecond {
		d = time.Millisecond
	}
	return d
}

// run は f を期限付きで実行する（Timeout.timeout(timeout) { yield }）。
func (s *Source) run(f func(ss *session) error) error {
	ss := s.newSession()
	done := make(chan error, 1)
	go func() { done <- f(ss) }()
	timer := time.NewTimer(ss.remaining())
	defer timer.Stop()
	select {
	case err := <-done:
		ss.close()
		return s.wrap(err)
	case <-timer.C:
		ss.close()
		<-done
		return &Error{Message: AuthMethodName + ": execution expired", Timeout: true}
	}
}

// wrap はネットワーク例外を AuthSourceException に変換する（NETWORK_EXCEPTIONS の rescue）。
func (s *Source) wrap(err error) error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return &Error{Message: AuthMethodName + ": execution expired", Timeout: true}
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return newError(fmt.Sprintf("Connection refused - connect(2) for %s:%d", s.Host, s.Port))
	}
	var le *goldap.Error
	if errors.As(err, &le) && le.Err != nil {
		if errors.Is(le.Err, syscall.ECONNREFUSED) {
			return newError(fmt.Sprintf("Connection refused - connect(2) for %s:%d", s.Host, s.Port))
		}
		return newError(le.Err.Error())
	}
	return newError(err.Error())
}

// dial は initialize_ldap_con の接続部分（bind は呼び出し側）。接続できなければ予備のホストを順に試す。
func (ss *session) dial() (*goldap.Conn, error) {
	s := ss.s
	var firstErr error
	for _, ep := range s.endpoints() {
		conn, err := ss.dialHost(ep.host, ep.port)
		if err == nil {
			return conn, nil
		}
		if firstErr == nil {
			firstErr = err
		}
		var le *Error
		if errors.As(err, &le) || time.Until(ss.deadline) <= 0 {
			// 設定の誤り（CA 証明書など）や期限切れは次のホストを試さない
			break
		}
	}
	return nil, firstErr
}

// dialHost は 1 つのホストに接続する（TLS / STARTTLS を含む）。
func (ss *session) dialHost(host string, port int) (*goldap.Conn, error) {
	s := ss.s
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: ss.remaining()}
	tc, err := s.tlsConfig(host)
	if err != nil {
		return nil, err
	}
	var conn *goldap.Conn
	if s.TLS {
		conn, err = goldap.DialURL("ldaps://"+addr, goldap.DialWithDialer(dialer), goldap.DialWithTLSConfig(tc))
	} else {
		conn, err = goldap.DialURL("ldap://"+addr, goldap.DialWithDialer(dialer))
	}
	if err != nil {
		return nil, err
	}
	ss.conns = append(ss.conns, conn)
	conn.SetTimeout(ss.remaining())
	if !s.TLS && s.StartTLS {
		if err := conn.StartTLS(tc); err != nil {
			return nil, err
		}
	}
	return conn, nil
}

// bind は simple bind を行い、資格情報の誤りなら false（例外にしない）を返す。
// user / password がともに空なら匿名（bind しない）。
func bind(conn *goldap.Conn, user, password string) (bool, error) {
	if user == "" && password == "" {
		return true, nil
	}
	var err error
	if password == "" {
		err = conn.UnauthenticatedBind(user)
	} else {
		err = conn.Bind(user, password)
	}
	if err == nil {
		return true, nil
	}
	var le *goldap.Error
	if errors.As(err, &le) && le.ResultCode != goldap.ErrorNetwork && le.ResultCode != goldap.ErrorUnexpectedResponse {
		return false, nil
	}
	return false, err
}

// authenticateDN は authenticate_dn（DN とパスワードで bind できれば true）。
func (ss *session) authenticateDN(dn, password string) (bool, error) {
	if dn == "" || password == "" {
		return false, nil
	}
	conn, err := ss.dial()
	if err != nil {
		return false, err
	}
	return bind(conn, dn, password)
}

// Authenticate は AuthSourceLdap#authenticate。認証できれば属性（dn 以外）を、できなければ nil を返す。
func (s *Source) Authenticate(login, password string) (*Attrs, error) {
	if strings.TrimSpace(login) == "" || strings.TrimSpace(password) == "" {
		return nil, nil
	}
	var out *Attrs
	err := s.run(func(ss *session) error {
		attrs, entry, conn, err := ss.getUserDN(login, password)
		if err != nil || attrs == nil || attrs.DN == "" {
			return err
		}
		ok, err := ss.authenticateDN(attrs.DN, password)
		if err != nil || !ok {
			return err
		}
		a := *attrs
		if s.Groups.Enabled() {
			// グループの取得の失敗は認証の失敗にしない（グループは同期しない）
			if groups, gerr := ss.userGroups(conn, attrs.DN, login, entry); gerr == nil {
				a.Groups, a.GroupsFetched = groups, true
			}
		}
		a.DN = ""
		out = &a
		return nil
	})
	return out, err
}

// getUserDN は get_user_dn（ログイン名から DN と、オンザフライ登録なら属性を得る）。
// グループの取得のため、検索したエントリと検索に使った接続も返す。
func (ss *session) getUserDN(login, password string) (*Attrs, *goldap.Entry, *goldap.Conn, error) {
	s := ss.s
	user, pw := s.Account, s.AccountPassword
	if strings.Contains(s.Account, "$login") {
		user, pw = strings.Replace(s.Account, "$login", EscapeDN(login), 1), password
	}
	conn, err := ss.dial()
	if err != nil {
		return nil, nil, nil, err
	}
	ok, err := bind(conn, user, pw)
	if err != nil {
		return nil, nil, nil, err
	}
	if !ok {
		// net-ldap の search は bind 失敗時に結果なしを返す
		return &Attrs{}, nil, conn, nil
	}
	attrNames := []string{"dn"}
	if s.OntheflyRegister {
		attrNames = append(attrNames, s.AttrFirstname, s.AttrLastname, s.AttrMail)
	}
	if s.Groups.Mode == GroupModeMemberOf {
		attrNames = append(attrNames, s.Groups.memberOfAttr())
	}
	req := goldap.NewSearchRequest(s.BaseDN, goldap.ScopeWholeSubtree, goldap.NeverDerefAliases, 0, 0, false,
		s.searchFilter(eqFilter(s.AttrLogin, login)), compact(attrNames), nil)
	res, err := conn.Search(req)
	if err != nil {
		if res == nil || !searchResultUsable(err) {
			return nil, nil, conn, searchError(err)
		}
	}
	attrs := &Attrs{}
	var entry *goldap.Entry
	for _, e := range res.Entries {
		if s.OntheflyRegister {
			attrs = s.entryAttrs(e)
		} else {
			attrs = &Attrs{DN: e.DN}
		}
		entry = e
	}
	return attrs, entry, conn, nil
}

// searchResultUsable はサーバのエラー応答（検索結果コードが成功以外）を結果なしとして扱えるか。
func searchResultUsable(err error) bool {
	var le *goldap.Error
	return errors.As(err, &le) && le.ResultCode != goldap.ErrorNetwork && le.ResultCode != goldap.ErrorUnexpectedResponse
}

func searchError(err error) error {
	var le *goldap.Error
	if errors.As(err, &le) && le.ResultCode != goldap.ErrorNetwork && le.ResultCode != goldap.ErrorUnexpectedResponse {
		// net-ldap は検索の失敗コードを例外にしない
		return nil
	}
	return err
}

func compact(ss []string) []string {
	out := ss[:0:0]
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// entryAttrs は get_user_attributes_from_ldap_entry。
func (s *Source) entryAttrs(e *goldap.Entry) *Attrs {
	return &Attrs{
		DN:           e.DN,
		Firstname:    getAttr(e, s.AttrFirstname),
		Lastname:     getAttr(e, s.AttrLastname),
		Mail:         getAttr(e, s.AttrMail),
		AuthSourceID: s.ID,
	}
}

// getAttr は AuthSourceLdap.get_attr（先頭の値。属性名が空なら空文字列）。
func getAttr(e *goldap.Entry, name string) string {
	if name == "" {
		return ""
	}
	if strings.EqualFold(name, "dn") {
		return e.DN
	}
	return e.GetEqualFoldAttributeValue(name)
}

// TestConnection は AuthSourceLdap#test_connection。bindFailedMessage は l(:error_ldap_bind_credentials)。
func (s *Source) TestConnection(bindFailedMessage string) error {
	return s.run(func(ss *session) error {
		conn, err := ss.dial()
		if err != nil {
			return err
		}
		// ldap_con.open {}: 接続して bind する（bind の失敗は無視される）
		if _, err := bind(conn, s.Account, s.AccountPassword); err != nil {
			return err
		}
		if s.Account != "" && !strings.Contains(s.Account, "$login") && s.AccountPassword != "" {
			ok, err := ss.authenticateDN(s.Account, s.AccountPassword)
			if err != nil {
				return err
			}
			if !ok {
				return &Error{Message: bindFailedMessage}
			}
		}
		return nil
	})
}

// Search は AuthSourceLdap#search（attr_login の前方一致で最大 10 件）。
func (s *Source) Search(q string) ([]Attrs, error) {
	q = strings.TrimSpace(q)
	if !s.Searchable() || q == "" {
		return nil, nil
	}
	var out []Attrs
	err := s.run(func(ss *session) error {
		conn, err := ss.dial()
		if err != nil {
			return err
		}
		ok, err := bind(conn, s.Account, s.AccountPassword)
		if err != nil || !ok {
			return err
		}
		req := goldap.NewSearchRequest(s.BaseDN, goldap.ScopeWholeSubtree, goldap.NeverDerefAliases, 10, 0, false,
			s.searchFilter("("+s.AttrLogin+"="+goldap.EscapeFilter(q)+"*)"),
			[]string{"dn", s.AttrLogin, s.AttrFirstname, s.AttrLastname, s.AttrMail}, nil)
		res, err := conn.Search(req)
		if err != nil && (res == nil || !goldap.IsErrorWithCode(err, goldap.LDAPResultSizeLimitExceeded)) {
			if res == nil || !searchResultUsable(err) {
				return searchError(err)
			}
		}
		for _, e := range res.Entries {
			a := s.entryAttrs(e)
			a.Login = getAttr(e, s.AttrLogin)
			out = append(out, *a)
		}
		return nil
	})
	return out, err
}

// eqFilter は Net::LDAP::Filter.equals（値をエスケープした等価フィルタ）。
func eqFilter(attr, value string) string {
	return "(" + attr + "=" + goldap.EscapeFilter(value) + ")"
}

// searchFilter は base_filter & extra（base_filter = (objectClass=*) & ldap_filter）。
func (s *Source) searchFilter(extra string) string {
	base := "(objectClass=*)"
	if f, ok := normalizeFilter(s.Filter); ok && f != "" {
		base = "(&" + base + f + ")"
	}
	return "(&" + base + extra + ")"
}

// normalizeFilter は Net::LDAP::Filter.construct の入力（括弧なしの単一条件も可）を
// 括弧付きのフィルタに直し、構文を検証する。空なら ("", true)。
func normalizeFilter(f string) (string, bool) {
	f = strings.TrimSpace(f)
	if f == "" {
		return "", true
	}
	if !strings.HasPrefix(f, "(") {
		f = "(" + f + ")"
	}
	if _, err := goldap.CompileFilter(f); err != nil {
		return "", false
	}
	return f, true
}

// ValidFilter は validate_filter（filter が空でなく、解析できなければ false）。
func ValidFilter(f string) bool {
	if strings.TrimSpace(f) == "" {
		return true
	}
	_, ok := normalizeFilter(f)
	return ok
}

// EscapeDN は Net::LDAP::DN.escape（, + " \ < > ; と、先頭の空白・#、末尾の空白をバックスラッシュでエスケープ）。
func EscapeDN(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case strings.IndexByte(",+\"\\<>;", c) >= 0,
			i == 0 && (c == ' ' || c == '#'),
			i == len(s)-1 && c == ' ':
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	return b.String()
}
