// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package commonmark

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
)

// renderer は comrak の html.rs（Redmine の render オプション: unsafe, github_pre_lang=false,
// tasklist_classes, escaped_char_spans, hardbreaks, tagfilter）を移植した HTML 出力器。
type renderer struct {
	buf        bytes.Buffer
	src        []byte
	hardbreaks bool

	footnoteIx        int
	writtenFootnoteIx int
}

func (r *renderer) write(s string) { r.buf.WriteString(s) }

// cr は直前の出力が改行でなければ改行を書く。
func (r *renderer) cr() {
	b := r.buf.Bytes()
	if len(b) > 0 && b[len(b)-1] != '\n' {
		r.buf.WriteByte('\n')
	}
}

// escape は comrak の html::escape（& < > " をエスケープ）。
func (r *renderer) escape(s string) {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"':
			r.buf.WriteString("&quot;")
		case '&':
			r.buf.WriteString("&amp;")
		case '<':
			r.buf.WriteString("&lt;")
		case '>':
			r.buf.WriteString("&gt;")
		default:
			r.buf.WriteByte(c)
		}
	}
}

// escapeHref は comrak の html::escape_href。
func (r *renderer) escapeHref(s string) {
	i := 0
	if end := ipv6URLStart(s); end > 0 {
		r.buf.WriteString(s[:end])
		i = end
	}
	const hexDigits = "0123456789ABCDEF"
	for ; i < len(s); i++ {
		c := s[i]
		switch {
		case isHrefSafe(c):
			r.buf.WriteByte(c)
		case c == '&':
			r.buf.WriteString("&amp;")
		case c == '\'':
			r.buf.WriteString("&#x27;")
		default:
			r.buf.WriteByte('%')
			r.buf.WriteByte(hexDigits[c>>4])
			r.buf.WriteByte(hexDigits[c&0xF])
		}
	}
}

func isHrefSafe(c byte) bool {
	return isAlnum(c) || strings.IndexByte("-_.+!*(),%#@?=;:/,+$~", c) >= 0
}

// ipv6URLStart は 'http' [s]? '://[' [0-9a-fA-F:]+ ('%25' [a-zA-Z0-9]+)? ']'（大文字小文字無視）。
func ipv6URLStart(s string) int {
	l := strings.ToLower(s)
	var i int
	switch {
	case strings.HasPrefix(l, "https://["):
		i = 9
	case strings.HasPrefix(l, "http://["):
		i = 8
	default:
		return 0
	}
	start := i
	for i < len(s) && (isHex(s[i]) || s[i] == ':') {
		i++
	}
	if i == start {
		return 0
	}
	if strings.HasPrefix(s[i:], "%25") {
		j := i + 3
		for j < len(s) && isAlnum(s[j]) {
			j++
		}
		if j > i+3 {
			i = j
		}
	}
	if i < len(s) && s[i] == ']' {
		return i + 1
	}
	return 0
}

var tagfilterNames = []string{"title", "textarea", "style", "xmp", "iframe", "noembed", "noframes", "script", "plaintext"}

// tagfilter は comrak の tagfilter（GFM の禁止タグかどうか）。
func tagfilter(lit string) bool {
	if len(lit) < 3 || lit[0] != '<' {
		return false
	}
	i := 1
	if lit[i] == '/' {
		i++
	}
	lc := strings.ToLower(lit[i:])
	for _, t := range tagfilterNames {
		if strings.HasPrefix(lc, t) {
			j := i + len(t)
			if j >= len(lit) {
				return false
			}
			return isSpace(lit[j]) || lit[j] == '>' || (lit[j] == '/' && len(lit) >= j+2 && lit[j+1] == '>')
		}
	}
	return false
}

func (r *renderer) tagfilterBlock(s string) {
	for i := 0; i < len(s); i++ {
		if s[i] == '<' && tagfilter(s[i:]) {
			r.buf.WriteString("&lt;")
		} else {
			r.buf.WriteByte(s[i])
		}
	}
}

func (r *renderer) render(n ast.Node) {
	r.node(n)
	if r.footnoteIx > 0 {
		r.write("</ol>\n</section>\n")
	}
}

func (r *renderer) children(n ast.Node) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		r.node(c)
	}
}

func (r *renderer) blockLines(n ast.Node) string {
	var sb strings.Builder
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		s := lines.At(i)
		sb.Write(s.Value(r.src))
	}
	return sb.String()
}

func listTight(n ast.Node) bool {
	if l, ok := n.(*ast.List); ok {
		return l.IsTight
	}
	return false
}

