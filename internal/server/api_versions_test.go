package server_test

// Redmine の test/integration/api_test/versions_test.rb の移植。

import "testing"

func TestAPIVersions(t *testing.T) {
	t.Run("GET /projects/:project_id/versions.xml should return project versions", func(t *testing.T) {
		ts, _ := newFixtureServer(t)
		r := apiGet(t, ts, "/projects/1/versions.xml")
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/xml")
		var found bool
		for _, v := range xmlSelect(t, r.XML(t), "versions[type=array] > version") {
			if nodeText(xmlSelect(t, v, "id")[0]) == "2" {
				found = true
				assertXMLText(t, v, "name", "1.0")
			}
		}
		if !found {
			t.Error("version 2 not found")
		}
	})
	// createVersion は POST /projects/1/versions.xml を送り、作成された版の id を返す。
	createVersion := func(t *testing.T, form string) (apiResp, int64, func(string, ...any) string) {
		ts, d := newFixtureServer(t)
		before := mastersInt(t, d, `SELECT COUNT(*) FROM versions`)
		r := mastersForm(t, ts, "POST", "/projects/1/versions.xml", form, apiCreds("jsmith"))
		if n := mastersInt(t, d, `SELECT COUNT(*) FROM versions`); n != before+1 {
			t.Fatalf("versions %d -> %d: %s", before, n, r.Body)
		}
		id := mastersInt(t, d, `SELECT MAX(id) FROM versions`)
		r.expectStatus(t, 201)
		mastersMedia(t, r, "application/xml")
		assertXMLText(t, r.XML(t), "version id", itoa64(id))
		return r, id, func(q string, args ...any) string { return mastersStr(t, d, q, args...) }
	}
	t.Run("POST /projects/:project_id/versions.xml should create the version", func(t *testing.T) {
		_, id, str := createVersion(t, "version[name]=API+test")
		if s := str(`SELECT name FROM versions WHERE id = ?`, id); s != "API test" {
			t.Errorf("name = %s", s)
		}
	})
	t.Run("POST /projects/:project_id/versions.xml should create the version with due date", func(t *testing.T) {
		_, id, str := createVersion(t, "version[name]=API+test&version[due_date]=2012-01-24")
		if s := str(`SELECT name || '/' || effective_date FROM versions WHERE id = ?`, id); s != "API test/2012-01-24" {
			t.Errorf("version = %s", s)
		}
	})
	t.Run("POST /projects/:project_id/versions.xml should create the version with wiki page title", func(t *testing.T) {
		// WikiPage.first.title
		_, id, str := createVersion(t, "version[name]=API+test&version[wiki_page_title]=CookBook_documentation")
		if s := str(`SELECT wiki_page_title FROM versions WHERE id = ?`, id); s != "CookBook_documentation" {
			t.Errorf("wiki_page_title = %s", s)
		}
	})
	t.Run("POST /projects/:project_id/versions.xml should create the version with custom fields", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		// VersionCustomField.generate!
		mastersExec(t, d, `INSERT INTO custom_fields (id, owner_kind, name, field_format, position) VALUES (100, 'version', 'CustomField100', 'string', 1)`)
		r := mastersForm(t, ts, "POST", "/projects/1/versions.xml",
			"version[name]=API+test&version[custom_fields][][id]=100&version[custom_fields][][value]=Some+value", apiCreds("jsmith"))
		r.expectStatus(t, 201)
		mastersMedia(t, r, "application/xml")
		id := mastersInt(t, d, `SELECT MAX(id) FROM versions`)
		if s := mastersStr(t, d, `SELECT value FROM custom_values WHERE custom_field_id = 100 AND customized_id = ?`, id); s != "Some value" {
			t.Errorf("custom value = %q", s)
		}
		assertXMLText(t, r.XML(t), `version > custom_fields > custom_field[id="100"] > value`, "Some value")
	})
	t.Run("POST /projects/:project_id/versions.xml with failure should return the errors", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		before := mastersInt(t, d, `SELECT COUNT(*) FROM versions`)
		r := mastersForm(t, ts, "POST", "/projects/1/versions.xml", "version[name]=", apiCreds("jsmith"))
		r.expectStatus(t, 422)
		assertXMLText(t, r.XML(t), "errors error", "Name cannot be blank")
		if n := mastersInt(t, d, `SELECT COUNT(*) FROM versions`); n != before {
			t.Error("version created")
		}
	})
	t.Run("GET /versions/:id.xml should return the version", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		// TimeEntry.generate!(:issue_id => 2, :hours => 1.0) / (:issue_id => 12, :hours => 1.5)
		for _, te := range []struct {
			issue int
			hours float64
		}{{2, 1.0}, {12, 1.5}} {
			mastersExec(t, d, `INSERT INTO time_entries (project_id, user_id, author_id, issue_id, hours, activity_id, spent_on, tyear, tmonth, tweek, created_at, updated_at)
				SELECT project_id, 2, 2, id, ?, 9, '2026-01-15', 2026, 1, 3, '2026-01-15T12:00:00.000000Z', '2026-01-15T12:00:00.000000Z' FROM issues WHERE id = ?`, te.hours, te.issue)
		}
		r := apiGet(t, ts, "/versions/2.xml")
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/xml")
		doc := r.XML(t)
		assertXMLText(t, doc, "version > id", "2")
		assertXMLText(t, doc, "version > name", "1.0")
		assertXMLText(t, doc, "version > sharing", "none")
		assertXMLText(t, doc, "version > wiki_page_title", "ECookBookV1")
		assertXMLText(t, doc, "version > estimated_hours", "0.5")
		assertXMLText(t, doc, "version > spent_hours", "2.5")
	})
	t.Run("PUT /versions/:id.xml should update the version", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		mastersNoContent(t, mastersForm(t, ts, "PUT", "/versions/2.xml",
			"version[name]=API+update&version[wiki_page_title]=CookBook_documentation", apiCreds("jsmith")))
		if s := mastersStr(t, d, `SELECT name || '/' || wiki_page_title FROM versions WHERE id = 2`); s != "API update/CookBook_documentation" {
			t.Errorf("version = %s", s)
		}
	})
	t.Run("DELETE /versions/:id.xml should destroy the version", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		mastersNoContent(t, apiCall(t, ts, "DELETE", "/versions/3.xml", "", "", apiCreds("jsmith")))
		if n := mastersInt(t, d, `SELECT COUNT(*) FROM versions WHERE id = 3`); n != 0 {
			t.Error("version 3 still exists")
		}
	})
}
