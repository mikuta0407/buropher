// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package commonmark

import (
	"regexp"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/textformat/htmldom"
)

// Redmine の test/unit/lib/redmine/wiki_formatting/common_mark/*_test.rb の移植。

func toHTML(text string) string {
	return Format(text, Options{IconsPath: "/assets/icons-a9735328.svg"})
}

func eq(t *testing.T, want, got string) {
	t.Helper()
	if want != got {
		t.Errorf("\nwant: %q\n got: %q", want, got)
	}
}

func contains(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("%q does not contain %q", s, sub)
	}
}

var reNewlines = regexp.MustCompile(`[\r\n\t]`)

// ---- formatter_test.rb ----

func TestShouldRenderHardBreaks(t *testing.T) {
	html := "<p>foo<br>\nbar</p>"
	eq(t, html, toHTML("foo\\\nbar"))
	eq(t, html, toHTML("foo  \nbar"))
}

func TestShouldRenderSoftBreaks(t *testing.T) {
	eq(t, "<p>foo<br>\nbar</p>", toHTML("foo\nbar"))
}

func TestShouldNotRenderSoftBreaksWhenHardbreaksDisabled(t *testing.T) {
	eq(t, "<p>foo\nbar</p>", Format("foo\nbar", Options{DisableHardBreaks: true}))
}

func TestSyntaxErrorInImageReferenceShouldNotRaiseException(t *testing.T) {
	_ = toHTML("!>[](foo.png)")
}

func TestEmptyImageShouldNotRaiseException(t *testing.T) {
	_ = toHTML("![]()")
}

func TestInlineStyle(t *testing.T) {
	eq(t, "<p><strong>foo</strong></p>", toHTML("**foo**"))
}

func TestNotSetIntraEmphasis(t *testing.T) {
	eq(t, "<p>foo_bar_baz</p>", toHTML("foo_bar_baz"))
}

func TestWikiLinksShouldBePreserved(t *testing.T) {
	contains(t, toHTML("This is a wiki link: [[Foo]]"), "[[Foo]]")
}

func TestRedmineLinksWithDoubleQuotesShouldBePreserved(t *testing.T) {
	contains(t, toHTML(`This is a redmine link: version:"1.0"`), `version:"1.0"`)
}

func TestLinksByIDShouldBePreserved(t *testing.T) {
	eq(t, "<p>[project#3]</p>", toHTML("[project#3]"))
}

func TestLinksToUsersShouldBePreserved(t *testing.T) {
	for _, text := range []string{
		"[@login]", "[user:login]", "user:user@example.org", "[user:user@example.org]",
		"@user@example.org", "[@user@example.org]",
	} {
		eq(t, "<p>"+text+"</p>", toHTML(text))
	}
}

func TestFilesWithAtShouldNotEndUpAsMailtoLinks(t *testing.T) {
	for _, text := range []string{"printscreen@2x.png", "[printscreen@2x.png]"} {
		eq(t, "<p>"+text+"</p>", toHTML(text))
	}
}

func TestShouldSupportSyntaxHighlight(t *testing.T) {
	html := toHTML("~~~ruby\ndef foo\nend\n~~~\n")
	contains(t, html, `<pre><code class="ruby syntaxhl" data-language="ruby">`)
	contains(t, html, `<span class="k">def</span>`)
}

func TestShouldSupportSyntaxHighlightForLanguageWithSpecialChars(t *testing.T) {
	html := toHTML("~~~c++\nint main() {\n}\n~~~\n")
	contains(t, html, `<code class="c++ syntaxhl" data-language="c++">`)
	contains(t, html, `<span class="kt">int</span>`)
}

func TestExternalLinksShouldHaveExternalCSSClass(t *testing.T) {
	eq(t, `<p>This is a <a href="http://example.net/" class="external">link</a></p>`, toHTML("This is a [link](http://example.net/)"))
}

func TestLocalsLinksShouldNotHaveExternalCSSClass(t *testing.T) {
	eq(t, `<p>This is a <a href="/issues">link</a></p>`, toHTML("This is a [link](/issues)"))
}

func TestMarkdownShouldNotRequireSurroundedEmptyLine(t *testing.T) {
	text := "  This is a list:\n  * One\n  * Two\n"
	eq(t, "<p>This is a list:</p>\n<ul>\n<li>One</li>\n<li>Two</li>\n</ul>", toHTML(text))
}

func TestFootnotes(t *testing.T) {
	text := "This is some text[^1].\n\n[^1]: This is the foot note\n"
	expected := "<p>This is some text<sup><a href=\"#fn-1\" id=\"fnref-1\">1</a></sup>.</p>\n" +
		" <ol>\n<li id=\"fn-1\">\n<p>This is the foot note <a href=\"#fnref-1\" aria-label=\"Back to reference 1\">↩</a></p>\n</li>\n</ol>\n"
	eq(t, reNewlines.ReplaceAllString(expected, ""), strings.TrimRight(reNewlines.ReplaceAllString(toHTML(text), ""), " "))
}

