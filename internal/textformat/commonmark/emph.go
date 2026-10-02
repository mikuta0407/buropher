package commonmark

import (
	"unicode/utf8"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
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
	before := comrakBeforeChar(parent, block)
	after := '\n'
	j := n
	for j < len(line) && line[j] == '~' {
		j++
	}
	if j < len(line) {
		after, _ = utf8.DecodeRune(line[j:])
	}
	// ScanDelimiter には区切りの連続と直後の文字だけを渡す
	synthetic := make([]byte, 0, n+4)
	synthetic = append(synthetic, line[:n]...)
	synthetic = utf8.AppendRune(synthetic, after)
	node := parser.ScanDelimiter(synthetic, before, 1, emphProcessor)
	if node == nil {
		return nil
	}
	node.Segment = segment.WithStop(segment.Start + node.OriginalLength)
	block.Advance(node.OriginalLength)
	pc.PushDelimiter(node)
	return node
}

// comrakBeforeChar は comrak の get_before_char（'~' を読み飛ばす）。
func comrakBeforeChar(parent ast.Node, block text.Reader) rune {
	src := block.Source()
	l, pos := block.Position()
	lineStart := pos.Start
	if lines := parent.Lines(); lines != nil && l < lines.Len() && lines.At(l).Start <= pos.Start {
		lineStart = lines.At(l).Start
	} else {
		return block.PrecendingCharacter()
	}
	i := pos.Start - 1
	for i >= lineStart && (src[i] == '~' || src[i]>>6 == 2) {
		i--
	}
	if i < lineStart {
		if i+1 == pos.Start {
			return block.PrecendingCharacter()
		}
		return '\n'
	}
	r, _ := utf8.DecodeRune(src[i:pos.Start])
	return r
}
