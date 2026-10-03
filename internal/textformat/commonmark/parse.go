// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package commonmark

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// newParser は comrak（Redmine の commonmarker 設定）に合わせた goldmark パーサを作る。
//
// 有効な拡張: table, strikethrough, tagfilter（描画時）, autolink, footnotes,
// tasklist（後処理）, alerts。escaped_char_spans は後処理で Escaped ノードにする。
func newParser() parser.Parser {
	return parser.NewParser(
		parser.WithBlockParsers(
			util.Prioritized(parser.NewSetextHeadingParser(), 100),
			util.Prioritized(parser.NewThematicBreakParser(), 200),
			util.Prioritized(parser.NewListParser(), 300),
			util.Prioritized(parser.NewListItemParser(), 400),
			util.Prioritized(&tableParser{}, 450),
			util.Prioritized(parser.NewCodeBlockParser(), 500),
			util.Prioritized(&footnoteDefParser{}, 550),
			util.Prioritized(parser.NewATXHeadingParser(), 600),
			util.Prioritized(parser.NewFencedCodeBlockParser(), 700),
			util.Prioritized(&alertParser{}, 790),
			util.Prioritized(parser.NewBlockquoteParser(), 800),
			util.Prioritized(newHTMLBlockParser(), 900),
			util.Prioritized(parser.NewParagraphParser(), 1000),
		),
		parser.WithInlineParsers(
			util.Prioritized(&bracketTracker{}, 1),
			util.Prioritized(&footnoteRefParser{}, 50),
			util.Prioritized(&codeSpanGuard{}, 99),
			util.Prioritized(parser.NewCodeSpanParser(), 100),
			util.Prioritized(parser.NewLinkParser(), 200),
			util.Prioritized(parser.NewAutoLinkParser(), 300),
			util.Prioritized(parser.NewRawHTMLParser(), 400),
			util.Prioritized(&emphasisParser{}, 500),
			util.Prioritized(&strikeParser{}, 500),
			util.Prioritized(&urlAutolinkParser{}, 600),
			util.Prioritized(&wwwAutolinkParser{}, 600),
		),
		parser.WithParagraphTransformers(util.Prioritized(refDefTransformer{}, 100)),
		parser.WithASTTransformers(
			util.Prioritized(&normalizer{}, 100),
		),
	)
}

// ---- 括弧の内側かどうかの追跡（comrak の within_brackets） ----

var bracketKey = parser.NewContextKey()

type bracketState struct {
	block  ast.Node
	within bool
}

func withinBrackets(parent ast.Node, pc parser.Context) bool {
	st, _ := pc.Get(bracketKey).(*bracketState)
	return st != nil && st.block == parent && st.within
}

// bracketTracker は '[' / '![' / ']' を観測して within_brackets を更新する（何も消費しない）。
type bracketTracker struct{}

func (b *bracketTracker) Trigger() []byte { return []byte{'[', ']', '!'} }

func (b *bracketTracker) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	st, _ := pc.Get(bracketKey).(*bracketState)
	if st == nil || st.block != parent {
		st = &bracketState{block: parent}
		pc.Set(bracketKey, st)
	}
	line, _ := block.PeekLine()
	switch line[0] {
	case '[':
		st.within = true
	case ']':
		st.within = false
	case '!':
		if len(line) > 1 && line[1] == '[' && (len(line) < 3 || line[2] != '^') {
			st.within = true
		}
	}
	return nil
}

// ---- URL 自動リンク（comrak の autolink::url_match、':' で起動） ----

type urlAutolinkParser struct{}

func (p *urlAutolinkParser) Trigger() []byte { return []byte{':'} }

func (p *urlAutolinkParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	if withinBrackets(parent, pc) {
		return nil
	}
	line, seg := block.PeekLine()
	if len(line) < 4 || line[1] != '/' || line[2] != '/' {
		return nil
	}
	source := block.Source()
	// ':' の直前の英字列（スキーム）
	rewind := 0
	for seg.Start-rewind-1 >= 0 && isAlpha(source[seg.Start-rewind-1]) {
		rewind++
	}
	scheme := string(source[seg.Start-rewind : seg.Start])
	if scheme != "http" && scheme != "https" && scheme != "ftp" {
		return nil
	}
	// スキーム部分は直前のテキストノードの末尾にあるはず
	if !canRewindText(parent, source, seg.Start, rewind) {
		return nil
	}
	linkEnd, ok := checkDomain(line[3:], true)
	if !ok {
		return nil
	}
	for linkEnd < len(line) && !isSpace(line[linkEnd]) {
		linkEnd++
	}
	linkEnd = autolinkDelim(line, linkEnd)
	rewindText(parent, source, seg.Start, rewind)
	url := scheme + string(line[:linkEnd])
	block.Advance(linkEnd)
	link := &Link{URL: url}
	link.AppendChild(link, &Str{Value: url})
	return link
}

