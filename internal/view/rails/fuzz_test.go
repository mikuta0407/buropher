// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package rails

import (
	"testing"

	"github.com/mikuta0407/buropher/internal/secoracle"
)

// FuzzSanitize は sanitize / strip_tags / simple_format の出力をブラウザと同じ規則で解析し、
// 許可外の要素・イベントハンドラ・危険な URL が無いこと、innerHTML の往復で変化しないことを確かめる。
// strip_tags の出力には要素自体が無いこと（テキストのみ）を確かめる。
//
//	go test -run '^$' -fuzz FuzzSanitize ./internal/view/rails
func FuzzSanitize(f *testing.F) {
	for _, s := range []string{
		`<a href="javascript:alert(1)">x</a><img src=x onerror=alert(1)><script>alert(1)</script>`,
		`<a href=" &#106;avascript:x">y</a><a href="java&#x09;script:x">z</a><a href="data:text/html,<script>">d</a>`,
		`<p title="<script>">a</p><b/onclick=x>b</b><svg><script>x</script></svg><style>*{}</style>`,
		`<!-- --!><script>x</script> --><noscript><p title="</noscript><img src=x onerror=alert(1)>"></noscript>`,
		`<img src="x" alt='a"b' title=a'b><a href=//evil.com name=x>e</a><div class=x style="x:expression(1)">s</div>`,
		"<textarea><script>alert(1)</script></textarea><title>t</title><xmp><b>x</b></xmp><plaintext>p",
		"<a href='x' href='javascript:x'>dup</a><a\nhref=javascript:x>nl</a><a href=\"x\"onclick=\"y\">q</a>",
	} {
		f.Add(s)
	}
	policy := secoracle.HTMLPolicy{Elements: sanitizeAllowedTags, NoStyle: true}
	f.Fuzz(func(t *testing.T, in string) {
		var out, stripped, simple string
		secoracle.Bounded(t, len(in), 0, func() {
			out = string(Sanitize(in))
			stripped = string(StripTags(in))
			simple = string(SimpleFormat(in, nil, nil))
		})
		if err := secoracle.CheckHTML(out, policy); err != nil {
			t.Fatalf("Sanitize(%q) = %q\n%v", in, out, err)
		}
		if err := secoracle.CheckHTML(simple, policy); err != nil {
			t.Fatalf("SimpleFormat(%q) = %q\n%v", in, simple, err)
		}
		if err := secoracle.CheckHTML(stripped, secoracle.HTMLPolicy{Elements: map[string]bool{}}); err != nil {
			t.Fatalf("StripTags(%q) = %q\n%v", in, stripped, err)
		}
	})
}
