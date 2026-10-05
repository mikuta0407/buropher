// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package redmine

import (
	"strings"
	"testing"
)

// 属性値の中の Redmine リンク（#1 など）と "<" を含む文字列が、置換後もタグとして解釈されないことを確かめる。
// buropher は属性値の < > もエスケープする。
func TestAttributeLinkSubstitutionKeepsAttributeValues(t *testing.T) {
	e := newTestEnv(t)
	for _, f := range []string{"common_mark", "textile"} {
		r, _ := e.renderer(t, "admin", "ecookbook", f)
		for _, src := range []string{
			`<span title="a #1 <img src=x onerror=alert(1)>">z</span>`,
			`![a #1 <img src=x onerror=alert(1)>](x.png)`,
			`[foo](http://x "a [[Wiki]] <img src=x onerror=alert(1)>")`,
			`!x.png(a #1 <img src=x onerror=alert(1)>)!`,
		} {
			if got := string(r.Textilizable(src, Options{})); strings.Contains(got, "<img src=x") {
				t.Errorf("%s: %s\n=> raw HTML injected:\n%s", f, src, got)
			}
		}
	}
}

// #13723: collapse マクロ内の見出しにもセクション編集リンクが付く（セクション番号は続き番号）。
func TestSectionEditLinksWithCollapseMacro(t *testing.T) {
	e := newTestEnv(t)
	r, _ := e.renderer(t, "admin", "ecookbook", "common_mark")
	raw := "# Wiki\n## Section A\n\ncollapsed section\n{{collapse(View details...)\n## Section B\n}}\n\n## Section C\n"
	got := strings.ReplaceAll(string(r.Textilizable(raw, Options{EditSectionLinks: &EditSectionLinks{ProjectID: "1", ID: "Test"}})), "\n", "")
	for _, want := range []string{
		`<div class="contextual heading-2" title="Edit this section" id="section-3"><a class="icon-only icon-edit" href="/projects/1/wiki/Test/edit?section=3">`,
		`<a name="Section-B"></a><h2 >Section B<a href="#Section-B" class="wiki-anchor">&para;</a></h2>`,
		`<div class="contextual heading-2" title="Edit this section" id="section-4"><a class="icon-only icon-edit" href="/projects/1/wiki/Test/edit?section=4">`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}
