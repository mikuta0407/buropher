package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
)

// このファイルは Wiki（WikiController / WikisController）の画面・API を参照 Redmine（フィクスチャ投入済み、
// http://127.0.0.1:3998）の出力と比較し、書き込み系の振る舞い（版の作成・競合・セクション編集・名前変更と
// リダイレクト・削除）を確認する。testdata/wiki/*.html は参照の `compat fetch -raw` の出力から
// <div id="main"> ... <div id="footer"> を切り出し、ベース URL と atom キーを伏せたもの。

var (
	anyFormNameRe = regexp.MustCompile(`name="([a-z_]+)-[0-9a-f]{8}"`)
	anyCSRFRe     = regexp.MustCompile(`value="[A-Za-z0-9_-]{40,}"`)
	// wikiCSRFMetaRe は <meta name="csrf-token" content="..."> のトークン。
	wikiCSRFMetaRe = regexp.MustCompile(`<meta name="csrf-token" content="([A-Za-z0-9_-]+)"`)
)

// normalizeWiki は比較のための正規化（CSRF・フォーム名の乱数・アセットのダイジェスト・ベース URL）。
func normalizeWiki(s, base string) string {
	s = strings.ReplaceAll(s, base, "{{BASE}}")
	s = feedKeyRe.ReplaceAllString(s, "key=KEY")
	s = anyCSRFRe.ReplaceAllString(s, `value="{{CSRF}}"`)
	s = anyFormNameRe.ReplaceAllString(s, `name="$1-RANDOM"`)
	s = digestRe.ReplaceAllString(s, "-DIGEST.$1")
	return imageDigestRe.ReplaceAllString(s, "-DIGEST.$1")
}

func mainPart(s string) string {
	i := strings.Index(s, `<div id="main"`)
	j := strings.Index(s, `<div id="footer">`)
	if i < 0 || j < i {
		return s
	}
	return s[i:j]
}

// compareWikiGolden は got と testdata/wiki/<name> を行単位で比較する。
// 環境変数 WIKI_DUMP が設定されていれば、取得した本文をそのディレクトリに書き出す（差分調査用）。
func compareWikiGolden(t *testing.T, name, got, base string) {
	t.Helper()
	got = normalizeWiki(got, base)
	if dir := os.Getenv("WIKI_DUMP"); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, name), []byte(got), 0o644)
	}
	b, err := os.ReadFile("testdata/wiki/" + name)
	if err != nil {
		t.Fatal(err)
	}
	want := normalizeWiki(string(b), "{{BASE}}")
	if got == want {
		return
	}
	gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(gl) || i < len(wl); i++ {
		var a, w string
		if i < len(gl) {
			a = gl[i]
		}
		if i < len(wl) {
			w = wl[i]
		}
		if a != w {
			t.Fatalf("%s: line %d differs\n got: %q\nwant: %q", name, i+1, a, w)
		}
	}
}