var strWithPre = []string{
	"# Title\n\nLorem ipsum dolor sit amet, consectetuer adipiscing elit. Maecenas sed libero.",
	"## Heading 2\n\n~~~ruby\n  def foo\n  end\n~~~\n\nMorbi facilisis accumsan orci non pharetra.\n\n~~~ ruby\ndef foo\nend\n~~~\n\n```\nPre Content:\n\n## Inside pre\n\n<tag> inside pre block\n\nMorbi facilisis accumsan orci non pharetra.\n```",
	"### Heading 3\n\nNulla nunc nisi, egestas in ornare vel, posuere ac libero.",
}

func assertSectionWithHash(t *testing.T, expected, text string, index int) {
	t.Helper()
	got, hash := GetSection(text, index)
	eq(t, expected, got)
	eq(t, SectionHash(expected), hash)
}

func TestGetSectionShouldIgnorePreContent(t *testing.T) {
	text := strings.Join(strWithPre, "\n\n")
	assertSectionWithHash(t, strings.Join(strWithPre[1:3], "\n\n"), text, 2)
	assertSectionWithHash(t, strWithPre[2], text, 3)
}

func TestGetSectionShouldNotRecognizeDoubleHashIssueReferenceAsHeading(t *testing.T) {
	text := "## Section A\n\nThis text is a part of Section A.\n\n##1 : This is an issue reference, not an ATX heading.\n\nThis text is also a part of Section A.\n<!-- Section A ends here -->\n"
	assertSectionWithHash(t, strings.TrimSuffix(text, "\n"), text, 1)
}

func TestUpdateSectionShouldNotEscapePreContentOutsideSection(t *testing.T) {
	text := strings.Join(strWithPre, "\n\n")
	got, err := UpdateSection(text, 3, "New text", "")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, strings.Join([]string{strWithPre[0], strWithPre[1], "New text"}, "\n\n"), got)
}

func TestUpdateSectionStale(t *testing.T) {
	if _, err := UpdateSection("# A\n\nb", 1, "x", "deadbeef"); err != ErrStaleSection {
		t.Errorf("want ErrStaleSection, got %v", err)
	}
	_, hash := GetSection("# A\n\nb", 1)
	if _, err := UpdateSection("# A\n\nb", 1, "x", hash); err != nil {
		t.Errorf("unexpected error %v", err)
	}
}

func TestShouldEmphasizeText(t *testing.T) {
	eq(t, "<p>This <em>text</em> should be emphasized</p>", toHTML("This _text_ should be emphasized"))
}

func TestShouldStrikeThroughText(t *testing.T) {
	eq(t, "<p>This <del>text</del> should be striked through</p>", toHTML("This ~~text~~ should be striked through"))
}

func TestShouldAutolinkURLsAndEmails(t *testing.T) {
	cases := [][2]string{
		{"http://example.org", `<p><a href="http://example.org" class="external">http://example.org</a></p>`},
		{"http://www.redmine.org/projects/redmine/issues?utf8=✓",
			`<p><a href="http://www.redmine.org/projects/redmine/issues?utf8=%E2%9C%93" class="external">http://www.redmine.org/projects/redmine/issues?utf8=✓</a></p>`},
		{"[Letters](https://yandex.ru/search/?text=кол-во)", `<p><a href="https://yandex.ru/search/?text=%D0%BA%D0%BE%D0%BB-%D0%B2%D0%BE" class="external">Letters</a></p>`},
		{"www.example.org", `<p><a href="http://www.example.org" class="external">www.example.org</a></p>`},
		{"user@example.org", `<p><a href="mailto:user@example.org" class="email">user@example.org</a></p>`},
	}
	for _, c := range cases {
		eq(t, c[1], toHTML(c[0]))
	}
}

func TestShouldSupportHTMLTables(t *testing.T) {
	eq(t, "<table><tr><td>Cell</td></tr></table>", toHTML(`<table style="background: red"><tr><td>Cell</td></tr></table>`))
}

func TestShouldRemoveUnsafeURIs(t *testing.T) {
	eq(t, "<img>", toHTML(`<img src="data:foobar">`))
	eq(t, "<p><a>click me</a></p>", toHTML(`<a href="javascript:bla">click me</a>`))
}

func TestShouldEscapeUnwantedTags(t *testing.T) {
	eq(t, `<p>sit<br>amet &lt;style&gt;.foo { color: #fff; }&lt;/style&gt; &lt;script&gt;alert("hello world");&lt;/script&gt;</p>`,
		toHTML(`sit<br/>amet <style>.foo { color: #fff; }</style> <script>alert("hello world");</script>`))
}

func TestShouldSupportTaskList(t *testing.T) {
	text := "Task list:\n* [ ] Task 1\n* [x] Task 2\n"
	expected := "<p>Task list:</p>\n<ul class=\"contains-task-list\">\n<li class=\"task-list-item\">\n" +
		"<input type=\"checkbox\" class=\"task-list-item-checkbox\" disabled> Task 1\n</li>\n<li class=\"task-list-item\">\n" +
		"<input type=\"checkbox\" class=\"task-list-item-checkbox\" checked disabled> Task 2</li>\n</ul>\n"
	eq(t, reNewlines.ReplaceAllString(expected, ""), strings.TrimRight(reNewlines.ReplaceAllString(toHTML(text), ""), " "))
}

