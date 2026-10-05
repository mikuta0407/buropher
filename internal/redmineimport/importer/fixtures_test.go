// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package importer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/redmineimport/verify"
)

func fixturePaths(t *testing.T) (ref, srcDB, files string) {
	t.Helper()
	ref = findReference(t)
	srcDB = filepath.Join(ref, "redmine-fixtures", "db", "redmine.pristine.sqlite3")
	files = filepath.Join(ref, "redmine-fixtures", "test", "fixtures", "files")
	if _, err := os.Stat(srcDB); err != nil {
		t.Skip("redmine fixtures DB not found")
	}
	return
}

func TestImportFixtures(t *testing.T) {
	ref, srcDB, files := fixturePaths(t)
	dir := workDir(t, ref)
	archivePath := exportFixture(t, dir, srcDB, files, "UTC", true)
	targets(t, dir, func(t *testing.T, d *db.DB, filesDir string) {
		ctx := context.Background()
		rep, err := Run(ctx, d, archivePath, Options{FilesDir: filesDir, NewCipherKey: "test-secret", Now: fixturesNow, TempDir: dir})
		var buf bytes.Buffer
		rep.WriteText(&buf)
		t.Log(buf.String())
		if err != nil {
			t.Fatalf("import: %v", err)
		}
		checkFixtureFacts(t, d, rep, filesDir)
		// 6.1 の DB に default_issue_start_date_to_creation_date の行がなければ '1' を保存する
		// (Redmine 7.0 のマイグレーション 20260320090000 と同じ)
		jsonEq(t, "start date setting", q1[string](t, d, `SELECT value FROM settings WHERE name = 'default_issue_start_date_to_creation_date'`), `"1"`)
		for _, w := range rep.Warnings {
			if strings.Contains(w, "webhooks") {
				t.Errorf("unexpected warning for a 6.1 archive: %s", w)
			}
		}

		vr, err := verify.Verify(ctx, d, archivePath, verify.Options{
			FilesDir: filesDir, Digests: true, Passwords: map[string]string{"admin": "admin", "jsmith": "jsmith"},
		})
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		buf.Reset()
		vr.WriteText(&buf)
		t.Log(buf.String())
		if !vr.OK() {
			t.Error("verify failed")
		}
	})
}
