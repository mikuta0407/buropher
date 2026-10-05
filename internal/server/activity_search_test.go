// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// このファイルは活動（ActivitiesController・Atom・ユーザー詳細の活動欄）と検索（SearchController・API）の
// 参照 Redmine との比較テスト。
//
// testdata/activity_search/* は参照 Redmine 7.0.1（http://127.0.0.1:3998）の出力を正規化し、
// HTML は <title>・ページ固有の head・#main（サイドバーと本文）だけを抜き出したもの
// （共有の参照環境では閲覧で変わるプロジェクトジャンプボックスを比較から外すため）。
// 取り直すときは BUROPHER_ACTIVITY_GOLDEN_REF=http://127.0.0.1:3998 go test -run TestActivitySearchMatchRedmine ./internal/server
// （参照側は GET のみ。submit 付きのリクエストは個人設定を保存するので含めない）。
//
// 意図的に除いたもの: 2007-03-07 前後の wiki_edits（wiki_page_versions は wiki_contents と
// wiki_content_versions を統合しているため、フィクスチャで版の行が無いページの版 1 も活動に出る。docs/schema.md）。

type asCase struct {
	user, path, golden string
}

var activitySearchCases = []asCase{
	// 活動（全体）
	{"anonymous", "/activity", "activity_anonymous.html"},
	{"admin", "/activity", "activity_admin.html"},
	{"jsmith", "/activity", "activity_jsmith.html"},
	{"dlopper", "/activity", "activity_dlopper.html"},
	{"admin", "/activity?from=2007-04-15&show_changesets=1&show_news=1&show_documents=1&show_files=1&show_wiki_edits=1&show_messages=1&show_time_entries=1&show_issues=1", "activity_all_types_2007_04_15_admin.html"},
	{"admin", "/activity?from=2007-05-15&show_messages=1", "activity_messages_admin.html"},
	{"jsmith", "/activity?from=2007-04-25&show_time_entries=1", "activity_time_entries_jsmith.html"},
	{"admin", "/activity?from=2006-07-25&show_news=1&show_files=1&show_documents=1&show_issues=1", "activity_2006_07_25_admin.html"},
	{"admin", "/activity?user_id=2", "activity_user_2_admin.html"},
	{"admin", "/activity?user_id=3&from=2007-04-15", "activity_user_3_admin.html"},
	// 活動（プロジェクト）
	{"jsmith", "/projects/ecookbook/activity", "project_activity_jsmith.html"},
	{"jsmith", "/projects/ecookbook/activity?with_subprojects=0", "project_activity_nosub_jsmith.html"},
	{"anonymous", "/projects/ecookbook/activity", "project_activity_anonymous.html"},
	{"admin", "/projects/onlinestore/activity", "project_activity_onlinestore_admin.html"},
	{"jsmith", "/projects/ecookbook/activity?from=2007-03-06&show_documents=1&show_files=1", "project_activity_documents_jsmith.html"},
	// Atom
	{"admin", "/activity.atom?show_issues=1", "activity_issues_admin.atom"},
	{"anonymous", "/activity.atom", "activity_anonymous.atom"},
	{"admin", "/projects/ecookbook/activity.atom?show_changesets=1&show_documents=1&show_files=1&show_news=1", "project_activity_admin.atom"},
	{"admin", "/activity.atom?user_id=2", "activity_user_2_admin.atom"},
	// ユーザー詳細の活動欄
	{"admin", "/users/2", "users_2_admin.html"},
	{"anonymous", "/users/2", "users_2_anonymous.html"},
	{"jsmith", "/users/3", "users_3_jsmith.html"},
	// 検索
	{"admin", "/search", "search_empty_admin.html"},
	{"admin", "/search?q=recipe", "search_recipe_admin.html"},
	{"anonymous", "/search?q=recipe", "search_recipe_anonymous.html"},
	{"jsmith", "/search?q=issue", "search_issue_jsmith.html"},
	{"admin", "/search?q=e", "search_short_admin.html"},
	{"admin", "/search?q=cookbook&titles_only=1", "search_titles_only_admin.html"},
	{"admin", "/search?q=issue&all_words=&open_issues=1", "search_open_issues_admin.html"},
	{"admin", "/search?q=error&attachments=1", "search_attachments_admin.html"},
	{"admin", "/search?q=error&attachments=only", "search_attachments_only_admin.html"},
	{"jsmith", "/search?q=issue&scope=my_projects", "search_my_projects_jsmith.html"},
	{"admin", "/search?q=issue&issues=1", "search_issues_only_admin.html"},
	{"admin", "/search?q=%22first+post%22", "search_phrase_admin.html"},
	{"admin", "/search?q=issue&page=2", "search_page_2_admin.html"},
	{"admin", "/search?q=commit", "search_commit_admin.html"},
	{"admin", "/search?q=help&messages=1&wiki_pages=1", "search_messages_wiki_admin.html"},
	{"dlopper", "/search?q=private", "search_private_dlopper.html"},
	{"admin", "/search?q=cookbook+recipes&all_words=", "search_any_word_admin.html"},
	{"jsmith", "/projects/ecookbook/search?q=issue", "project_search_jsmith.html"},
	{"jsmith", "/projects/ecookbook/search?q=issue&scope=subprojects", "project_search_subprojects_jsmith.html"},
	{"anonymous", "/projects/ecookbook/search?q=cookbook", "project_search_anonymous.html"},
	// 検索 API（HTTP Basic）
	{"admin", "/search.json?q=recipe", "search_recipe_admin.json"},
	{"admin", "/search.xml?q=recipe", "search_recipe_admin.xml"},
	{"jsmith", "/search.json?q=issue&offset=2&limit=3", "search_issue_offset_jsmith.json"},
	{"admin", "/projects/ecookbook/search.json?q=issue&scope=subprojects", "project_search_subprojects_admin.json"},
}

