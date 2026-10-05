// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/attachments"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/handler"
)

// attachToContainer は添付 id を container（kind, id）に紐付ける。
func attachToContainer(t *testing.T, d *db.DB, id int64, kind string, containerID int64) {
	t.Helper()
	if _, err := d.Exec(context.Background(), `UPDATE attachments SET container_kind = ?, container_id = ? WHERE id = ?`, kind, containerID, id); err != nil {
		t.Fatal(err)
	}
}

// test_show_svg_file_as_image（#44126: SVG は XML のソースではなく画像として表示する）。
// ダウンロードは従来どおり attachment・sandbox の CSP 付きで返し、文書として開かれてもスクリプトは動かない。
func TestAttachmentShowSVGAsImage(t *testing.T) {
	ts, d := newFixtureServer(t)
	jsmith := login(t, ts, "jsmith", "jsmith")
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><script>alert(1)</script><rect width="10" height="10"/></svg>`
	id := uploadAPI(t, ts.URL, "filename=testfile.svg&content_type=image/svg%2Bxml", svg)
	attachToContainer(t, d, id, "issue", 1)

	res, body := get(t, jsmith, fmt.Sprintf("%s/attachments/%d", ts.URL, id))
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("status %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if want := fmt.Sprintf(`<img alt="testfile.svg" class="filecontent image" src="/attachments/download/%d/testfile.svg" />`, id); !strings.Contains(body, want) {
		t.Errorf("svg not shown as image:\n%s", contentMain(body))
	}
	if strings.Contains(body, "alert(1)") {
		t.Error("svg source shown in the page")
	}
	res, _ = get(t, jsmith, fmt.Sprintf("%s/attachments/download/%d/testfile.svg", ts.URL, id))
	if cd := res.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Errorf("svg download disposition %q", cd)
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") || !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("svg download CSP %q", csp)
	}
	if res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff missing")
	}
}

// test_show_pdf（#22483: PDF はダウンロードさせずにページ内に表示する）。
func TestAttachmentShowPDF(t *testing.T) {
	ts, d := newFixtureServer(t)
	jsmith := login(t, ts, "jsmith", "jsmith")
	id := uploadAPI(t, ts.URL, "filename=ecookbook-gantt.pdf&content_type=application/pdf", "%PDF-1.4\n%%EOF\n")
	attachToContainer(t, d, id, "issue", 1)

	_, body := get(t, jsmith, fmt.Sprintf("%s/attachments/%d", ts.URL, id))
	path := fmt.Sprintf("/attachments/download/%d/ecookbook-gantt.pdf", id)
	main := contentMain(body)
	for _, want := range []string{
		`<p class="pdf-full-view-link">`,
		`<a class="icon icon-maximize" href="` + path + `">`,
		`<span class="icon-label">Open in full view</span>`,
		`<div class="filecontent pdf">`,
		`<object data="` + path + `" type="application/pdf">`,
	} {
		if !strings.Contains(main, want) {
			t.Errorf("missing %q in\n%s", want, main)
		}
	}
	if !strings.Contains(squeezeWS(main), `<p class="nodata"> No preview available </p>`) {
		t.Errorf("fallback missing:\n%s", main)
	}
	// .ai は PDF として表示しない（#44335: 自動でダウンロードされてしまう）
	ai := uploadAPI(t, ts.URL, "filename=logo.ai", "%PDF-1.5\n")
	attachToContainer(t, d, ai, "issue", 1)
	_, body = get(t, jsmith, fmt.Sprintf("%s/attachments/%d", ts.URL, ai))
	if strings.Contains(body, `class="filecontent pdf"`) {
		t.Error(".ai shown as pdf")
	}
}

// squeezeWS は連続する空白を 1 つにまとめる。
func squeezeWS(s string) string { return strings.Join(strings.Fields(s), " ") }

// test_show_msword / test_show_libreoffice_writer（#8959: Office 文書を Pandoc で Markdown に変換して表示する）。
// Pandoc の代わりに引数を確かめる偽のコマンドを使う。
func TestAttachmentShowMarkdownized(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	dir := t.TempDir()
	pandoc := filepath.Join(dir, "pandoc")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then echo "pandoc 3.8.3"; exit 0; fi
[ "$1" = "-f" ] && [ "$3" = "-t" ] && [ "$4" = "gfm" ] || exit 3
cat <<'EOF'
# Document

Redmine is a flexible project management web application.

| a | b |
|---|---|
| 1 | 2 |

<script>alert(1)</script>
EOF
`
	if err := os.WriteFile(pandoc, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	var store *attachments.Store
	ts, d := newFixtureServer(t, func(a *handler.App, _ chi.Router) {
		store = a.AttachmentStore
		a.AttachmentStore.Markdownizer = attachments.NewMarkdownizer(pandoc, 0, 0, 0, nil)
	})
	jsmith := login(t, ts, "jsmith", "jsmith")
	for _, fn := range []string{"msword.docx", "libreoffice-writer.odt", "sheet.xlsx", "slides.pptx"} {
		id := uploadAPI(t, ts.URL, "filename="+fn, "PK\x03\x04"+fn)
		attachToContainer(t, d, id, "issue", 1)
		_, body := get(t, jsmith, fmt.Sprintf("%s/attachments/%d", ts.URL, id))
		main := contentMain(body)
		if !strings.Contains(main, `<div class="filecontent wiki">`) ||
			!strings.Contains(main, "<p>Redmine is a flexible project management web application.</p>") ||
			!strings.Contains(main, "<table>") {
			t.Errorf("%s: markdownized preview missing:\n%s", fn, main)
		}
		if strings.Contains(main, `class="nodata"`) || strings.Contains(main, "<script>alert(1)</script>") {
			t.Errorf("%s: nodata or unsanitized html:\n%s", fn, main)
		}
	}
	// 変換結果は添付の保存先の derived_cache に置かれる
	if m, _ := filepath.Glob(filepath.Join(store.Root, "derived_cache", "markdownized_previews", "*.md")); len(m) != 4 {
		t.Errorf("cached previews: %v", m)
	}
	// 対応していない形式はこれまでどおり
	id := uploadAPI(t, ts.URL, "filename=old.doc", "\xd0\xcf\x11\xe0")
	attachToContainer(t, d, id, "issue", 1)
	_, body := get(t, jsmith, fmt.Sprintf("%s/attachments/%d", ts.URL, id))
	if !strings.Contains(body, `class="nodata"`) {
		t.Errorf(".doc preview:\n%s", contentMain(body))
	}
}

// test_download_all_should_require_attachment_visibility（#43951, Redmine 6.1.3）:
// プロジェクトのファイルは view_files が無ければ個別にも一括でもダウンロードできない。
func TestAttachmentsDownloadAllRequiresAttachmentVisibility(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	if _, err := d.Exec(ctx, `DELETE FROM role_permissions WHERE permission = 'view_files' AND role_id IN (SELECT id FROM roles WHERE builtin = 1)`); err != nil {
		t.Fatal(err)
	}
	id := uploadAPI(t, ts.URL, "filename=testfile.txt&content_type=text/plain", "this is a text file for upload tests\r\nwith multiple lines\r\n")
	attachToContainer(t, d, id, "project", 1)

	someone := login(t, ts, "someone", "foo")
	if res, _ := get(t, someone, fmt.Sprintf("%s/attachments/download/%d", ts.URL, id)); res.StatusCode != 403 {
		t.Errorf("download: %d", res.StatusCode)
	}
	if res, _ := get(t, someone, ts.URL+"/attachments/projects/1/download"); res.StatusCode != 403 {
		t.Errorf("download_all: %d", res.StatusCode)
	}
	// view_files があれば一括ダウンロードできる
	if res, _ := get(t, login(t, ts, "jsmith", "jsmith"), ts.URL+"/attachments/projects/1/download"); res.StatusCode != 200 {
		t.Errorf("download_all by jsmith: %d", res.StatusCode)
	}
}
