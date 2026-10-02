package server_test

import (
	"context"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/web"
)

// getWithLang は Accept-Language 付きの GET。
func getWithLang(t *testing.T, c *http.Client, u, lang string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		t.Fatal(err)
	}
	if lang != "" {
		req.Header.Set("Accept-Language", lang)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

// TestHelpPages は help#show_wiki_syntax / show_code_highlighting が参照 Redmine（text_formatting = common_mark）と
// 一致することを確認する（testdata/help は参照の出力のアセットダイジェストを伏せたもの）。
func TestHelpPages(t *testing.T) {
	ts, _ := newFixtureServer(t)
	c := newClient(t)
	cases := []struct{ path, lang, golden string }{
		{"/help/wiki_syntax", "", "wiki_syntax_common_mark_en.html"},
		{"/help/wiki_syntax/detailed", "", "wiki_syntax_detailed_common_mark_en.html"},
		{"/help/wiki_syntax", "ja", "wiki_syntax_common_mark_ja.html"},
		{"/help/wiki_syntax/detailed", "ja", "wiki_syntax_detailed_common_mark_ja.html"},
		// fr の CommonMark 版は無いので en
		{"/help/wiki_syntax", "fr", "wiki_syntax_common_mark_en.html"},
		{"/help/code_highlighting", "", "code_highlighting.html"},
	}
	for _, tc := range cases {
		res, body := getWithLang(t, c, ts.URL+tc.path, tc.lang)
		if res.StatusCode != 200 {
			t.Errorf("%s (%s): status %d", tc.path, tc.lang, res.StatusCode)
			continue
		}
		if ct := res.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("%s: content-type %q", tc.path, ct)
		}
		compareGolden(t, "help/"+tc.golden, body)
	}
	// 未対応の type はルートに一致しない、html 以外の形式は本文なしの 404
	for path, ct := range map[string]string{
		"/help/wiki_syntax/foo":          "text/html; charset=utf-8",
		"/help/wiki_syntax.json":         "application/json",
		"/help/code_highlighting.json":   "application/json",
		"/help/wiki_syntax/detailed.xml": "application/xml",
	} {
		res, body := get(t, c, ts.URL+path)
		if res.StatusCode != 404 || res.Header.Get("Content-Type") != ct {
			t.Errorf("%s: status %d content-type %q", path, res.StatusCode, res.Header.Get("Content-Type"))
		}
		if ct != "text/html; charset=utf-8" && body != "" {
			t.Errorf("%s: body %q", path, body)
		}
	}
}

// TestHelpPagesAllTemplates は全書式・全言語のテンプレートが ERB を残さずに描画できることを確認する
// （参照環境は common_mark なので textile は内容の比較はしない）。
func TestHelpPagesAllTemplates(t *testing.T) {
	srv, ts, _ := newFixtureServerFull(t)
	ctx := context.Background()
	st := srv.App().Settings
	c := newClient(t)
	for _, tf := range []string{"textile", "common_mark"} {
		if err := st.Set(ctx, "text_formatting", tf); err != nil {
			t.Fatal(err)
		}
		dirs, err := fs.ReadDir(web.Templates(), "help/wiki_syntax/"+tf)
		if err != nil {
			t.Fatal(err)
		}
		for _, dir := range dirs {
			lang := dir.Name()
			// Redmine のロケール名（pt-BR 等）はディレクトリ名（pt-br）と大文字小文字が異なり en になるので小文字のものだけ
			for _, p := range []string{"/help/wiki_syntax", "/help/wiki_syntax/detailed"} {
				res, body := getWithLang(t, c, ts.URL+p, lang)
				if res.StatusCode != 200 || strings.Contains(body, "<%") || !strings.Contains(body, "/assets/") {
					t.Errorf("%s %s (%s): status %d, unrendered ERB or missing assets", tf, p, lang, res.StatusCode)
				}
			}
		}
	}
	// textile の en は textile のテンプレート
	if err := st.Set(ctx, "text_formatting", "textile"); err != nil {
		t.Fatal(err)
	}
	if _, body := get(t, c, ts.URL+"/help/wiki_syntax"); !strings.Contains(body, "Wiki Syntax Quick Reference") || strings.Contains(body, "CommonMark") {
		t.Errorf("textile help: %.300s", body)
	}
}

