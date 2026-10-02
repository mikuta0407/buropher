package server_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
)

// チケットの関連（IssueRelationsController）・ウォッチャー（WatchersController）・自動補完
// （AutoCompletesController#issues）のテスト。
//
// testdata/relwatch/*.js / *.html / *.json は参照 Redmine（http://127.0.0.1:3998）に XHR（X-Requested-With と
// Accept: text/javascript, ...）で GET した応答本文そのもの（/tmp の取得スクリプトで保存。CSRF トークンと form 名の
// 乱数・アセットのダイジェストは比較時に伏せる）。状態を変える操作（POST / DELETE）の応答は参照に送れないため、
// Redmine のソース（issue_relations/*.js.erb, watchers/*.js.erb）から期待値を組み立てて確認する。

var (
	jsTokenRe    = regexp.MustCompile(`authenticity_token\\" value=\\"[A-Za-z0-9_-]+\\"`)
	jsFormNameRe = regexp.MustCompile(`form-[0-9a-f]{8}\\"`)
)

func normalizeRelWatch(s string) string {
	s = jsTokenRe.ReplaceAllString(s, `authenticity_token\" value=\"TOKEN\"`)
	s = jsFormNameRe.ReplaceAllString(s, `form-RANDOM\"`)
	s = csrfInputRe.ReplaceAllString(s, `name="authenticity_token" value="TOKEN"`)
	s = formNameRe.ReplaceAllString(s, `form-RANDOM"`)
	return digestRe.ReplaceAllString(s, "-DIGEST.$1")
}

