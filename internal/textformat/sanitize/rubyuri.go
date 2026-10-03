// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package sanitize

import (
	"regexp"
	"strings"

	"github.com/dlclark/regexp2/v2"
)

// Ruby 3.3 の URI::RFC3986_Parser の正規表現（RFC3986_URI）を移植したもの。
// ExternalLinksFilter の URI.parse(url).scheme（失敗時は nil）を再現するために使う。
// 所有的量指定子はアトミックグループで、\g<...> の呼び出しは展開して表す。

const (
	hexd     = `[0-9A-Fa-f]`
	decOctet = `(?:[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-5]|[0-9])`
	ipv4     = decOctet + `\.` + decOctet + `\.` + decOctet + `\.` + decOctet
	ls32     = `(?:` + hexd + `{1,4}:` + hexd + `{1,4}|` + ipv4 + `)`
	h16c     = hexd + `{1,4}:`
	ipv6     = `(?:(?:` + h16c + `){6}` + ls32 +
		`|::(?:` + h16c + `){5}` + ls32 +
		`|` + hexd + `{1,4}?::(?:` + h16c + `){4}` + ls32 +
		`|(?:(?:` + h16c + `)?` + hexd + `{1,4})?::(?:` + h16c + `){3}` + ls32 +
		`|(?:(?:` + h16c + `){0,2}` + hexd + `{1,4})?::(?:` + h16c + `){2}` + ls32 +
		`|(?:(?:` + h16c + `){0,3}` + hexd + `{1,4})?::` + h16c + ls32 +
		`|(?:(?:` + h16c + `){0,4}` + hexd + `{1,4})?::` + ls32 +
		`|(?:(?:` + h16c + `){0,5}` + hexd + `{1,4})?::` + hexd + `{1,4}` +
		`|(?:(?:` + h16c + `){0,6}` + hexd + `{1,4})?::)`
	ipvFuture = `v(?>` + hexd + `+)\.(?>[!$&-.0-9:;=A-Z_a-z~]+)`
	host      = `(?:\[(?:` + ipv6 + `|` + ipvFuture + `)\]|` + ipv4 + `|(?>(?:%` + hexd + hexd + `|[!$&-.0-9;=A-Z_a-z~])*))`
	userinfo  = `(?>(?:%` + hexd + hexd + `|[!$&-.0-9:;=A-Z_a-z~])*)`
	seg       = `(?:%` + hexd + hexd + `|[!$&-.0-9:;=@A-Z_a-z~/])`
	fragment  = `(?>(?:%` + hexd + hexd + `|[!$&-.0-9:;=@A-Z_a-z~/?])*)`
	scheme    = `[A-Za-z](?>[+\-.0-9A-Za-z]*)`

	rfc3986URIPattern = `\A(?<scheme>` + scheme + `):` +
		`(?:(?<hier>//(?:` + userinfo + `@)?` + host + `(?::(?>[0-9]*))?)(?<abempty>(?:/(?>` + seg + `*))?)` +
		`|(?<absolute>/(?:(?!/)(?>` + seg + `+))?)` +
		`|(?<rootless>(?!/)(?>` + seg + `+))` +
		`|(?<empty>))` +
		`(?:\?(?<query>(?>[^#]*)))?` +
		`(?:#(?<fragment>` + fragment + `))?\z`
)

var (
	reRubyURI       = regexp2.MustCompile(rfc3986URIPattern, regexp2.None)
	reBadPercent    = regexp.MustCompile(`%[^0-9A-Fa-f][^0-9A-Fa-f]`)
	reMailtoAddress = regexp.MustCompile(`\A(?:[^@,;]+@[^@,;]+(?:\z|[,;]))*\z`)
)

// uriScheme は Ruby の URI.parse(url).scheme（例外時は ""）を返す。
func uriScheme(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return ""
		}
	}
	m, err := reRubyURI.FindStringMatch(s)
	if err != nil || m == nil {
		return ""
	}
	group := func(name string) (string, bool) {
		g := m.GroupByName(name)
		if g == nil || len(g.Captures) == 0 {
			return "", false
		}
		return g.String(), true
	}
	sch := strings.ToLower(m.GroupByName("scheme").String())
	query, hasQuery := group("query")
	_, hasFragment := group("fragment")
	opaque, hasOpaque := group("rootless")
	_, hasHier := group("hier")
	_, hasAbs := group("absolute")
	_, hasEmpty := group("empty")
	hasPath := hasHier || hasAbs || hasEmpty

	// Generic#query= は不正なパーセントエスケープで例外になる
	if hasQuery && !hasOpaque && reBadPercent.MatchString(strings.NewReplacer("\t", "", "\r", "", "\n", "").Replace(query)) {
		return ""
	}
	switch sch {
	case "mailto":
		if hasOpaque && hasQuery {
			opaque += "?" + query
		}
		if !hasOpaque {
			if !hasQuery {
				return ""
			}
			opaque = "?" + query
		}
		to := opaque
		if i := strings.IndexByte(opaque, '?'); i >= 0 {
			to = opaque[:i]
		}
		if !reMailtoAddress.MatchString(to) {
			return ""
		}
	case "ftp":
		if !hasPath {
			return ""
		}
	case "ldap", "ldaps":
		if hasFragment || !hasPath {
			return ""
		}
	}
	return sch
}
