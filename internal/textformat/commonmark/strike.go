// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package commonmark

import (
	"unicode/utf8"

	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// strikeProcessor は comrak と同じく、開始と終了の長さが一致する場合だけ取り消し線にする。
// 長さが一致しない '~' の組が見つかった場合、comrak の process_emphasis はそこで
// 処理を打ち切る（以降の区切りはすべて文字列のまま）ため、それも再現する。
type strikeProcessor struct{}

func (p *strikeProcessor) IsDelimiter(b byte) bool { return b == '~' }

func (p *strikeProcessor) CanOpenCloser(opener, closer *parser.Delimiter) bool {
	if opener.Char != closer.Char || !closer.CanClose {
		return false
	}
	ol, cl := opener.Length, closer.Length
	if (closer.CanOpen || opener.CanClose) && (ol+cl)%3 == 0 && !(ol%3 == 0 && cl%3 == 0) {
		return false
	}
	use := 1
	if ol >= 2 && cl >= 2 {
		use = 2
	}
	if ol-use != cl-use || ol-use > 0 {
		for d := closer; d != nil; d = d.NextDelimiter {
			d.CanOpen = false
			d.CanClose = false
		}
		return false
	}
	return true
}

func (p *strikeProcessor) OnMatch(consumes int) ast.Node { return east.NewStrikethrough() }

var defaultStrikeProcessor = &strikeProcessor{}

// strikeParser は '~' の区切りを読む（長さの制限なし）。
type strikeParser struct{}

func (s *strikeParser) Trigger() []byte { return []byte{'~'} }

func (s *strikeParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	before := block.PrecendingCharacter()
	line, segment := block.PeekLine()
	n := 0
	for n < len(line) && line[n] == '~' {
		n++
	}
	if n == 0 {
		return nil
	}
	after := '\n'
	if n < len(line) {
		after, _ = utf8.DecodeRune(line[n:])
	}
	// 区切りの判定は強調と同じ（cjk_friendly_emphasis 有効時の scan_delims）
	_, beforePos := comrakBeforeCharPos(parent, block)
	twoBefore := func() rune {
		if beforePos < 0 {
			return '\n'
		}
		r, _ := beforeCharAt(parent, block, beforePos)
		return r
	}
	canOpen, canClose := scanDelimsCJK('~', n, before, after, twoBefore)
	node := parser.NewDelimiter(canOpen, canClose, n, '~', defaultStrikeProcessor)
	node.Segment = segment.WithStop(segment.Start + node.OriginalLength)
	block.Advance(node.OriginalLength)
	// comrak 0.45 以降: 3 つ以上連続する "~" は区切りとして扱わない（文字列として残す）
	if (!node.CanOpen && !node.CanClose) || node.OriginalLength > 2 {
		return ast.NewTextSegment(node.Segment)
	}
	pc.PushDelimiter(node)
	return node
}
