// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package rails

import (
	"html"
	"regexp"
	"strings"
)

// sanitize は rails-html-sanitizer（Loofah / Nokogiri）の SafeListSanitizer の基本部分を
// 簡易トークナイザで近似したもの。HTML パーサによる木構造の修復（入れ子の矯正など）は
// 行わないため、整形式でない入力では Rails と結果が異なる場合がある。

var sanitizeAllowedTags = map[string]bool{}
var sanitizeAllowedAttrs = map[string]bool{}

// uriAttrs はプロトコル検査の対象となる属性（Loofah::HTML5::SafeList::ATTR_VAL_IS_URI の主要なもの）。
var uriAttrs = map[string]bool{
	"href": true, "src": true, "cite": true, "action": true, "longdesc": true,
	"xlink:href": true, "lowsrc": true, "dynsrc": true, "background": true, "poster": true,
}

// allowedProtocols は Loofah::HTML5::SafeList::ALLOWED_PROTOCOLS。
var allowedProtocols = map[string]bool{}

// rawTextElements は中身を生テキストとして扱う要素。
var rawTextElements = map[string]bool{"script": true, "style": true, "textarea": true, "title": true, "xmp": true, "iframe": true, "noembed": true, "noframes": true, "plaintext": true}

func init() {
	for _, t := range strings.Fields(`a abbr acronym address b big blockquote br cite code dd del dfn div dl dt em
		h1 h2 h3 h4 h5 h6 hr i img ins kbd li mark ol p pre samp small span strong sub sup time tt ul var`) {
		sanitizeAllowedTags[t] = true
	}
	for _, a := range strings.Fields(`abbr alt cite class datetime height href lang name src title width xml:lang`) {
		sanitizeAllowedAttrs[a] = true
	}
	for _, p := range strings.Fields(`afs aim callto data ed2k fax feed ftp gopher http https irc mailto news
		nntp rsync rtsp sftp sms ssh tag tel telnet urn webcal xmpp`) {
		allowedProtocols[p] = true
	}
}

var (
	tagNameRe  = regexp.MustCompile(`^[A-Za-z][^\s/>]*`)
	attrRe     = regexp.MustCompile(`^[\s/]*([^\s"'>/=]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]*)))?`)
	protocolRe = regexp.MustCompile(`^([a-zA-Z0-9][-+.a-zA-Z0-9]*):`)
	voidTags   = map[string]bool{"br": true, "hr": true, "img": true, "area": true, "base": true, "col": true, "embed": true, "input": true, "link": true, "meta": true, "source": true, "track": true, "wbr": true}
)

// Sanitize は sanitize(html)（既定の許可タグ・属性）。
func Sanitize(v any) HTML {
	s := ToS(v)
	if s == "" {
		return ""
	}
	var b strings.Builder
	var stack []string
	i := 0
	for i < len(s) {
		lt := strings.IndexByte(s[i:], '<')
		if lt < 0 {
			b.WriteString(escapeText(html.UnescapeString(s[i:])))
			break
		}
		b.WriteString(escapeText(html.UnescapeString(s[i : i+lt])))
		i += lt
		rest := s[i:]
		switch {
		case strings.HasPrefix(rest, "<!--"):
			end := strings.Index(rest[4:], "-->")
			if end < 0 {
				i = len(s)
			} else {
				i += 4 + end + 3
			}
		case strings.HasPrefix(rest, "</") && len(rest) > 2 && isASCIILetter(rest[2]):
			gt := strings.IndexByte(rest, '>')
			if gt < 0 {
				i = len(s)
				continue
			}
			name := strings.ToLower(tagNameRe.FindString(rest[2:gt]))
			i += gt + 1
			if sanitizeAllowedTags[name] {
				// スタック中の対応する開始タグまでを閉じる
				for j := len(stack) - 1; j >= 0; j-- {
					if stack[j] == name {
						for k := len(stack) - 1; k >= j; k-- {
							b.WriteString("</" + stack[k] + ">")
						}
						stack = stack[:j]
						break
					}
				}
			}
		case len(rest) > 1 && isASCIILetter(rest[1]):
			gt := tagEnd(rest)
			if gt < 0 {
				i = len(s)
				continue
			}
			inner := rest[1:gt]
			name := tagNameRe.FindString(inner)
			lname := strings.ToLower(name)
			attrs := inner[len(name):]
			i += gt + 1
			if rawTextElements[lname] {
				// 生テキスト要素は終了タグまでを本文として扱い、要素自体は除去してテキストを残す
				closeIdx := strings.Index(strings.ToLower(s[i:]), "</"+lname)
				var body string
				if closeIdx < 0 {
					body = s[i:]
					i = len(s)
				} else {
					body = s[i : i+closeIdx]
					i += closeIdx
					if gt2 := strings.IndexByte(s[i:], '>'); gt2 >= 0 {
						i += gt2 + 1
					} else {
						i = len(s)
					}
				}
				b.WriteString(escapeText(body))
				continue
			}
			if !sanitizeAllowedTags[lname] {
				continue
			}
			b.WriteString("<" + lname + sanitizeAttrs(attrs) + ">")
			if !voidTags[lname] {
				stack = append(stack, lname)
			}
		default:
			b.WriteString("&lt;")
			i++
		}
	}
	for k := len(stack) - 1; k >= 0; k-- {
		b.WriteString("</" + stack[k] + ">")
	}
	return HTML(b.String())
}

