// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/handler"
	"github.com/mikuta0407/buropher/internal/pdf"
	"github.com/mikuta0407/buropher/internal/pdf/pdftest"
)

// getPDF は u を取得し、PDF として解析する（BUROPHER_PDF_DEBUG_DIR があれば書き出す）。
func getPDF(t *testing.T, c *http.Client, u, wantFilename string) *pdftest.Doc {
	t.Helper()
	res, body := get(t, c, u)
	if res.StatusCode != 200 {
		t.Fatalf("%s: status %d", u, res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("%s: content-type %q", u, ct)
	}
	cd := res.Header.Get("Content-Disposition")
	if !strings.HasPrefix(cd, `attachment; filename="`+wantFilename+`"`) {
		t.Errorf("%s: content-disposition %q, want filename %q", u, cd, wantFilename)
	}
	if !strings.HasPrefix(body, "%PDF-1.") || !strings.Contains(body[len(body)-32:], "%%EOF") {
		t.Fatalf("%s: not a PDF", u)
	}
	if dir := os.Getenv("BUROPHER_PDF_DEBUG_DIR"); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, strings.ReplaceAll(t.Name(), "/", "_")+".pdf"), []byte(body), 0o644)
	}
	return pdftest.Parse([]byte(body))
}

func assertContains(t *testing.T, text string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(text, w) {
			t.Errorf("missing %q", w)
		}
	}
}

func TestIssuePDF(t *testing.T) {
	ts, _ := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	p := getPDF(t, admin, ts.URL+"/issues/1.pdf", "ecookbook-1.pdf")
	if p.Pages < 1 {
		t.Fatalf("pages %d", p.Pages)
	}
	text := p.Text()
	assertContains(t, text,
		"eCookbook - Bug #1", "Cannot print recipes", "Status:", "New", "Priority:", "Description",
		"Unable to print recipes", "History", "Journal notes", "Status changed from New to Assigned", "01/15/2026", "1/1")
	// 関連するチケットと添付ファイル
	p3 := getPDF(t, admin, ts.URL+"/issues/3.pdf", "ecookbook-3.pdf")
	assertContains(t, p3.Text(), "Related issues", "Related to Feature request #2", "Files", "error281.txt")
	// 匿名は見えないチケットの PDF を取得できない（ログイン画面へ）
	res, _ := get(t, newClient(t), ts.URL+"/issues/4.pdf")
	if res.StatusCode == 200 {
		t.Errorf("anonymous private issue: %d", res.StatusCode)
	}
}

func TestIssuesListPDF(t *testing.T) {
	ts, _ := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	p := getPDF(t, admin, ts.URL+"/projects/ecookbook/issues.pdf?set_filter=1&f[]=status_id&op[status_id]=*&c[]=tracker&c[]=status&c[]=subject&c[]=estimated_hours&c[]=description&group_by=tracker&t[]=estimated_hours", "issues.pdf")
	text := p.Text()
	assertContains(t, text, "eCookbook - Issues", "Tracker", "Subject", "Cannot print recipes", "Bug (", "Description", "Estimated time: ")

	// 保存済みクエリの名前がファイル名になる
	jsmith := login(t, ts, "jsmith", "jsmith")
	getPDF(t, jsmith, ts.URL+"/projects/ecookbook/issues.pdf?query_id=1", "multiple_custom_fields_query.pdf")

	// 不正なクエリは 422
	res, _ := get(t, admin, ts.URL+"/issues.pdf?set_filter=1&f[]=start_date&op[start_date]=%3D&v[start_date][]=bad")
	if res.StatusCode != 422 {
		t.Errorf("invalid query: %d", res.StatusCode)
	}
}

func TestWikiPDF(t *testing.T) {
	ts, _ := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	p := getPDF(t, admin, ts.URL+"/projects/ecookbook/wiki/CookBook_documentation.pdf", "CookBook_documentation.pdf")
	assertContains(t, p.Text(), "eCookbook - CookBook_documentation - # 3", "CookBook documentation")

	all := getPDF(t, admin, ts.URL+"/projects/ecookbook/wiki/export.pdf", "ecookbook.pdf")
	if all.Pages < 3 {
		t.Errorf("export pages %d", all.Pages)
	}
	assertContains(t, all.Text(), "eCookbook", "CookBook documentation")

	// export_wiki_pages の無いユーザーは PDF を受け付けない
	res, _ := get(t, newClient(t), ts.URL+"/projects/ecookbook/wiki/CookBook_documentation.pdf")
	if res.StatusCode == 200 && res.Header.Get("Content-Type") == "application/pdf" {
		t.Errorf("anonymous got PDF")
	}
}

func TestGanttPDF(t *testing.T) {
	ts, _ := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	p := getPDF(t, admin, ts.URL+"/projects/ecookbook/issues/gantt.pdf?zoom=2&months=6&month=1&year=2026", "ecookbook-gantt.pdf")
	assertContains(t, p.Text(), "eCookbook", "2026-1", "2026-6")
	all := getPDF(t, admin, ts.URL+"/issues/gantt.pdf?months=1&month=1&year=2026", "gantt.pdf")
	assertContains(t, all.Text(), "2026-1", "15")
}

// TestIssuePDFJapanese は日本語のチケットを CJK フォントで描けることを確認する（フォントが無ければ ? に置き換える）。
func TestIssuePDFJapanese(t *testing.T) {
	dir := cjkTestFontDir()
	ts, d := newFixtureServer(t, func(a *handler.App, _ chi.Router) {
		a.PDFFonts = pdf.NewFontSet(pdf.Config{Dir: dir})
	})
	ctx := context.Background()
	if _, err := d.Exec(ctx, `UPDATE issues SET subject = ?, description = ? WHERE id = 1`, "レシピを印刷できない", "印刷すると*エラー*になります。\n\n* 項目1\n* 項目2"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(ctx, `UPDATE user_accounts SET language = 'ja' WHERE principal_id = 1`); err != nil {
		t.Fatal(err)
	}
	admin := login(t, ts, "admin", "admin")
	p := getPDF(t, admin, ts.URL+"/issues/1.pdf", "ecookbook-1.pdf")
	joined := p.Joined()
	if dir == "" {
		if !strings.Contains(joined, "?") {
			t.Errorf("unsupported glyphs should be replaced: %q", joined)
		}
		return
	}
	assertContains(t, joined, "レシピを印刷できない", "印刷すると", "エラー", "項目1", "ステータス")
	lp := getPDF(t, admin, ts.URL+"/projects/ecookbook/issues.pdf", "issues.pdf")
	assertContains(t, lp.Joined(), "レシピを印刷できない")
}

// cjkTestFontDir はテスト用の CJK フォントのディレクトリ（無ければ ""）。
func cjkTestFontDir() string {
	if d := os.Getenv("BUROPHER_PDF_TEST_FONT_DIR"); d != "" {
		return d
	}
	dir, _ := os.Getwd()
	for range 8 {
		p := filepath.Join(dir, "_reference", "fonts")
		if _, err := os.Stat(filepath.Join(p, "DroidSansFallbackFull.ttf")); err == nil {
			return p
		}
		dir = filepath.Dir(dir)
	}
	return ""
}
