// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/handler"
)

// TestAttachmentDiffShowReadsOnlyDisplayedLines は差分の添付の表示が diff_max_lines_displayed 行より先を
// 読まずに切り詰めの表示になり、上限以内なら全体を表示すること（巨大なパッチを丸ごと読み込まない）。
func TestAttachmentDiffShowReadsOnlyDisplayedLines(t *testing.T) {
	var app *handler.App
	ts, d := newFixtureServer(t, func(a *handler.App, _ chi.Router) { app = a })
	root := app.AttachmentStore.Root
	ctx := context.Background()
	var dir, name string
	if err := d.Get(ctx, &dir, `SELECT COALESCE(disk_directory, '') FROM attachments WHERE id = 5`); err != nil {
		t.Fatal(err)
	}
	if err := d.Get(ctx, &name, `SELECT disk_filename FROM attachments WHERE id = 5`); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("--- a/first.txt\n+++ b/first.txt\n@@ -1,3 +1,3 @@\n line1\n-line2\n+line2x\n line3\n")
	b.WriteString("--- a/second.txt\n+++ b/second.txt\n@@ -1,1 +1,1 @@\n-old\n+new\n")
	p := filepath.Join(root, dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	admin := login(t, ts, "admin", "admin")

	_, body := get(t, admin, ts.URL+"/attachments/5")
	if !strings.Contains(body, "second.txt") || strings.Contains(body, "This diff was truncated") {
		t.Errorf("whole diff should be shown\n%s", contentMain(body))
	}

	if err := app.Settings.Set(ctx, "diff_max_lines_displayed", "5"); err != nil {
		t.Fatal(err)
	}
	_, body = get(t, admin, ts.URL+"/attachments/5")
	if !strings.Contains(body, "This diff was truncated") || strings.Contains(body, "second.txt") {
		t.Errorf("diff should be truncated at 5 lines\n%s", contentMain(body))
	}
}
