// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Redmine 7.0.1 #44310: inherit_members の変更にはメンバーの管理（manage_members）権限が必要
// （test_settings_of_subproject_should_not_show_inherit_members_checkbox、
// test_safe_attributes_inherit_members_for_new_record / _for_existing_record）。
func TestProjectInheritMembersRequiresManageMembers(t *testing.T) {
	ts, d := newFixtureServer(t)
	// jsmith は private-child（id 5、親は eCookbook）の Manager。Manager からメンバーの管理を外す
	mastersExec(t, d, `DELETE FROM role_permissions WHERE role_id = 1 AND permission = 'manage_members'`)
	// 子プロジェクトを作れるようにする（Manager は add_subprojects を持たない）
	mastersExec(t, d, `INSERT INTO role_permissions (role_id, permission, all_trackers, position) VALUES (1, 'add_subprojects', ?, 101)`, true)
	jsmith := login(t, ts, "jsmith", "jsmith")
	const checkbox = `name="project[inherit_members]"`

	_, body := get(t, jsmith, ts.URL+"/projects/private-child/settings")
	if !strings.Contains(body, `id="project_name"`) {
		t.Fatal("settings form not shown")
	}
	if strings.Contains(body, checkbox) {
		t.Error("inherit_members checkbox should not be shown without manage_members")
	}
	res, _ := projSubmit(t, jsmith, ts, http.MethodPatch, "/projects/private-child", url.Values{"project[inherit_members]": {"1"}}, false)
	expectRedirect(t, res, "/projects/private-child/settings")
	if v := queryInt(t, d, `SELECT COUNT(*) FROM projects WHERE id = 5 AND inherit_members = ?`, true); v != 0 {
		t.Error("inherit_members was changed without manage_members")
	}
	// API でも同じ
	res, _ = projectsAPIRequest(t, ts, http.MethodPut, "/projects/private-child.json", "jsmith", `{"project":{"inherit_members":true}}`)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("api update: %d", res.StatusCode)
	}
	if v := queryInt(t, d, `SELECT COUNT(*) FROM projects WHERE id = 5 AND inherit_members = ?`, true); v != 0 {
		t.Error("inherit_members was changed through the API without manage_members")
	}

	// 新規作成: 既定のメンバーロール（Manager）に manage_members が無ければ設定できない
	res, _ = projSubmit(t, jsmith, ts, http.MethodPost, "/projects", url.Values{
		"project[name]": {"Smith child"}, "project[identifier]": {"smith-child"}, "project[parent_id]": {"1"}, "project[inherit_members]": {"1"},
	}, false)
	expectRedirect(t, res, "/projects/smith-child/settings")
	if v := queryInt(t, d, `SELECT COUNT(*) FROM projects WHERE identifier = 'smith-child' AND inherit_members = ?`, true); v != 0 {
		t.Error("inherit_members was set on create without manage_members in the default member role")
	}

	// manage_members を戻すと変更できる
	mastersExec(t, d, `INSERT INTO role_permissions (role_id, permission, all_trackers, position) VALUES (1, 'manage_members', ?, 100)`, true)
	_, body = get(t, jsmith, ts.URL+"/projects/private-child/settings")
	if !strings.Contains(body, checkbox) {
		t.Error("inherit_members checkbox should be shown with manage_members")
	}
	res, _ = projSubmit(t, jsmith, ts, http.MethodPatch, "/projects/private-child", url.Values{"project[inherit_members]": {"1"}}, false)
	expectRedirect(t, res, "/projects/private-child/settings")
	if v := queryInt(t, d, `SELECT COUNT(*) FROM projects WHERE id = 5 AND inherit_members = ?`, true); v != 1 {
		t.Error("inherit_members should be changed with manage_members")
	}
	res, _ = projSubmit(t, jsmith, ts, http.MethodPost, "/projects", url.Values{
		"project[name]": {"Smith child 2"}, "project[identifier]": {"smith-child-2"}, "project[parent_id]": {"1"}, "project[inherit_members]": {"1"},
	}, false)
	expectRedirect(t, res, "/projects/smith-child-2/settings")
	if v := queryInt(t, d, `SELECT COUNT(*) FROM projects WHERE identifier = 'smith-child-2' AND inherit_members = ?`, true); v != 1 {
		t.Error("inherit_members should be set on create with manage_members in the default member role")
	}
}

// Redmine 6.1.3 #43910: 識別子 autocomplete・bulk_destroy は予約語（既存の識別子は変更しない限り検証しない）。
func TestProjectReservedIdentifiers(t *testing.T) {
	ts, d := newFixtureServer(t)
	// 予約語になる前に作られたプロジェクト
	mastersExec(t, d, `UPDATE projects SET identifier = 'autocomplete' WHERE id = 6`)
	admin := login(t, ts, "admin", "admin")
	for _, ident := range []string{"new", "autocomplete", "bulk_destroy"} {
		res, body := projSubmit(t, admin, ts, http.MethodPost, "/projects", url.Values{
			"project[name]": {"Reserved"}, "project[identifier]": {ident},
		}, false)
		if res.StatusCode != http.StatusOK || !strings.Contains(body, "Identifier is reserved") {
			t.Errorf("%s: status %d, want the exclusion error", ident, res.StatusCode)
		}
	}
	// 予約語になる前に作られたプロジェクトは識別子を変えずに更新できる
	res, body := projectsAPIRequest(t, ts, http.MethodPut, "/projects/6.json", "admin", `{"project":{"name":"Renamed child"}}`)
	if res.StatusCode != http.StatusNoContent {
		t.Errorf("update existing reserved identifier: %d %s", res.StatusCode, body)
	}
}
