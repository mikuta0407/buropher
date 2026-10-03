// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package commonmark

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// normalizer は goldmark の AST を comrak の AST に近い形へ変換する AST 変換器。
//
//  1. インラインノードを独自ノード（Str/Escaped/SoftBreak/Link 等）に置き換える
//  2. 脚注の番号付けと定義の移動（comrak の process_footnotes）
//  3. 隣接テキストの結合、タスクリスト、メール自動リンク（postprocess_text_nodes）
type normalizer struct{}

func (n *normalizer) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	src := reader.Source()
	var walkBlocks func(ast.Node)
	walkBlocks = func(b ast.Node) {
		trimLineEnds(b, src)
		for c := b.FirstChild(); c != nil; {
			next := c.NextSibling()
			if c.Type() == ast.TypeInline {
				convertInline(b, c, src)
			} else {
				walkBlocks(c)
			}
			c = next
		}
		if ws, ok := b.AttributeString("cm-lead-ws"); ok {
			// 怠惰な継続行が段落の先頭になった場合の行頭空白（refdef.go）
			if s, ok := ws.(string); ok && b.FirstChild() != nil {
				b.InsertBefore(b, b.FirstChild(), &Str{Value: s})
			}
		}
	}
	removeLinkRefDefs(doc)
	walkBlocks(doc)
	fixTableCellPipes(doc)
	processFootnotes(doc)
	postprocessText(doc, false)
}

// removeLinkRefDefs はリンク参照定義ノードを取り除き、それによって
// 子が減ったリストの tight/loose を goldmark と同じ規則で再判定する
// （comrak では参照定義だけの段落は finalize 時に消えるため、リストの判定に影響しない）。
func removeLinkRefDefs(n ast.Node) {
	for c := n.FirstChild(); c != nil; {
		next := c.NextSibling()
		if _, ok := c.(*ast.LinkReferenceDefinition); ok {
			if item, ok := n.(*ast.ListItem); ok {
				if list, ok := item.Parent().(*ast.List); ok && !list.IsTight {
					// 直前の子が空行で終わり、後続がある場合は cmark でも loose のまま
					if c.HasBlankPreviousLines() && c.PreviousSibling() != nil &&
						(item.NextSibling() != nil || c.NextSibling() != nil) {
						list.SetAttributeString("cm-keep-loose", true)
					}
					list.SetAttributeString("cm-recheck-tight", true)
				}
			}
			n.RemoveChild(n, c)
		} else {
			removeLinkRefDefs(c)
		}
		c = next
	}
	if list, ok := n.(*ast.List); ok {
		if v, ok := list.AttributeString("cm-recheck-tight"); ok && v == true {
			if _, keep := list.AttributeString("cm-keep-loose"); !keep {
				recheckTight(list)
			}
		}
	}
}

func recheckTight(list *ast.List) {
	tight := true
	for c := list.FirstChild(); c != nil && tight; c = c.NextSibling() {
		if c.FirstChild() != nil && c.FirstChild() != c.LastChild() {
			for c1 := c.FirstChild().NextSibling(); c1 != nil; c1 = c1.NextSibling() {
				if c1.HasBlankPreviousLines() {
					tight = false
					break
				}
			}
		}
		if c != list.FirstChild() && c.HasBlankPreviousLines() {
			tight = false
		}
	}
	if !tight {
		return
	}
	list.IsTight = true
	for item := list.FirstChild(); item != nil; item = item.NextSibling() {
		for gc := item.FirstChild(); gc != nil; {
			next := gc.NextSibling()
			if p, ok := gc.(*ast.Paragraph); ok {
				tb := ast.NewTextBlock()
				tb.SetLines(p.Lines())
				for x := p.FirstChild(); x != nil; {
					nx := x.NextSibling()
					tb.AppendChild(tb, x)
					x = nx
				}
				item.ReplaceChild(item, p, tb)
			}
			gc = next
		}
	}
}

