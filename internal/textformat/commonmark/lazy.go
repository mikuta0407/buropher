// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package commonmark

import (
	"github.com/yuin/goldmark/ast"
)

// lazyLineIndent は段落の行 pos（goldmark が行頭空白を除いた位置）が怠惰な
// 継続行であれば、comrak が段落内容に残す行頭の空白を返す。
//
// cmark/comrak は怠惰な継続行を、コンテナの照合に失敗した位置から
// 先頭空白を除かずに段落へ追加する。通常の文章では改行直後の空白は
// インライン解析で読み飛ばされるが、コードスパンの中ではそのまま残る。
func lazyLineIndent(block ast.Node, pos int, src []byte) string {
	lineStart := pos
	for lineStart > 0 && src[lineStart-1] != '\n' {
		lineStart--
	}
	prefix := src[lineStart:pos]
	var chain []ast.Node
	for n := block.Parent(); n != nil; n = n.Parent() {
		chain = append([]ast.Node{n}, chain...)
	}
	i, col := 0, 0
	// indentAt は i からの空白の桁数を返す
	indentAt := func() (int, int) {
		j, c := i, col
		for j < len(prefix) && (prefix[j] == ' ' || prefix[j] == '\t') {
			if prefix[j] == '\t' {
				c += 4 - c%4
			} else {
				c++
			}
			j++
		}
		return c - col, j
	}
	consume := func(width int) {
		target := col + width
		for i < len(prefix) && col < target && (prefix[i] == ' ' || prefix[i] == '\t') {
			if prefix[i] == '\t' {
				col += 4 - col%4
			} else {
				col++
			}
			i++
		}
	}
	matched := true
loop:
	for _, n := range chain {
		switch n := n.(type) {
		case *ast.Blockquote, *Alert:
			w, j := indentAt()
			if w > 3 || j >= len(prefix) || prefix[j] != '>' {
				matched = false
				break loop
			}
			col += w + 1
			i = j + 1
			if i < len(prefix) && (prefix[i] == ' ' || prefix[i] == '\t') {
				consume(1)
			}
		case *ast.ListItem:
			w, _ := indentAt()
			if w < n.Offset {
				matched = false
				break loop
			}
			consume(n.Offset)
		case *FootnoteDef:
			w, _ := indentAt()
			if w < 4 {
				matched = false
				break loop
			}
			consume(4)
		}
	}
	if matched {
		return ""
	}
	j := len(prefix)
	for j > i && (prefix[j-1] == ' ' || prefix[j-1] == '\t') {
		j--
	}
	return string(prefix[j:])
}
