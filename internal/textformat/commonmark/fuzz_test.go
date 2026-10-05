// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package commonmark

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/secoracle"
	"github.com/mikuta0407/buropher/internal/textformat/htmldom"
)

// commonmarkPolicy は CommonMark の出力に許す内容（サニタイザの許可要素とアラートのアイコン SVG）。
var commonmarkPolicy = secoracle.HTMLPolicy{SVG: true}

func FuzzFormat(f *testing.F) {
	f.Add("# h\n\n> [!NOTE]\n> x\n\n- [ ] a\n- [x] b\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n```ruby\nx\n```\n\n[a]: http://x \"t\"\n\n<div>raw</div> ~~s~~ **b** _e_ `c` <http://auto> www.example.com[^1]\n\n[^1]: fn")
	for _, s := range []string{
		"[x](javascript:alert(1)) [y]( JaVaScRiPt:x) <a href=\"vbscript:x\">v</a> ![i](javascript:x) <javascript:alert(1)>",
		"<img src=x onerror=alert(1)><script>alert(1)</script><style>*{}</style><iframe src=x></iframe>",
		"[a](<java\nscript:x>) [b](&#106;avascript:x) [c](java&#x09;script:x) [d][r]\n\n[r]: javascript:x",
		"<div style=\"background:url(javascript:x)\">a</div> <p style=\"color:red;x:expression(alert(1))\">b</p>",
		"> [!NOTE]\n> <svg onload=alert(1)>\n\n```js\n</code><script>x</script>\n```",
		"<!-- --!><script>x</script> --> <noscript><p title=\"</noscript><img src=x onerror=alert(1)>\"></noscript>",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		var out string
		secoracle.Bounded(t, len(src), 0, func() { out = Format(src, Options{}) })
		if err := secoracle.CheckHTML(out, commonmarkPolicy); err != nil {
			t.Fatalf("input %q\noutput %q\n%v", src, out, err)
		}
		// Format と同じ手順で作った木と、その直列化をブラウザが解析した木の差分
		frag, err := formatTree(src, Options{})
		if err == nil {
			err = secoracle.CheckDOMRender(frag)
		}
		if err != nil && !errors.Is(err, htmldom.ErrTreeTooDeep) && !errors.Is(err, htmldom.ErrTooManyAttributes) {
			t.Fatalf("input %q\n%v", src, err)
		}
		_, _ = GetSection(src, 1)
		_, _ = UpdateSection(src, 1, "x", "")
	})
}

// 大量のタグ・深く入れ子にした引用で描画が極端に遅くならない。
//   - "<div>"*n は tagfilter が '<' ごとに残り全体を小文字にしていた（2 乗）。入力を 10 倍にしたときの時間の比で確かめる。
//   - ">"*20000 はアラートの判定が入れ子の段数ごとに行全体を正規表現で走査していた（約 48 秒）。goldmark 自体の
//     入れ子の処理（LineOffset）も 2 乗なので比では確かめられず、-race 以外で絶対時間の上限を確かめる。
func TestFormatNoQuadraticBlowup(t *testing.T) {
	measure := func(src string) time.Duration {
		best := time.Duration(1 << 62)
		for range 3 {
			start := time.Now()
			_ = Format(src, Options{})
			best = min(best, time.Since(start))
		}
		return best
	}
	small := measure(strings.Repeat("<div>", 2000))
	large := measure(strings.Repeat("<div>", 20000))
	if large > 40*max(small, time.Millisecond) {
		t.Errorf("<div>: 10x input took %v vs %v (superlinear)", large, small)
	}
	if !raceEnabled {
		if d := measure(strings.Repeat(">", 20000) + " x"); d > 10*time.Second {
			t.Errorf(">*20000 took %v", d)
		}
	}
}