// tagEnd は引用符を考慮してタグの終わり（>）の位置を返す。
func tagEnd(s string) int {
	var q byte
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == q {
				q = 0
			}
		case c == '"' || c == '\'':
			// 属性値の開始（= の直後）でのみ引用符として扱う
			j := i - 1
			for j > 0 && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n') {
				j--
			}
			if s[j] == '=' {
				q = c
			}
		case c == '>':
			return i
		}
	}
	return -1
}

func isASCIILetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func sanitizeAttrs(s string) string {
	var b strings.Builder
	seen := map[string]bool{}
	for {
		m := attrRe.FindStringSubmatchIndex(s)
		if m == nil || m[1] == 0 {
			break
		}
		name := strings.ToLower(s[m[2]:m[3]])
		val := ""
		for g := 2; g <= 4; g++ {
			if m[2*g] >= 0 {
				val = s[m[2*g]:m[2*g+1]]
				break
			}
		}
		s = s[m[1]:]
		if seen[name] {
			continue
		}
		seen[name] = true
		if !sanitizeAllowedAttrs[name] {
			continue
		}
		val = html.UnescapeString(val)
		if uriAttrs[name] {
			check := strings.Map(func(r rune) rune {
				if r < 0x20 || r == 0x7f || r == ' ' {
					return -1
				}
				return r
			}, val)
			if pm := protocolRe.FindStringSubmatch(check); pm != nil && !allowedProtocols[strings.ToLower(pm[1])] {
				continue
			}
			// Loofah は data: をメディアタイプ（ALLOWED_URI_DATA_MEDIATYPES）で絞るが、その分割の都合で
			// 内容を持つ data: URI は実質すべて除かれる。同じく除く（data:text/html 等を残さない）
			if pm := protocolRe.FindStringSubmatch(check); pm != nil && strings.EqualFold(pm[1], "data") && len(check) > len("data:") {
				continue
			}
		}
		b.WriteString(" " + name + `="` + escapeAttrValue(val) + `"`)
	}
	return b.String()
}

// escapeText は Nokogiri（HTML4）のテキストノード直列化（& < >。U+00A0 はそのまま）。
func escapeText(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func escapeAttrValue(s string) string {
	r := strings.NewReplacer("&", "&amp;", `"`, "&quot;")
	return r.Replace(s)
}

// StripTags は strip_tags(html)（全タグを除去してテキストのみ残す）。
func StripTags(v any) HTML {
	s := ToS(v)
	if s == "" {
		return ""
	}
	var b strings.Builder
	i := 0
	for i < len(s) {
		lt := strings.IndexByte(s[i:], '<')
		if lt < 0 {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(s[i : i+lt])
		i += lt
		rest := s[i:]
		switch {
		case strings.HasPrefix(rest, "<!--"):
			end := strings.Index(rest[4:], "-->")
			if end < 0 {
				i = len(s)
			} else {
				i += 4 + end + 3
			}
		case len(rest) > 1 && (isASCIILetter(rest[1]) || rest[1] == '/' || rest[1] == '!'):
			gt := tagEnd(rest)
			if gt < 0 {
				i = len(s)
			} else {
				i += gt + 1
			}
		default:
			b.WriteByte('<')
			i++
		}
	}
	return HTML(escapeText(html.UnescapeString(b.String())))
}