// TestWikiPagesMatchRedmine は Wiki の各画面・API が参照 Redmine と一致することを確認する。
func TestWikiPagesMatchRedmine(t *testing.T) {
	ts, _ := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	anon := newClient(t)
	cases := []struct {
		client *http.Client
		path   string
		golden string
		whole  bool
	}{
		{admin, "/projects/ecookbook/wiki/CookBook_documentation", "show_cookbook_admin.html", false},
		{anon, "/projects/ecookbook/wiki/CookBook_documentation", "show_cookbook_anonymous.html", false},
		{admin, "/projects/ecookbook/wiki/Another_page", "show_another_admin.html", false},
		{admin, "/projects/ecookbook/wiki/CookBook_documentation/2", "show_cookbook_v2_admin.html", false},
		{admin, "/projects/ecookbook/wiki/index", "index_admin.html", false},
		{admin, "/projects/ecookbook/wiki/date_index", "date_index_admin.html", false},
		{admin, "/projects/ecookbook/wiki/Another_page/history", "history_another_admin.html", false},
		{admin, "/projects/ecookbook/wiki/CookBook_documentation/2/diff", "diff_cookbook_v2_admin.html", false},
		{admin, "/projects/ecookbook/wiki/CookBook_documentation/2/annotate", "annotate_cookbook_v2_admin.html", false},
		{admin, "/projects/ecookbook/wiki/Another_page/edit", "edit_another_admin.html", false},
		{admin, "/projects/ecookbook/wiki/Another_page/rename", "rename_another_admin.html", false},
		{admin, "/projects/ecookbook/wiki/new", "new_admin.html", false},
		{admin, "/projects/ecookbook/wiki/Unknown_page", "unknown_page_admin.html", false},
		{admin, "/projects/ecookbook/wiki/destroy", "wikis_destroy_admin.html", false},
		{admin, "/projects/ecookbook/wiki/index.json", "index_admin.json", true},
		{admin, "/projects/ecookbook/wiki/CookBook_documentation.json?include=attachments", "show_cookbook_admin.json", true},
		{admin, "/projects/ecookbook/wiki/CookBook_documentation/2.xml", "show_cookbook_v2_admin.xml", true},
	}
	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			res, body := get(t, tc.client, ts.URL+tc.path)
			if res.StatusCode != 200 {
				t.Fatalf("%s: status %d", tc.path, res.StatusCode)
			}
			if !tc.whole {
				body = mainPart(body)
			}
			compareWikiGolden(t, tc.golden, body, ts.URL)
		})
	}
}

// wikiForm は CSRF トークン付きのフォーム値（_method で PUT / DELETE を指定できる）。
func wikiForm(t *testing.T, c *http.Client, ts *httptest.Server, kv ...string) url.Values {
	t.Helper()
	_, body := get(t, c, ts.URL+"/projects/ecookbook/wiki/index")
	m := wikiCSRFMetaRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("csrf-token not found")
	}
	v := url.Values{"authenticity_token": {m[1]}}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Add(kv[i], kv[i+1])
	}
	return v
}

func wikiPageVersion(t *testing.T, d *db.DB, title string) (version int, count int) {
	t.Helper()
	ctx := context.Background()
	if err := d.Get(ctx, &version, `SELECT current_version FROM wiki_pages WHERE wiki_id = 1 AND title = ?`, title); err != nil {
		t.Fatalf("page %s: %v", title, err)
	}
	if err := d.Get(ctx, &count, `SELECT COUNT(*) FROM wiki_page_versions v JOIN wiki_pages p ON p.id = v.page_id WHERE p.wiki_id = 1 AND p.title = ?`, title); err != nil {
		t.Fatal(err)
	}
	return
}

