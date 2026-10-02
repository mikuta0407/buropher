package server_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// このファイルはバージョン（VersionsController: ロードマップ・詳細・フォーム・API）の参照 Redmine との比較テスト。
//
// testdata/versions/* は参照 Redmine 6.1.2（http://127.0.0.1:3998）の出力を asNormalize で正規化したもの
// （HTML は <title>・ページ固有の head・#main だけ）。取り直すときは
// BUROPHER_VERSIONS_GOLDEN_REF=http://127.0.0.1:3998 go test -run TestVersionsMatchRedmine ./internal/server

var versionsCases = []asCase{
	// ロードマップ
	{"admin", "/projects/ecookbook/roadmap", "roadmap_admin.html"},
	{"jsmith", "/projects/ecookbook/roadmap", "roadmap_jsmith.html"},
	{"anonymous", "/projects/ecookbook/roadmap", "roadmap_anonymous.html"},
	{"dlopper", "/projects/ecookbook/roadmap", "roadmap_dlopper.html"},
	{"admin", "/projects/ecookbook/roadmap?completed=1", "roadmap_completed_admin.html"},
	{"admin", "/projects/ecookbook/roadmap?tracker_ids%5B%5D=&tracker_ids%5B%5D=1&with_subprojects=0", "roadmap_tracker1_nosub_admin.html"},
	{"admin", "/projects/ecookbook/roadmap?tracker_ids=2", "roadmap_tracker2_admin.html"},
	{"admin", "/projects/ecookbook/versions", "versions_index_admin.html"},
	{"admin", "/projects/onlinestore/roadmap", "roadmap_onlinestore_admin.html"},
	{"admin", "/projects/subproject1/roadmap", "roadmap_subproject1_admin.html"},
	{"jsmith", "/projects/subproject1/roadmap", "roadmap_subproject1_jsmith.html"},
	// 詳細
	{"admin", "/versions/2", "show_2_admin.html"},
	{"jsmith", "/versions/2", "show_2_jsmith.html"},
	{"anonymous", "/versions/2", "show_2_anonymous.html"},
	{"admin", "/versions/1", "show_1_admin.html"},
	{"admin", "/versions/3", "show_3_admin.html"},
	{"jsmith", "/versions/3", "show_3_jsmith.html"},
	{"admin", "/versions/4", "show_4_admin.html"},
	{"admin", "/versions/7", "show_7_admin.html"},
	{"admin", "/versions/2?status_by=status", "show_2_status_admin.html"},
	{"admin", "/versions/2?status_by=priority", "show_2_priority_admin.html"},
	{"admin", "/versions/2?status_by=author", "show_2_author_admin.html"},
	{"admin", "/versions/2?status_by=assigned_to", "show_2_assigned_to_admin.html"},
	{"admin", "/versions/2?status_by=category", "show_2_category_admin.html"},
	{"admin", "/versions/2?status_by=unknown", "show_2_unknown_admin.html"},
	// フォーム
	{"admin", "/projects/ecookbook/versions/new", "new_admin.html"},
	{"jsmith", "/projects/ecookbook/versions/new", "new_jsmith.html"},
	{"admin", "/projects/subproject1/versions/new", "new_subproject1_admin.html"},
	{"admin", "/versions/2/edit", "edit_2_admin.html"},
	{"admin", "/versions/4/edit", "edit_4_admin.html"},
	{"jsmith", "/versions/3/edit", "edit_3_jsmith.html"},
	{"admin", "/projects/ecookbook/versions/new?version%5Bname%5D=X&version%5Bsharing%5D=system", "new_params_admin.html"},
	// API
	{"admin", "/projects/ecookbook/versions.json", "index_admin.json"},
	{"admin", "/projects/ecookbook/versions.xml", "index_admin.xml"},
	{"jsmith", "/projects/subproject1/versions.json", "index_subproject1_jsmith.json"},
	{"admin", "/versions/2.json", "show_2_admin.json"},
	{"admin", "/versions/2.xml", "show_2_admin.xml"},
	{"jsmith", "/versions/3.json", "show_3_jsmith.json"},
	{"dlopper", "/versions/2.json", "show_2_dlopper.json"},
	{"anonymous", "/versions/3.json", "show_3_anonymous.json"},
}

// TestVersionsMatchRedmine はバージョンの画面と API が参照 Redmine と一致することを確認する。
func TestVersionsMatchRedmine(t *testing.T) {
	dir := filepath.Join("testdata", "versions")
	if ref := os.Getenv("BUROPHER_VERSIONS_GOLDEN_REF"); ref != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		clients := map[string]*http.Client{}
		for _, tc := range versionsCases {
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
	for _, tc := range versionsCases {
		t.Run(tc.golden, func(t *testing.T) {
			status, body := asFetch(t, ts.URL, clients, tc.user, tc.path)
			if status != 200 {
				t.Fatalf("status %d", status)
			}
			want, err := os.ReadFile(filepath.Join(dir, tc.golden))
			if err != nil {
				t.Fatal(err)
			}
			got := asNormalize(body, ts.URL)
			if got != string(want) {
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