// trimLineEnds は行末（ソフト改行・空白 2 つによるハード改行）直前の空白を取り除く。
// goldmark は空白でインラインパーサを起動すると行末の空白が前のテキストに
// 結合されたまま残るため、comrak と同様に取り除く。
func trimLineEnds(b ast.Node, src []byte) {
	for c := b.FirstChild(); c != nil; c = c.NextSibling() {
		t, ok := c.(*ast.Text)
		if !ok || t.IsRaw() || !(t.SoftLineBreak() || t.HardLineBreak()) {
			continue
		}
		if t.HardLineBreak() && t.Segment.Stop < len(src) && src[t.Segment.Stop] == '\\' {
			// バックスラッシュによるハード改行は空白を残す
			continue
		}
		cur := t
		for {
			cur.Segment = cur.Segment.TrimRightSpace(src)
			if cur.Segment.Len() > 0 {
				break
			}
			prev, ok := cur.PreviousSibling().(*ast.Text)
			if !ok || prev.IsRaw() || prev.SoftLineBreak() || prev.HardLineBreak() || prev.Segment.Stop != cur.Segment.Start {
				break
			}
			cur = prev
		}
	}
}

// fixTableCellPipes は表のセル内の "\|" を comrak と同様に "|" として扱う。
func fixTableCellPipes(doc ast.Node) {
	var walk func(n ast.Node, inCell bool)
	walk = func(n ast.Node, inCell bool) {
		for c := n.FirstChild(); c != nil; {
			next := c.NextSibling()
			switch x := c.(type) {
			case *east.TableCell:
				walk(x, true)
			case *Escaped:
				s, _ := x.FirstChild().(*Str)
				if !inCell || s == nil {
					break
				}
				switch s.Value {
				case "|":
					// "\|" → "|"
					n.ReplaceChild(n, x, &Str{Value: "|"})
				case "\\":
					// "\\|" → バックスラッシュが 1 つ除かれ "\|"（エスケープされた '|'）になる
					if ns, ok := x.NextSibling().(*Str); ok && strings.HasPrefix(ns.Value, "|") {
						s.Value = "|"
						ns.Value = ns.Value[1:]
						if ns.Value == "" {
							n.RemoveChild(n, ns)
						}
						next = x.NextSibling()
					}
				}
			case *Code:
				if inCell {
					x.Literal = strings.ReplaceAll(x.Literal, `\|`, "|")
				}
			case *HTMLInline:
				if inCell {
					x.Literal = strings.ReplaceAll(x.Literal, `\|`, "|")
				}
			default:
				walk(c, inCell)
			}
			c = next
		}
	}
	walk(doc, false)
}

