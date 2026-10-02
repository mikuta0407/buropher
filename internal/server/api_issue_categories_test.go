package server_test

// Redmine の test/integration/api_test/issue_categories_test.rb の移植。

import "testing"

func TestAPIIssueCategories(t *testing.T) {
	ts, _ := newFixtureServer(t)
	t.Run("GET /projects/:project_id/issue_categories.xml should return the issue categories", func(t *testing.T) {
		r := apiGet(t, ts, "/projects/1/issue_categories.xml", apiCreds("jsmith"))
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/xml")
		if len(xmlSelect(t, r.XML(t), `issue_categories issue_category id:contains("2")`)) == 0 {
			t.Error("category 2 missing")
		}
	})
	t.Run("GET /issue_categories/:id.xml should return the issue category", func(t *testing.T) {
		r := apiGet(t, ts, "/issue_categories/2.xml", apiCreds("jsmith"))
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/xml")
		assertXMLText(t, r.XML(t), "issue_category id", "2")
	})

	t.Run("POST /projects/:project_id/issue_categories.xml should return create issue category", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		before := mastersInt(t, d, `SELECT COUNT(*) FROM issue_categories`)
		r := mastersForm(t, ts, "POST", "/projects/1/issue_categories.xml", "issue_category[name]=API", apiCreds("jsmith"))
		r.expectStatus(t, 201)
		mastersMedia(t, r, "application/xml")
		if n := mastersInt(t, d, `SELECT COUNT(*) FROM issue_categories`); n != before+1 {
			t.Fatalf("categories %d -> %d", before, n)
		}
		if s := mastersStr(t, d, `SELECT name || '/' || project_id FROM issue_categories ORDER BY id DESC LIMIT 1`); s != "API/1" {
			t.Errorf("category = %s", s)
		}
	})
	t.Run("POST /projects/:project_id/issue_categories.xml with invalid parameters should return errors", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		before := mastersInt(t, d, `SELECT COUNT(*) FROM issue_categories`)
		r := mastersForm(t, ts, "POST", "/projects/1/issue_categories.xml", "issue_category[name]=", apiCreds("jsmith"))
		r.expectStatus(t, 422)
		mastersMedia(t, r, "application/xml")
		assertXMLText(t, r.XML(t), "errors error", "Name cannot be blank")
		if n := mastersInt(t, d, `SELECT COUNT(*) FROM issue_categories`); n != before {
			t.Error("category created")
		}
	})
	t.Run("PUT /issue_categories/:id.xml with valid parameters should update the issue category", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		mastersNoContent(t, mastersForm(t, ts, "PUT", "/issue_categories/2.xml", "issue_category[name]=API+Update", apiCreds("jsmith")))
		if s := mastersStr(t, d, `SELECT name FROM issue_categories WHERE id = 2`); s != "API Update" {
			t.Errorf("name = %s", s)
		}
	})
	t.Run("PUT /issue_categories/:id.xml with invalid parameters should return errors", func(t *testing.T) {
		ts, _ := newFixtureServer(t)
		r := mastersForm(t, ts, "PUT", "/issue_categories/2.xml", "issue_category[name]=", apiCreds("jsmith"))
		r.expectStatus(t, 422)
		mastersMedia(t, r, "application/xml")
		assertXMLText(t, r.XML(t), "errors error", "Name cannot be blank")
	})
	t.Run("DELETE /issue_categories/:id.xml should destroy the issue category", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		mastersNoContent(t, apiCall(t, ts, "DELETE", "/issue_categories/1.xml", "", "", apiCreds("jsmith")))
		if n := mastersInt(t, d, `SELECT COUNT(*) FROM issue_categories WHERE id = 1`); n != 0 {
			t.Error("category 1 still exists")
		}
	})
	t.Run("DELETE /issue_categories/:id.xml should reassign issues with :reassign_to_id param", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		count := mastersInt(t, d, `SELECT COUNT(*) FROM issues WHERE category_id = 1`)
		if count == 0 {
			t.Fatal("no issues in category 1")
		}
		before2 := mastersInt(t, d, `SELECT COUNT(*) FROM issues WHERE category_id = 2`)
		mastersNoContent(t, apiCall(t, ts, "DELETE", "/issue_categories/1.xml?reassign_to_id=2", "", "", apiCreds("jsmith")))
		if n := mastersInt(t, d, `SELECT COUNT(*) FROM issues WHERE category_id = 2`); n != before2+3 {
			t.Errorf("issues in category 2: %d -> %d, want +3", before2, n)
		}
		if n := mastersInt(t, d, `SELECT COUNT(*) FROM issue_categories WHERE id = 1`); n != 0 {
			t.Error("category 1 still exists")
		}
	})
}
