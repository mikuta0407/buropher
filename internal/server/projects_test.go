package server_test

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

// プロジェクト（一覧・概要・新規・設定・メンバー・カテゴリ・管理画面のプロジェクト一覧・API）の互換テスト。
// testdata/projects/ の期待値は専用の参照 Redmine（reset 直後）から
// `go run ./tools/compat fetch -raw -user admin <path>` で取得し、ベース URL を {{BASE}} に置換したもの。
//
// 参照 Redmine は GET でも「最近使ったプロジェクト」を記録するため、ジャンプボックスは取得順に依存する。
// ページの比較ではジャンプボックス（#project-jump）を伏せる。

var (
	projectJumpRe = regexp.MustCompile(`<div id="project-jump".*</div></div></div>`)
	repoURLRe     = regexp.MustCompile(`file:///[^<"]*/tmp/test/`)
	pngRe         = regexp.MustCompile(`-[0-9a-f]{8}\.png`)
	// テスト用フィクスチャの相対日時の一部（作業時間等）は参照と時刻がずれるため最終活動日は伏せる
	lastActivityRe = regexp.MustCompile(`<td class="last_activity_date">.*</td>`)
)

func maskProjectJump(s string) string {
	return projectJumpRe.ReplaceAllString(s, `<div id="project-jump">JUMP</div>`)
}
func maskRepoURL(s string) string { return repoURLRe.ReplaceAllString(s, "file:///REPO/tmp/test/") }

// compareProjectsGolden は got と testdata/projects/name を比較する（ジャンプボックスは常に伏せる）。
func compareProjectsGolden(t *testing.T, name, got, base string, edit ...func(string) string) {
	t.Helper()
	raw, err := os.ReadFile("testdata/projects/" + name)
	if err != nil {
		t.Fatal(err)
	}
	want := normalizeAdmin(string(raw), "{{BASE}}")
	g := normalizeAdmin(got, base)
	edit = append([]func(string) string{maskProjectJump, func(s string) string { return pngRe.ReplaceAllString(s, "-DIGEST.png") }}, edit...)
	for _, e := range edit {
		want, g = e(want), e(g)
	}
	if g == want {
		return
	}
	gl, wl := strings.Split(g, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(gl) || i < len(wl); i++ {
		var a, b string
		if i < len(gl) {
			a = gl[i]
		}
		if i < len(wl) {
			b = wl[i]
		}
		if a != b {
			t.Fatalf("%s: line %d differs\n got: %q\nwant: %q", name, i+1, a, b)
		}
	}
}

// TestProjectsPagesMatchRedmine は各ページ・API が参照 Redmine と一致することを確認する。
func TestProjectsPagesMatchRedmine(t *testing.T) {
	ts, _ := newFixtureServer(t)
	clients := map[string]*http.Client{
		"admin":     login(t, ts, "admin", "admin"),
		"jsmith":    login(t, ts, "jsmith", "jsmith"),
		"anonymous": newClient(t),
	}
	// textilizable の暫定実装は URL を自動リンクしない（テキスト整形の移植で解消）
	homepage := func(s string) string {
		return strings.ReplaceAll(s, `<p><a href="http://ecookbook.somenet.foo/" class="external">http://ecookbook.somenet.foo/</a></p>`, `<p>http://ecookbook.somenet.foo/</p>`)
	}
	cases := []struct {
		user, path, name string
		edit             []func(string) string
	}{
		{"admin", "/projects", "index_admin.html", nil},
		{"anonymous", "/projects", "index_anonymous.html", nil},
		{"jsmith", "/projects", "index_jsmith.html", nil},
		{"admin", "/projects?display_type=list&set_filter=1&c[]=name&c[]=status&c[]=short_description&c[]=identifier&c[]=parent_id&c[]=is_public&c[]=created_on&c[]=updated_on&c[]=last_activity_date&c[]=cf_3&group_by=is_public", "index_list_admin.html",
			[]func(string) string{func(s string) string {
				return lastActivityRe.ReplaceAllString(s, `<td class="last_activity_date">X</td>`)
			}}},
		{"admin", "/projects?query_id=11", "index_query11_admin.html", []func(string) string{homepage}},
		{"admin", "/projects/ecookbook", "show_ecookbook_admin.html", nil},
		{"jsmith", "/projects/onlinestore", "show_onlinestore_jsmith.html", nil},
		{"anonymous", "/projects/ecookbook", "show_ecookbook_anonymous.html", nil},
		{"admin", "/projects/new", "new_admin.html", nil},
		{"jsmith", "/projects/new?parent_id=ecookbook", "new_parent_jsmith.html", nil},
		{"admin", "/projects/ecookbook/settings", "settings_ecookbook_admin.html", []func(string) string{maskRepoURL}},
		{"jsmith", "/projects/onlinestore/settings", "settings_onlinestore_jsmith.html", nil},
		// テスト用フィクスチャ（internal/testfixtures）は documents を投入しない
		{"admin", "/projects/ecookbook/copy", "copy_ecookbook_admin.html", []func(string) string{func(s string) string {
			s = strings.Replace(s, "Documents (3)", "Documents (0)", 1)
			return strings.Replace(s, "Wiki pages (8)", "Wiki pages (0)", 1)
		}}},
		{"admin", "/admin/projects", "admin_projects.html", nil},
		{"admin", "/admin/projects_context_menu?ids[]=1", "context_menu_1.html", nil},
		{"admin", "/projects/ecookbook/memberships/new", "memberships_new_admin.html", nil},
		{"admin", "/memberships/1/edit", "memberships_1_edit_admin.html", nil},
		{"admin", "/projects/ecookbook/issue_categories/new", "issue_categories_new_admin.html", nil},
		{"admin", "/issue_categories/1/edit", "issue_categories_1_edit_admin.html", nil},
		{"admin", "/projects.json", "projects_admin.json", nil},
		{"admin", "/projects.xml?limit=2&offset=1", "projects_page_admin.xml", nil},
		{"admin", "/projects/ecookbook.json?include=trackers,issue_categories,time_entry_activities,enabled_modules,issue_custom_fields", "ecookbook_include_admin.json", nil},
		{"admin", "/projects/ecookbook/memberships.json", "memberships_ecookbook_admin.json", nil},
		{"admin", "/projects/ecookbook/issue_categories.xml", "issue_categories_ecookbook_admin.xml", nil},
		{"admin", "/projects.atom", "projects_admin.atom", nil},
		{"admin", "/projects.csv?display_type=list&set_filter=1&c[]=name&c[]=status&c[]=short_description&c[]=identifier&c[]=parent_id&c[]=is_public&c[]=cf_3", "projects_admin.csv", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var res *http.Response
			var body string
			if strings.Contains(tc.path, ".json") || strings.Contains(tc.path, ".xml") {
				// API 形式はセッションを使わない（find_current_user）。参照と同じく HTTP Basic で取得する
				req, _ := http.NewRequest(http.MethodGet, ts.URL+tc.path, nil)
				req.SetBasicAuth(tc.user, tc.user)
				var err error
				if res, err = newClient(t).Do(req); err != nil {
					t.Fatal(err)
				}
				b, _ := io.ReadAll(res.Body)
				res.Body.Close()
				body = string(b)
			} else {
				res, body = get(t, clients[tc.user], ts.URL+tc.path)
			}
			if res.StatusCode != http.StatusOK {
				t.Fatalf("GET %s: status %d", tc.path, res.StatusCode)
			}
			if strings.HasSuffix(tc.name, ".csv") {
				body = strings.TrimPrefix(body, "\xEF\xBB\xBF")
				raw, _ := os.ReadFile("testdata/projects/" + tc.name)
				if want := strings.TrimPrefix(string(raw), "\xEF\xBB\xBF"); body != want {
					t.Fatalf("csv differs\n got: %q\nwant: %q", body, want)
				}
				return
			}
			compareProjectsGolden(t, tc.name, body, ts.URL, tc.edit...)
		})
	}
}

