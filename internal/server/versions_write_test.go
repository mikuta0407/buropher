// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// このファイルはバージョンの作成・更新・削除・close_completed の振る舞いテスト（DB の変化を確認する）。

// versionsPost は CSRF トークンを取ってからフォームを送る（_method で PATCH / PUT / DELETE も送る）。
func versionsPost(t *testing.T, c *http.Client, base, tokenPage, path string, form url.Values) (*http.Response, string) {
	t.Helper()
	_, body := get(t, c, base+tokenPage)
	if form == nil {
		form = url.Values{}
	}
	form.Set("authenticity_token", csrfToken(t, body))
	return post(t, c, base+path, form)
}

func TestVersionsWriteBehavior(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	admin := login(t, ts, "admin", "admin")

	// 作成（不正）: フォームを再表示
	res, body := versionsPost(t, admin, ts.URL, "/projects/ecookbook/versions/new", "/projects/ecookbook/versions",
		url.Values{"version[name]": {""}, "version[effective_date]": {"2026-02-30"}})
	if res.StatusCode != 200 || !strings.Contains(body, `id='errorExplanation'`) ||
		!strings.Contains(body, "Name cannot be blank") || !strings.Contains(body, "Due date is not a valid date") {
		t.Fatalf("invalid create: %d %s", res.StatusCode, body[strings.Index(body, "<div id=\"content\">"):])
	}

	// 作成: 既定バージョンに設定し、プロジェクト設定へ戻る
	res, _ = versionsPost(t, admin, ts.URL, "/projects/ecookbook/versions/new", "/projects/ecookbook/versions",
		url.Values{"version[name]": {"3.0"}, "version[sharing]": {"system"}, "version[default_project_version]": {"1"},
			"version[effective_date]": {"2026-03-01"}})
	if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/projects/ecookbook/settings/versions") {
		t.Fatalf("create: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	var id int64
	var sharing, date string
	if err := d.QueryRow(ctx, `SELECT id, sharing, effective_date FROM versions WHERE name = '3.0' AND project_id = 1`).Scan(&id, &sharing, &date); err != nil {
		t.Fatal(err)
	}
	if sharing != "system" || date != "2026-03-01" {
		t.Errorf("created version: %s %s", sharing, date)
	}
	var def int64
	if err := d.QueryRow(ctx, `SELECT default_version_id FROM projects WHERE id = 1`).Scan(&def); err != nil || def != id {
		t.Errorf("default version: %d %v", def, err)
	}

	// jsmith（管理者でない）は system 共有を指定できない（無視される）
	jsmith := login(t, ts, "jsmith", "jsmith")
	res, _ = versionsPost(t, jsmith, ts.URL, "/projects/ecookbook/versions/new", "/projects/ecookbook/versions",
		url.Values{"version[name]": {"4.0"}, "version[sharing]": {"system"}})
	if res.StatusCode != 302 {
		t.Fatalf("jsmith create: %d", res.StatusCode)
	}
	if err := d.QueryRow(ctx, `SELECT sharing FROM versions WHERE name = '4.0'`).Scan(&sharing); err != nil || sharing != "none" {
		t.Errorf("jsmith sharing: %s %v", sharing, err)
	}

	// 権限のないユーザーは 403
	dlopper := login(t, ts, "someone", "foo")
	res, _ = versionsPost(t, dlopper, ts.URL, "/projects/ecookbook/roadmap", "/projects/ecookbook/versions",
		url.Values{"version[name]": {"x"}})
	if res.StatusCode != 403 {
		t.Errorf("dlopper create: %d", res.StatusCode)
	}

	// 更新（back_url に戻る）
	res, _ = versionsPost(t, admin, ts.URL, "/versions/3/edit", "/versions/3",
		url.Values{"_method": {"patch"}, "version[name]": {"2.0b"}, "version[status]": {"locked"}, "back_url": {"/projects/ecookbook/roadmap"}})
	if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/projects/ecookbook/roadmap") {
		t.Fatalf("update: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	var name, status string
	if err := d.QueryRow(ctx, `SELECT name, status FROM versions WHERE id = 3`).Scan(&name, &status); err != nil || name != "2.0b" || status != "locked" {
		t.Errorf("updated: %s %s %v", name, status, err)
	}

	// close_completed: 期日を過ぎて未完了チケットの無いバージョンを閉じる
	if _, err := d.Exec(ctx, `UPDATE versions SET effective_date = '2026-01-01' WHERE id = 3`); err != nil {
		t.Fatal(err)
	}
	res, _ = versionsPost(t, admin, ts.URL, "/projects/ecookbook/roadmap", "/projects/ecookbook/versions/close_completed",
		url.Values{"_method": {"put"}})
	if res.StatusCode != 302 {
		t.Fatalf("close_completed: %d", res.StatusCode)
	}
	if err := d.QueryRow(ctx, `SELECT status FROM versions WHERE id = 3`).Scan(&status); err != nil || status != "closed" {
		t.Errorf("close_completed status: %s %v", status, err)
	}
	if err := d.QueryRow(ctx, `SELECT status FROM versions WHERE id = 2`).Scan(&status); err != nil || status != "locked" {
		t.Errorf("version 2 must stay locked: %s %v", status, err)
	}

	// 削除: チケットのあるバージョンは削除できない
	res, _ = versionsPost(t, admin, ts.URL, "/versions/2", "/versions/2", url.Values{"_method": {"delete"}})
	if res.StatusCode != 302 {
		t.Fatalf("destroy in use: %d", res.StatusCode)
	}
	_, body = get(t, admin, ts.URL+"/projects/ecookbook/roadmap")
	if !strings.Contains(body, "Unable to delete version") {
		t.Error("flash error not shown")
	}
	res, _ = versionsPost(t, admin, ts.URL, "/versions/3/edit", "/versions/3", url.Values{"_method": {"delete"}})
	if res.StatusCode != 302 {
		t.Fatalf("destroy: %d", res.StatusCode)
	}
	var n int
	if err := d.QueryRow(ctx, `SELECT COUNT(*) FROM versions WHERE id = 3`).Scan(&n); err != nil || n != 0 {
		t.Errorf("version 3 not deleted: %d %v", n, err)
	}

	// API: 作成（201 + Location）・不正（422）・削除（204）
	res, body = apiRequest(t, "POST", ts.URL+"/projects/ecookbook/versions.json", "application/json",
		`{"version":{"name":"API","due_date":"2026-04-01"}}`)
	if res.StatusCode != 201 || !strings.Contains(res.Header.Get("Location"), "/versions/") || !strings.Contains(body, `"due_date":"2026-04-01"`) {
		t.Fatalf("api create: %d %s", res.StatusCode, body)
	}
	res, body = apiRequest(t, "POST", ts.URL+"/projects/ecookbook/versions.json", "application/json", `{"version":{"name":"API"}}`)
	if res.StatusCode != 422 || !strings.Contains(body, "Name has already been taken") {
		t.Errorf("api invalid: %d %s", res.StatusCode, body)
	}
	if err := d.QueryRow(ctx, `SELECT id FROM versions WHERE name = 'API'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if res, _ = apiRequest(t, "DELETE", ts.URL+"/versions/2.json", "", ""); res.StatusCode != 422 {
		t.Errorf("api destroy in use: %d", res.StatusCode)
	}
	res, _ = apiRequest(t, "DELETE", ts.URL+"/versions/"+strconv.FormatInt(id, 10)+".json", "", "")
	if res.StatusCode != 204 {
		t.Errorf("api destroy: %d", res.StatusCode)
	}
}
