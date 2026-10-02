package commonmark

import (
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// comrak の MAXBACKTICKS
const maxBackticks = 80

var backtickKey = parser.NewContextKey()

// backtickState は comrak の Subject の scanned_for_backticks / backticks。
type backtickState struct {
	block     ast.Node
	scanned   bool
	backticks [maxBackticks + 1]int
}

// codeSpanGuard は comrak の scan_to_closing_backtick の挙動を再現し、
// comrak が閉じのバッククォートを見つけられない場合にバッククォート列を
// 文字列として消費する（見つかる場合は goldmark のコードスパンパーサに任せる）。
//
// comrak は走査中に見つけた各長さのバッククォート列の位置を記録するが、
// 再走査で前方の位置に上書きされることがあり、その後の同じ長さの開始記号が
// （実際には閉じがあっても）コードスパンにならないことがある。
type codeSpanGuard struct{}

func (p *codeSpanGuard) Trigger() []byte { return []byte{'`'} }

func (p *codeSpanGuard) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	st, _ := pc.Get(backtickKey).(*backtickState)
	if st == nil || st.block != parent {
		st = &backtickState{block: parent}
		pc.Set(backtickKey, st)
	}
	line, seg := block.PeekLine()
	n := 0
	for n < len(line) && line[n] == '`' {
		n++
	}
	if n == 0 {
		return nil
	}
	blocked := false
	if n > maxBackticks {
		blocked = true
	} else if st.scanned && st.backticks[n] <= seg.Start+n {
		blocked = true
	} else {
		blocked = !p.scan(st, n, block)
	}
	if !blocked {
		return nil
	}
	block.Advance(n)
	return ast.NewTextSegment(seg.WithStop(seg.Start + n))
}

// scan は開始記号の直後から閉じを探し、見つかれば true を返す（読み位置は戻す）。
func (p *codeSpanGuard) scan(st *backtickState, n int, block text.Reader) bool {
	l, pos := block.Position()
	defer block.SetPosition(l, pos)
	block.Advance(n)
	for {
		line, seg := block.PeekLine()
		if line == nil {
			st.scanned = true
			return false
		}
		i := 0
		for i < len(line) {
			if line[i] != '`' {
				i++
				continue
			}
			start := i
			for i < len(line) && line[i] == '`' {
				i++
			}
			k := i - start
			if k <= maxBackticks {
				st.backticks[k] = seg.Start + start
			}
			if k == n {
				return true
			}
		}
		block.AdvanceLine()
	}
}
