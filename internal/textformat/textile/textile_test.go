// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package textile

// Redmine 6.1.2 の test/unit/lib/redmine/wiki_formatting/textile_formatter_test.rb の移植。

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// rougeLikeHighlighter は Rouge の出力を模したテスト用ハイライタ (ruby のみサポート)。
func rougeLikeHighlighter(lang, code string) (string, bool) {
	if lang != "ruby" {
		return "", false
	}
	return `<span class="nb">` + htmlEscapeERB(code) + `</span>`, true
}

var testOpts = &Options{Highlight: rougeLikeHighlighter}

func toHTML(s string) string { return Format(s, testOpts) }

// assertHTMLOutput は assert_html_output 相当。
// preWrapper は CopypreScrubber が pre を包む div（テストのアイコンパスは既定値）。
func preWrapper(pre string) string {
	return `<div class="pre-wrapper" data-controller="clipboard"><a class="copy-pre-content-link icon-only" title="Copy" data-action="clipboard#copyPre">` +
		`<svg class="s18 icon-svg" aria-hidden="true"><use href="/assets/icons.svg#icon--copy-pre-content"></use></svg></a>` + pre + `</div>`
}

func assertHTMLOutput(t *testing.T, cases map[string]string, expectParagraph bool) {
	t.Helper()
	for text, expected := range cases {
		if expectParagraph {
			expected = "<p>" + expected + "</p>"
		}
		if got := toHTML(text); got != expected {
			t.Errorf("Formatting the following text failed:\n===\n%s\n===\nwant: %q\ngot:  %q", text, expected, got)
		}
	}
}

var reAllSpace = regexp.MustCompile(`\s+`)
var reCRLFTab = regexp.MustCompile(`[\r\n\t]`)

func assertNoSpaceEqual(t *testing.T, expected, raw string) {
	t.Helper()
	want := reAllSpace.ReplaceAllString(expected, "")
	got := reAllSpace.ReplaceAllString(toHTML(raw), "")
	if want != got {
		t.Errorf("input:\n%s\nwant: %s\ngot:  %s", raw, want, got)
	}
}

func assertNoCRLFTabEqual(t *testing.T, expected, raw string) {
	t.Helper()
	want := reCRLFTab.ReplaceAllString(expected, "")
	got := reCRLFTab.ReplaceAllString(toHTML(raw), "")
	if want != got {
		t.Errorf("input:\n%s\nwant: %s\ngot:  %s", raw, want, got)
	}
}

var modifiers = [][2]string{
	{"*", "strong"}, // bold
	{"_", "em"},     // italic
	{"+", "ins"},    // underline
	{"-", "del"},    // deleted
	{"^", "sup"},    // superscript
	{"~", "sub"},    // subscript
}

func TestModifiers(t *testing.T) {
	assertHTMLOutput(t, map[string]string{
		"*bold*":            "<strong>bold</strong>",
		"before *bold*":     "before <strong>bold</strong>",
		"*bold* after":      "<strong>bold</strong> after",
		"*two words*":       "<strong>two words</strong>",
		"*two*words*":       "<strong>two*words</strong>",
		"*two * words*":     "<strong>two * words</strong>",
		"*two* *words*":     "<strong>two</strong> <strong>words</strong>",
		"*(two)* *(words)*": "<strong>(two)</strong> <strong>(words)</strong>",
	}, true)
}

func TestModifiersCombination(t *testing.T) {
	for _, m1 := range modifiers {
		for _, m2 := range modifiers {
			if m1[0] == m2[0] {
				continue
			}
			text := m2[0] + m1[0] + "Phrase modifiers" + m1[0] + m2[0]
			html := "<" + m2[1] + "><" + m1[1] + ">Phrase modifiers</" + m1[1] + "></" + m2[1] + ">"
			assertHTMLOutput(t, map[string]string{text: html}, true)
		}
	}
}

func TestModifierShouldWorkWithOneNonASCIICharacter(t *testing.T) {
	assertHTMLOutput(t, map[string]string{"*Ä*": "<strong>Ä</strong>"}, true)
}

