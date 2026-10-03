// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
)

// チケットの関連・ウォッチャーの書き込み（POST / DELETE）のテスト。参照 Redmine には送れないため、
// GET の応答は参照から取得した値、状態を変える応答は Redmine のビュー（*.js.erb / *.api.rsb）から組み立てた値で確認する。

// relationJournalDetails は issue のジャーナル明細（property = 'relation'）を "prop_key old->value" で返す。
func relationJournalDetails(t *testing.T, d *db.DB, issueID int64) []string {
	t.Helper()
	var rows []struct {
		Key string  `db:"prop_key"`
		Old *string `db:"old_value"`
		Val *string `db:"value"`
	}
	if err := d.Select(t.Context(), &rows, `SELECT d.prop_key, d.old_value, d.value FROM issue_journal_details d
JOIN issue_journals j ON j.id = d.journal_id WHERE j.issue_id = ? AND d.property = 'relation' ORDER BY d.id`, issueID); err != nil {
		t.Fatal(err)
	}
	str := func(p *string) string {
		if p == nil {
			return "nil"
		}
		return *p
	}
	var out []string
	for _, r := range rows {
		out = append(out, r.Key+" "+str(r.Old)+"->"+str(r.Val))
	}
	return out
}

func itoaTest(n int64) string { return strconv.FormatInt(n, 10) }

// anonAPI は認証なしで API を呼ぶ。
func anonAPI(t *testing.T, method, u, body string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, u, strings.NewReader(body))
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