// canRewindText は parent の末尾のテキストノードが pos で終わり、rewind バイト以上あるか調べる。
func canRewindText(parent ast.Node, source []byte, pos, rewind int) bool {
	need := rewind
	end := pos
	for c := parent.LastChild(); need > 0; c = c.PreviousSibling() {
		t, ok := c.(*ast.Text)
		if !ok || t.IsRaw() || t.Segment.Stop != end || t.SoftLineBreak() || t.HardLineBreak() {
			return false
		}
		l := t.Segment.Len()
		if l >= need {
			return true
		}
		need -= l
		end = t.Segment.Start
	}
	return true
}

func rewindText(parent ast.Node, source []byte, pos, rewind int) {
	for rewind > 0 {
		t := parent.LastChild().(*ast.Text)
		l := t.Segment.Len()
		if rewind < l {
			t.Segment = t.Segment.WithStop(t.Segment.Stop - rewind)
			return
		}
		rewind -= l
		parent.RemoveChild(parent, t)
	}
}

// ---- www 自動リンク（comrak の autolink::www_match） ----

// wwwAutolinkParser は空白（行頭を含む）と '(' の直後の "www." を処理する。
// '*' '_' '~' の直後のものは正規化時に処理する。
type wwwAutolinkParser struct{}

func (p *wwwAutolinkParser) Trigger() []byte { return []byte{' ', '(', '\\'} }

func (p *wwwAutolinkParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	if withinBrackets(parent, pc) {
		return nil
	}
	line, seg := block.PeekLine()
	consume := 0
	switch {
	case isSpace(line[0]) || line[0] == '(':
		consume = 1
	case line[0] == '\\':
		// comrak は直前の生の文字を見るため、"\*www." なども対象になる
		if len(line) < 2 || bytes.IndexByte([]byte("*_~(["), line[1]) < 0 {
			return nil
		}
		consume = 2
	case line[0] == 'w':
		// 行頭、または強調等の区切り（* _ ~）の直後のみ（comrak の WWW_DELIMS）
		l, pos := block.Position()
		lines := parent.Lines()
		lineHead := lines != nil && l < lines.Len() && pos.Start == lines.At(l).Start
		if !lineHead {
			prev := block.PrecendingCharacter()
			if prev != '*' && prev != '_' && prev != '~' {
				return nil
			}
		}
	default:
		return nil
	}
	rest := line[consume:]
	if !bytes.HasPrefix(rest, []byte("www.")) {
		return nil
	}
	linkEnd, ok := wwwMatch(rest)
	if !ok {
		return nil
	}
	if consume > 0 {
		ast.MergeOrAppendTextSegment(parent, seg.WithStop(seg.Start+consume))
	}
	block.Advance(consume + linkEnd)
	t := string(rest[:linkEnd])
	link := &Link{URL: "http://" + t}
	link.AppendChild(link, &Str{Value: t})
	return link
}

// wwwMatch は data（"www." で始まる）からリンク末尾を求める。
func wwwMatch(data []byte) (int, bool) {
	linkEnd, ok := checkDomain(data, false)
	if !ok {
		return 0, false
	}
	for linkEnd < len(data) && !isSpace(data[linkEnd]) {
		linkEnd++
	}
	linkEnd = autolinkDelim(data, linkEnd)
	return linkEnd, true
}

// ---- アラート（comrak の handle_alert） ----

var reAlertStart = regexp.MustCompile(`(?i)^>+ \[!(note|tip|important|warning|caution)\]`)

type alertParser struct{}

func (p *alertParser) Trigger() []byte { return []byte{'>'} }