func TestStyles(t *testing.T) {
	// single style
	assertHTMLOutput(t, map[string]string{
		"p{color:red}. text":         `<p style="color:red;">text</p>`,
		"p{color:red;}. text":        `<p style="color:red;">text</p>`,
		"p{color: red}. text":        `<p style="color: red;">text</p>`,
		"p{color:#f00}. text":        `<p style="color:#f00;">text</p>`,
		"p{color:#ff0000}. text":     `<p style="color:#ff0000;">text</p>`,
		"p{border:10px}. text":       `<p style="border:10px;">text</p>`,
		"p{border:10}. text":         `<p style="border:10;">text</p>`,
		"p{border:10%}. text":        `<p style="border:10%;">text</p>`,
		"p{border:10em}. text":       `<p style="border:10em;">text</p>`,
		"p{border:1.5em}. text":      `<p style="border:1.5em;">text</p>`,
		"p{border-left:1px}. text":   `<p style="border-left:1px;">text</p>`,
		"p{border-right:1px}. text":  `<p style="border-right:1px;">text</p>`,
		"p{border-top:1px}. text":    `<p style="border-top:1px;">text</p>`,
		"p{border-bottom:1px}. text": `<p style="border-bottom:1px;">text</p>`,
		"p{width:50px}. text":        `<p style="width:50px;">text</p>`,
		"p{max-width:100px}. text":   `<p style="max-width:100px;">text</p>`,
		"p{height:40px}. text":       `<p style="height:40px;">text</p>`,
		"p{max-height:80px}. text":   `<p style="max-height:80px;">text</p>`,
	}, false)

	// multiple styles
	assertHTMLOutput(t, map[string]string{
		"p{color:red; border-top:1px}. text":  `<p style="color:red;border-top:1px;">text</p>`,
		"p{color:red ; border-top:1px}. text": `<p style="color:red;border-top:1px;">text</p>`,
		"p{color:red;border-top:1px}. text":   `<p style="color:red;border-top:1px;">text</p>`,
	}, false)

	// styles with multiple values
	assertHTMLOutput(t, map[string]string{
		"p{border:1px solid red;}. text":             `<p style="border:1px solid red;">text</p>`,
		"p{border-top-left-radius: 10px 5px;}. text": `<p style="border-top-left-radius: 10px 5px;">text</p>`,
	}, false)
}

func TestInvalidStylesShouldBeFiltered(t *testing.T) {
	assertHTMLOutput(t, map[string]string{
		"p{invalid}. text":                `<p>text</p>`,
		"p{invalid:red}. text":            `<p>text</p>`,
		"p{color:(red)}. text":            `<p>text</p>`,
		"p{color:red;invalid:blue}. text": `<p style="color:red;">text</p>`,
		"p{invalid:blue;color:red}. text": `<p style="color:red;">text</p>`,
		`p{color:"}. text`:                `<p>p{color:"}. text</p>`,
	}, false)
}

func TestInlineCode(t *testing.T) {
	assertHTMLOutput(t, map[string]string{
		"this is @some code@":   "this is <code>some code</code>",
		"@<Location /redmine>@": "<code>&lt;Location /redmine&gt;</code>",
	}, true)
}

func TestLangAttribute(t *testing.T) {
	assertHTMLOutput(t, map[string]string{
		"*[fr]French*":    `<strong lang="fr">French</strong>`,
		"*[fr-fr]French*": `<strong lang="fr-fr">French</strong>`,
		"*[fr_fr]French*": `<strong lang="fr_fr">French</strong>`,
	}, true)
}

func TestLangAttributeShouldIgnoreInvalidValue(t *testing.T) {
	assertHTMLOutput(t, map[string]string{"*[fr3]French*": "<strong>[fr3]French</strong>"}, true)
}

func TestNestedLists(t *testing.T) {
	raw := "# Item 1\n# Item 2\n** Item 2a\n** Item 2b\n# Item 3\n** Item 3a\n"
	expected := `<ol>
  <li>Item 1</li>
  <li>Item 2
    <ul>
      <li>Item 2a</li>
      <li>Item 2b</li>
    </ul>
  </li>
  <li>Item 3
    <ul>
      <li>Item 3a</li>
    </ul>
  </li>
</ol>`
	assertNoSpaceEqual(t, expected, raw)

	raw = "* Item-1\n\n  * Item-1a\n  * Item-1b\n"
	expected = `<ul>
  <li>Item-1
    <ul>
      <li>Item-1a</li>
      <li>Item-1b</li>
    </ul>
  </li>
</ul>`
	assertNoSpaceEqual(t, expected, raw)
}