// TestIssueRelationsAPI は関連の REST API（test/integration/api_test/issue_relations_test.rb 相当と参照の応答）。
func TestIssueRelationsAPI(t *testing.T) {
	ts, d := newFixtureServer(t)
	// 参照 Redmine の応答（GET は参照から取得）
	gets := []struct{ user, path, want string }{
		{"admin", "/issues/2/relations.json", `{"relations":[{"id":2,"issue_id":2,"issue_to_id":3,"relation_type":"relates","delay":null}]}`},
		{"admin", "/issues/1/relations.json", `{"relations":[]}`},
		{"admin", "/issues/2/relations.xml", `<?xml version="1.0" encoding="UTF-8"?><relations type="array"><relation><id>2</id><issue_id>2</issue_id><issue_to_id>3</issue_to_id><relation_type>relates</relation_type><delay/></relation></relations>`},
		{"admin", "/relations/1.json", `{"relation":{"id":1,"issue_id":10,"issue_to_id":9,"relation_type":"blocks","delay":null}}`},
		{"admin", "/relations/2.xml", `<?xml version="1.0" encoding="UTF-8"?><relation><id>2</id><issue_id>2</issue_id><issue_to_id>3</issue_to_id><relation_type>relates</relation_type><delay/></relation>`},
		{"jsmith", "/issues/9/relations.json", `{"relations":[{"id":1,"issue_id":10,"issue_to_id":9,"relation_type":"blocks","delay":null}]}`},
	}
	for _, g := range gets {
		res, body := projectsAPIRequest(t, ts, http.MethodGet, g.path, g.user, "")
		if res.StatusCode != 200 || body != g.want {
			t.Errorf("GET %s: %d %s", g.path, res.StatusCode, body)
		}
	}
	for _, p := range []string{"/relations/999.json", "/issues/999/relations.json"} {
		if res, _ := projectsAPIRequest(t, ts, http.MethodGet, p, "admin", ""); res.StatusCode != 404 {
			t.Errorf("GET %s: %d", p, res.StatusCode)
		}
	}
	// 匿名: index は manage_issue_relations が要る（401）、show は可視性のみ（200）
	if res, _ := anonAPI(t, http.MethodGet, ts.URL+"/issues/2/relations.json", ""); res.StatusCode != 401 {
		t.Errorf("anonymous index: %d", res.StatusCode)
	}
	if res, body := anonAPI(t, http.MethodGet, ts.URL+"/relations/2.json", ""); res.StatusCode != 200 || !strings.Contains(body, `"id":2`) {
		t.Errorf("anonymous show: %d %s", res.StatusCode, body)
	}
	// html は head :ok
	admin := login(t, ts, "admin", "admin")
	if res, body := get(t, admin, ts.URL+"/issues/2/relations"); res.StatusCode != 200 || body != "" {
		t.Errorf("index html: %d %q", res.StatusCode, body)
	}

	// 作成（JSON の数値の issue_to_id）
	res, body := projectsAPIRequest(t, ts, http.MethodPost, "/issues/2/relations.json", "jsmith",
		`{"relation":{"issue_to_id":7,"relation_type":"relates"}}`)
	if res.StatusCode != 201 {
		t.Fatalf("create: %d %s", res.StatusCode, body)
	}
	var id int64
	if err := d.Get(t.Context(), &id, `SELECT id FROM issue_relations WHERE issue_from_id = 2 AND issue_to_id = 7 AND relation_type = 'relates'`); err != nil {
		t.Fatal(err)
	}
	if want := `{"relation":{"id":` + itoaTest(id) + `,"issue_id":2,"issue_to_id":7,"relation_type":"relates","delay":null}}`; body != want {
		t.Errorf("create body: %s", body)
	}
	if loc := res.Header.Get("Location"); loc != ts.URL+"/relations/"+itoaTest(id) {
		t.Errorf("Location: %s", loc)
	}
	if got := relationJournalDetails(t, d, 2); len(got) != 1 || got[0] != "relates nil->7" {
		t.Errorf("issue 2 journal: %v", got)
	}
	if got := relationJournalDetails(t, d, 7); len(got) != 1 || got[0] != "relates nil->2" {
		t.Errorf("issue 7 journal: %v", got)
	}
	// 検証エラー
	res, body = projectsAPIRequest(t, ts, http.MethodPost, "/issues/2/relations.json", "jsmith",
		`{"relation":{"issue_to_id":8,"relation_type":"foo"}}`)
	if res.StatusCode != 422 || body != `{"errors":["Relation type is not included in the list"]}` {
		t.Errorf("create invalid: %d %s", res.StatusCode, body)
	}
	res, body = projectsAPIRequest(t, ts, http.MethodPost, "/issues/2/relations.xml?relation[issue_to_id]=3&relation[relation_type]=relates", "jsmith", "")
	if res.StatusCode != 422 || !strings.Contains(body, "<error>Related issue has already been taken</error>") {
		t.Errorf("create taken: %d %s", res.StatusCode, body)
	}
	if res, _ := anonAPI(t, http.MethodPost, ts.URL+"/issues/2/relations.json", `{"relation":{"issue_to_id":7}}`); res.StatusCode != 401 {
		t.Errorf("anonymous create: %d", res.StatusCode)
	}
	// 削除
	if res, _ := anonAPI(t, http.MethodDelete, ts.URL+"/relations/2.json", ""); res.StatusCode != 401 {
		t.Errorf("anonymous destroy: %d", res.StatusCode)
	}
	res, body = projectsAPIRequest(t, ts, http.MethodDelete, "/relations/2.json", "jsmith", "")
	if res.StatusCode != 204 || body != "" {
		t.Errorf("destroy: %d %q", res.StatusCode, body)
	}
	if n := countRows(t, d, `SELECT COUNT(*) FROM issue_relations WHERE id = 2`); n != 0 {
		t.Errorf("relation 2 not deleted")
	}
	if got := relationJournalDetails(t, d, 3); len(got) != 1 || got[0] != "relates 2->nil" {
		t.Errorf("issue 3 journal: %v", got)
	}
}

