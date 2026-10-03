// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package textile

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

const benchDoc = `h1. 見出し

本文の段落です。*強調*や_斜体_、@コード@も使えます。 http://example.com/ と foo@example.com
二行目 "リンク":https://redmine.jp/ と !画像.png! もあります。GPL(General Public License)

* 箇条書き1
* 箇条書き2
** ネスト

# 番号付き1
# 番号付き2

|_. 列1|_. 列2|
|値1|値2|

bq. 引用文です。

<pre><code class="ruby">
def foo
  "bar"
end
</code></pre>

`

func BenchmarkFormat(b *testing.B) {
	doc := strings.Repeat(benchDoc, 50)
	b.SetBytes(int64(len(doc)))
	for b.Loop() {
		Format(doc, fakeOpts)
	}
}

// TestPathologicalInputs は極端な入力でも現実的な時間で終わることを確認する。
func TestPathologicalInputs(t *testing.T) {
	n := 1000
	if v, err := strconv.Atoi(os.Getenv("TEXTILE_PATHO_N")); err == nil {
		n = v
	}
	inputs := []string{
		strings.Repeat("*a ", n),
		strings.Repeat("a", 20*n),
		strings.Repeat("_", 4*n),
		strings.Repeat("-", 4*n),
		strings.Repeat("* ", n),
		"p{" + strings.Repeat("color:red ", n/2) + "!}. x",
		strings.Repeat("|", 4*n),
		strings.Repeat("|a", 2*n),
		strings.Repeat("\"a", 10000),
		strings.Repeat("!a", 2*n),
		strings.Repeat("(", 2*n),
		strings.Repeat("<", 2*n),
		strings.Repeat("<pre>", n/2),
		strings.Repeat("@a", 2*n),
		strings.Repeat("http://a.b/", n),
		strings.Repeat("a@b.c", n),
		strings.Repeat("ABC(", n),
		strings.Repeat("==a", 2*n),
		strings.Repeat("\n ", 2*n) + "x",
		strings.Repeat("> ", n),
		strings.Repeat(">\n", n),
		strings.Repeat("# a\n", n),
		strings.Repeat("h1. a\n\n", n/2),
		strings.Repeat("|a|b|\n", n),
		strings.Repeat(benchDoc, n/25),
	}
	for i, in := range inputs {
		start := time.Now()
		formatOrTimeout(t, in)
		d := time.Since(start)
		t.Logf("input %d: %v", i, d)
		if d > 5*time.Second {
			t.Errorf("input %d took %v", i, d)
		}
	}
}

// formatOrTimeout は Format と GetSection を実行する。照合の時間切れ（ErrMatchTimeout の panic。
// -race 等で遅い環境では起こり得る）は上限内に打ち切られたものとして許容する。
func formatOrTimeout(t *testing.T, in string) {
	t.Helper()
	defer func() {
		if rec := recover(); rec != nil && rec != ErrMatchTimeout { //nolint:errorlint // 番兵値そのものとの比較
			panic(rec)
		}
	}()
	Format(in, fakeOpts)
	GetSection(in, 2)
}

// TestMatchTimeoutBoundsQuadratic は閉じ記号の無い "@" を大量に含む本文
// （CODE_RE の遅延量指定子で入力長の 2 乗の時間がかかる）の照合が時間切れで打ち切られることを確かめる。
func TestMatchTimeoutBoundsQuadratic(t *testing.T) {
	in := strings.Repeat("\n@a", 20000)
	start := time.Now()
	func() {
		defer func() {
			if rec := recover(); rec != ErrMatchTimeout { //nolint:errorlint // 番兵値そのものとの比較
				t.Errorf("recover = %v, want ErrMatchTimeout", rec)
			}
		}()
		Format(in, fakeOpts)
	}()
	if d := time.Since(start); d > 30*time.Second {
		t.Errorf("took %v", d)
	}
}

// TestMailInLinkCacheBounded は本文中の異なるメールアドレスごとの正規表現のキャッシュが上限を超えないことを確かめる。
func TestMailInLinkCacheBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<a href="x">x</a> `)
	for i := range 2*mailInLinkCacheMax + 10 {
		b.WriteString("u" + strconv.Itoa(i) + "@example.com ")
	}
	out := autoMailto(b.String())
	if !strings.Contains(out, `href="mailto:u0@example.com"`) {
		t.Fatalf("mail not linked: %.200s", out)
	}
	if n := mailInLinkCached.Load(); n > mailInLinkCacheMax {
		t.Fatalf("cached %d > %d", n, mailInLinkCacheMax)
	}
}
