// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package commonmark

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yuin/goldmark/ast"
)

// bestOf3 は f の 3 回の実行で最短の時間を返す。
func bestOf3(f func()) time.Duration {
	best := time.Duration(1 << 62)
	for range 3 {
		start := time.Now()
		f()
		best = min(best, time.Since(start))
	}
	return best
}

// 入力を 10 倍にしても描画時間が 2 乗（100 倍）にならない。
//   - "- " / "1. " / ">" を 1 行に大量に並べた入れ子（goldmark は段ごとに行頭から走査する）
//   - 隣接テキストの += による連結（"*a " の繰り返しなど、閉じない強調の区切りがテキストとして残る）
//   - リンク参照定義の大量の並び（goldmark が定義ごとに残りの行を詰め直す）
//   - "a<" の繰り返し（htmldom がエンティティごとにテキストノードへ += していた）
func TestFormatLinearOnRepeatedInput(t *testing.T) {
	cases := []struct {
		name string
		gen  func(n int) string
	}{
		{"nested bullet list", func(n int) string { return strings.Repeat("- ", n) + "x" }},
		{"nested ordered list", func(n int) string { return strings.Repeat("1. ", n) + "x" }},
		{"nested blockquote", func(n int) string { return strings.Repeat(">", n) + " x" }},
		{"unclosed emphasis", func(n int) string { return strings.Repeat("*a ", n) + "x" }},
		{"unclosed brackets", func(n int) string { return strings.Repeat("[a ", n) + "x" }},
		{"link reference definitions", func(n int) string { return strings.Repeat("[a]: b\n", n) }},
		{"escaped lt", func(n int) string { return strings.Repeat("a<", n) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			small := bestOf3(func() { _ = Format(tc.gen(2000), Options{}) })
			large := bestOf3(func() { _ = Format(tc.gen(20000), Options{}) })
			if large > 40*max(small, 2*time.Millisecond) {
				t.Errorf("10x input took %v vs %v (superlinear)", large, small)
			}
		})
	}
}

// メールアドレスの自動リンクの抽出が残りのテキストの複製・再帰で 2 乗の時間にならない。
func TestProcessEmailAutolinksLinear(t *testing.T) {
	run := func(n int) func() {
		return func() {
			p := ast.NewParagraph()
			s := &Str{Value: strings.Repeat("a@b.co ", n)}
			p.AppendChild(p, s)
			processEmailAutolinks(s)
		}
	}
	small := bestOf3(run(2000))
	large := bestOf3(run(40000))
	if large > 80*max(small, time.Millisecond) {
		t.Errorf("20x input took %v vs %v (superlinear)", large, small)
	}
	// 分割結果は従来どおり（テキスト・リンクが交互に並び、最後の空白も残る）
	p := ast.NewParagraph()
	s := &Str{Value: "x a@b.co y c@d.org"}
	p.AppendChild(p, s)
	processEmailAutolinks(s)
	var got []string
	for c := p.FirstChild(); c != nil; c = c.NextSibling() {
		switch n := c.(type) {
		case *Str:
			got = append(got, "T:"+n.Value)
		case *Link:
			got = append(got, "L:"+n.URL)
		}
	}
	if want := "T:x |L:mailto:a@b.co|T: y |L:mailto:c@d.org"; strings.Join(got, "|") != want {
		t.Errorf("got %q, want %q", strings.Join(got, "|"), want)
	}
}

// 行頭のコンテナの印が上限以下なら入力を書き換えない。超えた行は上限の段までで入れ子が止まる。
func TestCapContainerDepth(t *testing.T) {
	for _, src := range []string{
		"",
		"- a\n  - b\n",
		strings.Repeat("- ", maxContainerMarkers) + "x\n" + strings.Repeat(">", maxContainerMarkers) + " y",
		strings.Repeat("- ", 300) + "-",       // 長い水平線
		strings.Repeat("* ", 300) + "*\n",     // 長い水平線
		"    " + strings.Repeat("- a ", 300), // 先頭の印は 1 つだけ
	} {
		if got := string(capContainerDepth([]byte(src))); got != src {
			t.Errorf("capContainerDepth(%.40q) changed the input: %.80q", src, got)
		}
	}
	src := strings.Repeat("> ", maxContainerMarkers+5) + "x"
	want := strings.Repeat("> ", maxContainerMarkers) + `\` + strings.Repeat("> ", 5) + "x"
	if got := string(capContainerDepth([]byte(src))); got != want {
		t.Errorf("capContainerDepth: got ...%q, want ...%q", got[len(got)-30:], want[len(want)-30:])
	}
	src = strings.Repeat("1. ", maxContainerMarkers+1) + "x"
	want = strings.Repeat("1. ", maxContainerMarkers) + `1\. x`
	if got := string(capContainerDepth([]byte(src))); got != want {
		t.Errorf("capContainerDepth ordered: got ...%q", got[len(got)-20:])
	}
	html := Format(strings.Repeat("- ", maxContainerMarkers+10)+"x", Options{})
	if n := strings.Count(html, "<ul>"); n != maxContainerMarkers {
		t.Errorf("nesting depth = %d, want %d", n, maxContainerMarkers)
	}
}

// リンク参照定義を分けて抽出しても、一度に抽出した場合と同じ HTML になる。
func TestRefDefBatchesEquivalent(t *testing.T) {
	var defs strings.Builder
	for i := range 300 {
		switch i % 5 {
		case 0, 4:
			fmt.Fprintf(&defs, "[l%d]: /u%d\n", i, i)
		case 1:
			fmt.Fprintf(&defs, "[l%d]: /u%d \"title %d\"\n", i, i, i)
		case 2:
			fmt.Fprintf(&defs, "[l%d]:\n  <http://e/%d>\n  'multi\nline'\n", i, i)
		case 3:
			fmt.Fprintf(&defs, "  [L%d]: /dup%d (paren)\n", i-3, i)
		}
	}
	var refs strings.Builder
	for i := range 300 {
		fmt.Fprintf(&refs, "[x][l%d] ", i)
	}
	srcs := []string{
		defs.String() + "\n" + refs.String(),
		defs.String() + "tail paragraph\n\n" + refs.String(),
		"- item\n\n  " + strings.ReplaceAll(defs.String(), "\n", "\n  ") + "\n" + refs.String(),
		"> " + strings.ReplaceAll(defs.String(), "\n", "\n> ") + "lazy\n" + refs.String(),
		defs.String() + "[bad]: <unclosed\n" + defs.String() + refs.String(),
	}
	saved := refDefBatch
	defer func() { refDefBatch = saved }()
	for i, src := range srcs {
		refDefBatch = 1 << 30
		want := Format(src, Options{})
		for _, b := range []int{1, 7, 64} {
			refDefBatch = b
			if got := Format(src, Options{}); got != want {
				t.Errorf("src %d batch %d: output differs from unbatched", i, b)
			}
		}
	}
}