// TestIssueRelationsCreateDestroyJS は issue_relations#create / destroy の JS 応答と DB の変化を確認する。
func TestIssueRelationsCreateDestroyJS(t *testing.T) {
	ts, d := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	// 複数（"2, #3,foo,1"）: 2・3 は保存（blocked は反転して blocks で保存）、foo は空、1 は自分自身
	res, body := projSubmit(t, admin, ts, http.MethodPost, "/issues/1/relations",
		url.Values{"relation[issue_to_id]": {"2, #3,foo,1"}, "relation[relation_type]": {"blocked"}, "relation[delay]": {""}}, true)
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/javascript") {
		t.Fatalf("create.js: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if n := countRows(t, d, `SELECT COUNT(*) FROM issue_relations WHERE issue_to_id = 1 AND relation_type = 'blocks' AND issue_from_id IN (2, 3)`); n != 2 {
		t.Errorf("relations created: %d", n)
	}
	for _, s := range []string{
		`$('#relations').html('<div class=\"contextual\">`,
		`<li>Related issue cannot be blank<\/li>`,
		`<li>Related issue is invalid: #1<\/li>`,
		`<form class=\"new_relation\" id=\"new-relation-form\"`,
		`<option selected=\"selected\" value=\"blocked\">Blocked by<\/option>`,
		`Issue #<input value=\"1\" size=\"10\"`,
		`Delay: <input size=\"3\" type=\"text\" value=\"\" name=\"relation[delay]\"`,
		`<tr id=\"relation-3\"`,
		`Blocked by <a class=\"issue tracker-2`,
	} {
		if !strings.Contains(body, s) {
			t.Errorf("create.js lacks %s\n%s", s, body)
		}
	}
	if !strings.HasSuffix(body, "<\\/form>');\n$('#new-relation-form').show();\n$('#relation_issue_to_id').focus();\n") {
		t.Errorf("create.js tail: %s", body)
	}
	if got := relationJournalDetails(t, d, 1); len(got) != 2 || got[0] != "blocked nil->2" || got[1] != "blocked nil->3" {
		t.Errorf("issue 1 journal: %v", got)
	}
	if got := relationJournalDetails(t, d, 2); len(got) != 1 || got[0] != "blocks nil->1" {
		t.Errorf("issue 2 journal: %v", got)
	}

	// 成功のみ（保存済みの @relation: edit_relation。入力欄をクリアする JS が付く）
	res, body = projSubmit(t, admin, ts, http.MethodPost, "/issues/1/relations",
		url.Values{"relation[issue_to_id]": {"7"}, "relation[relation_type]": {"precedes"}, "relation[delay]": {"2"}}, true)
	if res.StatusCode != 200 {
		t.Fatalf("create.js precedes: %d", res.StatusCode)
	}
	for _, s := range []string{
		`<form class=\"edit_relation\" id=\"new-relation-form\"`,
		`<option selected=\"selected\" value=\"precedes\">Precedes<\/option>`,
		`Issue #<input value=\"\" size=\"10\"`,
		`Delay: <input size=\"3\" type=\"text\" value=\"2\"`,
		"');\n  $('#relation_delay').val('');\n  $('#relation_issue_to_id').val('');\n$('#new-relation-form').show();",
	} {
		if !strings.Contains(body, s) {
			t.Errorf("create.js (saved) lacks %s", s)
		}
	}
	if strings.Contains(body, "errorExplanation") {
		t.Errorf("create.js (saved) has errors")
	}
	var delay int
	if err := d.Get(t.Context(), &delay, `SELECT delay FROM issue_relations WHERE issue_from_id = 1 AND issue_to_id = 7`); err != nil || delay != 2 {
		t.Errorf("precedes delay: %d %v", delay, err)
	}

	// 反転後の関連が既にある（RecordNotUnique → errors.add :base, :taken）
	for i := 0; i < 2; i++ {
		res, body = projSubmit(t, admin, ts, http.MethodPost, "/issues/5/relations",
			url.Values{"relation[issue_to_id]": {"13"}, "relation[relation_type]": {"follows"}, "relation[delay]": {""}}, true)
	}
	if res.StatusCode != 200 || !strings.Contains(body, "has already been taken") {
		t.Errorf("duplicated follows: %d %s", res.StatusCode, body)
	}

	// html はリダイレクト
	res, _ = projSubmit(t, admin, ts, http.MethodPost, "/issues/2/relations",
		url.Values{"relation[issue_to_id]": {"7"}, "relation[relation_type]": {"relates"}}, false)
	if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/issues/2") {
		t.Errorf("create html: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if res, _ := projSubmit(t, admin, ts, http.MethodPost, "/issues/999/relations",
		url.Values{"relation[issue_to_id]": {"1"}}, false); res.StatusCode != 404 {
		t.Errorf("create on missing issue: %d", res.StatusCode)
	}

	// 削除（destroy.js）
	res, body = projSubmit(t, admin, ts, http.MethodDelete, "/relations/2?issue_id=2", nil, true)
	if res.StatusCode != 200 {
		t.Fatalf("destroy.js: %d", res.StatusCode)
	}
	if !strings.HasPrefix(body, `$('#relation-2').remove();`+"\n"+`$(".issues-stat").replaceWith('<span class=\"issues-stat\">`) ||
		!strings.HasSuffix(body, "<\\/span>')\n") {
		t.Errorf("destroy.js: %s", body)
	}
	if n := countRows(t, d, `SELECT COUNT(*) FROM issue_relations WHERE id = 2`); n != 0 {
		t.Errorf("relation 2 not deleted")
	}
	if res, _ := projSubmit(t, admin, ts, http.MethodDelete, "/relations/999", nil, false); res.StatusCode != 404 {
		t.Errorf("destroy missing: %d", res.StatusCode)
	}
	// html の削除は関連元へリダイレクト
	res, _ = projSubmit(t, admin, ts, http.MethodDelete, "/relations/1", nil, false)
	if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/issues/10") {
		t.Errorf("destroy html: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
}

// watcherIDsOf は watchers のユーザー id（id 順）。
func watcherIDsOf(t *testing.T, d *db.DB, kind string, id int64) []int64 {
	t.Helper()
	var ids []int64
	if err := d.Select(t.Context(), &ids, `SELECT principal_id FROM watchers WHERE watchable_kind = ? AND watchable_id = ? ORDER BY principal_id`, kind, id); err != nil {
		t.Fatal(err)
	}
	return ids
}

func idsString(ids []int64) string {
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = itoaTest(id)
	}
	return strings.Join(s, ",")
}

