// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package importer

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/redmineimport/verify"
)

// TestImportScale は大量データでの所要時間を確認する(BUROPHER_TEST_SCALE=1 のときのみ)。
func TestImportScale(t *testing.T) {
	if os.Getenv("BUROPHER_TEST_SCALE") == "" {
		t.Skip("BUROPHER_TEST_SCALE not set")
	}
	ref, srcDB, files := fixturePaths(t)
	dir := workDir(t, ref)
	cp := filepath.Join(dir, "scale.sqlite3")
	copyTo(t, srcDB, cp)
	sdb, err := sql.Open("sqlite", cp)
	if err != nil {
		t.Fatal(err)
	}
	const n = 50000
	for _, q := range []string{
		// 50000 件のチケット(10 件ごとに 1 つ前のチケットを親にした連鎖を作る)
		`WITH RECURSIVE s(i) AS (SELECT 1000 UNION ALL SELECT i + 1 FROM s WHERE i < 1000 + 50000 - 1)
		 INSERT INTO issues (id, tracker_id, project_id, subject, description, status_id, priority_id, author_id, lock_version, done_ratio,
		   is_private, parent_id, root_id, lft, rgt, created_on, updated_on)
		 SELECT i, 1, 1 + (i % 6), 'Issue ' || i, 'desc ' || i, 1 + (i % 6), 4 + (i % 5), 2, 0, 0, 0,
		   CASE WHEN i % 10 = 0 THEN NULL ELSE i - 1 END, i - (i % 10), 0, 0, '2024-03-10 02:30:00', '2024-11-03 01:30:00' FROM s`,
		`WITH RECURSIVE s(i) AS (SELECT 1000 UNION ALL SELECT i + 1 FROM s WHERE i < 1000 + 100000 - 1)
		 INSERT INTO journals (id, journalized_id, journalized_type, user_id, notes, created_on, private_notes)
		 SELECT i, 1000 + (i % 50000), 'Issue', 1 + (i % 4), 'note ' || i, '2025-01-01 00:00:00', 0 FROM s`,
		`WITH RECURSIVE s(i) AS (SELECT 1000 UNION ALL SELECT i + 1 FROM s WHERE i < 1000 + 100000 - 1)
		 INSERT INTO journal_details (id, journal_id, property, prop_key, old_value, value)
		 SELECT i, i, 'attr', 'status_id', '1', '2' FROM s`,
		`WITH RECURSIVE s(i) AS (SELECT 1000 UNION ALL SELECT i + 1 FROM s WHERE i < 1000 + 50000 - 1)
		 INSERT INTO custom_values (id, customized_type, customized_id, custom_field_id, value)
		 SELECT i, 'Issue', i, 2, 'v' || i FROM s`,
		`WITH RECURSIVE s(i) AS (SELECT 1000 UNION ALL SELECT i + 1 FROM s WHERE i < 1000 + 20000 - 1)
		 INSERT INTO time_entries (id, project_id, user_id, issue_id, hours, activity_id, spent_on, tyear, tmonth, tweek, created_on, updated_on, author_id)
		 SELECT i, 1, 2, i, 1.5, 9, '2025-01-01', 2025, 1, 1, '2025-01-01 00:00:00', '2025-01-01 00:00:00', 2 FROM s`,
	} {
		if _, err := sdb.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	sdb.Close()
	start := time.Now()
	archivePath := exportFixture(t, dir, cp, files, "America/New_York", true)
	t.Logf("export: %s", time.Since(start))
	targets(t, dir, func(t *testing.T, d *db.DB, filesDir string) {
		ctx := context.Background()
		start := time.Now()
		rep, err := Run(ctx, d, archivePath, Options{FilesDir: filesDir, NewCipherKey: "k", TempDir: dir})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("import: %s, issues %d, journals %d", time.Since(start), rep.Lookup("issues").Imported["issues"], rep.Lookup("journals").Imported["issue_journals"])
		if got := rep.Lookup("issues").Imported["issues"]; got != 14+n {
			t.Errorf("issues = %d", got)
		}
		start = time.Now()
		vr, err := verify.Verify(ctx, d, archivePath, verify.Options{FilesDir: filesDir})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("verify: %s ok=%v", time.Since(start), vr.OK())
		if !vr.OK() {
			t.Error("verify failed")
		}
	})
}