func TestShouldRenderAlertBlocks(t *testing.T) {
	text := "> [!note]\n> This is a note.\n\n> [!tip]\n> This is a tip.\n\n> [!warning]\n> This is a warning.\n\n> [!caution]\n> This is a caution.\n\n> [!important]\n> This is a important.\n"
	html := toHTML(text)
	for _, alert := range []string{"note", "tip", "warning", "caution", "important"} {
		icon := alertIcons[alert]
		re := regexp.MustCompile(`<div class="markdown-alert markdown-alert-` + alert + `">\n<p class="markdown-alert-title"><svg class="s18 icon-svg" aria-hidden="true"><use href="/assets/icons-\w+.svg#icon--` + icon +
			`"></use></svg><span class="icon-label">` + strings.ToUpper(alert[:1]) + alert[1:] + `</span></p>\n<p>This is a ` + alert + `.</p>\n</div>`)
		if !re.MatchString(html) {
			t.Errorf("alert %s not rendered: %q", alert, html)
		}
	}
}

func TestShouldNotRenderUnknownAlertType(t *testing.T) {
	html := toHTML("> [!unknown]\n> This should not become an alert.\n")
	contains(t, html, "<blockquote>")
	contains(t, html, "[!unknown]")
	contains(t, html, "This should not become an alert.")
	if strings.Contains(html, "markdown-alert") {
		t.Errorf("unexpected alert: %q", html)
	}
}

// ---- markdown_filter_test.rb ----

func TestMarkdownFilterShouldRenderMarkdown(t *testing.T) {
	eq(t, "<p><strong>bold</strong></p>", MarkdownToHTML("**bold**", true))
}

// ---- fixup_auto_links_filter_test.rb ----

func fixup(markdown string) string {
	frag := htmldom.ParseFragment(MarkdownToHTML(markdown, true))
	FixupAutoLinksFilter(frag)
	return htmldom.Render(frag)
}

func TestShouldFixupAutolinkedUserReferences(t *testing.T) {
	eq(t, "<p>user:user@example.org</p>", fixup("user:user@example.org"))
	eq(t, "<p>@user@example.org</p>", fixup("@user@example.org"))
}

func TestShouldFixupAutolinkedHiresFiles(t *testing.T) {
	eq(t, "<p>printscreen@2x.png</p>", fixup("printscreen@2x.png"))
}

// ---- alerts_icons_filter_test.rb ----

func alertsFilter(markdown string, opts Options) string {
	frag := htmldom.ParseFragment(MarkdownToHTML(markdown, true))
	AlertsIconsFilter(frag, opts)
	return htmldom.Render(frag)
}

func TestShouldRenderAlertBlocksWithLocalizedLabels(t *testing.T) {
	ja := func(key string) string {
		return map[string]string{"label_alert_note": "注記"}[key]
	}
	html := alertsFilter("> [!note]\n> This is a note.\n", Options{Translate: ja})
	contains(t, html, `<span class="icon-label">注記</span>`)
}

func TestShouldNotTranslateTitleIfOverridden(t *testing.T) {
	ja := func(key string) string {
		return map[string]string{"label_alert_note": "注記"}[key]
	}
	html := alertsFilter("> [!note] Custom Note Title\n> This is a note.\n", Options{Translate: ja})
	contains(t, html, `<span class="icon-label">Custom Note Title</span>`)
}

// ---- syntax_highlight_filter_test.rb ----

func highlightFilter(html string) string {
	frag := htmldom.ParseFragment(html)
	SyntaxHighlightFilter(frag)
	return htmldom.Render(frag)
}

func TestShouldHighlightSupportedLanguage(t *testing.T) {
	input := "<pre><code class=\"language-ruby\">\ndef foo\nend\n</code></pre>\n"
	expected := "<pre><code class=\"ruby syntaxhl\" data-language=\"ruby\">\n<span class=\"k\">def</span> <span class=\"nf\">foo</span>\n<span class=\"k\">end</span>\n</code></pre>\n"
	eq(t, expected, highlightFilter(input))
}

func TestShouldHighlightSupportedLanguageWithSpecialChars(t *testing.T) {
	input := "<pre><code class=\"language-c-k&amp;r\">\nint i;\n</code></pre>\n"
	expected := "<pre><code data-language=\"c-k&amp;r\">\nint i;\n</code></pre>\n"
	eq(t, expected, highlightFilter(input))
}

func TestShouldStripCodeClassAndPreserveDataLanguageAttrForUnknownLanguage(t *testing.T) {
	input := "<pre><code class=\"language-foobar\">\ndef foo\nend\n</code></pre>\n"
	expected := "<pre><code data-language=\"foobar\">\ndef foo\nend\n</code></pre>\n"
	eq(t, expected, highlightFilter(input))
}

func TestShouldIgnoreCodeWithoutClass(t *testing.T) {
	input := "<pre><code>\ndef foo\nend\n</code></pre>\n"
	eq(t, input, highlightFilter(input))
}
