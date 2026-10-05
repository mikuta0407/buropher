// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package scrubber

import (
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/textformat/htmldom"
)

// Redmine 7.0.1 の test/unit/lib/redmine/wiki_formatting/*_scrubber_test.rb の移植。

func run(t *testing.T, html string, f func(*htmldom.Node)) string {
	t.Helper()
	frag, err := htmldom.ParseHTML5Fragment(html)
	if err != nil {
		t.Fatal(err)
	}
	Run(frag, func(n *htmldom.Node) bool { f(n); return false })
	return htmldom.RenderHTML5(frag)
}

const table3 = "<table>\n  <tbody><tr>\n    <th>A</th>\n    <th>B</th>\n  </tr>\n  <tr>\n    <td></td>\n    <td></td>\n  </tr>\n  <tr>\n    <td></td>\n    <td></td>\n  </tr>\n</tbody></table>\n"

func TestTablesort(t *testing.T) {
	// 既定（設定無効）では変えない
	if got := run(t, table3, func(n *htmldom.Node) { Tablesort(n, &Options{}) }); got != table3 {
		t.Errorf("disabled: %q", got)
	}
	on := &Options{TablesortEnabled: true}
	// 2 行以下は変えない
	two := "<table><tbody><tr><th>A</th></tr><tr><td></td></tr></tbody></table>"
	if got := run(t, two, func(n *htmldom.Node) { Tablesort(n, on) }); got != two {
		t.Errorf("2 rows: %q", got)
	}
	want := strings.Replace(strings.Replace(table3, "<table>", `<table data-controller="tablesort">`, 1),
		"<tbody><tr>", `<tbody><tr data-sort-method="none">`, 1)
	if got := run(t, table3, func(n *htmldom.Node) { Tablesort(n, on) }); got != want {
		t.Errorf("enabled:\n got %q\nwant %q", got, want)
	}
	// 見出し行が無ければ変えない
	noth := "<table><tbody><tr><td>A</td></tr><tr><td></td></tr><tr><td></td></tr></tbody></table>"
	if got := run(t, noth, func(n *htmldom.Node) { Tablesort(n, on) }); got != noth {
		t.Errorf("no th: %q", got)
	}
	// 見出し行の td にも data-sort-method を付ける
	mixed := "<table><tbody><tr><th>A</th><td>B</td></tr><tr><td></td><td></td></tr><tr><td></td><td></td></tr></tbody></table>"
	got := run(t, mixed, func(n *htmldom.Node) { Tablesort(n, on) })
	if !strings.Contains(got, `<tr data-sort-method="none"><th>A</th><td data-sort-method="none">B</td>`) {
		t.Errorf("mixed: %q", got)
	}
}

func TestHiresImages(t *testing.T) {
	got := run(t, `<img src="image@2x.png"><img src="/path/to/image@3x.JPG" alt="x"><img src="image.png"><img src="a@2x.svg">`, HiresImages)
	want := `<img src="image@2x.png" srcset="image@2x.png 2x"><img src="/path/to/image@3x.JPG" alt="x" srcset="/path/to/image@3x.JPG 3x"><img src="image.png"><img src="a@2x.svg">`
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

func TestInlineAttachments(t *testing.T) {
	o := &Options{FindAttachment: func(name string) (string, string, bool) {
		switch strings.ToLower(name) {
		case "logo.gif":
			return "/attachments/download/3/logo.gif", "This is a logo", true
		case "%e3%83%86%e3%82%b9%e3%83%88.png": // CGI.unescape は呼び出し側（FindAttachment）の責務
			return "/attachments/download/18/%E3%83%86%E3%82%B9%E3%83%88.png", `a "quoted" desc`, true
		}
		return "", "", false
	}}
	cases := []struct{ in, want string }{
		{`<img src="logo.gif">`, `<img src="/attachments/download/3/logo.gif" title="This is a logo" alt="This is a logo" loading="lazy">`},
		{`<img src="LOGO.GIF" alt="">`, `<img src="/attachments/download/3/logo.gif" alt="This is a logo" title="This is a logo" loading="lazy">`},
		{`<img src="logo.gif" alt="mine">`, `<img src="/attachments/download/3/logo.gif" alt="mine" loading="lazy">`},
		{`<img src="other.gif">`, `<img src="other.gif">`},
		{`<img src="/logo.gif">`, `<img src="/logo.gif">`},
		{`<img src="logo.svg">`, `<img src="logo.svg">`},
		{`<img src="%E3%83%86%E3%82%B9%E3%83%88.png">`, `<img src="/attachments/download/18/%E3%83%86%E3%82%B9%E3%83%88.png" title="a quoted desc" alt="a quoted desc" loading="lazy">`},
	}
	for _, c := range cases {
		if got := run(t, c.in, func(n *htmldom.Node) { InlineAttachments(n, o) }); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestCopyPre(t *testing.T) {
	got := run(t, "<p>a</p><pre>x</pre>", func(n *htmldom.Node) { CopyPre(n, &Options{IconsPath: "/i.svg"}) })
	want := `<p>a</p><div class="pre-wrapper" data-controller="clipboard"><a class="copy-pre-content-link icon-only" title="Copy" data-action="clipboard#copyPre">` +
		`<svg class="s18 icon-svg" aria-hidden="true"><use href="/i.svg#icon--copy-pre-content"></use></svg></a><pre data-clipboard-target="pre">x</pre></div>`
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}