// TestWatchersWrite は watch / unwatch / create / destroy / append の JS・html・API と DB の変化を確認する。
func TestWatchersWrite(t *testing.T) {
	ts, d := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")

	// watch（JS: watcher_link の置き換えと #watchers の再描画）
	res, body := projSubmit(t, admin, ts, http.MethodPost, "/watchers/watch?object_type=issue&object_id=1", nil, true)
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/javascript") {
		t.Fatalf("watch.js: %d", res.StatusCode)
	}
	if got := idsString(watcherIDsOf(t, d, "issue", 1)); got != "1" {
		t.Errorf("watchers after watch: %s", got)
	}
	wantHead := `$(".issue-1-watcher").each(function(){$(this).replaceWith("<a class=\"issue-1-watcher icon icon-fav\" data-remote=\"true\" rel=\"nofollow\" data-method=\"delete\" href=\"/watchers/watch?object_id=1&amp;object_type=issue\">`
	if !strings.HasPrefix(body, wantHead) || !strings.Contains(body, "\n$('#watchers').html('<div class=\\\"contextual\\\">\\n<a data-remote=\\\"true\\\" data-method=\\\"get\\\" href=\\\"/watchers/new?object_id=1&amp;object_type=issue\\\">Add<\\/a>\\n<\\/div>\\n\\n<h3>Watchers (1)<\\/h3>") ||
		!strings.HasSuffix(body, "<\\/ul>\\n');\n") {
		t.Errorf("watch.js: %s", body)
	}
	// 二重に watch しても行は増えない
	projSubmit(t, admin, ts, http.MethodPost, "/watchers/watch?object_type=issue&object_id=1", nil, true)
	if got := idsString(watcherIDsOf(t, d, "issue", 1)); got != "1" {
		t.Errorf("watchers after double watch: %s", got)
	}
	// unwatch（html: Referer が無ければ 'Watcher removed.' をレイアウト付きで表示）
	res, body = projSubmit(t, admin, ts, http.MethodDelete, "/watchers/watch?object_type=issue&object_id=1", nil, false)
	if res.StatusCode != 200 || !strings.Contains(body, "Watcher removed.") || !strings.Contains(body, `<div id="content">`) {
		t.Errorf("unwatch html: %d", res.StatusCode)
	}
	if got := idsString(watcherIDsOf(t, d, "issue", 1)); got != "" {
		t.Errorf("watchers after unwatch: %s", got)
	}
	// 一括（コンテキストメニュー）
	res, body = projSubmit(t, admin, ts, http.MethodPost, "/watchers/watch?object_type=issue&object_id[]=1&object_id[]=3", nil, true)
	if res.StatusCode != 200 || !strings.HasPrefix(body, `$(".issue-bulk-watcher").each(function(){$(this).replaceWith("<a class=\"issue-bulk-watcher icon icon-fav\" data-remote=\"true\" rel=\"nofollow\" data-method=\"delete\" href=\"/watchers/watch?object_id%5B%5D=1&amp;object_id%5B%5D=3&amp;object_type=issue\">`) {
		t.Errorf("bulk watch.js: %d %s", res.StatusCode, body)
	}
	if watcherIDsOf(t, d, "issue", 3)[0] != 1 {
		t.Errorf("bulk watch not saved")
	}
	// 見つからない・匿名
	if res, _ := projSubmit(t, admin, ts, http.MethodPost, "/watchers/watch?object_type=issue&object_id=999", nil, true); res.StatusCode != 404 {
		t.Errorf("watch missing: %d", res.StatusCode)
	}
	if res, _ := projSubmit(t, admin, ts, http.MethodPost, "/watchers/watch?object_type=foo&object_id=1", nil, true); res.StatusCode != 404 {
		t.Errorf("watch unknown type: %d", res.StatusCode)
	}

	// create（モーダルのフォーム: watcher[user_ids][]、グループも追加できる）
	res, body = projSubmit(t, admin, ts, http.MethodPost, "/watchers",
		url.Values{"object_type": {"issue"}, "object_id[]": {"1"}, "watcher[user_ids][]": {"3", "10"}}, true)
	if res.StatusCode != 200 {
		t.Fatalf("create.js: %d", res.StatusCode)
	}
	if got := idsString(watcherIDsOf(t, d, "issue", 1)); got != "1,3,10" {
		t.Errorf("watchers after create: %s", got)
	}
	if !strings.HasPrefix(body, "$('#ajax-modal').html(\n  '<h3 class=\\\"title\\\">Add watchers<\\/h3>") ||
		!strings.Contains(body, "<\\/form>');\n\n  $(\".issue-1-watcher\")") ||
		!strings.Contains(body, "<h3>Watchers (3)<\\/h3>") {
		t.Errorf("create.js: %s", body)
	}
	// Principal.sorted: ユーザーが先、グループが後
	if i, j := strings.Index(body, `<li class=\"user-3\">`), strings.Index(body, `<li class=\"user-10\">`); i < 0 || j < i {
		t.Errorf("watchers order: %s", body)
	}
	// destroy（JS）
	res, body = projSubmit(t, admin, ts, http.MethodDelete, "/watchers?object_type=issue&object_id=1&user_id=3", nil, true)
	if res.StatusCode != 200 || !strings.HasPrefix(body, `  $(".issue-1-watcher")`) || !strings.Contains(body, "<h3>Watchers (2)<\\/h3>") {
		t.Errorf("destroy.js: %d %s", res.StatusCode, body)
	}
	if got := idsString(watcherIDsOf(t, d, "issue", 1)); got != "1,10" {
		t.Errorf("watchers after destroy: %s", got)
	}
	if res, _ := projSubmit(t, admin, ts, http.MethodDelete, "/watchers?object_type=issue&object_id=1&user_id=999", nil, true); res.StatusCode != 404 {
		t.Errorf("destroy unknown user: %d", res.StatusCode)
	}

	// API（POST /issues/:id/watchers.json, DELETE /issues/:id/watchers/:user_id.json）
	res, body = projectsAPIRequest(t, ts, http.MethodPost, "/issues/1/watchers.json", "jsmith", `{"user_id":3}`)
	if res.StatusCode != 204 || body != "" {
		t.Errorf("api create: %d %q", res.StatusCode, body)
	}
	if got := idsString(watcherIDsOf(t, d, "issue", 1)); got != "1,3,10" {
		t.Errorf("watchers after api create: %s", got)
	}
	res, body = projectsAPIRequest(t, ts, http.MethodDelete, "/issues/1/watchers/3.xml", "jsmith", "")
	if res.StatusCode != 204 || body != "" {
		t.Errorf("api destroy: %d %q", res.StatusCode, body)
	}
	if got := idsString(watcherIDsOf(t, d, "issue", 1)); got != "1,10" {
		t.Errorf("watchers after api destroy: %s", got)
	}
	// 権限なし（dlopper の Developer は add_issue_watchers / delete_issue_watchers を持たない）
	dl := login(t, ts, "dlopper", "foo")
	if res, _ := projSubmit(t, dl, ts, http.MethodPost, "/issues/1/watchers", url.Values{"user_id": {"2"}}, true); res.StatusCode != 403 {
		t.Errorf("create without permission: %d", res.StatusCode)
	}
	if res, _ := projSubmit(t, dl, ts, http.MethodDelete, "/watchers?object_type=issue&object_id=1&user_id=10", nil, true); res.StatusCode != 403 {
		t.Errorf("destroy without permission: %d", res.StatusCode)
	}
	// ログインしていれば権限が無くても watch はできる（可視性のみ）
	if res, _ := projSubmit(t, dl, ts, http.MethodPost, "/watchers/watch?object_type=issue&object_id=1", nil, true); res.StatusCode != 200 {
		t.Errorf("watch by developer: %d", res.StatusCode)
	}
	if got := idsString(watcherIDsOf(t, d, "issue", 1)); got != "1,3,10" {
		t.Errorf("watchers after dlopper watch: %s", got)
	}
	// 匿名の watch はログインへ
	anon := newClient(t)
	_, page := get(t, anon, ts.URL+"/login")
	res, _ = post(t, anon, ts.URL+"/watchers/watch?object_type=issue&object_id=1", url.Values{"authenticity_token": {csrfToken(t, page)}})
	if res.StatusCode != 302 || !strings.Contains(res.Header.Get("Location"), "/login?back_url=") {
		t.Errorf("anonymous watch: %d %s", res.StatusCode, res.Header.Get("Location"))
	}

	// append（新規チケットのフォーム。永続化されたチケットは不要）
	res, body = projSubmit(t, admin, ts, http.MethodPost, "/watchers/append", url.Values{"project_id": {"ecookbook"}, "watcher[user_ids][]": {"3", "2"}}, true)
	want := `  $("#issue_watcher_user_ids_2").remove();
  $("#issue_watcher_user_ids_3").remove();
$('#watchers_inputs').append('<label id=\"issue_watcher_user_ids_2\" class=\"floating\"><input type=\"checkbox\" name=\"issue[watcher_user_ids][]\" value=\"2\" checked=\"checked\" /> John Smith<\/label><label id=\"issue_watcher_user_ids_3\" class=\"floating\"><input type=\"checkbox\" name=\"issue[watcher_user_ids][]\" value=\"3\" checked=\"checked\" /> Dave Lopper<\/label>');
`
	if res.StatusCode != 200 || body != want {
		t.Errorf("append.js: %d\n%s", res.StatusCode, body)
	}
	if res, body := projSubmit(t, admin, ts, http.MethodPost, "/watchers/append", url.Values{"project_id": {"ecookbook"}}, true); res.StatusCode != 200 || body != "" {
		t.Errorf("append without users: %d %q", res.StatusCode, body)
	}
}

// TestWatchersWikiPage は wiki_page のウォッチ（汎用の watchers/_watchers）を確認する。
func TestWatchersWikiPage(t *testing.T) {
	ts, d := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	res, body := projSubmit(t, admin, ts, http.MethodPost, "/watchers/watch?object_type=wiki_page&object_id=2", nil, true)
	if res.StatusCode != 200 || !strings.HasPrefix(body, `$(".wiki_page-2-watcher").each(`) || !strings.Contains(body, "$('#watchers').html('") {
		t.Errorf("wiki_page watch.js: %d %s", res.StatusCode, body)
	}
	if got := idsString(watcherIDsOf(t, d, "wiki_page", 2)); got != "1" {
		t.Errorf("wiki_page watchers: %s", got)
	}
}