// xhr は XHR（jQuery の $.ajax と同じヘッダ）でリクエストを送る。
func xhr(t *testing.T, c *http.Client, method, u string, form url.Values) (*http.Response, string) {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, u, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Accept", "text/javascript, application/javascript, application/ecmascript, application/x-ecmascript, */*; q=0.01")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res, string(b)
}

func compareRelWatch(t *testing.T, name, got string) {
	t.Helper()
	want, err := os.ReadFile("testdata/relwatch/" + name)
	if err != nil {
		t.Fatal(err)
	}
	w, g := normalizeRelWatch(string(want)), normalizeRelWatch(got)
	if w != g {
		t.Errorf("%s mismatch\n--- want\n%s\n--- got\n%s", name, w, g)
	}
}

func countRows(t *testing.T, d *db.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := d.Get(t.Context(), &n, query, args...); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestWatchersReadMatchesRedmine はウォッチャー追加モーダル・候補一覧・メンション候補が参照と一致することを確認する。
func TestWatchersReadMatchesRedmine(t *testing.T) {
	ts, _ := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	jsmith := login(t, ts, "jsmith", "jsmith")
	cases := []struct {
		c    *http.Client
		file string
		path string
		ct   string
	}{
		{admin, "new_issue1.js", "/watchers/new.js?object_type=issue&object_id=1", "text/javascript"},
		{admin, "new_issue2.js", "/watchers/new.js?object_type=issue&object_id=2", "text/javascript"},
		{admin, "new_bulk.js", "/watchers/new.js?object_type=issue&object_id[]=1&object_id[]=6", "text/javascript"},
		{admin, "new_project.js", "/watchers/new.js?project_id=ecookbook", "text/javascript"},
		{admin, "new_wiki_page.js", "/watchers/new.js?object_type=wiki_page&object_id=1", "text/javascript"},
		{admin, "acu_project5.html", "/watchers/autocomplete_for_user?project_id=5", "text/html"},
		{admin, "acu_q.html", "/watchers/autocomplete_for_user?object_id%5B%5D=1&object_type=issue&project_id=ecookbook&q=jo", "text/html"},
		{jsmith, "new_issue1_jsmith.js", "/watchers/new.js?object_type=issue&object_id=1", "text/javascript"},
		{jsmith, "acm_issue1.json", "/watchers/autocomplete_for_mention?object_id=1&object_type=issue&q=", "application/json"},
		{jsmith, "acu_q_all.html", "/watchers/autocomplete_for_user?project_id=ecookbook&q=e", "text/html"},
	}
	for _, tc := range cases {
		res, body := xhr(t, tc.c, http.MethodGet, ts.URL+tc.path, nil)
		if res.StatusCode != 200 {
			t.Errorf("%s: status %d", tc.path, res.StatusCode)
			continue
		}
		if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, tc.ct) {
			t.Errorf("%s: content-type %q", tc.path, ct)
		}
		compareRelWatch(t, tc.file, body)
	}
	// 権限・形式
	if res, _ := xhr(t, admin, http.MethodGet, ts.URL+"/watchers/new.js", nil); res.StatusCode != 403 {
		t.Errorf("new.js without object: %d", res.StatusCode)
	}
	if res, _ := get(t, admin, ts.URL+"/watchers/new?object_type=issue&object_id=1"); res.StatusCode != 404 {
		t.Errorf("new html: %d", res.StatusCode)
	}
	if res, _ := xhr(t, admin, http.MethodGet, ts.URL+"/watchers/new.js?object_type=issue&object_id=999", nil); res.StatusCode != 403 {
		t.Errorf("new.js missing issue: %d", res.StatusCode)
	}
	if res, _ := xhr(t, admin, http.MethodGet, ts.URL+"/watchers/new.js?object_type=foo&object_id=1", nil); res.StatusCode != 500 {
		t.Errorf("new.js unknown type: %d", res.StatusCode)
	}
}

// TestAutoCompleteIssuesMatchesRedmine は /issues/auto_complete の JSON が参照と一致することを確認する。
func TestAutoCompleteIssuesMatchesRedmine(t *testing.T) {
	ts, _ := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	jsmith := login(t, ts, "jsmith", "jsmith")
	cases := []struct {
		c    *http.Client
		file string
		path string
	}{
		{admin, "ac_all.json", "/issues/auto_complete?q="},
		{admin, "ac_13.json", "/issues/auto_complete?q=%2313"},
		{admin, "ac_issue_ecookbook.json", "/issues/auto_complete?q=issue&project_id=ecookbook"},
		{admin, "ac_tree.json", "/issues/auto_complete?project_id=subproject1&scope=tree"},
		{admin, "ac_hier.json", "/issues/auto_complete?project_id=subproject1&scope=hierarchy"},
		{admin, "ac_desc.json", "/issues/auto_complete?project_id=ecookbook&scope=descendants&status=o"},
		{admin, "ac_closed.json", "/issues/auto_complete?project_id=ecookbook&status=c&issue_id=8"},
		{jsmith, "ac_jsmith_term.json", "/issues/auto_complete?term=recipe"},
		{jsmith, "ac_jsmith_1.json", "/issues/auto_complete?q=1&project_id=1"},
		{admin, "ac_two_words.json", "/issues/auto_complete?q=closed+issue&scope=all&project_id=ecookbook"},
	}
	for _, tc := range cases {
		res, body := xhr(t, tc.c, http.MethodGet, ts.URL+tc.path, nil)
		if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
			t.Errorf("%s: %d %s", tc.path, res.StatusCode, res.Header.Get("Content-Type"))
		}
		compareRelWatch(t, tc.file, body)
	}
	if res, _ := get(t, admin, ts.URL+"/issues/auto_complete?project_id=nope"); res.StatusCode != 404 {
		t.Errorf("unknown project: %d", res.StatusCode)
	}
	// 匿名（公開プロジェクトの見えるチケットのみ）
	_, body := get(t, newClient(t), ts.URL+"/issues/auto_complete?q=13")
	if body != `[{"id":13,"label":"Bug #13: Subproject issue two","value":13}]` {
		t.Errorf("anonymous: %s", body)
	}
}

// relwatchServer はフィクスチャのサーバと CSRF 付きの送信を用意する。
func relwatchSubmit(t *testing.T, c *http.Client, ts *httptest.Server, method, path string, form url.Values, isXHR bool) (*http.Response, string) {
	t.Helper()
	return projSubmit(t, c, ts, method, path, form, isXHR)
}
