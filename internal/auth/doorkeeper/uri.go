// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package doorkeeper

import (
	"errors"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// このファイルは Ruby の URI.parse（URI::RFC3986_Parser）のうち、Doorkeeper の
// リダイレクト URI の検証（Doorkeeper::RedirectUriValidator）と照合
// （Doorkeeper::OAuth::Helpers::URIChecker）、Authorization::URIBuilder に必要な部分の移植。

// ErrInvalidURI は URI::InvalidURIError。
var ErrInvalidURI = errors.New("bad URI(is not URI?)")

// URI は URI::Generic（URI::HTTP / URI::HTTPS を含む）の構成要素。
type URI struct {
	// Scheme は小文字化したスキーム（無ければ ""）。
	Scheme   string
	Userinfo *string
	// Host は authority のホスト（authority が無ければ nil。"http://" なら空文字）。
	Host *string
	// Port はポート（無ければ http / https の既定ポート、それ以外は 0）。
	Port int
	// explicitPort はポートが明示されていたか（to_s で既定ポートを省くため）。
	explicitPort bool
	Path         string
	// Opaque は path-rootless（"javascript:alert(1)" の "alert(1)"）。nil なら無し。
	Opaque   *string
	Query    *string
	Fragment *string
}

const (
	reHex      = `[0-9A-Fa-f]`
	rePct      = `%` + reHex + reHex
	reSeg      = `(?:` + rePct + `|[!$&-.0-9:;=@A-Z_a-z~/])`
	reSegNS    = `(?:` + rePct + `|[!$&-.0-9:;=@A-Z_a-z~])`
	reSegNC    = `(?:` + rePct + `|[!$&-.0-9;=@A-Z_a-z~])`
	reUserinfo = `(?:` + rePct + `|[!$&-.0-9:;=A-Z_a-z~])*`
	reHost     = `(?:\[(?:[0-9A-Fa-f:.]+|v` + reHex + `+\.[!$&-.0-9:;=A-Z_a-z~]+)\]|(?:` + rePct + `|[!$&-.0-9;=A-Z_a-z~])*)`
	reFragment = `(?:` + rePct + `|[!$&-.0-9:;=@A-Z_a-z~/?])*`
	reScheme   = `[A-Za-z][+\-.0-9A-Za-z]*`
)

// rfc3986URI は URI::RFC3986_Parser::RFC3986_URI。
var rfc3986URI = regexp.MustCompile(`\A(` + reScheme + `):` +
	`(?://(?:(` + reUserinfo + `)@)?(` + reHost + `)(?::([0-9]*))?((?:/` + reSeg + `*)?)` +
	`|(/(?:` + reSegNS + reSeg + `*)?)` +
	`|(` + reSegNS + reSeg + `*)` +
	`|())` +
	`(?:\?([^#]*))?(?:#(` + reFragment + `))?\z`)

// rfc3986Relative は URI::RFC3986_Parser::RFC3986_relative_ref。
var rfc3986Relative = regexp.MustCompile(`\A` +
	`(?://(?:(` + reUserinfo + `)@)?(` + reHost + `)?(?::([0-9]*))?((?:/` + reSeg + `*)?)` +
	`|(/` + reSeg + `*)` +
	`|(` + reSegNC + `+(?:/` + reSeg + `*)?)` +
	`|())` +
	`(?:\?([^#]*))?(?:#(` + reFragment + `))?\z`)

func strp(s string) *string { return &s }

// defaultPort は URI::HTTP / URI::HTTPS の DEFAULT_PORT。
func defaultPort(scheme string) int {
	switch scheme {
	case "http":
		return 80
	case "https":
		return 443
	}
	return 0
}

// ParseURI は URI.parse(s)。
func ParseURI(s string) (*URI, error) {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return nil, ErrInvalidURI
		}
	}
	idx := func(m []int, i int) (string, bool) {
		if m[2*i] < 0 {
			return "", false
		}
		return s[m[2*i]:m[2*i+1]], true
	}
	if m := rfc3986URI.FindStringSubmatchIndex(s); m != nil {
		u := &URI{}
		scheme, _ := idx(m, 1)
		u.Scheme = strings.ToLower(scheme)
		query, hasQuery := idx(m, 9)
		frag, hasFrag := idx(m, 10)
		if hasFrag {
			u.Fragment = strp(frag)
		}
		if op, ok := idx(m, 7); ok {
			if hasQuery {
				op += "?" + query
			}
			u.Opaque = strp(op)
		} else {
			if ui, ok := idx(m, 2); ok {
				u.Userinfo = strp(ui)
			}
			if h, ok := idx(m, 3); ok {
				u.Host = strp(h)
			}
			if p, ok := idx(m, 4); ok && p != "" {
				u.Port, _ = strconv.Atoi(p)
				u.explicitPort = true
			}
			for _, i := range []int{5, 6, 8} {
				if p, ok := idx(m, i); ok {
					u.Path = p
					break
				}
			}
			if hasQuery {
				u.Query = strp(query)
			}
		}
		if !u.explicitPort {
			u.Port = defaultPort(u.Scheme)
		}
		return u, nil
	}
	if m := rfc3986Relative.FindStringSubmatchIndex(s); m != nil {
		u := &URI{}
		if ui, ok := idx(m, 1); ok {
			u.Userinfo = strp(ui)
		}
		if h, ok := idx(m, 2); ok {
			u.Host = strp(h)
		}
		if p, ok := idx(m, 3); ok && p != "" {
			u.Port, _ = strconv.Atoi(p)
			u.explicitPort = true
		}
		for _, i := range []int{4, 5, 6, 7} {
			if p, ok := idx(m, i); ok {
				u.Path = p
				break
			}
		}
		if q, ok := idx(m, 8); ok {
			u.Query = strp(q)
		}
		if f, ok := idx(m, 9); ok {
			u.Fragment = strp(f)
		}
		return u, nil
	}
	return nil, ErrInvalidURI
}

