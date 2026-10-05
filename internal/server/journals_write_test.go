// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

// JournalsController#new / edit / update（引用返信・注記の編集・削除）のテスト。
// journal_2_edit_admin.js は参照 Redmine（3998）から XHR の GET で取得したもの:
//
//	go run ./tools/compat fetch -raw -user admin -H "X-Requested-With: XMLHttpRequest" \
//	  -H "Accept: text/javascript, application/javascript, application/ecmascript, application/x-ecmascript, */*; q=0.01" /journals/2/edit
//
// new / update は POST / PUT のため、期待値は app/views/journals/*.js.erb から導いたもの。

var (
	journalJSTokenRe    = regexp.MustCompile(`name=\\"authenticity_token\\" value=\\"[^"\\]+\\"`)
	journalJSFormNameRe = regexp.MustCompile(`name=\\"([a-z0-9_-]+?)-[0-9a-f]{8}\\"`)
)

func normalizeJS(s string) string {
	s = journalJSTokenRe.ReplaceAllString(s, `name=\"authenticity_token\" value=\"TOKEN\"`)
	return journalJSFormNameRe.ReplaceAllString(s, `name=\"$1-RANDOM\"`)
}

// xhrGet は XHR（Accept: text/javascript）で GET する。
func xhrGet(t *testing.T, c *http.Client, u string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Accept", "text/javascript, application/javascript, application/ecmascript, application/x-ecmascript, */*; q=0.01")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := res.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return res, b.String()
}

func TestJournalsEdit(t *testing.T) {
	ts, _ := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")

	res, body := xhrGet(t, admin, ts.URL+"/journals/2/edit")
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/javascript") {
		t.Fatalf("edit.js: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	recaptureGolden(t, "testdata/issues_write/journal_2_edit_admin.js", body, ts.URL)
	raw, err := os.ReadFile("testdata/issues_write/journal_2_edit_admin.js")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := normalizeJS(body), normalizeJS(string(raw)); got != want {
		t.Fatalf("edit.js differs\n got: %s\nwant: %s", got, want)
	}
	// 形式が js でない（respond_to { format.js } のみ）→ 406
	res, _ = get(t, admin, ts.URL+"/journals/2/edit")
	if res.StatusCode != http.StatusNotAcceptable {
		t.Errorf("edit html: %d", res.StatusCode)
	}
	// 編集権限なし（dlopper は他人の注記を編集できない）→ 403
	dlopper := login(t, ts, "dlopper", "foo")
	res, _ = xhrGet(t, dlopper, ts.URL+"/journals/2/edit")
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("dlopper edit: %d", res.StatusCode)
	}
	res, _ = xhrGet(t, admin, ts.URL+"/journals/999/edit")
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("missing journal: %d", res.StatusCode)
	}
}

func TestJournalsNewQuote(t *testing.T) {
	ts, d := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	head := "$('#update').show();\nshowAndScrollTo(\"add_notes\");\n\nvar notes = $('#issue_notes').val();\nif (notes > \"\") { notes = notes + \"\\n\\n\"}\n\n"

	// チケットの説明を引用
	res, body := projSubmit(t, admin, ts, http.MethodPost, "/issues/1/quoted", nil, true)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("quoted: %d", res.StatusCode)
	}
	want := head + "$('#issue_notes').blur().focus().val(notes + \"John Smith wrote:\\n> Unable to print recipes\\n\\n\");\n\n"
	if body != want {
		t.Errorf("quote issue:\n got: %q\nwant: %q", body, want)
	}
	// ジャーナルを引用（部分引用）
	res, body = projSubmit(t, admin, ts, http.MethodPost, "/issues/1/quoted?journal_id=2&journal_indice=2", url.Values{"quote": {"Some notes\nline"}}, true)
	want = head + "$('#issue_notes').blur().focus().val(notes + \"John Smith wrote in #note-2:\\n> Some notes\\n> line\\n\\n\");\n\n"
	if res.StatusCode != http.StatusOK || body != want {
		t.Errorf("quote journal: %d\n got: %q\nwant: %q", res.StatusCode, body, want)
	}
	// 非公開の注記を引用すると非公開のチェックを入れる
	if _, err := d.Exec(t.Context(), `UPDATE issue_journals SET private_notes = 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	res, body = projSubmit(t, admin, ts, http.MethodPost, "/issues/1/quoted?journal_id=1&journal_indice=1", nil, true)
	want = head + "$('#issue_notes').blur().focus().val(notes + \"Redmine Admin wrote in #note-1:\\n> Journal notes\\n\\n\");\n" +
		"$('#issue_private_notes').prop('checked', true);\n\n"
	if res.StatusCode != http.StatusOK || body != want {
		t.Errorf("quote private journal: %d\n got: %q\nwant: %q", res.StatusCode, body, want)
	}
	// 見えないジャーナル → 404
	res, _ = projSubmit(t, admin, ts, http.MethodPost, "/issues/1/quoted?journal_id=999", nil, true)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("missing journal: %d", res.StatusCode)
	}
	// 権限なし（OnlineStore の非メンバーで非公開プロジェクト）→ 403
	dlopper := login(t, ts, "dlopper", "foo")
	res, _ = projSubmit(t, dlopper, ts, http.MethodPost, "/issues/4/quoted", nil, true)
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("dlopper quoted: %d", res.StatusCode)
	}
}

func TestJournalsUpdate(t *testing.T) {
	ts, d := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")

	// 注記の更新（js）
	res, body := projSubmit(t, admin, ts, http.MethodPut, "/journals/2", url.Values{"journal[notes]": {"Updated **notes**"}}, true)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("update.js: %d", res.StatusCode)
	}
	if n := queryString(t, d, `SELECT notes FROM issue_journals WHERE id = 2`); n != "Updated **notes**" {
		t.Errorf("notes = %q", n)
	}
	if n := queryInt(t, d, `SELECT updated_by_id FROM issue_journals WHERE id = 2`); n != 1 {
		t.Errorf("updated_by_id = %d", n)
	}
	for _, want := range []string{
		"  $(\"#change-2\").attr('class', 'journal has-notes');\n",
		`  $("#change-2 .journal-actions").html('`,
		`/issues/1/quoted?journal_id=2&amp;journal_indice=2`,
		`  $("#journal-2-private_notes").replaceWith('<span id=\"journal-2-private_notes\" class=\"\"><\/span>');`,
		`  $("#journal-2-notes").replaceWith('<div id=\"journal-2-notes\" class=\"wiki journal-note\" data-quote-reply-target=\"content\"><p>Updated <strong>notes<\/strong><\/p><\/div>');`,
		`    journal_updated_info.replaceWith('<span title=\"01/15/2026 12:00 PM by Redmine Admin\" class=\"update-info\">· Edited<\/span>');`,
		"  setupHoverTooltips();\n\n\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if t.Failed() {
		t.Logf("%s", body)
	}
	// 非公開にする（admin は set_notes_private を持つ）
	res, body = projSubmit(t, admin, ts, http.MethodPut, "/journals/2", url.Values{"journal[notes]": {"Updated **notes**"}, "journal[private_notes]": {"1"}}, true)
	if res.StatusCode != http.StatusOK || queryInt(t, d, `SELECT private_notes FROM issue_journals WHERE id = 2`) != 1 ||
		!strings.Contains(body, "'journal has-notes private-notes'") {
		t.Errorf("private_notes not updated: %d", res.StatusCode)
	}
	// 詳細のあるジャーナルの注記を空にしても残る
	res, body = projSubmit(t, admin, ts, http.MethodPut, "/journals/1", url.Values{"journal[notes]": {""}}, true)
	if res.StatusCode != http.StatusOK || queryInt(t, d, `SELECT COUNT(*) FROM issue_journals WHERE id = 1`) != 1 ||
		!strings.Contains(body, "'journal has-details'") {
		t.Errorf("journal with details: %d %s", res.StatusCode, body)
	}
	// 詳細の無いジャーナルの注記を空にすると履歴から取り除く（Redmine 7.0 #44258: ジャーナル自体は残す）
	res, body = projSubmit(t, admin, ts, http.MethodPut, "/journals/2", url.Values{"journal[notes]": {""}}, true)
	if res.StatusCode != http.StatusOK || body != "  $(\"#change-2\").remove();\n\n\n" {
		t.Errorf("remove: %d %q", res.StatusCode, body)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM issue_journals WHERE id = 2`); n != 1 {
		t.Errorf("journal destroyed")
	}
	// html はチケットへリダイレクト
	res, _ = projSubmit(t, admin, ts, http.MethodPatch, "/journals/3", url.Values{"journal[notes]": {"html update"}}, false)
	expectRedirect(t, res, "/issues/2")
	// API
	res, _ = apiAs(t, ts, http.MethodPut, "/journals/4.json", "admin", "admin", `{"journal":{"notes":"api notes"}}`)
	if res.StatusCode != http.StatusNoContent || queryString(t, d, `SELECT notes FROM issue_journals WHERE id = 4`) != "api notes" {
		t.Errorf("api update: %d", res.StatusCode)
	}
	// 権限なし
	dlopper := login(t, ts, "dlopper", "foo")
	res, _ = projSubmit(t, dlopper, ts, http.MethodPut, "/journals/3", url.Values{"journal[notes]": {"x"}}, true)
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("dlopper update: %d", res.StatusCode)
	}
}
