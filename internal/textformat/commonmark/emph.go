// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package commonmark

import (
	"unicode/utf8"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// emphasisParser は goldmark の強調パーサと同じだが、区切りの前後の文字を
// comrak の scan_delims と同様に求める。comrak は strikethrough が有効だと
// '~' を skip_chars とし、前後の文字を調べるときに読み飛ばす
// （例: "**~~" の直後が改行なら "**" は開けない）。
type emphasisParser struct{}

type emphasisDelimiterProcessor struct{}

func (emphasisDelimiterProcessor) IsDelimiter(b byte) bool { return b == '*' || b == '_' }

func (emphasisDelimiterProcessor) CanOpenCloser(opener, closer *parser.Delimiter) bool {
	return opener.Char == closer.Char
}

func (emphasisDelimiterProcessor) OnMatch(consumes int) ast.Node { return ast.NewEmphasis(consumes) }

var emphProcessor = emphasisDelimiterProcessor{}

func (p *emphasisParser) Trigger() []byte { return []byte{'*', '_'} }

func (p *emphasisParser) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	line, segment := block.PeekLine()
	if len(line) == 0 {
		return nil
	}
	c := line[0]
	n := 0
	for n < len(line) && line[n] == c {
		n++
	}
	before, beforePos := comrakBeforeCharPos(parent, block)
	after := '\n'
	j := n
	for j < len(line) && line[j] == '~' {
		j++
	}
	if j < len(line) {
		after, _ = utf8.DecodeRune(line[j:])
	}
	twoBefore := func() rune {
		if beforePos < 0 {
			return '\n'
		}
		r, _ := beforeCharAt(parent, block, beforePos)
		return r
	}
	canOpen, canClose := scanDelimsCJK(c, n, before, after, twoBefore)
	node := parser.NewDelimiter(canOpen, canClose, n, c, emphProcessor)
	node.Segment = segment.WithStop(segment.Start + node.OriginalLength)
	block.Advance(node.OriginalLength)
	pc.PushDelimiter(node)
	return node
}

// scanDelimsCJK は comrak の scan_delims（cjk_friendly_emphasis 有効時）の判定。
// twoBefore は区切りの 2 文字前（直前が異体字セレクタのときだけ参照する）。
func scanDelimsCJK(c byte, n int, before, after rune, twoBefore func() rune) (canOpen, canClose bool) {
	punct := util.IsPunctRune
	beforeWS, afterWS := util.IsSpaceRune(before), util.IsSpaceRune(after)
	afterP := punct(after)
	beforeVS := isNonEmojiGeneralPurposeVS(before)
	var leftAlt bool
	switch {
	case isCJK(after):
		leftAlt = true
	case beforeVS:
		tb := twoBefore()
		leftAlt = isCJK(tb) || punct(tb) || (isCJKAmbiguousPunct(tb) && before == 0xfe01)
	default:
		leftAlt = isCJK(before) || isIdeographicVS(before) || punct(before)
	}
	left := n > 0 && !afterWS && (!afterP || beforeWS || leftAlt)
	var x bool
	if !isCJK(after) {
		if beforeVS {
			tb := twoBefore()
			x = !isCJK(tb) && punct(tb) && !(isCJKAmbiguousPunct(tb) && before == 0xfe01)
		} else {
			x = !isCJK(before) && punct(before)
		}
	}
	right := n > 0 && !beforeWS && (!x || afterWS || afterP)
	if c == '_' {
		bp := punct(before)
		if beforeVS {
			bp = punct(twoBefore())
		}
		return left && (!right || bp), right && (!left || afterP)
	}
	return left, right
}

// isCJK は markdown-cjk-friendly の CJK 文字の範囲（comrak の is_cjk。Unicode 16 時点）。
func isCJK(r rune) bool {
	for _, rg := range cjkRanges {
		if r < rg[0] {
			return false
		}
		if r <= rg[1] {
			return true
		}
	}
	return false
}

