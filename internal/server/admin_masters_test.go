package server_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
)

// 管理画面のマスタ（ロール・トラッカー・ステータス・列挙）の互換テスト。
// testdata/admin_masters/ の期待値は共用の参照 Redmine（3998）から
// `go run ./tools/compat fetch -base http://127.0.0.1:3998 -raw -user admin <path>` で取得し、
// ベース URL を {{BASE}} に置換したもの（CSRF・フォーム名・ダイジェストは比較時に伏せる）。

var adminFormNameRe = regexp.MustCompile(`name="([A-Za-z0-9_]+)-[0-9a-f]{8}"`)

func normalizeAdmin(s, base string) string {
	s = normalize(normalizeFixture(s, base))
	return adminFormNameRe.ReplaceAllString(s, `name="$1-RANDOM"`)
}

// trackerProjectsRe はトラッカーのフォームのプロジェクトのツリー（兄弟の並びが参照環境と異なる部分）。
var trackerProjectsRe = regexp.MustCompile(`(?s)(<fieldset class="box" id="tracker_project_ids">).*?(</fieldset>)`)

// compareAdminGolden は got と testdata/admin_masters/name を正規化して比較する。
func compareAdminGolden(t *testing.T, name, got, base string, edit ...func(string) string) {
	t.Helper()
	raw, err := os.ReadFile("testdata/admin_masters/" + name)
	if err != nil {
		t.Fatal(err)
	}
	want := normalizeAdmin(string(raw), "{{BASE}}")
	g := normalizeAdmin(got, base)
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

func adminGet(t *testing.T, c *http.Client, u string) string {
	t.Helper()
	res, body := get(t, c, u)
	if res.StatusCode != 200 {
		t.Fatalf("GET %s: status %d", u, res.StatusCode)
	}
	return body
}

// TestAdminMastersPagesMatchRedmine は管理画面の各ページが参照 Redmine と一致することを確認する。
func TestAdminMastersPagesMatchRedmine(t *testing.T) {
	ts, _ := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")
	noTree := func(s string) string { return trackerProjectsRe.ReplaceAllString(s, "$1(projects)$2") }
	cases := []struct {
		name, path string
		edit       []func(string) string
	}{
		{"roles_index.html", "/roles", nil},
		{"roles_new.html", "/roles/new", nil},
		{"roles_new_copy.html", "/roles/new?copy=2", nil},
		{"roles_edit_1.html", "/roles/1/edit", nil},
		{"roles_edit_4.html", "/roles/4/edit", nil},
		{"roles_edit_5.html", "/roles/5/edit", nil},
		{"roles_permissions.html", "/roles/permissions", nil},
		{"roles_permissions_ids.html", "/roles/permissions?ids[]=1&ids[]=5", nil},
		{"roles.json", "/roles.json", nil},
		{"roles.xml", "/roles.xml", nil},
		// プロジェクトの兄弟の並び（docs/schema.md 17）は参照環境の lft と異なるため別途確認する
		{"trackers_index.html", "/trackers", nil},
		{"trackers_new.html", "/trackers/new", []func(string) string{noTree}},
		{"trackers_edit_2.html", "/trackers/2/edit", []func(string) string{noTree}},
		{"trackers_fields.html", "/trackers/fields", nil},
		{"trackers.json", "/trackers.json", nil},
		{"trackers.xml", "/trackers.xml", nil},
		{"issue_statuses_index.html", "/issue_statuses", nil},
		{"issue_statuses_new.html", "/issue_statuses/new", nil},
		{"issue_statuses_edit_1.html", "/issue_statuses/1/edit", nil},
		{"issue_statuses.json", "/issue_statuses.json", nil},
		{"issue_statuses.xml", "/issue_statuses.xml", nil},
		{"enumerations_index.html", "/enumerations", nil},
		{"enumerations_new_priority.html", "/enumerations/new?type=IssuePriority", nil},
		{"enumerations_new_activity.html", "/enumerations/new?type=TimeEntryActivity", nil},
		{"enumerations_edit_9.html", "/enumerations/9/edit", nil},
		{"enumerations_edit_2.html", "/enumerations/2/edit", nil},
		{"enumerations_priorities.json", "/enumerations/issue_priorities.json", nil},
		{"enumerations_document_categories.xml", "/enumerations/document_categories.xml", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			compareAdminGolden(t, tc.name, adminGet(t, c, ts.URL+tc.path), ts.URL, tc.edit...)
		})
	}

	t.Run("tracker project tree", func(t *testing.T) {
		body := adminGet(t, c, ts.URL+"/trackers/2/edit")
		// 兄弟は名前のバイト順（OnlineStore → eCookbook、'P' < 'e' で Private child が先）
		want := "<ul class='projects root'>\n" +
			`<li class='root'><div class='root'><label><input type="checkbox" name="tracker[project_ids][]" value="2" checked="checked" /> OnlineStore</label></div>` + "\n" +
			`</li><li class='root'><div class='root'><label><input type="checkbox" name="tracker[project_ids][]" value="1" checked="checked" /> eCookbook</label></div>` + "\n" +
			"<ul class='projects '>\n" +
			`<li class='child'><div class='child'><label><input type="checkbox" name="tracker[project_ids][]" value="5" checked="checked" /> Private child of eCookbook</label></div>` + "\n" +
			"<ul class='projects '>\n" +
			`<li class='child'><div class='child'><label><input type="checkbox" name="tracker[project_ids][]" value="6" /> Child of private child</label></div>` + "\n" +
			"</li></ul></li>\n" +
			`<li class='child'><div class='child'><label><input type="checkbox" name="tracker[project_ids][]" value="3" checked="checked" /> eCookbook Subproject 1</label></div>` + "\n" +
			`</li><li class='child'><div class='child'><label><input type="checkbox" name="tracker[project_ids][]" value="4" checked="checked" /> eCookbook Subproject 2</label></div>` + "\n" +
			"</li></ul>\n</li></ul>\n"
		if !strings.Contains(body, want) {
			t.Errorf("project tree mismatch:\n%s", extract(body, `<fieldset class="box" id="tracker_project_ids">`, `</fieldset>`))
		}
	})
}

