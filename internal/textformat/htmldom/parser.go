// SPDX-License-Identifier: GPL-2.0-or-later AND MIT
// Copyright (C) 2026 mikuta0407 and Buropher contributors
// Portions ported from libxml2 (https://gitlab.gnome.org/GNOME/libxml2),
// Copyright (C) 1998-2012 Daniel Veillard, MIT License.

package htmldom

import (
	"strings"
	"unicode/utf8"
)

// elemDesc は libxml2 の htmlElemDesc のうち必要な項目。
type elemDesc struct {
	startTag, endTag, saveEndTag int
	empty, isInline              bool
}

func tagLookup(name string) (elemDesc, bool) {
	d, ok := html40Elements[strings.ToLower(name)]
	return d, ok
}

// htmlEndPriority は libxml2 の htmlEndPriority。
func endPriority(name string) int {
	switch name {
	case "div":
		return 150
	case "td", "th":
		return 160
	case "tr":
		return 170
	case "thead", "tbody", "tfoot":
		return 180
	case "table":
		return 190
	case "head", "body":
		return 200
	case "html":
		return 220
	}
	return 100
}

var allowPCData = map[string]bool{
	"a": true, "abbr": true, "acronym": true, "address": true, "applet": true, "b": true, "bdo": true, "big": true,
	"blockquote": true, "body": true, "button": true, "caption": true, "center": true, "cite": true, "code": true,
	"dd": true, "del": true, "dfn": true, "div": true, "dt": true, "em": true, "font": true, "form": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "h5": true, "h6": true, "i": true, "iframe": true, "ins": true, "kbd": true, "label": true, "legend": true,
	"li": true, "noframes": true, "noscript": true, "object": true, "p": true, "pre": true, "q": true, "s": true, "samp": true,
	"small": true, "span": true, "strike": true, "strong": true, "td": true, "th": true, "tt": true, "u": true, "var": true,
}

// textChunk は libxml2 の HTML_PARSER_BIG_BUFFER_SIZE（文字データの分割単位）。
const textChunk = 1000

// parser は libxml2 HTMLparser.c（htmlParseDocument 系）の移植。
type parser struct {
	in  []byte
	pos int

	names []string // htmlnamePush/Pop のスタック（ctxt->nameTab）
	nodes []*Node  // nodePush/Pop のスタック（ctxt->node）
	doc   *Node
	html  int // ctxt->html
	depth int // ctxt->depth
}

func isBlankCh(c byte) bool { return c == 0x20 || c == 0x09 || c == 0x0A || c == 0x0D }

func isASCIILetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func isASCIIDigit(c byte) bool { return c >= '0' && c <= '9' }

// isChar は XML の Char 生成規則（IS_CHAR）。
func isChar(c rune) bool {
	return c == 0x9 || c == 0xA || c == 0xD || (c >= 0x20 && c <= 0xD7FF) ||
		(c >= 0xE000 && c <= 0xFFFD) || (c >= 0x10000 && c <= 0x10FFFF)
}

func (p *parser) cur() byte { return p.nxt(0) }

func (p *parser) nxt(n int) byte {
	if p.pos+n < len(p.in) {
		return p.in[p.pos+n]
	}
	return 0
}

func (p *parser) upp(n int) byte {
	c := p.nxt(n)
	if c >= 'a' && c <= 'z' {
		return c - 0x20
	}
	return c
}

// curChar は htmlCurrentChar 相当。NUL は（入力途中なら）空白として扱う。
// 不正な UTF-8 はそのバイト値を文字として返す。
func (p *parser) curChar() (rune, int) {
	if p.pos >= len(p.in) {
		return 0, 0
	}
	c := p.in[p.pos]
	if c < 0x80 {
		if c == 0 {
			return ' ', 1
		}
		return rune(c), 1
	}
	r, l := utf8.DecodeRune(p.in[p.pos:])
	if r == utf8.RuneError && l <= 1 {
		return rune(c), 1
	}
	return r, l
}

// next は xmlNextChar 相当（1 文字進める）。
func (p *parser) next() {
	if p.pos >= len(p.in) {
		return
	}
	_, l := p.curChar()
	if l == 0 {
		l = 1
	}
	p.pos += l
}

