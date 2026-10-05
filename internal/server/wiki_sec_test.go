// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// TestWikiUpdateCrossWikiRedirectNeedsTargetPermission は別プロジェクトの Wiki へのリダイレクト
// （ページを他プロジェクトへ移動した跡）を経由して、権限の無いプロジェクトのページを
// 更新・プレビューできないことを確認する。
func TestWikiUpdateCrossWikiRedirectNeedsTargetPermission(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	// ecookbook の Moved_page → onlinestore（非公開、dlopper は非メンバー）の Start_page
	if _, err := d.Exec(ctx, `INSERT INTO wiki_redirects (wiki_id, title, redirects_to, redirects_to_wiki_id, created_at) VALUES (1, 'Moved_page', 'Start_page', 2, '2026-01-01T00:00:00.000000Z')`); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := d.Get(ctx, &before, `SELECT current_version FROM wiki_pages WHERE id = 3`); err != nil {
		t.Fatal(err)
	}
	c := login(t, ts, "dlopper", "foo")
	res, _ := post(t, c, ts.URL+"/projects/ecookbook/wiki/Moved_page", wikiForm(t, c, ts,
		"_method", "put", "content[text]", "pwned", "content[version]", strconv.Itoa(before)))
	if res.StatusCode != 403 {
		t.Errorf("update through cross-wiki redirect: status %d, want 403", res.StatusCode)
	}
	var after int
	if err := d.Get(ctx, &after, `SELECT current_version FROM wiki_pages WHERE id = 3`); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("onlinestore Start_page was updated by a non-member (version %d -> %d)", before, after)
	}
	var n int
	if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM wiki_page_versions WHERE text = 'pwned'`); err != nil || n != 0 {
		t.Fatalf("pwned versions = %d, %v", n, err)
	}
	// プレビューも転送先のページ（添付ファイル・本文）を使わない
	if _, err := d.Exec(ctx, `INSERT INTO attachments (container_kind, container_id, filename, disk_filename, filesize, content_type, downloads, author_id, description, created_at) VALUES ('wiki_page', 3, 'secret-attachment.txt', 'x_secret.txt', 1, 'text/plain', 0, 1, '', '2026-01-01T00:00:00.000000Z')`); err != nil {
		t.Fatal(err)
	}
	_, body := post(t, c, ts.URL+"/projects/ecookbook/wiki/Moved_page/preview", wikiForm(t, c, ts,
		"content[text]", "{{thumbnail(secret-attachment.txt)}} attachment:secret-attachment.txt"))
	if strings.Contains(body, "/attachments/") {
		t.Errorf("preview resolved an attachment of the redirect target: %s", body)
	}
	// 転送先のプロジェクトで権限があれば従来どおり（Redmine と同じ）転送先を更新する
	admin := login(t, ts, "admin", "admin")
	res, _ = post(t, admin, ts.URL+"/projects/ecookbook/wiki/Moved_page", wikiForm(t, admin, ts,
		"_method", "put", "content[text]", "by admin", "content[version]", strconv.Itoa(before)))
	if res.StatusCode != 302 {
		t.Fatalf("admin update: status %d", res.StatusCode)
	}
	if err := d.Get(ctx, &after, `SELECT current_version FROM wiki_pages WHERE id = 3`); err != nil || after != before+1 {
		t.Errorf("admin update: version %d (before %d), %v", after, before, err)
	}
}

// TestWikiMoveToOtherWikiStartPageNeedsTargetManageWiki はページを他プロジェクトの Wiki へ移動するとき、
// 移動先で manage_wiki が無ければ移動先 Wiki のメインページを変えられないことを確認する。
func TestWikiMoveToOtherWikiStartPageNeedsTargetManageWiki(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	// jsmith は ecookbook で Manager、onlinestore で Developer（rename_wiki_pages のみ追加、manage_wiki なし）
	if _, err := d.Exec(ctx, `INSERT INTO role_permissions (role_id, permission, position) VALUES (2, 'rename_wiki_pages', 100)`); err != nil {
		t.Fatal(err)
	}
	c := login(t, ts, "jsmith", "jsmith")
	for _, kv := range [][]string{
		{"wiki_page[title]", "Another_page", "wiki_page[wiki_id]", "2", "wiki_page[is_start_page]", "1"},
		// ecookbook のメインページをそのまま移動（is_start_page の既定値）
		{"wiki_page[title]", "CookBook_documentation", "wiki_page[wiki_id]", "2"},
	} {
		title := kv[1]
		res, _ := post(t, c, ts.URL+"/projects/ecookbook/wiki/"+title+"/rename", wikiForm(t, c, ts, kv...))
		if res.StatusCode != 302 {
			t.Fatalf("move %s: status %d", title, res.StatusCode)
		}
		var wikiID int64
		if err := d.Get(ctx, &wikiID, `SELECT wiki_id FROM wiki_pages WHERE title = ?`, title); err != nil || wikiID != 2 {
			t.Fatalf("move %s: wiki_id = %d, %v", title, wikiID, err)
		}
		var start string
		if err := d.Get(ctx, &start, `SELECT start_page FROM wikis WHERE id = 2`); err != nil {
			t.Fatal(err)
		}
		if start != "Start page" {
			t.Fatalf("move %s: onlinestore start page changed to %q without manage_wiki", title, start)
		}
	}
}