// TestAdminMastersAccess は require_admin / require_admin_or_api_request を確認する。
func TestAdminMastersAccess(t *testing.T) {
	ts, _ := newFixtureServer(t)
	jsmith := login(t, ts, "jsmith", "jsmith")
	for _, p := range []string{"/roles", "/trackers", "/issue_statuses", "/enumerations"} {
		// 管理者以外のログインユーザーは HTML では 406、匿名はログイン画面へ
		if res, _ := get(t, jsmith, ts.URL+p); res.StatusCode != http.StatusNotAcceptable {
			t.Errorf("jsmith GET %s: status %d, want 406", p, res.StatusCode)
		}
		if res, _ := get(t, newClient(t), ts.URL+p); res.StatusCode != http.StatusFound ||
			!strings.Contains(res.Header.Get("Location"), "/login?back_url=") {
			t.Errorf("anonymous GET %s: status %d location %q", p, res.StatusCode, res.Header.Get("Location"))
		}
		// API は管理者でなくても使える
		if res, _ := get(t, jsmith, ts.URL+p+".json"); p != "/enumerations" && res.StatusCode != 200 {
			t.Errorf("jsmith GET %s.json: status %d", p, res.StatusCode)
		}
	}
	for _, p := range []string{"/roles/new", "/roles/1/edit", "/roles/permissions", "/trackers/new", "/trackers/fields",
		"/issue_statuses/new", "/enumerations/new?type=IssuePriority", "/enumerations/4/edit"} {
		if res, _ := get(t, jsmith, ts.URL+p); res.StatusCode != http.StatusForbidden {
			t.Errorf("jsmith GET %s: status %d, want 403", p, res.StatusCode)
		}
	}
	admin := login(t, ts, "admin", "admin")
	for p, want := range map[string]int{
		"/roles/999/edit": 404, "/trackers/999/edit": 404, "/issue_statuses/999/edit": 404, "/enumerations/999/edit": 404,
		"/enumerations/new?type=Enumeration": 404, "/enumerations/foo.json": 404, "/roles/1": 406,
	} {
		if res, _ := get(t, admin, ts.URL+p); res.StatusCode != want {
			t.Errorf("admin GET %s: status %d, want %d", p, res.StatusCode, want)
		}
	}
}