func (p *parser) skip(n int) {
	p.pos += n
	if p.pos > len(p.in) {
		p.pos = len(p.in)
	}
}

func (p *parser) skipBlanks() {
	for isBlankCh(p.cur()) {
		p.pos++
	}
}

func (p *parser) name() string {
	if len(p.names) == 0 {
		return ""
	}
	return p.names[len(p.names)-1]
}

func (p *parser) namePush(name string) {
	if p.html < 3 && name == "head" {
		p.html = 3
	}
	if p.html < 10 && name == "body" {
		p.html = 10
	}
	p.names = append(p.names, name)
}

func (p *parser) namePop() {
	if len(p.names) > 0 {
		p.names = p.names[:len(p.names)-1]
	}
}

func (p *parser) curNode() *Node {
	if len(p.nodes) == 0 {
		return nil
	}
	return p.nodes[len(p.nodes)-1]
}

func (p *parser) nodePop() {
	if len(p.nodes) > 0 {
		p.nodes = p.nodes[:len(p.nodes)-1]
	}
}

// ---- SAX2 相当 ----

func (p *parser) appendChild(n *Node) {
	parent := p.curNode()
	if parent == nil {
		parent = p.doc
	}
	parent.AppendChild(n)
}

func (p *parser) startElement(name string, attrs []Attr) {
	el := &Node{Type: ElementNode, Data: name, Attr: attrs}
	p.appendChild(el)
	p.nodes = append(p.nodes, el)
}

func (p *parser) endElement() {
	p.nodePop()
}

func (p *parser) characters(s string, typ NodeType) {
	parent := p.curNode()
	if parent == nil {
		// libxml2 は ctxt->node が NULL の場合テキストを捨てる
		return
	}
	if last := parent.LastChild; last != nil && last.Type == typ {
		last.Data += s
		return
	}
	parent.AppendChild(&Node{Type: typ, Data: s})
}

func (p *parser) comment(s string) {
	p.appendChild(&Node{Type: CommentNode, Data: s})
}

// ---- 自動クローズ ----

func checkAutoClose(newtag, oldtag string) bool {
	return htmlStartClose[[2]string{oldtag, newtag}]
}

func (p *parser) autoCloseOnClose(newtag string) {
	priority := endPriority(newtag)
	i := len(p.names) - 1
	for ; i >= 0; i-- {
		if p.names[i] == newtag {
			break
		}
		if endPriority(p.names[i]) > priority {
			return
		}
	}
	if i < 0 {
		return
	}
	for p.name() != newtag {
		p.endElement()
		p.namePop()
	}
}

func (p *parser) autoCloseOnEnd() {
	for len(p.names) > 0 {
		p.endElement()
		p.namePop()
	}
}

func (p *parser) autoClose(newtag string) {
	for p.name() != "" && checkAutoClose(newtag, p.name()) {
		p.endElement()
		p.namePop()
	}
}

func (p *parser) checkImplied(newtag string) {
	if newtag == "html" {
		return
	}
	if len(p.names) <= 0 {
		p.namePush("html")
		p.startElement("html", nil)
	}
	if newtag == "body" || newtag == "head" {
		return
	}
	if len(p.names) <= 1 && (newtag == "script" || newtag == "style" || newtag == "meta" ||
		newtag == "link" || newtag == "title" || newtag == "base") {
		if p.html >= 3 {
			return
		}
		p.namePush("head")
		p.startElement("head", nil)
	} else if newtag != "noframes" && newtag != "frame" && newtag != "frameset" {
		if p.html >= 10 {
			return
		}
		for _, n := range p.names {
			if n == "body" || n == "head" {
				return
			}
		}
		p.namePush("body")
		p.startElement("body", nil)
	}
}

func (p *parser) checkParagraph() {
	tag := p.name()
	if tag == "" || tag == "html" || tag == "head" {
		p.autoClose("p")
		p.checkImplied("p")
		p.namePush("p")
		p.startElement("p", nil)
	}
}

