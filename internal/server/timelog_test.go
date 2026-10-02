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
// testdata/timelog/* は参照 Redmine 6.1.2（http://127.0.0.1:3998）の出力を正規化し、HTML は <title>・
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
	// フォーム
	{"admin", "/time_entries/new", "new_admin.html"},
	{"jsmith", "/projects/ecookbook/time_entries/new", "project_new_jsmith.html"},
	{"jsmith", "/issues/1/time_entries/new", "issue_new_jsmith.html"},
	{"admin", "/time_entries/1/edit", "edit_1_admin.html"},
	{"jsmith", "/time_entries/1/edit", "edit_1_jsmith.html"},
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
