// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package textile

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// TestPglAndTagScanEquivalence は pglScan / hasAnyTag が原典の正規表現と同じ結果になることを確認する。
func TestPglAndTagScanEquivalence(t *testing.T) {
	alphabet := []string{"A", "B", "Z", "AB", "A1", "a", "é", "日", "²", "_", "(", ")", " ", "\n", "\"", "'", "<", ">", "&", "-", "1", "x"}
	reTag := rx(`<.*>`)
	rng := rand.New(rand.NewPCG(5, 6))
	for i := 0; i < 50000; i++ {
		var b strings.Builder
		k := 1 + rng.IntN(20)
		for j := 0; j < k; j++ {
			b.WriteString(alphabet[rng.IntN(len(alphabet))])
		}
		s := b.String()
		if got, want := pglScan(s), pglRegexp(s); got != want {
			t.Fatalf("pgl mismatch for %q\nwant %q\ngot  %q", s, want, got)
		}
		if got, want := hasAnyTag(s), matches(reTag, s); got != want {
			t.Fatalf("hasAnyTag mismatch for %q: want %v", s, want)
		}
	}
}

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
