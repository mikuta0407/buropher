// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package commonmark

import (
	"bytes"
	"strings"

	"github.com/yuin/goldmark/ast"
)

// codeSpanLiteral はコードスパンの内容を comrak の段落内容と同じ規則で
// 原文から組み立て、normalize_code を適用する。
// 各行は通常は行頭の空白を除き、怠惰な継続行では照合に失敗した位置からの
// 空白を残す。組み立てられない場合は ok=false を返す。
func codeSpanLiteral(n *ast.CodeSpan, block ast.Node, src []byte) (string, bool) {
	var first, last *ast.Text
	for t := n.FirstChild(); t != nil; t = t.NextSibling() {
		if tt, ok := t.(*ast.Text); ok {
			if first == nil {
				first = tt
			}
			last = tt
		}
	}
	if first == nil || block == nil {
		return "", false
	}
	o := first.Segment.Start - 1
	for o >= 0 && src[o] != '`' {
		o--
	}
	if o < 0 {
		return "", false
	}
	start := o + 1
	cl := bytes.IndexByte(src[last.Segment.Stop:], '`')
	if cl < 0 {
		return "", false
	}
	closer := last.Segment.Stop + cl
	lines := block.Lines()
	var sb strings.Builder
	k := 0
	for i := start; i < closer; {
		c := src[i]
		if c != '\n' {
			sb.WriteByte(c)
			i++
			continue
		}
		sb.WriteByte('\n')
		for k < lines.Len() && lines.At(k).Start <= i {
			k++
		}
		if k >= lines.Len() || lines.At(k).Start > closer {
			return "", false
		}
		j := lines.At(k).Start
		for j < closer && (src[j] == ' ' || src[j] == '\t') {
			j++
		}
		sb.WriteString(lazyLineIndent(block, j, src))
		i = j
	}
	lit := strings.ReplaceAll(sb.String(), "\r\n", " ")
	lit = strings.ReplaceAll(lit, "\n", " ")
	if len(lit) > 1 && lit[0] == ' ' && lit[len(lit)-1] == ' ' && strings.Trim(lit, " ") != "" {
		lit = lit[1 : len(lit)-1]
	}
	return lit, true
}