var cjkRanges = [][2]rune{
	{0x1100, 0x11ff}, {0x20a9, 0x20a9}, {0x2329, 0x232a}, {0x2630, 0x2637}, {0x268a, 0x268f},
	{0x2e80, 0x2e99}, {0x2e9b, 0x2ef3}, {0x2f00, 0x2fd5}, {0x2ff0, 0x303e}, {0x3041, 0x3096},
	{0x3099, 0x30ff}, {0x3105, 0x312f}, {0x3131, 0x318e}, {0x3190, 0x31e5}, {0x31ef, 0x321e},
	{0x3220, 0x3247}, {0x3250, 0xa48c}, {0xa490, 0xa4c6}, {0xa960, 0xa97c}, {0xac00, 0xd7a3},
	{0xd7b0, 0xd7c6}, {0xd7cb, 0xd7fb}, {0xf900, 0xfaff}, {0xfe10, 0xfe19}, {0xfe30, 0xfe52},
	{0xfe54, 0xfe66}, {0xfe68, 0xfe6b}, {0xff01, 0xffbe}, {0xffc2, 0xffc7}, {0xffca, 0xffcf},
	{0xffd2, 0xffd7}, {0xffda, 0xffdc}, {0xffe0, 0xffe6}, {0xffe8, 0xffee}, {0x16fe0, 0x16fe4},
	{0x16ff0, 0x16ff6}, {0x17000, 0x18cd5}, {0x18cff, 0x18d1e}, {0x18d80, 0x18df2}, {0x1aff0, 0x1aff3},
	{0x1aff5, 0x1affb}, {0x1affd, 0x1affe}, {0x1b000, 0x1b122}, {0x1b132, 0x1b132}, {0x1b150, 0x1b152},
	{0x1b155, 0x1b155}, {0x1b164, 0x1b167}, {0x1b170, 0x1b2fb}, {0x1d300, 0x1d356}, {0x1d360, 0x1d376},
	{0x1f200, 0x1f200}, {0x1f202, 0x1f202}, {0x1f210, 0x1f219}, {0x1f21b, 0x1f22e}, {0x1f230, 0x1f231},
	{0x1f237, 0x1f237}, {0x1f23b, 0x1f23b}, {0x1f240, 0x1f248}, {0x1f260, 0x1f265}, {0x20000, 0x3fffd},
}

func isNonEmojiGeneralPurposeVS(r rune) bool { return r >= 0xfe00 && r <= 0xfe0e }

func isIdeographicVS(r rune) bool { return r >= 0xe0100 && r <= 0xe01ef }

func isCJKAmbiguousPunct(r rune) bool {
	return r == 0x2018 || r == 0x2019 || r == 0x201c || r == 0x201d
}

// comrakBeforeCharPos は comrakBeforeChar と同じ文字と、その位置（行内で求められない場合は -1）を返す。
func comrakBeforeCharPos(parent ast.Node, block text.Reader) (rune, int) {
	_, pos := block.Position()
	return beforeCharAt(parent, block, pos.Start)
}

// beforeCharAt は at の直前の文字（'~' と UTF-8 の継続バイトを読み飛ばす）とその位置を返す。
func beforeCharAt(parent ast.Node, block text.Reader, at int) (rune, int) {
	src := block.Source()
	l, pos := block.Position()
	var lineStart int
	if lines := parent.Lines(); lines != nil && l < lines.Len() && lines.At(l).Start <= pos.Start {
		lineStart = lines.At(l).Start
	} else {
		if at == pos.Start {
			return block.PrecendingCharacter(), -1
		}
		return '\n', -1
	}
	i := at - 1
	for i >= lineStart && (src[i] == '~' || src[i]>>6 == 2) {
		i--
	}
	if i < lineStart {
		if i+1 == pos.Start {
			return block.PrecendingCharacter(), -1
		}
		return '\n', -1
	}
	r, _ := utf8.DecodeRune(src[i:at])
	return r, i
}
