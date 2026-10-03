// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

// チケットの作成・更新（IssuesController#new / create / edit / update と REST API）の振る舞いのテスト。
// HTML の一致は compat シナリオ（testdata/compat/scenarios/issues_new.yml・issues_write.yml）で確認しており、
// ここでは DB の状態（チケット・ジャーナル・作業時間・ウォッチャー・関連）と通知を確認する。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/config"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/server"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/testfixtures"
)

// fakeNotifier は配送された通知を記録する issues.Notifier。
type fakeNotifier struct {
	mu   sync.Mutex
	sent []issues.Notification
}

func (n *fakeNotifier) Enqueue(_ context.Context, x issues.Notification) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sent = append(n.sent, x)
	return nil
}

func (n *fakeNotifier) take() []issues.Notification {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := n.sent
	n.sent = nil
	return out
}

// newIssuesWriteServer は newFixtureServer と同じ環境で、通知を fakeNotifier に集める。
func newIssuesWriteServer(t *testing.T) (*httptest.Server, *db.DB, *fakeNotifier) {
	t.Helper()
	ctx := context.Background()
	d := dbtest.New(t)
	if err := testfixtures.LoadContext(ctx, d, frozenTime, testfixtures.All()...); err != nil {
		t.Fatal(err)
	}
	st, err := settings.New(ctx, repository.SettingsStore{DB: d})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Set(ctx, "rest_api_enabled", "1"); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Server.SecretKey = "test-secret"
	cfg.Storage.AttachmentsPath = t.TempDir()
	n := &fakeNotifier{}
	srv, err := server.New(cfg, d, server.Options{TempDir: t.TempDir(), Now: func() time.Time { return frozenTime }, Notifier: n})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, d, n
}

func countRows(t *testing.T, d *db.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := d.Get(context.Background(), &n, q, args...); err != nil {
		t.Fatal(err)
	}
	return n
}

func issuesAPIRequest(t *testing.T, ts *httptest.Server, method, path, user, pw, ctype, body string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	req.SetBasicAuth(user, pw)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := newClient(t).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		k, err := res.Body.Read(buf)
		b.Write(buf[:k])
		if err != nil {
			break
		}
	}
	return res, b.String()
}

func TestIssuesCreate(t *testing.T) {
	ts, d, n := newIssuesWriteServer(t)
	ctx := context.Background()
	c := login(t, ts, "jsmith", "jsmith")

	res, _ := projSubmit(t, c, ts, http.MethodPost, "/projects/ecookbook/issues", url.Values{
		"issue[tracker_id]": {"1"}, "issue[subject]": {"Created by test"}, "issue[priority_id]": {"5"},
		"issue[assigned_to_id]": {"3"}, "issue[custom_field_values][2]": {"cf value"},
		"issue[watcher_user_ids][]": {"", "3"},
	}, false)
	if res.StatusCode != http.StatusFound || !strings.HasSuffix(res.Header.Get("Location"), "/issues/15") {
		t.Fatalf("create: status %d location %q", res.StatusCode, res.Header.Get("Location"))
	}
	var row struct {
		Subject    string `db:"subject"`
		AuthorID   int64  `db:"author_id"`
		AssignedTo int64  `db:"assigned_to_id"`
		StatusID   int64  `db:"status_id"`
	}
	if err := d.Get(ctx, &row, `SELECT subject, author_id, assigned_to_id, status_id FROM issues WHERE id = 15`); err != nil {
		t.Fatal(err)
	}
	if row.Subject != "Created by test" || row.AuthorID != 2 || row.AssignedTo != 3 || row.StatusID != 1 {
		t.Errorf("issue row = %+v", row)
	}
	if got := countRows(t, d, `SELECT COUNT(*) FROM custom_values WHERE customized_kind = 'issue' AND customized_id = 15 AND custom_field_id = 2 AND value = 'cf value'`); got != 1 {
		t.Errorf("custom value rows = %d", got)
	}
	if got := countRows(t, d, `SELECT COUNT(*) FROM watchers WHERE watchable_kind = 'issue' AND watchable_id = 15 AND principal_id = 3`); got != 1 {
		t.Errorf("watcher rows = %d", got)
	}
	// 新規作成はジャーナルを作らない
	if got := countRows(t, d, `SELECT COUNT(*) FROM issue_journals WHERE issue_id = 15`); got != 0 {
		t.Errorf("journals = %d", got)
	}
	ns := n.take()
	if len(ns) != 1 || ns[0].Event != issues.NotifyIssueAdd || ns[0].IssueID != 15 {
		t.Fatalf("notifications = %+v", ns)
	}
	// flash（リンク付き）
	_, body := get(t, c, ts.URL+"/issues/15")
	if !strings.Contains(body, `Issue <a title="Created by test" href="/issues/15">#15</a> created.`) {
		t.Errorf("flash notice not found")
	}

	// 検証エラーは new を再描画し、何も作らない
	res, body = projSubmit(t, c, ts, http.MethodPost, "/projects/ecookbook/issues", url.Values{
		"issue[tracker_id]": {"1"}, "issue[subject]": {""}, "issue[priority_id]": {"5"},
	}, false)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "<li>Subject cannot be blank</li>") {
		t.Errorf("invalid create: status %d", res.StatusCode)
	}
	if got := countRows(t, d, `SELECT COUNT(*) FROM issues`); got != 15 {
		t.Errorf("issues = %d", got)
	}
	if len(n.take()) != 0 {
		t.Error("notification on failed create")
	}

	// 作成して続ける
	res, _ = projSubmit(t, c, ts, http.MethodPost, "/projects/ecookbook/issues", url.Values{
		"issue[tracker_id]": {"2"}, "issue[subject]": {"Continue"}, "issue[priority_id]": {"5"}, "continue": {"1"},
	}, false)
	if loc := res.Header.Get("Location"); !strings.HasSuffix(loc, "/projects/ecookbook/issues/new?issue%5Btracker_id%5D=2") {
		t.Errorf("continue location = %q", loc)
	}

	// 匿名は作成できない（ログインへ）
	anon := newClient(t)
	_, page := get(t, anon, ts.URL+"/projects/ecookbook/issues")
	form := url.Values{"authenticity_token": {csrfMeta(t, page)}, "issue[subject]": {"anon"}}
	res, _ = post(t, anon, ts.URL+"/projects/ecookbook/issues", form)
	if res.StatusCode != http.StatusFound || !strings.Contains(res.Header.Get("Location"), "/login") {
		t.Errorf("anonymous create: status %d location %q", res.StatusCode, res.Header.Get("Location"))
	}
}