// TestRobots は welcome#robots が参照 Redmine と一致すること、login_required では全体を Disallow にすることを確認する。
func TestRobots(t *testing.T) {
	srv, ts, _ := newFixtureServerFull(t)
	for _, c := range []*http.Client{newClient(t), login(t, ts, "admin", "admin")} {
		res, body := get(t, c, ts.URL+"/robots.txt")
		if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Fatalf("status %d content-type %q", res.StatusCode, res.Header.Get("Content-Type"))
		}
		compareGolden(t, "help/robots.txt", body)
	}
	if res, _ := get(t, newClient(t), ts.URL+"/robots.json"); res.StatusCode != 404 {
		t.Errorf("robots.json: status %d", res.StatusCode)
	}
	if err := srv.App().Settings.Set(context.Background(), "login_required", "1"); err != nil {
		t.Fatal(err)
	}
	res, body := get(t, newClient(t), ts.URL+"/robots.txt")
	if res.StatusCode != 200 || body != "User-agent: *\nDisallow: /\n" {
		t.Errorf("login_required robots: %d %q", res.StatusCode, body)
	}
}

// TestRailsHealthCheck は /up（rails/health#show）。
func TestRailsHealthCheck(t *testing.T) {
	ts, _ := newFixtureServer(t)
	for _, p := range []string{"/up", "/up.json"} {
		res, body := get(t, newClient(t), ts.URL+p)
		if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/html; charset=utf-8" ||
			body != `<!DOCTYPE html><html><body style="background-color: green"></body></html>` {
			t.Errorf("%s: %d %q %q", p, res.StatusCode, res.Header.Get("Content-Type"), body)
		}
	}
}

// TestAutoCompleteWikiPages は auto_completes#wiki_pages（test/functional/auto_completes_controller_test.rb の
// test_wiki_pages_* と参照 Redmine の出力）。
func TestAutoCompleteWikiPages(t *testing.T) {
	ts, d := newFixtureServer(t)
	c := newClient(t)
	check := func(path, golden string) {
		t.Helper()
		res, body := get(t, c, ts.URL+path)
		if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/json; charset=utf-8" {
			t.Fatalf("%s: status %d content-type %q", path, res.StatusCode, res.Header.Get("Content-Type"))
		}
		compareGolden(t, "help/"+golden, body)
	}
	// test_wiki_pages_should_not_be_case_sensitive / test_wiki_pages_should_return_json
	check("/wiki_pages/auto_complete?project_id=ecookbook&q=pAgE", "wiki_pages_page.json")
	// q なしは新しい順に 10 件まで
	check("/wiki_pages/auto_complete?project_id=ecookbook", "wiki_pages_all.json")
	// test_wiki_pages_without_project_id_params_should_not_return_pages、Wiki モジュールが無いプロジェクト
	for _, p := range []string{"/wiki_pages/auto_complete?project_id=", "/wiki_pages/auto_complete", "/wiki_pages/auto_complete?project_id=onlinestore"} {
		if res, body := get(t, c, ts.URL+p); res.StatusCode != 200 || body != "[]" {
			t.Errorf("%s: %d %q", p, res.StatusCode, body)
		}
	}
	if res, _ := get(t, c, ts.URL+"/wiki_pages/auto_complete?project_id=nonexist"); res.StatusCode != 404 {
		t.Errorf("nonexist project: status %d", res.StatusCode)
	}
	// test_wiki_pages_without_view_wiki_pages_permission_should_not_return_pages（匿名ロール = builtin 2）
	ctx := context.Background()
	if _, err := d.Exec(ctx, `DELETE FROM role_permissions WHERE permission = 'view_wiki_pages' AND role_id = (SELECT id FROM roles WHERE builtin = 2)`); err != nil {
		t.Fatal(err)
	}
	if res, body := get(t, c, ts.URL+"/wiki_pages/auto_complete?project_id=ecookbook&q=Page_with_an_inline_image"); res.StatusCode != 200 || body != "[]" {
		t.Errorf("without view_wiki_pages: %d %q", res.StatusCode, body)
	}
	// test_wiki_pages_without_q_params_should_return_last_10_pages
	for i := range 3 {
		if _, err := d.Exec(ctx, `INSERT INTO wiki_pages (wiki_id, title, created_at) VALUES (1, ?, '2026-01-15T12:00:00.000000Z')`,
			"test"+string(rune('0'+i))); err != nil {
			t.Fatal(err)
		}
	}
	jc := login(t, ts, "admin", "admin")
	res, body := get(t, jc, ts.URL+"/wiki_pages/auto_complete?project_id=ecookbook")
	if res.StatusCode != 200 || strings.Count(body, `"id":`) != 10 || !strings.HasPrefix(body, `[{"id":`) || !strings.Contains(body, `"value":"test2"},{"id"`) {
		t.Errorf("last 10 pages: %q", body)
	}
}
