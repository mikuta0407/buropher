// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package sanitize

import (
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/textformat/htmldom"
	"github.com/mikuta0407/buropher/internal/textformat/internal/fixtures"
)

// filter は SanitizationFilter.to_html 相当（サニタイズのみ）。
func filter(html string) string {
	frag, err := htmldom.ParseHTML5Fragment(html)
	if err != nil {
		panic(err)
	}
	Node(frag)
	return htmldom.RenderHTML5(frag)
}

// 以下は Redmine の test/unit/lib/redmine/wiki_formatting/common_mark/sanitization_filter_test.rb の移植。

func TestShouldFilterTags(t *testing.T) {
	assertEqual(t, `foo dont blink`, filter(`<textarea>foo</textarea> <blink>dont blink</blink>`))
}

func TestShouldSanitizeAttributes(t *testing.T) {
	assertEqual(t, `<a href="foo">link</a>`, filter(`<a href="foo" onclick="bar" baz="foo">link</a>`))
}

func TestShouldAllowRelativeLinks(t *testing.T) {
	in := `<a href="foo/bar">foo/bar</a>`
	assertEqual(t, in, filter(in))
}

func TestShouldSupportFootnotes(t *testing.T) {
	for _, in := range []string{
		`<a href="#fn-1" id="fnref-1">foo</a>`,
		`<a href="#fn-1" id="fnref-1-2">foo</a>`,
		`<ol><li id="fn-1">footnote</li></ol>`,
	} {
		assertEqual(t, in, filter(in))
		assertEqual(t, in, filter(in))
	}
}

func TestShouldRemoveInvalidIDs(t *testing.T) {
	assertEqual(t, `<a href="#fn1">foo</a>`, filter(`<a href="#fn1" id="foo">foo</a>`))
	assertEqual(t, `<ol><li>footnote</li></ol>`, filter(`<ol><li id="foo">footnote</li></ol>`))
}

func TestShouldAllowClassOnCodeOnly(t *testing.T) {
	assertEqual(t, `<p>bar</p>`, filter(`<p class="foo">bar</p>`))
	in := `<code class="language-ruby">foo</code>`
	assertEqual(t, in, filter(in))
	assertEqual(t, `<code>foo</code>`, filter(`<code class="foo">foo</code>`))
}

func TestShouldAllowValidAlertDivAndPClasses(t *testing.T) {
	html := "<div class=\"markdown-alert markdown-alert-tip\">\n  <p class=\"markdown-alert-title\">Tip</p>\n  <p>Useful tip.</p>\n</div>\n"
	got := filter(html)
	if !strings.Contains(got, `class="markdown-alert markdown-alert-tip"`) || !strings.Contains(got, `class="markdown-alert-title"`) {
		t.Errorf("alert classes removed: %q", got)
	}
}

func TestShouldRemoveInvalidDivAndPClass(t *testing.T) {
	if got := filter(`<div class="bad-class">Text</div>`); strings.Contains(got, "bad-class") {
		t.Errorf("got %q", got)
	}
	if got := filter(`<p class="bad-class">Text</p>`); strings.Contains(got, "bad-class") {
		t.Errorf("got %q", got)
	}
}

func TestShouldAllowLinksWithSafeURLSchemes(t *testing.T) {
	for _, scheme := range []string{"http", "https", "ftp", "ssh", "foo"} {
		in := `<a href="` + scheme + `://example.org/">foo</a>`
		assertEqual(t, in, filter(in))
	}
}

func TestShouldAllowMailtoLinks(t *testing.T) {
	in := `<a href="mailto:foo@example.org">bar</a>`
	assertEqual(t, in, filter(in))
}

func TestShouldRemoveEmptyLink(t *testing.T) {
	assertEqual(t, `<a>bar</a>`, filter(`<a href="">bar</a>`))
	assertEqual(t, `<a>bar</a>`, filter(`<a href=" ">bar</a>`))
}

