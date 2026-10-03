// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

package redmine

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/testfixtures"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// TestWikiRedirect は Wiki#find_page がリダイレクトを辿ること（フィクスチャに wiki_redirects が無いので行を足す）。
func TestWikiRedirect(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	if err := testfixtures.LoadContext(ctx, d, frozenNow, "wikis", "wiki_pages", "wiki_contents", "users", "members", "member_roles", "roles", "enabled_modules"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(ctx, `INSERT INTO wiki_redirects (wiki_id, title, redirects_to, redirects_to_wiki_id, created_at) VALUES (1, 'Old_page', 'Another_page', 1, ?)`, db.NewTime(frozenNow)); err != nil {
		t.Fatal(err)
	}
	u, err := repository.FindUserByLogin(ctx, d, "jsmith")
	if err != nil {
		t.Fatal(err)
	}
	p, err := repository.FindProject(ctx, d, "ecookbook")
	if err != nil {
		t.Fatal(err)
	}
	r := &Renderer{Store: NewDBStore(ctx, d, authz.New(d, u)), User: u, Project: p, TextFormatting: "textile"}
	got := string(r.Textilizable("[[Old page]] [[Missing]]", Options{}))
	want := `<p><a class="wiki-page" href="/projects/ecookbook/wiki/Old_page">Old page</a> <a class="wiki-page new" href="/projects/ecookbook/wiki/Missing">Missing</a></p>`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// 期待値は参照 Redmine（フィクスチャ DB・admin）でヘルパーを直接呼んだ結果。

func TestLinkHelpers(t *testing.T) {
	e := newTestEnv(t)
	r, st := e.renderer(t, "admin", "ecookbook", "textile")
	r.onlyPath = true
	atts, err := repository.AttachmentsByIDs(context.Background(), e.d, []int64{3})
	if err != nil || len(atts) != 1 {
		t.Fatal(err)
	}
	a := atts[0]
	is, err := st.VisibleIssue(1)
	if err != nil || is == nil {
		t.Fatal(err)
	}
	tests := []struct{ name, got, want string }{
		{"thumbnail_tag", string(r.ThumbnailTag(a, 100)),
			`<div class="thumbnail" title="logo.gif"><a href="/attachments/3"><img srcset="/attachments/thumbnail/3/200 2x" style="max-width: 100px; max-height: 100px;" alt="logo.gif" loading="lazy" src="/attachments/thumbnail/3/200" /></a></div>`},
		{"link_to_attachment", string(r.LinkToAttachment(a, LinkToAttachmentOptions{})), `<a href="/attachments/3">logo.gif</a>`},
		{"link_to_attachment text", string(r.LinkToAttachment(a, LinkToAttachmentOptions{Text: "Text"})), `<a href="/attachments/3">Text</a>`},
		{"link_to_attachment class", string(r.LinkToAttachment(a, LinkToAttachmentOptions{HTML: rails.NewHash("class", "foo")})), `<a class="foo" href="/attachments/3">logo.gif</a>`},
		{"link_to_attachment download", string(r.LinkToAttachment(a, LinkToAttachmentOptions{Download: true})), `<a href="/attachments/download/3/logo.gif">logo.gif</a>`},
		{"link_to_attachment icon", string(r.LinkToAttachment(a, LinkToAttachmentOptions{Download: true, Icon: "download",
			HTML: rails.NewHash("class", "icon-only icon-download", "title", "Download")})),
			`<a class="icon-only icon-download" title="Download" href="/attachments/download/3/logo.gif"><svg class="s18 icon-svg" aria-hidden="true"><use href="/assets/icons-a9735328.svg#icon--download"></use></svg><span class="icon-label">logo.gif</span></a>`},
		{"link_to_attachment full", string(r.LinkToAttachment(a, LinkToAttachmentOptions{FullURL: true})), `<a href="http://test.host/attachments/3">logo.gif</a>`},
		{"link_to_issue", string(r.LinkToIssue(is, LinkToIssueOptions{})),
			`<a class="issue tracker-1 status-1 priority-4 priority-lowest behind-schedule" href="/issues/1">Bug #1</a>: Cannot print recipes`},
		{"link_to_issue truncate project", string(r.LinkToIssue(is, LinkToIssueOptions{Truncate: 6, Project: true})),
			`eCookbook - <a class="issue tracker-1 status-1 priority-4 priority-lowest behind-schedule" href="/issues/1">Bug #1</a>: Can...`},
		{"link_to_issue no subject/tracker", string(r.LinkToIssue(is, LinkToIssueOptions{NoSubject: true, NoTracker: true})),
			`<a class="issue tracker-1 status-1 priority-4 priority-lowest behind-schedule" title="Cannot print recipes" href="/issues/1">#1</a>`},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s:\n got %s\nwant %s", tt.name, tt.got, tt.want)
		}
	}
}

// 添付の説明は title / alt 属性に入り、その後に Redmine リンク（#1 など）の置換が文字列全体に走る。
// 説明は HTML エスケープしてから属性に入れる。
func TestInlineAttachmentDescriptionEscaped(t *testing.T) {
	e := newTestEnv(t)
	r, _ := e.renderer(t, "admin", "ecookbook", "textile")
	atts, err := repository.AttachmentsByIDs(context.Background(), e.d, []int64{3})
	if err != nil || len(atts) != 1 {
		t.Fatal(err)
	}
	a := *atts[0]
	a.Description.String, a.Description.Valid = ` #1 <img src=x onerror=alert(1)>`, true
	got := string(r.Textilizable("!logo.gif!", Options{Attachments: []*Attachment{&a}}))
	if strings.Contains(got, "<img src=x") {
		t.Errorf("description injected raw HTML:\n%s", got)
	}
}

func TestQuoteReply(t *testing.T) {
	q := QuoteBuilder{DefaultLanguage: "en"}
	tests := []struct{ name, got, want string }{
		{"issue", q.QuoteIssue("John Smith", "Unable to print recipes", ""), "John Smith wrote:\n> Unable to print recipes\n\n"},
		{"partial", q.QuoteIssue("John Smith", "Unable to print recipes", "a\r\nb"), "John Smith wrote:\n> a\n> b\n\n"},
		{"journal", q.QuoteIssueJournal("Redmine Admin", "Journal notes", "1", ""), "Redmine Admin wrote in #note-1:\n> Journal notes\n\n"},
		{"root message", q.QuoteRootMessage("Redmine Admin", "This is the very first post\nin the forum", ""),
			"Redmine Admin wrote:\n> This is the very first post\n> in the forum\n\n"},
		{"message", q.QuoteMessage("John Smith", "An other reply", "3", ""), "John Smith wrote in message#3:\n> An other reply\n\n"},
		{"pre", q.QuoteIssue("John Smith", "line1\n<pre>code\nx</pre>\r\nline3  ", ""), "John Smith wrote:\n> line1\n> [...]\n> line3\n\n"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s: got %q want %q", tt.name, tt.got, tt.want)
		}
	}
}

// macros_test.rb の単体部分（引数の分割・オプションの取り出し・登録）。
func TestMacroArgs(t *testing.T) {
	split := []struct {
		in   string
		want []string
	}{
		{"", []string{}},
		{"foo", []string{"foo"}},
		{"foo, bar", []string{"foo", "bar"}},
		{`"foo, bar", baz`, []string{"foo, bar", "baz"}},
		{`"a ""quoted"" b"`, []string{`a "quoted" b`}},
		{"a,,b,", []string{"a", "", "b"}},
	}
	for _, tt := range split {
		if got := splitMacroArgs(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("split %q = %q, want %q", tt.in, got, tt.want)
		}
	}
	args, opts := ExtractMacroOptions([]string{"Foo", "parent=1", "DEPTH=2"}, "parent", "depth")
	if !reflect.DeepEqual(args, []string{"Foo"}) || opts["parent"] != "1" || opts["depth"] != "2" {
		t.Errorf("extract: %q %v", args, opts)
	}
	args, opts = ExtractMacroOptions([]string{"title=x", "Foo", "size=3"}, "size", "title")
	if !reflect.DeepEqual(args, []string{"title=x", "Foo"}) || opts["size"] != "3" || len(opts) != 1 {
		t.Errorf("extract stops at non-option: %q %v", args, opts)
	}
}

func TestRegisterMacro(t *testing.T) {
	saved := macroRegistry
	t.Cleanup(func() { macroRegistry = saved })
	macroRegistry = append([]*Macro(nil), saved...)
	RegisterMacro(&Macro{Name: "Foo_Bar", Desc: "test", AcceptsBlock: true, Func: func(c *MacroContext) (any, error) {
		return "<" + strings.Join(c.Args, "|") + ">" + c.Text, nil
	}})
	r := &Renderer{TextFormatting: "textile"}
	got := string(r.Textilizable("{{foo_bar(a, b)\nblock\n}}", Options{}))
	if want := "<p>&lt;a|b&gt;block</p>"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	got = string(r.Textilizable("!{{foo_bar}} {{unknown}}", Options{}))
	if want := "<p>{{foo_bar}} {{unknown}}</p>"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
}