// TestWikiEditCreatesVersion は更新で新しい版が作られ、同じ版のままの再送信が競合になることを確認する。
func TestWikiEditCreatesVersion(t *testing.T) {
	ts, d := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")
	res, _ := post(t, c, ts.URL+"/projects/ecookbook/wiki/Another_page", wikiForm(t, c, ts,
		"_method", "put", "content[text]", "# Another page\n\nnew text", "content[comments]", "my comment", "content[version]", "1"))
	if res.StatusCode != 302 || res.Header.Get("Location") != ts.URL+"/projects/ecookbook/wiki/Another_page" {
		t.Fatalf("update: status %d location %q", res.StatusCode, res.Header.Get("Location"))
	}
	v, n := wikiPageVersion(t, d, "Another_page")
	if v != 2 || n != 2 {
		t.Fatalf("version=%d count=%d, want 2/2", v, n)
	}
	var text, comments string
	var author int64
	if err := d.QueryRow(context.Background(), `SELECT text, comments, author_id FROM wiki_page_versions WHERE page_id = 2 AND version = 2`).Scan(&text, &comments, &author); err != nil {
		t.Fatal(err)
	}
	if text != "# Another page\n\nnew text" || comments != "my comment" || author != 1 {
		t.Errorf("version 2 = %q %q %d", text, comments, author)
	}
	// 古い版番号での更新は競合（ActiveRecord::StaleObjectError → notice_locking_conflict）
	res, body := post(t, c, ts.URL+"/projects/ecookbook/wiki/Another_page", wikiForm(t, c, ts,
		"_method", "put", "content[text]", "conflict", "content[version]", "1"))
	if res.StatusCode != 200 || !strings.Contains(body, "Data has been updated by another user.") {
		t.Fatalf("conflict: status %d", res.StatusCode)
	}
	if !strings.Contains(body, ">\nconflict</textarea>") {
		t.Error("conflict page should keep the submitted text")
	}
	if v, _ := wikiPageVersion(t, d, "Another_page"); v != 2 {
		t.Errorf("version after conflict = %d", v)
	}
	// 本文が同じなら版は増えない
	res, _ = post(t, c, ts.URL+"/projects/ecookbook/wiki/Another_page", wikiForm(t, c, ts,
		"_method", "put", "content[text]", "# Another page\n\nnew text", "content[version]", "2"))
	if res.StatusCode != 302 {
		t.Fatalf("same text: status %d", res.StatusCode)
	}
	if v, n := wikiPageVersion(t, d, "Another_page"); v != 2 || n != 2 {
		t.Errorf("same text: version=%d count=%d", v, n)
	}
	// 空の本文は検証エラー
	_, body = post(t, c, ts.URL+"/projects/ecookbook/wiki/Another_page", wikiForm(t, c, ts,
		"_method", "put", "content[text]", "", "content[version]", "2"))
	if !strings.Contains(body, "Text field cannot be blank") {
		t.Error("blank text should be rejected")
	}
}

// TestWikiCreatePageAndSectionEdit は新規ページの作成とセクション編集（ハッシュの競合検出を含む）を確認する。
func TestWikiCreatePageAndSectionEdit(t *testing.T) {
	ts, d := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")
	res, _ := post(t, c, ts.URL+"/projects/ecookbook/wiki/New_page", wikiForm(t, c, ts,
		"_method", "put", "content[text]", "# New page\n\nintro\n\n## Sub\n\nsub text", "wiki_page[parent_id]", "1"))
	if res.StatusCode != 302 {
		t.Fatalf("create: status %d", res.StatusCode)
	}
	if v, n := wikiPageVersion(t, d, "New_page"); v != 1 || n != 1 {
		t.Fatalf("new page version=%d count=%d", v, n)
	}
	var parent int64
	if err := d.Get(context.Background(), &parent, `SELECT parent_id FROM wiki_pages WHERE title = 'New_page'`); err != nil || parent != 1 {
		t.Errorf("parent = %d, %v", parent, err)
	}
	// 編集フォームのセクション 2 とハッシュ
	_, body := get(t, c, ts.URL+"/projects/ecookbook/wiki/New_page/edit?section=2")
	m := regexp.MustCompile(`name="section_hash" id="section_hash" value="([0-9a-f]+)"`).FindStringSubmatch(body)
	if m == nil || !strings.Contains(body, ">\n## Sub\n\nsub text</textarea>") {
		t.Fatalf("section form: %s", extract(body, "<textarea", "</textarea>"))
	}
	res, _ = post(t, c, ts.URL+"/projects/ecookbook/wiki/New_page", wikiForm(t, c, ts,
		"_method", "put", "section", "2", "section_hash", m[1], "content[text]", "## Sub\n\nchanged", "content[version]", "1"))
	if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/wiki/New_page#section-2") {
		t.Fatalf("section update: status %d location %q", res.StatusCode, res.Header.Get("Location"))
	}
	var text string
	if err := d.Get(context.Background(), &text, `SELECT v.text FROM wiki_page_versions v JOIN wiki_pages p ON p.id = v.page_id AND v.version = p.current_version WHERE p.title = 'New_page'`); err != nil {
		t.Fatal(err)
	}
	if text != "# New page\n\nintro\n\n## Sub\n\nchanged" {
		t.Errorf("text after section update = %q", text)
	}
	// 古いハッシュは競合
	res, body = post(t, c, ts.URL+"/projects/ecookbook/wiki/New_page", wikiForm(t, c, ts,
		"_method", "put", "section", "2", "section_hash", m[1], "content[text]", "## Sub\n\nagain", "content[version]", "2"))
	if res.StatusCode != 200 || !strings.Contains(body, "Data has been updated by another user.") {
		t.Errorf("stale section: status %d", res.StatusCode)
	}
}

