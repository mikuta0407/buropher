// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// helloDigest は "hello" の SHA256。
const helloDigest = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"

var csrfMetaTokenRe = regexp.MustCompile(`<meta name="csrf-token" content="([^"]+)"`)

func upload(t *testing.T, c *http.Client, u, contentType, body string, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, u, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if user, ok := hdr["basic"]; ok {
		req.Header.Del("basic")
		req.SetBasicAuth(user, user)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := readUnbranded(res.Body)
	return res, string(b)
}

// TestUploads は attachments#upload（POST /uploads.json|xml|js）と download を確認する。
// 期待値は参照 Redmine に同じリクエストを送った応答（id 以外は同一）。
func TestUploads(t *testing.T) {
	ts, d := newFixtureServer(t)
	nextID := func() int64 {
		var n int64
		if err := d.Get(context.Background(), &n, `SELECT COALESCE(MAX(id), 0) + 1 FROM attachments`); err != nil {
			t.Fatal(err)
		}
		return n
	}
	api := map[string]string{"basic": "jsmith"}

	t.Run("json", func(t *testing.T) {
		id := nextID()
		res, body := upload(t, newClient(t), ts.URL+"/uploads.json?filename=a.txt", "application/octet-stream", "hello", api)
		if res.StatusCode != 201 || res.Header.Get("Content-Type") != "application/json; charset=utf-8" {
			t.Fatalf("status %d %s", res.StatusCode, res.Header.Get("Content-Type"))
		}
		want := fmt.Sprintf(`{"upload":{"id":%d,"token":"%d.%s"}}`, id, id, helloDigest)
		if body != want {
			t.Errorf("got %s\nwant %s", body, want)
		}
	})

	t.Run("xml", func(t *testing.T) {
		id := nextID()
		res, body := upload(t, newClient(t), ts.URL+"/uploads.xml?filename=a.txt", "application/octet-stream", "hello", api)
		if res.StatusCode != 201 || res.Header.Get("Content-Type") != "application/xml; charset=utf-8" {
			t.Fatalf("status %d %s", res.StatusCode, res.Header.Get("Content-Type"))
		}
		want := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><upload><id>%d</id><token>%d.%s</token></upload>`, id, id, helloDigest)
		if body != want {
			t.Errorf("got %s\nwant %s", body, want)
		}
	})

	t.Run("not octet-stream", func(t *testing.T) {
		res, _ := upload(t, newClient(t), ts.URL+"/uploads.json?filename=a.txt", "text/plain", "hello", api)
		if res.StatusCode != 406 {
			t.Errorf("status %d", res.StatusCode)
		}
	})

	t.Run("validation error", func(t *testing.T) {
		res, body := upload(t, newClient(t), ts.URL+"/uploads.json?filename="+strings.Repeat("a", 260)+".txt", "application/octet-stream", "hello", api)
		if res.StatusCode != 422 || body != `{"errors":["File is too long (maximum is 255 characters)"]}` {
			t.Errorf("status %d %s", res.StatusCode, body)
		}
	})

	t.Run("js", func(t *testing.T) {
		c := login(t, ts, "jsmith", "jsmith")
		_, page := get(t, c, ts.URL+"/")
		m := csrfMetaTokenRe.FindStringSubmatch(page)
		if m == nil {
			t.Fatal("csrf-token meta not found")
		}
		hdr := map[string]string{"X-CSRF-Token": m[1], "X-Requested-With": "XMLHttpRequest", "Accept": "text/javascript, application/javascript"}

		id := nextID()
		res, body := upload(t, c, ts.URL+"/uploads.js?attachment_id=1&filename=t%C3%A9st.txt&content_type=", "application/octet-stream", "hello", hdr)
		if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/javascript; charset=utf-8" {
			t.Fatalf("status %d %s", res.StatusCode, res.Header.Get("Content-Type"))
		}
		want := fmt.Sprintf(`var fileSpan = $('#attachments_1');
fileSpan.find('input.token').val('%d.%s');
fileSpan.find('a.remove-upload')
  .attr({
    "data-remote": true,
    "data-method": 'delete',
    href: '/attachments/%d.js?attachment_id=1'
  })
  .off('click');
`, id, helloDigest, id)
		if body != want {
			t.Errorf("got:\n%s\nwant:\n%s", body, want)
		}

		// attachment_id はエスケープされる（j の後に HTML エスケープ）
		id2 := nextID()
		_, body = upload(t, c, ts.URL+"/uploads.js?attachment_id=a%26b&filename=x.txt", "application/octet-stream", "hello", hdr)
		want2 := fmt.Sprintf(`var fileSpan = $('#attachments_a&amp;b');
fileSpan.find('input.token').val('%d.%s');
fileSpan.find('a.remove-upload')
  .attr({
    "data-remote": true,
    "data-method": 'delete',
    href: '/attachments/%d.js?attachment_id=a%%26b'
  })
  .off('click');
`, id2, helloDigest, id2)
		if body != want2 {
			t.Errorf("got:\n%s\nwant:\n%s", body, want2)
		}

		// 検証エラー
		_, body = upload(t, c, ts.URL+"/uploads.js?attachment_id=3&filename="+strings.Repeat("a", 260)+"'x.txt", "application/octet-stream", "hello", hdr)
		wantErr := `var fileSpan = $('#attachments_3');
  fileSpan.hide();
  alert("File is too long (maximum is 255 characters)");
`
		if body != wantErr {
			t.Errorf("got:\n%s\nwant:\n%s", body, wantErr)
		}

		// ダウンロード（未紐付けの添付は作成者のみ）
		path := fmt.Sprintf("%s/attachments/download/%d/t%%C3%%A9st.txt", ts.URL, id)
		res, body = get(t, c, path)
		if res.StatusCode != 200 || body != "hello" {
			t.Fatalf("download: status %d %q", res.StatusCode, body)
		}
		for k, v := range map[string]string{
			"Content-Type":              "text/plain",
			"Content-Disposition":       `attachment; filename="test.txt"; filename*=UTF-8''t%C3%A9st.txt`,
			"Content-Transfer-Encoding": "binary",
			"Content-Security-Policy":   "default-src 'none'; style-src 'unsafe-inline'; sandbox",
			"Etag":                      `W/"ebde1b934fa81da163dcf4b7d7cfe18e"`,
		} {
			if got := res.Header.Get(k); got != v {
				t.Errorf("%s: %q, want %q", k, got, v)
			}
		}
		if res, _ := get(t, c, fmt.Sprintf("%s/attachments/download/%d", ts.URL, id)); res.StatusCode != 200 {
			t.Errorf("download without filename: %d", res.StatusCode)
		}
		if res, _ := get(t, c, fmt.Sprintf("%s/attachments/download/%d/other.txt", ts.URL, id)); res.StatusCode != 404 {
			t.Errorf("wrong filename: %d", res.StatusCode)
		}
		res, _ = get(t, newClient(t), fmt.Sprintf("%s/attachments/download/%d", ts.URL, id))
		if res.StatusCode != 302 || !strings.HasPrefix(res.Header.Get("Location"), ts.URL+"/login?back_url=") {
			t.Errorf("anonymous: %d %s", res.StatusCode, res.Header.Get("Location"))
		}
		res, _ = get(t, login(t, ts, "admin", "admin"), fmt.Sprintf("%s/attachments/download/%d", ts.URL, id))
		if res.StatusCode != 403 {
			t.Errorf("other user: %d", res.StatusCode)
		}
	})

	t.Run("js without csrf token", func(t *testing.T) {
		// CSRF トークンが無ければ 422（handle_unverified_request はログアウトさせる）
		res, _ := upload(t, login(t, ts, "jsmith", "jsmith"), ts.URL+"/uploads.js?attachment_id=1&filename=x.txt",
			"application/octet-stream", "hello", map[string]string{"X-Requested-With": "XMLHttpRequest"})
		if res.StatusCode != 422 {
			t.Errorf("status %d", res.StatusCode)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		// フィクスチャの添付はディスクにファイルが無いので 404（file_readable）
		res, _ := get(t, login(t, ts, "admin", "admin"), ts.URL+"/attachments/download/1/error281.txt")
		if res.StatusCode != 404 {
			t.Errorf("status %d", res.StatusCode)
		}
	})
}
