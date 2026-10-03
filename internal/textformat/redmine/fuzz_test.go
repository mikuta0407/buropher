// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package redmine

import (
	"testing"
)

// FuzzTextilizable は textilizable（Redmine のリンク・マクロ・書式）が任意の入力で panic しないことを確かめる。
//
//	go test -run '^$' -fuzz FuzzTextilizable ./internal/textformat/redmine
func FuzzTextilizable(f *testing.F) {
	for _, s := range []string{
		"#1 r1 commit:abc document:\"Test document\" version:1.0 attachment:error281.txt",
		"[[CookBook documentation|doc]] [[ecookbook:Another page#anchor]] {{toc}} {{child_pages(depth=2)}}",
		"{{include(Another page)}} {{thumbnail(image.png, size=300)}} {{collapse(x)\nbody\n}}",
		"h1. Title\n\n* item\n\n<pre><code class=\"ruby\">x</code></pre> !image.png! \"link\":http://x",
		"# head\n\n```ruby\nx\n```\n[a](#1) ##123 source:\"repo|a/b@1#L2\" user:jsmith @admin forum#1 message#1 news#1 project:ecookbook",
		"{{issue(1, project=true)}} {{recent_pages(time=true, days=0)}} {{macro_list}} {{hello_world(a,b)}}",
	} {
		f.Add(s, false)
		f.Add(s, true)
	}
	e := newTestEnv(f)
	f.Fuzz(func(t *testing.T, text string, markdown bool) {
		formatting := "textile"
		if markdown {
			formatting = "common_mark"
		}
		r, st := e.renderer(t, "admin", "ecookbook", formatting)
		var o Options
		if obj, err := st.LoadObject("issue", 1); err == nil {
			o.Object = obj
		}
		_ = r.Textilizable(text, o)
	})
}
