// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package htmldom

import (
	"strings"
	"testing"
	"time"
)

// TestParseFragmentLinearText は細切れの文字データ（エンティティと文字の繰り返し）を
// テキストノードへ連結する処理が 2 乗の時間にならず、内容が正しく連結されることを確かめる。
func TestParseFragmentLinearText(t *testing.T) {
	measure := func(in string) time.Duration {
		best := time.Duration(1 << 62)
		for range 3 {
			start := time.Now()
			_ = Render(ParseFragment(in))
			best = min(best, time.Since(start))
		}
		return best
	}
	small := measure(strings.Repeat("a&lt;", 4000))
	large := measure(strings.Repeat("a&lt;", 40000))
	if large > 40*max(small, 2*time.Millisecond) {
		t.Errorf("10x input took %v vs %v (superlinear)", large, small)
	}
	frag := ParseFragment("<p>a&lt;b&amp;c</p>x&gt;<b>y</b>z&#65;")
	if got, want := Render(frag), "<p>a&lt;b&amp;c</p>x&gt;<b>y</b>zA"; got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
	if p := frag.FirstChild; p == nil || p.FirstChild == nil || p.FirstChild.Data != "a<b&c" || p.FirstChild.NextSibling != nil {
		t.Errorf("text node not merged: %#v", p.FirstChild)
	}
}

// libxml2（Nokogiri::HTML4::DocumentFragment）の解析・直列化結果と一致することを確認する。
// 期待値は Redmine 環境の Nokogiri 1.19 / libxml2 2.13.9 で確認したもの。
func TestRoundTrip(t *testing.T) {
	cases := [][2]string{
		// 改行の挿入（ブロック要素の子が要素のみの場合）
		{`<ul><li>a</li><li>b</li></ul>`, "<ul>\n<li>a</li>\n<li>b</li>\n</ul>"},
		{`<div><p>a</p><p>b</p></div>`, "<div>\n<p>a</p>\n<p>b</p>\n</div>"},
		{`<p><b>a</b><i>b</i></p>`, `<p><b>a</b><i>b</i></p>`},
		// 自動クローズ
		{`<p>one<p>two`, `<p>one</p><p>two</p>`},
		{`<ul><li>a<li>b</ul>`, "<ul>\n<li>a</li>\n<li>b</li>\n</ul>"},
		// 空要素と論理属性
		{`<br/><hr><img src="a.png">`, `<br><hr><img src="a.png">`},
		{`<input type="checkbox" checked disabled="">`, `<input type="checkbox" checked disabled>`},
		{`<div itemscope>x</div>`, `<div itemscope>x</div>`},
		// 属性値の引用符
		{`<span title='a "b" c'>q</span>`, `<span title='a "b" c'>q</span>`},
		{`<span title="it's">q</span>`, `<span title="it's">q</span>`},
		// URI 属性のエスケープ
		{`<a href="http://例え.jp/ä?a=1&b=2">x</a>`, `<a href="http://%E4%BE%8B%E3%81%88.jp/%C3%A4?a=1&amp;b=2">x</a>`},
		// テキストのエスケープ
		{`a < b & c > d "e"`, `a &lt; b &amp; c &gt; d "e"`},
		// 実体参照（HTML 4 のみ）
		{`&copy;&hearts;&NotAnEntity;`, `©♥&amp;NotAnEntity;`},
		// 不明な要素
		{`<foo><bar>t</bar></foo>`, `<foo><bar>t</bar></foo>`},
		// 対応しない終了タグは無視
		{`text </div> stray`, `text  stray`},
		// 属性値内の <!--...--> / &{...}（旧 libxml2 の SSI 特例）は常にエスケープする。
		// libxml2 2.13 系はこの特例を削除している。
		{`<span title="a &{<img src=x onerror=alert(1)>}">q</span>`, `<span title="a &amp;{&lt;img src=x onerror=alert(1)&gt;}">q</span>`},
		{`<span title="a <!-- x --><img> b">q</span>`, `<span title="a &lt;!-- x --&gt;&lt;img&gt; b">q</span>`},
	}
	for _, c := range cases {
		if got := Render(ParseFragment(c[0])); got != c[1] {
			t.Errorf("%q:\nwant %q\n got %q", c[0], c[1], got)
		}
	}
}

func TestDOMOperations(t *testing.T) {
	frag := ParseFragment(`<p><a href="x">link</a> text</p>`)
	a := frag.FindAll(func(n *Node) bool { return n.IsElement("a") })[0]
	a.SetAttr("class", "external")
	a.SetAttr("href", "y")
	if got := Render(frag); got != `<p><a href="y" class="external">link</a> text</p>` {
		t.Errorf("got %q", got)
	}
	a.ReplaceWithChildren()
	if got := Render(frag); got != `<p>link text</p>` {
		t.Errorf("got %q", got)
	}
}