func (p *alertParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, _ := reader.PeekLine()
	pos := pc.BlockOffset()
	if pos < 0 || pc.BlockIndent() >= 4 {
		return nil, parser.NoChildren
	}
	l := line[pos:]
	m := reAlertStart.FindSubmatchIndex(l)
	if m == nil {
		return nil, parser.NoChildren
	}
	// ']' までの '>' の数（fence_length）
	closeIdx := bytes.IndexByte(l, ']')
	if bytes.Count(l[:closeIdx], []byte{'>'}) != 1 {
		return nil, parser.NoChildren
	}
	title := string(l[closeIdx+1:])
	title = strings.TrimRight(title, "\n")
	title = string(unescapeBackslash([]byte(strings.Trim(unescapeHTML(title), " \t\n\v\f\r"))))
	node := &Alert{AlertType: strings.ToLower(string(l[m[2]:m[3]]))}
	if title != "" {
		node.Title = title
		node.HasTitle = true
	}
	reader.AdvanceToEOL()
	return node, parser.HasChildren
}

func (p *alertParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	line, _ := reader.PeekLine()
	w, pos := util.IndentWidth(line, reader.LineOffset())
	if w > 3 || pos >= len(line) || line[pos] != '>' {
		return parser.Close
	}
	pos++
	if pos >= len(line) || line[pos] == '\n' {
		reader.Advance(pos)
		return parser.Continue | parser.HasChildren
	}
	reader.Advance(pos)
	if line[pos] == ' ' || line[pos] == '\t' {
		padding := 0
		if line[pos] == '\t' {
			padding = util.TabWidth(reader.LineOffset()) - 1
		}
		reader.AdvanceAndSetPadding(1, padding)
	}
	return parser.Continue | parser.HasChildren
}

func (p *alertParser) Close(node ast.Node, reader text.Reader, pc parser.Context) {}

func (p *alertParser) CanInterruptParagraph() bool { return true }

func (p *alertParser) CanAcceptIndentedLine() bool { return false }

// ---- 脚注定義（comrak の handle_footnote） ----

var (
	reFootnoteDef = regexp.MustCompile(`^\[\^([^\] \r\n\x00\t]+)\]:[ \t]*`)
	footnoteNames = parser.NewContextKey()
)

type footnoteDefParser struct{}

func (p *footnoteDefParser) Trigger() []byte { return []byte{'['} }

func (p *footnoteDefParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, _ := reader.PeekLine()
	pos := pc.BlockOffset()
	if pos < 0 {
		return nil, parser.NoChildren
	}
	m := reFootnoteDef.FindSubmatchIndex(line[pos:])
	if m == nil {
		return nil, parser.NoChildren
	}
	name := string(line[pos+m[2] : pos+m[3]])
	names, _ := pc.Get(footnoteNames).(map[string]bool)
	if names == nil {
		names = map[string]bool{}
		pc.Set(footnoteNames, names)
	}
	names[normalizeLabel(name)] = true
	reader.Advance(pos + m[1])
	return &FootnoteDef{Name: name}, parser.HasChildren
}

func (p *footnoteDefParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	line, _ := reader.PeekLine()
	if string(line) == "\n" || string(line) == "\r\n" {
		return parser.Continue | parser.HasChildren
	}
	w, _ := util.IndentWidth(line, reader.LineOffset())
	if w >= 4 {
		pos, padding := util.IndentPosition(line, reader.LineOffset(), 4)
		if pos < 0 {
			return parser.Close
		}
		reader.AdvanceAndSetPadding(pos, padding)
		return parser.Continue | parser.HasChildren
	}
	return parser.Close
}

func (p *footnoteDefParser) Close(node ast.Node, reader text.Reader, pc parser.Context) {}

func (p *footnoteDefParser) CanInterruptParagraph() bool { return true }

func (p *footnoteDefParser) CanAcceptIndentedLine() bool { return false }

// ---- 脚注参照（comrak の handle_close_bracket の脚注部分の近似） ----

type footnoteRefParser struct{}

func (p *footnoteRefParser) Trigger() []byte { return []byte{'[', '!'} }

func (p *footnoteRefParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	line, seg := block.PeekLine()
	if len(line) > 0 && line[0] == '!' {
		// "![^" は画像ではない（comrak）。'!' を文字列にして脚注参照として扱う
		if len(line) < 5 || line[1] != '[' || line[2] != '^' {
			return nil
		}
		if p.lookahead(line[1:], pc, block, 1) == nil {
			return nil
		}
		ast.MergeOrAppendTextSegment(parent, seg.WithStop(seg.Start+1))
		block.Advance(1)
		line, _ = block.PeekLine()
	}
	n := p.lookahead(line, pc, block, 0)
	if n == nil {
		return nil
	}
	block.Advance(bytes.IndexByte(line, ']') + 1)
	// ']' を消費したので within_brackets を解除する
	if st, _ := pc.Get(bracketKey).(*bracketState); st != nil && st.block == parent {
		st.within = false
	}
	return n
}