// convertInline は 1 つのインラインノード c を独自ノードへ置き換える。
func convertInline(parent, c ast.Node, src []byte) {
	switch n := c.(type) {
	case *ast.Text:
		nodes := convertText(n, src)
		for _, x := range nodes {
			parent.InsertBefore(parent, c, x)
		}
		parent.RemoveChild(parent, c)
	case *ast.String:
		parent.ReplaceChild(parent, c, &Str{Value: string(n.Value)})
	case *ast.CodeSpan:
		var sb strings.Builder
		block := parent
		for block != nil && block.Type() != ast.TypeBlock {
			block = block.Parent()
		}
		prevStop := -1
		for t := n.FirstChild(); t != nil; t = t.NextSibling() {
			if tt, ok := t.(*ast.Text); ok {
				v := tt.Segment.Value(src)
				if block != nil && prevStop > 0 && bytes.IndexByte(src[prevStop-1:tt.Segment.Start], '\n') >= 0 {
					// 怠惰な継続行の行頭空白は comrak ではコードスパンに残る
					sb.WriteString(lazyLineIndent(block, tt.Segment.Start, src))
				}
				sb.Write(v)
				prevStop = tt.Segment.Stop
			}
		}
		if block != nil && prevStop > 0 && src[prevStop-1] == '\n' {
			// 閉じのバッククォートが次の行の先頭にある場合
			if i := bytes.IndexByte(src[prevStop:], '`'); i >= 0 {
				sb.WriteString(lazyLineIndent(block, prevStop+i, src))
			}
		}
		lit := strings.ReplaceAll(sb.String(), "\r\n", " ")
		lit = strings.ReplaceAll(lit, "\n", " ")
		if _, isCell := block.(*east.TableCell); block != nil && !isCell {
			// 表のセル以外は原文から comrak と同じ規則で組み立てる
			// （セルは comrak がパイプのエスケープを外した内容を解析するため対象外）
			if l, ok := codeSpanLiteral(n, block, src); ok {
				lit = l
			}
		}
		parent.ReplaceChild(parent, c, &Code{Literal: lit})
	case *ast.RawHTML:
		var sb strings.Builder
		for i := 0; i < n.Segments.Len(); i++ {
			s := n.Segments.At(i)
			sb.Write(s.Value(src))
		}
		parent.ReplaceChild(parent, c, &HTMLInline{Literal: sb.String()})
	case *ast.Link:
		l := &Link{URL: cleanURL(n.Destination), Title: cleanTitle(n.Title)}
		moveChildren(n, l, src)
		parent.ReplaceChild(parent, c, l)
	case *ast.Image:
		img := &Image{URL: cleanURL(n.Destination), Title: cleanTitle(n.Title)}
		moveChildren(n, img, src)
		parent.ReplaceChild(parent, c, img)
	case *ast.AutoLink:
		label := string(n.Label(src))
		url := unescapeHTML(strings.Trim(label, " \t\n\v\f\r"))
		if n.AutoLinkType == ast.AutoLinkEmail {
			url = "mailto:" + url
		}
		l := &Link{URL: url}
		l.AppendChild(l, &Str{Value: unescapeHTML(label)})
		parent.ReplaceChild(parent, c, l)
	case *Link, *Image:
		// 自動リンクで生成済み
	default:
		// Emphasis, Strikethrough など: 子を変換する
		for cc := c.FirstChild(); cc != nil; {
			next := cc.NextSibling()
			convertInline(c, cc, src)
			cc = next
		}
	}
}

func moveChildren(from, to ast.Node, src []byte) {
	for cc := from.FirstChild(); cc != nil; {
		next := cc.NextSibling()
		to.AppendChild(to, cc)
		cc = next
	}
	for cc := to.FirstChild(); cc != nil; {
		next := cc.NextSibling()
		convertInline(to, cc, src)
		cc = next
	}
}

// convertText は Text ノードをエスケープ・実体参照・改行で分割する。
func convertText(t *ast.Text, src []byte) []ast.Node {
	v := t.Segment.Value(src)
	var out []ast.Node
	if t.IsRaw() {
		out = append(out, &Str{Value: string(v)})
	} else {
		var sb strings.Builder
		flush := func() {
			if sb.Len() > 0 {
				out = append(out, &Str{Value: sb.String()})
				sb.Reset()
			}
		}
		for i := 0; i < len(v); {
			c := v[i]
			if c == '\\' && i+1 < len(v) && isPunct(v[i+1]) {
				flush()
				e := &Escaped{}
				e.AppendChild(e, &Str{Value: string(v[i+1])})
				out = append(out, e)
				i += 2
				continue
			}
			if c == '&' {
				if s, n := decodeEntity(v[i:]); n > 0 {
					sb.WriteString(s)
					i += n
					continue
				}
			}
			sb.WriteByte(c)
			i++
		}
		flush()
	}
	if t.HardLineBreak() {
		out = append(out, &HardBreak{})
	} else if t.SoftLineBreak() {
		out = append(out, &SoftBreak{})
	}
	return out
}

// decodeEntity は先頭の文字参照を解決する（comrak の entity::unescape と同等）。
func decodeEntity(b []byte) (string, int) {
	if len(b) < 3 || b[0] != '&' {
		return "", 0
	}
	if b[1] == '#' {
		i := 2
		var cp int
		digits := 0
		if i < len(b) && (b[i] == 'x' || b[i] == 'X') {
			i++
			for i < len(b) && digits < 7 && isHex(b[i]) {
				cp = cp*16 + hexVal(b[i])
				i++
				digits++
			}
			if digits == 0 || digits > 6 {
				return "", 0
			}
		} else {
			for i < len(b) && digits < 8 && b[i] >= '0' && b[i] <= '9' {
				cp = cp*10 + int(b[i]-'0')
				i++
				digits++
			}
			if digits == 0 || digits > 7 {
				return "", 0
			}
		}
		if i >= len(b) || b[i] != ';' {
			return "", 0
		}
		if cp == 0 || cp > 0x10FFFF || (cp >= 0xD800 && cp <= 0xE000) {
			cp = 0xFFFD
		}
		return string(rune(cp)), i + 1
	}
	i := 1
	for i < len(b) && i < 40 && isAlnum(b[i]) {
		i++
	}
	if i == 1 || i >= len(b) || b[i] != ';' {
		return "", 0
	}
	if e, ok := util.LookUpHTML5EntityByName(string(b[1:i])); ok {
		return string(e.Characters), i + 1
	}
	return "", 0
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	}
	return int(c-'A') + 10
}

