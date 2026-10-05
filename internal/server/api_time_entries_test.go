// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

// test/integration/api_test/time_entries_test.rb の移植。

import (
	"net/http"
	"strings"
	"testing"
)

func TestAPITimeEntries(t *testing.T) {
	f := newContentFixture(t)
	ts := f.ts

	// hoursOf は time_entry の id ごとの hours 要素のテキスト（assert_select 'time_entry:has(id:contains(4)) hours'）。
	hoursOf := func(t *testing.T, res apiResp, id string) string {
		t.Helper()
		for _, e := range xmlSelect(t, res.XML(t), "time_entry") {
			if nodeText(xmlSelect(t, e, "id")[0]) == id {
				return nodeText(xmlSelect(t, e, "hours")[0])
			}
		}
		return "(none)"
	}

	t.Run("GET /time_entries.xml should return time entries", func(t *testing.T) {
		res := apiGet(t, ts, "/time_entries.xml", apiCreds("jsmith"))
		res.expectStatus(t, 200)
		if res.ContentType() != "application/xml" {
			t.Errorf("content type %s", res.ContentType())
		}
		assertXMLText(t, res.XML(t), "time_entries[type=array] time_entry id", "4")
		if h := hoursOf(t, res, "4"); h != "7.65" {
			t.Errorf("hours of 4: %s", h)
		}
		if h := hoursOf(t, res, "3"); h != "1.0" {
			t.Errorf("hours of 3: %s", h)
		}
	})

	t.Run("GET /time_entries.xml with limit should return limited results", func(t *testing.T) {
		res := apiGet(t, ts, "/time_entries.xml?limit=2", apiCreds("jsmith"))
		res.expectStatus(t, 200)
		assertXMLCount(t, res.XML(t), "time_entries[type=array] time_entry", 2)
	})

	t.Run("GET /time_entries/:id.xml should return the time entry", func(t *testing.T) {
		res := apiGet(t, ts, "/time_entries/4.xml", apiCreds("jsmith"))
		res.expectStatus(t, 200)
		x := res.XML(t)
		assertXMLText(t, x, "time_entry id", "4")
		assertXMLText(t, x, "time_entry hours", "7.65")
	})

	t.Run("GET /time_entries/:id.xml on closed project should return the time entry", func(t *testing.T) {
		f := newContentFixture(t)
		f.exec(t, `UPDATE projects SET status = 5 WHERE id = (SELECT project_id FROM time_entries WHERE id = 2)`)
		res := apiGet(t, f.ts, "/time_entries/2.xml", apiCreds("jsmith"))
		res.expectStatus(t, 200)
		assertXMLText(t, res.XML(t), "time_entry id", "2")
	})

	t.Run("GET /time_entries/:id.xml with invalid id should 404", func(t *testing.T) {
		apiGet(t, ts, "/time_entries/999.xml", apiCreds("jsmith")).expectStatus(t, 404)
	})

	t.Run("GET /time_entries/:id.xml with non visible time entry should 403", func(t *testing.T) {
		f := newContentFixture(t)
		f.exec(t, `UPDATE roles SET time_entries_visibility = 'own' WHERE builtin = 1`)
		apiGet(t, f.ts, "/time_entries/4.xml", apiCreds("jsmith")).expectStatus(t, 403)
	})

	// lastEntry は最後に作られた工数の列を返す。
	type entry struct {
		user, author, project, activity int64
		issue                           int64
		spentOn                         string
		hours                           string
	}
	lastEntry := func(t *testing.T, f *contentFixture) entry {
		t.Helper()
		id := f.int(t, `SELECT MAX(id) FROM time_entries`)
		return entry{
			user:     f.int(t, `SELECT user_id FROM time_entries WHERE id = ?`, id),
			author:   f.int(t, `SELECT COALESCE(author_id, 0) FROM time_entries WHERE id = ?`, id),
			project:  f.int(t, `SELECT project_id FROM time_entries WHERE id = ?`, id),
			activity: f.int(t, `SELECT activity_id FROM time_entries WHERE id = ?`, id),
			issue:    f.int(t, `SELECT COALESCE(issue_id, 0) FROM time_entries WHERE id = ?`, id),
			spentOn:  f.str(t, `SELECT spent_on FROM time_entries WHERE id = ?`, id),
			hours:    f.str(t, `SELECT CAST(hours AS TEXT) FROM time_entries WHERE id = ?`, id),
		}
	}

	t.Run("POST /time_entries.xml with issue_id should create time entry", func(t *testing.T) {
		f := newContentFixture(t)
		before := f.int(t, `SELECT COUNT(*) FROM time_entries`)
		res := apiCall(t, f.ts, http.MethodPost, "/time_entries.xml", cFormType, cForm(
			"time_entry[issue_id]", "1", "time_entry[spent_on]", "2010-12-02", "time_entry[hours]", "3.5", "time_entry[activity_id]", "11"), apiCreds("jsmith"))
		res.expectStatus(t, 201)
		if res.ContentType() != "application/xml" {
			t.Errorf("content type %s", res.ContentType())
		}
		if f.int(t, `SELECT COUNT(*) FROM time_entries`) != before+1 {
			t.Fatal("not created")
		}
		e := lastEntry(t, f)
		if e.user != 2 || e.issue != 1 || e.project != 1 || !strings.HasPrefix(e.spentOn, "2010-12-02") || e.hours != "3.5" || e.activity != 11 {
			t.Errorf("entry %+v", e)
		}
	})

	t.Run("POST /time_entries.xml with issue_id should accept custom fields", func(t *testing.T) {
		f := newContentFixture(t)
		// TimeEntryCustomField.create!(:name => 'Test', :field_format => 'string')
		f.exec(t, `INSERT INTO custom_fields (owner_kind, name, field_format, position) VALUES ('time_entry', 'Test', 'string', 99)`)
		cfID := cItoa(f.int(t, `SELECT MAX(id) FROM custom_fields`))
		res := apiCall(t, f.ts, http.MethodPost, "/time_entries.xml", cFormType, cForm(
			"time_entry[issue_id]", "1", "time_entry[spent_on]", "2010-12-02", "time_entry[hours]", "3.5", "time_entry[activity_id]", "11",
			"time_entry[custom_fields][][id]", cfID, "time_entry[custom_fields][][value]", "accepted"), apiCreds("jsmith"))
		res.expectStatus(t, 201)
		id := f.int(t, `SELECT MAX(id) FROM time_entries`)
		if v := f.str(t, `SELECT value FROM custom_values WHERE customized_kind = 'time_entry' AND customized_id = ? AND custom_field_id = ?`, id, cfID); v != "accepted" {
			t.Errorf("custom value %q", v)
		}
	})

	t.Run("POST /time_entries.xml with project_id should create time entry", func(t *testing.T) {
		f := newContentFixture(t)
		res := apiCall(t, f.ts, http.MethodPost, "/time_entries.xml", cFormType, cForm(
			"time_entry[project_id]", "1", "time_entry[spent_on]", "2010-12-02", "time_entry[hours]", "3.5", "time_entry[activity_id]", "11"), apiCreds("jsmith"))
		res.expectStatus(t, 201)
		if res.ContentType() != "application/xml" {
			t.Errorf("content type %s", res.ContentType())
		}
		e := lastEntry(t, f)
		if e.user != 2 || e.issue != 0 || e.project != 1 || !strings.HasPrefix(e.spentOn, "2010-12-02") || e.hours != "3.5" || e.activity != 11 {
			t.Errorf("entry %+v", e)
		}
	})

	t.Run("POST /time_entries.xml with invalid parameters should return errors", func(t *testing.T) {
		before := f.int(t, `SELECT COUNT(*) FROM time_entries`)
		res := apiCall(t, ts, http.MethodPost, "/time_entries.xml", cFormType, cForm(
			"time_entry[project_id]", "1", "time_entry[spent_on]", "2010-12-02", "time_entry[activity_id]", "11"), apiCreds("jsmith"))
		res.expectStatus(t, 422)
		if res.ContentType() != "application/xml" {
			t.Errorf("content type %s", res.ContentType())
		}
		assertXMLText(t, res.XML(t), "errors error", "Hours cannot be blank")
		if f.int(t, `SELECT COUNT(*) FROM time_entries`) != before {
			t.Error("created")
		}
	})

	for _, target := range []string{"project_id", "issue_id"} {
		t.Run("POST /time_entries.xml with :"+target+" for other user", func(t *testing.T) {
			f := newContentFixture(t)
			// Role.find_by_name('Manager').add_permission! :log_time_for_other_users
			f.exec(t, `INSERT INTO role_permissions (role_id, permission) SELECT id, 'log_time_for_other_users' FROM roles WHERE name = 'Manager'`)
			before := f.int(t, `SELECT COUNT(*) FROM time_entries`)
			res := apiCall(t, f.ts, http.MethodPost, "/time_entries.xml", cFormType, cForm(
				"time_entry["+target+"]", "1", "time_entry[spent_on]", "2010-12-02", "time_entry[user_id]", "3",
				"time_entry[hours]", "3.5", "time_entry[activity_id]", "11"), apiCreds("jsmith"))
			res.expectStatus(t, 201)
			if f.int(t, `SELECT COUNT(*) FROM time_entries`) != before+1 {
				t.Fatal("not created")
			}
			if e := lastEntry(t, f); e.user != 3 || e.author != 2 {
				t.Errorf("user %d author %d", e.user, e.author)
			}
		})
	}

	t.Run("PUT /time_entries/:id.xml with valid parameters should update time entry", func(t *testing.T) {
		f := newContentFixture(t)
		res := apiCall(t, f.ts, http.MethodPut, "/time_entries/2.xml", cFormType, cForm("time_entry[comments]", "API Update"), apiCreds("jsmith"))
		res.expectStatus(t, 204)
		if res.Body != "" {
			t.Errorf("body %q", res.Body)
		}
		if s := f.str(t, `SELECT comments FROM time_entries WHERE id = 2`); s != "API Update" {
			t.Errorf("comments %q", s)
		}
	})

	t.Run("PUT /time_entries/:id.xml with invalid parameters should return errors", func(t *testing.T) {
		res := apiCall(t, ts, http.MethodPut, "/time_entries/2.xml", cFormType, cForm("time_entry[hours]", "", "time_entry[comments]", "API Update"), apiCreds("jsmith"))
		res.expectStatus(t, 422)
		if res.ContentType() != "application/xml" {
			t.Errorf("content type %s", res.ContentType())
		}
		assertXMLText(t, res.XML(t), "errors error", "Hours cannot be blank")
	})

	t.Run("PUT /time_entries/:id.xml without permissions should fail", func(t *testing.T) {
		apiCall(t, ts, http.MethodPut, "/time_entries/2.xml", cFormType, cForm("time_entry[hours]", "2.3", "time_entry[comments]", "API Update"), apiCreds("dlopper")).expectStatus(t, 403)
	})

	t.Run("DELETE /time_entries/:id.xml should destroy time entry", func(t *testing.T) {
		f := newContentFixture(t)
		before := f.int(t, `SELECT COUNT(*) FROM time_entries`)
		res := apiCall(t, f.ts, http.MethodDelete, "/time_entries/2.xml", "", "", apiCreds("jsmith"))
		res.expectStatus(t, 204)
		if res.Body != "" {
			t.Errorf("body %q", res.Body)
		}
		if f.int(t, `SELECT COUNT(*) FROM time_entries`) != before-1 || f.int(t, `SELECT COUNT(*) FROM time_entries WHERE id = 2`) != 0 {
			t.Error("not deleted")
		}
	})

	// 7.0.1 (#44146)
	t.Run("GET /time_entries/:id.xml should only return visible custom fields", func(t *testing.T) {
		f := newContentFixture(t)
		f.exec(t, `INSERT INTO custom_fields (owner_kind, name, field_format, visible, position) VALUES ('time_entry', 'Visible field', 'string', ?, 98)`, false)
		cf1 := f.int(t, `SELECT MAX(id) FROM custom_fields`)
		f.exec(t, `INSERT INTO custom_fields (owner_kind, name, field_format, visible, position) VALUES ('time_entry', 'Non visible field', 'string', ?, 99)`, false)
		cf2 := f.int(t, `SELECT MAX(id) FROM custom_fields`)
		f.exec(t, `INSERT INTO custom_fields_roles (custom_field_id, role_id) SELECT ?, id FROM roles WHERE name = 'Manager'`, cf1)
		f.exec(t, `INSERT INTO custom_fields_roles (custom_field_id, role_id) SELECT ?, id FROM roles WHERE name = 'Developer'`, cf2)
		f.exec(t, `INSERT INTO custom_values (customized_kind, customized_id, custom_field_id, value) VALUES ('time_entry', 3, ?, 'value1'), ('time_entry', 3, ?, 'value2')`, cf1, cf2)
		res := apiGet(t, f.ts, "/time_entries/3.xml", apiCreds("jsmith"))
		res.expectStatus(t, 200)
		x := res.XML(t)
		assertXMLText(t, x, `time_entry custom_fields custom_field[id="`+cItoa(cf1)+`"][name="Visible field"] value`, "value1")
		assertXMLCount(t, x, `time_entry custom_fields custom_field[id="`+cItoa(cf2)+`"]`, 0)
	})

	// "DELETE /time_entries/:id.xml with failure should return errors" は TimeEntry#destroy を
	// スタブで失敗させるテストで、Go では同じ注入ができないため移植しない。
}
