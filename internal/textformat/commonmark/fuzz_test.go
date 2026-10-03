package commonmark

import (
	"strings"
	"testing"
	"time"
)

func FuzzFormat(f *testing.F) {
	f.Add("# h\n\n> [!NOTE]\n> x\n\n- [ ] a\n- [x] b\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n```ruby\nx\n```\n\n[a]: http://x \"t\"\n\n<div>raw</div> ~~s~~ **b** _e_ `c` <http://auto> www.example.com[^1]\n\n[^1]: fn")
	f.Fuzz(func(t *testing.T, src string) {
		_ = Format(src, Options{})
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