// unescapeHTML は文字列中の文字参照をすべて解決する。
func unescapeHTML(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	b := []byte(s)
	var sb strings.Builder
	for i := 0; i < len(b); {
		if b[i] == '&' {
			if r, n := decodeEntity(b[i:]); n > 0 {
				sb.WriteString(r)
				i += n
				continue
			}
		}
		sb.WriteByte(b[i])
		i++
	}
	return sb.String()
}

// unescapeBackslash は comrak の strings::unescape（バックスラッシュエスケープの除去）。
func unescapeBackslash(v []byte) []byte {
	out := make([]byte, 0, len(v))
	for i := 0; i < len(v); i++ {
		if v[i] == '\\' && i+1 < len(v) && isPunct(v[i+1]) {
			out = append(out, v[i+1])
			i++
			continue
		}
		out = append(out, v[i])
	}
	return out
}

func cleanURL(dest []byte) string {
	s := strings.Trim(string(dest), " \t\n\v\f\r")
	if s == "" {
		return ""
	}
	return string(unescapeBackslash([]byte(unescapeHTML(s))))
}

func cleanTitle(title []byte) string {
	if len(title) == 0 {
		return ""
	}
	return string(unescapeBackslash([]byte(unescapeHTML(string(title)))))
}

// normalizeLabel は comrak の normalize_label(Case::Fold)。
func normalizeLabel(s string) string {
	return util.ToLinkReference([]byte(s))
}

// ---- 脚注 ----

func processFootnotes(doc *ast.Document) {
	defs := map[string]*FootnoteDef{}
	var defOrder []*FootnoteDef
	var findDefs func(ast.Node)
	findDefs = func(n ast.Node) {
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			if d, ok := c.(*FootnoteDef); ok {
				defs[normalizeLabel(d.Name)] = d
				defOrder = append(defOrder, d)
				continue
			}
			findDefs(c)
		}
	}
	findDefs(doc)

	ix := 0
	var findRefs func(ast.Node)
	findRefs = func(n ast.Node) {
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			if r, ok := c.(*FootnoteRef); ok {
				d := defs[normalizeLabel(r.Name)]
				if d == nil {
					s := &Str{Value: "[^" + r.Name + "]"}
					n.ReplaceChild(n, c, s)
					c = s
					continue
				}
				if d.Ix == 0 {
					ix++
					d.Ix = ix
				}
				d.TotalRefs++
				r.RefNum = d.TotalRefs
				r.Ix = d.Ix
				r.Name = d.Name
				continue
			}
			findRefs(c)
		}
	}
	findRefs(doc)

	if len(defs) > 0 {
		for _, d := range defOrder {
			if p := d.Parent(); p != nil {
				p.RemoveChild(p, d)
			}
		}
	}
	if ix > 0 {
		used := make([]*FootnoteDef, 0, len(defs))
		for _, d := range defs {
			if d.Ix > 0 {
				used = append(used, d)
			}
		}
		for i := 1; i <= ix; i++ {
			for _, d := range used {
				if d.Ix == i {
					doc.AppendChild(doc, d)
				}
			}
		}
	}
}

// ---- テキスト後処理（comrak の postprocess_text_nodes） ----

