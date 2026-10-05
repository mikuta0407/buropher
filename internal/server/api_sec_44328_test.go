// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

// Redmine 7.0.1 (#44328) の移植: issue_categories#index/show は view_issues で読める。
// projects API の include=trackers/issue_categories/issue_custom_fields は view_issues、
// include=time_entry_activities は view_time_entries があるときだけ返す。

import (
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
)

func removeRolePermission(t *testing.T, d *db.DB, roleCond, perm string) {
	t.Helper()
	mastersExec(t, d, `DELETE FROM role_permissions WHERE permission = ? AND role_id IN (SELECT id FROM roles WHERE `+roleCond+`)`, perm)
}

func TestAPIIssueCategoriesViewIssues44328(t *testing.T) {
	ts, d := newFixtureServer(t)
	removeRolePermission(t, d, "id = 1", "manage_categories")

	t.Run("GET /projects/:project_id/issue_categories.xml should be allowed with view_issues permission", func(t *testing.T) {
		r := apiGet(t, ts, "/projects/1/issue_categories.xml", apiCreds("jsmith"))
		r.expectStatus(t, 200)
		if len(xmlSelect(t, r.XML(t), `issue_categories issue_category id:contains("2")`)) == 0 {
			t.Error("category 2 missing")
		}
	})
	t.Run("GET /issue_categories/:id.xml should be allowed with view_issues permission", func(t *testing.T) {
		r := apiGet(t, ts, "/issue_categories/2.xml", apiCreds("jsmith"))
		r.expectStatus(t, 200)
		assertXMLText(t, r.XML(t), "issue_category id", "2")
	})
	t.Run("POST /projects/:project_id/issue_categories.xml should be denied without manage_categories permission", func(t *testing.T) {
		before := mastersInt(t, d, `SELECT COUNT(*) FROM issue_categories`)
		r := mastersForm(t, ts, "POST", "/projects/1/issue_categories.xml", "issue_category[name]=API", apiCreds("jsmith"))
		r.expectStatus(t, 403)
		if n := mastersInt(t, d, `SELECT COUNT(*) FROM issue_categories`); n != before {
			t.Error("category created")
		}
	})
	t.Run("PUT /issue_categories/:id.xml should be denied without manage_categories permission", func(t *testing.T) {
		r := mastersForm(t, ts, "PUT", "/issue_categories/2.xml", "issue_category[name]=API", apiCreds("jsmith"))
		r.expectStatus(t, 403)
	})
}

func TestAPIProjectsIncludesPermissions44328(t *testing.T) {
	const anon = "builtin = 2"
	cases := []struct {
		path, perm, sel string
	}{
		{"/projects.xml?include=issue_categories", "view_issues", "issue_categories"},
		{"/projects.xml?include=trackers", "view_issues", "trackers"},
		{"/projects.xml?include=time_entry_activities", "view_time_entries", "time_entry_activities"},
		{"/projects.xml?include=issue_custom_fields", "view_issues", "issue_custom_fields"},
		{"/projects/1.xml?include=issue_categories", "view_issues", "issue_categories"},
		{"/projects/1.xml?include=time_entry_activities", "view_time_entries", "time_entry_activities"},
		{"/projects/1.xml?include=trackers", "view_issues", "trackers"},
		{"/projects/1.xml?include=issue_custom_fields", "view_issues", "issue_custom_fields"},
	}
	ts, _ := newFixtureServer(t)
	for _, tc := range cases {
		t.Run(tc.path+" with permission", func(t *testing.T) {
			r := apiGet(t, ts, tc.path)
			r.expectStatus(t, 200)
			if len(xmlSelect(t, r.XML(t), tc.sel)) == 0 {
				t.Errorf("%s missing", tc.sel)
			}
		})
	}
	ts2, d2 := newFixtureServer(t)
	removeRolePermission(t, d2, anon, "view_issues")
	removeRolePermission(t, d2, anon, "view_time_entries")
	for _, tc := range cases {
		t.Run(tc.path+" without "+tc.perm, func(t *testing.T) {
			r := apiGet(t, ts2, tc.path)
			r.expectStatus(t, 200)
			assertXMLCount(t, r.XML(t), tc.sel, 0)
		})
	}
}
