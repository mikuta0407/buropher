// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

// 保存クエリ（QueriesController: new / create / edit / update / destroy / filter / index）のテスト。
// testdata/queries/ の期待値は共用の参照 Redmine（3998）から GET のみで
// `go run ./tools/compat fetch -raw -user <user> <path>` で取得し、ベース URL を {{BASE}} に置換したもの。
// 「最近使ったプロジェクト」は参照の取得順に依存するため、ジャンプボックスは伏せて比較する。

// queriesJumpRe はジャンプボックス（project-jump）。
var queriesJumpRe = regexp.MustCompile(`<div id="project-jump".*</div></div></div>`)

func compareQueriesGolden(t *testing.T, name, got, base string) {
	t.Helper()
	if recaptureGolden(t, "testdata/queries/"+name, got, base) {
		return
	}
	raw, err := os.ReadFile("testdata/queries/" + name)
	if err != nil {
		t.Fatal(err)
	}
	mask := func(s string) string {
		s = queriesJumpRe.ReplaceAllString(s, `<div id="project-jump">JUMP</div>`)
		return pngRe.ReplaceAllString(s, "-DIGEST.png")
	}
	want := mask(normalizeAdmin(string(raw), "{{BASE}}"))
	g := mask(normalizeAdmin(got, base))
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

// TestQueriesPagesMatchRedmine は new / edit のフォームが参照 Redmine と一致することを確認する。
func TestQueriesPagesMatchRedmine(t *testing.T) {
	ts, _ := newFixtureServer(t)
	clients := map[string]*http.Client{
		"admin":  login(t, ts, "admin", "admin"),
		"jsmith": login(t, ts, "jsmith", "jsmith"),
	}
	cases := []struct{ user, path, name string }{
		{"admin", "/queries/new", "new_admin.html"},
		{"admin", "/projects/ecookbook/queries/new", "new_project_admin.html"},
		{"jsmith", "/projects/ecookbook/queries/new", "new_project_jsmith.html"},
		{"admin", "/queries/new?type=TimeEntryQuery", "new_time_entry_admin.html"},
		{"admin", "/queries/new?type=ProjectQuery", "new_project_query_admin.html"},
		{"admin", "/queries/new?type=UserQuery", "new_user_query_admin.html"},
		{"admin", "/queries/new?type=ProjectAdminQuery", "new_project_admin_query_admin.html"},
		{"admin", "/projects/ecookbook/queries/new?gantt=1", "new_gantt_admin.html"},
		{"admin", "/projects/ecookbook/queries/new?calendar=1", "new_calendar_admin.html"},
		{"admin", "/queries/1/edit", "edit_1_admin.html"},
		{"jsmith", "/queries/1/edit", "edit_1_jsmith.html"},
		{"admin", "/queries/new?set_filter=1&f[]=status_id&op[status_id]=o&f[]=tracker_id&op[tracker_id]==&v[tracker_id][]=1&c[]=subject&c[]=status&group_by=tracker&sort=priority:desc,id", "new_params_admin.html"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, body := get(t, clients[tc.user], ts.URL+tc.path)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("GET %s: status %d", tc.path, res.StatusCode)
			}
			compareQueriesGolden(t, tc.name, body, ts.URL)
		})
	}
}

// postQueries は CSRF トークン付きで method（POST / PUT / DELETE は _method）を送る。
func postQueries(t *testing.T, c *http.Client, base, path, method string, form url.Values) (*http.Response, string) {
	t.Helper()
	_, page := get(t, c, base+"/my/page")
	form.Set("authenticity_token", csrfToken(t, page))
	if method != http.MethodPost {
		form.Set("_method", strings.ToLower(method))
	}
	return post(t, c, base+path, form)
}

