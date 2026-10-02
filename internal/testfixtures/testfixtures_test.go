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
	if _, err := Resolve("news"); err == nil || !strings.Contains(err.Error(), "not supported") {
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