func (p *parser) areBlanks(s []byte) bool {
	for _, c := range s {
		if !isBlankCh(c) {
			return false
		}
	}
	if p.cur() == 0 {
		return true
	}
	if p.cur() != '<' {
		return false
	}
	name := p.name()
	if name == "" || name == "html" || name == "head" {
		return true
	}
	node := p.curNode()
	if node == nil {
		return false
	}
	last := node.LastChild
	for last != nil && last.Type == CommentNode {
		last = last.PrevSibling
	}
	if last == nil {
		if allowPCData[name] {
			return false
		}
	} else if last.Type == TextNode {
		return false
	} else if allowPCData[last.Data] {
		return false
	}
	return true
}

// ---- 名前・属性 ----

// parseHTMLName は htmlParseHTMLName（小文字化した要素名・属性名）。
func (p *parser) parseHTMLName() (string, bool) {
	c := p.cur()
	if !isASCIILetter(c) && c != '_' && c != ':' && c != '.' {
		return "", false
	}
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		c = p.cur()
		if !(isASCIILetter(c) || isASCIIDigit(c) || c == ':' || c == '-' || c == '_' || c == '.') {
			break
		}
		if c >= 'A' && c <= 'Z' {
			c += 0x20
		}
		sb.WriteByte(c)
		p.next()
	}
	return sb.String(), true
}

// parseHTMLNameNonInvasive は htmlParseHTMLName_nonInvasive（"<" の次から読むが位置は進めない）。
func (p *parser) parseHTMLNameNonInvasive() (string, bool) {
	c := p.nxt(1)
	if !isASCIILetter(c) && c != '_' && c != ':' {
		return "", false
	}
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		c = p.nxt(1 + i)
		if !(isASCIILetter(c) || isASCIIDigit(c) || c == ':' || c == '-' || c == '_') {
			break
		}
		if c >= 'A' && c <= 'Z' {
			c += 0x20
		}
		sb.WriteByte(c)
	}
	return sb.String(), true
}

func isNameStartByte(c byte) bool {
	return isASCIILetter(c) || c == '_' || c == ':'
}

func isNameByte(c byte) bool {
	return isASCIILetter(c) || isASCIIDigit(c) || c == '_' || c == '-' || c == ':' || c == '.'
}

// parseName は htmlParseName（実体参照名などに使う XML Name）。
func (p *parser) parseName() (string, bool) {
	start := p.pos
	if p.pos < len(p.in) && isNameStartByte(p.in[p.pos]) {
		i := p.pos + 1
		for i < len(p.in) && isNameByte(p.in[i]) {
			i++
		}
		if i == len(p.in) {
			// libxml2: 入力末尾に達した場合は NULL
			return "", false
		}
		if p.in[i] > 0 && p.in[i] < 0x80 {
			p.pos = i
			return string(p.in[start:i]), true
		}
	}
	return p.parseNameComplex()
}

func (p *parser) parseNameComplex() (string, bool) {
	start := p.pos
	c, l := p.curChar()
	if c == ' ' || c == '>' || c == '/' || (!isLetter(c) && c != '_' && c != ':') {
		return "", false
	}
	for c != ' ' && c != '>' && c != '/' &&
		(isLetter(c) || isDigit(c) || c == '.' || c == '-' || c == '_' || c == ':' || isCombining(c) || isExtender(c)) {
		p.pos += l
		c, l = p.curChar()
		if l == 0 {
			break
		}
	}
	if p.pos == start {
		return "", false
	}
	return string(p.in[start:p.pos]), true
}

// parseCharRef は htmlParseCharRef。不正値は 0。
func (p *parser) parseCharRef() rune {
	val := 0
	if p.cur() == '&' && p.nxt(1) == '#' && (p.nxt(2) == 'x' || p.nxt(2) == 'X') {
		p.skip(3)
		for p.cur() != ';' {
			c := p.cur()
			switch {
			case c >= '0' && c <= '9':
				if val < 0x110000 {
					val = val*16 + int(c-'0')
				}
			case c >= 'a' && c <= 'f':
				if val < 0x110000 {
					val = val*16 + int(c-'a') + 10
				}
			case c >= 'A' && c <= 'F':
				if val < 0x110000 {
					val = val*16 + int(c-'A') + 10
				}
			default:
				goto done
			}
			p.next()
		}
		if p.cur() == ';' {
			p.next()
		}
	} else if p.cur() == '&' && p.nxt(1) == '#' {
		p.skip(2)
		for p.cur() != ';' {
			c := p.cur()
			if c >= '0' && c <= '9' {
				if val < 0x110000 {
					val = val*10 + int(c-'0')
				}
			} else {
				goto done
			}
			p.next()
		}
		if p.cur() == ';' {
			p.next()
		}
	}
done:
	if isChar(rune(val)) {
		return rune(val)
	}
	return 0
}