func TestShouldSanitizeHTMLStrings(t *testing.T) {
	cases := [][2]string{
		{
			`<span style="color: #333; background: url('https://example.com/evil.svg')">hello</span>"`,
			`<span style="color: #333; ">hello</span>"`,
		},
		{
			`<img src="photo.jpg" style="min-width: 100px; max-width: 200px; min-height: 100px; max-height: 200px;">`,
			`<img src="photo.jpg" style="min-width: 100px; max-width: 200px; min-height: 100px; max-height: 200px;">`,
		},
		{
			`<b>Lo<!-- comment -->rem</b> <a href="pants" title="foo" style="text-decoration: underline;">ipsum</a> <a href="http://foo.com/"><strong>dolor</strong></a> sit<br/>amet <style>.foo { color: #fff; }</style> <script>alert("hello world");</script>`,
			`<b>Lorem</b> <a href="pants" title="foo" style="text-decoration: underline;">ipsum</a> <a href="http://foo.com/"><strong>dolor</strong></a> sit<br>amet .foo { color: #fff; } `,
		},
		{
			`Lo<!-- comment -->rem</b> <a href=pants title="foo>ipsum <a href="http://foo.com/"><strong>dolor</a></strong> sit<br/>amet <script>alert("hello world");`,
			// buropher は属性値の < > もエスケープする（htmldom.RenderHTML5 を参照）
			`Lorem <a href="pants" title="foo&gt;ipsum &lt;a href="><strong>dolor</strong></a> sit<br>amet `,
		},
		{
			`<p>a</p><blockquote>b`,
			`<p>a</p><blockquote>b</blockquote>`,
		},
		{
			`<b>Lo<!-- comment -->rem</b> <a href="javascript:pants" title="foo">ipsum</a> <a href="http://foo.com/"><strong>dolor</strong></a> sit<br/>amet <<foo>script>alert("hello world");</script>`,
			`<b>Lorem</b> <a title="foo">ipsum</a> <a href="http://foo.com/"><strong>dolor</strong></a> sit<br>amet &lt;script&gt;alert("hello world");`,
		},
	}
	for _, c := range cases {
		assertEqual(t, c[1], filter(c[0]))
	}
}

func TestShouldNotAllowProtocols(t *testing.T) {
	cases := map[string][2]string{
		"simple, no spaces":                  {`<a href="javascript:alert('XSS');">foo</a>`, `<a>foo</a>`},
		"simple, spaces before":              {`<a href="javascript    :alert('XSS');">foo</a>`, `<a>foo</a>`},
		"simple, spaces after":               {`<a href="javascript:    alert('XSS');">foo</a>`, `<a>foo</a>`},
		"simple, spaces before and after":    {`<a href="javascript    :   alert('XSS');">foo</a>`, `<a>foo</a>`},
		"preceding colon":                    {`<a href=":javascript:alert('XSS');">foo</a>`, `<a>foo</a>`},
		"UTF-8 encoding":                     {`<a href="javascript&#58;">foo</a>`, `<a>foo</a>`},
		"long UTF-8 encoding":                {`<a href="javascript&#0058;">foo</a>`, `<a>foo</a>`},
		"long UTF-8 encoding w/o semicolons": {`<a href=&#0000106&#0000097&#0000118&#0000097&#0000115&#0000099&#0000114&#0000105&#0000112&#0000116&#0000058&#0000097&#0000108&#0000101&#0000114&#0000116&#0000040&#0000039&#0000088&#0000083&#0000083&#0000039&#0000041>foo</a>`, `<a>foo</a>`},
		"hex encoding":                       {`<a href="javascript&#x3A;">foo</a>`, `<a>foo</a>`},
		"long hex encoding":                  {`<a href="javascript&#x003A;">foo</a>`, `<a>foo</a>`},
		"hex encoding without semicolons":    {`<a href=&#x6A&#x61&#x76&#x61&#x73&#x63&#x72&#x69&#x70&#x74&#x3A&#x61&#x6C&#x65&#x72&#x74&#x28&#x27&#x58&#x53&#x53&#x27&#x29>foo</a>`, `<a>foo</a>`},
		"null char":                          {"<img src=java\x00script:alert(\"XSS\")>", `<img>`},
		"invalid URL char":                   {`<img src=java\script:alert("XSS")>`, `<img>`},
		"spaces and entities":                {`<img src=" &#14;  javascript:alert('XSS');">`, `<img>`},
		"protocol whitespace":                {`<a href=" http://example.com/"></a>`, `<a href="http://example.com/"></a>`},
		"data images sources":                {`<img src="data:image/png;base64,foobar">`, `<img>`},
		"data URIs":                          {`<a href="data:text/html;base64,foobar">XSS</a>`, `<a>XSS</a>`},
		"vbscript URIs":                      {`<a href="vbscript:foobar">XSS</a>`, `<a>XSS</a>`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) { assertEqual(t, c[1], filter(c[0])) })
	}
}