var asPasswords = map[string]string{"admin": "admin", "jsmith": "jsmith", "dlopper": "foo"}

// asFetch は base（テストサーバまたは参照 Redmine）から user で path を取得する。
func asFetch(t *testing.T, base string, clients map[string]*http.Client, user, path string) (int, string) {
	t.Helper()
	isAPI := strings.Contains(path, ".json") || strings.Contains(path, ".xml")
	var req *http.Request
	var err error
	if req, err = http.NewRequest("GET", base+path, nil); err != nil {
		t.Fatal(err)
	}
	c := clients[user]
	if c == nil {
		c = newClient(t)
		if user != "anonymous" && !isAPI {
			_, body := get(t, c, base+"/login")
			res, _ := post(t, c, base+"/login", url.Values{
				"authenticity_token": {csrfToken(t, body)}, "username": {user}, "password": {asPasswords[user]},
			})
			if res.StatusCode != 302 {
				t.Fatalf("login %s: %d", user, res.StatusCode)
			}
		}
		if !isAPI {
			clients[user] = c
		}
	}
	if isAPI && user != "anonymous" {
		req.SetBasicAuth(user, asPasswords[user])
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := readUnbranded(res.Body)
	return res.StatusCode, string(b)
}

var (
	asTitleRe = regexp.MustCompile(`(?s)<title>.*?</title>`)
	asHeadRe  = regexp.MustCompile(`(?s)<!-- page specific tags -->.*?</head>`)
	asMainRe  = regexp.MustCompile(`(?s)<div id="main".*?<div id="footer">`)
)

// asNormalize は正規化し、HTML なら比較する部分だけを抜き出す。
func asNormalize(body, base string) string {
	s := normalizeUsersAdmin(body, base)
	if !strings.HasPrefix(s, "<!DOCTYPE html>") {
		return s
	}
	return asTitleRe.FindString(s) + "\n----\n" + asHeadRe.FindString(s) + "\n----\n" + asMainRe.FindString(s) + "\n"
}

// TestActivitySearchMatchRedmine は活動・検索の画面と API が参照 Redmine と一致することを確認する。
func TestActivitySearchMatchRedmine(t *testing.T) {
	dir := filepath.Join("testdata", "activity_search")
	if ref := os.Getenv("BUROPHER_ACTIVITY_GOLDEN_REF"); ref != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		clients := map[string]*http.Client{}
		for _, tc := range activitySearchCases {
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
	for _, tc := range activitySearchCases {
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

// TestActivitySearchBehavior は 404・リダイレクトなどの振る舞いを確認する。
func TestActivitySearchBehavior(t *testing.T) {
	ts, _ := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")
	if res, _ := get(t, c, ts.URL+"/activity?user_id=999"); res.StatusCode != 404 {
		t.Errorf("unknown user_id: %d", res.StatusCode)
	}
	if res, _ := get(t, c, ts.URL+"/projects/unknown/activity"); res.StatusCode != 404 {
		t.Errorf("unknown project: %d", res.StatusCode)
	}
	// "#3" / "3" は可視なチケットへ移動する
	for _, q := range []string{"%233", "3"} {
		res, _ := get(t, c, ts.URL+"/search?q="+q)
		if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/issues/3") {
			t.Errorf("quick jump %s: %d %s", q, res.StatusCode, res.Header.Get("Location"))
		}
	}
	if res, _ := get(t, c, ts.URL+"/search?q=%23999"); res.StatusCode != 200 {
		t.Errorf("missing issue: %d", res.StatusCode)
	}
	// 非公開プロジェクトの活動は匿名ではログインを要求する
	if res, _ := get(t, newClient(t), ts.URL+"/projects/onlinestore/activity"); res.StatusCode != 302 {
		t.Errorf("anonymous private project: %d", res.StatusCode)
	}
	// submit 付きで activity_scope を保存し、次回の既定スコープになる
	if res, _ := get(t, c, ts.URL+"/activity?show_news=1&submit=Apply"); res.StatusCode != 200 {
		t.Fatalf("submit: %d", res.StatusCode)
	}
	_, body := get(t, c, ts.URL+"/activity")
	if !strings.Contains(body, `<input type="checkbox" name="show_news" id="show_news" value="1" checked="checked" />`) ||
		strings.Contains(body, `<input type="checkbox" name="show_issues" id="show_issues" value="1" checked="checked" />`) {
		t.Error("saved activity_scope not applied")
	}
}
