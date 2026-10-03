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

// 深く入れ子にした引用・大量のタグで描画が 2 乗の時間にならない（以前は 20KB で数十秒かかった）。
func TestFormatNoQuadraticBlowup(t *testing.T) {
	for _, src := range []string{strings.Repeat(">", 20000) + " x", strings.Repeat("<div>", 20000)} {
		start := time.Now()
		_ = Format(src, Options{})
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("Format(%q...) took %v", src[:10], d)
		}
	}
}
