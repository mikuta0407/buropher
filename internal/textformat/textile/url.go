// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package textile

// Redmine::Helpers::URL (lib/redmine/helpers/url.rb) の移植。

import (
	"regexp"
	"strings"
)

// Ruby 3.3 の URI::RFC3986_Parser の正規表現 (uri/rfc3986_parser.rb) を簡略化して移植したもの。
// 所有量指定子は直後の文字と排他的なため、通常の量指定子 (RE2) でも受理言語は変わらない。
const (
	uriHex   = `%[0-9A-Fa-f]{2}`
	uriSeg   = `(?:` + uriHex + `|[!$&-.0-9:;=@A-Z_a-z~/])`
	uriSegNC = `(?:` + uriHex + `|[!$&-.0-9;=@A-Z_a-z~])`
	// (?!/) の代替: 先頭が "/" でない seg
	uriSegNS    = `(?:` + uriHex + `|[!$&-.0-9:;=@A-Z_a-z~])`
	uriFragment = `(?:` + uriHex + `|[!$&-.0-9:;=@A-Z_a-z~/?])*`
	uriUserinfo = `(?:` + uriHex + `|[!$&-.0-9:;=A-Z_a-z~])*`
	// IP-literal は厳密な IPv6 文法を省略し、使用可能文字のみ検査する
	uriHost = `(?:\[[0-9A-Fa-fvV:.!$&-.;=_~]+\]|(?:` + uriHex + `|[!$&-.0-9;=A-Z_a-z~])*)`
)

var (
	reRFC3986URI = regexp.MustCompile(`\A([A-Za-z][+\-.0-9A-Za-z]*):` +
		`(?:(//(?:` + uriUserinfo + `@)?` + uriHost + `(?::[0-9]*)?(?:/` + uriSeg + `*)?)` +
		`|(/(?:` + uriSegNS + uriSeg + `*)?)` +
		`|(` + uriSegNS + uriSeg + `*)` +
		`|)` +
		`(\?[^#]*)?(?:#` + uriFragment + `)?\z`)
	reMailtoTo        = regexp.MustCompile(`\A(?:[^@,;]+@[^@,;]+(?:\z|[,;]))*\z`)
	reRFC3986Relative = regexp.MustCompile(`\A(?://(?:` + uriUserinfo + `@)?(?:` + uriHost + `)?(?::[0-9]*)?(?:/` + uriSeg + `*)?` +
		`|/` + uriSeg + `*` +
		`|` + uriSegNC + `+(?:/` + uriSeg + `*)?` +
		`|)` +
		`(?:\?[^#]*)?(?:#` + uriFragment + `)?\z`)
)

// uriScheme は URI.parse(uri).scheme 相当。パースに失敗した場合は ok=false。
func uriScheme(uri string) (scheme string, hasScheme bool, ok bool) {
	for i := 0; i < len(uri); i++ {
		if uri[i] >= 0x80 {
			// URI must be ascii only
			return "", false, false
		}
	}
	if m := reRFC3986URI.FindStringSubmatchIndex(uri); m != nil {
		scheme := strings.ToLower(uri[m[2]:m[3]])
		group := func(i int) (string, bool) {
			if m[2*i] < 0 {
				return "", false
			}
			return uri[m[2*i]:m[2*i+1]], true
		}
		opaque, hasOpaque := group(4)
		query, hasQuery := group(5)
		// URI.for でスキーム別クラスを生成する際の検査 (uri/ftp.rb, uri/mailto.rb)
		switch scheme {
		case "ftp":
			// path-rootless (opaque) の場合 path が nil になり InvalidURIError
			if hasOpaque {
				return "", false, false
			}
			path := ""
			if p, ok := group(2); ok {
				// authority 以降のパス部分
				rest := p[2:]
				if i := strings.IndexByte(rest, '/'); i >= 0 {
					path = rest[i:]
				}
			} else if p, ok := group(3); ok {
				path = p
			}
			if i := strings.Index(path, ";type="); i >= 0 {
				switch path[i+len(";type="):] {
				case "a", "i", "d":
				default:
					return "", false, false
				}
			}
		case "mailto":
			if !hasOpaque {
				if !hasQuery {
					return "", false, false
				}
				opaque = query
			} else if hasQuery {
				opaque += query
			}
			to, _, _ := strings.Cut(opaque, "?")
			if !reMailtoTo.MatchString(to) {
				return "", false, false
			}
		}
		return scheme, true, true
	}
	if reRFC3986Relative.MatchString(uri) {
		return "", false, true
	}
	return "", false, false
}

// uriWithSafeScheme は uri_with_safe_scheme? (url.rb:26-35) の移植。
// ユーザ操作なしで取得されるリソース (画像など) に安全なスキームかを返す。
func uriWithSafeScheme(uri string) bool {
	// プロトコル区切りを含まない相対 URL は無害
	if !strings.Contains(uri, ":") {
		return true
	}
	scheme, has, ok := uriScheme(uri)
	if !ok {
		return false
	}
	if !has {
		return true
	}
	switch scheme {
	case "http", "https", "ftp", "mailto":
		return true
	}
	return false
}

var (
	reLinkSafeScheme = rxi(`\A` + reS + `*([^/#]*?)(?::|&#0*58|&#x0*3a)`)
	reRFCScheme      = regexp.MustCompile(`\A[a-z][a-z0-9+.\-]*\z`)
)

// uriWithLinkSafeScheme は uri_with_link_safe_scheme? (url.rb:38-47) の移植。
func uriWithLinkSafeScheme(uri string) bool {
	m := match(reLinkSafeScheme, uri)
	if m == nil {
		return true
	}
	// 絶対スキーム
	scheme := strings.ToLower(m.s(1))
	if !reRFCScheme.MatchString(scheme) {
		return false
	}
	switch scheme {
	case "data", "javascript", "vbscript":
		return false
	}
	return true
}
