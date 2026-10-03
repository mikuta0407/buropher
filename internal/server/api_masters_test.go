// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

// Redmine の test/integration/api_test/{trackers,issue_statuses,enumerations,roles,custom_fields}_test.rb の移植。

import (
	"encoding/json"
	"strconv"
	"testing"
)

// TestAPITrackers は trackers_test.rb。
func TestAPITrackers(t *testing.T) {
	t.Run("GET /trackers.xml should return trackers", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		// Tracker.find(2).update_attribute :core_fields, %w[assigned_to_id due_date]
		mastersExec(t, d, `UPDATE trackers SET disabled_core_fields = ? WHERE id = 2`,
			`["category_id","fixed_version_id","parent_issue_id","start_date","estimated_hours","done_ratio","description","priority_id"]`)
		r := apiGet(t, ts, "/trackers.xml")
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/xml")
		doc := r.XML(t)
		trs := xmlSelect(t, doc, `trackers[type=array] > tracker`)
		var found bool
		for _, tr := range trs {
			if nodeText(xmlSelect(t, tr, "id")[0]) != "2" {
				continue
			}
			found = true
			assertXMLText(t, tr, "name", "Feature request")
			assertXMLText(t, tr, "description", "Description for Feature request tracker")
			assertXMLCount(t, tr, "enabled_standard_fields[type=array] > field", 2)
			fs := xmlSelect(t, tr, "enabled_standard_fields > field")
			if len(fs) == 2 && (nodeText(fs[0]) != "assigned_to_id" || nodeText(fs[1]) != "due_date") {
				t.Errorf("fields = %s, %s", nodeText(fs[0]), nodeText(fs[1]))
			}
		}
		if !found {
			t.Error("tracker 2 not found")
		}
	})
}

// TestAPIIssueStatuses は issue_statuses_test.rb。
func TestAPIIssueStatuses(t *testing.T) {
	t.Run("GET /issue_statuses.xml should return issue statuses", func(t *testing.T) {
		ts, _ := newFixtureServer(t)
		r := apiGet(t, ts, "/issue_statuses.xml")
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/xml")
		doc := r.XML(t)
		st := xmlSelect(t, doc, `issue_statuses[type=array] > issue_status:nth-of-type(2)`)
		if len(st) != 1 {
			t.Fatal("issue_status 2 not found")
		}
		assertXMLText(t, st[0], "id", "2")
		assertXMLText(t, st[0], "name", "Assigned")
		assertXMLText(t, st[0], "is_closed", "false")
		assertXMLText(t, st[0], "description", "Description for Assigned issue status")
	})
}

// TestAPIEnumerations は enumerations_test.rb。
func TestAPIEnumerations(t *testing.T) {
	ts, _ := newFixtureServer(t)
	t.Run("GET /enumerations/issue_priorities.xml should return priorities", func(t *testing.T) {
		r := apiGet(t, ts, "/enumerations/issue_priorities.xml")
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/xml")
		doc := r.XML(t)
		p3 := xmlSelect(t, doc, `issue_priorities[type=array] > issue_priority:nth-of-type(3)`)
		p6 := xmlSelect(t, doc, `issue_priorities[type=array] > issue_priority:nth-of-type(6)`)
		if len(p3) != 1 || len(p6) != 1 {
			t.Fatalf("priorities: %s", r.Body)
		}
		assertXMLText(t, p3[0], "id", "6")
		assertXMLText(t, p3[0], "name", "High")
		assertXMLText(t, p3[0], "active", "true")
		assertXMLText(t, p6[0], "id", "15")
		assertXMLText(t, p6[0], "name", "Inactive Priority")
		assertXMLText(t, p6[0], "active", "false")
	})
	t.Run("GET /enumerations/invalid_subclass.xml should return 404", func(t *testing.T) {
		r := apiGet(t, ts, "/enumerations/invalid_subclass.xml")
		r.expectStatus(t, 404)
		mastersMedia(t, r, "application/xml")
	})
}