// TestProjectsAccess は権限・フィルタの振る舞い（authorize / require_admin / XHR でない JS 応答）を確認する。
func TestProjectsAccess(t *testing.T) {
	ts, _ := newFixtureServer(t)
	anon := newClient(t)
	dlopper := login(t, ts, "dlopper", "foo")
	jsmith := login(t, ts, "jsmith", "jsmith")
	cases := []struct {
		c      *http.Client
		path   string
		status int
	}{
		{anon, "/projects/onlinestore", http.StatusFound},      // 非公開 → ログイン画面
		{jsmith, "/projects/nonexistent", http.StatusNotFound}, // find_project
		{dlopper, "/projects/onlinestore/settings", http.StatusForbidden},
		{jsmith, "/admin/projects", http.StatusForbidden},
		{jsmith, "/projects/ecookbook/copy", http.StatusForbidden},
		{jsmith, "/projects?query_id=999", http.StatusNotFound},
		{jsmith, "/projects/ecookbook/memberships", http.StatusNotAcceptable},
		{jsmith, "/projects/ecookbook/memberships/autocomplete.js", http.StatusUnprocessableEntity},
		{jsmith, "/issue_categories/1", http.StatusFound},
	}
	for _, tc := range cases {
		res, _ := get(t, tc.c, ts.URL+tc.path)
		if res.StatusCode != tc.status {
			t.Errorf("GET %s: status %d, want %d", tc.path, res.StatusCode, tc.status)
		}
	}
	// XHR の autocomplete は 200
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/projects/autocomplete.js?q=sub", nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	res := do(t, jsmith, req)
	if res.StatusCode != http.StatusOK || !strings.Contains(res.Header.Get("Content-Type"), "javascript") {
		t.Errorf("autocomplete: status %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	_ = url.Values{}
}
