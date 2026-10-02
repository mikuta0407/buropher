package server_test

// test/integration/api_test/news_test.rb の移植。

import (
	"net/http"
	"strings"
	"testing"
)

func TestAPINews(t *testing.T) {
	f := newContentFixture(t)
	ts := f.ts

	t.Run("GET /news.xml should return news", func(t *testing.T) {
		res := apiGet(t, ts, "/news.xml")
		res.expectStatus(t, 200)
		assertXMLText(t, res.XML(t), "news[type=array] news id", "2")
	})

	t.Run("GET /news.json should return news", func(t *testing.T) {
		m := apiGet(t, ts, "/news.json").JSON(t)
		assertJSON(t, m, "news.0.id", "2")
	})

	t.Run("GET /projects/:project_id/news.xml should return news", func(t *testing.T) {
		assertXMLText(t, apiGet(t, ts, "/projects/ecookbook/news.xml").XML(t), "news[type=array] news id", "2")
	})

	t.Run("GET /projects/:project_id/news.json should return news", func(t *testing.T) {
		assertJSON(t, apiGet(t, ts, "/projects/ecookbook/news.json").JSON(t), "news.0.id", "2")
	})

	t.Run("GET /news/:id.xml", func(t *testing.T) {
		res := apiGet(t, ts, "/news/1.xml")
		res.expectStatus(t, 200)
		if res.ContentType() != "application/xml" {
			t.Errorf("content type %s", res.ContentType())
		}
		x := res.XML(t)
		assertXMLText(t, x, "news > id", "1")
		assertXMLCount(t, x, `news > project[id="1"][name="eCookbook"]`, 1)
		assertXMLCount(t, x, `news > author[id="2"][name="John Smith"]`, 1)
		assertXMLText(t, x, "news > title", "eCookbook first release !")
		assertXMLText(t, x, "news > summary", "First version was released...")
		// Redmine のテストは改行を区別しない比較（フィクスチャの本文は "released.\n\nVisit"）
		if got := strings.Join(strings.Fields(nodeText(xmlSelect(t, x, "news > description")[0])), " "); got != "eCookbook 1.0 has been released. Visit http://ecookbook.somenet.foo/" {
			t.Errorf("description %q", got)
		}
		created := f.str(t, `SELECT created_at FROM news WHERE id = 1`)
		if got := nodeText(xmlSelect(t, x, "news > created_on")[0]); !strings.HasPrefix(strings.Replace(created, " ", "T", 1), strings.TrimSuffix(got, "Z")) {
			t.Errorf("created_on %s (db %s)", got, created)
		}
	})

	t.Run("GET /news/:id.json", func(t *testing.T) {
		res := apiGet(t, ts, "/news/1.json")
		res.expectStatus(t, 200)
		if res.ContentType() != "application/json" {
			t.Errorf("content type %s", res.ContentType())
		}
		assertJSON(t, res.JSON(t), "news.id", "1")
	})

	t.Run("GET /news/:id.xml with attachments", func(t *testing.T) {
		f := newContentFixture(t)
		f.writeAttachmentFile(t, 1, []byte("x"))
		f.exec(t, `UPDATE attachments SET container_kind = 'news', container_id = 1 WHERE id = 1`)
		x := apiGet(t, f.ts, "/news/1.xml?include=attachments").XML(t)
		assertXMLText(t, x, "news attachments[type=array] attachment > id", "1")
		assertXMLText(t, x, "news attachments[type=array] attachment > id ~ filename", "error281.txt")
		assertXMLText(t, x, "news attachments[type=array] attachment > id ~ content_url", f.ts.URL+"/attachments/download/1/error281.txt")
	})

	t.Run("GET /news/:id.xml with comments", func(t *testing.T) {
		x := apiGet(t, ts, "/news/1.xml?include=comments").XML(t)
		assertXMLCount(t, x, "news comments[type=array] comment", 2)
		assertXMLCount(t, x, `news comments comment[id="1"] author[id="1"][name="Redmine Admin"]`, 1)
		assertXMLText(t, x, `news comments comment[id="1"] content`, "my first comment")
		assertXMLCount(t, x, `news comments comment[id="2"] author[id="2"][name="John Smith"]`, 1)
		assertXMLText(t, x, `news comments comment[id="2"] content`, "This is an other comment")
	})

	// newsCreated は title のニュースの件数・summary・description・author_id・project_id を確認する。
	newsCreated := func(t *testing.T, f *contentFixture, title, summary, desc string) {
		t.Helper()
		if n := f.int(t, `SELECT COUNT(*) FROM news WHERE title = ?`, title); n != 1 {
			t.Fatalf("news %q: %d", title, n)
		}
		if s := f.str(t, `SELECT summary FROM news WHERE title = ?`, title); s != summary {
			t.Errorf("summary %q", s)
		}
		if s := f.str(t, `SELECT description FROM news WHERE title = ?`, title); s != desc {
			t.Errorf("description %q", s)
		}
		if n := f.int(t, `SELECT author_id FROM news WHERE title = ?`, title); n != 2 {
			t.Errorf("author %d", n)
		}
		if n := f.int(t, `SELECT project_id FROM news WHERE title = ?`, title); n != 1 {
			t.Errorf("project %d", n)
		}
	}

	t.Run("POST /project/:project_id/news.xml should create a news with the attributes", func(t *testing.T) {
		f := newContentFixture(t)
		payload := `<?xml version="1.0" encoding="UTF-8" ?>
<news>
  <title>NewsXmlApiTest</title>
  <summary>News XML-API Test</summary>
  <description>This is the description</description>
</news>
`
		res := apiCall(t, f.ts, http.MethodPost, "/projects/1/news.xml", "application/xml", payload, apiCreds("jsmith"))
		res.expectStatus(t, 204)
		newsCreated(t, f, "NewsXmlApiTest", "News XML-API Test", "This is the description")
	})

	t.Run("POST /project/:project_id/news.xml with failure should return errors", func(t *testing.T) {
		before := f.int(t, `SELECT COUNT(*) FROM news`)
		res := apiCall(t, ts, http.MethodPost, "/projects/1/news.xml", cFormType, cForm("news[title]", ""), apiCreds("jsmith"))
		res.expectStatus(t, 422)
		assertXMLText(t, res.XML(t), "errors error", "Title cannot be blank")
		if n := f.int(t, `SELECT COUNT(*) FROM news`); n != before {
			t.Error("news created")
		}
	})

	t.Run("POST /project/:project_id/news.json should create a news with the attributes", func(t *testing.T) {
		f := newContentFixture(t)
		payload := `{"news": {"title": "NewsJsonApiTest", "summary": "News JSON-API Test", "description": "This is the description"}}`
		apiCall(t, f.ts, http.MethodPost, "/projects/1/news.json", "application/json", payload, apiCreds("jsmith")).expectStatus(t, 204)
		newsCreated(t, f, "NewsJsonApiTest", "News JSON-API Test", "This is the description")
	})

	t.Run("POST /project/:project_id/news.json with failure should return errors", func(t *testing.T) {
		res := apiCall(t, ts, http.MethodPost, "/projects/1/news.json", cFormType, cForm("news[title]", ""), apiCreds("jsmith"))
		res.expectStatus(t, 422)
		if !strings.Contains(res.Body, `"Title cannot be blank"`) {
			t.Errorf("body %s", res.Body)
		}
	})

	t.Run("POST /project/:project_id/news.xml with attachment should create a news with attachment", func(t *testing.T) {
		f := newContentFixture(t)
		token := f.upload(t, "xml", "test_create_with_attachment", apiCreds("jsmith"))
		attID, _, _ := strings.Cut(token, ".")
		res := apiCall(t, f.ts, http.MethodPost, "/projects/1/news.xml", cFormType, cForm(
			"news[title]", "News XML-API with Attachment", "news[description]", "desc",
			"attachments[][token]", token, "attachments[][filename]", "test.txt", "attachments[][content_type]", "text/plain",
		), apiCreds("jsmith"))
		res.expectStatus(t, 204)
		newsID := f.int(t, `SELECT id FROM news WHERE title = 'News XML-API with Attachment'`)
		if n := f.int(t, `SELECT COUNT(*) FROM attachments WHERE id = ? AND container_kind = 'news' AND container_id = ?`, attID, newsID); n != 1 {
			t.Error("attachment not attached")
		}
		if s := f.str(t, `SELECT filename FROM attachments WHERE id = ?`, attID); s != "test.txt" {
			t.Errorf("filename %q", s)
		}
		if s := f.str(t, `SELECT content_type FROM attachments WHERE id = ?`, attID); s != "text/plain" {
			t.Errorf("content_type %q", s)
		}
		if n := f.int(t, `SELECT filesize FROM attachments WHERE id = ?`, attID); n != int64(len("test_create_with_attachment")) {
			t.Errorf("filesize %d", n)
		}
		if n := f.int(t, `SELECT author_id FROM attachments WHERE id = ?`, attID); n != 2 {
			t.Errorf("author %d", n)
		}
	})

	uploadsXML := func(title, extra, t1, t2 string) string {
		return `<?xml version="1.0" encoding="UTF-8" ?>
<news>
  <title>` + title + `</title>` + extra + `
  <uploads type="array">
    <upload>
      <token>` + t1 + `</token>
      <filename>test1.txt</filename>
    </upload>
    <upload>
      <token>` + t2 + `</token>
      <filename>test2.txt</filename>
    </upload>
  </uploads>
</news>
`
	}
	uploadsJSON := func(title, extra, t1, t2 string) string {
		return `{"news": {"title": "` + title + `",` + extra + ` "uploads": [{"token": "` + t1 + `", "filename": "test1.txt"}, {"token": "` + t2 + `", "filename": "test2.txt"}]}}`
	}
	newsAttachments := func(t *testing.T, f *contentFixture, title string) int64 {
		return f.int(t, `SELECT COUNT(*) FROM attachments a JOIN news n ON a.container_kind = 'news' AND a.container_id = n.id WHERE n.title = ?`, title)
	}

	t.Run("POST /project/:project_id/news.xml with multiple attachment should create a news with attachments", func(t *testing.T) {
		f := newContentFixture(t)
		t1 := f.upload(t, "xml", "File content 1", apiCreds("jsmith"))
		t2 := f.upload(t, "xml", "File content 2", apiCreds("jsmith"))
		title := "News XML-API with attachments"
		apiCall(t, f.ts, http.MethodPost, "/projects/1/news.xml", "application/xml",
			uploadsXML(title, "\n  <description>News with multiple attachments</description>", t1, t2), apiCreds("jsmith")).expectStatus(t, 204)
		if n := newsAttachments(t, f, title); n != 2 {
			t.Errorf("attachments %d", n)
		}
	})

	t.Run("POST /project/:project_id/news.json with multiple attachment should create a news with attachments", func(t *testing.T) {
		f := newContentFixture(t)
		t1 := f.upload(t, "json", "File content 1", apiCreds("jsmith"))
		t2 := f.upload(t, "json", "File content 2", apiCreds("jsmith"))
		title := "News JSON-API with attachments"
		apiCall(t, f.ts, http.MethodPost, "/projects/1/news.json", "application/json",
			uploadsJSON(title, ` "description": "News with multiple attachments",`, t1, t2), apiCreds("jsmith")).expectStatus(t, 204)
		if n := newsAttachments(t, f, title); n != 2 {
			t.Errorf("attachments %d", n)
		}
	})

	newsUpdated := func(t *testing.T, f *contentFixture, title, summary, desc string) {
		t.Helper()
		if s := f.str(t, `SELECT title FROM news WHERE id = 1`); s != title {
			t.Errorf("title %q", s)
		}
		if s := f.str(t, `SELECT summary FROM news WHERE id = 1`); s != summary {
			t.Errorf("summary %q", s)
		}
		if s := f.str(t, `SELECT description FROM news WHERE id = 1`); s != desc {
			t.Errorf("description %q", s)
		}
	}

	t.Run("PUT /news/:id.xml", func(t *testing.T) {
		f := newContentFixture(t)
		payload := `<?xml version="1.0" encoding="UTF-8" ?>
<news>
  <title>NewsUpdateXmlApiTest</title>
  <summary>News Update XML-API Test</summary>
  <description>update description via xml api</description>
</news>
`
		apiCall(t, f.ts, http.MethodPut, "/news/1.xml", "application/xml", payload, apiCreds("jsmith")).expectStatus(t, 204)
		newsUpdated(t, f, "NewsUpdateXmlApiTest", "News Update XML-API Test", "update description via xml api")
	})

	t.Run("PUT /news/:id.json", func(t *testing.T) {
		f := newContentFixture(t)
		payload := `{"news": {"title": "NewsUpdateJsonApiTest", "summary": "News Update JSON-API Test", "description": "update description via json api"}}`
		apiCall(t, f.ts, http.MethodPut, "/news/1.json", "application/json", payload, apiCreds("jsmith")).expectStatus(t, 204)
		newsUpdated(t, f, "NewsUpdateJsonApiTest", "News Update JSON-API Test", "update description via json api")
	})

	t.Run("PUT /news/:id.xml with failed update", func(t *testing.T) {
		res := apiCall(t, ts, http.MethodPut, "/news/1.xml", cFormType, cForm("news[title]", ""), apiCreds("jsmith"))
		res.expectStatus(t, 422)
		assertXMLText(t, res.XML(t), "errors error", "Title cannot be blank")
	})

	t.Run("PUT /news/:id.json with failed update", func(t *testing.T) {
		res := apiCall(t, ts, http.MethodPut, "/news/1.json", cFormType, cForm("news[title]", ""), apiCreds("jsmith"))
		res.expectStatus(t, 422)
		if !strings.Contains(res.Body, `"Title cannot be blank"`) {
			t.Errorf("body %s", res.Body)
		}
	})

	t.Run("PUT /news/:id.xml with multiple attachment should update a news with attachments", func(t *testing.T) {
		f := newContentFixture(t)
		t1 := f.upload(t, "xml", "File content 1", apiCreds("jsmith"))
		t2 := f.upload(t, "xml", "File content 2", apiCreds("jsmith"))
		title := "News Update XML-API with attachments"
		apiCall(t, f.ts, http.MethodPut, "/news/1.xml", "application/xml", uploadsXML(title, "", t1, t2), apiCreds("jsmith")).expectStatus(t, 204)
		if n := newsAttachments(t, f, title); n != 2 {
			t.Errorf("attachments %d", n)
		}
	})

	t.Run("PUT /news/:id.json with multiple attachment should update a news with attachments", func(t *testing.T) {
		f := newContentFixture(t)
		t1 := f.upload(t, "json", "File content 1", apiCreds("jsmith"))
		t2 := f.upload(t, "json", "File content 2", apiCreds("jsmith"))
		title := "News Update JSON-API with attachments"
		apiCall(t, f.ts, http.MethodPut, "/news/1.json", "application/json", uploadsJSON(title, "", t1, t2), apiCreds("jsmith")).expectStatus(t, 204)
		if n := newsAttachments(t, f, title); n != 2 {
			t.Errorf("attachments %d", n)
		}
	})

	for _, format := range []string{"xml", "json"} {
		t.Run("DELETE /news/:id."+format, func(t *testing.T) {
			f := newContentFixture(t)
			before := f.int(t, `SELECT COUNT(*) FROM news`)
			res := apiCall(t, f.ts, http.MethodDelete, "/news/1."+format, "", "", apiCreds("jsmith"))
			res.expectStatus(t, 204)
			if res.Body != "" {
				t.Errorf("body %q", res.Body)
			}
			if n := f.int(t, `SELECT COUNT(*) FROM news`); n != before-1 {
				t.Errorf("count %d", n)
			}
			if n := f.int(t, `SELECT COUNT(*) FROM news WHERE id = 1`); n != 0 {
				t.Error("news 1 remains")
			}
		})
	}
}
