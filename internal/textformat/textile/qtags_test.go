// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

package textile

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// TestQtagMatcherEquivalence は専用マッチャが原典の正規表現と同じ結果になることを
// ランダム入力で確認する。
func TestQtagMatcherEquivalence(t *testing.T) {
	alphabet := []string{"*", "**", "_", "__", "-", "--", "+", "^", "~", "%", "??", "?",
		"a", "b", "Z", "日", "²", " ", " ", "\n", "\t", "(", ")", ">", "<", ".", ",", "!", "{", "}",
		"[", "]", "#", "c(x)", "{color:red}", "[fr]", " ", "́"}
	rng := rand.New(rand.NewPCG(1, 2))
	n := 50000
	if testing.Short() {
		n = 5000
	}
	for i := 0; i < n; i++ {
		var b strings.Builder
		k := 1 + rng.IntN(20)
		for j := 0; j < k; j++ {
			b.WriteString(alphabet[rng.IntN(len(alphabet))])
		}
		s := b.String()
		a := (&redcloth{}).inlineTextileSpan(s)
		want := (&redcloth{}).inlineTextileSpanRegexp(s)
		if a != want {
			t.Fatalf("mismatch for %q\nwant %q\ngot  %q", s, want, a)
		}
		ra, rw := &redcloth{}, &redcloth{}
		ra.inlineTextileSpan(s)
		rw.inlineTextileSpanRegexp(s)
		if strings.Join(ra.shelf, "\x00") != strings.Join(rw.shelf, "\x00") {
			t.Fatalf("shelf mismatch for %q", s)
		}
	}
}
