// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package commonmark

import (
	"bytes"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// htmlBlockParser は goldmark の HTML ブロックパーサーを包み、cmark/comrak の
// 開始条件 7 の判定に合わせる。cmark は「直前に照合できたコンテナ」が段落か
// どうかで判定するため、怠惰な継続行（引用やリスト項目の外にある行）では
// 段落の途中でも条件 7 の HTML ブロックが始まる。また cmark の条件 7 は
// script/style/pre の終了タグも除外しない。
type htmlBlockParser struct {
	parser.BlockParser
}

func newHTMLBlockParser() *htmlBlockParser {
	return &htmlBlockParser{parser.NewHTMLBlockParser()}
}

func (b *htmlBlockParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	node, state := b.BlockParser.Open(parent, reader, pc)
	if node != nil {
		return node, state
	}
	line, seg := reader.PeekLine()
	// goldmark の正規表現は行頭のタブを許さないため、タブで字下げされた行
	// （字下げ幅 3 以下）は自前で判定する
	tl := bytes.TrimLeft(line, " \t")
	var typ ast.HTMLBlockType
	switch {
	case reHTMLBlock1.Match(tl):
		typ = ast.HTMLBlockType1
	case reHTMLBlock2.Match(tl):
		typ = ast.HTMLBlockType2
	case reHTMLBlock3.Match(tl):
		typ = ast.HTMLBlockType3
	case reHTMLBlock4.Match(tl):
		typ = ast.HTMLBlockType4
	case reHTMLBlock5.Match(tl):
		typ = ast.HTMLBlockType5
	case reHTMLBlock6.Match(tl):
		typ = ast.HTMLBlockType6
	case reHTMLBlock7.Match(tl):
		if last := pc.LastOpenedBlock().Node; last != nil && ast.IsParagraph(last) && last.Parent() == parent {
			return nil, parser.NoChildren
		}
		typ = ast.HTMLBlockType7
	default:
		return nil, parser.NoChildren
	}
	hb := ast.NewHTMLBlock(typ)
	reader.AdvanceToEOL()
	hb.Lines().Append(seg)
	return hb, parser.NoChildren
}
