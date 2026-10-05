// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

// Redmine 6.1.5 / 7.0.2 のセキュリティ修正の移植（test/integration/api_test/groups_test.rb ほか）。

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// #44468: GET /groups/:id?include=memberships は、閲覧者がメンバーを見られる（view_members）プロジェクトの
// メンバーシップだけを出す（非公開プロジェクトの名前とロールが漏れない）。
func TestAPIGroupsShowMembershipsOnlyVisibleProjects(t *testing.T) {
	ts, _ := newFixtureServer(t)
	// グループ 10 は非公開プロジェクト 5 のメンバー（dlopper は見られない）。プロジェクト 1 にも加える
	res := apiCall(t, ts, http.MethodPost, "/projects/1/memberships.json", "",
		`{"membership":{"user_id":10,"role_ids":[2]}}`, apiCreds("admin"))
	res.expectStatus(t, http.StatusCreated)

	res = apiGet(t, ts, "/groups/10.xml?include=memberships", apiCreds("admin"))
	res.expectStatus(t, http.StatusOK)
	assertXMLCount(t, res.XML(t), "group memberships membership", 2)

	res = apiGet(t, ts, "/groups/10.xml?include=memberships", apiCreds("dlopper"))
	res.expectStatus(t, http.StatusOK)
	doc := res.XML(t)
	assertXMLCount(t, doc, "group memberships membership", 1)
	assertXMLCount(t, doc, `group memberships membership project[id="1"]`, 1)
	assertXMLCount(t, doc, `group memberships membership project[id="5"]`, 0)

	res = apiGet(t, ts, "/groups/10.json?include=memberships", apiCreds("dlopper"))
	res.expectStatus(t, http.StatusOK)
	if ids := membershipProjectIDs(t, res, "group.memberships"); !slices.Equal(ids, []string{"1"}) {
		t.Errorf("memberships project ids = %v: %s", ids, res.Body)
	}
}

// #44468: users#show のメンバーシップも Member.visible（view_members）で絞る。
// アーカイブされたプロジェクトのメンバーシップは管理者にも出さない。
func TestAPIUsersShowMembershipsVisibleScope(t *testing.T) {
	ts, d := newFixtureServer(t)
	res := apiGet(t, ts, "/users/2.json?include=memberships", apiCreds("dlopper"))
	res.expectStatus(t, http.StatusOK)
	ids := membershipProjectIDs(t, res, "user.memberships")
	if slices.Contains(ids, "5") || !slices.Contains(ids, "1") {
		t.Errorf("memberships project ids = %v: %s", ids, res.Body)
	}
	if _, err := d.Exec(t.Context(), `UPDATE projects SET status = 9 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	res = apiGet(t, ts, "/users/2.json?include=memberships", apiCreds("admin"))
	res.expectStatus(t, http.StatusOK)
	if ids := membershipProjectIDs(t, res, "user.memberships"); slices.Contains(ids, "1") {
		t.Errorf("archived project 1 listed: %s", res.Body)
	}
}

// #44467: GET /issues/:id?include=children は見えない子孫（非公開の子・孫、見えないプロジェクトの子）を出さない
// （test/integration/api_test/issues_test.rb "with subtasks should not include invisible descendants"）。
func TestAPIIssueChildrenExcludeInvisibleDescendants(t *testing.T) {
	ts, _ := newFixtureServer(t)
	create := func(project int, subject string, parent string, private bool) string {
		t.Helper()
		body := fmt.Sprintf(`{"issue":{"project_id":%d,"tracker_id":1,"subject":%q,"parent_issue_id":%s,"is_private":%t}}`, project, subject, parent, private)
		res := apiCall(t, ts, http.MethodPost, "/issues.json", "", body, apiCreds("admin"))
		res.expectStatus(t, http.StatusCreated)
		id, _ := jsonPath(res.JSON(t), "issue.id")
		return fmt.Sprint(id)
	}
	parent := create(1, "Parent", "null", false)
	child1 := create(1, "Child1", parent, false)
	create(1, "Child2", parent, false)
	create(1, "Child11", child1, false)
	create(1, "Private child", parent, true)
	create(1, "Private grandchild", child1, true)
	create(5, "Child in private project", parent, false)

	for _, path := range []string{"/issues/" + parent + ".json?include=children", "/issues/" + parent + ".xml?include=children"} {
		res := apiGet(t, ts, path, apiCreds("dlopper"))
		res.expectStatus(t, http.StatusOK)
		for _, leaked := range []string{"Private child", "Private grandchild", "Child in private project"} {
			if strings.Contains(res.Body, leaked) {
				t.Errorf("%s leaked %q: %s", path, leaked, res.Body)
			}
		}
		for _, want := range []string{"Child1", "Child2", "Child11"} {
			if !strings.Contains(res.Body, want) {
				t.Errorf("%s missing %q: %s", path, want, res.Body)
			}
		}
	}
}

// membershipProjectIDs は JSON の path にあるメンバーシップのプロジェクト id を並び順で返す。
func membershipProjectIDs(t *testing.T, res apiResp, path string) []string {
	t.Helper()
	ms, _ := jsonPath(res.JSON(t), path)
	arr, _ := ms.([]any)
	var ids []string
	for _, m := range arr {
		ids = append(ids, fmt.Sprint(m.(map[string]any)["project"].(map[string]any)["id"]))
	}
	return ids
}
