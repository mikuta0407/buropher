// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package mailhandler

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// このファイルは HTML メールの本文のテキスト化（Redmine::WikiFormatting::HtmlParser.to_text と、
// 書式ごとの Textile::HtmlParser / CommonMark::HtmlParser のタグ対応表）。
//
// Redmine は Loofah（Nokogiri / libxml2 の HTML4 パーサ）を使うが、ここでは golang.org/x/net/html
// （HTML5 パーサ）で解析する。メール本文のテキスト化で差が出るのは不正な HTML の木構造の補正に限られる。

// tagFormat は WikiTags のタグの変換規則。
type tagFormat struct {
	pre, post string
	// remove は 'style' => ''（要素を空文字列に置き換える）。
	remove bool
	// link は 'a' の lambda。
	link func(content, href string, hasHref bool) string
}

var baseTags = map[string]tagFormat{
	"br":    {post: "\n"},
	"style": {remove: true},
}

func mergeTags(extra map[string]tagFormat) map[string]tagFormat {
	m := map[string]tagFormat{}
	for k, v := range baseTags {
		m[k] = v
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

var textileTags = mergeTags(map[string]tagFormat{
	"b":      {pre: "*", post: "*"},
	"strong": {pre: "*", post: "*"},
	"i":      {pre: "_", post: "_"},
	"em":     {pre: "_", post: "_"},
	"u":      {pre: "+", post: "+"},
	"strike": {pre: "-", post: "-"},
	"h1":     {pre: "\n\nh1. ", post: "\n\n"},
	"h2":     {pre: "\n\nh2. ", post: "\n\n"},
	"h3":     {pre: "\n\nh3. ", post: "\n\n"},
	"h4":     {pre: "\n\nh4. ", post: "\n\n"},
	"h5":     {pre: "\n\nh5. ", post: "\n\n"},
	"h6":     {pre: "\n\nh6. ", post: "\n\n"},
	"th":     {pre: "*", post: "*\n"},
	"td":     {pre: "", post: "\n"},
	"a": {link: func(content, href string, hasHref bool) string {
		switch {
		case strings.TrimSpace(content) != "" && hasHref:
			return ` "` + content + `":` + href + ` `
		case hasHref:
			return ` ` + href + ` `
		}
		return content
	}},
})

var commonMarkTags = mergeTags(map[string]tagFormat{
	"b":      {pre: "**", post: "**"},
	"strong": {pre: "**", post: "**"},
	"i":      {pre: "*", post: "*"},
	"em":     {pre: "*", post: "*"},
	"u":      {pre: "_", post: "_"},
	"strike": {pre: "~~", post: "~~"},
	"h1":     {pre: "\n\n# ", post: "\n\n"},
	"h2":     {pre: "\n\n## ", post: "\n\n"},
	"h3":     {pre: "\n\n### ", post: "\n\n"},
	"h4":     {pre: "\n\n#### ", post: "\n\n"},
	"h5":     {pre: "\n\n##### ", post: "\n\n"},
	"h6":     {pre: "\n\n###### ", post: "\n\n"},
	"th":     {pre: "*", post: "*\n"},
	"td":     {pre: "", post: "\n"},
	"a": {link: func(content, href string, hasHref bool) string {
		switch {
		case strings.TrimSpace(content) != "" && hasHref:
			return ` [` + content + `](` + href + `) `
		case hasHref:
			return ` ` + href + ` `
		}
		return content
	}},
})

// htmlParserTags は Redmine::WikiFormatting.html_parser（Setting.text_formatting に応じたタグ対応表）。
func htmlParserTags(textFormatting string) map[string]tagFormat {
	switch textFormatting {
	case "textile":
		return textileTags
	case "common_mark":
		return commonMarkTags
	}
	return baseTags
}

// lineBreakers は Loofah::Elements::LINEBREAKERS（BLOCK_LEVEL + INLINE_LINE_BREAK）。
var lineBreakers = func() map[string]bool {
	m := map[string]bool{}
	for _, s := range strings.Fields(`address blockquote center dir div dl fieldset form h1 h2 h3 h4 h5 h6 hr isindex
		menu noframes noscript ol p pre table ul article aside canvas dd dt figcaption figure footer header hgroup li
		main nav output section tfoot video frameset tbody td th thead tr br`) {
		m[s] = true
	}
	return m
}()

var (
	crlfRe        = regexp.MustCompile(`[\n\r]`)
	extraneousRe  = regexp.MustCompile(`\n\s*\n\s*\n`)
	leadingSpaces = regexp.MustCompile(`(?m)^ +`)
	multiSpaces   = regexp.MustCompile(` {2,}`)
)

// HTMLToText は HtmlParser.to_text(html)。textFormatting は Setting.text_formatting。
func HTMLToText(src, textFormatting string) string {
	tags := htmlParserTags(textFormatting)
	src = crlfRe.ReplaceAllString(src, " ")
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return ""
	}
	body := findBody(doc)
	if body == nil {
		return ""
	}
	// doc.scrub!(WikiTags.new(tags))（bottom_up）
	scrubBottomUp(body, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		f, ok := tags[n.Data]
		if !ok {
			return
		}
		var repl string
		switch {
		case f.remove:
			repl = ""
		case f.link != nil:
			href, has := attr(n, "href")
			repl = f.link(nodeContent(n), href, has)
		default:
			repl = f.pre + nodeContent(n) + f.post
		}
		replaceWithText(n, repl)
	})
	// doc.scrub!(:newline_block_elements)
	scrubBottomUp(body, func(n *html.Node) {
		if n.Type != html.ElementNode || !lineBreakers[n.Data] {
			return
		}
		if n.Data == "br" {
			replaceWithText(n, "\n")
			return
		}
		replaceWithText(n, "\n"+nodeContent(n)+"\n")
	})
	// doc.text(:encode_special_chars => false)（body の子のうちコメント以外のテキスト）
	var sb strings.Builder
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.CommentNode {
			continue
		}
		sb.WriteString(nodeContent(c))
	}
	text := extraneousRe.ReplaceAllString(sb.String(), "\n\n")
	text = rubyStrip(text)
	text = multiSpaces.ReplaceAllString(text, " ")
	return leadingSpaces.ReplaceAllString(text, "")
}

func findBody(n *html.Node) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == atom.Body {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if b := findBody(c); b != nil {
			return b
		}
	}
	return nil
}

// scrubBottomUp は Loofah の :direction => :bottom_up の走査（子を先に処理する）。
func scrubBottomUp(n *html.Node, fn func(*html.Node)) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		scrubBottomUp(c, fn)
		fn(c)
		c = next
	}
}

// nodeContent は Nokogiri の Node#content（子孫のテキストの連結。コメントは含まない）。
func nodeContent(n *html.Node) string {
	switch n.Type {
	case html.TextNode:
		return n.Data
	case html.CommentNode:
		return ""
	}
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			switch c.Type {
			case html.TextNode:
				sb.WriteString(c.Data)
			case html.ElementNode:
				walk(c)
			}
		}
	}
	walk(n)
	return sb.String()
}

func replaceWithText(n *html.Node, s string) {
	t := &html.Node{Type: html.TextNode, Data: s}
	n.Parent.InsertBefore(t, n)
	n.Parent.RemoveChild(n)
}

func attr(n *html.Node, name string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val, true
		}
	}
	return "", false
}

// rubyStrip は String#strip（先頭・末尾の空白と NUL を除く）。
func rubyStrip(s string) string {
	return strings.Trim(s, " \t\n\v\f\r\x00")
}
