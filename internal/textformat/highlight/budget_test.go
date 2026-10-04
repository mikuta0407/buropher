// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package highlight

import (
	"strings"
	"testing"
	"time"
)

// chroma で字句解析する言語（Rouge のレキサーを移植していないもの）も全体の時間制限で打ち切られる。
// ceylon は改行の繰り返しで入力長の 2 乗以上の時間がかかり、制限が無いと数十秒かかっていた。
func TestChromaLexingIsTimeBounded(t *testing.T) {
	if rougeImpls["ceylon"] != nil {
		t.Skip("ceylon is lexed by a ported Rouge lexer")
	}
	src := strings.Repeat("a\n", 15000)
	start := time.Now()
	out := HighlightByLanguage(src, "ceylon")
	if d := time.Since(start); d > lexTimeout+3*time.Second {
		t.Errorf("took %v (limit %v)", d, lexTimeout)
	}
	if !strings.Contains(out, "a\na\n") && !strings.Contains(out, "a") {
		t.Errorf("unexpected output %.80q", out)
	}
}

// Budget を共有すると、遅いコードブロックを多数並べても合計の時間が制限内に収まり、
// 制限を超えた後のブロックは装飾されないテキストになる。
func TestBudgetSharedAcrossBlocks(t *testing.T) {
	block := strings.Repeat("*", 5000)
	b := NewBudget()
	start := time.Now()
	var last []string
	for range 5 {
		var parts []string
		for _, n := range Nodes(block, "c", b) {
			parts = append(parts, n.Data)
		}
		last = parts
	}
	if d := time.Since(start); d > 2*lexTimeout {
		t.Errorf("5 blocks took %v with a shared budget (limit %v)", d, lexTimeout)
	}
	if len(last) != 1 || last[0] != block {
		t.Errorf("block after the budget ran out is not plain text: %d nodes", len(last))
	}
	// 通常の入力は従来どおり装飾される
	if got := HighlightByLanguage("int x;", "c", NewBudget()); !strings.Contains(got, `<span class="kt">int</span>`) {
		t.Errorf("highlight = %q", got)
	}
}