// lookahead は line（"[^" で始まる）が脚注参照として扱われる場合そのノードを返す（位置は進めない）。
func (p *footnoteRefParser) lookahead(line []byte, pc parser.Context, block text.Reader, base int) ast.Node {
	if len(line) < 4 || line[0] != '[' || line[1] != '^' {
		return nil
	}
	end := bytes.IndexByte(line, ']')
	if end < 0 {
		return nil
	}
	content := line[2:end]
	if len(content) == 0 || bytes.ContainsAny(content, "`<&\\[\n\r") {
		return nil
	}
	if end+1 < len(line) && line[end+1] == '(' && inlineLinkFollows(block, base+end+1) {
		return nil
	}
	if end+1 < len(line) && line[end+1] == '[' {
		// 参照リンク [^x][label] / [^x][]
		if e2 := bytes.IndexByte(line[end+1:], ']'); e2 > 0 {
			label := line[end+2 : end+1+e2]
			if len(bytes.TrimSpace(label)) == 0 {
				label = line[1:end]
			}
			if _, ok := pc.Reference(util.ToLinkReference(label)); ok {
				return nil
			}
		}
	}
	if _, ok := pc.Reference(util.ToLinkReference(line[1:end])); ok {
		return nil
	}
	name := string(content)
	names, _ := pc.Get(footnoteNames).(map[string]bool)
	if names[normalizeLabel(name)] {
		return &FootnoteRef{Name: name}
	}
	return &Str{Value: "[^" + name + "]"}
}

// ---- 表（comrak の parser/table.rs） ----

var reTableStart = regexp.MustCompile(`^\|?[ \t\v\f]*:?-+:?[ \t\v\f]*(?:\|[ \t\v\f]*:?-+:?[ \t\v\f]*)*\|?[ \t\v\f]*\r?\n$`)

type tableParser struct{}

func (p *tableParser) Trigger() []byte { return []byte{'|', ':', '-'} }

var tableVisitedKey = parser.NewContextKey()

// tableCell は row() で得たセル（unescape_pipes 前の区間）。
type tableCell struct {
	start, stop int // 元の行内の位置（trim 済み）
}

// parseTableRow は comrak の row()。line は改行で終わる。
// 複数行（段落）の場合、最後の行のセルと段落部分の長さ（paragraphOffset）を返す。
func parseTableRow(line []byte) (cells []tableCell, paragraphOffset int, ok bool) {
	n := len(line)
	offset := tableCellEnd(line)
	expectMore := true
	for offset < n && expectMore {
		cellMatched := tableCellLen(line[offset:])
		pipeMatched := tableCellEnd(line[offset+cellMatched:])
		if cellMatched > 0 || pipeMatched > 0 {
			s, e := offset, offset+cellMatched
			for s < e && isSpace(line[s]) {
				s++
			}
			for e > s && isSpace(line[e-1]) {
				e--
			}
			cells = append(cells, tableCell{start: s, stop: e})
		}
		offset += cellMatched + pipeMatched
		if pipeMatched > 0 {
			expectMore = true
		} else {
			rowEnd := tableRowEnd(line[offset:])
			offset += rowEnd
			if rowEnd > 0 && offset != n {
				paragraphOffset = offset
				cells = cells[:0]
				offset += tableCellEnd(line[offset:])
				expectMore = true
			} else {
				expectMore = false
			}
		}
	}
	if offset != n || len(cells) == 0 {
		return nil, 0, false
	}
	return cells, paragraphOffset, true
}

func isTableSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\v' || c == '\f' }

// tableCellEnd は "[|] table_spacechar*" に一致した長さ。
func tableCellEnd(s []byte) int {
	if len(s) == 0 || s[0] != '|' {
		return 0
	}
	i := 1
	for i < len(s) && isTableSpace(s[i]) {
		i++
	}
	return i
}

// tableRowEnd は "table_spacechar* table_newline" に一致した長さ。
func tableRowEnd(s []byte) int {
	i := 0
	for i < len(s) && isTableSpace(s[i]) {
		i++
	}
	if i < len(s) && s[i] == '\r' {
		i++
	}
	if i < len(s) && s[i] == '\n' {
		return i + 1
	}
	return 0
}