func (r *renderer) node(n ast.Node) {
	switch n := n.(type) {
	case *ast.Document:
		r.children(n)
	case *ast.Blockquote:
		r.cr()
		r.write("<blockquote>\n")
		r.children(n)
		r.cr()
		r.write("</blockquote>\n")
	case *Alert:
		r.cr()
		r.write(`<div class="markdown-alert markdown-alert-` + n.AlertType + "\">\n")
		r.write(`<p class="markdown-alert-title">`)
		if n.HasTitle {
			r.escape(n.Title)
		} else {
			r.write(alertDefaultTitle(n.AlertType))
		}
		r.write("</p>\n")
		r.children(n)
		r.cr()
		r.write("</div>\n")
	case *ast.Heading:
		r.cr()
		r.write("<h" + strconv.Itoa(n.Level) + ">")
		r.children(n)
		r.write("</h" + strconv.Itoa(n.Level) + ">\n")
	case *ast.ThematicBreak:
		r.cr()
		r.write("<hr />\n")
	case *ast.CodeBlock:
		r.cr()
		r.write("<pre><code>")
		r.escape(r.blockLines(n))
		r.write("</code></pre>\n")
	case *ast.FencedCodeBlock:
		r.cr()
		info := ""
		if n.Info != nil {
			info = fenceInfo(n.Info.Segment.Value(r.src))
		}
		if info != "" {
			lang := info
			if i := strings.IndexAny(info, " \t\n\v\f\r"); i >= 0 {
				lang = info[:i]
			}
			r.write(`<pre><code class="`)
			r.escape("language-" + lang)
			r.write(`">`)
		} else {
			r.write("<pre><code>")
		}
		r.escape(r.blockLines(n))
		r.write("</code></pre>\n")
	case *ast.HTMLBlock:
		r.cr()
		lit := r.blockLines(n)
		if n.HasClosure() {
			lit += string(n.ClosureLine.Value(r.src))
		}
		r.tagfilterBlock(lit)
		r.cr()
	case *ast.List:
		r.cr()
		task := false
		if v, ok := n.AttributeString("cm-tasklist"); ok && v == true {
			task = true
		}
		if n.IsOrdered() {
			r.write("<ol")
			if task {
				r.write(` class="contains-task-list"`)
			}
			if n.Start == 1 {
				r.write(">\n")
			} else {
				r.write(` start="` + strconv.Itoa(n.Start) + "\">\n")
			}
			r.children(n)
			r.write("</ol>\n")
		} else {
			r.write("<ul")
			if task {
				r.write(` class="contains-task-list"`)
			}
			r.write(">\n")
			r.children(n)
			r.write("</ul>\n")
		}
	case *ast.ListItem:
		r.cr()
		if v, ok := n.AttributeString("cm-task"); ok {
			sym := v.([]byte)[0]
			r.write(`<li class="task-list-item"><input type="checkbox" class="task-list-item-checkbox"`)
			if sym != ' ' {
				r.write(` checked=""`)
			}
			r.write(` disabled="" /> `)
		} else {
			r.write("<li>")
		}
		r.children(n)
		r.write("</li>\n")
	case *ast.Paragraph, *ast.TextBlock:
		tight := false
		if p := n.Parent(); p != nil {
			tight = listTight(p.Parent())
		}
		if _, ok := n.(*ast.TextBlock); ok {
			tight = true
		}
		if tight {
			r.children(n)
			return
		}
		r.cr()
		r.write("<p>")
		r.children(n)
		if fd, ok := n.Parent().(*FootnoteDef); ok && n.NextSibling() == nil {
			r.write(" ")
			r.footnoteBackref(fd)
		}
		r.write("</p>\n")
	case *east.Table:
		r.cr()
		r.write("<table>\n")
		r.children(n)
		if n.FirstChild() != n.LastChild() {
			r.cr()
			r.write("</tbody>\n")
		}
		r.cr()
		r.write("</table>\n")
	case *east.TableHeader:
		r.cr()
		r.write("<thead>\n<tr>")
		r.children(n)
		r.cr()
		r.write("</tr>")
		r.cr()
		r.write("</thead>")
	case *east.TableRow:
		r.cr()
		if _, ok := n.PreviousSibling().(*east.TableHeader); ok {
			r.write("<tbody>\n")
		}
		r.write("<tr>")
		r.children(n)
		r.cr()
		r.write("</tr>")
	case *east.TableCell:
		r.cr()
		_, header := n.Parent().(*east.TableHeader)
		tag := "td"
		if header {
			tag = "th"
		}
		r.write("<" + tag)
		switch n.Alignment {
		case east.AlignLeft:
			r.write(` align="left"`)
		case east.AlignRight:
			r.write(` align="right"`)
		case east.AlignCenter:
			r.write(` align="center"`)
		}
		r.write(">")
		r.children(n)
		r.write("</" + tag + ">")
	case *FootnoteDef:
		if r.footnoteIx == 0 {
			r.write("<section class=\"footnotes\" data-footnotes>\n<ol>\n")
		}
		r.footnoteIx++
		r.write(`<li id="fn-`)
		r.escapeHref(n.Name)
		r.write(`">`)
		r.children(n)
		if r.footnoteBackref(n) {
			r.write("\n")
		}
		r.write("</li>\n")

	// ---- インライン ----
	case *Str:
		r.escape(n.Value)
	case *Escaped:
		r.write("<span data-escaped-char>")
		r.children(n)
		r.write("</span>")
	case *SoftBreak:
		if r.hardbreaks {
			r.write("<br />\n")
		} else {
			r.write("\n")
		}
	case *HardBreak:
		r.write("<br />\n")
	case *Code:
		r.write("<code>")
		r.escape(n.Literal)
		r.write("</code>")
	case *HTMLInline:
		if tagfilter(n.Literal) {
			r.write("&lt;")
			r.write(n.Literal[1:])
		} else {
			r.write(n.Literal)
		}
	case *ast.Emphasis:
		tag := "em"
		if n.Level == 2 {
			tag = "strong"
		}
		r.write("<" + tag + ">")
		r.children(n)
		r.write("</" + tag + ">")
	case *east.Strikethrough:
		r.write("<del>")
		r.children(n)
		r.write("</del>")
	case *Link:
		r.write(`<a href="`)
		r.escapeHref(n.URL)
		if n.Title != "" {
			r.write(`" title="`)
			r.escape(n.Title)
		}
		r.write(`">`)
		r.children(n)
		r.write("</a>")
	case *Image:
		r.write(`<img src="`)
		r.escapeHref(n.URL)
		r.write(`" alt="`)
		r.plain(n)
		if n.Title != "" {
			r.write(`" title="`)
			r.escape(n.Title)
		}
		r.write(`" />`)
	case *FootnoteRef:
		refID := "fnref-" + n.Name
		if n.RefNum > 1 {
			refID += "-" + strconv.Itoa(n.RefNum)
		}
		r.write(`<sup class="footnote-ref"><a href="#fn-`)
		r.escapeHref(n.Name)
		r.write(`" id="`)
		r.escapeHref(refID)
		r.write(`" data-footnote-ref>` + strconv.Itoa(n.Ix) + "</a></sup>")
	default:
		r.children(n)
	}
}

