package server_test

// test/integration/api_test/wiki_pages_test.rb の移植。

import (
	"net/http"
	"strings"
	"testing"
)

func TestAPIWikiPages(t *testing.T) {
	f := newContentFixture(t)
	ts := f.ts

	t.Run("GET /projects/:project_id/wiki/index.xml should return wiki pages", func(t *testing.T) {
		res := apiGet(t, ts, "/projects/ecookbook/wiki/index.xml")
		res.expectStatus(t, 200)
		if res.ContentType() != "application/xml" {
			t.Errorf("content type %s", res.ContentType())
		}
		x := res.XML(t)
		assertXMLCount(t, x, "wiki_pages[type=array] wiki_page", int(f.int(t, `SELECT COUNT(*) FROM wiki_pages WHERE wiki_id = 1`)))
		var doc, inline bool
		for _, p := range xmlSelect(t, x, "wiki_pages wiki_page") {
			switch nodeText(xmlSelect(t, p, "title")[0]) {
			case "CookBook_documentation":
				doc = true
				assertXMLText(t, p, "version", "3")
				assertXMLCount(t, p, "created_on", 1)
				assertXMLCount(t, p, "updated_on", 1)
			case "Page_with_an_inline_image":
				inline = true
				assertXMLCount(t, p, `parent[title="CookBook_documentation"]`, 1)
			}
		}
		if !doc || !inline {
			t.Errorf("pages missing: %v %v", doc, inline)
		}
	})

	t.Run("GET /projects/:project_id/wiki/:title.xml should return wiki page", func(t *testing.T) {
		res := apiGet(t, ts, "/projects/ecookbook/wiki/CookBook_documentation.xml")
		res.expectStatus(t, 200)
		x := res.XML(t)
		assertXMLText(t, x, "wiki_page > title", "CookBook_documentation")
		assertXMLText(t, x, "wiki_page > version", "3")
		for _, e := range []string{"text", "author", "comments", "created_on", "updated_on"} {
			assertXMLCount(t, x, "wiki_page > "+e, 1)
		}
	})

	t.Run("GET /projects/:project_id/wiki/:title.xml?include=attachments should include attachments", func(t *testing.T) {
		f := newContentFixture(t)
		f.writeAttachmentFile(t, 3, []byte("GIF89a"))
		res := apiGet(t, f.ts, "/projects/ecookbook/wiki/Page_with_an_inline_image.xml?include=attachments")
		res.expectStatus(t, 200)
		x := res.XML(t)
		assertXMLText(t, x, "wiki_page > title", "Page_with_an_inline_image")
		assertXMLText(t, x, "wiki_page attachments[type=array] attachment > id", "3")
		assertXMLText(t, x, "wiki_page attachments[type=array] attachment > filename", "logo.gif")
	})

	t.Run("GET /projects/:project_id/wiki/:title.xml with unknown title and edit permission should respond with 404", func(t *testing.T) {
		res := apiGet(t, ts, "/projects/ecookbook/wiki/Invalid_Page.xml", apiCreds("jsmith"))
		res.expectStatus(t, 404)
		if res.ContentType() != "application/xml" {
			t.Errorf("content type %s", res.ContentType())
		}
	})

	t.Run("GET /projects/:project_id/wiki/:title/:version.xml should return wiki page version", func(t *testing.T) {
		res := apiGet(t, ts, "/projects/ecookbook/wiki/CookBook_documentation/2.xml")
		res.expectStatus(t, 200)
		x := res.XML(t)
		assertXMLText(t, x, "wiki_page > title", "CookBook_documentation")
		assertXMLText(t, x, "wiki_page > version", "2")
		assertXMLText(t, x, "wiki_page > comments", "Small update")
		for _, e := range []string{"text", "author", "created_on", "updated_on"} {
			assertXMLCount(t, x, "wiki_page > "+e, 1)
		}
	})

	t.Run("GET /projects/:project_id/wiki/:title/:version.xml without permission should be denied", func(t *testing.T) {
		f := newContentFixture(t)
		// Role.anonymous.remove_permission! :view_wiki_edits
		f.exec(t, `DELETE FROM role_permissions WHERE permission = 'view_wiki_edits' AND role_id = (SELECT id FROM roles WHERE builtin = 2)`)
		res := apiGet(t, f.ts, "/projects/ecookbook/wiki/CookBook_documentation/2.xml")
		res.expectStatus(t, 401)
		if res.ContentType() != "application/xml" {
			t.Errorf("content type %s", res.ContentType())
		}
	})

	// wikiContent はページの本文・版・コメント・作成者を返す。
	wikiContent := func(t *testing.T, f *contentFixture, pageID int64) (text string, version int64, comments, author string) {
		t.Helper()
		// wiki_page_versions の最新版（wiki_pages.current_version）が WikiContent に当たる
		cur := `(SELECT current_version FROM wiki_pages WHERE id = ?)`
		return f.str(t, `SELECT text FROM wiki_page_versions WHERE page_id = ? AND version = `+cur, pageID, pageID),
			f.int(t, `SELECT current_version FROM wiki_pages WHERE id = ?`, pageID),
			f.str(t, `SELECT comments FROM wiki_page_versions WHERE page_id = ? AND version = `+cur, pageID, pageID),
			f.str(t, `SELECT u.login FROM wiki_page_versions c JOIN user_accounts u ON u.principal_id = c.author_id WHERE c.page_id = ? AND c.version = `+cur, pageID, pageID)
	}
	countVersions := func(t *testing.T, f *contentFixture) int64 {
		return f.int(t, `SELECT COUNT(*) FROM wiki_page_versions`)
	}
	countPages := func(t *testing.T, f *contentFixture) int64 { return f.int(t, `SELECT COUNT(*) FROM wiki_pages`) }

	for _, tc := range []struct{ name, version string }{
		{"PUT /projects/:project_id/wiki/:title.xml should update wiki page", ""},
		{"PUT /projects/:project_id/wiki/:title.xml with current versino should update wiki page", "3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newContentFixture(t)
			pages, versions := countPages(t, f), countVersions(t, f)
			kv := []string{"wiki_page[text]", "New content from API", "wiki_page[comments]", "API update"}
			if tc.version != "" {
				kv = append(kv, "wiki_page[version]", tc.version)
			}
			apiCall(t, f.ts, http.MethodPut, "/projects/ecookbook/wiki/CookBook_documentation.xml", cFormType, cForm(kv...), apiCreds("jsmith")).expectStatus(t, 204)
			if countPages(t, f) != pages || countVersions(t, f) != versions+1 {
				t.Error("page / version count")
			}
			text, ver, comments, author := wikiContent(t, f, 1)
			if text != "New content from API" || ver != 4 || comments != "API update" || author != "jsmith" {
				t.Errorf("content %q %d %q %q", text, ver, comments, author)
			}
		})
	}

	t.Run("GET /projects/:project_id/wiki/:title/:version.xml should not includ author if not exists", func(t *testing.T) {
		f := newContentFixture(t)
		f.exec(t, `UPDATE wiki_page_versions SET author_id = NULL WHERE page_id = 1 AND version = 2`)
		res := apiGet(t, f.ts, "/projects/ecookbook/wiki/CookBook_documentation/2.xml")
		res.expectStatus(t, 200)
		assertXMLCount(t, res.XML(t), "wiki_page author", 0)
	})

	t.Run("PUT /projects/:project_id/wiki/:title.xml with stale version should respond with 409", func(t *testing.T) {
		f := newContentFixture(t)
		pages, versions := countPages(t, f), countVersions(t, f)
		apiCall(t, f.ts, http.MethodPut, "/projects/ecookbook/wiki/CookBook_documentation.xml", cFormType,
			cForm("wiki_page[text]", "New content from API", "wiki_page[comments]", "API update", "wiki_page[version]", "2"), apiCreds("jsmith")).expectStatus(t, 409)
		if countPages(t, f) != pages || countVersions(t, f) != versions {
			t.Error("page / version count changed")
		}
	})

	t.Run("PUT /projects/:project_id/wiki/:title.xml should create the page if it does not exist", func(t *testing.T) {
		f := newContentFixture(t)
		pages, versions := countPages(t, f), countVersions(t, f)
		apiCall(t, f.ts, http.MethodPut, "/projects/ecookbook/wiki/New_page_from_API.xml", cFormType,
			cForm("wiki_page[text]", "New content from API", "wiki_page[comments]", "API create"), apiCreds("jsmith")).expectStatus(t, 201)
		if countPages(t, f) != pages+1 || countVersions(t, f) != versions+1 {
			t.Error("page / version count")
		}
		id := f.int(t, `SELECT MAX(id) FROM wiki_pages`)
		if s := f.str(t, `SELECT title FROM wiki_pages WHERE id = ?`, id); s != "New_page_from_API" {
			t.Errorf("title %q", s)
		}
		text, ver, comments, author := wikiContent(t, f, id)
		if text != "New content from API" || ver != 1 || comments != "API create" || author != "jsmith" {
			t.Errorf("content %q %d %q %q", text, ver, comments, author)
		}
		if n := f.int(t, `SELECT COUNT(*) FROM wiki_pages WHERE id = ? AND parent_id IS NULL`, id); n != 1 {
			t.Error("parent not nil")
		}
	})

	t.Run("PUT /projects/:project_id/wiki/:title.xml with attachment", func(t *testing.T) {
		f := newContentFixture(t)
		token := f.upload(t, "json", "this is a text file for upload tests\r\nwith multiple lines\r\n", apiCreds("jsmith"))
		attID, _, _ := strings.Cut(token, ".")
		pages, versions := countPages(t, f), countVersions(t, f)
		apiCall(t, f.ts, http.MethodPut, "/projects/ecookbook/wiki/New_page_from_API.xml", cFormType, cForm(
			"wiki_page[text]", "New content from API with Attachments", "wiki_page[comments]", "API create with Attachments",
			"wiki_page[uploads][][token]", token, "wiki_page[uploads][][filename]", "testfile.txt", "wiki_page[uploads][][content_type]", "text/plain",
		), apiCreds("jsmith")).expectStatus(t, 201)
		if countPages(t, f) != pages+1 || countVersions(t, f) != versions+1 {
			t.Error("page / version count")
		}
		id := f.int(t, `SELECT MAX(id) FROM wiki_pages`)
		if s := f.str(t, `SELECT title FROM wiki_pages WHERE id = ?`, id); s != "New_page_from_API" {
			t.Errorf("title %q", s)
		}
		if n := f.int(t, `SELECT COUNT(*) FROM attachments WHERE id = ? AND container_kind = 'wiki_page' AND container_id = ?`, attID, id); n != 1 {
			t.Error("attachment not attached")
		}
		if s := f.str(t, `SELECT filename FROM attachments WHERE id = ?`, attID); s != "testfile.txt" {
			t.Errorf("filename %q", s)
		}
	})

	t.Run("PUT /projects/:project_id/wiki/:title.xml with parent", func(t *testing.T) {
		f := newContentFixture(t)
		pages, versions := countPages(t, f), countVersions(t, f)
		apiCall(t, f.ts, http.MethodPut, "/projects/ecookbook/wiki/New_subpage_from_API.xml", cFormType,
			cForm("wiki_page[parent_title]", "CookBook_documentation", "wiki_page[text]", "New content from API", "wiki_page[comments]", "API create"), apiCreds("jsmith")).expectStatus(t, 201)
		if countPages(t, f) != pages+1 || countVersions(t, f) != versions+1 {
			t.Error("page / version count")
		}
		id := f.int(t, `SELECT MAX(id) FROM wiki_pages`)
		if s := f.str(t, `SELECT title FROM wiki_pages WHERE id = ?`, id); s != "New_subpage_from_API" {
			t.Errorf("title %q", s)
		}
		if n := f.int(t, `SELECT COALESCE(parent_id, 0) FROM wiki_pages WHERE id = ?`, id); n != 1 {
			t.Errorf("parent %d", n)
		}
	})

	// 以下は互換シナリオ（api_write.yml）で見つかった差分の確認（Redmine のテストには無い）。
	t.Run("PUT /projects/:project_id/wiki/:title.xml with parent_title in XML body", func(t *testing.T) {
		f := newContentFixture(t)
		apiCall(t, f.ts, http.MethodPut, "/projects/ecookbook/wiki/XML_child.xml", "application/xml",
			`<wiki_page><text>XML child</text><parent_title>CookBook_documentation</parent_title></wiki_page>`, apiCreds("jsmith")).expectStatus(t, 201)
		res := apiGet(t, f.ts, "/projects/ecookbook/wiki/XML_child.xml", apiCreds("jsmith"))
		assertXMLCount(t, res.XML(t), `wiki_page > parent[title="CookBook_documentation"]`, 1)
	})

	t.Run("PUT /projects/:project_id/wiki/:title.json with itself as parent should respond with 422", func(t *testing.T) {
		f := newContentFixture(t)
		versions := countVersions(t, f)
		res := apiCall(t, f.ts, http.MethodPut, "/projects/ecookbook/wiki/Another_page.json", "application/json",
			`{"wiki_page":{"text":"x","parent_title":"Another_page"}}`, apiCreds("jsmith"))
		res.expectStatus(t, 422)
		// render_validation_errors(@content): ページの検証エラーは本文に含まれない
		if res.Body != `{"errors":[]}` {
			t.Errorf("body %s", res.Body)
		}
		if countVersions(t, f) != versions {
			t.Error("version created")
		}
	})

	t.Run("DELETE /projects/:project_id/wiki/:title.xml should destroy the page", func(t *testing.T) {
		f := newContentFixture(t)
		pages := countPages(t, f)
		apiCall(t, f.ts, http.MethodDelete, "/projects/ecookbook/wiki/CookBook_documentation.xml", "", "", apiCreds("jsmith")).expectStatus(t, 204)
		if countPages(t, f) != pages-1 {
			t.Error("page count")
		}
		if n := f.int(t, `SELECT COUNT(*) FROM wiki_pages WHERE id = 1`); n != 0 {
			t.Error("page 1 remains")
		}
	})
}