// adminSubmit は CSRF トークン付きでフォームを送る（method が POST 以外なら _method で上書き）。
func adminSubmit(t *testing.T, c *http.Client, ts *httptest.Server, method, path string, form url.Values) (*http.Response, string) {
	t.Helper()
	_, page := get(t, c, ts.URL+"/roles/new")
	if form == nil {
		form = url.Values{}
	}
	form.Set("authenticity_token", csrfToken(t, page))
	if method != http.MethodPost {
		form.Set("_method", strings.ToLower(method))
	}
	return post(t, c, ts.URL+path, form)
}

// adminXHR は並べ替え（positionedItems）と同じ XHR の PUT を送る。
func adminXHR(t *testing.T, c *http.Client, ts *httptest.Server, path string, form url.Values) *http.Response {
	t.Helper()
	_, page := get(t, c, ts.URL+"/roles/new")
	form.Set("authenticity_token", csrfToken(t, page))
	req, _ := http.NewRequest(http.MethodPut, ts.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Accept", "text/javascript, application/javascript, application/ecmascript, application/x-ecmascript, */*; q=0.01")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return res
}

func expectRedirect(t *testing.T, res *http.Response, path string) {
	t.Helper()
	if res.StatusCode != http.StatusFound || !strings.HasSuffix(res.Header.Get("Location"), path) {
		t.Fatalf("status %d location %q, want redirect to %s", res.StatusCode, res.Header.Get("Location"), path)
	}
}

func queryInt(t *testing.T, d *db.DB, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := d.Get(context.Background(), &n, q, args...); err != nil {
		t.Fatal(err)
	}
	return n
}

func queryString(t *testing.T, d *db.DB, q string, args ...any) string {
	t.Helper()
	var s string
	if err := d.Get(context.Background(), &s, q, args...); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRolesWrite(t *testing.T) {
	ts, d := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")

	// 名前なし → new を再描画してエラー
	res, body := adminSubmit(t, c, ts, http.MethodPost, "/roles", url.Values{"role[name]": {""}, "role[permissions][]": {"add_issues", ""}})
	if res.StatusCode != 200 || !strings.Contains(body, "<li>Name cannot be blank</li>") ||
		!strings.Contains(body, `value="add_issues" data-shows=".add_issues_shown" checked="checked" />`) {
		t.Fatalf("invalid create: status %d\n%s", res.StatusCode, extract(body, "<div id='errorExplanation'>", "</div>"))
	}

	res, _ = adminSubmit(t, c, ts, http.MethodPost, "/roles", url.Values{
		"role[name]": {"Tester"}, "role[assignable]": {"0"}, "role[all_roles_managed]": {"0"},
		"role[managed_role_ids][]":                     {"2", ""},
		"role[permissions][]":                          {"view_issues", "add_issues", ""},
		"role[permissions_all_trackers][view_issues]":  {"0"},
		"role[permissions_all_trackers][add_issues]":   {"1"},
		"role[permissions_tracker_ids][view_issues][]": {"1", "3", ""},
		"role[default_time_entry_activity_id]":         {"9"},
		"copy_workflow_from":                           {"1"},
	})
	expectRedirect(t, res, "/roles")
	id := queryInt(t, d, `SELECT id FROM roles WHERE name = 'Tester'`)
	if id != 6 {
		t.Errorf("new role id %d", id)
	}
	if n := queryInt(t, d, `SELECT position FROM roles WHERE id = ?`, id); n != 4 {
		t.Errorf("position %d, want 4", n)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM role_permission_trackers WHERE role_id = ? AND permission = 'view_issues'`, id); n != 2 {
		t.Errorf("view_issues trackers %d", n)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM role_permissions WHERE role_id = ? AND all_trackers = ?`, id, false); n != 1 {
		t.Errorf("restricted permissions %d", n)
	}
	if a, b := queryInt(t, d, `SELECT COUNT(*) FROM workflow_transitions WHERE role_id = ?`, id),
		queryInt(t, d, `SELECT COUNT(*) FROM workflow_transitions WHERE role_id = 1`); a != b || a == 0 {
		t.Errorf("copied workflows %d, want %d", a, b)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM roles_managed_roles WHERE role_id = ?`, id); n != 1 {
		t.Errorf("managed roles %d", n)
	}
	body = adminGet(t, c, ts.URL+"/roles")
	if !strings.Contains(body, `<div class="flash notice" id="flash_notice">`) || !strings.Contains(body, "Successful creation.") {
		t.Error("flash notice not shown")
	}

	// 名前の重複 → edit を再描画（タイトルは入力中の名前）
	res, body = adminSubmit(t, c, ts, http.MethodPatch, "/roles/6", url.Values{"role[name]": {"Manager"}})
	if res.StatusCode != 200 || !strings.Contains(body, "<li>Name has already been taken</li>") || !strings.Contains(body, "<title>Manager - Roles - Redmine</title>") {
		t.Fatalf("taken: status %d", res.StatusCode)
	}

	// 並べ替え（XHR の PUT は head :ok）
	if res := adminXHR(t, c, ts, "/roles/6", url.Values{"role[position]": {"1"}}); res.StatusCode != 200 {
		t.Fatalf("reorder: status %d", res.StatusCode)
	}
	got := queryString(t, d, `SELECT GROUP_CONCAT(name, ',') FROM (SELECT name FROM roles WHERE builtin = 0 ORDER BY position)`)
	if got != "Tester,Manager,Developer,Reporter" {
		t.Errorf("order after reorder: %s", got)
	}
	// 失敗は 422
	if res := adminXHR(t, c, ts, "/roles/6", url.Values{"role[name]": {""}}); res.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("invalid xhr update: status %d", res.StatusCode)
	}

	// 使用中のロールは削除できない（プロジェクトのメンバー設定へのリンク付きのエラー）
	res, body = adminSubmit(t, c, ts, http.MethodDelete, "/roles/1", nil)
	if res.StatusCode != 200 || !strings.Contains(body, "This role is in use and cannot be deleted.") ||
		!strings.Contains(body, `<a href="/projects/ecookbook/settings/members">eCookbook</a>`) {
		t.Fatalf("destroy in use: status %d\n%s", res.StatusCode, extract(body, `<div class="flash error"`, "</div>"))
	}
	res, _ = adminSubmit(t, c, ts, http.MethodDelete, "/roles/6", nil)
	expectRedirect(t, res, "/roles")
	got = queryString(t, d, `SELECT GROUP_CONCAT(position, ',') FROM (SELECT position FROM roles WHERE builtin = 0 ORDER BY position)`)
	if got != "1,2,3" {
		t.Errorf("positions after destroy: %s", got)
	}

	// 権限レポートの一括更新（トラッカー制限は保持する）
	res, _ = adminSubmit(t, c, ts, http.MethodPost, "/roles/permissions", url.Values{
		"permissions[3][]": {"view_issues", "add_issues", ""}, "permissions[5][]": {""},
	})
	expectRedirect(t, res, "/roles")
	if n := queryInt(t, d, `SELECT COUNT(*) FROM role_permissions WHERE role_id = 3`); n != 2 {
		t.Errorf("reporter permissions %d", n)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM role_permissions WHERE role_id = 5`); n != 0 {
		t.Errorf("anonymous permissions %d", n)
	}

	// CSV（en の既定は ISO-8859-1・BOM なし、空文字列は "" で引用）
	res, body = get(t, c, ts.URL+"/roles/permissions.csv?ids[]=3")
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/csv") ||
		!strings.HasPrefix(body, "Module,Permissions,Reporter\n\"\",Create project,No\n") {
		t.Errorf("csv: status %d %q", res.StatusCode, body[:min(len(body), 80)])
	}
}

func TestTrackersWrite(t *testing.T) {
	ts, d := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")

	res, body := adminSubmit(t, c, ts, http.MethodPost, "/trackers", url.Values{"tracker[name]": {"Bug"}, "tracker[default_status_id]": {""}})
	if res.StatusCode != 200 || !strings.Contains(body, "<li>Default status cannot be blank</li>\n<li>Name has already been taken</li>") {
		t.Fatalf("invalid create: status %d\n%s", res.StatusCode, extract(body, "<div id='errorExplanation'>", "</div>"))
	}

	res, _ = adminSubmit(t, c, ts, http.MethodPost, "/trackers", url.Values{
		"tracker[name]": {"QA"}, "tracker[default_status_id]": {"2"}, "tracker[is_in_roadmap]": {"0"},
		"tracker[core_fields][]":      {"assigned_to_id", "due_date", ""},
		"tracker[custom_field_ids][]": {"1", "6", ""},
		"tracker[project_ids][]":      {"1", "5", ""},
		"copy_workflow_from":          {"2"},
	})
	expectRedirect(t, res, "/trackers")
	if got := queryString(t, d, `SELECT disabled_core_fields FROM trackers WHERE id = 4`); got !=
		`["category_id","fixed_version_id","parent_issue_id","start_date","estimated_hours","done_ratio","description","priority_id"]` {
		t.Errorf("disabled core fields %s", got)
	}
	if n := queryInt(t, d, `SELECT position FROM trackers WHERE id = 4`); n != 4 {
		t.Errorf("position %d", n)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM project_trackers WHERE tracker_id = 4`); n != 2 {
		t.Errorf("projects %d", n)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM custom_fields_trackers WHERE tracker_id = 4`); n != 2 {
		t.Errorf("custom fields %d", n)
	}
	if a, b := queryInt(t, d, `SELECT COUNT(*) FROM workflow_transitions WHERE tracker_id = 4`),
		queryInt(t, d, `SELECT COUNT(*) FROM workflow_transitions WHERE tracker_id = 2`); a != b || a == 0 {
		t.Errorf("copied workflows %d, want %d", a, b)
	}

	if res := adminXHR(t, c, ts, "/trackers/4", url.Values{"tracker[position]": {"1"}}); res.StatusCode != 200 {
		t.Fatalf("reorder: status %d", res.StatusCode)
	}
	if got := queryString(t, d, `SELECT GROUP_CONCAT(id, ',') FROM (SELECT id FROM trackers ORDER BY position)`); got != "4,1,2,3" {
		t.Errorf("order %s", got)
	}

	// 使用中のトラッカーは削除できない
	res, body = adminSubmit(t, c, ts, http.MethodDelete, "/trackers/1", nil)
	if res.StatusCode != 200 || !strings.Contains(body, "This tracker contains issues and cannot be deleted.") ||
		!strings.Contains(body, `<a href="/projects/ecookbook/issues?set_filter=1&amp;status_id=%2A&amp;tracker_id=1">eCookbook</a>`) {
		t.Fatalf("destroy in use: status %d\n%s", res.StatusCode, extract(body, `<div class="flash error"`, "</div>"))
	}
	res, _ = adminSubmit(t, c, ts, http.MethodDelete, "/trackers/4", nil)
	expectRedirect(t, res, "/trackers")
	if got := queryString(t, d, `SELECT GROUP_CONCAT(position, ',') FROM (SELECT position FROM trackers ORDER BY position)`); got != "1,2,3" {
		t.Errorf("positions after destroy %s", got)
	}

	res, _ = adminSubmit(t, c, ts, http.MethodPost, "/trackers/fields", url.Values{
		"trackers[1][core_fields][]": {"assigned_to_id", ""}, "trackers[1][custom_field_ids][]": {""},
		"trackers[2][core_fields][]": {""}, "trackers[2][custom_field_ids][]": {"2", ""},
	})
	expectRedirect(t, res, "/trackers/fields")
	if n := queryInt(t, d, `SELECT COUNT(*) FROM custom_fields_trackers WHERE tracker_id = 1`); n != 0 {
		t.Errorf("tracker 1 custom fields %d", n)
	}
	if got := queryString(t, d, `SELECT disabled_core_fields FROM trackers WHERE id = 2`); !strings.Contains(got, `"assigned_to_id"`) {
		t.Errorf("tracker 2 disabled %s", got)
	}
}

func TestIssueStatusesWrite(t *testing.T) {
	ts, d := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")

	res, body := adminSubmit(t, c, ts, http.MethodPost, "/issue_statuses", url.Values{"issue_status[name]": {"New"}})
	if res.StatusCode != 200 || !strings.Contains(body, "<li>Name has already been taken</li>") {
		t.Fatalf("invalid create: status %d", res.StatusCode)
	}
	res, _ = adminSubmit(t, c, ts, http.MethodPost, "/issue_statuses", url.Values{"issue_status[name]": {"Testing"}, "issue_status[is_closed]": {"0"}})
	expectRedirect(t, res, "/issue_statuses")
	id := queryInt(t, d, `SELECT id FROM issue_statuses WHERE name = 'Testing'`)

	// チケットを付けてから終了ステータスにすると closed_at が埋まる
	if _, err := d.Exec(context.Background(), `UPDATE issues SET status_id = ?, closed_at = NULL WHERE id = 1`, id); err != nil {
		t.Fatal(err)
	}
	res, _ = adminSubmit(t, c, ts, http.MethodPatch, "/issue_statuses/"+itoa(id), url.Values{"issue_status[is_closed]": {"1"}})
	expectRedirect(t, res, "/issue_statuses")
	if n := queryInt(t, d, `SELECT COUNT(*) FROM issues WHERE id = 1 AND closed_at IS NOT NULL`); n != 1 {
		t.Error("closed_at not set")
	}

	if res := adminXHR(t, c, ts, "/issue_statuses/"+itoa(id), url.Values{"issue_status[position]": {"1"}}); res.StatusCode != 200 {
		t.Fatalf("reorder: status %d", res.StatusCode)
	}
	if n := queryInt(t, d, `SELECT position FROM issue_statuses WHERE id = 1`); n != 2 {
		t.Errorf("status 1 position %d", n)
	}

	res, _ = adminSubmit(t, c, ts, http.MethodDelete, "/issue_statuses/"+itoa(id), nil)
	expectRedirect(t, res, "/issue_statuses")
	body = adminGet(t, c, ts.URL+"/issue_statuses")
	if !strings.Contains(body, "Unable to delete issue status (This status is used by some issues)") {
		t.Errorf("in use error not shown:\n%s", extract(body, `<div class="flash`, "</div>"))
	}

	res, _ = adminSubmit(t, c, ts, http.MethodPost, "/issue_statuses/update_issue_done_ratio", nil)
	expectRedirect(t, res, "/issue_statuses")
	if body := adminGet(t, c, ts.URL+"/issue_statuses"); !strings.Contains(body, "Issue done ratios not updated.") {
		t.Error("done ratio error not shown")
	}
}

func TestEnumerationsWrite(t *testing.T) {
	ts, d := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")

	res, body := adminSubmit(t, c, ts, http.MethodPost, "/enumerations", url.Values{"enumeration[type]": {"IssuePriority"}, "enumeration[name]": {"Low"}})
	if res.StatusCode != 200 || !strings.Contains(body, "<li>Name has already been taken</li>") {
		t.Fatalf("invalid create: status %d", res.StatusCode)
	}
	res, _ = adminSubmit(t, c, ts, http.MethodPost, "/enumerations", url.Values{
		"enumeration[type]": {"IssuePriority"}, "enumeration[name]": {"Critical"}, "enumeration[active]": {"1"}, "enumeration[is_default]": {"1"},
	})
	expectRedirect(t, res, "/enumerations")
	// id は種類をまたいで一意（Redmine の enumerations.id の続き）
	if id := queryInt(t, d, `SELECT id FROM issue_priorities WHERE name = 'Critical'`); id != 17 {
		t.Errorf("new id %d", id)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM issue_priorities WHERE is_default = ?`, true); n != 1 {
		t.Errorf("defaults %d", n)
	}
	if got := queryString(t, d, `SELECT position_name FROM issue_priorities WHERE id = 17`); got != "default" {
		t.Errorf("position name %s", got)
	}

	res, _ = adminSubmit(t, c, ts, http.MethodPost, "/enumerations", url.Values{
		"enumeration[type]": {"TimeEntryActivity"}, "enumeration[name]": {"Support"}, "enumeration[custom_field_values][7]": {"1"},
	})
	expectRedirect(t, res, "/enumerations")
	if got := queryString(t, d, `SELECT value FROM custom_values WHERE customized_kind = 'enumeration' AND customized_id = 18`); got != "1" {
		t.Errorf("custom value %q", got)
	}
	if body := adminGet(t, c, ts.URL+"/enumerations/18/edit"); !strings.Contains(body, `<option selected="selected" value="1">Yes</option>`) {
		t.Error("custom value not selected")
	}

	if res := adminXHR(t, c, ts, "/enumerations/17", url.Values{"enumeration[position]": {"1"}}); res.StatusCode != 200 {
		t.Fatalf("reorder: status %d", res.StatusCode)
	}
	if got := queryString(t, d, `SELECT GROUP_CONCAT(id, ',') FROM (SELECT id FROM issue_priorities ORDER BY position)`); got != "17,4,5,6,7,8,15" {
		t.Errorf("order %s", got)
	}

	// 使用中なら付け替え先を選ぶ画面
	res, body = adminSubmit(t, c, ts, http.MethodDelete, "/enumerations/4", nil)
	if res.StatusCode != 200 || !strings.Contains(body, `<select name="reassign_to_id" id="reassign_to_id">`) ||
		strings.Contains(body, `<option value="4">`) {
		t.Fatalf("destroy in use: status %d", res.StatusCode)
	}
	used := queryInt(t, d, `SELECT COUNT(*) FROM issues WHERE priority_id = 4`)
	before := queryInt(t, d, `SELECT COUNT(*) FROM issues WHERE priority_id = 5`)
	res, _ = adminSubmit(t, c, ts, http.MethodDelete, "/enumerations/4", url.Values{"reassign_to_id": {"5"}})
	expectRedirect(t, res, "/enumerations")
	if n := queryInt(t, d, `SELECT COUNT(*) FROM issues WHERE priority_id = 5`); n != before+used {
		t.Errorf("reassigned %d, want %d", n, before+used)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM issue_priorities WHERE id = 4`); n != 0 {
		t.Error("priority not deleted")
	}
	res, _ = adminSubmit(t, c, ts, http.MethodDelete, "/enumerations/18", nil)
	expectRedirect(t, res, "/enumerations")
	if n := queryInt(t, d, `SELECT COUNT(*) FROM custom_values WHERE customized_kind = 'enumeration' AND customized_id = 18`); n != 0 {
		t.Error("custom values not deleted")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
