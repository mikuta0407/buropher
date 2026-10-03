// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package main

import (
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
)

// finding は応答中で「実行可能な文脈」に現れたペイロードの 1 件。
type finding struct {
	Kind string // attr-event / url-scheme / script-code / js-code / js-string-html / csv-formula / u2028-in-js / atom-html ...
	ID   int    // ペイロード番号（xss(N) の N）
	Ctx  string // 前後の抜粋
}

func (f finding) key() string { return f.Kind + "#" + strconv.Itoa(f.ID) }

var reMarker = regexp.MustCompile(`xss\((\d+)\)`)

func markerIDs(s string) []int {
	var ids []int
	for _, m := range reMarker.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.Atoi(m[1])
		ids = append(ids, n)
	}
	return ids
}

func snippet(s string, i int) string {
	a, b := i-60, i+80
	if a < 0 {
		a = 0
	}
	if b > len(s) {
		b = len(s)
	}
	for a > 0 && !utf8.RuneStart(s[a]) {
		a--
	}
	for b < len(s) && !utf8.RuneStart(s[b]) {
		b++
	}
	return strings.ReplaceAll(s[a:b], "\n", "⏎")
}

// urlAttrs は URL として解釈される属性。
var urlAttrs = map[string]bool{
	"href": true, "src": true, "action": true, "formaction": true, "data": true,
	"xlink:href": true, "poster": true, "background": true, "srcdoc": true, "codebase": true,
}