func TestEscaping(t *testing.T) {
	assertHTMLOutput(t, map[string]string{"this is a <script>": "this is a &lt;script&gt;"}, true)
}

func TestKbd(t *testing.T) {
	assertHTMLOutput(t, map[string]string{"<kbd>test</kbd>": "<kbd>test</kbd>"}, false)
}

func TestUseOfBackslashesFollowedByNumbersInHeaders(t *testing.T) {
	assertHTMLOutput(t, map[string]string{`h1. 2009\02\09`: `<h1>2009\02\09</h1>`}, false)
}

func TestDoubleDashesShouldNotStrikethrough(t *testing.T) {
	assertHTMLOutput(t, map[string]string{
		"double -- dashes -- test":   "double -- dashes -- test",
		"double -- *dashes* -- test": "double -- <strong>dashes</strong> -- test",
	}, true)
}

func TestAbbreviations(t *testing.T) {
	assertHTMLOutput(t, map[string]string{
		"this is an abbreviation: GPL(General Public License)": `this is an abbreviation: <abbr title="General Public License">GPL</abbr>`,
		"2 letters JP(Jean-Philippe) abbreviation":             `2 letters <abbr title="Jean-Philippe">JP</abbr> abbreviation`,
		`GPL(This is a double-quoted "title")`:                 `<abbr title="This is a double-quoted &quot;title&quot;">GPL</abbr>`,
	}, true)
}

func TestBlockquote(t *testing.T) {
	raw := `John said:
> Lorem ipsum dolor sit amet, consectetuer adipiscing elit. Maecenas sed libero.
> Nullam commodo metus accumsan nulla. Curabitur lobortis dui id dolor.
> * Donec odio lorem,
> * sagittis ac,
> * malesuada in,
> * adipiscing eu, dolor.
>
> >Nulla varius pulvinar diam. Proin id arcu id lorem scelerisque condimentum. Proin vehicula turpis vitae lacus.
> Proin a tellus. Nam vel neque.

He's right.
`
	expected := `<p>John said:</p>
<blockquote>
Lorem ipsum dolor sit amet, consectetuer adipiscing elit. Maecenas sed libero.<br>
Nullam commodo metus accumsan nulla. Curabitur lobortis dui id dolor.
<ul>
  <li>Donec odio lorem,</li>
  <li>sagittis ac,</li>
  <li>malesuada in,</li>
  <li>adipiscing eu, dolor.</li>
</ul>
<blockquote>
<p>Nulla varius pulvinar diam. Proin id arcu id lorem scelerisque condimentum. Proin vehicula turpis vitae lacus.</p>
</blockquote>
<p>Proin a tellus. Nam vel neque.</p>
</blockquote>
<p>He's right.</p>
`
	assertNoSpaceEqual(t, expected, raw)
}

func TestTable(t *testing.T) {
	raw := "This is a table with empty cells:\n\n|cell11|cell12||\n|cell21||cell23|\n|cell31|cell32|cell33|\n"
	expected := `<p>This is a table with empty cells:</p>
<table><tbody>
  <tr><td>cell11</td><td>cell12</td><td></td></tr>
  <tr><td>cell21</td><td></td><td>cell23</td></tr>
  <tr><td>cell31</td><td>cell32</td><td>cell33</td></tr>
</tbody></table>`
	assertNoSpaceEqual(t, expected, raw)
}

func TestTableWithAlignment(t *testing.T) {
	raw := "|>. right|\n|<. left|\n|<>. justify|\n"
	expected := `<table><tbody>
  <tr><td style="text-align:right;">right</td></tr>
  <tr><td style="text-align:left;">left</td></tr>
  <tr><td style="text-align:justify;">justify</td></tr>
</tbody></table>`
	assertNoSpaceEqual(t, expected, raw)
}

func TestTableWithTrailingWhitespace(t *testing.T) {
	raw := "This is a table with trailing whitespace in one row:\n\n|cell11|cell12|\n|cell21|cell22| \n|cell31|cell32|\n"
	expected := `<p>This is a table with trailing whitespace in one row:</p>
<table><tbody>
  <tr><td>cell11</td><td>cell12</td></tr>
  <tr><td>cell21</td><td>cell22</td></tr>
  <tr><td>cell31</td><td>cell32</td></tr>
</tbody></table>`
	assertNoSpaceEqual(t, expected, raw)
}