// TestAPIRoles は roles_test.rb。
func TestAPIRoles(t *testing.T) {
	ts, d := newFixtureServer(t)
	t.Run("GET /roles.xml should return the roles", func(t *testing.T) {
		r := apiGet(t, ts, "/roles.xml")
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/xml")
		doc := r.XML(t)
		assertXMLCount(t, doc, "roles role", 3)
		roles := xmlSelect(t, doc, `roles[type=array] > role`)
		var found bool
		for _, ro := range roles {
			if nodeText(xmlSelect(t, ro, "id")[0]) == "2" {
				found = true
				assertXMLText(t, ro, "name", "Developer")
			}
		}
		if !found {
			t.Error("role 2 not found")
		}
	})
	t.Run("GET /roles.json should return the roles", func(t *testing.T) {
		r := apiGet(t, ts, "/roles.json")
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/json")
		var v struct {
			Roles []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"roles"`
		}
		if err := json.Unmarshal([]byte(r.Body), &v); err != nil {
			t.Fatal(err)
		}
		if len(v.Roles) != 3 {
			t.Errorf("roles = %d, want 3", len(v.Roles))
		}
		var found bool
		for _, ro := range v.Roles {
			found = found || (ro.ID == 2 && ro.Name == "Developer")
		}
		if !found {
			t.Errorf("{id: 2, name: Developer} not included: %s", r.Body)
		}
	})
	t.Run("GET /roles/:id.xml should return the role", func(t *testing.T) {
		r := apiGet(t, ts, "/roles/1.xml")
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/xml")
		doc := r.XML(t)
		assertXMLText(t, doc, "role > name", "Manager")
		assertXMLText(t, doc, "role > assignable", "true")
		assertXMLText(t, doc, "role > issues_visibility", "all")
		assertXMLText(t, doc, "role > time_entries_visibility", "all")
		assertXMLText(t, doc, "role > users_visibility", "all")
		n := mastersInt(t, d, `SELECT COUNT(*) FROM role_permissions WHERE role_id = 1`)
		assertXMLCount(t, doc, "role permissions[type=array] permission", int(n))
		if len(xmlSelect(t, doc, `role permissions permission:contains("view_issues")`)) == 0 {
			t.Error("view_issues permission missing")
		}
	})
}

// TestAPICustomFields は custom_fields_test.rb。
func TestAPICustomFields(t *testing.T) {
	t.Run("GET /custom_fields.xml should return custom fields", func(t *testing.T) {
		ts, _ := newFixtureServer(t)
		r := apiGet(t, ts, "/custom_fields.xml", apiCreds("admin"))
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/xml")
		doc := r.XML(t)
		// assert_select 'custom_fields' > 'custom_field' の最初（id 1 Database）
		cf := xmlSelect(t, doc, "custom_fields > custom_field:first-child")
		if len(cf) != 1 {
			t.Fatalf("custom_field not found: %.300s", r.Body)
		}
		assertXMLText(t, cf[0], "name", "Database")
		assertXMLText(t, cf[0], "description", "Select one of the databases")
		assertXMLText(t, cf[0], "id", "1")
		assertXMLText(t, cf[0], "customized_type", "issue")
		if len(xmlSelect(t, cf[0], `possible_values[type=array] possible_value > value:contains("PostgreSQL")`)) != 1 ||
			len(xmlSelect(t, cf[0], `possible_values[type=array] possible_value > label:contains("PostgreSQL")`)) != 1 {
			t.Error("possible value PostgreSQL missing")
		}
		assertXMLCount(t, cf[0], "trackers[type=array]", 1)
		assertXMLCount(t, cf[0], "roles[type=array]", 1)
		assertXMLText(t, cf[0], "visible", "true")
		assertXMLText(t, cf[0], "editable", "true")
		// Redmine のテストの assert_select は 'id' text '2' を含む（description のある CF 2 も一覧に含まれる）
		assertXMLCount(t, doc, "custom_fields > custom_field > id:contains(\"2\")", 1)
	})
	t.Run("GET /custom_fields.xml should include value and label for enumeration custom fields", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		// IssueCustomField.generate!(:field_format => 'enumeration') と enumerations.create!(Foo, Bar)
		mastersExec(t, d, `INSERT INTO custom_fields (id, owner_kind, name, field_format, position) VALUES (100, 'issue', 'CustomField100', 'enumeration', 100)`)
		mastersExec(t, d, `INSERT INTO custom_field_enumerations (id, custom_field_id, name, active, position) VALUES (11, 100, 'Foo', 1, 1), (12, 100, 'Bar', 1, 2)`)
		r := apiGet(t, ts, "/custom_fields.xml", apiCreds("admin"))
		r.expectStatus(t, 200)
		doc := r.XML(t)
		for id, name := range map[int]string{11: "Foo", 12: "Bar"} {
			sel := `possible_value > value:contains("` + strconv.Itoa(id) + `") + label:contains("` + name + `")`
			if len(xmlSelect(t, doc, sel)) == 0 {
				t.Errorf("%s not found", sel)
			}
		}
	})
	t.Run("GET /custom_fields.json requires admin", func(t *testing.T) {
		// before_action :require_admin（API では非管理者 403、未認証 401）
		ts, _ := newFixtureServer(t)
		apiGet(t, ts, "/custom_fields.json", apiCreds("jsmith")).expectStatus(t, 403)
		apiGet(t, ts, "/custom_fields.json").expectStatus(t, 401)
	})
}
