// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// apiAs は HTTP Basic 認証（パスワード指定）で API を呼ぶ。
func apiAs(t *testing.T, ts *httptest.Server, method, path, user, pw, body string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	req.SetBasicAuth(user, pw)
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

// TestIssuesBulkUpdate は一括更新（属性・注記・"none"・カスタムフィールド）、移動、コピー、失敗時の再描画を確認する。
func TestIssuesBulkUpdate(t *testing.T) {
	ts, d := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")

	// 属性の一括更新 + 注記（back_url が無ければプロジェクトのチケット一覧へ）
	res, _ := projSubmit(t, admin, ts, http.MethodPost, "/issues/bulk_update", url.Values{
		"ids[]": {"1", "2"}, "notes": {"Bulk editing"},
		"issue[priority_id]": {"7"}, "issue[assigned_to_id]": {"none"}, "issue[category_id]": {""},
		"issue[custom_field_values][2]": {"777"}, "issue[start_date]": {""},
	}, false)
	expectRedirect(t, res, "/projects/ecookbook/issues")
	if n := queryInt(t, d, `SELECT COUNT(*) FROM issues WHERE id IN (1, 2) AND priority_id = 7 AND assigned_to_id IS NULL`); n != 2 {
		t.Errorf("updated issues = %d", n)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM issue_journals WHERE issue_id IN (1, 2) AND notes = 'Bulk editing' AND user_id = 1`); n != 2 {
		t.Errorf("journals with notes = %d", n)
	}
	jid := queryInt(t, d, `SELECT MAX(id) FROM issue_journals WHERE issue_id = 1`)
	if n := queryInt(t, d, `SELECT COUNT(*) FROM issue_journal_details WHERE journal_id = ? AND property = 'attr' AND prop_key = 'priority_id' AND old_value = '4' AND value = '7'`, jid); n != 1 {
		t.Errorf("priority detail not journalized")
	}
	if v := queryString(t, d, `SELECT value FROM custom_values WHERE customized_kind = 'issue' AND customized_id = 1 AND custom_field_id = 2`); v != "777" {
		t.Errorf("custom value = %q", v)
	}
	// フラッシュ
	_, page := get(t, admin, ts.URL+"/projects/ecookbook/issues")
	if !strings.Contains(page, "Successful update.") {
		t.Errorf("notice not shown")
	}
	// back_url があればそちらへ
	res, _ = projSubmit(t, admin, ts, http.MethodPatch, "/issues/bulk_update", url.Values{
		"ids[]": {"1"}, "issue[done_ratio]": {"30"}, "back_url": {"/issues/1"},
	}, false)
	expectRedirect(t, res, "/issues/1")
	if n := queryInt(t, d, `SELECT done_ratio FROM issues WHERE id = 1`); n != 30 {
		t.Errorf("done_ratio = %d", n)
	}

	// 移動（follow で移動先プロジェクトの一覧へ）
	res, _ = projSubmit(t, admin, ts, http.MethodPost, "/issues/bulk_update", url.Values{
		"ids[]": {"1", "2"}, "issue[project_id]": {"2"}, "follow": {"Move and follow"},
	}, false)
	expectRedirect(t, res, "/projects/onlinestore/issues")
	if n := queryInt(t, d, `SELECT COUNT(*) FROM issues WHERE id IN (1, 2) AND project_id = 2`); n != 2 {
		t.Errorf("moved issues = %d", n)
	}

	// コピー 1 件 + follow → コピーしたチケットへ。link_copy で copied_to 関連ができる
	before := queryInt(t, d, `SELECT MAX(id) FROM issues`)
	res, _ = projSubmit(t, admin, ts, http.MethodPost, "/issues/bulk_update", url.Values{
		"ids[]": {"3"}, "copy": {"1"}, "link_copy": {"1"}, "issue[project_id]": {"1"}, "follow": {"Copy and follow"},
	}, false)
	newID := queryInt(t, d, `SELECT MAX(id) FROM issues`)
	if newID == before {
		t.Fatalf("issue not copied (status %d)", res.StatusCode)
	}
	expectRedirect(t, res, "/issues/"+strconv.FormatInt(newID, 10))
	if s := queryString(t, d, `SELECT subject FROM issues WHERE id = ?`, newID); s != "Error 281 when updating a recipe" {
		t.Errorf("copied subject = %q", s)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM issue_relations WHERE issue_from_id = 3 AND issue_to_id = ? AND relation_type = 'copied_to'`, newID); n != 1 {
		t.Errorf("copied_to relation = %d", n)
	}
	// コピー（リンクなし・複数）→ 一覧へ
	before = queryInt(t, d, `SELECT COUNT(*) FROM issues`)
	rels := queryInt(t, d, `SELECT COUNT(*) FROM issue_relations`)
	res, _ = projSubmit(t, admin, ts, http.MethodPost, "/issues/bulk_update", url.Values{
		"ids[]": {"5", "6"}, "copy": {"1"}, "link_copy": {"0"}, "issue[project_id]": {""},
	}, false)
	if res.StatusCode != http.StatusFound {
		t.Fatalf("bulk copy: status %d", res.StatusCode)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM issues`); n != before+2 {
		t.Errorf("issues after copy = %d, want %d", n, before+2)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM issue_relations`); n != rels {
		t.Errorf("relations created without link_copy")
	}

	// 失敗（見積時間が負）→ bulk_edit をエラー付きで再描画
	res, body := projSubmit(t, admin, ts, http.MethodPost, "/issues/bulk_update", url.Values{
		"ids[]": {"7", "8"}, "issue[estimated_hours]": {"-1"}, "notes": {"failing"},
	}, false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("failed bulk update: status %d", res.StatusCode)
	}
	for _, want := range []string{
		`<div id="errorExplanation">`,
		"Failed to save 2 issue(s) on 2 selected: #7, #8.",
		"<li>Estimated time is invalid: #7, #8</li>",
		">\nfailing</textarea>",
		`<input type="text" name="issue[estimated_hours]" id="issue_estimated_hours" value="-1" size="10" />`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM issue_journals WHERE notes = 'failing'`); n != 0 {
		t.Errorf("journal saved for a failed update")
	}

	// 権限・存在しないチケット
	res, _ = get(t, newClient(t), ts.URL+"/issues/bulk_edit?ids[]=1")
	if res.StatusCode != http.StatusFound || !strings.Contains(res.Header.Get("Location"), "/login") {
		t.Errorf("anonymous bulk_edit: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	res, _ = get(t, admin, ts.URL+"/issues/bulk_edit?ids[]=999")
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("missing issue: %d", res.StatusCode)
	}
}

// TestIssuesBulkEditJS は bulk_edit.js（フォームの再描画）が #content を差し替える JS を返すことを確認する。
func TestIssuesBulkEditJS(t *testing.T) {
	ts, _ := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	res, body := projSubmit(t, admin, ts, http.MethodPost, "/issues/bulk_edit.js", url.Values{
		"ids[]": {"1", "2"}, "issue[project_id]": {"2"}, "issue[tracker_id]": {""},
	}, true)
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/javascript") {
		t.Fatalf("bulk_edit.js: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if !strings.HasPrefix(body, `$('#content').html('<h2>Bulk edit selected issues<\/h2>`) || !strings.HasSuffix(body, "');\n") {
		t.Errorf("unexpected body: %.200s", body)
	}
	if !strings.Contains(body, `<option value=\"2\" selected=\"selected\">OnlineStore<\/option>`) ||
		!strings.Contains(body, `value=\"Move\"`) {
		t.Errorf("target project not applied: %.3000s", body)
	}
}

// TestIssuesDestroy は削除（工数の扱いの確認画面・付け替え・API）を確認する。
// 確認画面は POST 以外で取得できないため、期待値は app/views/issues/destroy.html.erb から導いたもの。
func TestIssuesDestroy(t *testing.T) {
	ts, d := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")

	// 工数があるチケット: 確認画面
	res, body := projSubmit(t, admin, ts, http.MethodDelete, "/issues/1", nil, false)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("destroy with time entries: status %d", res.StatusCode)
	}
	for _, want := range []string{
		`<h2>Confirmation</h2>`,
		`<form action="/issues/1" accept-charset="UTF-8" name="form-`,
		`" method="post"><input type="hidden" name="_method" value="delete" autocomplete="off" />`,
		`<input type="hidden" name="ids[]" value="1" autocomplete="off" />`,
		`<p><strong>154:15 hours were reported on the issues you are about to delete. What do you want to do?</strong></p>`,
		`<label><input type="radio" name="todo" id="todo_destroy" value="destroy" checked="checked" /> Delete reported hours</label><br />`,
		`<label><input type="radio" name="todo" id="todo_nullify" value="nullify" /> Assign reported hours to the project</label><br />`,
		`<label><input type="radio" name="todo" id="todo_reassign" value="reassign" onchange="if (this.checked) { $(&quot;#reassign_to_id&quot;).focus(); }" /> Reassign reported hours to this issue:</label>`,
		`<input type="text" name="reassign_to_id" id="reassign_to_id" size="6" onfocus="$(&quot;#todo_reassign&quot;).attr(&quot;checked&quot;, true);" />`,
		`observeAutocompleteField('reassign_to_id', '/issues/auto_complete?project_id=ecookbook')`,
		`<input type="submit" name="commit" value="Apply" data-disable-with="Apply" />`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if t.Failed() {
		t.Logf("%s", extract(body, `<div id="content">`, `<div id="footer">`))
	}
	// 付け替え先が別プロジェクト → エラー
	res, body = projSubmit(t, admin, ts, http.MethodDelete, "/issues", url.Values{"ids[]": {"1", "3"}, "todo": {"reassign"}, "reassign_to_id": {"4"}}, false)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "The issue was not found or does not belong to this project") ||
		!strings.Contains(body, `<input type="text" name="reassign_to_id" id="reassign_to_id" value="4"`) ||
		!strings.Contains(body, `<form action="/issues" accept-charset="UTF-8" name="form-`) {
		t.Fatalf("reassign to another project: %d\n%s", res.StatusCode, extract(body, `<div id="content">`, `<div id="footer">`))
	}
	// 削除対象への付け替え → エラー
	res, body = projSubmit(t, admin, ts, http.MethodDelete, "/issues", url.Values{"ids[]": {"1", "3"}, "todo": {"reassign"}, "reassign_to_id": {"3"}}, false)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Spent time cannot be reassigned to an issue that is about to be deleted") {
		t.Fatalf("reassign to a deleted issue: %d", res.StatusCode)
	}
	// 付け替えて削除
	res, _ = projSubmit(t, admin, ts, http.MethodDelete, "/issues", url.Values{"ids[]": {"1", "3"}, "todo": {"reassign"}, "reassign_to_id": {"2"}}, false)
	expectRedirect(t, res, "/projects/ecookbook/issues")
	if n := queryInt(t, d, `SELECT COUNT(*) FROM issues WHERE id IN (1, 3)`); n != 0 {
		t.Errorf("issues not deleted")
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM time_entries WHERE issue_id = 2`); n != 3 {
		t.Errorf("reassigned time entries = %d", n)
	}
	_, page := get(t, admin, ts.URL+"/projects/ecookbook/issues")
	if !strings.Contains(page, "Successful deletion.") {
		t.Errorf("notice not shown")
	}
	// 工数を外して削除（back_url へ戻る）
	res, _ = projSubmit(t, admin, ts, http.MethodDelete, "/issues/2", url.Values{"todo": {"nullify"}, "back_url": {"/issues"}}, false)
	expectRedirect(t, res, "/issues")
	if n := queryInt(t, d, `SELECT COUNT(*) FROM time_entries WHERE issue_id IS NULL`); n != 5 {
		t.Errorf("nullified time entries = %d", n)
	}

	// 権限: dlopper（Developer）は delete_issues を持たない
	res, _ = apiAs(t, ts, http.MethodDelete, "/issues/5.json", "dlopper", "foo", "")
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("dlopper api delete: %d", res.StatusCode)
	}
	// API: 204
	res, _ = apiAs(t, ts, http.MethodDelete, "/issues/5.json", "admin", "admin", "")
	if res.StatusCode != http.StatusNoContent {
		t.Errorf("api delete: %d", res.StatusCode)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM issues WHERE id = 5`); n != 0 {
		t.Errorf("issue 5 not deleted")
	}
	res, _ = apiAs(t, ts, http.MethodDelete, "/issues/999.json", "admin", "admin", "")
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("api delete missing: %d", res.StatusCode)
	}
}