// parseEntityRef は htmlParseEntityRef。名前が無ければ ok=false。
func (p *parser) parseEntityRef() (name string, val rune, found bool, ok bool) {
	if p.cur() != '&' {
		return "", 0, false, false
	}
	p.next()
	name, ok = p.parseName()
	if !ok {
		return "", 0, false, false
	}
	if p.cur() == ';' {
		if v, e := html40Entities[name]; e {
			p.next()
			return name, v, true, true
		}
	}
	return name, 0, false, true
}

// parseHTMLAttribute は htmlParseHTMLAttribute。stop が 0 の場合は空白か > で終わる。
// 値の途中に NUL（不正な文字参照）が入った場合、C 文字列としてそこで切れる挙動を再現する。
func (p *parser) parseHTMLAttribute(stop byte) string {
	var buf []byte
	truncated := -1
	for p.cur() != 0 && p.cur() != stop {
		if stop == 0 && p.cur() == '>' {
			break
		}
		if stop == 0 && isBlankCh(p.cur()) {
			break
		}
		if p.cur() == '&' {
			if p.nxt(1) == '#' {
				c := p.parseCharRef()
				if c == 0 && truncated < 0 {
					truncated = len(buf)
				}
				buf = utf8.AppendRune(buf, c)
			} else {
				name, v, found, ok := p.parseEntityRef()
				switch {
				case !ok:
					buf = append(buf, '&')
				case !found:
					buf = append(buf, '&')
					buf = append(buf, name...)
				default:
					buf = utf8.AppendRune(buf, v)
				}
			}
		} else {
			c, l := p.curChar()
			buf = utf8.AppendRune(buf, c)
			p.pos += l
		}
	}
	if truncated >= 0 {
		buf = buf[:truncated]
	}
	return string(buf)
}

func (p *parser) parseAttValue() (string, bool) {
	switch p.cur() {
	case '"':
		p.next()
		v := p.parseHTMLAttribute('"')
		if p.cur() == '"' {
			p.next()
		}
		return v, true
	case '\'':
		p.next()
		v := p.parseHTMLAttribute('\'')
		if p.cur() == '\'' {
			p.next()
		}
		return v, true
	}
	return p.parseHTMLAttribute(0), true
}

func (p *parser) parseAttribute() (name string, value string, hasValue bool, ok bool) {
	name, ok = p.parseHTMLName()
	if !ok {
		return "", "", false, false
	}
	p.skipBlanks()
	if p.cur() == '=' {
		p.next()
		p.skipBlanks()
		value, hasValue = p.parseAttValue()
	}
	return name, value, hasValue, true
}

// ---- 要素 ----

// parseStartTag は htmlParseStartTag。戻り値: -1 エラー, 0 成功, 1 破棄。
func (p *parser) parseStartTag() int {
	if p.cur() != '<' {
		return -1
	}
	p.next()
	name, ok := p.parseHTMLName()
	if !ok {
		for p.cur() != 0 && p.cur() != '>' {
			p.next()
		}
		return -1
	}
	p.autoClose(name)
	p.checkImplied(name)

	discard := 0
	if len(p.names) > 0 && name == "html" {
		discard = 1
		p.depth++
	}
	if len(p.names) != 1 && name == "head" {
		discard = 1
		p.depth++
	}
	if name == "body" {
		for _, n := range p.names {
			if n == "body" {
				discard = 1
				p.depth++
			}
		}
	}

	var attrs []Attr
	p.skipBlanks()
	for p.cur() != 0 && p.cur() != '>' && (p.cur() != '/' || p.nxt(1) != '>') {
		aname, aval, hasVal, ok := p.parseAttribute()
		if ok {
			dup := false
			for _, a := range attrs {
				if a.Name == aname {
					dup = true
					break
				}
			}
			if !dup {
				if !hasVal && isBooleanAttr(aname) {
					attrs = append(attrs, Attr{Name: aname, Value: aname})
				} else {
					attrs = append(attrs, Attr{Name: aname, Value: aval, NoValue: !hasVal})
				}
			}
		} else {
			for p.cur() != 0 && !isBlankCh(p.cur()) && p.cur() != '>' && (p.cur() != '/' || p.nxt(1) != '>') {
				p.next()
			}
		}
		p.skipBlanks()
	}

	if discard == 0 {
		p.namePush(name)
		p.startElement(name, attrs)
	}
	return discard
}