// TestWikiRenameCreatesRedirect は名前変更でリダイレクトが作られ、旧タイトルが新しいページへ転送されることを確認する。
func TestWikiRenameCreatesRedirect(t *testing.T) {
	ts, d := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")
	res, _ := post(t, c, ts.URL+"/projects/ecookbook/wiki/Child_2/rename", wikiForm(t, c, ts,
		"wiki_page[title]", "Renamed child", "wiki_page[redirect_existing_links]", "1", "wiki_page[parent_id]", "1"))
	if res.StatusCode != 302 || res.Header.Get("Location") != ts.URL+"/projects/ecookbook/wiki/Renamed_child" {
		t.Fatalf("rename: status %d location %q", res.StatusCode, res.Header.Get("Location"))
	}
	var n int
	if err := d.Get(context.Background(), &n, `SELECT COUNT(*) FROM wiki_redirects WHERE wiki_id = 1 AND title = 'Child_2' AND redirects_to = 'Renamed_child' AND redirects_to_wiki_id = 1`); err != nil || n != 1 {
		t.Fatalf("redirect rows = %d, %v", n, err)
	}
	res, _ = get(t, c, ts.URL+"/projects/ecookbook/wiki/Child_2")
	if res.StatusCode != 302 || res.Header.Get("Location") != ts.URL+"/projects/ecookbook/wiki/Renamed_child" {
		t.Errorf("old title: status %d location %q", res.StatusCode, res.Header.Get("Location"))
	}
	res, _ = get(t, c, ts.URL+"/projects/ecookbook/wiki/Child_2/history")
	if res.StatusCode != 302 || res.Header.Get("Location") != ts.URL+"/projects/ecookbook/wiki/Renamed_child/history" {
		t.Errorf("old title history: status %d location %q", res.StatusCode, res.Header.Get("Location"))
	}
	// 戻すとリダイレクトは消える（自分自身を指すものは削除）
	res, _ = post(t, c, ts.URL+"/projects/ecookbook/wiki/Renamed_child/rename", wikiForm(t, c, ts,
		"wiki_page[title]", "Child_2", "wiki_page[redirect_existing_links]", "1"))
	if res.StatusCode != 302 {
		t.Fatalf("rename back: status %d", res.StatusCode)
	}
	if err := d.Get(context.Background(), &n, `SELECT COUNT(*) FROM wiki_redirects WHERE wiki_id = 1 AND title = 'Child_2'`); err != nil || n != 0 {
		t.Errorf("redirects to self should be removed: %d", n)
	}
	// 重複するタイトルは検証エラー
	_, body := post(t, c, ts.URL+"/projects/ecookbook/wiki/Child_1/rename", wikiForm(t, c, ts, "wiki_page[title]", "Another page"))
	if !strings.Contains(body, "Title has already been taken") {
		t.Error("duplicate title should be rejected")
	}
}

