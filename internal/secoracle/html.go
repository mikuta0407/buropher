// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package secoracle はファジング・テスト用のセキュリティ判定（オラクル）をまとめたもの。
//
// 本番コードからは使わない。HTML の判定はブラウザと同じ HTML5 パーサ（golang.org/x/net/html）で
// 出力を解析し、許可外の要素・イベントハンドラ属性・危険なスキームの URL・危険な CSS が
// 無いことを確かめる。直列化 → 再解析 → 直列化 → 再解析でも同じ判定になること
// （mutation XSS が起きないこと）も確かめる。
package secoracle

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// HTMLPolicy は出力 HTML に許す内容。
type HTMLPolicy struct {
	// Elements は許可する要素名（HTML 名前空間）。nil なら DefaultElements。
	Elements map[string]bool
	// SVG が true なら svg > use（アイコンスプライト）を許可する。
	SVG bool
	// StrictURL が true なら URL 属性のスキームを http / https / mailto / ftp / 相対 に限る。
	// false なら javascript / vbscript / data / livescript 等の危険なスキームのみ拒否する
	// （Redmine の uri_with_link_safe_scheme? と同じく、それ以外の独自スキームは許す）。
	StrictURL bool
	// AllowDataImage が true なら img[src] の data:image/(png|gif|jpeg|webp) を許す。
	AllowDataImage bool
	// AllowOnclick は許可する onclick の値（正規表現）。nil ならイベントハンドラ属性は全て拒否する。
	AllowOnclick *regexp.Regexp
	// NoStyle が true なら style 属性自体を拒否する。
	NoStyle bool
	// BareElement は属性を持たない場合に限り許可する要素名（正規表現。Textile の <redpre#N> 用）。
	BareElement *regexp.Regexp
}

// TextileBareElement は Textile が属性なしで素通しする内部プレースホルダ（Redmine と同じ）。
var TextileBareElement = regexp.MustCompile(`\Aredpre#[0-9]+\z`)

// DefaultElements は Redmine の CommonMark サニタイザの許可要素に、Textile・Redmine リンク・マクロが
// 生成する要素（span, acronym, 表の要素など）を加えたもの。
var DefaultElements = toSet(
	"h1", "h2", "h3", "h4", "h5", "h6", "br", "b", "i", "strong", "em", "a", "pre", "code", "img", "tt",
	"div", "ins", "del", "sup", "sub", "p", "ol", "ul", "table", "thead", "tbody", "tfoot", "blockquote",
	"dl", "dt", "dd", "kbd", "q", "samp", "var", "hr", "ruby", "rt", "rp", "li", "tr", "td", "th", "s", "strike",
	"summary", "details", "caption", "figure", "figcaption", "abbr", "bdo", "cite", "dfn", "mark", "small",
	"span", "time", "wbr", "u", "acronym", "input", "colgroup", "col", "label",
)