// parseEndTag は htmlParseEndTag。現在の要素を閉じた場合 true。
func (p *parser) parseEndTag() bool {
	if p.cur() != '<' || p.nxt(1) != '/' {
		return false
	}
	p.skip(2)
	name, ok := p.parseHTMLName()
	if !ok {
		return false
	}
	p.skipBlanks()
	if p.cur() != '>' {
		for p.cur() != 0 && p.cur() != '>' {
			p.next()
		}
	}
	if p.cur() == '>' {
		p.next()
	}
	if p.depth > 0 && (name == "html" || name == "body" || name == "head") {
		p.depth--
		return false
	}
	i := len(p.names) - 1
	for ; i >= 0; i-- {
		if p.names[i] == name {
			break
		}
	}
	if i < 0 {
		return false
	}
	p.autoCloseOnClose(name)
	if p.name() != "" && p.name() == name {
		p.endElement()
		p.namePop()
		return true
	}
	return false
}

func (p *parser) parseReference() {
	if p.cur() != '&' {
		return
	}
	if p.nxt(1) == '#' {
		c := p.parseCharRef()
		if c == 0 {
			return
		}
		p.checkParagraph()
		p.characters(string(c), TextNode)
		return
	}
	name, v, found, ok := p.parseEntityRef()
	if !ok {
		p.checkParagraph()
		p.characters("&", TextNode)
		return
	}
	if !found || v <= 0 {
		p.checkParagraph()
		p.characters("&"+name, TextNode)
		return
	}
	p.checkParagraph()
	p.characters(string(v), TextNode)
}

// parseScript は htmlParseScript（script/style の内容を CDATA として読む。回復モード）。
func (p *parser) parseScript() {
	var buf []byte
	name := p.name()
	for {
		c, l := p.curChar()
		if l == 0 {
			break
		}
		if c == '<' && p.nxt(1) == '/' {
			rest := p.in[p.pos+2:]
			if len(rest) >= len(name) && strings.EqualFold(string(rest[:len(name)]), name) {
				break
			}
		}
		if isChar(c) {
			buf = utf8.AppendRune(buf, c)
		}
		p.pos += l
		if len(buf) >= textChunk {
			p.characters(string(buf), CDATANode)
			buf = buf[:0]
		}
	}
	if len(buf) > 0 {
		p.characters(string(buf), CDATANode)
	}
}

func (p *parser) flushCharData(buf []byte) {
	if p.areBlanks(buf) {
		p.characters(string(buf), TextNode)
	} else {
		p.checkParagraph()
		p.characters(string(buf), TextNode)
	}
}

// parseCharData は htmlParseCharDataInternal。
func (p *parser) parseCharData() {
	var buf []byte
	for {
		c, l := p.curChar()
		if c == '<' || c == '&' || l == 0 {
			break
		}
		if isChar(c) {
			buf = utf8.AppendRune(buf, c)
		}
		p.pos += l
		if len(buf) >= textChunk {
			p.flushCharData(buf)
			buf = buf[:0]
		}
	}
	if len(buf) > 0 {
		p.flushCharData(buf)
	}
}

func (p *parser) parseComment() {
	if p.cur() != '<' || p.nxt(1) != '!' || p.nxt(2) != '-' || p.nxt(3) != '-' {
		return
	}
	p.skip(4)
	var buf []byte
	q, ql := p.curChar()
	if ql == 0 {
		return
	}
	if q == '>' {
		p.next()
		p.comment("")
		return
	}
	p.pos += ql
	r, rl := p.curChar()
	if rl == 0 {
		return
	}
	if q == '-' && r == '>' {
		p.next()
		p.comment("")
		return
	}
	p.pos += rl
	cur, l := p.curChar()
	for l != 0 && (cur != '>' || r != '-' || q != '-') {
		p.pos += l
		next, nl := p.curChar()
		if q == '-' && r == '-' && cur == '!' && next == '>' {
			cur = '>'
			l = 1
			break
		}
		if isChar(q) {
			buf = utf8.AppendRune(buf, q)
		}
		q, ql = r, rl
		r, rl = cur, l
		cur, l = next, nl
	}
	_ = ql
	_ = rl
	if l != 0 && cur == '>' {
		p.next()
		p.comment(string(buf))
	}
}

