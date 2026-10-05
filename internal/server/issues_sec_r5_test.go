// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
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

// チケットの CSV インポートは import_issues の無いプロジェクトに取り込まず、そこにカテゴリ・バージョンも作らない
// （チケットのプロジェクトは import.project に揃える）。
func TestIssueImportProjectWithoutImportPermission(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	// jsmith は project 2 で Developer（add_issues あり、import_issues なし）。カテゴリ・バージョンの管理も外す
	if _, err := d.Exec(ctx, `DELETE FROM role_permissions WHERE role_id = 2 AND permission IN ('manage_categories', 'manage_versions', 'import_issues')`); err != nil {
		t.Fatal(err)
	}
	jsmith := login(t, ts, "jsmith", "jsmith")
	v0 := queryInt(t, d, `SELECT MAX(id) FROM versions`)
	c0 := queryInt(t, d, `SELECT MAX(id) FROM issue_categories`)
	_, ids := runImport(t, jsmith, ts, d, "IssueImport", "import_issues.csv", "issues", utf8Semicolon,
		merge(issueImportMapping, map[string]string{"project_id": "2", "fixed_version": "9", "create_versions": "1",
			"category": "10", "create_categories": "1"}))
	if len(ids) != 3 {
		t.Fatalf("issues = %d", len(ids))
	}
	for _, id := range ids {
		if p := queryInt(t, d, `SELECT project_id FROM issues WHERE id = ?`, id); p == 2 {
			t.Errorf("issue %d imported into a project without import_issues", id)
		}
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM versions WHERE id > ? AND project_id = 2`, v0); n != 0 {
		t.Errorf("version created in project 2 without manage_versions")
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM issue_categories WHERE id > ? AND project_id = 2`, c0); n != 0 {
		t.Errorf("category created in project 2 without manage_categories")
	}
}

// 既存の工数は log_time の無いプロジェクトへ移せない（update / bulk_update）。
func TestTimeEntryMoveToProjectWithoutLogTime(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	enableTimeTracking := func(d *db.DB) {
		if _, err := d.Exec(ctx, `INSERT INTO project_modules (project_id, name) VALUES (2, 'time_tracking')`); err != nil {
			t.Fatal(err)
		}
	}
	// jsmith は project 2 で Developer。工数管理を有効にして log_time を外す
	enableTimeTracking(d)
	if _, err := d.Exec(ctx, `DELETE FROM role_permissions WHERE role_id = 2 AND permission = 'log_time'`); err != nil {
		t.Fatal(err)
	}
	res := apiCall(t, ts, http.MethodPut, "/time_entries/1.json", "", `{"time_entry":{"project_id":2,"issue_id":""}}`, apiCreds("jsmith"))
	if res.Status != http.StatusUnprocessableEntity {
		t.Errorf("update: status %d %s", res.Status, res.Body)
	}
	c := login(t, ts, "jsmith", "jsmith")
	projSubmit(t, c, ts, http.MethodPost, "/time_entries/bulk_update",
		url.Values{"ids[]": {"1"}, "time_entry[project_id]": {"2"}, "time_entry[issue_id]": {"none"}}, false)
	if p := queryInt(t, d, `SELECT project_id FROM time_entries WHERE id = 1`); p != 1 {
		t.Errorf("time entry moved to project %d", p)
	}
	// log_time のあるプロジェクトへは従来どおり移せる
	ts, d = newFixtureServer(t)
	enableTimeTracking(d)
	res = apiCall(t, ts, http.MethodPut, "/time_entries/1.json", "", `{"time_entry":{"project_id":2,"issue_id":""}}`, apiCreds("jsmith"))
	res.expectStatus(t, http.StatusNoContent)
}