func toSet(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

// urlAttrs は値が URL として解釈される属性（HTML・SVG）。
var urlAttrs = toSet("href", "src", "action", "formaction", "cite", "longdesc", "background", "poster",
	"data", "codebase", "dynsrc", "lowsrc", "xlink:href", "usemap", "manifest", "icon", "profile", "archive",
	"classid", "itemtype", "ping")

// forbiddenAttrs は値に関わらず危険な属性。
var forbiddenAttrs = toSet("srcdoc", "srcset", "http-equiv", "xmlns", "xmlns:xlink", "is", "autofocus",
	"formtarget", "attributename", "values", "from", "to", "by")

// dangerousSchemes はブラウザでスクリプトの実行・内容の注入につながるスキーム。
var dangerousSchemes = toSet("javascript", "vbscript", "data", "livescript", "mocha", "jar", "blob", "filesystem")

var strictSchemes = toSet("http", "https", "mailto", "ftp")

var (
	reSchemeStart = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9+.\-]*):`)
	reDataImage   = regexp.MustCompile(`(?i)^data:image/(png|gif|jpeg|webp)[;,]`)
)

// URLScheme はブラウザ（WHATWG URL）と同じ前処理（前後の C0 制御文字・空白の除去、タブ・改行の除去）の後の
// スキーム（小文字）を返す。相対 URL なら ""。value は文字参照を展開済みであること。
func URLScheme(value string) string {
	v := strings.TrimFunc(value, func(r rune) bool { return r <= 0x20 })
	v = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, v)
	m := reSchemeStart.FindStringSubmatch(v)
	if m == nil {
		return ""
	}
	return strings.ToLower(m[1])
}

func (p *HTMLPolicy) urlOK(el, attr, value string) error {
	scheme := URLScheme(value)
	if scheme == "" {
		return nil
	}
	if el == "img" && attr == "src" && p.AllowDataImage && reDataImage.MatchString(strings.TrimSpace(value)) {
		return nil
	}
	if dangerousSchemes[scheme] {
		return fmt.Errorf("<%s %s> has dangerous scheme %q: %q", el, attr, scheme, value)
	}
	if p.StrictURL && !strictSchemes[scheme] {
		return fmt.Errorf("<%s %s> has non-allowlisted scheme %q: %q", el, attr, scheme, value)
	}
	return nil
}

var (
	reCSSComment = regexp.MustCompile(`(?s)/\*.*?(\*/|$)`)
	reCSSEscape  = regexp.MustCompile(`\\([0-9a-fA-F]{1,6})\s?|\\(.)`)
	cssBad       = []string{"expression(", "url(", "@import", "behavior:", "-moz-binding",
		"image(", "image-set(", "src("}
)

// CSSOK は style 属性の値が危険な構文（expression・url()・javascript: など）を含まないことを確かめる。
// CSS のエスケープ（\XX）とコメントを展開・除去し、空白を詰めて小文字にしてから判定する。
func CSSOK(style string) error {
	s := reCSSEscape.ReplaceAllStringFunc(style, func(m string) string {
		sm := reCSSEscape.FindStringSubmatch(m)
		if sm[1] != "" {
			var r rune
			_, _ = fmt.Sscanf(sm[1], "%x", &r)
			return string(r)
		}
		return sm[2]
	})
	s = reCSSComment.ReplaceAllString(s, "")
	s = strings.ToLower(strings.Map(func(r rune) rune {
		if r <= 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s))
	for _, bad := range cssBad {
		if strings.Contains(s, bad) {
			return fmt.Errorf("style contains %q: %q", bad, style)
		}
	}
	return nil
}

func (p *HTMLPolicy) elements() map[string]bool {
	if p.Elements != nil {
		return p.Elements
	}
	return DefaultElements
}

// checkTree は解析済みの木を判定する。
func (p *HTMLPolicy) checkTree(nodes []*html.Node) error {
	var errs []error
	var walk func(n *html.Node, inSVG bool)
	walk = func(n *html.Node, inSVG bool) {
		if n.Type == html.ElementNode {
			name := n.Data
			switch {
			case n.Namespace == "svg":
				if !p.SVG || (name != "svg" && name != "use") || (name == "use" && !inSVG) {
					errs = append(errs, fmt.Errorf("forbidden svg element <%s>", name))
				}
				inSVG = true
			case n.Namespace != "":
				errs = append(errs, fmt.Errorf("forbidden %s element <%s>", n.Namespace, name))
			case p.BareElement != nil && len(n.Attr) == 0 && p.BareElement.MatchString(name):
			case !p.elements()[name]:
				errs = append(errs, fmt.Errorf("forbidden element <%s>", name))
			}
			if name == "input" && n.Namespace == "" {
				if attrVal(n, "type") != "checkbox" {
					errs = append(errs, fmt.Errorf("input of type %q", attrVal(n, "type")))
				}
			}
			for _, a := range n.Attr {
				key := strings.ToLower(a.Key)
				if a.Namespace != "" {
					key = a.Namespace + ":" + key
				}
				switch {
				case strings.HasPrefix(key, "on"):
					if p.AllowOnclick == nil || key != "onclick" || !p.AllowOnclick.MatchString(a.Val) {
						errs = append(errs, fmt.Errorf("<%s> has event handler %s=%q", name, key, a.Val))
					}
				case forbiddenAttrs[key]:
					errs = append(errs, fmt.Errorf("<%s> has forbidden attribute %s=%q", name, key, a.Val))
				case urlAttrs[key]:
					if err := p.urlOK(name, key, a.Val); err != nil {
						errs = append(errs, err)
					}
				case key == "style":
					if p.NoStyle {
						errs = append(errs, fmt.Errorf("<%s> has style %q", name, a.Val))
					} else if err := CSSOK(a.Val); err != nil {
						errs = append(errs, fmt.Errorf("<%s>: %w", name, err))
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inSVG)
		}
	}
	for _, n := range nodes {
		walk(n, false)
	}
	return errors.Join(errs...)
}

func attrVal(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return a.Val
		}
	}
	return ""
}

// ParseBody は s を body 内（div の子）の断片としてブラウザと同じ規則で解析する。
func ParseBody(s string) ([]*html.Node, error) {
	ctx := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	return html.ParseFragment(strings.NewReader(s), ctx)
}

// RenderNodes は解析結果を直列化する（ブラウザの innerHTML 相当）。
func RenderNodes(nodes []*html.Node) string {
	var sb strings.Builder
	for _, n := range nodes {
		_ = html.Render(&sb, n)
	}
	return sb.String()
}

// CheckHTML は出力 HTML s を判定する。
//
//  1. s をブラウザと同じ規則で解析した木に許可外の要素・属性・URL・CSS が無い
//  2. その木を直列化した s1 を再解析した木（innerHTML の読み書きを 1 往復したもの）でも 1 が成り立つ
//  3. s1 をさらに直列化・再解析しても要素と属性の集合が変わらない（直列化が安定している）
func CheckHTML(s string, p HTMLPolicy) error {
	n0, err := ParseBody(s)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	if err := p.checkTree(n0); err != nil {
		return fmt.Errorf("output: %w", err)
	}
	s1 := RenderNodes(n0)
	n1, err := ParseBody(s1)
	if err != nil {
		return fmt.Errorf("reparse: %w", err)
	}
	if err := p.checkTree(n1); err != nil {
		return fmt.Errorf("after innerHTML round trip (mXSS): %w\nreserialized: %q", err, s1)
	}
	s2 := RenderNodes(n1)
	n2, err := ParseBody(s2)
	if err != nil {
		return fmt.Errorf("reparse 2: %w", err)
	}
	if a, b := Signature(n1), Signature(n2); a != b {
		return fmt.Errorf("unstable serialization (mXSS):\n s1=%q\n s2=%q\n sig1=%s\n sig2=%s", s1, s2, a, b)
	}
	return nil
}

// Signature は木に現れる要素名と属性名（値は URL・style・on* のみ）の集合を文字列にしたもの。
func Signature(nodes []*html.Node) string {
	set := map[string]bool{}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			el := n.Namespace + ":" + n.Data
			set[el] = true
			for _, a := range n.Attr {
				k := strings.ToLower(a.Key)
				if urlAttrs[k] {
					set[el+"@"+k+"="+URLScheme(a.Val)] = true
				} else {
					set[el+"@"+k] = true
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, " ")
}