func postprocessText(node ast.Node, inBracket bool) {
	for c := node.FirstChild(); c != nil; {
		if s, ok := c.(*Str); ok {
			for {
				ns, ok := s.NextSibling().(*Str)
				if !ok {
					break
				}
				s.Value += ns.Value
				node.RemoveChild(node, ns)
			}
			processTasklist(s)
			if !inBracket {
				processEmailAutolinks(s)
			}
			next := s.NextSibling()
			if s.Value == "" && s.Parent() != nil {
				s.Parent().RemoveChild(s.Parent(), s)
			}
			c = next
			continue
		}
		switch c.(type) {
		case *Link, *Image:
			postprocessText(c, true)
		default:
			postprocessText(c, inBracket)
		}
		c = c.NextSibling()
	}
}

func isParagraphLike(n ast.Node) bool {
	switch n.(type) {
	case *ast.Paragraph, *ast.TextBlock:
		return true
	}
	return false
}

// processTasklist は comrak の process_tasklist。
func processTasklist(s *Str) {
	end, symbol, ok := scanTasklist(s.Value)
	if !ok || (symbol != ' ' && symbol != 'x' && symbol != 'X') {
		return
	}
	parent := s.Parent()
	if s.PreviousSibling() != nil || parent == nil || parent.PreviousSibling() != nil {
		return
	}
	if !isParagraphLike(parent) {
		return
	}
	item, ok := parent.Parent().(*ast.ListItem)
	if !ok {
		return
	}
	list, ok := item.Parent().(*ast.List)
	if !ok {
		return
	}
	s.Value = s.Value[end:]
	if s.Value == "" && s.NextSibling() == nil {
		item.RemoveChild(item, parent)
	}
	item.SetAttributeString("cm-task", []byte{symbol})
	list.SetAttributeString("cm-tasklist", true)
}

// scanTasklist は comrak の scanners::tasklist。
// spacechar* "[" 任意の 1 文字 "]" (spacechar | 終端)
func scanTasklist(s string) (end int, symbol byte, ok bool) {
	i := 0
	for i < len(s) && isSpace(s[i]) {
		i++
	}
	if i >= len(s) || s[i] != '[' {
		return 0, 0, false
	}
	i++
	if i >= len(s) {
		return 0, 0, false
	}
	r, l := utf8.DecodeRuneInString(s[i:])
	if r == 0 || r == '\r' || r == '\n' {
		return 0, 0, false
	}
	symbol = s[i]
	i += l
	if i >= len(s) || s[i] != ']' {
		return 0, 0, false
	}
	i++
	if i == len(s) {
		return i, symbol, true
	}
	if isSpace(s[i]) || s[i] == 0 {
		return i + 1, symbol, true
	}
	return 0, 0, false
}

// ---- 自動リンク補助（comrak の parser/autolink.rs） ----

// checkDomain は comrak の check_domain。
func checkDomain(data []byte, allowShort bool) (int, bool) {
	np, uscore1, uscore2 := 0, 0, 0
	for i := 0; i < len(data); {
		r, l := utf8.DecodeRune(data[i:])
		switch {
		case r == '\\' && i < len(data)-1:
			// エスケープ文字は無視
		case r == '_':
			uscore2++
		case r == '.':
			uscore1 = uscore2
			uscore2 = 0
			np++
		case !isValidHostchar(r) && r != '-':
			if uscore1 == 0 && uscore2 == 0 && (allowShort || np > 0) {
				return i, true
			}
			return 0, false
		}
		i += l
	}
	if (uscore1 > 0 || uscore2 > 0) && np <= 10 {
		return 0, false
	}
	if allowShort || np > 0 {
		return len(data), true
	}
	return 0, false
}

// isValidHostchar は Rust の !(is_whitespace || is_punctuation || is_symbol)。
func isValidHostchar(r rune) bool {
	if unicode.IsSpace(r) || r == 0x85 || r == 0x1680 || (r >= 0x2000 && r <= 0x200A) ||
		r == 0x2028 || r == 0x2029 || r == 0x202F || r == 0x205F || r == 0x3000 {
		return false
	}
	return !unicode.IsPunct(r) && !unicode.IsSymbol(r)
}

