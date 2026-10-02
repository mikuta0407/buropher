package testfixtures

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
)

func TestResolve(t *testing.T) {
	got, err := Resolve("member_roles")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"issue_statuses", "trackers", "users", "projects", "roles", "members", "member_roles"}
	if !slices.Equal(got, want) {
		t.Errorf("Resolve = %v, want %v", got, want)
	}
	if _, err := Resolve("no_such_fixture"); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("unsupported fixture error = %v", err)
	}
}

func TestEvalERB(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	cases := map[string]string{
		"<%= 2.days.ago.to_fs(:db) %>":                 "2026-03-08 12:00:00",
		"<%= 1.minute.ago.to_fs(:db) %>":               "2026-03-10 11:59:00",
		"<%= 1.days.from_now.to_date.to_fs(:db) %>":    "2026-03-11",
		"<%= Date.today.to_fs(:db) %>":                 "2026-03-10",
		"a: <%= 20.day.from_now.to_date.to_fs(:db) %>": "a: 2026-03-30",
	}
	for in, want := range cases {
		got, err := evalERB(in, now)
		if err != nil || got != want {
			t.Errorf("evalERB(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := evalERB("<%= User.find(1) %>", now); err == nil {
		t.Error("unknown ERB should fail")
	}
}

func TestParsePermissions(t *testing.T) {
	got := ParsePermissions("---\n- :view_issues\n- :add_issues\n- :view_issues\n")
	if !slices.Equal(got, []string{"view_issues", "add_issues"}) {
		t.Errorf("ParsePermissions = %v", got)
	}
}

func TestLoadAll(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		Load(t, d, All()...)
		ctx := context.Background()
		counts := map[string]int{
			"principals": 13, "user_accounts": 9, "group_users": 2, "projects": 6, "project_closure": 6 + 5,
			"project_modules": 26, "members": 10, "member_roles": 12, "roles": 5, "issues": 14,
			"issue_priorities": 6, "time_entry_activities": 4, "document_categories": 4, "trackers": 3,
			"workflow_transitions": 276,
			"custom_fields":        10, "custom_fields_projects": 1, "custom_fields_trackers": 10, "custom_values": 17,
			"issue_journals": 5, "issue_journal_details": 6, "time_entries": 5, "queries": 12, "issue_relations": 2,
			"attachments": 24, "user_preferences": 3, "user_project_bookmarks": 2,
		}
		for tbl, want := range counts {
			var n int
			if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM `+tbl); err != nil {
				t.Fatal(err)
			}
			if n != want {
				t.Errorf("%s: %d rows, want %d", tbl, n, want)
			}
		}
		var kind, name string
		if err := d.QueryRow(ctx, `SELECT kind, name FROM principals WHERE id = 12`).Scan(&kind, &name); err != nil {
			t.Fatal(err)
		}
		if kind != "group_non_member" || name != "Non member users" {
			t.Errorf("principal 12 = %s %q", kind, name)
		}
		var perms int
		if err := d.Get(ctx, &perms, `SELECT COUNT(*) FROM role_permissions WHERE role_id = 1`); err != nil {
			t.Fatal(err)
		}
		if perms < 60 {
			t.Errorf("role 1 permissions = %d", perms)
		}
		// プロジェクト 6 (project5 の子, project5 は project1 の子) の祖先
		var depth int
		if err := d.Get(ctx, &depth, `SELECT depth FROM project_closure WHERE ancestor_id = 1 AND descendant_id = 6`); err != nil || depth != 2 {
			t.Errorf("closure 1->6 depth = %d, %v", depth, err)
		}
		// シーケンスが進んでいること
		id, err := d.InsertReturningID(ctx, `INSERT INTO projects (name, identifier, created_at, updated_at) VALUES ('x', 'x', ?, ?)`, db.Now(), db.Now())
		if err != nil || id != 7 {
			t.Errorf("next project id = %d, %v", id, err)
		}
	})
}

func TestLoadTextContents(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		Load(t, d, All()...)
		ctx := context.Background()
		counts := map[string]int{
			"documents": 3, "messages": 7, "news_comments": 2, "wiki_pages": 12, "issue_journals": 5,
			"issue_journal_details": 6, "attachments": 24,
		}
		for tbl, want := range counts {
			var n int
			if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM `+tbl); err != nil {
				t.Fatal(err)
			}
			if n != want {
				t.Errorf("%s: %d rows, want %d", tbl, n, want)
			}
		}
		// Wiki の最新版は wiki_contents の本文 (同じ版の履歴より優先)
		var text string
		var cur int
		if err := d.QueryRow(ctx, `SELECT v.text, p.current_version FROM wiki_pages p
JOIN wiki_page_versions v ON v.page_id = p.id AND v.version = p.current_version
WHERE p.title = 'CookBook_documentation'`).Scan(&text, &cur); err != nil {
			t.Fatal(err)
		}
		if cur != 3 || !strings.Contains(text, "with gzipped history") {
			t.Errorf("CookBook_documentation = v%d %q", cur, text)
		}
		var vers int
		if err := d.Get(ctx, &vers, `SELECT COUNT(*) FROM wiki_page_versions WHERE page_id = 1`); err != nil || vers != 3 {
			t.Errorf("page 1 versions = %d, %v", vers, err)
		}
		var kind, desc string
		var cid int64
		if err := d.QueryRow(ctx, `SELECT container_kind, container_id, description FROM attachments WHERE filename = 'error281.txt'`).Scan(&kind, &cid, &desc); err != nil {
			t.Fatal(err)
		}
		if kind != "issue" || cid != 3 || desc != "An attachment" {
			t.Errorf("error281.txt = %s %d %q", kind, cid, desc)
		}
		var parent int64
		if err := d.Get(ctx, &parent, `SELECT parent_id FROM messages WHERE id = 5`); err != nil || parent != 4 {
			t.Errorf("message 5 parent = %d, %v", parent, err)
		}
		var last int64
		if err := d.Get(ctx, &last, `SELECT last_message_id FROM boards WHERE id = 1`); err != nil || last == 0 {
			t.Errorf("board 1 last_message_id = %d, %v", last, err)
		}
		var scmid string
		if err := d.Get(ctx, &scmid, `SELECT scmid FROM changesets WHERE repository_id = 10 AND revision = '1'`); err != nil || scmid != "691322a8eb01e11fd7" {
			t.Errorf("changeset r1 scmid = %q, %v", scmid, err)
		}
	})
}
