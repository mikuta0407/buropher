// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package wikidiff

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestDiffPathologicalIsBounded は同じ行・単語ばかりの長いページ（一致の組が二乗個になる）でも
// 差分と注釈の計算が短時間で終わること。
func TestDiffPathologicalIsBounded(t *testing.T) {
	const n = 200000
	from := "y\n" + strings.Repeat("x\n", n) + "y"
	to := "z\n" + strings.Repeat("x\n", n) + "z"
	start := time.Now()
	if out := WordDiffHTML(to, from); !strings.Contains(out, "z") {
		t.Error("diff lost changes")
	}
	hunks := DiffStrings(SplitLines(from), SplitLines(to))
	if len(hunks) == 0 {
		t.Error("no hunks")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("diff took %v", d)
	}
}

// TestWordDiffManyHunksIsBounded は交互に単語を変えた長文（ハンクが大量にできる）でも短時間で終わること。
func TestWordDiffManyHunksIsBounded(t *testing.T) {
	const n = 100000
	var a, b strings.Builder
	for i := range n {
		a.WriteString("k a" + strconv.Itoa(i) + " ")
		b.WriteString("k b" + strconv.Itoa(i) + " ")
	}
	start := time.Now()
	WordDiffHTML(b.String(), a.String())
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("diff took %v", d)
	}
}
