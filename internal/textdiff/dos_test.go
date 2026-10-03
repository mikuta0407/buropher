// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package textdiff

import (
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestToHTMLPathologicalIsBounded は同じ単語ばかりの長い文章（一致の組が二乗個になる）でも
// 差分の計算が短時間で終わること（説明の変更履歴の表示でサーバーを止められない）。
func TestToHTMLPathologicalIsBounded(t *testing.T) {
	const n = 200000
	from := "y " + strings.Repeat("x ", n) + "y"
	to := "z " + strings.Repeat("x ", n) + "z"
	start := time.Now()
	out := ToHTML(to, from)
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("diff took %v", d)
	}
	if !strings.Contains(out, "z") || !strings.Contains(out, "y") {
		t.Errorf("diff lost changes")
	}
}

// TestToHTMLManyHunksIsBounded は交互に単語を変えた長文（ハンクが大量にできる）でも短時間で終わること。
func TestToHTMLManyHunksIsBounded(t *testing.T) {
	const n = 100000
	var a, b strings.Builder
	for i := range n {
		a.WriteString("k a" + strconv.Itoa(i) + " ")
		b.WriteString("k b" + strconv.Itoa(i) + " ")
	}
	start := time.Now()
	ToHTML(b.String(), a.String())
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("diff took %v", d)
	}
}

// naiveToHTML は gapList 導入前の ToHTML（slice への挿入）。結果が一致することの確認用。
func naiveToHTML(contentTo, contentFrom string) string {
	words := splitWords(contentTo)
	diffs := Diff(splitWords(contentFrom), words)
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = h(w)
	}
	wordsAdd, wordsDel, dels, delOff := 0, 0, 0, 0
	for _, d := range diffs {
		addAt, addTo, delAt := -1, -1, -1
		deleted := ""
		for _, ch := range d {
			if ch.Op == '+' {
				if addAt < 0 {
					addAt = ch.Pos + dels
				}
				addTo = ch.Pos + dels
				wordsAdd++
			} else {
				if delAt < 0 {
					delAt = ch.Pos
				}
				if deleted != "" {
					deleted += " "
				}
				deleted += ch.Elem
				wordsDel++
			}
		}
		if addAt >= 0 {
			out[addAt] = `<span class="diff_in">` + out[addAt]
			out[addTo] = out[addTo] + `</span>`
		}
		if delAt >= 0 {
			idx := delAt - delOff + dels + wordsAdd
			ins := `<span class="diff_out">` + h(deleted) + `</span>`
			if idx < 0 {
				idx += len(out) + 1
			}
			for len(out) < idx {
				out = append(out, "")
			}
			out = append(out[:idx], append([]string{ins}, out[idx:]...)...)
			dels++
			delOff += wordsDel
			wordsDel = 0
		}
	}
	return strings.Join(out, " ")
}

// TestToHTMLMatchesNaive は乱数の入力で ToHTML が従来の実装と同じ結果になること。
func TestToHTMLMatchesNaive(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	vocab := []string{"a", "b", "c", "d", "<e>", "f&g"}
	gen := func() string {
		n := r.IntN(30)
		var b strings.Builder
		for range n {
			b.WriteString(vocab[r.IntN(len(vocab))])
			if r.IntN(5) == 0 {
				b.WriteString("\n")
			} else {
				b.WriteString(" ")
			}
		}
		return b.String()
	}
	for i := range 5000 {
		a, b := gen(), gen()
		if got, want := ToHTML(b, a), naiveToHTML(b, a); got != want {
			t.Fatalf("#%d %q -> %q:\n got %q\nwant %q", i, a, b, got, want)
		}
	}
}
