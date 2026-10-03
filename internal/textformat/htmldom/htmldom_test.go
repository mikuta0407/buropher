// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package htmldom

import "testing"

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