func csrfMeta(t *testing.T, page string) string {
	t.Helper()
	m := csrfMetaContentRe.FindStringSubmatch(page)
	if m == nil {
		t.Fatal("csrf-token meta not found")
	}
	return m[1]
}

func TestIssuesCopy(t *testing.T) {
	ts, d, _ := newIssuesWriteServer(t)
	c := login(t, ts, "admin", "admin")
	res, _ := projSubmit(t, c, ts, http.MethodPost, "/projects/ecookbook/issues", url.Values{
		"copy_from": {"1"}, "link_copy": {"1"}, "issue[tracker_id]": {"1"}, "issue[subject]": {"Copied"},
		"issue[priority_id]": {"4"},
	}, false)
	if res.StatusCode != http.StatusFound {
		t.Fatalf("copy: status %d", res.StatusCode)
	}
	if got := countRows(t, d, `SELECT COUNT(*) FROM issue_relations WHERE issue_from_id = 1 AND issue_to_id = 15 AND relation_type = 'copied_to'`); got != 1 {
		t.Errorf("copied_to relations = %d", got)
	}
	// コピー元が見えなければ 404
	res, _ = get(t, c, ts.URL+"/projects/ecookbook/issues/999/copy")
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("copy unknown: status %d", res.StatusCode)
	}
}