// IsHTTP は uri.is_a?(URI::HTTP)（https を含む）。
func (u *URI) IsHTTP() bool { return u.Scheme == "http" || u.Scheme == "https" }

// HostString は uri.host（nil なら ""）。
func (u *URI) HostString() string {
	if u.Host == nil {
		return ""
	}
	return *u.Host
}

// String は URI#to_s。
func (u *URI) String() string {
	var b strings.Builder
	if u.Scheme != "" {
		b.WriteString(u.Scheme)
		b.WriteByte(':')
	}
	if u.Opaque != nil {
		b.WriteString(*u.Opaque)
	} else {
		if u.Host != nil {
			b.WriteString("//")
			if u.Userinfo != nil {
				b.WriteString(*u.Userinfo)
				b.WriteByte('@')
			}
			b.WriteString(*u.Host)
			if u.explicitPort && u.Port != defaultPort(u.Scheme) {
				b.WriteByte(':')
				b.WriteString(strconv.Itoa(u.Port))
			}
		}
		b.WriteString(u.Path)
		if u.Query != nil {
			b.WriteByte('?')
			b.WriteString(*u.Query)
		}
	}
	if u.Fragment != nil {
		b.WriteByte('#')
		b.WriteString(*u.Fragment)
	}
	return b.String()
}

// componentKey は URI#== の比較に使う normalize 後の component_ary。
func (u *URI) componentKey() string {
	opt := func(p *string) string {
		if p == nil {
			return "\x00nil"
		}
		return "\x01" + *p
	}
	path := u.Path
	if u.Opaque == nil && path == "" {
		path = "/"
	}
	host := u.Host
	if host != nil {
		host = strp(strings.ToLower(*host))
	}
	cls := "generic"
	if u.IsHTTP() {
		cls = u.Scheme
	}
	return strings.Join([]string{cls, u.Scheme, opt(u.Userinfo), opt(host), strconv.Itoa(u.Port), path, opt(u.Opaque), opt(u.Query), opt(u.Fragment)}, "\x02")
}

// ---------------------------------------------------------------- 検証（RedirectUriValidator）

// OOB は Doorkeeper::OAuth::NonStandard::IETF_WG_OAUTH2_OOB_METHODS。
const (
	OOB     = "urn:ietf:wg:oauth:2.0:oob"
	OOBAuto = "urn:ietf:wg:oauth:2.0:oob:auto"
)

// IsOOB は URIChecker.oob_uri?。
func IsOOB(s string) bool { return s == OOB || s == OOBAuto }

// forcesSSL は Redmine の force_ssl_in_redirect_uri（ループバック等以外は https 必須）。
func forcesSSL(u *URI) bool {
	switch u.HostString() {
	case "localhost", "127.0.0.1", "web", "localohst:8080":
		if u.Host != nil {
			return false
		}
	}
	return true
}

// forbiddenURI は Redmine の forbid_redirect_uri（data / vbscript / javascript スキーム）。
func forbiddenURI(u *URI) bool {
	switch u.Scheme {
	case "data", "vbscript", "javascript":
		return true
	}
	return false
}

// ValidateRedirectURI は Doorkeeper::RedirectUriValidator#validate_each。
// エラーのキー（blank / forbidden_uri / fragment_present / unspecified_scheme / relative_uri /
// secured_uri / invalid_uri）を追加順に返す（同じキーは 1 つにまとめる。ActiveModel の errors と同じ）。
func ValidateRedirectURI(value string) []string {
	var keys []string
	add := func(k string) {
		for _, x := range keys {
			if x == k {
				return
			}
		}
		keys = append(keys, k)
	}
	fields := strings.Fields(value)
	if len(fields) == 0 {
		// allow_blank_redirect_uri は authorization_code フローがあるため偽
		return []string{"blank"}
	}
	for _, v := range fields {
		if IsOOB(v) {
			continue
		}
		u, err := ParseURI(v)
		if err != nil {
			// rescue URI::InvalidURIError は each の外側にあるため、以降の URI は検証しない
			add("invalid_uri")
			return keys
		}
		if forbiddenURI(u) {
			add("forbidden_uri")
		}
		if u.Fragment != nil {
			add("fragment_present")
		}
		if (u.Opaque != nil && *u.Opaque != "") || u.Scheme == "localhost" {
			add("unspecified_scheme")
		}
		if u.Scheme == "" && u.HostString() == "" {
			add("relative_uri")
		}
		if u.Scheme == "http" && forcesSSL(u) {
			add("secured_uri")
		}
		if u.IsHTTP() && u.HostString() == "" {
			add("invalid_uri")
		}
	}
	return keys
}

