package server_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// プロジェクト・メンバー・カテゴリ・作業分類の書き込み（作成・更新・削除）の振る舞いのテスト。
// HTML の一致は compat シナリオ（testdata/compat/scenarios/projects_write.yml）で確認しており、
// ここでは DB の状態とリダイレクト先を確認する。

var csrfMetaContentRe = regexp.MustCompile(`<meta name="csrf-token" content="([^"]+)"`)

// projSubmit は CSRF トークン付きでフォームを送る（method が POST 以外なら _method で上書き）。
func projSubmit(t *testing.T, c *http.Client, ts *httptest.Server, method, path string, form url.Values, xhr bool) (*http.Response, string) {
	t.Helper()
	_, page := get(t, c, ts.URL+"/projects")
	m := csrfMetaContentRe.FindStringSubmatch(page)
	if m == nil {
		t.Fatal("csrf-token meta not found")
	}
	if form == nil {
		form = url.Values{}
	}
	form.Set("authenticity_token", m[1])
	if method != http.MethodPost {
		form.Set("_method", strings.ToLower(method))
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if xhr {
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		req.Header.Set("Accept", "text/javascript, application/javascript, application/ecmascript, application/x-ecmascript, */*; q=0.01")
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := readUnbranded(res.Body)
	res.Body.Close()
	return res, string(b)
}

// apiRequest は HTTP Basic 認証で API を呼ぶ。
func projectsAPIRequest(t *testing.T, ts *httptest.Server, method, path, user, body string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	req.SetBasicAuth(user, user)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := newClient(t).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := readUnbranded(res.Body)
	res.Body.Close()
	return res, string(b)
}

func TestProjectsCreateUpdate(t *testing.T) {
	ts, d := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")

	// 検証エラー（名前が空・識別子が数字のみ）
	res, body := projSubmit(t, admin, ts, http.MethodPost, "/projects", url.Values{
		"project[name]": {""}, "project[identifier]": {"1234"},
	}, false)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Name cannot be blank") || !strings.Contains(body, "Identifier is invalid") {
		t.Fatalf("invalid create: status %d", res.StatusCode)
	}
	// 作成（親・継承メンバー・モジュール・トラッカー・カスタムフィールド）
	res, _ = projSubmit(t, admin, ts, http.MethodPost, "/projects", url.Values{
		"project[name]": {"New project"}, "project[identifier]": {"new-project"}, "project[parent_id]": {"1"},
		"project[inherit_members]": {"1"}, "project[custom_field_values][3]": {"Beta"},
		"project[enabled_module_names][]": {"issue_tracking", "wiki", ""}, "project[tracker_ids][]": {"1", "2", ""},
	}, false)
	expectRedirect(t, res, "/projects/new-project/settings")
	id := queryInt(t, d, `SELECT id FROM projects WHERE identifier = 'new-project'`)
	if n := queryInt(t, d, `SELECT COUNT(*) FROM project_closure WHERE descendant_id = ?`, id); n != 2 {
		t.Errorf("closure rows = %d, want 2", n)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM project_modules WHERE project_id = ?`, id); n != 2 {
		t.Errorf("modules = %d", n)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM wikis WHERE project_id = ?`, id); n != 1 {
		t.Errorf("wiki not created for the wiki module")
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM project_trackers WHERE project_id = ?`, id); n != 2 {
		t.Errorf("trackers = %d", n)
	}
	if v := queryString(t, d, `SELECT value FROM custom_values WHERE customized_kind = 'project' AND customized_id = ? AND custom_field_id = 3`, id); v != "Beta" {
		t.Errorf("custom value = %q", v)
	}
	// inherit_members: 親（eCookbook）のメンバーが継承ロールで追加される
	if n := queryInt(t, d, `SELECT COUNT(*) FROM members WHERE project_id = ?`, id); n != queryInt(t, d, `SELECT COUNT(*) FROM members WHERE project_id = 1`) {
		t.Errorf("inherited members = %d", n)
	}

	// 更新（名前・公開・親を外す・モジュール）
	res, _ = projSubmit(t, admin, ts, http.MethodPatch, "/projects/new-project", url.Values{
		"project[name]": {"Renamed"}, "project[is_public]": {"0"}, "project[parent_id]": {""},
		"project[enabled_module_names][]": {"issue_tracking", "boards", ""},
	}, false)
	expectRedirect(t, res, "/projects/new-project/settings")
	if n := queryString(t, d, `SELECT name FROM projects WHERE id = ?`, id); n != "Renamed" {
		t.Errorf("name = %q", n)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM projects WHERE id = ? AND parent_id IS NULL AND is_public = 0`, id); n != 1 {
		t.Errorf("parent/is_public not updated")
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM members WHERE project_id = ?`, id); n != 0 {
		t.Errorf("inherited members should be removed when the parent is removed (%d)", n)
	}
	// 設定タブ（issues）の更新は tab へ戻る
	res, _ = projSubmit(t, admin, ts, http.MethodPatch, "/projects/new-project", url.Values{
		"tab": {"issues"}, "project[tracker_ids][]": {"3", ""}, "project[default_issue_query_id]": {"4"},
	}, false)
	expectRedirect(t, res, "/projects/new-project/settings/issues")
	if n := queryInt(t, d, `SELECT COUNT(*) FROM project_trackers WHERE project_id = ? AND tracker_id = 3`, id); n != 1 {
		t.Errorf("trackers not updated")
	}

	// 非管理者の作成: 既定ロールでメンバーになる（jsmith は add_project を持つ）
	jsmith := login(t, ts, "jsmith", "jsmith")
	res, _ = projSubmit(t, jsmith, ts, http.MethodPost, "/projects", url.Values{
		"project[name]": {"Smith root"}, "project[identifier]": {"smith-root"},
	}, false)
	expectRedirect(t, res, "/projects/smith-root/settings")
	sid := queryInt(t, d, `SELECT id FROM projects WHERE identifier = 'smith-root'`)
	if n := queryInt(t, d, `SELECT COUNT(*) FROM members WHERE project_id = ? AND principal_id = 2`, sid); n != 1 {
		t.Errorf("creator is not a member")
	}

	// API
	res, body = projectsAPIRequest(t, ts, http.MethodPost, "/projects.json", "admin", `{"project":{"name":"API","identifier":"api-project"}}`)
	if res.StatusCode != http.StatusCreated || !strings.Contains(body, `"identifier":"api-project"`) {
		t.Fatalf("api create: %d %s", res.StatusCode, body)
	}
	res, _ = projectsAPIRequest(t, ts, http.MethodPut, "/projects/api-project.json", "admin", `{"project":{"name":"API 2"}}`)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("api update: %d", res.StatusCode)
	}
	res, body = projectsAPIRequest(t, ts, http.MethodPost, "/projects.json", "admin", `{"project":{"name":"API","identifier":"api-project"}}`)
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "Identifier has already been taken") {
		t.Fatalf("api create taken: %d %s", res.StatusCode, body)
	}
}

func TestProjectsStatusAndDestroy(t *testing.T) {
	ts, d := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	jsmith := login(t, ts, "jsmith", "jsmith")

	// close / reopen（close_project 権限、子孫も対象）
	res, _ := projSubmit(t, jsmith, ts, http.MethodPost, "/projects/ecookbook/close", nil, false)
	expectRedirect(t, res, "/projects/ecookbook")
	if n := queryInt(t, d, `SELECT COUNT(*) FROM projects WHERE id IN (1, 3, 4) AND status = 5`); n != 3 {
		t.Errorf("closed = %d", n)
	}
	res, _ = projSubmit(t, jsmith, ts, http.MethodPost, "/projects/ecookbook/reopen", nil, false)
	expectRedirect(t, res, "/projects/ecookbook")
	if n := queryInt(t, d, `SELECT COUNT(*) FROM projects WHERE status = 5`); n != 0 {
		t.Errorf("still closed = %d", n)
	}
	// archive / unarchive（管理者のみ）
	res, _ = projSubmit(t, jsmith, ts, http.MethodPost, "/projects/onlinestore/archive", nil, false)
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("jsmith archive: status %d", res.StatusCode)
	}
	res, _ = projSubmit(t, admin, ts, http.MethodPost, "/projects/onlinestore/archive", nil, false)
	expectRedirect(t, res, "/admin/projects")
	if n := queryInt(t, d, `SELECT status FROM projects WHERE id = 2`); n != 9 {
		t.Errorf("archived status = %d", n)
	}
	res, _ = projSubmit(t, admin, ts, http.MethodPost, "/projects/onlinestore/unarchive", nil, false)
	expectRedirect(t, res, "/admin/projects")
	if n := queryInt(t, d, `SELECT status FROM projects WHERE id = 2`); n != 1 {
		t.Errorf("unarchived status = %d", n)
	}
	// destroy: 確認なしなら確認画面、識別子が一致すれば子孫ごと削除
	res, body := projSubmit(t, admin, ts, http.MethodDelete, "/projects/private-child", nil, false)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Child of private child") {
		t.Fatalf("destroy confirmation: status %d", res.StatusCode)
	}
	res, _ = projSubmit(t, admin, ts, http.MethodDelete, "/projects/private-child", url.Values{"confirm": {"private-child"}}, false)
	expectRedirect(t, res, "/admin/projects")
	if n := queryInt(t, d, `SELECT COUNT(*) FROM projects WHERE id IN (5, 6)`); n != 0 {
		t.Errorf("projects not destroyed (%d)", n)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM custom_values WHERE customized_kind = 'project' AND customized_id IN (5, 6)`); n != 0 {
		t.Errorf("custom values remain (%d)", n)
	}
	// 削除権限のないユーザーは 403
	dlopper := login(t, ts, "dlopper", "foo")
	res, _ = projSubmit(t, dlopper, ts, http.MethodDelete, "/projects/ecookbook", url.Values{"confirm": {"ecookbook"}}, false)
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("dlopper destroy: status %d", res.StatusCode)
	}
	// bulk_destroy（"Yes" で確認）
	res, body = projSubmit(t, admin, ts, http.MethodDelete, "/projects/bulk_destroy?ids[]=3&ids[]=4", nil, false)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "eCookbook Subproject 1") {
		t.Fatalf("bulk destroy confirmation: status %d", res.StatusCode)
	}
	res, _ = projSubmit(t, admin, ts, http.MethodDelete, "/projects/bulk_destroy?ids[]=3&ids[]=4", url.Values{"confirm": {"Yes"}}, false)
	expectRedirect(t, res, "/admin/projects")
	if n := queryInt(t, d, `SELECT COUNT(*) FROM projects WHERE id IN (3, 4)`); n != 0 {
		t.Errorf("bulk destroy: %d remain", n)
	}
	// bookmark
	res, body = projSubmit(t, admin, ts, http.MethodPost, "/projects/onlinestore/bookmark", nil, true)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "bookmark") {
		t.Fatalf("bookmark: %d", res.StatusCode)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM user_project_bookmarks WHERE user_id = 1 AND project_id = 2`); n != 1 {
		t.Errorf("bookmark not stored")
	}
}

func TestMembersCategoriesActivities(t *testing.T) {
	ts, d := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")

	// メンバー追加（2 ユーザー）と XHR の失敗（ロールなし）
	res, _ := projSubmit(t, admin, ts, http.MethodPost, "/projects/onlinestore/memberships", url.Values{
		"membership[user_ids][]": {"4", "7"}, "membership[role_ids][]": {"3"},
	}, false)
	expectRedirect(t, res, "/projects/onlinestore/settings/members")
	if n := queryInt(t, d, `SELECT COUNT(*) FROM members WHERE project_id = 2 AND principal_id IN (4, 7)`); n != 2 {
		t.Errorf("members = %d", n)
	}
	res, body := projSubmit(t, admin, ts, http.MethodPost, "/projects/onlinestore/memberships", url.Values{
		"membership[user_ids][]": {"9"},
	}, true)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "alert(") {
		t.Fatalf("member js invalid: %d %s", res.StatusCode, body)
	}
	mid := queryInt(t, d, `SELECT id FROM members WHERE project_id = 2 AND principal_id = 4`)
	res, _ = projSubmit(t, admin, ts, http.MethodPut, "/memberships/"+itoa(mid), url.Values{"membership[role_ids][]": {"1", "2", ""}}, false)
	expectRedirect(t, res, "/projects/onlinestore/settings/members")
	if n := queryInt(t, d, `SELECT COUNT(*) FROM member_roles WHERE member_id = ?`, mid); n != 2 {
		t.Errorf("member roles = %d", n)
	}
	res, _ = projSubmit(t, admin, ts, http.MethodDelete, "/memberships/"+itoa(mid), nil, true)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("member destroy: %d", res.StatusCode)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM members WHERE id = ?`, mid); n != 0 {
		t.Errorf("member not destroyed")
	}

	// カテゴリ
	res, body = projSubmit(t, admin, ts, http.MethodPost, "/projects/onlinestore/issue_categories", url.Values{"issue_category[name]": {""}}, false)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Name cannot be blank") {
		t.Fatalf("category invalid: %d", res.StatusCode)
	}
	res, _ = projSubmit(t, admin, ts, http.MethodPost, "/projects/onlinestore/issue_categories", url.Values{"issue_category[name]": {"Store"}, "issue_category[assigned_to_id]": {"2"}}, false)
	expectRedirect(t, res, "/projects/onlinestore/settings/categories")
	cid := queryInt(t, d, `SELECT id FROM issue_categories WHERE project_id = 2 AND name = 'Store'`)
	res, _ = projSubmit(t, admin, ts, http.MethodPut, "/issue_categories/"+itoa(cid), url.Values{"issue_category[name]": {"Store 2"}}, false)
	expectRedirect(t, res, "/projects/onlinestore/settings/categories")
	// チケットのあるカテゴリは確認画面、todo=reassign で付け替えて削除
	res, body = projSubmit(t, admin, ts, http.MethodDelete, "/issue_categories/1", nil, false)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "reassign_to_id") {
		t.Fatalf("category destroy confirmation: %d", res.StatusCode)
	}
	issues := queryInt(t, d, `SELECT COUNT(*) FROM issues WHERE category_id = 1`)
	res, _ = projSubmit(t, admin, ts, http.MethodDelete, "/issue_categories/1", url.Values{"todo": {"reassign"}, "reassign_to_id": {"2"}}, false)
	expectRedirect(t, res, "/projects/ecookbook/settings/categories")
	if n := queryInt(t, d, `SELECT COUNT(*) FROM issues WHERE category_id = 2`); n < issues {
		t.Errorf("issues not reassigned")
	}

	// 作業分類: 上書き → リセット
	res, _ = projSubmit(t, admin, ts, http.MethodPut, "/projects/ecookbook/enumerations", url.Values{
		"enumerations[9][parent_id]": {"9"}, "enumerations[9][active]": {"0"},
	}, false)
	expectRedirect(t, res, "/projects/ecookbook/settings/activities")
	if n := queryInt(t, d, `SELECT COUNT(*) FROM time_entry_activities WHERE project_id = 1 AND parent_id = 9 AND active = 0`); n != 1 {
		t.Errorf("activity override not created")
	}
	res, _ = projSubmit(t, admin, ts, http.MethodDelete, "/projects/ecookbook/enumerations", nil, false)
	expectRedirect(t, res, "/projects/ecookbook/settings/activities")
	if n := queryInt(t, d, `SELECT COUNT(*) FROM time_entry_activities WHERE project_id = 1`); n != 0 {
		t.Errorf("activity overrides remain (%d)", n)
	}
}

func TestProjectsCopy(t *testing.T) {
	ts, d := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	res, _ := projSubmit(t, admin, ts, http.MethodPost, "/projects/ecookbook/copy", url.Values{
		"project[name]": {"Copied"}, "project[identifier]": {"copied"},
		"project[enabled_module_names][]": {"issue_tracking", "boards", ""},
		"only[]":                          {"members", "versions", "issue_categories", "queries", "boards", ""},
	}, false)
	expectRedirect(t, res, "/projects/copied/settings")
	id := queryInt(t, d, `SELECT id FROM projects WHERE identifier = 'copied'`)
	for _, c := range []struct{ table, src string }{
		{"versions", "project_id = 1"},
		{"issue_categories", "project_id = 1"},
		{"boards", "project_id = 1"},
		{"queries", "project_id = 1"},
	} {
		want := queryInt(t, d, `SELECT COUNT(*) FROM `+c.table+` WHERE `+c.src)
		if got := queryInt(t, d, `SELECT COUNT(*) FROM `+c.table+` WHERE project_id = ?`, id); got != want {
			t.Errorf("%s copied = %d, want %d", c.table, got, want)
		}
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM members WHERE project_id = ?`, id); n == 0 {
		t.Errorf("members not copied")
	}
}
