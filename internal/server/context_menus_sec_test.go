// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"strconv"
	"strings"
	"testing"
)

// 管理画面のコンテキストメニュー（プロジェクト・ユーザー）は管理者専用（Redmine 6.1.3 #44109）。
func TestAdminContextMenusRequireAdmin(t *testing.T) {
	ts, _ := newFixtureServer(t)
	// プロジェクト 2（onlinestore）は非公開で dlopper はメンバーでない
	paths := []string{"/admin/projects_context_menu?ids[]=2", "/users/context_menu?ids[]=2"}
	for _, who := range []string{"anonymous", "dlopper"} {
		client := newClient(t)
		if who == "dlopper" {
			client = login(t, ts, "dlopper", "foo")
		}
		for _, p := range paths {
			res, body := get(t, client, ts.URL+p)
			if res.StatusCode == 200 || strings.Contains(body, "onlinestore") {
				t.Errorf("%s %s: status %d body %.300s", who, p, res.StatusCode, body)
			}
		}
	}
	admin := login(t, ts, "admin", "admin")
	for _, p := range paths {
		if res, _ := get(t, admin, ts.URL+p); res.StatusCode != 200 {
			t.Errorf("admin %s: status %d", p, res.StatusCode)
		}
	}
}

// Redmine 6.1.3 #44109: プロジェクト・ユーザーのコンテキストメニューはログイン済みの非管理者に 403 を返す
// （test_projects_context_menu_not_admin_user / test_users_context_menu_without_permission）。
func TestAdminContextMenusForbiddenForNonAdmin(t *testing.T) {
	ts, _ := newFixtureServer(t)
	jsmith := login(t, ts, "jsmith", "jsmith")
	for _, p := range []string{"/admin/projects_context_menu?ids[]=1&ids[]=2", "/users/context_menu?ids[]=8"} {
		if res, _ := get(t, jsmith, ts.URL+p); res.StatusCode != 403 {
			t.Errorf("jsmith %s: status %d, want 403", p, res.StatusCode)
		}
	}
}

// Redmine 6.1.3 #44109: 見えない工数が 1 件でも含まれていれば工数のコンテキストメニューは 404
// （test_time_entries_context_menu_with_time_entry_that_is_not_visible_should_fail）。
func TestTimeEntriesContextMenuWithInvisibleEntry(t *testing.T) {
	ts, d := newFixtureServer(t)
	// project.enable_module!(:time_tracking) / TimeEntry.generate!(project: project)（プロジェクト 2 は jsmith に見えない）
	mastersExec(t, d, `INSERT INTO project_modules (project_id, name) SELECT 2, 'time_tracking' WHERE NOT EXISTS (SELECT 1 FROM project_modules WHERE project_id = 2 AND name = 'time_tracking')`)
	mastersExec(t, d, `INSERT INTO time_entries (project_id, user_id, author_id, hours, activity_id, spent_on, tyear, tmonth, tweek, created_at, updated_at)
		VALUES (2, 2, 2, 1.0, 9, '2026-01-15', 2026, 1, 3, '2026-01-15T12:00:00.000000Z', '2026-01-15T12:00:00.000000Z')`)
	id := mastersInt(t, d, `SELECT MAX(id) FROM time_entries`)
	jsmith := login(t, ts, "jsmith", "jsmith")
	res, _ := get(t, jsmith, ts.URL+"/time_entries/context_menu?ids[]=1&ids[]=5&ids[]="+strconv.FormatInt(id, 10))
	if res.StatusCode != 404 {
		t.Errorf("status %d, want 404", res.StatusCode)
	}
	// 見える工数だけなら表示できる
	if res, _ := get(t, jsmith, ts.URL+"/time_entries/context_menu?ids[]=1&ids[]=2"); res.StatusCode != 200 {
		t.Errorf("visible entries: status %d, want 200", res.StatusCode)
	}
}