// 管理画面のクエリ（UserQuery / ProjectAdminQuery）は管理者以外は作れない。
func TestAdminQueryKindsRequireAdmin(t *testing.T) {
	ts, _ := newFixtureServer(t)
	c := login(t, ts, "jsmith", "jsmith")
	for _, typ := range []string{"UserQuery", "ProjectAdminQuery"} {
		res, body := get(t, c, ts.URL+"/queries/new?type="+typ+"&set_filter=1&f[]=auth_source_id&op[auth_source_id]=%3D&v[auth_source_id][]=1")
		if res.StatusCode != http.StatusForbidden || strings.Contains(body, "LDAP test server") {
			t.Errorf("new %s as non-admin: %d", typ, res.StatusCode)
		}
		res, _ = projSubmit(t, c, ts, http.MethodPost, "/queries", url.Values{"type": {typ}, "query[name]": {"x"}}, false)
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("create %s as non-admin: %d", typ, res.StatusCode)
		}
	}
	res, _ := get(t, login(t, ts, "admin", "admin"), ts.URL+"/queries/new?type=UserQuery")
	if res.StatusCode != http.StatusOK {
		t.Errorf("admin new UserQuery: %d", res.StatusCode)
	}
}

// 工数のコンテキストメニューは見えない工数を扱わない（Redmine 6.1.3 #44109）。
func TestTimeEntryContextMenuHidesInvisible(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	// dlopper（project 1 の Developer）は自分の工数だけ見える
	if _, err := d.Exec(ctx, `UPDATE roles SET time_entries_visibility = 'own' WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	c := login(t, ts, "dlopper", "foo")
	res, body := get(t, c, ts.URL+"/time_entries/context_menu?ids[]=1")
	if res.StatusCode != http.StatusNotFound || strings.Contains(body, "Design") {
		t.Errorf("context menu of invisible time entry: %d %.200s", res.StatusCode, body)
	}
	res, _ = get(t, login(t, ts, "jsmith", "jsmith"), ts.URL+"/time_entries/context_menu?ids[]=1")
	if res.StatusCode != http.StatusOK {
		t.Errorf("context menu of visible time entry: %d", res.StatusCode)
	}
}

// チケットの CSV インポートの relation_* 列で、見えないチケットに関連を張れない。
func TestIssueImportRelationToInvisibleIssue(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	if _, err := d.Exec(ctx, `UPDATE issues SET is_private = ?, author_id = 1, assigned_to_id = NULL WHERE id = 2`, true); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(ctx, `UPDATE roles SET issues_visibility = 'default' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	csv := filepath.Join(t.TempDir(), "rel.csv")
	if err := os.WriteFile(csv, []byte("subject;related\nImported A;#2\nImported B;#3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	jsmith := login(t, ts, "jsmith", "jsmith")
	_, ids := runImport(t, jsmith, ts, d, "IssueImport", csv, "issues", utf8Semicolon,
		map[string]string{"project_id": "1", "tracker": "value:1", "subject": "0", "relation_relates": "1"})
	if len(ids) != 2 {
		t.Fatalf("issues = %d", len(ids))
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM issue_relations WHERE issue_from_id = ? OR issue_to_id = ?`, ids[0], ids[0]); n != 0 {
		t.Errorf("relation to invisible issue created")
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM issue_relations WHERE issue_from_id = ? OR issue_to_id = ?`, ids[1], ids[1]); n != 1 {
		t.Errorf("relation to visible issue missing: %d", n)
	}
}

// 新しいチケットのフォーム更新（カテゴリ変更）は、他のプロジェクトのカテゴリの担当者名を返さない。
func TestIssueNewCategoryAssigneeOtherProject(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	// category 3 は非公開プロジェクト 2 のカテゴリ。担当者を miscuser8（User Misc）にする
	if _, err := d.Exec(ctx, `UPDATE issue_categories SET assigned_to_id = 8 WHERE id = 3`); err != nil {
		t.Fatal(err)
	}
	c := login(t, ts, "dlopper", "foo")
	xhr := func(cat string) string {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/projects/ecookbook/issues/new.js?form_update_triggered_by=issue_category_id&issue[tracker_id]=1&issue[category_id]="+cat, nil)
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := readUnbranded(res.Body)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status %d", res.StatusCode)
		}
		return string(b)
	}
	if body := xhr("3"); strings.Contains(body, "User Misc") {
		t.Errorf("assignee of another project's category leaked")
	}
	if body := xhr("1"); !strings.Contains(body, ".html(\n      'John Smith')") && !strings.Contains(body, "'John Smith');") {
		t.Errorf("assignee of the project's category missing: %s", body[strings.LastIndex(body, "removeAttr"):])
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