// ---------------------------------------------------------------- 照合（URIChecker）

// uriValid は URIChecker.valid?。
func uriValid(s string) bool {
	if IsOOB(s) {
		return true
	}
	u, err := ParseURI(s)
	if err != nil {
		return false
	}
	if u.Scheme == "" || u.Scheme == "localhost" {
		return false
	}
	if u.IsHTTP() && u.HostString() == "" {
		return false
	}
	return u.Fragment == nil && u.Opaque == nil
}

// isLoopback は URIChecker.loopback_uri?（IPAddr.new(uri.host).loopback?）。
func isLoopback(u *URI) bool {
	h := strings.TrimSuffix(strings.TrimPrefix(u.HostString(), "["), "]")
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// queryMatches は URIChecker.query_matches?。
func queryMatches(query, clientQuery *string) bool {
	blank := func(p *string) bool { return p == nil || strings.TrimSpace(*p) == "" }
	if blank(clientQuery) && blank(query) {
		return true
	}
	if clientQuery == nil || query == nil {
		return false
	}
	a, b := strings.Split(*clientQuery, "&"), strings.Split(*query, "&")
	sort.Strings(a)
	sort.Strings(b)
	return strings.Join(a, "&") == strings.Join(b, "&")
}

// uriMatches は URIChecker.matches?。
func uriMatches(url, clientURL string) bool {
	u, err := ParseURI(url)
	if err != nil {
		return false
	}
	c, err := ParseURI(clientURL)
	if err != nil {
		return false
	}
	if c.Query != nil {
		if !queryMatches(u.Query, c.Query) {
			return false
		}
		c.Query = nil
	}
	if isLoopback(u) && isLoopback(c) {
		u.Port, c.Port = 0, 0
	}
	u.Query = nil
	return u.componentKey() == c.componentKey()
}

// ValidForAuthorization は URIChecker.valid_for_authorization?(url, client_url)。
func ValidForAuthorization(url, clientURLs string) bool {
	if !uriValid(url) {
		return false
	}
	for _, c := range strings.Fields(clientURLs) {
		if uriMatches(url, c) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- URIBuilder

// Param はクエリの 1 項目（順序を保つ）。
type Param struct{ Key, Value string }

// escapeComponent は URI.encode_www_form_component（Rack::Utils.escape）。
func escapeComponent(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '*', c == '-', c == '.', c == '_':
			b.WriteByte(c)
		case c == ' ':
			b.WriteByte('+')
		default:
			b.WriteByte('%')
			b.WriteByte("0123456789ABCDEF"[c>>4])
			b.WriteByte("0123456789ABCDEF"[c&15])
		}
	}
	return b.String()
}

// unescapeComponent は URI.decode_www_form_component（不正な % はそのまま）。
func unescapeComponent(s string) string {
	s = strings.ReplaceAll(s, "+", " ")
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				b.WriteByte(byte(v))
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// buildQuery は URIBuilder.build_query（空の値は除く）。
func buildQuery(params []Param) string {
	var parts []string
	for _, p := range params {
		if strings.TrimSpace(p.Value) == "" {
			continue
		}
		parts = append(parts, escapeComponent(p.Key)+"="+escapeComponent(p.Value))
	}
	return strings.Join(parts, "&")
}

// URIWithQuery は URIBuilder.uri_with_query（元のクエリに params をマージする）。
func URIWithQuery(url string, params []Param) string {
	u, err := ParseURI(url)
	if err != nil {
		return url
	}
	var merged []Param
	if u.Query != nil {
		for _, kv := range strings.Split(*u.Query, "&") {
			if kv == "" {
				continue
			}
			k, v, _ := strings.Cut(kv, "=")
			merged = append(merged, Param{unescapeComponent(k), unescapeComponent(v)})
		}
	}
	for _, p := range params {
		replaced := false
		for i := range merged {
			if merged[i].Key == p.Key {
				merged[i].Value = p.Value
				replaced = true
			}
		}
		if !replaced {
			merged = append(merged, p)
		}
	}
	q := buildQuery(merged)
	u.Query = &q
	return u.String()
}

// URIWithFragment は URIBuilder.uri_with_fragment。
func URIWithFragment(url string, params []Param) string {
	u, err := ParseURI(url)
	if err != nil {
		return url
	}
	f := buildQuery(params)
	u.Fragment = &f
	return u.String()
}
