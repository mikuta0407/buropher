package server_test

// test/integration/api_test/files_test.rb の移植。

import (
	"net/http"
	"testing"
)

func TestAPIFiles(t *testing.T) {
	t.Run("GET /projects/:project_id/files.xml should return the list of uploaded files", func(t *testing.T) {
		f := newContentFixture(t)
		res := apiGet(t, f.ts, "/projects/1/files.xml", apiCreds("jsmith"))
		res.expectStatus(t, 200)
		assertXMLText(t, res.XML(t), "files > file > id", "8")
	})

	// lastAttachment は最後に作られた添付の container_type / container_id。
	lastAttachment := func(t *testing.T, f *contentFixture) (string, int64) {
		id := f.int(t, `SELECT MAX(id) FROM attachments`)
		return f.str(t, `SELECT container_kind FROM attachments WHERE id = ?`, id), f.int(t, `SELECT COALESCE(container_id, 0) FROM attachments WHERE id = ?`, id)
	}

	t.Run("POST /projects/:project_id/files.json should create a file", func(t *testing.T) {
		f := newContentFixture(t)
		token := f.upload(t, "xml", "File content", apiCreds("jsmith"))
		res := apiCall(t, f.ts, http.MethodPost, "/projects/1/files.json", "application/json", `{ "file": { "token": "`+token+`" } }`, apiCreds("jsmith"))
		if res.Status/100 != 2 {
			t.Fatalf("status %d %s", res.Status, res.Body)
		}
		if ct, id := lastAttachment(t, f); ct != "project" || id != 1 {
			t.Errorf("container %s %d", ct, id)
		}
	})

	t.Run("POST /projects/:project_id/files.xml should create a file", func(t *testing.T) {
		f := newContentFixture(t)
		token := f.upload(t, "xml", "File content", apiCreds("jsmith"))
		res := apiCall(t, f.ts, http.MethodPost, "/projects/1/files.xml", "application/xml", "<file>\n  <token>"+token+"</token>\n</file>\n", apiCreds("jsmith"))
		if res.Status/100 != 2 {
			t.Fatalf("status %d %s", res.Status, res.Body)
		}
		if ct, id := lastAttachment(t, f); ct != "project" || id != 1 {
			t.Errorf("container %s %d", ct, id)
		}
	})

	t.Run("POST /projects/:project_id/files.json should refuse requests without the :token parameter", func(t *testing.T) {
		f := newContentFixture(t)
		// Redmine のテストの本文は末尾のカンマで不正な JSON（パースエラーでも 400）
		res := apiCall(t, f.ts, http.MethodPost, "/projects/1/files.json", "application/json", `{ "file": { "filename": "project_file.zip", } }`, apiCreds("jsmith"))
		res.expectStatus(t, 400)
		// 正しい JSON でトークンが無い場合は render :status => :bad_request が API テンプレート（files/create.api.rsb）を
		// 見つけられず、Redmine の rescue_from ActionView::MissingTemplate で 404 になる
		res = apiCall(t, f.ts, http.MethodPost, "/projects/1/files.json", "application/json", `{ "file": { "filename": "project_file.zip" } }`, apiCreds("jsmith"))
		res.expectStatus(t, 404)
	})

	t.Run("POST /projects/:project_id/files.json should accept :filename, :description, :content_type as optional parameters", func(t *testing.T) {
		f := newContentFixture(t)
		token := f.upload(t, "xml", "File content", apiCreds("jsmith"))
		res := apiCall(t, f.ts, http.MethodPost, "/projects/1/files.json", "application/json",
			`{ "file": { "filename": "New filename", "description": "New description", "content_type": "application/txt", "token": "`+token+`" } }`, apiCreds("jsmith"))
		if res.Status/100 != 2 {
			t.Fatalf("status %d %s", res.Status, res.Body)
		}
		id := f.int(t, `SELECT MAX(id) FROM attachments`)
		if s := f.str(t, `SELECT filename FROM attachments WHERE id = ?`, id); s != "New filename" {
			t.Errorf("filename %q", s)
		}
		if s := f.str(t, `SELECT description FROM attachments WHERE id = ?`, id); s != "New description" {
			t.Errorf("description %q", s)
		}
		if s := f.str(t, `SELECT content_type FROM attachments WHERE id = ?`, id); s != "application/txt" {
			t.Errorf("content_type %q", s)
		}
	})

	t.Run("POST /projects/:project_id/files.json should accept :version_id to attach the files to a version", func(t *testing.T) {
		f := newContentFixture(t)
		token := f.upload(t, "xml", "File content", apiCreds("jsmith"))
		apiCall(t, f.ts, http.MethodPost, "/projects/1/files.json", "application/json",
			`{ "file": { "version_id": 3, "filename": "New filename", "description": "New description", "token": "`+token+`" } }`, apiCreds("jsmith"))
		if ct, id := lastAttachment(t, f); ct != "version" || id != 3 {
			t.Errorf("container %s %d", ct, id)
		}
	})
}
