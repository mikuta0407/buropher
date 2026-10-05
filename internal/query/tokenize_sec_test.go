// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package query

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestTokenizeLongQuestionIsLinear は互いに異なる 1 文字の語を大量に含む検索語でも Tokenize が
// すぐに終わること（重複除去が全語どうしの比較だと二次の時間になり、/search?q= で DoS になる）。
func TestTokenizeLongQuestionIsLinear(t *testing.T) {
	var b strings.Builder
	for i := range 60000 {
		b.WriteString(strconv.Itoa(i) + "　")
	}
	q := b.String() + "x"
	start := time.Now()
	got := Tokenize(q)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Tokenize took %v for %d bytes", d, len(q))
	}
	if want := []string{"10", "11", "12", "13", "14"}; !slices.Equal(got, want) {
		t.Errorf("Tokenize = %q, want %q", got, want)
	}
	// 重複除去は 1 文字の語の除外より前（Redmine の uniq → select と同じ）
	if got := Tokenize(`ab ab "ab" c cd 漢 ef gh ij`); !slices.Equal(got, []string{"ab", "cd", "漢", "ef", "gh"}) {
		t.Errorf("Tokenize = %q", got)
	}
}
