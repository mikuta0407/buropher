// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// 見えないチケット（非公開）の関連は、プロジェクトの権限があっても一覧・追加できない
// （7.0.1 #44309）。
func TestIssueRelationsInvisibleIssue(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	if _, err := d.Exec(ctx, `UPDATE issues SET is_private = ?, author_id = 1, assigned_to_id = NULL WHERE id = 2`, true); err != nil {
		t.Fatal(err)
	}
	res := apiGet(t, ts, "/issues/2/relations.json", apiCreds("dlopper"))
	if res.Status != http.StatusForbidden || strings.Contains(res.Body, "issue_to_id") {
		t.Errorf("relations of invisible issue: %d %s", res.Status, res.Body)
	}
	res = apiCall(t, ts, http.MethodPost, "/issues/2/relations.json", "",
		`{"relation":{"issue_to_id":7,"relation_type":"relates"}}`, apiCreds("dlopper"))
	if res.Status != http.StatusForbidden {
		t.Errorf("create relation on invisible issue: %d %s", res.Status, res.Body)
	}
	var n int
	if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM issue_relations WHERE issue_from_id = 2 AND issue_to_id = 7`); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("relation created on invisible issue")
	}
	// 見えるチケットは従来どおり
	res = apiGet(t, ts, "/issues/2/relations.json", apiCreds("jsmith"))
	res.expectStatus(t, http.StatusOK)
	if !strings.Contains(res.Body, `"issue_to_id":3`) {
		t.Errorf("visible relations missing: %s", res.Body)
	}
}

// プロジェクト・バージョンのファイルの一括ダウンロードは view_files が無ければ拒否する
// （個別のダウンロードと同じく attachment.visible? を確認する）。
func TestAttachmentsDownloadAllRequiresViewFiles(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	for kind, fn := range map[string]string{"project": "pfile.txt", "version": "vfile.txt"} {
		id := uploadAPI(t, ts.URL, "filename="+fn, "secret "+kind)
		if _, err := d.Exec(ctx, `UPDATE attachments SET container_kind = ?, container_id = 1 WHERE id = ?`, kind, id); err != nil {
			t.Fatal(err)
		}
	}
	c := login(t, ts, "jsmith", "jsmith")
	for _, path := range []string{"/attachments/projects/1/download", "/attachments/versions/1/download"} {
		res, _ := get(t, c, ts.URL+path)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s before disabling files: %d", path, res.StatusCode)
		}
	}
	if _, err := d.Exec(ctx, `DELETE FROM project_modules WHERE project_id = 1 AND name = 'files'`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/attachments/projects/1/download", "/attachments/versions/1/download"} {
		res, body := get(t, c, ts.URL+path)
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("%s without view_files: %d %.100q", path, res.StatusCode, body)
		}
	}
}

// チケット削除で工数を付け替える先に、見えないチケット（非公開）は指定できない。
func TestIssueDestroyReassignToInvisible(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	if _, err := d.Exec(ctx, `UPDATE issues SET is_private = ?, author_id = 1, assigned_to_id = NULL WHERE id = 2`, true); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(ctx, `UPDATE roles SET issues_visibility = 'default' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	res := apiCall(t, ts, http.MethodDelete, "/issues/1.json?todo=reassign&reassign_to_id=2", "", "", apiCreds("jsmith"))
	var n int
	if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM time_entries WHERE issue_id = 2`); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("time entries reassigned to invisible issue (status %d)", res.Status)
	}
	// 見えるチケットへは付け替えられる
	res = apiCall(t, ts, http.MethodDelete, "/issues/1.json?todo=reassign&reassign_to_id=3", "", "", apiCreds("jsmith"))
	res.expectStatus(t, http.StatusNoContent)
	if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM time_entries WHERE issue_id = 3`); err != nil {
		t.Fatal(err)
	}
	if n < 2 {
		t.Errorf("time entries not reassigned to visible issue: %d", n)
	}
}