// plain は画像の alt 用にテキストだけを出力する（comrak の ChildRendering::Plain）。
func (r *renderer) plain(n ast.Node) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch c := c.(type) {
		case *Str:
			r.escape(c.Value)
		case *Code:
			r.escape(c.Literal)
		case *HTMLInline:
			r.escape(c.Literal)
		case *SoftBreak, *HardBreak:
			r.write(" ")
		default:
			r.plain(c)
		}
	}
}

func (r *renderer) footnoteBackref(fd *FootnoteDef) bool {
	if r.writtenFootnoteIx >= r.footnoteIx {
		return false
	}
	r.writtenFootnoteIx = r.footnoteIx
	for refNum := 1; refNum <= fd.TotalRefs; refNum++ {
		refSuffix, sup := "", ""
		if refNum > 1 {
			refSuffix = "-" + strconv.Itoa(refNum)
			sup = `<sup class="footnote-ref">` + strconv.Itoa(refNum) + "</sup>"
			r.write(" ")
		}
		r.write(`<a href="#fnref-`)
		r.escapeHref(fd.Name)
		ix := strconv.Itoa(r.footnoteIx)
		r.write(refSuffix + `" class="footnote-backref" data-footnote-backref data-footnote-backref-idx="` + ix + refSuffix +
			`" aria-label="Back to reference ` + ix + refSuffix + `">↩` + sup + "</a>")
	}
	return true
}

func alertDefaultTitle(t string) string {
	switch t {
	case "note":
		return "Note"
	case "tip":
		return "Tip"
	case "important":
		return "Important"
	case "warning":
		return "Warning"
	case "caution":
		return "Caution"
	}
	return ""
}

// fenceInfo はフェンスの情報文字列を comrak と同様に整える（実体参照とエスケープを解決し前後の空白を除く）。
func fenceInfo(raw []byte) string {
	s := unescapeHTML(string(raw))
	s = strings.Trim(s, " \t\n\v\f\r")
	return string(unescapeBackslash([]byte(s)))
}
