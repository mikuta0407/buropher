package commonmark

import (
	"bytes"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// refDefTransformer は goldmark のリンク参照定義抽出を comrak の判定に合わせて包む。
//
//   - 表の見出し行の前にあった段落（comrak の try_inserting_table_header_paragraph
//     で作られる段落）は finalize されないため、リンク参照定義を抽出しない。
//   - comrak（manual_scan_link_url）が無効とする定義（括弧の対応が取れない
//     リンク先など）以降は抽出しない。
type refDefTransformer struct{}

func (refDefTransformer) Transform(node *ast.Paragraph, reader text.Reader, pc parser.Context) {
	if v, ok := node.AttributeString("cm-no-refdefs"); ok && v == true {
		return
	}
	src := reader.Source()
	lines := node.Lines()
	var content []byte
	lineEnds := make([]int, lines.Len())
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		// comrak の段落内容は各行の先頭空白を除き、行末に改行を持つ
		v := bytes.TrimLeft(seg.Value(src), " \t")
		content = append(content, v...)
		if len(v) == 0 || v[len(v)-1] != '\n' {
			content = append(content, '\n')
		}
		lineEnds[i] = len(content)
	}
	// comrak が受理する定義が覆う行数を求める
	pos := 0
	for pos < len(content) && content[pos] == '[' {
		n, ok := scanReferenceDef(content[pos:])
		if !ok {
			break
		}
		pos += n
	}
	if pos == 0 {
		return
	}
	nlines := 0
	for nlines < len(lineEnds) && lineEnds[nlines] <= pos {
		nlines++
	}
	if nlines >= lines.Len() {
		parser.LinkReferenceParagraphTransformer.Transform(node, reader, pc)
		return
	}
	rest := lines.Sliced(nlines, lines.Len())
	parent, next := node.Parent(), node.NextSibling()
	lines.SetSliced(0, nlines)
	parser.LinkReferenceParagraphTransformer.Transform(node, reader, pc)
	if node.Parent() == nil {
		// 段落全体が定義だった場合は残りの行で段落を戻す
		node.Lines().Clear()
		if next != nil {
			parent.InsertBefore(parent, next, node)
		} else {
			parent.AppendChild(parent, node)
		}
		// 残りの先頭行が怠惰な継続行なら、comrak では行頭の空白が段落の
		// 先頭に残る（通常の段落先頭の空白はブロック解析で除かれている）
		// （goldmark はインライン解析で先頭の空白を除くため、属性で normalizer に伝える）
		if len(rest) > 0 {
			v := rest[0].Value(src)
			ws := len(v) - len(bytes.TrimLeft(v, " \t"))
			if ws > 0 && lazyLineIndent(node, rest[0].Start+ws, src) != "" {
				node.SetAttributeString("cm-lead-ws", string(v[:ws]))
			}
		}
	}
	node.Lines().AppendAll(rest)
}

// scanReferenceDef は comrak の parse_reference_inline の受理判定。
// 受理した場合は消費したバイト数を返す。
func scanReferenceDef(s []byte) (int, bool) {
	pos := 0
	// link_label
	if pos >= len(s) || s[pos] != '[' {
		return 0, false
	}
	pos++
	start := pos
	length := 0
	var c byte
	for pos < len(s) {
		c = s[pos]
		if c == '[' || c == ']' {
			break
		}
		if c == '\\' {
			pos++
			length++
			if pos < len(s) && isPunct(s[pos]) {
				pos++
				length++
			}
		} else {
			pos++
			length++
		}
		if length > 1000 {
			return 0, false
		}
	}
	if pos >= len(s) || s[pos] != ']' {
		return 0, false
	}
	blank := true
	for _, b := range s[start:pos] {
		if !isSpace(b) {
			blank = false
			break
		}
	}
	if blank {
		return 0, false
	}
	pos++
	if pos >= len(s) || s[pos] != ':' {
		return 0, false
	}
	pos++
	pos = spnl(s, pos)
	n, ok := manualScanLinkURL(s[pos:])
	if !ok {
		return 0, false
	}
	pos += n
	beforeTitle := pos
	pos = spnl(s, pos)
	hasTitle := false
	if pos != beforeTitle {
		if t, ok := scanLinkTitle(s[pos:]); ok {
			pos += t
			hasTitle = true
		} else {
			pos = beforeTitle
		}
	} else {
		pos = beforeTitle
	}
	pos = skipSpacesTabs(s, pos)
	if p, ok := skipLineEnd(s, pos); ok {
		return p, true
	}
	if !hasTitle {
		return 0, false
	}
	pos = skipSpacesTabs(s, beforeTitle)
	if p, ok := skipLineEnd(s, pos); ok {
		return p, true
	}
	return 0, false
}

func skipSpacesTabs(s []byte, pos int) int {
	for pos < len(s) && (s[pos] == ' ' || s[pos] == '\t') {
		pos++
	}
	return pos
}

func skipLineEnd(s []byte, pos int) (int, bool) {
	old := pos
	if pos < len(s) && s[pos] == '\r' {
		pos++
	}
	if pos < len(s) && s[pos] == '\n' {
		pos++
	}
	return pos, pos > old || pos >= len(s)
}

func spnl(s []byte, pos int) int {
	pos = skipSpacesTabs(s, pos)
	if p, ok := skipLineEnd(s, pos); ok {
		pos = skipSpacesTabs(s, p)
	}
	return pos
}

// manualScanLinkURL は comrak の manual_scan_link_url。
func manualScanLinkURL(s []byte) (int, bool) {
	i := 0
	if len(s) > 0 && s[0] == '<' {
		i++
		for i < len(s) {
			b := s[i]
			if b == '>' {
				i++
				break
			} else if b == '\\' {
				i += 2
			} else if b == '\n' || b == '<' {
				return 0, false
			} else {
				i++
			}
		}
		if i >= len(s) {
			return 0, false
		}
		return i, true
	}
	nb := 0
	for i < len(s) {
		b := s[i]
		if b == '\\' && i+1 < len(s) && isPunct(s[i+1]) {
			i += 2
		} else if b == '(' {
			nb++
			i++
			if nb > 32 {
				return 0, false
			}
		} else if b == ')' {
			if nb == 0 {
				break
			}
			nb--
			i++
		} else if isSpace(b) || b < 0x20 || b == 0x7f {
			if i == 0 {
				return 0, false
			}
			break
		} else {
			i++
		}
	}
	if i >= len(s) || nb != 0 {
		return 0, false
	}
	return i, true
}

// scanLinkTitle は comrak の scanners::link_title。
func scanLinkTitle(s []byte) (int, bool) {
	if len(s) == 0 {
		return 0, false
	}
	open := s[0]
	var close byte
	switch open {
	case '"', '\'':
		close = open
	case '(':
		close = ')'
	default:
		return 0, false
	}
	for i := 1; i < len(s); i++ {
		b := s[i]
		switch {
		case b == '\\' && i+1 < len(s) && isPunct(s[i+1]):
			i++
		case b == close:
			return i + 1, true
		case b == 0 || (open == '(' && b == '('):
			return 0, false
		}
	}
	return 0, false
}