func TestTableWithLineBreaks(t *testing.T) {
	raw := "This is a table with line breaks:\n\n|cell11\ncontinued|cell12||\n|-cell21-||cell23\ncell23 line2\ncell23 *line3*|\n|cell31|cell32\ncell32 line2|cell33|\n\n"
	expected := `<p>This is a table with line breaks:</p>
<table><tbody>
  <tr>
    <td>cell11<br>continued</td>
    <td>cell12</td>
    <td></td>
  </tr>
  <tr>
    <td><del>cell21</del></td>
    <td></td>
    <td>cell23<br>cell23 line2<br>cell23 <strong>line3</strong></td>
  </tr>
  <tr>
    <td>cell31</td>
    <td>cell32<br>cell32 line2</td>
    <td>cell33</td>
  </tr>
</tbody></table>`
	assertNoSpaceEqual(t, expected, raw)
}

func TestTablesWithLists(t *testing.T) {
	raw := "This is a table with lists:\n\n|cell11|cell12|\n|cell21|ordered list\n# item\n# item 2|\n|cell31|unordered list\n* item\n* item 2|\n\n"
	expected := `<p>This is a table with lists:</p>
<table><tbody>
  <tr>
    <td>cell11</td>
    <td>cell12</td>
  </tr>
  <tr>
    <td>cell21</td>
    <td>ordered list<br># item<br># item 2</td>
  </tr>
  <tr>
    <td>cell31</td>
    <td>unordered list<br>* item<br>* item 2</td>
  </tr>
</tbody></table>`
	assertNoSpaceEqual(t, expected, raw)
}

func TestTextileShouldNotMangleBrackets(t *testing.T) {
	if got := toHTML("[msg1][msg2]"); got != "<p>[msg1][msg2]</p>" {
		t.Errorf("got %q", got)
	}
}

func TestTextileShouldEscapeImageURLs(t *testing.T) {
	// onclick="alert('XSS');" をエンコードしたもの
	raw := `!/images/comment.png"onclick=&#x61;&#x6c;&#x65;&#x72;&#x74;&#x28;&#x27;&#x58;&#x53;&#x53;&#x27;&#x29;;&#x22;!`
	expected := `<p><img src="/images/comment.png&quot;onclick=` +
		`&amp;#x61;&amp;#x6c;&amp;#x65;&amp;#x72;&amp;#x74;&amp;#x28;` +
		`&amp;#x27;&amp;#x58;&amp;#x53;&amp;#x53;&amp;#x27;&amp;#x29;;&amp;#x22;" alt=""></p>`
	assertNoSpaceEqual(t, expected, raw)
}

var strWithoutPre = []string{
	// 0
	"h1. Title\n\nLorem ipsum dolor sit amet, consectetuer adipiscing elit. Maecenas sed libero.",
	// 1
	"h2. Heading 2\n\nMaecenas sed elit sit amet mi accumsan vestibulum non nec velit. Proin porta tincidunt lorem, consequat rhoncus dolor fermentum in.\n\nCras ipsum felis, ultrices at porttitor vel, faucibus eu nunc.",
	// 2
	"h2. Heading 2\n\nMorbi facilisis accumsan orci non pharetra.\n\nh3. Heading 3\n\nNulla nunc nisi, egestas in ornare vel, posuere ac libero.",
	// 3
	"h3. Heading 3\n\nPraesent eget turpis nibh, a lacinia nulla.",
	// 4
	"h2. Heading 2\n\nUt rhoncus elementum adipiscing.",
}

var textWithoutPre = strings.Join(strWithoutPre, "\n\n")

func joinSections(parts ...any) string {
	var res []string
	for _, p := range parts {
		switch v := p.(type) {
		case string:
			res = append(res, v)
		case []string:
			res = append(res, v...)
		}
	}
	return strings.Join(res, "\n\n")
}

func assertSectionWithHash(t *testing.T, expected, text string, index int) {
	t.Helper()
	sec, hash := GetSection(text, index)
	if sec != expected {
		t.Errorf("section content did not match (index %d)\nwant %q\ngot  %q", index, expected, sec)
	}
	if hash != SectionHash(expected) {
		t.Errorf("section hash did not match (index %d)", index)
	}
}