func dangerousURL(v string) bool {
	var b strings.Builder
	for _, r := range strings.ToLower(v) {
		if r <= ' ' || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	s := b.String()
	return strings.HasPrefix(s, "javascript:") || strings.HasPrefix(s, "vbscript:") ||
		strings.HasPrefix(s, "data:text/html") || strings.HasPrefix(s, "data:image/svg") ||
		strings.HasPrefix(s, "&#") // 実体参照で難読化された scheme
}

// checkHTML は HTML 文字列を字句解析し、実行可能な文脈のペイロードを返す。
// prefix は Kind の接頭辞（JS 文字列から復号した HTML なら "js-string-"）。
func checkHTML(s, prefix string) []finding {
	if !strings.Contains(s, "xss(") {
		return nil
	}
	var out []finding
	z := xhtml.NewTokenizer(strings.NewReader(s))
	inScript := false
	for {
		tt := z.Next()
		if tt == xhtml.ErrorToken {
			break
		}
		switch tt {
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			tok := z.Token()
			if tok.Data == "script" && tt == xhtml.StartTagToken {
				inScript = true
			}
			for _, a := range tok.Attr {
				ids := markerIDs(a.Val)
				if len(ids) == 0 {
					continue
				}
				key := strings.ToLower(a.Key)
				ctx := "<" + tok.Data + " " + a.Key + "=" + a.Val
				switch {
				case strings.HasPrefix(key, "on"):
					for _, id := range ids {
						out = append(out, finding{prefix + "attr-event", id, ctx})
					}
				case urlAttrs[key] && dangerousURL(a.Val):
					for _, id := range ids {
						out = append(out, finding{prefix + "url-scheme", id, ctx})
					}
				case key == "style":
					for _, id := range ids {
						out = append(out, finding{prefix + "style", id, ctx})
					}
				}
			}
		case xhtml.EndTagToken:
			if z.Token().Data == "script" {
				inScript = false
			}
		case xhtml.TextToken:
			if inScript {
				txt := string(z.Text())
				for _, f := range checkJS(txt) {
					f.Kind = prefix + "script-" + strings.TrimPrefix(f.Kind, "js-")
					out = append(out, f)
				}
			}
		}
	}
	return out
}

// checkJS は JS ソースを簡易字句解析し、文字列・コメント外の xss(N) と、
// 文字列リテラルを復号して HTML として解釈した場合の危険箇所を返す。
func checkJS(js string) []finding {
	if !strings.Contains(js, "xss(") {
		return nil
	}
	var out []finding
	var code strings.Builder // 文字列・コメントを除いたコード
	var codePos []int
	lastSig := byte('(')
	i := 0
	for i < len(js) {
		ch := js[i]
		switch {
		case ch == '\'' || ch == '"' || ch == '`':
			q := ch
			j := i + 1
			var lit strings.Builder
			for j < len(js) && js[j] != q {
				if js[j] == '\\' && j+1 < len(js) {
					j++
					switch js[j] {
					case 'n':
						lit.WriteByte('\n')
					case 'u':
						if j+4 < len(js) {
							if n, err := strconv.ParseUint(js[j+1:j+5], 16, 32); err == nil {
								lit.WriteRune(rune(n))
								j += 4
								break
							}
						}
						lit.WriteByte('u')
					default:
						lit.WriteByte(js[j])
					}
					j++
					continue
				}
				if q != '`' && js[j] == '\n' {
					break // 不正な改行: 文字列終了とみなす
				}
				if strings.HasPrefix(js[j:], " ") || strings.HasPrefix(js[j:], " ") {
					for _, id := range markerIDs(js[i:min(j+200, len(js))]) {
						out = append(out, finding{"u2028-in-js", id, snippet(js, j)})
						break
					}
				}
				lit.WriteByte(js[j])
				j++
			}
			str := lit.String()
			out = append(out, checkHTML(str, "js-string-")...)
			i = j + 1
			lastSig = q
		case ch == '/' && i+1 < len(js) && js[i+1] == '/':
			for i < len(js) && js[i] != '\n' {
				i++
			}
		case ch == '/' && i+1 < len(js) && js[i+1] == '*':
			e := strings.Index(js[i+2:], "*/")
			if e < 0 {
				i = len(js)
			} else {
				i += e + 4
			}
		case ch == '/' && strings.IndexByte("(,=:[!&|?{};+-*%<>~^\n", lastSig) >= 0:
			// 正規表現リテラル
			j := i + 1
			inClass := false
			for j < len(js) && js[j] != '\n' {
				if js[j] == '\\' {
					j += 2
					continue
				}
				if js[j] == '[' {
					inClass = true
				} else if js[j] == ']' {
					inClass = false
				} else if js[j] == '/' && !inClass {
					break
				}
				j++
			}
			i = j + 1
			lastSig = 'a'
		default:
			code.WriteByte(ch)
			codePos = append(codePos, i)
			if ch != ' ' && ch != '\t' && ch != '\r' {
				lastSig = ch
			}
			i++
		}
	}
	cs := code.String()
	for _, m := range reMarker.FindAllStringSubmatchIndex(cs, -1) {
		n, _ := strconv.Atoi(cs[m[2]:m[3]])
		out = append(out, finding{"js-code", n, snippet(js, codePos[m[0]])})
	}
	return out
}

// checkCSV はセル先頭が数式文字で始まるペイロードを返す（CSV 数式インジェクション）。
func checkCSV(b []byte) []finding {
	if !bytes.Contains(b, []byte("xss(")) {
		return nil
	}
	r := csv.NewReader(bytes.NewReader(b))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	var out []finding
	for {
		rec, err := r.Read()
		if err != nil {
			break
		}
		for _, cell := range rec {
			if cell == "" {
				continue
			}
			if strings.ContainsRune("=+-@\t\r", rune(cell[0])) {
				for _, id := range markerIDs(cell) {
					out = append(out, finding{"csv-formula", id, cell})
				}
			}
		}
	}
	return out
}

// checkAtom は Atom の type="html" 要素の中身を HTML として検査する。
func checkAtom(b []byte) []finding {
	if !bytes.Contains(b, []byte("xss(")) {
		return nil
	}
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = false
	var out []finding
	var stack []bool
	var text strings.Builder
	for {
		t, err := d.Token()
		if err != nil {
			break
		}
		switch e := t.(type) {
		case xml.StartElement:
			isHTML := false
			for _, a := range e.Attr {
				if a.Name.Local == "type" && (a.Value == "html" || a.Value == "xhtml") {
					isHTML = true
				}
				if a.Name.Local == "href" && dangerousURL(a.Value) {
					for _, id := range markerIDs(a.Value) {
						out = append(out, finding{"atom-url-scheme", id, a.Value})
					}
				}
			}
			stack = append(stack, isHTML)
			text.Reset()
		case xml.CharData:
			text.Write(e)
		case xml.EndElement:
			if n := len(stack); n > 0 {
				if stack[n-1] {
					for _, f := range checkHTML(text.String(), "atom-") {
						out = append(out, f)
					}
				}
				stack = stack[:n-1]
			}
			text.Reset()
		}
	}
	return out
}

// analyze は Content-Type に応じて検査器を選ぶ。
func analyze(ct string, body []byte) []finding {
	ct = strings.ToLower(ct)
	var fs []finding
	switch {
	case strings.Contains(ct, "javascript"):
		fs = checkJS(string(body))
	case strings.Contains(ct, "atom"):
		fs = checkAtom(body)
	case strings.Contains(ct, "csv"):
		fs = checkCSV(body)
	case strings.Contains(ct, "html"):
		fs = checkHTML(string(body), "")
	case strings.Contains(ct, "svg"), strings.Contains(ct, "xml") && !strings.Contains(ct, "application/xml"):
		fs = checkHTML(string(body), "svgxml-")
	}
	return dedup(fs)
}

func dedup(fs []finding) []finding {
	seen := map[string]bool{}
	var out []finding
	for _, f := range fs {
		if seen[f.key()] {
			continue
		}
		seen[f.key()] = true
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key() < out[j].key() })
	return out
}
