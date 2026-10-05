// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package sanitize

import (
	"testing"

	"github.com/mikuta0407/buropher/internal/secoracle"
	"github.com/mikuta0407/buropher/internal/textformat/htmldom"
)

// sanitizerPolicy はサニタイズ後に残り得る要素（allowedElements）だけを許す判定。
func sanitizerPolicy() secoracle.HTMLPolicy {
	return secoracle.HTMLPolicy{Elements: allowedElements}
}

// FuzzHTML はサニタイザの出力をブラウザと同じ規則で解析し、許可外の要素・イベントハンドラ・危険な URL・CSS が
// 無いこと、innerHTML の往復で変化しないこと、サニタイズ済みの木と直列化結果の解析木が食い違わないことを確かめる。
//
//	go test -run '^$' -fuzz FuzzHTML ./internal/textformat/sanitize
func FuzzHTML(f *testing.F) {
	for _, s := range []string{
		`<a href="javascript:alert(1)">x</a><img src=x onerror=alert(1)><script>alert(1)</script>`,
		`<p style="color:red;background:url(javascript:alert(1))">x</p><a href=" &#106;avascript:x">y</a>`,
		`<svg><use href="data:x"/></svg><math><mi>x</mi></math><table><td>x</table><li>y</li>`,
		`<!-- --!><script>x</script> --><a title="<script>">z</a><div class="markdown-alert markdown-alert-note">a</div>`,
		`<input type="checkbox" class="task-list-item-checkbox" checked><ul class="contains-task-list"><li class="task-list-item">x</li></ul>`,
		`<style>p{}</style><noscript><p title="</noscript><img src=x onerror=alert(1)>"></noscript>`,
		`<a href="java&#x09;script:x" name="a b">c</a><a href="foo:bar" target="_blank">ext</a><a href="mailto:a@b">m</a>`,
		"<code class=\"language-ruby\">x</code><pre><code class=\"lang\">y</code></pre><a id=\"fnref-1\">1</a><li id=\"fn-1\">x</li>",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		var out string
		secoracle.Bounded(t, len(in), 0, func() { out = HTML(in) })
		if err := secoracle.CheckHTML(out, sanitizerPolicy()); err != nil {
			t.Fatalf("input %q\noutput %q\n%v", in, out, err)
		}
		frag, err := htmldom.ParseHTML5Fragment(in)
		if err != nil {
			return
		}
		Node(frag)
		frag.ScrubTopDown(func(n *htmldom.Node) bool { ExternalLink(n); return false })
		if err := secoracle.CheckDOMRender(frag); err != nil {
			t.Fatalf("input %q\n%v", in, err)
		}
	})
}
