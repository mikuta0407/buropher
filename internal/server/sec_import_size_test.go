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
)

// TestImportFileSizeLimit は、attachment_max_size を超える CSV のインポートを受け付けないことを確かめる。
// インポートのファイルは settings・mapping・run の各リクエストで丸ごとメモリに読むため、
// max_request_body_mb の既定（無制限）では巨大なファイルで何度でもメモリを使わせられた。
func TestImportFileSizeLimit(t *testing.T) {
	srv, ts, d := newFixtureServerFull(t)
	if err := srv.App().Settings.Set(context.Background(), "attachment_max_size", "1"); err != nil {
		t.Fatal(err)
	}
	jsmith := login(t, ts, "jsmith", "jsmith")
	before := queryInt(t, d, `SELECT COUNT(*) FROM imports`)

	big := filepath.Join(t.TempDir(), "big.csv")
	if err := os.WriteFile(big, []byte("subject;tracker\n"+strings.Repeat("a;Bug\n", 400)), 0o600); err != nil {
		t.Fatal(err)
	}
	res, body := importUploadRaw(t, jsmith, ts, "IssueImport", big, "")
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "exceeds the maximum allowed file size") {
		t.Errorf("create with a too big file: status %d location %q", res.StatusCode, res.Header.Get("Location"))
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM imports`); n != before {
		t.Errorf("import created for a too big file")
	}

	// 上限以下のファイルは従来どおり
	importUpload(t, jsmith, ts, "IssueImport", "import_issues.csv", "")
}