// tableCellLen は "(escaped_char|[^\x00|\r\n])+" に一致した長さ。
func tableCellLen(s []byte) int {
	i := 0
	for i < len(s) {
		c := s[i]
		if c == 0 || c == '\r' || c == '\n' {
			break
		}
		// '|' は直前がバックスラッシュの場合のみセルに含まれる（re2c の最長一致）
		if c == '|' && (i == 0 || s[i-1] != '\\') {
			break
		}
		i++
	}
	return i
}

// cellSegments はセル内容の区間を返す。comrak はインライン解析前に "\|" の
// バックスラッシュを取り除くが、ここでは元の区間のまま解析し、正規化時に
// "\|" 由来のエスケープを通常の "|" に戻す（fixTableCellPipes）。
func cellSegments(line []byte, base int, c tableCell) *text.Segments {
	segs := text.NewSegments()
	if c.stop > c.start {
		segs.Append(text.NewSegment(base+c.start, base+c.stop))
	}
	return segs
}

func lineWithNewline(line []byte) []byte {
	if len(line) == 0 || line[len(line)-1] != '\n' {
		return append(append([]byte(nil), line...), '\n')
	}
	return line
}

func (p *tableParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	last := pc.LastOpenedBlock()
	para, ok := last.Node.(*ast.Paragraph)
	if !ok || para.Parent() != parent || pc.BlockIndent() >= 4 || pc.BlockOffset() < 0 {
		return nil, parser.NoChildren
	}
	if visited, _ := pc.Get(tableVisitedKey).(map[ast.Node]bool); visited[para] {
		return nil, parser.NoChildren
	}
	line, seg := reader.PeekLine()
	pos := pc.BlockOffset()
	dline := lineWithNewline(line[pos:])
	if !reTableStart.Match(dline) {
		return nil, parser.NoChildren
	}
	markVisited := func() {
		visited, _ := pc.Get(tableVisitedKey).(map[ast.Node]bool)
		if visited == nil {
			visited = map[ast.Node]bool{}
			pc.Set(tableVisitedKey, visited)
		}
		visited[para] = true
	}
	delimCells, _, ok := parseTableRow(dline)
	if !ok {
		markVisited()
		return nil, parser.NoChildren
	}
	lines := para.Lines()
	if lines.Len() == 0 {
		return nil, parser.NoChildren
	}
	source := reader.Source()
	hseg := lines.At(lines.Len() - 1)
	hline := lineWithNewline(source[hseg.Start:hseg.Stop])
	for len(hline) > 0 && (hline[0] == ' ' || hline[0] == '\t') {
		hline = hline[1:]
		hseg.Start++
	}
	headerCells, _, ok := parseTableRow(hline)
	if !ok || len(headerCells) != len(delimCells) {
		markVisited()
		return nil, parser.NoChildren
	}
	var aligns []east.Alignment
	for _, c := range delimCells {
		cell := dline[c.start:c.stop]
		left := len(cell) > 0 && cell[0] == ':'
		right := len(cell) > 0 && cell[len(cell)-1] == ':'
		switch {
		case left && right:
			aligns = append(aligns, east.AlignCenter)
		case left:
			aligns = append(aligns, east.AlignLeft)
		case right:
			aligns = append(aligns, east.AlignRight)
		default:
			aligns = append(aligns, east.AlignNone)
		}
	}
	table := east.NewTable()
	table.Alignments = aligns
	header := east.NewTableHeader(east.NewTableRow(aligns))
	for i, c := range headerCells {
		cell := east.NewTableCell()
		cell.Alignment = aligns[i]
		cell.SetLines(cellSegments(hline, hseg.Start, c))
		header.AppendChild(header, cell)
	}
	table.AppendChild(table, header)
	// 見出し行を段落から取り除く（段落が空になれば goldmark が Close で削除する）
	if lines.Len() == 1 && para.HasBlankPreviousLines() {
		// 段落の直前の空行を表に引き継ぐ（リストの tight 判定用。Close で反映する）
		table.SetAttributeString("cm-blank-before", true)
	}
	if lines.Len() > 1 {
		// comrak では見出し行より前の部分は finalize されない段落になる（refdef.go）
		para.SetAttributeString("cm-no-refdefs", true)
	}
	lines.SetSliced(0, lines.Len()-1)
	_ = seg
	reader.AdvanceToEOL()
	return table, parser.NoChildren
}

var reOtherBlockStart = regexp.MustCompile(`^(?:>|#{1,6}(?:[ \t]|\r?\n|$)|` + "```" + `|~~~|<|\[\^[^\] \r\n\x00\t]+\]:|(?:[-+*]|[0-9]{1,9}[.)])(?:[ \t]|\r?\n|$))`)