func (p *parser) skipBogusComment() {
	for {
		c := p.cur()
		if c == 0 {
			break
		}
		p.next()
		if c == '>' {
			break
		}
	}
}

func (p *parser) parsePI() {
	if p.cur() != '<' || p.nxt(1) != '?' {
		return
	}
	p.skip(2)
	target, ok := p.parseName()
	if !ok {
		return
	}
	if p.cur() == '>' {
		p.skip(1)
		p.appendChild(&Node{Type: PINode, Data: target})
		return
	}
	p.skipBlanks()
	var buf []byte
	c, l := p.curChar()
	for l != 0 && c != '>' {
		if isChar(c) {
			buf = utf8.AppendRune(buf, c)
		}
		p.pos += l
		c, l = p.curChar()
	}
	if c == '>' && l != 0 {
		p.skip(1)
		p.appendChild(&Node{Type: PINode, Data: target, PIContent: string(buf), HasPIContent: true})
	}
}

// parseDocTypeDecl は DOCTYPE を読み飛ばす（DTD ノードは断片に含まれないため作らない）。
func (p *parser) parseDocTypeDecl() {
	p.skip(9)
	p.skipBlanks()
	p.parseName()
	p.skipBlanks()
	p.parseExternalID()
	p.skipBlanks()
	for p.cur() != 0 && p.cur() != '>' {
		p.next()
	}
	if p.cur() == '>' {
		p.next()
	}
}

func (p *parser) parseExternalID() {
	parseLiteral := func() {
		q := p.cur()
		if q != '"' && q != '\'' {
			return
		}
		p.next()
		for p.cur() != 0 && p.cur() != q {
			p.next()
		}
		if p.cur() == q {
			p.next()
		}
	}
	if p.upp(0) == 'S' && p.upp(1) == 'Y' && p.upp(2) == 'S' && p.upp(3) == 'T' && p.upp(4) == 'E' && p.upp(5) == 'M' {
		p.skip(6)
		p.skipBlanks()
		parseLiteral()
	} else if p.upp(0) == 'P' && p.upp(1) == 'U' && p.upp(2) == 'B' && p.upp(3) == 'L' && p.upp(4) == 'I' && p.upp(5) == 'C' {
		p.skip(6)
		p.skipBlanks()
		parseLiteral()
		p.skipBlanks()
		if p.cur() == '"' || p.cur() == '\'' {
			parseLiteral()
		}
	}
}

func (p *parser) finishElementParsing() {
	if p.cur() == 0 {
		p.autoCloseOnEnd()
	}
}

func (p *parser) parseElementInternal() {
	failed := p.parseStartTag()
	name := p.name()
	if failed == -1 || name == "" {
		if p.cur() == '>' {
			p.next()
		}
		return
	}
	info, known := tagLookup(name)
	if p.cur() == '/' && p.nxt(1) == '>' {
		p.skip(2)
		p.endElement()
		p.namePop()
		return
	}
	if p.cur() == '>' {
		p.next()
	} else {
		// 開始タグの終わりが見つからない: 要素はそのまま閉じる
		if name == p.name() {
			p.nodePop()
			p.namePop()
		}
		p.finishElementParsing()
		return
	}
	if known && info.empty {
		p.endElement()
		p.namePop()
	}
}

