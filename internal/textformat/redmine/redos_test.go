// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package redmine

import (
	"strings"
	"testing"
	"time"
)

// TestTextilizableQuadraticInputBounded は遅延量指定子で 2 乗の時間がかかる本文でも、
// 照合の時間切れで装飾なしのテキストに切り替わり、表示が現実的な時間で終わることを確かめる。
// （修正前は "\n@a" を 4000 回繰り返しただけの 12KB の本文で数十秒、20000 回で数十分かかっていた）
func TestTextilizableQuadraticInputBounded(t *testing.T) {
	e := newTestEnv(t)
	cases := []struct {
		formatting string
		text       string
	}{
		{"textile", strings.Repeat("\n@a", 20000) + "<script>"},
		{"textile", strings.Repeat("|a\n", 20000) + "<script>"},
		{"common_mark", strings.Repeat("[[a", 20000) + "<script>"},
	}
	for i, tc := range cases {
		r, _ := e.renderer(t, "admin", "ecookbook", tc.formatting)
		start := time.Now()
		out := string(r.Textilizable(tc.text, Options{}))
		d := time.Since(start)
		t.Logf("case %d: %v", i, d)
		if d > 30*time.Second {
			t.Errorf("case %d: took %v", i, d)
		}
		if strings.Contains(out, "<script>") {
			t.Errorf("case %d: unescaped output", i)
		}
		start = time.Now()
		out = r.ToHTML(tc.text)
		if d := time.Since(start); d > 30*time.Second {
			t.Errorf("case %d: ToHTML took %v", i, d)
		}
		if strings.Contains(out, "<script>") {
			t.Errorf("case %d: ToHTML unescaped output", i)
		}
	}
}