func TestGetSectionShouldReturnTheRequestedSectionAndItsHash(t *testing.T) {
	assertSectionWithHash(t, strWithoutPre[1], textWithoutPre, 2)
	assertSectionWithHash(t, joinSections(strWithoutPre[2:4]), textWithoutPre, 3)
	assertSectionWithHash(t, strWithoutPre[3], textWithoutPre, 5)
	assertSectionWithHash(t, strWithoutPre[4], textWithoutPre, 6)

	assertSectionWithHash(t, "", textWithoutPre, 0)
	assertSectionWithHash(t, "", textWithoutPre, 10)
}

func mustUpdate(t *testing.T, text string, index int, update, hash string) string {
	t.Helper()
	s, err := UpdateSection(text, index, update, hash)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestUpdateSectionShouldUpdateTheRequestedSection(t *testing.T) {
	r := "New text"
	cases := []struct {
		index int
		want  string
	}{
		{2, joinSections(strWithoutPre[0], r, strWithoutPre[2:5])},
		{3, joinSections(strWithoutPre[0:2], r, strWithoutPre[4])},
		{5, joinSections(strWithoutPre[0:3], r, strWithoutPre[4])},
		{6, joinSections(strWithoutPre[0:4], r)},
		{0, textWithoutPre},
		{10, textWithoutPre},
	}
	for _, c := range cases {
		if got := mustUpdate(t, textWithoutPre, c.index, r, ""); got != c.want {
			t.Errorf("index %d\nwant %q\ngot  %q", c.index, c.want, got)
		}
	}
}

func TestUpdateSectionWithHashShouldUpdateTheRequestedSection(t *testing.T) {
	r := "New text"
	want := joinSections(strWithoutPre[0], r, strWithoutPre[2:5])
	if got := mustUpdate(t, textWithoutPre, 2, r, SectionHash(strWithoutPre[1])); got != want {
		t.Errorf("want %q\ngot  %q", want, got)
	}
}

func TestUpdateSectionWithWrongHashShouldRaiseAnError(t *testing.T) {
	_, err := UpdateSection(textWithoutPre, 2, "New text", SectionHash("Old text"))
	if !errors.Is(err, ErrStaleSection) {
		t.Errorf("want ErrStaleSection, got %v", err)
	}
}

var strWithPre = []string{
	// 0
	"h1. Title\n\nLorem ipsum dolor sit amet, consectetuer adipiscing elit. Maecenas sed libero.",
	// 1
	"h2. Heading 2\n\n<pre><code class=\"ruby\">\n  def foo\n  end\n</code></pre>\n\n<pre><code><pre><code class=\"ruby\">\n  Place your code here.\n</code></pre>\n</code></pre>\n\nMorbi facilisis accumsan orci non pharetra.\n\n<pre>\nPre Content:\n\nh2. Inside pre\n\n<tag> inside pre block\n\nMorbi facilisis accumsan orci non pharetra.\n</pre>",
	// 2
	"h3. Heading 3\n\nNulla nunc nisi, egestas in ornare vel, posuere ac libero.",
}

func TestGetSectionShouldIgnorePreContent(t *testing.T) {
	text := strings.Join(strWithPre, "\n\n")
	assertSectionWithHash(t, joinSections(strWithPre[1:3]), text, 2)
	assertSectionWithHash(t, strWithPre[2], text, 3)
}

func TestUpdateSectionShouldNotEscapePreContentOutsideSection(t *testing.T) {
	text := strings.Join(strWithPre, "\n\n")
	want := joinSections(strWithPre[0:2], "New text")
	if got := mustUpdate(t, text, 3, "New text", ""); got != want {
		t.Errorf("want %q\ngot  %q", want, got)
	}
}

func TestGetSectionShouldSupportLinesWithSpacesBeforeHeading(t *testing.T) {
	// Content 2 と Heading 4 の後の行は空白を含む
	text := "h1. Heading 1\n\nContent 1\n\nh1. Heading 2\n\nContent 2\n \nh1. Heading 3\n\nContent 3\n\nh1. Heading 4\n \nContent 4\n"
	for index := 1; index <= 4; index++ {
		sec, _ := GetSection(text, index)
		re := regexp.MustCompile(fmt.Sprintf(`(?s)\Ah1. Heading %d.+Content %d`, index, index))
		if !re.MatchString(sec) {
			t.Errorf("index %d: got %q", index, sec)
		}
	}
}

func TestGetSectionShouldSupportHeadingsStartingWithATab(t *testing.T) {
	text := "h1.\tHeading 1\n\nContent 1\n\nh1. Heading 2\n\nContent 2\n"
	sec, _ := GetSection(text, 1)
	if !regexp.MustCompile(`\Ah1.\tHeading 1\s+Content 1\z`).MatchString(sec) {
		t.Errorf("got %q", sec)
	}
}

func TestShouldNotAllowArbitraryClassAttributeOnOfftags(t *testing.T) {
	cases := [][2]string{
		{`class="foo"`, `data-language="foo"`},
		{`class='foo'`, `data-language="foo"`},
		{`class="ruby foo"`, `data-language="ruby foo"`},
		{`class='ruby foo'`, `data-language="ruby foo"`},
		{`class="ruby "foo" bar"`, `data-language="ruby "`},
	}
	for _, c := range cases {
		assertHTMLOutput(t, map[string]string{"<code " + c[0] + ">test</code>": "<code " + c[1] + ">test</code>"}, false)
		assertHTMLOutput(t, map[string]string{"<pre " + c[0] + ">test</pre>": preWrapper(`<pre data-clipboard-target="pre">test</pre>`)}, false)
		assertHTMLOutput(t, map[string]string{"<kbd " + c[0] + ">test</kbd>": "<kbd>test</kbd>"}, false)
	}
	assertHTMLOutput(t, map[string]string{
		`<notextile class="foo">test</notextile>`:            "test",
		`<notextile class='foo'>test</notextile>`:            "test",
		`<notextile class="ruby foo">test</notextile>`:       "test",
		`<notextile class='ruby foo'>test</notextile>`:       "test",
		`<notextile class="ruby "foo" bar">test</notextile>`: "test",
	}, false)
}

func TestShouldAllowValidLanguageClassAttributeOnCodeTags(t *testing.T) {
	// 言語名がダブルクォート
	assertHTMLOutput(t, map[string]string{
		`<code class="ruby">test</code>`: `<code class="ruby syntaxhl" data-language="ruby"><span class="nb">test</span></code>`,
	}, false)
	// 言語名がシングルクォート
	assertHTMLOutput(t, map[string]string{
		`<code class='ruby'>test</code>`: `<code class="ruby syntaxhl" data-language="ruby"><span class="nb">test</span></code>`,
	}, false)
}

func TestShouldPreserveCodeLanguageClassAttributeInDataLanguage(t *testing.T) {
	assertHTMLOutput(t, map[string]string{
		`<code class="foolang">unsupported language</code>`: `<code data-language="foolang">unsupported language</code>`,
		`<code class="c-k&r">special-char language</code>`:  `<code data-language="c-k&amp;r">special-char language</code>`,
	}, false)
}

func TestShouldNotAllowValidLanguageClassAttributeOnNonCodeOfftags(t *testing.T) {
	assertHTMLOutput(t, map[string]string{`<pre class="ruby">test</pre>`: preWrapper(`<pre data-clipboard-target="pre">test</pre>`)}, false)
	assertHTMLOutput(t, map[string]string{`<kbd class="ruby">test</kbd>`: "<kbd>test</kbd>"}, false)
	assertHTMLOutput(t, map[string]string{`<notextile class="ruby">test</notextile>`: "test"}, false)
}

func TestShouldPrefixClassAttributeOnTags(t *testing.T) {
	assertHTMLOutput(t, map[string]string{
		"!(foo)test.png!": `<p><img src="test.png" class="wiki-class-foo" alt=""></p>`,
		"%(foo)test%":     `<p><span class="wiki-class-foo">test</span></p>`,
		"p(foo). test":    `<p class="wiki-class-foo">test</p>`,
		"|(foo). test|":   "<table>\n\t\t<tbody><tr>\n\t\t\t<td class=\"wiki-class-foo\">test</td>\n\t\t</tr>\n\t</tbody></table>",
	}, false)
}

func TestShouldPrefixIDAttributeOnTags(t *testing.T) {
	assertHTMLOutput(t, map[string]string{
		"!(#foo)test.png!": `<p><img src="test.png" id="wiki-id-foo" alt=""></p>`,
		"%(#foo)test%":     `<p><span id="wiki-id-foo">test</span></p>`,
		"p(#foo). test":    `<p id="wiki-id-foo">test</p>`,
		"|(#foo). test|":   "<table>\n\t\t<tbody><tr>\n\t\t\t<td id=\"wiki-id-foo\">test</td>\n\t\t</tr>\n\t</tbody></table>",
	}, false)
}

func TestShouldNotPrefixClassAndIDAttributesAlreadyPrefixed(t *testing.T) {
	assertHTMLOutput(t, map[string]string{
		"!(wiki-class-foo#wiki-id-bar)test.png!": `<p><img src="test.png" class="wiki-class-foo" id="wiki-id-bar" alt=""></p>`,
	}, false)
}

func TestFootnotes(t *testing.T) {
	text := "This is some text[1].\n\nfn1. This is the foot note\n"
	expected := `<p>This is some text<sup><a href="#fn1">1</a></sup>.</p>
<p id="fn1" class="footnote"><sup>1</sup> This is the foot note</p>
`
	assertNoCRLFTabEqual(t, expected, text)
}

func TestShouldNotCrashWithSpecialInput(t *testing.T) {
	toHTML(" \f")
	toHTML(" \v")
}

func TestShouldNotHandleAsPreformattedTextTagsThatStartsWithPre(t *testing.T) {
	text := "<pree>\n  This is some text\n</pree>\n"
	expected := "<p>&lt;pree&gt;<br>\n  This is some text<br>\n&lt;/pree&gt;</p>\n"
	assertNoCRLFTabEqual(t, expected, text)
}

func TestShouldEscapeTagsThatStartWithPre(t *testing.T) {
	assertNoCRLFTabEqual(t, "<p>&lt;preä demo&gt;Text</p>\n", "<preä demo>Text\n")
}

func TestShouldRemoveHTMLComments(t *testing.T) {
	text := `<!-- begin -->
Hello <!-- comment between words -->world.

<!--
  multi-line
comment -->Foo

<pre>
This is a code block.
<p>
<!-- comments in a code block should be preserved -->
</p>
</pre>
`
	expected := `<p>Hello world.</p>

<p>Foo</p>

` + preWrapper(`<pre data-clipboard-target="pre">
This is a code block.
&lt;p&gt;
&lt;!-- comments in a code block should be preserved --&gt;
&lt;/p&gt;
</pre>`) + `

`
	assertNoCRLFTabEqual(t, expected, text)
}

func TestShouldEscapeBqCitations(t *testing.T) {
	assertHTMLOutput(t, map[string]string{
		`bq.:http://x/"onmouseover="alert(document.domain) Hover me`: "<blockquote cite=\"http://x/&quot;onmouseover=&quot;alert(document.domain)\">\n\t\t<p>Hover me</p>\n\t</blockquote>",
	}, false)
}

func TestShouldAllowMultipleFootnotes(t *testing.T) {
	text := "Some demo[1][2] And a sentence.[1]\n\nfn1. One\n\nfn2. Two\n"
	expected := `<p>Some demo<sup><a href="#fn1">1</a></sup><sup><a href="#fn2">2</a></sup> And a sentence.[1]</p>
<p id="fn1" class="footnote"><sup>1</sup> One</p>
<p id="fn2" class="footnote"><sup>2</sup> Two</p>
`
	assertNoCRLFTabEqual(t, expected, text)
}

// application_helper_test.rb の textile 関連のうちフォーマッタ単体で確認できるもの
// (textilizable 側の処理を含まないため、期待値は Formatter#to_html の出力に合わせている)

func TestSyntaxHighlightAmpersand(t *testing.T) {
	// test_syntax_highlight_ampersand_in_textile 相当 (x%x% の復元)
	got := toHTML("<pre><code class=\"ruby\">\nx = a & b\n</code></pre>")
	want := preWrapper("<pre data-clipboard-target=\"pre\"><code class=\"ruby syntaxhl\" data-language=\"ruby\"><span class=\"nb\">x = a &amp; b\n</span></code></pre>")
	if got != want {
		t.Errorf("want %q\ngot  %q", want, got)
	}
}

func TestNotextileTags(t *testing.T) {
	assertHTMLOutput(t, map[string]string{
		"<notextile>no *textile* formatting</notextile>":  "no *textile* formatting",
		"<notextile>this is <tag>a tag</tag></notextile>": "this is &lt;tag&gt;a tag&lt;/tag&gt;",
	}, false)
}

func TestNilHighlighter(t *testing.T) {
	got := Format(`<code class="ruby">x</code>`, nil)
	if got != `<code data-language="ruby">x</code>` {
		t.Errorf("got %q", got)
	}
}