func (p *parser) parseContentInternal() {
	depth := len(p.names)
	currentNode := p.name()
	for {
		if p.cur() == '<' && p.nxt(1) == '/' {
			if p.parseEndTag() && (currentNode != "" || len(p.names) == 0) {
				depth = len(p.names)
				currentNode = p.name()
			}
			continue
		} else if p.cur() == '<' && (isASCIILetter(p.nxt(1)) || p.nxt(1) == '_' || p.nxt(1) == ':') {
			name, ok := p.parseHTMLNameNonInvasive()
			if !ok {
				p.finishElementParsing()
				currentNode = p.name()
				depth = len(p.names)
				continue
			}
			if p.name() != "" && checkAutoClose(name, p.name()) {
				p.autoClose(name)
				continue
			}
		}

		if len(p.names) > 0 && depth >= len(p.names) && currentNode != p.name() {
			p.finishElementParsing()
			currentNode = p.name()
			depth = len(p.names)
			continue
		}

		switch {
		case p.cur() != 0 && (currentNode == "script" || currentNode == "style"):
			p.parseScript()
		case p.cur() == '<' && p.nxt(1) == '!':
			if p.upp(2) == 'D' && p.upp(3) == 'O' && p.upp(4) == 'C' && p.upp(5) == 'T' &&
				p.upp(6) == 'Y' && p.upp(7) == 'P' && p.upp(8) == 'E' {
				p.parseDocTypeDecl()
			} else if p.nxt(2) == '-' && p.nxt(3) == '-' {
				p.parseComment()
			} else {
				p.skipBogusComment()
			}
		case p.cur() == '<' && p.nxt(1) == '?':
			p.parsePI()
		case p.cur() == '<' && isASCIILetter(p.nxt(1)):
			p.parseElementInternal()
			currentNode = p.name()
			depth = len(p.names)
		case p.cur() == '<':
			p.characters("<", TextNode)
			p.next()
		case p.cur() == '&':
			p.parseReference()
		case p.cur() == 0:
			p.autoCloseOnEnd()
			return
		default:
			p.parseCharData()
		}
	}
}

func (p *parser) parseDocument() {
	p.skipBlanks()
	for (p.cur() == '<' && p.nxt(1) == '!' && p.nxt(2) == '-' && p.nxt(3) == '-') ||
		(p.cur() == '<' && p.nxt(1) == '?') {
		p.parseComment()
		p.parsePI()
		p.skipBlanks()
	}
	if p.cur() == '<' && p.nxt(1) == '!' && p.upp(2) == 'D' && p.upp(3) == 'O' && p.upp(4) == 'C' &&
		p.upp(5) == 'T' && p.upp(6) == 'Y' && p.upp(7) == 'P' && p.upp(8) == 'E' {
		p.parseDocTypeDecl()
	}
	p.skipBlanks()
	for (p.cur() == '<' && p.nxt(1) == '!' && p.nxt(2) == '-' && p.nxt(3) == '-') ||
		(p.cur() == '<' && p.nxt(1) == '?') {
		p.parseComment()
		p.parsePI()
		p.skipBlanks()
	}
	p.parseContentInternal()
	if p.cur() == 0 {
		p.autoCloseOnEnd()
	}
}

func isBooleanAttr(name string) bool {
	switch strings.ToLower(name) {
	case "checked", "compact", "declare", "defer", "disabled", "ismap",
		"multiple", "nohref", "noresize", "noshade", "nowrap", "readonly", "selected":
		return true
	}
	return false
}

// ParseFragment は Nokogiri::HTML4::DocumentFragment.parse(html) 相当。
// "<html><body>" + html を libxml2 互換で解析し、body の子を断片として返す。
func ParseFragment(html string) *Node {
	p := &parser{in: []byte("<html><body>" + html), doc: &Node{Type: FragmentNode}}
	p.parseDocument()
	frag := &Node{Type: FragmentNode}
	bodyItself := isBodyStart(html)
	for h := p.doc.FirstChild; h != nil; h = h.NextSibling {
		if !h.IsElement("html") {
			continue
		}
		for b := h.FirstChild; b != nil; {
			next := b.NextSibling
			if b.IsElement("body") {
				if bodyItself {
					frag.AppendChild(b)
				} else {
					for _, c := range b.Children() {
						frag.AppendChild(c)
					}
				}
			}
			b = next
		}
	}
	return frag
}

// isBodyStart は Nokogiri の /^\s*?<body/i 判定（Ruby の ^ は行頭）。
func isBodyStart(s string) bool {
	for _, line := range strings.SplitAfter(s, "\n") {
		t := strings.TrimLeft(line, " \t\r\n\f\v")
		if len(t) >= 5 && strings.EqualFold(t[:5], "<body") {
			return true
		}
	}
	return false
}