// TestWikiDestroy は子ページの付け替え・版の削除・Wiki の削除を確認する。
func TestWikiDestroy(t *testing.T) {
	ts, d := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")
	ctx := context.Background()
	// 子ページがあれば確認フォーム
	_, body := post(t, c, ts.URL+"/projects/ecookbook/wiki/Another_page", wikiForm(t, c, ts, "_method", "delete"))
	if !strings.Contains(body, `name="todo" id="todo_reassign" value="reassign"`) {
		t.Fatalf("destroy form expected: %s", extract(body, `<div class="box">`, `</div>`))
	}
	res, _ := post(t, c, ts.URL+"/projects/ecookbook/wiki/Another_page", wikiForm(t, c, ts, "_method", "delete", "todo", "reassign", "reassign_to_id", "11"))
	if res.StatusCode != 302 || res.Header.Get("Location") != ts.URL+"/projects/ecookbook/wiki/index" {
		t.Fatalf("destroy: status %d location %q", res.StatusCode, res.Header.Get("Location"))
	}
	var n int
	if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM wiki_pages WHERE parent_id = 11`); err != nil || n != 2 {
		t.Errorf("reassigned children = %d, %v", n, err)
	}
	if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM wiki_pages WHERE id = 2`); err != nil || n != 0 {
		t.Errorf("page should be deleted")
	}
	// 版の削除（最新版を消すと前の版が最新になる）
	res, _ = post(t, c, ts.URL+"/projects/ecookbook/wiki/CookBook_documentation/3", wikiForm(t, c, ts, "_method", "delete"))
	if res.StatusCode != 302 {
		t.Fatalf("destroy_version: status %d", res.StatusCode)
	}
	if v, n := wikiPageVersion(t, d, "CookBook_documentation"); v != 2 || n != 2 {
		t.Errorf("after destroy_version: version=%d count=%d", v, n)
	}
	// Wiki の削除（既定の Wiki が作り直される）
	res, _ = post(t, c, ts.URL+"/projects/onlinestore/wiki/destroy", wikiForm(t, c, ts, "confirm", "1"))
	if res.StatusCode != 302 || res.Header.Get("Location") != ts.URL+"/projects/onlinestore" {
		t.Fatalf("wikis#destroy: status %d location %q", res.StatusCode, res.Header.Get("Location"))
	}
	var start string
	if err := d.Get(ctx, &start, `SELECT start_page FROM wikis WHERE project_id = 2`); err != nil || start != "Wiki" {
		t.Errorf("default wiki: %q %v", start, err)
	}
	if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM wiki_pages p JOIN wikis w ON w.id = p.wiki_id WHERE w.project_id = 2`); err != nil || n != 0 {
		t.Errorf("pages remain: %d", n)
	}
}

// TestWikiAPI は API の作成（201）・更新（204）・競合（409）・削除（204）を確認する。
func TestWikiAPI(t *testing.T) {
	ts, _ := newFixtureServer(t)
	do := func(method, path, body string) (*http.Response, string) {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.SetBasicAuth("admin", "admin")
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := readUnbranded(res.Body)
		return res, string(b)
	}
	res, body := do("PUT", "/projects/ecookbook/wiki/Api_page.json", `{"wiki_page":{"text":"API text","comments":"via api"}}`)
	if res.StatusCode != 201 || res.Header.Get("Location") != "/projects/ecookbook/wiki/Api_page" {
		t.Fatalf("create: %d %q %s", res.StatusCode, res.Header.Get("Location"), body)
	}
	if !strings.Contains(body, `"text":"API text","version":1,"author":{"id":1,"name":"Redmine Admin"},"comments":"via api"`) {
		t.Errorf("create body: %s", body)
	}
	if res, _ := do("PUT", "/projects/ecookbook/wiki/Api_page.json", `{"wiki_page":{"text":"v2","version":1}}`); res.StatusCode != 204 {
		t.Errorf("update: %d", res.StatusCode)
	}
	if res, _ := do("PUT", "/projects/ecookbook/wiki/Api_page.json", `{"wiki_page":{"text":"v3","version":1}}`); res.StatusCode != 409 {
		t.Errorf("conflict: %d", res.StatusCode)
	}
	if res, body := do("PUT", "/projects/ecookbook/wiki/Api_page.json", `{"wiki_page":{"text":""}}`); res.StatusCode != 422 || body != `{"errors":["Text field cannot be blank"]}` {
		t.Errorf("blank: %d %s", res.StatusCode, body)
	}
	if res, _ := do("DELETE", "/projects/ecookbook/wiki/Api_page.json", ""); res.StatusCode != 204 {
		t.Errorf("delete: %d", res.StatusCode)
	}
	if res, _ := do("GET", "/projects/ecookbook/wiki/Api_page.json", ""); res.StatusCode != 404 {
		t.Errorf("deleted page: %d", res.StatusCode)
	}
}
