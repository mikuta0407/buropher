// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package textile

import (
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/textformat/htmldom"
)

// Redmine 7.0.1 の textile_formatter_test.rb で追加されたテストの移植。

func TestStylesTextDecoration(t *testing.T) {
	assertHTMLOutput(t, map[string]string{
		"p{text-decoration: line-through}. text": `<p style="text-decoration: line-through;">text</p>`,
	}, false)
}

// findAll は HTML5 として解析した out から条件に合う要素を集める。
func findAll(t *testing.T, out string, match func(*htmldom.Node) bool) []*htmldom.Node {
	t.Helper()
	frag, err := htmldom.ParseHTML5Fragment(out)
	if err != nil {
		t.Fatal(err)
	}
	return frag.FindAll(match)
}

func TestShouldNotAllowXSSInCodeClassAttribute(t *testing.T) {
	for _, payload := range []string{
		`<code class="x"><script>alert('XSS-1')</script></code>`,
		`<code class="foo"><img src=x onerror=alert('XSS-2')></code>`,
		`<pre><code class="unknownlang"><script>alert('XSS-3')</script></code></pre>`,
		`<code class="x"><a href="javascript:alert(1)">click</a></code>`,
		`<pre><code class="ruby"><img src=x onerror=alert(1)></code></pre>`,
	} {
		out := Format(payload, fakeOpts)
		for _, code := range findAll(t, out, func(n *htmldom.Node) bool { return n.IsElement("code") }) {
			for c := code.FirstChild; c != nil; c = c.NextSibling {
				// フェイクハイライタが出力する span 以外の要素があってはならない
				if c.Type == htmldom.ElementNode && !(c.IsElement("span") && strings.HasPrefix(c.AttrVal("class"), "hl-")) {
					t.Errorf("payload %q produced live HTML inside <code>: %s", payload, out)
				}
			}
		}
	}
}

func TestRestoreRedmineLinksShouldNotBreakOutOfAttributeValues(t *testing.T) {
	out := Format(`https://example.com/?a:"onmouseover=alert(document.domain)//"`, fakeOpts)
	if !strings.Contains(out, "&quot;") {
		t.Errorf("quotes inside the href must stay escaped: %s", out)
	}
	if n := findAll(t, out, func(n *htmldom.Node) bool { return n.HasAttr("onmouseover") }); len(n) > 0 {
		t.Errorf("restore_redmine_links injected an attribute: %s", out)
	}
}

func TestRestoreRedmineLinksShouldNotBridgeAcrossTags(t *testing.T) {
	out := Format(`https://a.com/?p:"X https://b.com/?q:"onmouseover=alert(document.domain)//"`, fakeOpts)
	if n := findAll(t, out, func(n *htmldom.Node) bool { return n.HasAttr("onmouseover") }); len(n) > 0 {
		t.Errorf("restore_redmine_links bridged across a tag boundary: %s", out)
	}
}

func TestRestoreRedmineLinksRestoresQuotedLinksInText(t *testing.T) {
	if out := Format(`version:"1.0"`, fakeOpts); !strings.Contains(out, `version:"1.0"`) {
		t.Errorf("got %s", out)
	}
	if out := Format(`version:"foo <bar>"`, fakeOpts); !strings.Contains(out, `version:"foo &lt;bar&gt;"`) {
		t.Errorf("got %s", out)
	}
}

func TestNestedNotextileTagBoundaries(t *testing.T) {
	for _, payload := range []string{
		"<<notextile>div class=foo >",
		"<</notextile>div class=foo >",
	} {
		out := Format(payload, fakeOpts)
		if n := findAll(t, out, func(n *htmldom.Node) bool { return n.AttrVal("class") == "foo" }); len(n) > 0 {
			t.Errorf("attributes restored as tag attributes for %q: %s", payload, out)
		}
		if strings.Contains(out, "<div") {
			t.Errorf("%q: %s", payload, out)
		}
	}
}