func TestIssuesUpdate(t *testing.T) {
	ts, d, n := newIssuesWriteServer(t)
	ctx := context.Background()
	c := login(t, ts, "jsmith", "jsmith")

	res, _ := projSubmit(t, c, ts, http.MethodPut, "/issues/2", url.Values{
		"issue[subject]": {"Updated"}, "issue[status_id]": {"2"}, "issue[notes]": {"Some notes"},
		"issue[lock_version]": {"3"}, "prev_issue_id": {"1"}, "next_issue_id": {"3"},
	}, false)
	if res.StatusCode != http.StatusFound || !strings.HasSuffix(res.Header.Get("Location"), "/issues/2") {
		t.Fatalf("update: status %d location %q", res.StatusCode, res.Header.Get("Location"))
	}
	var lock int
	if err := d.Get(ctx, &lock, `SELECT lock_version FROM issues WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	if lock != 4 {
		t.Errorf("lock_version = %d", lock)
	}
	var j struct {
		ID    int64  `db:"id"`
		Notes string `db:"notes"`
	}
	if err := d.Get(ctx, &j, `SELECT id, notes FROM issue_journals WHERE issue_id = 2 ORDER BY id DESC LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	if j.Notes != "Some notes" {
		t.Errorf("journal notes = %q", j.Notes)
	}
	if got := countRows(t, d, `SELECT COUNT(*) FROM issue_journal_details WHERE journal_id = ? AND prop_key = 'subject' AND value = 'Updated'`, j.ID); got != 1 {
		t.Errorf("journal details = %d", got)
	}
	ns := n.take()
	if len(ns) != 1 || ns[0].Event != issues.NotifyIssueEdit || ns[0].JournalID != j.ID {
		t.Fatalf("notifications = %+v", ns)
	}
	// 前後のチケットは flash で詳細画面に渡る
	_, body := get(t, c, ts.URL+"/issues/2")
	if !strings.Contains(body, "Successful update.") || !strings.Contains(body, `href="/issues/3"`) {
		t.Errorf("show after update: flash or next link missing")
	}

	// 古い lock_version → 競合（保存しない）
	res, body = projSubmit(t, c, ts, http.MethodPut, "/issues/2", url.Values{
		"issue[subject]": {"Stale"}, "issue[notes]": {"stale note"}, "issue[lock_version]": {"3"}, "last_journal_id": {"1"},
	}, false)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `<div class="conflict">`) || !strings.Contains(body, `value="add_notes"`) {
		t.Errorf("stale: status %d", res.StatusCode)
	}
	if got := countRows(t, d, `SELECT COUNT(*) FROM issues WHERE id = 2 AND subject = 'Updated'`); got != 1 {
		t.Error("stale update was saved")
	}
	// add_notes で注記だけ追加
	res, _ = projSubmit(t, c, ts, http.MethodPut, "/issues/2", url.Values{
		"issue[subject]": {"Stale"}, "issue[notes]": {"only notes"}, "issue[lock_version]": {"3"}, "conflict_resolution": {"add_notes"},
	}, false)
	if res.StatusCode != http.StatusFound {
		t.Errorf("add_notes: status %d", res.StatusCode)
	}
	if got := countRows(t, d, `SELECT COUNT(*) FROM issue_journals WHERE issue_id = 2 AND notes = 'only notes'`); got != 1 {
		t.Error("add_notes journal missing")
	}
	if got := countRows(t, d, `SELECT COUNT(*) FROM issues WHERE id = 2 AND subject = 'Updated'`); got != 1 {
		t.Error("add_notes changed the subject")
	}
	n.take()

	// 時間の記録
	res, _ = projSubmit(t, c, ts, http.MethodPut, "/issues/3", url.Values{
		"time_entry[hours]": {"1:30"}, "time_entry[activity_id]": {"9"}, "time_entry[comments]": {"work"},
	}, false)
	if res.StatusCode != http.StatusFound {
		t.Fatalf("log time: status %d", res.StatusCode)
	}
	var te struct {
		Hours    float64 `db:"hours"`
		UserID   int64   `db:"user_id"`
		Activity int64   `db:"activity_id"`
		SpentOn  string  `db:"spent_on"`
	}
	if err := d.Get(ctx, &te, `SELECT hours, user_id, activity_id, spent_on FROM time_entries WHERE issue_id = 3 ORDER BY id DESC LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	if te.Hours != 1.5 || te.UserID != 2 || te.Activity != 9 || !strings.HasPrefix(te.SpentOn, "2026-01-15") {
		t.Errorf("time entry = %+v", te)
	}
	// 変更が無ければジャーナル・通知は無く、「更新しました」も出ない
	if ns := n.take(); len(ns) != 0 {
		t.Errorf("notifications for time only = %+v", ns)
	}

	// 不正な作業時間 → 何も保存しない
	before := countRows(t, d, `SELECT COUNT(*) FROM time_entries`)
	res, body = projSubmit(t, c, ts, http.MethodPut, "/issues/3", url.Values{
		"issue[notes]": {"bad time"}, "time_entry[hours]": {"2z"}, "time_entry[activity_id]": {""},
	}, false)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "<li>Log time is invalid</li>") || !strings.Contains(body, "<li>Activity cannot be blank</li>") {
		t.Errorf("invalid time entry: status %d", res.StatusCode)
	}
	if countRows(t, d, `SELECT COUNT(*) FROM time_entries`) != before ||
		countRows(t, d, `SELECT COUNT(*) FROM issue_journals WHERE notes = 'bad time'`) != 0 {
		t.Error("invalid time entry saved something")
	}

	// 非公開の注記
	res, _ = projSubmit(t, c, ts, http.MethodPatch, "/issues/1", url.Values{
		"issue[notes]": {"private"}, "issue[private_notes]": {"1"},
	}, false)
	if res.StatusCode != http.StatusFound {
		t.Errorf("private notes: status %d", res.StatusCode)
	}
	if got := countRows(t, d, `SELECT COUNT(*) FROM issue_journals WHERE issue_id = 1 AND notes = 'private' AND private_notes = ?`, true); got != 1 {
		t.Error("private journal missing")
	}
}

func TestIssuesEditForm(t *testing.T) {
	ts, _, _ := newIssuesWriteServer(t)
	c := login(t, ts, "jsmith", "jsmith")
	res, body := get(t, c, ts.URL+"/issues/1/edit?issue[status_id]=2&issue[notes]=draft")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("edit: status %d", res.StatusCode)
	}
	if !strings.Contains(body, `<option selected="selected" value="2">Assigned</option>`) || !strings.Contains(body, ">\ndraft</textarea>") {
		t.Error("edit form does not reflect params")
	}
	// update_form（XHR）
	res, body = projSubmit(t, c, ts, http.MethodPatch, "/issues/1/edit.js", url.Values{
		"form_update_triggered_by": {"issue_tracker_id"}, "issue[tracker_id]": {"2"},
	}, true)
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(body, "replaceIssueFormWith('") {
		t.Errorf("edit.js: status %d", res.StatusCode)
	}
	res, body = projSubmit(t, c, ts, http.MethodPost, "/projects/ecookbook/issues/new.js", url.Values{
		"form_update_triggered_by": {"issue_project_id"}, "issue[project_id]": {"3"},
	}, true)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `$("#watchers_form_container").html(`) {
		t.Errorf("new.js: status %d", res.StatusCode)
	}
}

// カテゴリの既定担当者の名前は .html() に渡るので HTML エスケープしてから JS エスケープする（XSS）。
func TestIssuesNewJSCategoryAssigneeEscaped(t *testing.T) {
	ts, d, _ := newIssuesWriteServer(t)
	// カテゴリ 1 の担当者は jsmith（id 2）
	if _, err := d.Exec(context.Background(), `UPDATE principals SET lastname = '<img src=x onerror=alert(1)>' WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	c := login(t, ts, "jsmith", "jsmith")
	res, body := projSubmit(t, c, ts, http.MethodPost, "/projects/ecookbook/issues/new.js", url.Values{
		"form_update_triggered_by": {"issue_category_id"}, "issue[tracker_id]": {"1"}, "issue[category_id]": {"1"},
	}, true)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("new.js: status %d", res.StatusCode)
	}
	if strings.Contains(body, "<img src=x") || !strings.Contains(body, "'John &lt;img src=x onerror=alert(1)&gt;'") {
		t.Errorf("assignee name not escaped:\n%s", body[strings.LastIndex(body, ".html("):])
	}
}

func TestIssuesAPIWrite(t *testing.T) {
	ts, d, n := newIssuesWriteServer(t)
	res, body := issuesAPIRequest(t, ts, http.MethodPost, "/issues.json", "jsmith", "jsmith", "application/json",
		`{"issue": {"project_id": 1, "tracker_id": 1, "subject": "API", "priority_id": 5, "custom_fields": [{"id": 2, "value": "x"}]}}`)
	if res.StatusCode != http.StatusCreated || !strings.HasSuffix(res.Header.Get("Location"), "/issues/15") ||
		!strings.Contains(body, `"subject":"API"`) {
		t.Fatalf("api create: status %d location %q body %s", res.StatusCode, res.Header.Get("Location"), body)
	}
	if got := countRows(t, d, `SELECT COUNT(*) FROM custom_values WHERE customized_id = 15 AND custom_field_id = 2 AND value = 'x'`); got != 1 {
		t.Errorf("api custom value rows = %d", got)
	}
	res, body = issuesAPIRequest(t, ts, http.MethodPost, "/issues.json", "jsmith", "jsmith", "application/json",
		`{"issue": {"project_id": 1, "subject": ""}}`)
	if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "Subject cannot be blank") {
		t.Errorf("api invalid: status %d body %s", res.StatusCode, body)
	}
	res, _ = issuesAPIRequest(t, ts, http.MethodPut, "/issues/15.json", "jsmith", "jsmith", "application/json",
		`{"issue": {"notes": "api note", "status_id": 2}}`)
	if res.StatusCode != http.StatusNoContent {
		t.Errorf("api update: status %d", res.StatusCode)
	}
	if got := countRows(t, d, `SELECT COUNT(*) FROM issues WHERE id = 15 AND status_id = 2`); got != 1 {
		t.Error("api update not saved")
	}
	ns := n.take()
	if len(ns) != 2 || ns[0].Event != issues.NotifyIssueAdd || ns[1].Event != issues.NotifyIssueEdit {
		t.Errorf("api notifications = %+v", ns)
	}
}