// queryRow は queries テーブルの 1 行（確認用）。
type queryRow struct {
	ID          int64   `db:"id"`
	Kind        string  `db:"kind"`
	ProjectID   *int64  `db:"project_id"`
	UserID      *int64  `db:"user_id"`
	Name        string  `db:"name"`
	Description *string `db:"description"`
	Visibility  int     `db:"visibility"`
	Filters     *string `db:"filters"`
	ColumnNames *string `db:"column_names"`
	SortCrit    *string `db:"sort_criteria"`
	Options     *string `db:"options"`
}

// TestQueriesControllerFunctional は test/functional/queries_controller_test.rb の主要なケースの移植。
func TestQueriesControllerFunctional(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	admin := login(t, ts, "admin", "admin")
	jsmith := login(t, ts, "jsmith", "jsmith")
	dlopper := login(t, ts, "dlopper", "foo")

	findByName := func(t *testing.T, name string) *queryRow {
		t.Helper()
		var r queryRow
		if err := d.Get(ctx, &r, `SELECT id, kind, project_id, user_id, name, description, visibility, filters, column_names, sort_criteria, options FROM queries WHERE name = ?`, name); err != nil {
			t.Fatalf("query %q: %v", name, err)
		}
		return &r
	}
	roleIDs := func(t *testing.T, id int64) []int64 {
		var ids []int64
		if err := d.Select(ctx, &ids, `SELECT role_id FROM queries_roles WHERE query_id = ? ORDER BY role_id`, id); err != nil {
			t.Fatal(err)
		}
		return ids
	}
	expectRedirect := func(t *testing.T, res *http.Response, want string) {
		t.Helper()
		if res.StatusCode != http.StatusFound {
			t.Fatalf("status %d, want 302", res.StatusCode)
		}
		if loc := strings.TrimPrefix(res.Header.Get("Location"), ts.URL); loc != want {
			t.Fatalf("Location = %q, want %q", loc, want)
		}
	}
	itoa := func(n int64) string { return strconvItoa(n) }

	t.Run("index_html_406", func(t *testing.T) {
		res, _ := get(t, newClient(t), ts.URL+"/queries")
		if res.StatusCode != http.StatusNotAcceptable {
			t.Fatalf("status %d", res.StatusCode)
		}
	})

	t.Run("new_on_invalid_project", func(t *testing.T) {
		res, _ := get(t, jsmith, ts.URL+"/projects/invalid/queries/new")
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("status %d", res.StatusCode)
		}
	})

	t.Run("new_global_query_jsmith", func(t *testing.T) {
		_, body := get(t, jsmith, ts.URL+"/queries/new")
		if strings.Contains(body, `name="query[visibility]"`) {
			t.Error("visibility shown without manage_public_queries")
		}
		if !strings.Contains(body, `<input type="checkbox" name="query_is_for_all" id="query_is_for_all" value="1" class="disable-unless-private" checked="checked" />`) {
			t.Error("for all projects checkbox")
		}
	})

	t.Run("new_with_calendar", func(t *testing.T) {
		_, body := get(t, jsmith, ts.URL+"/queries/new?calendar=1")
		if !strings.Contains(body, `id="calendar"`) || strings.Contains(body, `fieldset id="options"`) ||
			strings.Contains(body, `fieldset id="columns"`) || strings.Contains(body, `<legend>Sort</legend>`) {
			t.Error("calendar form")
		}
	})

	t.Run("backslash_escaped_in_filters", func(t *testing.T) {
		_, body := get(t, jsmith, ts.URL+"/queries/new?subject=foo/bar")
		if !strings.Contains(body, `addFilter("subject", "=", ["foo\/bar"]);`) {
			t.Error("subject filter not escaped")
		}
	})

	t.Run("new_anonymous_requires_login", func(t *testing.T) {
		res, _ := get(t, newClient(t), ts.URL+"/queries/new")
		if res.StatusCode != http.StatusFound || !strings.Contains(res.Header.Get("Location"), "/login?back_url=") {
			t.Fatalf("status %d %s", res.StatusCode, res.Header.Get("Location"))
		}
	})

	t.Run("create_project_public_query", func(t *testing.T) {
		res, _ := postQueries(t, jsmith, ts.URL, "/projects/ecookbook/queries", http.MethodPost, url.Values{
			"default_columns": {"1"}, "f[]": {"status_id", "assigned_to_id"},
			"op[assigned_to_id]": {"="}, "op[status_id]": {"o"},
			"v[assigned_to_id][]": {"1"}, "v[status_id][]": {"1"},
			"query[name]": {"test_new_project_public_query"}, "query[visibility]": {"2"},
		})
		q := findByName(t, "test_new_project_public_query")
		expectRedirect(t, res, "/projects/ecookbook/issues?query_id="+itoa(q.ID))
		if q.Visibility != 2 || q.ColumnNames != nil || q.ProjectID == nil || *q.ProjectID != 1 || q.UserID == nil || *q.UserID != 2 {
			t.Errorf("query = %+v", q)
		}
	})

	t.Run("create_project_private_query_legacy_params", func(t *testing.T) {
		res, _ := postQueries(t, dlopper, ts.URL, "/projects/ecookbook/queries", http.MethodPost, url.Values{
			"default_columns": {"1"}, "fields[]": {"status_id", "assigned_to_id"},
			"operators[assigned_to_id]": {"="}, "operators[status_id]": {"o"},
			"values[assigned_to_id][]": {"1"}, "values[status_id][]": {"1"},
			"query[name]": {"test_new_project_private_query"}, "query[visibility]": {"0"},
		})
		q := findByName(t, "test_new_project_private_query")
		expectRedirect(t, res, "/projects/ecookbook/issues?query_id="+itoa(q.ID))
		if q.Visibility != 0 {
			t.Errorf("visibility %d", q.Visibility)
		}
	})

	t.Run("create_project_roles_query", func(t *testing.T) {
		res, _ := postQueries(t, jsmith, ts.URL, "/projects/ecookbook/queries", http.MethodPost, url.Values{
			"default_columns": {"1"}, "query[name]": {"test_create_project_roles_query"},
			"query[visibility]": {"1"}, "query[role_ids][]": {"1", "2", ""},
		})
		q := findByName(t, "test_create_project_roles_query")
		expectRedirect(t, res, "/projects/ecookbook/issues?query_id="+itoa(q.ID))
		if q.Visibility != 1 {
			t.Errorf("visibility %d", q.Visibility)
		}
		if got := roleIDs(t, q.ID); len(got) != 2 || got[0] != 1 || got[1] != 2 {
			t.Errorf("roles %v", got)
		}
	})

	t.Run("create_global_private_query_with_custom_columns", func(t *testing.T) {
		res, _ := postQueries(t, dlopper, ts.URL, "/queries", http.MethodPost, url.Values{
			"fields[]": {"status_id", "assigned_to_id"}, "operators[assigned_to_id]": {"="}, "operators[status_id]": {"o"},
			"values[assigned_to_id][]": {"me"}, "values[status_id][]": {"1"},
			"query[name]": {"test_new_global_private_query"}, "query[visibility]": {"0"},
			"c[]": {"", "tracker", "subject", "priority", "category"},
		})
		q := findByName(t, "test_new_global_private_query")
		expectRedirect(t, res, "/issues?query_id="+itoa(q.ID))
		if q.ProjectID != nil || q.ColumnNames == nil || !strings.Contains(*q.ColumnNames, `"tracker","subject","priority","category"`) {
			t.Errorf("query = %+v", q)
		}
	})

	t.Run("create_global_query_with_custom_filters", func(t *testing.T) {
		postQueries(t, dlopper, ts.URL, "/queries", http.MethodPost, url.Values{
			"fields[]": {"assigned_to_id"}, "operators[assigned_to_id]": {"="}, "values[assigned_to_id][]": {"me"},
			"query[name]": {"test_new_global_query"},
		})
		q := findByName(t, "test_new_global_query")
		if q.Filters == nil || strings.Contains(*q.Filters, "status_id") || !strings.Contains(*q.Filters, "assigned_to_id") {
			t.Errorf("filters %v", q.Filters)
		}
	})

	t.Run("create_with_sort", func(t *testing.T) {
		postQueries(t, admin, ts.URL, "/queries", http.MethodPost, url.Values{
			"default_columns": {"1"}, "operators[status_id]": {"o"}, "values[status_id][]": {"1"},
			"query[name]": {"test_new_with_sort"}, "query[visibility]": {"2"},
			"query[sort_criteria][0][]": {"due_date", "desc"}, "query[sort_criteria][1][]": {"tracker", ""},
		})
		q := findByName(t, "test_new_with_sort")
		var sc [][]string
		if q.SortCrit == nil || json.Unmarshal([]byte(*q.SortCrit), &sc) != nil {
			t.Fatalf("sort criteria %v", q.SortCrit)
		}
		if len(sc) != 2 || sc[0][0] != "due_date" || sc[0][1] != "desc" || sc[1][0] != "tracker" || sc[1][1] != "asc" {
			t.Errorf("sort criteria %v", sc)
		}
	})

	t.Run("create_with_failure", func(t *testing.T) {
		res, body := postQueries(t, jsmith, ts.URL, "/projects/ecookbook/queries", http.MethodPost, url.Values{"query[name]": {""}})
		if res.StatusCode != http.StatusOK || !strings.Contains(body, `name="query[name]"`) ||
			!strings.Contains(body, "<li>Name cannot be blank</li>") {
			t.Fatalf("status %d", res.StatusCode)
		}
	})

	t.Run("create_global_query_from_gantt", func(t *testing.T) {
		res, _ := postQueries(t, admin, ts.URL, "/queries", http.MethodPost, url.Values{
			"gantt": {"1"}, "operators[status_id]": {"o"}, "values[status_id][]": {"1"},
			"query[name]": {"test_create_from_gantt"}, "query[draw_relations]": {"1"},
			"query[draw_progress_line]": {"1"}, "query[draw_selected_columns]": {"1"},
		})
		q := findByName(t, "test_create_from_gantt")
		expectRedirect(t, res, "/issues/gantt?query_id="+itoa(q.ID))
		if q.Options == nil || !strings.Contains(*q.Options, `"draw_progress_line":"1"`) || !strings.Contains(*q.Options, `"draw_selected_columns":"1"`) {
			t.Errorf("options %v", q.Options)
		}
	})

	t.Run("create_project_query_from_calendar", func(t *testing.T) {
		res, _ := postQueries(t, admin, ts.URL, "/projects/ecookbook/queries", http.MethodPost, url.Values{
			"calendar": {"1"}, "query[name]": {"test_create_from_calendar"},
		})
		q := findByName(t, "test_create_from_calendar")
		expectRedirect(t, res, "/projects/ecookbook/issues/calendar?query_id="+itoa(q.ID))
	})

	t.Run("public_forced_private_without_permission", func(t *testing.T) {
		postQueries(t, dlopper, ts.URL, "/projects/ecookbook/queries", http.MethodPost, url.Values{
			"query[name]": {"forced_private_project"}, "query[visibility]": {"2"},
		})
		if q := findByName(t, "forced_private_project"); q.Visibility != 0 {
			t.Errorf("visibility %d", q.Visibility)
		}
		// manage_public_queries があってもプロジェクト外（全体）のクエリは管理者以外非公開
		postQueries(t, jsmith, ts.URL, "/queries", http.MethodPost, url.Values{
			"query[name]": {"forced_private_global"}, "query[visibility]": {"2"},
		})
		if q := findByName(t, "forced_private_global"); q.Visibility != 0 {
			t.Errorf("visibility %d", q.Visibility)
		}
		postQueries(t, jsmith, ts.URL, "/projects/ecookbook/queries", http.MethodPost, url.Values{
			"query[name]": {"public_with_permission"}, "query[visibility]": {"2"},
		})
		if q := findByName(t, "public_with_permission"); q.Visibility != 2 {
			t.Errorf("visibility %d", q.Visibility)
		}
		postQueries(t, admin, ts.URL, "/queries", http.MethodPost, url.Values{
			"query[name]": {"global_public_by_admin"}, "query[visibility]": {"2"},
		})
		if q := findByName(t, "global_public_by_admin"); q.Visibility != 2 {
			t.Errorf("visibility %d", q.Visibility)
		}
	})

	t.Run("create_other_types_redirect", func(t *testing.T) {
		res, _ := postQueries(t, jsmith, ts.URL, "/projects/ecookbook/queries", http.MethodPost, url.Values{
			"type": {"TimeEntryQuery"}, "default_columns": {"1"}, "f[]": {"spent_on"}, "op[spent_on]": {"="},
			"v[spent_on][]": {"2016-07-14"}, "query[name]": {"te_query"}, "query[visibility]": {"2"},
		})
		q := findByName(t, "te_query")
		expectRedirect(t, res, "/projects/ecookbook/time_entries?query_id="+itoa(q.ID))
		if q.Kind != "time_entry" {
			t.Errorf("kind %s", q.Kind)
		}
		res, _ = postQueries(t, admin, ts.URL, "/queries", http.MethodPost, url.Values{
			"type": {"ProjectQuery"}, "f[]": {"status"}, "op[status]": {"="}, "v[status][]": {"1"},
			"query[name]": {"pq"}, "query[visibility]": {"2"},
		})
		expectRedirect(t, res, "/projects?query_id="+itoa(findByName(t, "pq").ID))
		res, _ = postQueries(t, admin, ts.URL, "/queries", http.MethodPost, url.Values{
			"type": {"ProjectAdminQuery"}, "query[name]": {"paq"}, "query[visibility]": {"2"},
		})
		expectRedirect(t, res, "/admin/projects?query_id="+itoa(findByName(t, "paq").ID))
	})

	t.Run("edit_access", func(t *testing.T) {
		for _, tc := range []struct {
			c      *http.Client
			path   string
			status int
		}{
			{admin, "/queries/4/edit", 200},
			{dlopper, "/queries/3/edit", 200},
			{dlopper, "/queries/2/edit", 200},
			{jsmith, "/queries/1/edit", 200},
			{jsmith, "/queries/99/edit", 404},
			{dlopper, "/queries/1/edit", 403},
		} {
			if res, _ := get(t, tc.c, ts.URL+tc.path); res.StatusCode != tc.status {
				t.Errorf("%s: status %d, want %d", tc.path, res.StatusCode, tc.status)
			}
		}
		_, body := get(t, admin, ts.URL+"/queries/4/edit")
		if !strings.Contains(body, `<input type="radio" value="2" checked="checked" name="query[visibility]" id="query_visibility_2" />`) ||
			!strings.Contains(body, `name="query_is_for_all" id="query_is_for_all" value="1" disabled="disabled" class="" checked="checked" />`) {
			t.Error("edit query 4 form")
		}
		_, body = get(t, admin, ts.URL+"/queries/5/edit")
		if !strings.Contains(body, `<option selected="selected" value="priority">`) || !strings.Contains(body, `value="Description for Open issues by priority and tracker"`) {
			t.Error("edit query 5 sort / description")
		}
	})

	t.Run("update_global_private_query", func(t *testing.T) {
		res, _ := postQueries(t, dlopper, ts.URL, "/queries/3", http.MethodPut, url.Values{
			"default_columns": {"1"}, "query[name]": {"test_edit_global_private_query"}, "query[visibility]": {"2"},
		})
		expectRedirect(t, res, "/issues?query_id=3")
		if q := findByName(t, "test_edit_global_private_query"); q.Visibility != 0 {
			t.Errorf("visibility %d", q.Visibility)
		}
	})

	t.Run("update_with_failure", func(t *testing.T) {
		res, body := postQueries(t, admin, ts.URL, "/queries/4", http.MethodPut, url.Values{"query[name]": {""}})
		if res.StatusCode != http.StatusOK || !strings.Contains(body, "Name cannot be blank") {
			t.Fatalf("status %d", res.StatusCode)
		}
	})

	t.Run("update_description", func(t *testing.T) {
		res, _ := postQueries(t, admin, ts.URL, "/queries/5", http.MethodPatch, url.Values{
			"query[name]": {"Open issues by priority and tracker"}, "query[description]": {"query description updated"},
		})
		expectRedirect(t, res, "/issues?query_id=5")
		if q := findByName(t, "Open issues by priority and tracker"); q.Description == nil || *q.Description != "query description updated" {
			t.Errorf("description %v", q.Description)
		}
	})

	t.Run("destroy", func(t *testing.T) {
		res, _ := postQueries(t, jsmith, ts.URL, "/queries/1", http.MethodDelete, url.Values{})
		expectRedirect(t, res, "/projects/ecookbook/issues?set_filter=1")
		var n int
		if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM queries WHERE id = 1`); err != nil || n != 0 {
			t.Errorf("query 1 not deleted (%d, %v)", n, err)
		}
	})
}

// TestQueriesFilter は queries#filter（フィルタの選択肢 JSON）を確認する。
func TestQueriesFilter(t *testing.T) {
	ts, _ := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	jsmith := login(t, ts, "jsmith", "jsmith")
	getJSON := func(t *testing.T, c *http.Client, path string) [][]string {
		t.Helper()
		res, body := get(t, c, ts.URL+path)
		if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
			t.Fatalf("GET %s: %d %s", path, res.StatusCode, res.Header.Get("Content-Type"))
		}
		var out [][]string
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("%v: %s", err, body)
		}
		return out
	}
	includes := func(list [][]string, want ...string) bool {
		for _, e := range list {
			if strings.Join(e, "\x00") == strings.Join(want, "\x00") {
				return true
			}
		}
		return false
	}
	if v := getJSON(t, jsmith, "/queries/filter?project_id=1&name=fixed_version_id"); !includes(v, "eCookbook - 2.0", "3", "open") {
		t.Errorf("fixed_version_id: %v", v)
	}
	if v := getJSON(t, jsmith, "/queries/filter?project_id=1&type=TimeEntryQuery&name=issue.fixed_version_id"); !includes(v, "eCookbook - 2.0", "3", "open") {
		t.Errorf("issue.fixed_version_id: %v", v)
	}
	if v := getJSON(t, jsmith, "/queries/filter?project_id=1&type=TimeEntryQuery&name=subproject_id"); len(v) != 4 || !includes(v, "Private child of eCookbook", "5") {
		t.Errorf("subproject_id: %v", v)
	}
	if v := getJSON(t, admin, "/queries/filter?project_id=1&type=IssueQuery&name=assigned_to_id"); len(v) != 6 ||
		!includes(v, "<< me >>", "me") || !includes(v, "Dave Lopper", "3", "active") || !includes(v, "Dave2 Lopper2", "5", "locked") {
		t.Errorf("assigned_to_id: %v", v)
	}
	if v := getJSON(t, admin, "/queries/filter?project_id=1&type=IssueQuery&name=watcher_id"); len(v) != 7 || !includes(v, "A Team", "10", "active") {
		t.Errorf("watcher_id: %v", v)
	}
	if v := getJSON(t, admin, "/queries/filter?type=IssueQuery&name=watcher_id"); len(v) != 8 {
		t.Errorf("watcher_id global: %v", v)
	}
	if v := getJSON(t, jsmith, "/queries/filter?project_id=1&name=unknown"); len(v) != 0 {
		t.Errorf("unknown: %v", v)
	}
	if res, _ := get(t, jsmith, ts.URL+"/queries/filter?project_id=999&name=status_id"); res.StatusCode != 404 {
		t.Errorf("unknown project: %d", res.StatusCode)
	}
}

func strconvItoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
