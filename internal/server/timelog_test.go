// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// このファイルは工数（TimelogController・ContextMenusController#time_entries・API）の参照 Redmine との比較テスト。
//
// testdata/timelog/* は参照 Redmine 7.0.1（http://127.0.0.1:3998）の出力を正規化し、HTML は <title>・
// ページ固有の head・#main だけを抜き出したもの（activity_search_test.go と同じ形式）。
// 取り直すときは BUROPHER_TIMELOG_GOLDEN_REF=http://127.0.0.1:3998 go test -run TestTimelogMatchRedmine ./internal/server
// （参照側は GET のみ）。

var timelogCases = []asCase{
	// 一覧
	{"admin", "/time_entries", "index_admin.html"},
	{"jsmith", "/time_entries", "index_jsmith.html"},
	{"anonymous", "/time_entries", "index_anonymous.html"},
	{"dlopper", "/time_entries", "index_dlopper.html"},
	{"admin", "/projects/ecookbook/time_entries", "project_index_admin.html"},
	{"jsmith", "/projects/ecookbook/time_entries", "project_index_jsmith.html"},
	{"jsmith", "/projects/ecookbook/time_entries?issue_id=1", "project_index_issue_jsmith.html"},
	{"admin", "/time_entries?set_filter=1&group_by=project", "index_group_project_admin.html"},
	{"admin", "/time_entries?set_filter=1&group_by=activity&sort=hours", "index_group_activity_admin.html"},
	{"admin", "/time_entries?set_filter=1&group_by=spent_on&t[]=", "index_group_spent_on_admin.html"},
	{"admin", "/time_entries?set_filter=1&c[]=project&c[]=spent_on&c[]=created_on&c[]=tweek&c[]=author&c[]=issue.tracker&c[]=issue.status&c[]=issue.parent&c[]=issue.category&c[]=issue.fixed_version&c[]=cf_10&c[]=hours", "index_columns_admin.html"},
	{"admin", "/time_entries?set_filter=1&f[]=spent_on&op[spent_on]=%3E%3C&v[spent_on][]=2007-03-01&v[spent_on][]=2007-03-31&f[]=", "index_filter_admin.html"},
	{"admin", "/time_entries?per_page=2&page=2", "index_page2_admin.html"},
	{"admin", "/time_entries?set_filter=1&f[]=hours&op[hours]=%3E%3D&v[hours][]=1000", "index_empty_admin.html"},
	// レポート
	{"admin", "/time_entries/report", "report_admin.html"},
	{"admin", "/time_entries/report?criteria[]=project&criteria[]=user&columns=month", "report_project_user_admin.html"},
	{"jsmith", "/projects/ecookbook/time_entries/report?criteria[]=activity&criteria[]=issue&criteria[]=user&columns=week", "report_3_week_jsmith.html"},
	{"admin", "/projects/ecookbook/time_entries/report?criteria[]=version&criteria[]=status&columns=year", "report_version_year_admin.html"},
	{"admin", "/time_entries/report?criteria[]=tracker&criteria[]=category&columns=day&set_filter=1&f[]=spent_on&op[spent_on]=%3E%3C&v[spent_on][]=2007-03-20&v[spent_on][]=2007-04-30", "report_day_admin.html"},
	{"admin", "/time_entries/report?criteria[]=cf_10&criteria[]=cf_7&criteria[]=cf_1", "report_cf_admin.html"},
	{"dlopper", "/time_entries/report?criteria[]=project", "report_dlopper.html"},
	{"admin", "/time_entries/report.csv?criteria[]=project&criteria[]=user&columns=month", "report_admin.csv"},
	{"admin", "/time_entries.csv", "index_admin.csv"},
	{"admin", "/time_entries.atom", "index_admin.atom"},
	{"admin", "/projects/ecookbook/time_entries.csv?c[]=all_inline", "project_index_all_admin.csv"},
	// フォーム
	{"admin", "/time_entries/new", "new_admin.html"},
	{"jsmith", "/projects/ecookbook/time_entries/new", "project_new_jsmith.html"},
	{"jsmith", "/issues/1/time_entries/new", "issue_new_jsmith.html"},
	{"admin", "/time_entries/1/edit", "edit_1_admin.html"},
	{"jsmith", "/time_entries/1/edit", "edit_1_jsmith.html"},
	// 一括編集・コンテキストメニュー
	{"admin", "/time_entries/bulk_edit?ids[]=1&ids[]=2&ids[]=4", "bulk_edit_admin.html"},
	{"jsmith", "/time_entries/bulk_edit?ids[]=1&ids[]=2", "bulk_edit_jsmith.html"},
	{"admin", "/time_entries/context_menu?ids[]=1&ids[]=2", "context_menu_admin.html"},
	{"admin", "/time_entries/context_menu?ids[]=1&back_url=%2Ftime_entries", "context_menu_1_admin.html"},
	{"dlopper", "/time_entries/context_menu?ids[]=1", "context_menu_1_dlopper.html"},
	// API
	{"admin", "/time_entries.json", "index_admin.json"},
	{"admin", "/time_entries.xml", "index_admin.xml"},
	{"jsmith", "/projects/ecookbook/time_entries.json?limit=2&offset=1", "project_index_jsmith.json"},
	{"admin", "/time_entries/1.json", "show_1_admin.json"},
	{"admin", "/time_entries/2.xml", "show_2_admin.xml"},
	{"admin", "/time_entries.json?issue_id=1", "index_issue_admin.json"},
}

// TestTimelogMatchRedmine は工数の画面・API が参照 Redmine と一致することを確認する。
func TestTimelogMatchRedmine(t *testing.T) {
	dir := filepath.Join("testdata", "timelog")
	if ref := os.Getenv("BUROPHER_TIMELOG_GOLDEN_REF"); ref != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		clients := map[string]*http.Client{}
		for _, tc := range timelogCases {
			status, body := asFetch(t, ref, clients, tc.user, tc.path)
			if status != 200 {
				t.Fatalf("ref %s %s: status %d", tc.user, tc.path, status)
			}
			if err := os.WriteFile(filepath.Join(dir, tc.golden), []byte(asNormalize(body, ref)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	ts, _ := newFixtureServer(t)
	clients := map[string]*http.Client{}
	for _, tc := range timelogCases {
		t.Run(tc.golden, func(t *testing.T) {
			status, body := asFetch(t, ts.URL, clients, tc.user, tc.path)
			if status != 200 {
				t.Fatalf("status %d: %s", status, body)
			}
			want, err := os.ReadFile(filepath.Join(dir, tc.golden))
			if err != nil {
				t.Fatal(err)
			}
			got := asNormalize(body, ts.URL)
			if got != string(want) {
				if os.Getenv("BUROPHER_TIMELOG_DUMP") != "" {
					_ = os.WriteFile(filepath.Join(os.TempDir(), "got_"+tc.golden), []byte(got), 0o644)
				}
				gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
				for i := 0; i < len(gl) || i < len(wl); i++ {
					var a, b string
					if i < len(gl) {
						a = gl[i]
					}
					if i < len(wl) {
						b = wl[i]
					}
					if a != b {
						t.Fatalf("line %d differs\n got: %q\nwant: %q", i+1, a, b)
					}
				}
			}
		})
	}
}
