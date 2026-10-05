// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

// test/functional/wiki_controller_test.rb の test_export_to_zip* の移植（#43978）。

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"
)

type zipEntry struct {
	content  string
	modified time.Time
}

func wikiZIPEntries(t *testing.T, body string) map[string]zipEntry {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader([]byte(body)), int64(len(body)))
	if err != nil {
		t.Fatalf("zip: %v", err)
	}
	out := map[string]zipEntry{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = zipEntry{content: string(b), modified: f.Modified}
	}
	return out
}

func TestWikiExportZIP(t *testing.T) {
	f := newContentFixture(t)
	ts := f.ts
	f.exec(t, `UPDATE user_preferences SET time_zone = 'Tokyo' WHERE user_id = 2`)
	if f.int(t, `SELECT COUNT(*) FROM user_preferences WHERE user_id = 2 AND time_zone = 'Tokyo'`) != 1 {
		t.Fatal("jsmith has no preference row")
	}
	jsmith := login(t, ts, "jsmith", "jsmith")

	res, body := get(t, jsmith, ts.URL+"/projects/ecookbook/wiki/export.zip")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/zip" {
		t.Errorf("content type %q", ct)
	}
	if cd := res.Header.Get("Content-Disposition"); cd != `attachment; filename="ecookbook-wiki.zip"; filename*=UTF-8''ecookbook-wiki.zip` {
		t.Errorf("content disposition %q", cd)
	}
	entries := wikiZIPEntries(t, body)

	// ページ一覧（タイトル・最新版の本文・更新日時）は API から取る
	api := apiGet(t, ts, "/projects/ecookbook/wiki/index.json").JSON(t)
	var titles []string
	updated := map[string]time.Time{}
	for _, p := range api["wiki_pages"].([]any) {
		m := p.(map[string]any)
		title := m["title"].(string)
		titles = append(titles, title+".txt")
		u, err := time.Parse(time.RFC3339, m["updated_on"].(string))
		if err != nil {
			t.Fatal(err)
		}
		updated[title] = u
	}
	var names []string
	for n := range entries {
		names = append(names, n)
	}
	sort.Strings(titles)
	sort.Strings(names)
	if strings.Join(names, "\n") != strings.Join(titles, "\n") {
		t.Fatalf("entries %q, want %q", names, titles)
	}
	tokyo := time.FixedZone("JST", 9*3600)
	for name, e := range entries {
		title := strings.TrimSuffix(name, ".txt")
		want := f.str(t, `SELECT v.text FROM wiki_pages p JOIN wiki_page_versions v ON v.page_id = p.id AND v.version = p.current_version WHERE p.wiki_id = 1 AND p.title = ?`, title)
		if e.content != want {
			t.Errorf("%s: content %q, want %q", name, e.content, want)
		}
		// UT 拡張フィールドは UTC の絶対時刻、DOS 日時はユーザー（Tokyo）の表示上の時刻
		if !e.modified.Equal(updated[title]) {
			t.Errorf("%s: modified %v, want %v", name, e.modified, updated[title])
		}
		local := updated[title].In(tokyo)
		got := e.modified
		if got.Year() != local.Year() || got.Month() != local.Month() || got.Day() != local.Day() ||
			got.Hour() != local.Hour() || got.Minute() != local.Minute() || got.Second()/2 != local.Second()/2 {
			t.Errorf("%s: DOS time %v, want %v", name, got, local)
		}
	}

	// 一覧・日付順の索引に ZIP のリンクがある
	for _, p := range []string{"index", "date_index"} {
		if _, b := get(t, jsmith, ts.URL+"/projects/ecookbook/wiki/"+p); !strings.Contains(b, `<a class="zip" rel="nofollow" href="/projects/ecookbook/wiki/export.zip">ZIP</a>`) {
			t.Errorf("%s: ZIP link missing", p)
		}
	}

	// export_wiki_pages が無ければ拒否
	f.exec(t, `DELETE FROM role_permissions WHERE permission = 'export_wiki_pages' AND role_id = 1`)
	if res, _ := get(t, jsmith, ts.URL+"/projects/ecookbook/wiki/export.zip"); res.StatusCode != http.StatusForbidden {
		t.Errorf("without permission: %d", res.StatusCode)
	}
}

func TestWikiExportZIPSanitizesEntryNames(t *testing.T) {
	f := newContentFixture(t)
	ts := f.ts
	jsmith := login(t, ts, "jsmith", "jsmith")
	res, _ := post(t, jsmith, ts.URL+"/projects/ecookbook/wiki/Foo*", wikiForm(t, jsmith, ts,
		"_method", "put", "content[text]", "sanitized"))
	if res.StatusCode != http.StatusFound {
		t.Fatalf("create Foo*: %d", res.StatusCode)
	}
	_, body := get(t, jsmith, ts.URL+"/projects/ecookbook/wiki/export.zip")
	entries := wikiZIPEntries(t, body)
	if e, ok := entries["Foo_.txt"]; !ok || e.content != "sanitized" {
		t.Errorf("Foo_.txt = %+v (%v)", e, ok)
	}
	if _, ok := entries["Foo*.txt"]; ok {
		t.Error("unsanitized entry name")
	}
}
