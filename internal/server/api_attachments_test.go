// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

// test/integration/api_test/attachments_test.rb の移植。

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/server"
)

// contentFixture は API テスト（添付・ファイル・ニュース・Wiki・工数・検索・リポジトリ）用のフィクスチャサーバ。
type contentFixture struct {
	srv *server.Server
	ts  *httptest.Server
	d   *db.DB
}

func newContentFixture(t *testing.T) *contentFixture {
	t.Helper()
	srv, ts, d := newFixtureServerFull(t)
	return &contentFixture{srv: srv, ts: ts, d: d}
}

// int は SQL の結果（整数 1 つ）を返す。
func (f *contentFixture) int(t *testing.T, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := f.d.Get(context.Background(), &n, q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// str は SQL の結果（文字列 1 つ。NULL は ""）を返す。
func (f *contentFixture) str(t *testing.T, q string, args ...any) string {
	t.Helper()
	var s *string
	if err := f.d.Get(context.Background(), &s, q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	if s == nil {
		return ""
	}
	return *s
}

// exec は SQL を実行する（テストの前提条件の変更用）。
func (f *contentFixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := f.d.Exec(context.Background(), q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// setting は Setting の値を変える（with_settings 相当。サーバのキャッシュも更新する）。
func (f *contentFixture) setting(t *testing.T, name string, v any) {
	t.Helper()
	if err := f.srv.App().Settings.Set(context.Background(), name, v); err != nil {
		t.Fatal(err)
	}
}

// writeAttachmentFile はフィクスチャの添付のファイルを保存先に置く（set_fixtures_attachments_directory 相当）。
func (f *contentFixture) writeAttachmentFile(t *testing.T, id int64, content []byte) {
	t.Helper()
	dir := f.str(t, `SELECT disk_directory FROM attachments WHERE id = ?`, id)
	name := f.str(t, `SELECT disk_filename FROM attachments WHERE id = ?`, id)
	p := filepath.Join(f.srv.App().AttachmentStore.Root, dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

// upload は POST /uploads.<format> でファイルを上げてトークンを返す（xml_upload / json_upload）。
func (f *contentFixture) upload(t *testing.T, format, content string, opts ...apiOpt) string {
	t.Helper()
	res := rawUpload(t, f.ts, "/uploads."+format, content, opts...)
	res.expectStatus(t, http.StatusCreated)
	if format == "xml" {
		return nodeText(xmlSelect(t, res.XML(t), "upload token")[0])
	}
	tok, _ := jsonPath(res.JSON(t), "upload.token")
	return tok.(string)
}

// rawUpload は application/octet-stream の本文を POST する（本文が空でも Content-Type を付ける）。
func rawUpload(t *testing.T, ts *httptest.Server, path, content string, opts ...apiOpt) apiResp {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(content))
	req.Header.Set("Content-Type", "application/octet-stream")
	for _, o := range opts {
		o(req)
	}
	hr, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer hr.Body.Close()
	b, _ := readUnbranded(hr.Body)
	return apiResp{Status: hr.StatusCode, Header: hr.Header, Body: string(b)}
}

// form は Rails のフォーム形式（application/x-www-form-urlencoded）の本文。
func cForm(kv ...string) string {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Add(kv[i], kv[i+1])
	}
	return v.Encode()
}

const cFormType = "application/x-www-form-urlencoded"

func TestAPIAttachments(t *testing.T) {
	f := newContentFixture(t)
	ts := f.ts
	f.writeAttachmentFile(t, 7, []byte("PK\x05\x06"+strings.Repeat("\x00", 18)))
	var pngBuf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{255, 0, 0, 255})
	_ = png.Encode(&pngBuf, img)
	f.writeAttachmentFile(t, 16, pngBuf.Bytes())

	t.Run("GET /attachments/:id.xml should return the attachment", func(t *testing.T) {
		res := apiGet(t, ts, "/attachments/7.xml", apiCreds("jsmith"))
		res.expectStatus(t, 200)
		if res.ContentType() != "application/xml" {
			t.Errorf("content type %s", res.ContentType())
		}
		x := res.XML(t)
		assertXMLText(t, x, "attachment > id", "7")
		assertXMLText(t, x, "attachment > id ~ filename", "archive.zip")
		assertXMLText(t, x, "attachment > id ~ content_url", ts.URL+"/attachments/download/7/archive.zip")
	})

	t.Run("GET /attachments/:id.xml for image should include thumbnail_url", func(t *testing.T) {
		res := apiGet(t, ts, "/attachments/16.xml", apiCreds("jsmith"))
		res.expectStatus(t, 200)
		assertXMLText(t, res.XML(t), "attachment > id ~ thumbnail_url", ts.URL+"/attachments/thumbnail/16")
	})

	t.Run("GET /attachments/:id.xml should deny access without credentials", func(t *testing.T) {
		apiGet(t, ts, "/attachments/7.xml").expectStatus(t, 401)
	})

	t.Run("GET /attachments/download/:id/:filename should return the attachment content", func(t *testing.T) {
		res := apiGet(t, ts, "/attachments/download/7/archive.zip", apiCreds("jsmith"))
		res.expectStatus(t, 200)
		if res.ContentType() != "application/zip" {
			t.Errorf("content type %s", res.ContentType())
		}
	})

	t.Run("GET /attachments/download/:id/:filename should deny access without credentials", func(t *testing.T) {
		apiGet(t, ts, "/attachments/download/7/archive.zip").expectStatus(t, 302)
	})

	t.Run("GET /attachments/thumbnail/:id should return the thumbnail", func(t *testing.T) {
		apiGet(t, ts, "/attachments/thumbnail/16", apiCreds("jsmith")).expectStatus(t, 200)
	})

	for _, format := range []string{"xml", "json"} {
		t.Run("DELETE /attachments/:id."+format+" should return ok and delete Attachment", func(t *testing.T) {
			f := newContentFixture(t)
			before := f.int(t, `SELECT COUNT(*) FROM attachments`)
			res := apiCall(t, f.ts, http.MethodDelete, "/attachments/7."+format, "", "", apiCreds("jsmith"))
			res.expectStatus(t, 204)
			if res.Body != "" {
				t.Errorf("body %q", res.Body)
			}
			if n := f.int(t, `SELECT COUNT(*) FROM attachments`); n != before-1 {
				t.Errorf("count %d, want %d", n, before-1)
			}
			if n := f.int(t, `SELECT COUNT(*) FROM attachments WHERE id = 7`); n != 0 {
				t.Error("attachment 7 not deleted")
			}
		})
	}

	t.Run("PATCH /attachments/:id.json should update the attachment", func(t *testing.T) {
		f := newContentFixture(t)
		res := apiCall(t, f.ts, http.MethodPatch, "/attachments/7.json", cFormType,
			cForm("attachment[filename]", "renamed.zip", "attachment[description]", "updated"), apiCreds("jsmith"))
		res.expectStatus(t, 204)
		if ct := res.Header.Get("Content-Type"); ct != "" {
			t.Errorf("content type %q", ct)
		}
		if s := f.str(t, `SELECT filename FROM attachments WHERE id = 7`); s != "renamed.zip" {
			t.Errorf("filename %q", s)
		}
		if s := f.str(t, `SELECT description FROM attachments WHERE id = 7`); s != "updated" {
			t.Errorf("description %q", s)
		}
	})

	t.Run("PATCH /attachments/:id.json with failure should return the errors", func(t *testing.T) {
		res := apiCall(t, ts, http.MethodPatch, "/attachments/7.json", cFormType,
			cForm("attachment[filename]", "", "attachment[description]", "updated"), apiCreds("jsmith"))
		res.expectStatus(t, 422)
		if res.ContentType() != "application/json" {
			t.Errorf("content type %s", res.ContentType())
		}
		if !strings.Contains(res.Body, `"File cannot be blank"`) {
			t.Errorf("body %s", res.Body)
		}
	})

	t.Run("POST /uploads.xml should return the token", func(t *testing.T) {
		f := newContentFixture(t)
		before := f.int(t, `SELECT COUNT(*) FROM attachments`)
		res := apiCall(t, f.ts, http.MethodPost, "/uploads.xml", "application/octet-stream", "File content", apiCreds("jsmith"))
		res.expectStatus(t, 201)
		if res.ContentType() != "application/xml" {
			t.Errorf("content type %s", res.ContentType())
		}
		if n := f.int(t, `SELECT COUNT(*) FROM attachments`); n != before+1 {
			t.Fatalf("count %d", n)
		}
		x := res.XML(t)
		token := nodeText(xmlSelect(t, x, "upload > token")[0])
		id := nodeText(xmlSelect(t, x, "upload > id")[0])
		lastID := f.int(t, `SELECT MAX(id) FROM attachments`)
		if id == "" || id != cItoa(lastID) {
			t.Errorf("id %q, last %d", id, lastID)
		}
		digest := f.str(t, `SELECT digest FROM attachments WHERE id = ?`, lastID)
		if token != cItoa(lastID)+"."+digest {
			t.Errorf("token %q", token)
		}
		if n := f.int(t, `SELECT COUNT(*) FROM attachments WHERE id = ? AND container_id IS NULL AND author_id = 2 AND filesize = 12`, lastID); n != 1 {
			t.Error("container / author / filesize mismatch")
		}
		if ct := f.str(t, `SELECT content_type FROM attachments WHERE id = ?`, lastID); ct != "" {
			t.Errorf("content_type %q", ct)
		}
		if fn := f.str(t, `SELECT filename FROM attachments WHERE id = ?`, lastID); fn == "" {
			t.Error("filename blank")
		}
		disk := f.str(t, `SELECT disk_filename FROM attachments WHERE id = ?`, lastID)
		if !regexp.MustCompile(`\d+_[0-9a-z]+`).MatchString(disk) {
			t.Errorf("disk_filename %q", disk)
		}
		dir := f.str(t, `SELECT disk_directory FROM attachments WHERE id = ?`, lastID)
		b, err := os.ReadFile(filepath.Join(f.srv.App().AttachmentStore.Root, dir, disk))
		if err != nil || string(b) != "File content" {
			t.Errorf("file %q %v", b, err)
		}
	})

	t.Run("POST /uploads.json should return the token", func(t *testing.T) {
		res := apiCall(t, ts, http.MethodPost, "/uploads.json", "application/octet-stream", "File content", apiCreds("jsmith"))
		res.expectStatus(t, 201)
		if res.ContentType() != "application/json" {
			t.Errorf("content type %s", res.ContentType())
		}
		tok, _ := jsonPath(res.JSON(t), "upload.token")
		lastID := f.int(t, `SELECT MAX(id) FROM attachments`)
		if tok != cItoa(lastID)+"."+f.str(t, `SELECT digest FROM attachments WHERE id = ?`, lastID) {
			t.Errorf("token %v", tok)
		}
	})

	t.Run("POST /uploads.xml should accept :filename param as the attachment filename", func(t *testing.T) {
		// 同じ内容のファイルは既存のファイルを再利用する（reuse_existing_file_if_possible）ため新しいサーバで確認する
		f := newContentFixture(t)
		ts := f.ts
		apiCall(t, ts, http.MethodPost, "/uploads.xml?filename=test.txt", "application/octet-stream", "File content", apiCreds("jsmith")).expectStatus(t, 201)
		lastID := f.int(t, `SELECT MAX(id) FROM attachments`)
		if s := f.str(t, `SELECT filename FROM attachments WHERE id = ?`, lastID); s != "test.txt" {
			t.Errorf("filename %q", s)
		}
		if s := f.str(t, `SELECT disk_filename FROM attachments WHERE id = ?`, lastID); !strings.HasSuffix(s, "_test.txt") {
			t.Errorf("disk_filename %q", s)
		}
	})

	t.Run("POST /uploads.xml should not accept other content types", func(t *testing.T) {
		before := f.int(t, `SELECT COUNT(*) FROM attachments`)
		apiCall(t, ts, http.MethodPost, "/uploads.xml", "image/png", "PNG DATA", apiCreds("jsmith")).expectStatus(t, 406)
		if n := f.int(t, `SELECT COUNT(*) FROM attachments`); n != before {
			t.Error("attachment created")
		}
	})

	t.Run("POST /uploads.xml should return errors if file is too big", func(t *testing.T) {
		f := newContentFixture(t)
		f.setting(t, "attachment_max_size", "1")
		before := f.int(t, `SELECT COUNT(*) FROM attachments`)
		res := apiCall(t, f.ts, http.MethodPost, "/uploads.xml", "application/octet-stream", strings.Repeat("x", 2048), apiCreds("jsmith"))
		res.expectStatus(t, 422)
		errs := xmlSelect(t, res.XML(t), "error")
		if len(errs) == 0 || !strings.Contains(nodeText(errs[0]), "exceeds the maximum allowed file size") {
			t.Errorf("body %s", res.Body)
		}
		if n := f.int(t, `SELECT COUNT(*) FROM attachments`); n != before {
			t.Error("attachment created")
		}
	})

	t.Run("POST /uploads.json should create an empty file and return a valid token", func(t *testing.T) {
		token := f.upload(t, "json", "", apiCreds("jsmith"))
		id, _, _ := strings.Cut(token, ".")
		if f.int(t, `SELECT filesize FROM attachments WHERE id = ?`, id) != 0 {
			t.Error("filesize")
		}
		if f.str(t, `SELECT digest FROM attachments WHERE id = ?`, id) == "" {
			t.Error("digest blank")
		}
		dir := f.str(t, `SELECT disk_directory FROM attachments WHERE id = ?`, id)
		disk := f.str(t, `SELECT disk_filename FROM attachments WHERE id = ?`, id)
		if _, err := os.Stat(filepath.Join(f.srv.App().AttachmentStore.Root, dir, disk)); err != nil {
			t.Error(err)
		}
	})

	// fcgi / uwsgi の入力（rack.input の差し替え）は Rack 固有なので、Content-Length 付きの通常の本文で確認する。
	t.Run("POST /uploads.json should be compatible with an fcgi's input", func(t *testing.T) {
		token := f.upload(t, "json", "File content", apiCreds("jsmith"))
		id, _, _ := strings.Cut(token, ".")
		if f.int(t, `SELECT filesize FROM attachments WHERE id = ?`, id) != 12 {
			t.Error("filesize")
		}
	})
}

func cItoa(n int64) string { return strconv.FormatInt(n, 10) }