// html_sanitizer_test.rb の移植。

func TestHTMLSanitizerSafeSchemesAppendExternalClass(t *testing.T) {
	for _, scheme := range []string{"http", "https", "ftp", "ssh", "foo"} {
		in := `<a href="` + scheme + `://example.org/">foo</a>`
		assertEqual(t, `<a href="`+scheme+`://example.org/" class="external">foo</a>`, HTML(in))
	}
}

func TestHTMLSanitizerRejectUnsafeSchemes(t *testing.T) {
	assertEqual(t, "<a>foo</a>", HTML(`<a href="javascript:alert('hello');">foo</a>`))
}

func TestHTMLSanitizerStrictTaskListItems(t *testing.T) {
	cases := [][2]string{
		{`<input type="checkbox" class="">`, ""},
		{`<input type="checkbox" class="task-list-item-checkbox other">`, ""},
		{`<input type="checkbox" class="task-list-item-checkbox" id="item1">`, `<input type="checkbox" class="task-list-item-checkbox">`},
		{`<input type="text" class="">`, ""},
		{`<input />`, ""},
		{`<ul class="other"></ul`, "<ul></ul>"},
		{`<ul class="contains-task-list"></ul`, `<ul class="contains-task-list"></ul>`},
		{`<ul class="contains-task-list" id="list1"></ul`, `<ul class="contains-task-list"></ul>`},
		{`<li class="other"></li>`, ""},
		{`<li id="other"></li>`, ""},
		{`<li class="task-list-item"></li>`, ""},
		{`<li class="task-list-item">Item 1</li>`, "Item 1"},
	}
	for _, c := range cases {
		assertEqual(t, c[1], HTML(c[0]))
	}
}

// external_links_filter_test.rb の移植。

func externalLinks(html string) string {
	frag, err := htmldom.ParseHTML5Fragment(html)
	if err != nil {
		panic(err)
	}
	frag.ScrubTopDown(func(n *htmldom.Node) bool { ExternalLink(n); return false })
	return htmldom.RenderHTML5(frag)
}

func TestExternalLinksFilter(t *testing.T) {
	assertEqual(t, `<a href="http://example.net/" class="external">link</a>`, externalLinks(`<a href="http://example.net/">link</a>`))
	assertEqual(t, `<a href="/">home</a>`, externalLinks(`<a href="/">home</a>`))
	assertEqual(t, `<a href="relative">relative</a>`, externalLinks(`<a href="relative">relative</a>`))
	assertEqual(t, `<a href="#anchor">anchor</a>`, externalLinks(`<a href="#anchor">anchor</a>`))
	assertEqual(t, `<a href="mailto:user@example.org" class="email">user</a>`, externalLinks(`<a href="mailto:user@example.org">user</a>`))
	externalLinks(`<a href="http://example.com/foo#bar#">Malformed URI</a>`)
	assertEqual(t, `<a target="_blank" href="http://example.net/" class="external" rel="noopener">link</a>`,
		externalLinks(`<a target="_blank" href="http://example.net/">link</a>`))
	assertEqual(t, `<a target="_blank" href="http://example.net/" rel="nofollow noopener" class="external">link</a>`,
		externalLinks(`<a target="_blank" href="http://example.net/" rel="nofollow">link</a>`))
}

// TestFixtures は Redmine 本体の HtmlSanitizer.call の出力と比較する。
func TestFixtures(t *testing.T) {
	f, err := fixtures.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range f.ByMode("sanitize") {
		t.Run(e.Name, func(t *testing.T) {
			assertEqual(t, fixtures.EscapeAttrAngles(e.ExpectedString()), HTML(e.Input))
		})
	}
}

func assertEqual(t *testing.T, want, got string) {
	t.Helper()
	if want != got {
		t.Errorf("\nwant: %q\n got: %q", want, got)
	}
}