// autolinkDelim は comrak の autolink_delim（末尾の句読点等を取り除く）。
func autolinkDelim(data []byte, linkEnd int) int {
	for i := 0; i < linkEnd && i < len(data); i++ {
		if data[i] == '<' {
			linkEnd = i
			break
		}
	}
	for linkEnd > 0 {
		cclose := data[linkEnd-1]
		switch {
		case strings.IndexByte("?!.,:*_~'\"", cclose) >= 0:
			linkEnd--
		case cclose == ';':
			newEnd := linkEnd - 2
			for newEnd > 0 && isAlpha(data[newEnd]) {
				newEnd--
			}
			if newEnd < linkEnd-2 && data[newEnd] == '&' {
				linkEnd = newEnd
			} else {
				linkEnd--
			}
		case cclose == ')':
			opening, closing := 0, 0
			for _, b := range data[:linkEnd] {
				if b == '(' {
					opening++
				} else if b == ')' {
					closing++
				}
			}
			if closing <= opening {
				return linkEnd
			}
			linkEnd--
		default:
			return linkEnd
		}
	}
	return linkEnd
}

// processEmailAutolinks は comrak の process_email_autolinks。
func processEmailAutolinks(s *Str) {
	contents := []byte(s.Value)
	n := len(contents)
	i := 0
	for i < n {
		bracketOpening := 0
		var post *Link
		var reverse, skip int
		for i < n {
			switch contents[i] {
			case '[':
				bracketOpening++
			case ']':
				bracketOpening--
			}
			if bracketOpening > 0 {
				i++
				continue
			}
			if contents[i] == '@' {
				if l, rv, sk, ok := emailMatch(contents, i); ok {
					post, reverse, skip = l, rv, sk
					break
				}
			}
			i++
		}
		if post == nil {
			return
		}
		i -= reverse
		parent := s.Parent()
		parent.InsertAfter(parent, s, post)
		remain := ""
		if i+skip < n {
			remain = string(contents[i+skip:])
		}
		s.Value = string(contents[:i])
		if remain != "" {
			after := &Str{Value: remain}
			parent.InsertAfter(parent, post, after)
			processEmailAutolinks(after)
		}
		return
	}
}

func emailMatch(contents []byte, i int) (*Link, int, int, bool) {
	size := len(contents)
	autoMailto := true
	isXMPP := false
	rewind := 0
	for rewind < i {
		c := contents[i-rewind-1]
		if isAlnum(c) || c == '.' || c == '+' || c == '-' || c == '_' {
			rewind++
			continue
		}
		if c == ':' {
			if validateProtocol("mailto", contents, i-rewind-1) {
				autoMailto = false
				rewind++
				continue
			}
			if validateProtocol("xmpp", contents, i-rewind-1) {
				isXMPP = true
				autoMailto = false
				rewind++
				continue
			}
		}
		break
	}
	if rewind == 0 {
		return nil, 0, 0, false
	}
	linkEnd := 1
	np := 0
	for linkEnd < size-i {
		c := contents[i+linkEnd]
		if isAlnum(c) {
			// 何もしない
		} else if c == '@' {
			return nil, 0, 0, false
		} else if c == '.' && linkEnd < size-i-1 && isAlnum(contents[i+linkEnd+1]) {
			np++
		} else if c == '/' && isXMPP {
			// xmpp は '/' を許す
		} else if c != '-' && c != '_' {
			break
		}
		linkEnd++
	}
	if linkEnd < 2 || np == 0 || (!isAlpha(contents[i+linkEnd-1]) && contents[i+linkEnd-1] != '.') {
		return nil, 0, 0, false
	}
	linkEnd = autolinkDelim(contents[i:], linkEnd)
	if linkEnd == 0 {
		return nil, 0, 0, false
	}
	t := string(contents[i-rewind : linkEnd+i])
	url := t
	if autoMailto {
		url = "mailto:" + t
	}
	l := &Link{URL: url}
	l.AppendChild(l, &Str{Value: t})
	return l, rewind, rewind + linkEnd, true
}

func validateProtocol(protocol string, contents []byte, cursor int) bool {
	size := len(contents)
	rewind := 0
	for rewind < cursor && isAlpha(contents[cursor-rewind-1]) {
		rewind++
	}
	return size-cursor+rewind >= len(protocol) && string(contents[cursor-rewind:cursor]) == protocol
}
