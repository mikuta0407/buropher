// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// TestWikiAddAttachment はアップロードのトークンで Wiki ページに添付を追加し、表示されることを確認する。
func TestWikiAddAttachment(t *testing.T) {
	ts, d := newFixtureServer(t)
	res, body := upload(t, newClient(t), ts.URL+"/uploads.json?filename=note.txt", "application/octet-stream", "hello", map[string]string{"basic": "admin"})
	if res.StatusCode != 201 {
		t.Fatalf("upload: %d %s", res.StatusCode, body)
	}
	m := regexp.MustCompile(`"token":"([^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("token: %s", body)
	}
	c := login(t, ts, "admin", "admin")
	res, _ = post(t, c, ts.URL+"/projects/ecookbook/wiki/Another_page/add_attachment", wikiForm(t, c, ts,
		"attachments[1][token]", m[1], "attachments[1][description]", "a note"))
	if res.StatusCode != 302 || res.Header.Get("Location") != ts.URL+"/projects/ecookbook/wiki/Another_page" {
		t.Fatalf("add_attachment: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	var kind string
	var cid int64
	if err := d.QueryRow(context.Background(), `SELECT container_kind, container_id FROM attachments WHERE filename = 'note.txt'`).Scan(&kind, &cid); err != nil {
		t.Fatal(err)
	}
	if kind != "wiki_page" || cid != 2 {
		t.Errorf("container = %s %d", kind, cid)
	}
	_, body = get(t, c, ts.URL+"/projects/ecookbook/wiki/Another_page")
	if !strings.Contains(body, `<span class="icon-label">note.txt</span></a>    <span class="size">(5 Bytes)</span>`) ||
		!strings.Contains(body, "<td>a note</td>") || !strings.Contains(body, "Files (1)") {
		t.Errorf("attachment not listed: %s", extract(body, `<div class="attachments">`, `</table>`))
	}
	// 編集フォームで削除にチェックした添付は保存時に消える
	_, body = get(t, c, ts.URL+"/projects/ecookbook/wiki/Another_page/edit")
	if !strings.Contains(body, `name="wiki_page[deleted_attachment_ids][]"`) {
		t.Fatalf("existing attachments block missing")
	}
	var id string
	if err := d.Get(context.Background(), &id, `SELECT CAST(id AS TEXT) FROM attachments WHERE filename = 'note.txt'`); err != nil {
		t.Fatal(err)
	}
	res, _ = post(t, c, ts.URL+"/projects/ecookbook/wiki/Another_page", wikiForm(t, c, ts,
		"_method", "put", "content[text]", "changed", "content[version]", "1", "wiki_page[deleted_attachment_ids][]", id))
	if res.StatusCode != 302 {
		t.Fatalf("update: %d", res.StatusCode)
	}
	var n int
	if err := d.Get(context.Background(), &n, `SELECT COUNT(*) FROM attachments WHERE filename = 'note.txt'`); err != nil || n != 0 {
		t.Errorf("attachment should be deleted: %d %v", n, err)
	}
}
