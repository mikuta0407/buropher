// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package normalize

import (
	"strings"
	"testing"
)

func ptr(b bool) *bool { return &b }

func mustNew(t *testing.T, cfg Config, bases ...string) *Normalizer {
	t.Helper()
	n, err := New(cfg, bases...)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func mustHTML(t *testing.T, n *Normalizer, src string) string {
	t.Helper()
	out, err := n.HTML([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestHTMLStructureAndWhitespace(t *testing.T) {
	n := mustNew(t, Config{})
	src := "<!DOCTYPE html>\n<html><head><title> T </title></head>\n<body>\n  <div   id=\"a\"   class=\"x  y\">\n   Hello\n\n   world  <b>bold</b>\n  </div>\n<br/></body></html>"
	want := `<!DOCTYPE html>
<html>
  <head>
    <title>T</title>
  </head>
  <body>
    <div class="x y" id="a">
      Hello world
      <b>bold</b>
    </div>
    <br>
  </body>
</html>
`
	if got := mustHTML(t, n, src); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestHTMLWhitespaceEquivalence(t *testing.T) {
	n := mustNew(t, Config{})
	a := mustHTML(t, n, "<p>a  b</p>\n\n<ul>\n <li>x</li>\n</ul>")
	b := mustHTML(t, n, "<p>a\n b</p><ul><li>x</li></ul>")
	if a != b {
		t.Errorf("whitespace-only differences should normalize equal:\n%s\n---\n%s", a, b)
	}
}

func TestHTMLAttributeOrder(t *testing.T) {
	n := mustNew(t, Config{})
	a := mustHTML(t, n, `<a title="t" href="/x" class="c">x</a>`)
	b := mustHTML(t, n, `<a class="c" href="/x" title="t">x</a>`)
	if a != b {
		t.Errorf("attribute order should not matter:\n%s\n---\n%s", a, b)
	}
	if !strings.Contains(a, `<a class="c" href="/x" title="t">x</a>`) {
		t.Errorf("attributes not sorted: %s", a)
	}
}

func TestHTMLSortClasses(t *testing.T) {
	src := `<div class="b a"></div>`
	if got := mustHTML(t, mustNew(t, Config{}), src); !strings.Contains(got, `class="b a"`) {
		t.Errorf("class order must be kept by default: %s", got)
	}
	if got := mustHTML(t, mustNew(t, Config{SortClasses: ptr(true)}), src); !strings.Contains(got, `class="a b"`) {
		t.Errorf("classes not sorted: %s", got)
	}
}

func TestHTMLPreAndTextareaPreserved(t *testing.T) {
	n := mustNew(t, Config{})
	src := "<body><pre>  line1\n    line2  <b>x</b>\n</pre><textarea name=\"t\">  a\n  b </textarea></body>"
	got := mustHTML(t, n, src)
	if !strings.Contains(got, "<pre>  line1\n    line2  <b>x</b>\n</pre>") {
		t.Errorf("pre content changed:\n%s", got)
	}
	if !strings.Contains(got, "<textarea name=\"t\">  a\n  b </textarea>") {
		t.Errorf("textarea content changed:\n%s", got)
	}
}

func TestHTMLCSRF(t *testing.T) {
	n := mustNew(t, Config{})
	src := `<html><head><meta name="csrf-token" content="TOKEN123"></head><body>
<form><input type="hidden" name="authenticity_token" value="FORMTOK" autocomplete="off"></form>
<script>var t = "TOKEN123";</script></body></html>`
	got := mustHTML(t, n, src)
	if strings.Contains(got, "TOKEN123") || strings.Contains(got, "FORMTOK") {
		t.Errorf("csrf token not masked:\n%s", got)
	}
	if !strings.Contains(got, `content="{{CSRF}}"`) || !strings.Contains(got, `value="{{CSRF}}"`) {
		t.Errorf("csrf placeholder missing:\n%s", got)
	}
	if !strings.Contains(got, `var t = "{{CSRF}}";`) {
		t.Errorf("csrf token in script not masked:\n%s", got)
	}
}

func TestHTMLRandomFormName(t *testing.T) {
	n := mustNew(t, Config{})
	a := mustHTML(t, n, `<form name="form-1a2b3c4d" action="/x"></form><form id="q" name="q-deadbeef"></form>`)
	b := mustHTML(t, n, `<form name="form-99999999" action="/x"></form><form id="q" name="q-0000ffff"></form>`)
	if a != b {
		t.Errorf("random form names not normalized:\n%s\n---\n%s", a, b)
	}
	if !strings.Contains(a, `name="form-RANDOM"`) || !strings.Contains(a, `name="q-RANDOM"`) {
		t.Errorf("unexpected form names:\n%s", a)
	}
}

// .js レスポンスの JS 文字列に埋め込まれたフォームのランダムな name も -RANDOM に揃える。
func TestTextJSRandomFormName(t *testing.T) {
	n := mustNew(t, Config{})
	a := n.Text([]byte(`$('#x').html('<form id=\"csv-export-form\" action=\"/x.csv\" name=\"csv-export-form-85e3a79e\" method=\"get\"><input name=\"q-deadbeef\">');`))
	b := n.Text([]byte(`$('#x').html('<form id=\"csv-export-form\" action=\"/x.csv\" name=\"csv-export-form-d5db4191\" method=\"get\"><input name=\"q-deadbeef\">');`))
	if a != b {
		t.Errorf("random form names in JS not normalized:\n%s\n---\n%s", a, b)
	}
	if !strings.Contains(a, `name=\"csv-export-form-RANDOM\"`) {
		t.Errorf("unexpected form name:\n%s", a)
	}
	// フォーム以外の要素の name は変えない
	if !strings.Contains(a, `<input name=\"q-deadbeef\">`) {
		t.Errorf("non-form name changed:\n%s", a)
	}
}

func TestHTMLAssetDigestAndBaseURL(t *testing.T) {
	n := mustNew(t, Config{}, "http://127.0.0.1:3998/")
	src := `<head><link rel="stylesheet" href="/assets/application-6dc0ec44.css">
<script src="/assets/jstoolbar/lang/jstoolbar-en-1a2b3c4d.js"></script>
<link rel="alternate" href="http://127.0.0.1:3998/issues.atom?key=0123456789abcdef0123456789abcdef01234567">
<input type="hidden" name="back_url" value="http%3A%2F%2F127.0.0.1%3A3998%2Fmy%2Fpage"></head>`
	got := mustHTML(t, n, src)
	for _, want := range []string{
		`href="/assets/application-DIGEST.css"`,
		`src="/assets/jstoolbar/lang/jstoolbar-en-DIGEST.js"`,
		`href="{{BASE}}/issues.atom?key=KEY"`,
		`value="{{BASE}}%2Fmy%2Fpage"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// 拡張子のない 8 桁 hex は置換しない
	if s := n.String("/issues/abc-12345678"); s != "/issues/abc-12345678" {
		t.Errorf("unexpected replacement: %s", s)
	}
}

func TestHTMLStripAndMask(t *testing.T) {
	n := mustNew(t, Config{
		StripSelectors:    []string{"#footer"},
		MaskTextSelectors: []string{"td.last_login_on"},
		MaskAttrs:         []AttrMask{{Selector: "a.time", Attr: "title"}},
	})
	src := `<body><table><tr><td class="last_login_on">2026-01-01 <b>12:00</b></td></tr></table>
<a class="time" title="01/14/2026 12:00 PM" href="/x">1 day</a><div id="footer">Powered by Redmine</div></body>`
	got := mustHTML(t, n, src)
	if strings.Contains(got, "Powered by") || strings.Contains(got, "footer") {
		t.Errorf("footer not stripped:\n%s", got)
	}
	if !strings.Contains(got, `<td class="last_login_on">MASKED</td>`) {
		t.Errorf("text not masked:\n%s", got)
	}
	if !strings.Contains(got, `title="MASKED"`) {
		t.Errorf("attr not masked:\n%s", got)
	}
}

func TestHTMLBadSelector(t *testing.T) {
	n := mustNew(t, Config{StripSelectors: []string{"[[["}})
	if _, err := n.HTML([]byte("<p>x</p>")); err == nil {
		t.Error("expected selector error")
	}
}

func TestHTMLComments(t *testing.T) {
	src := `<body><!-- c --><p>x</p></body>`
	if got := mustHTML(t, mustNew(t, Config{}), src); strings.Contains(got, "<!--") {
		t.Errorf("comments should be dropped by default:\n%s", got)
	}
	if got := mustHTML(t, mustNew(t, Config{KeepComments: ptr(true)}), src); !strings.Contains(got, "<!-- c -->") {
		t.Errorf("comment not kept:\n%s", got)
	}
}

func TestHTMLPreserveEdgeSpace(t *testing.T) {
	src := `<p><a>x</a> , <a>y</a></p>`
	if got := mustHTML(t, mustNew(t, Config{}), src); !strings.Contains(got, "\n      ,\n") {
		t.Errorf("text should be trimmed by default:\n%s", got)
	}
	if got := mustHTML(t, mustNew(t, Config{PreserveEdgeSpace: ptr(true)}), src); !strings.Contains(got, "\n       , \n") {
		t.Errorf("edge spaces should be kept:\n%q", got)
	}
}

func TestRelativeTimesAndTimestamps(t *testing.T) {
	n := mustNew(t, Config{MaskRelativeTimes: ptr(true), MaskTimestamps: ptr(true)})
	cases := map[string]string{
		"Updated about 2 hours ago":   "Updated RELTIME ago",
		"Added less than a minute":    "Added RELTIME",
		"3 days":                      "RELTIME",
		"over 19 years ago":           "RELTIME ago",
		"at 2026-01-15T12:00:00Z":     "at TIMESTAMP",
		"x 2006-07-19T17:13:59+02:00": "x TIMESTAMP",
	}
	for in, want := range cases {
		if got := n.String(in); got != want {
			t.Errorf("String(%q) = %q, want %q", in, got, want)
		}
	}
	plain := mustNew(t, Config{})
	if got := plain.String("3 days"); got != "3 days" {
		t.Errorf("relative times must not be masked by default: %q", got)
	}
}

func TestCustomRegex(t *testing.T) {
	n := mustNew(t, Config{Regex: []RegexMask{{Pattern: `session-\d+`, Replace: "session-N"}}})
	if got := n.String("id session-42 end"); got != "id session-N end" {
		t.Errorf("got %q", got)
	}
	if _, err := New(Config{Regex: []RegexMask{{Pattern: "("}}}); err == nil {
		t.Error("expected regex compile error")
	}
}

func TestJSONOrderAndSort(t *testing.T) {
	src := `{"b":1,"a":{"y":2.0,"x":[true,null,"s"]},"c":[],"d":{}}`
	got, err := mustNew(t, Config{}).JSON([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "b": 1,
  "a": {
    "y": 2.0,
    "x": [
      true,
      null,
      "s"
    ]
  },
  "c": [],
  "d": {}
}
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	sorted, err := mustNew(t, Config{SortKeys: ptr(true)}).JSON([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(sorted, `"a"`) > strings.Index(sorted, `"b"`) {
		t.Errorf("keys not sorted:\n%s", sorted)
	}
}

func TestJSONMasking(t *testing.T) {
	n := mustNew(t, Config{MaskTimestamps: ptr(true), MaskKeys: []string{"updated_on", "closed_on"}}, "http://h:1")
	src := `{"created_on":"2026-01-15T12:00:00Z","updated_on":"x","closed_on":null,"url":"http://h:1/issues/1","html":"<b>&</b>"}`
	got, err := n.JSON([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"created_on": "TIMESTAMP"`,
		`"updated_on": "MASKED"`,
		`"closed_on": null`,
		`"url": "{{BASE}}/issues/1"`,
		`"html": "<b>&</b>"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestJSONInvalidFallsBackToText(t *testing.T) {
	out, err := mustNew(t, Config{}).Normalize(FormatJSON, []byte("not json"))
	if err != nil {
		t.Fatal(err)
	}
	if out != "not json\n" {
		t.Errorf("got %q", out)
	}
}

func TestXML(t *testing.T) {
	n := mustNew(t, Config{MaskTimestamps: ptr(true), MaskKeys: []string{"updated_on"}})
	src := `<?xml version="1.0" encoding="UTF-8"?>
<issues type="array" total_count="2" offset="0"><issue><id>1</id>
<subject>A &amp; B</subject><parent/><description></description>
<created_on>2026-01-15T12:00:00Z</created_on><updated_on>2026-01-15T12:00:00Z</updated_on></issue></issues>`
	got, err := n.XML([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := `<?xml version="1.0" encoding="UTF-8"?>
<issues offset="0" total_count="2" type="array">
  <issue>
    <id>1</id>
    <subject>A &amp; B</subject>
    <parent/>
    <description/>
    <created_on>TIMESTAMP</created_on>
    <updated_on>MASKED</updated_on>
  </issue>
</issues>
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestXMLInvalid(t *testing.T) {
	if _, err := mustNew(t, Config{}).XML([]byte("<a><b></a>")); err == nil {
		t.Error("expected error")
	}
}

func TestDetectFormat(t *testing.T) {
	cases := map[string]Format{
		"text/html; charset=utf-8":        FormatHTML,
		"application/json; charset=utf-8": FormatJSON,
		"application/xml":                 FormatXML,
		"text/csv":                        FormatText,
		"":                                FormatText,
	}
	for ct, want := range cases {
		if got := DetectFormat(ct); got != want {
			t.Errorf("DetectFormat(%q) = %s, want %s", ct, got, want)
		}
	}
}

func TestConfigMerge(t *testing.T) {
	base := Config{StripSelectors: []string{"#a"}, SortKeys: ptr(true), MaskTimestamps: ptr(true)}
	over := Config{StripSelectors: []string{"#b"}, SortKeys: ptr(false)}
	m := base.Merge(over)
	if len(m.StripSelectors) != 2 || m.StripSelectors[1] != "#b" {
		t.Errorf("lists should be concatenated: %v", m.StripSelectors)
	}
	if val(m.SortKeys) {
		t.Error("override bool should win")
	}
	if !val(m.MaskTimestamps) {
		t.Error("unset bool should inherit")
	}
}

func TestTextNormalize(t *testing.T) {
	n := mustNew(t, Config{}, "http://h:1")
	if got := n.Text([]byte("a\r\nhttp://h:1/x h:1")); got != "a\n{{BASE}}/x {{HOST}}\n" {
		t.Errorf("got %q", got)
	}
}