var reThematicBreak = regexp.MustCompile(`^(?:(?:\*[ \t]*){3,}|(?:-[ \t]*){3,}|(?:_[ \t]*){3,})\r?\n?$`)

func (p *tableParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	line, seg := reader.PeekLine()
	if line == nil || util.IsBlank(line) {
		return parser.Close
	}
	w, pos := util.IndentWidth(line, reader.LineOffset())
	if w >= 4 {
		return parser.Close
	}
	rest := line[pos:]
	if reThematicBreak.Match(rest) || (reOtherBlockStart.Match(rest) && startsOtherBlock(rest)) {
		return parser.Close
	}
	rline := lineWithNewline(rest)
	cells, _, ok := parseTableRow(rline)
	if !ok {
		return parser.Close
	}
	table := node.(*east.Table)
	row := east.NewTableRow(table.Alignments)
	for i := 0; i < len(table.Alignments); i++ {
		cell := east.NewTableCell()
		cell.Alignment = table.Alignments[i]
		if i < len(cells) {
			cell.SetLines(cellSegments(rline, seg.Start+pos, cells[i]))
		}
		row.AppendChild(row, cell)
	}
	table.AppendChild(table, row)
	reader.AdvanceToEOL()
	return parser.Continue | parser.NoChildren
}

// startsOtherBlock は reOtherBlockStart に一致した行が本当に別ブロックを開始するかを詳しく判定する。
func startsOtherBlock(rest []byte) bool {
	if rest[0] == '<' {
		return htmlBlockStart(rest)
	}
	return true
}

func (p *tableParser) Close(node ast.Node, reader text.Reader, pc parser.Context) {
	if v, ok := node.AttributeString("cm-blank-before"); ok && v == true {
		node.SetBlankPreviousLines(true)
	}
}

func (p *tableParser) CanInterruptParagraph() bool { return true }

func (p *tableParser) CanAcceptIndentedLine() bool { return false }

// htmlBlockStart は CommonMark の HTML ブロック開始条件 1〜7 のいずれかに一致するか。
var (
	reHTMLBlock1 = regexp.MustCompile(`(?i)^<(?:script|pre|style|textarea)(?:\s|>|$)`)
	reHTMLBlock2 = regexp.MustCompile(`^<!--`)
	reHTMLBlock3 = regexp.MustCompile(`^<\?`)
	reHTMLBlock4 = regexp.MustCompile(`^<![A-Za-z]`)
	reHTMLBlock5 = regexp.MustCompile(`^<!\[CDATA\[`)
	reHTMLBlock6 = regexp.MustCompile(`(?i)^</?(?:address|article|aside|base|basefont|blockquote|body|caption|center|col|colgroup|dd|details|dialog|dir|div|dl|dt|fieldset|figcaption|figure|footer|form|frame|frameset|h1|h2|h3|h4|h5|h6|head|header|hr|html|iframe|legend|li|link|main|menu|menuitem|nav|noframes|ol|optgroup|option|p|param|search|section|summary|table|tbody|td|tfoot|th|thead|title|tr|track|ul)(?:\s|/?>|$)`)
	reHTMLBlock7 = regexp.MustCompile(`^(?:<[A-Za-z][A-Za-z0-9-]*(?:\s+[a-zA-Z_:][a-zA-Z0-9:._-]*(?:\s*=\s*(?:[^"'=<>` + "`" + `\x00-\x20]+|'[^']*'|"[^"]*"))?)*\s*/?>|</[A-Za-z][A-Za-z0-9-]*\s*>)\s*$`)
)

func htmlBlockStart(s []byte) bool {
	return reHTMLBlock1.Match(s) || reHTMLBlock2.Match(s) || reHTMLBlock3.Match(s) || reHTMLBlock4.Match(s) ||
		reHTMLBlock5.Match(s) || reHTMLBlock6.Match(s) || reHTMLBlock7.Match(s)
}

// ---- 文字種 ----

func isAlpha(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func isAlnum(c byte) bool { return isAlpha(c) || (c >= '0' && c <= '9') }

// isSpace は C の isspace（ASCII 空白）。
func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r'
}

// isPunct は C の ispunct（ASCII 記号）。
func isPunct(c byte) bool {
	return (c >= 33 && c <= 47) || (c >= 58 && c <= 64) || (c >= 91 && c <= 96) || (c >= 123 && c <= 126)
}
