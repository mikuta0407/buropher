// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package importer

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/redmineimport/verify"
)

// Redmine 7.0.1 の公式フィクスチャ DB(_reference/redmine7-fixtures)に 7.0 固有のデータ
// (Webhook、trackers.private_by_default、mail_notification='only_my_watches'、
// auto_watch_on の issue_assigned_to_me)を足して取り込む。
func TestImportFixtures70(t *testing.T) {
	ref := findReference(t)
	srcDB := filepath.Join(ref, "redmine7-fixtures", "db", "redmine.pristine.sqlite3")
	files := filepath.Join(ref, "redmine7-fixtures", "test", "fixtures", "files")
	if _, err := os.Stat(srcDB); err != nil {
		t.Skip("redmine7 fixtures DB not found")
	}
	dir := workDir(t, ref)
	mod := filepath.Join(dir, "mod.sqlite3")
	copyTo(t, srcDB, mod)
	sdb, err := sql.Open("sqlite", mod)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		`INSERT INTO webhooks (id, url, secret, events, user_id, active, created_at, updated_at) VALUES
		 (1, 'https://hooks.example.com/a', 's3cret', '---
- issue.created
- ''''
- wiki_page.updated
', 2, 1, '2026-01-10 10:00:00', '2026-01-11 10:00:00'),
		 (2, 'https://hooks.example.com/b', '', '--- []
', 3, 0, '2026-01-10 10:00:00', '2026-01-10 10:00:00'),
		 (3, 'https://hooks.example.com/c', NULL, '---
- issue.updated
', 999, 1, '2026-01-10 10:00:00', '2026-01-10 10:00:00')`,
		`INSERT INTO projects_webhooks (id, project_id, webhook_id) VALUES (1, 1, 1), (2, 2, 1), (3, 1, 1), (4, 999, 1), (5, 1, 3), (6, 5, 2)`,
		`UPDATE trackers SET private_by_default = 1 WHERE id = 2`,
		`UPDATE users SET mail_notification = 'only_my_watches' WHERE id = 3`,
		`UPDATE user_preferences SET others = '---
:no_self_notified: false
:auto_watch_on:
- issue_created
- issue_contributed_to
- issue_assigned_to_me
' WHERE user_id = 2`,
	} {
		if _, err := sdb.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	sdb.Close()

	archivePath := exportFixture(t, dir, mod, files, "UTC", true)
	targets(t, dir, func(t *testing.T, d *db.DB, filesDir string) {
		ctx := context.Background()
		rep, err := Run(ctx, d, archivePath, Options{FilesDir: filesDir, NewCipherKey: "test-secret", Now: fixturesNow, TempDir: dir})
		var buf bytes.Buffer
		rep.WriteText(&buf)
		t.Log(buf.String())
		if err != nil {
			t.Fatalf("import: %v", err)
		}
		// webhooks: 存在しないユーザーの Webhook 3 は破棄、events は JSON 配列(空要素除去)、secret は平文
		if n := q1[int](t, d, `SELECT COUNT(*) FROM webhooks`); n != 2 {
			t.Errorf("webhooks = %d", n)
		}
		if s := q1[string](t, d, `SELECT secret FROM webhooks WHERE id = 1`); s != "s3cret" {
			t.Errorf("webhook 1 secret = %q", s)
		}
		if s := q1[sql.NullString](t, d, `SELECT secret FROM webhooks WHERE id = 2`); s.Valid {
			t.Errorf("webhook 2 secret = %q, want NULL", s.String)
		}
		jsonEq(t, "webhook 1 events", q1[string](t, d, `SELECT events FROM webhooks WHERE id = 1`), `["issue.created","wiki_page.updated"]`)
		jsonEq(t, "webhook 2 events", q1[string](t, d, `SELECT events FROM webhooks WHERE id = 2`), `[]`)
		if !q1[bool](t, d, `SELECT active FROM webhooks WHERE id = 1`) || q1[bool](t, d, `SELECT active FROM webhooks WHERE id = 2`) {
			t.Error("webhook active flags")
		}
		if s := tsStr(t, d, `SELECT updated_at FROM webhooks WHERE id = 1`); s != "2026-01-11T10:00:00.000000Z" {
			t.Errorf("webhook 1 updated_at = %s", s)
		}
		// webhook_projects: 重複・存在しないプロジェクト・破棄した Webhook を除く
		if got := qs[string](t, d, `SELECT webhook_id || '-' || project_id FROM webhook_projects ORDER BY webhook_id, project_id`); strings.Join(got, ",") != "1-1,1-2,2-5" {
			t.Errorf("webhook_projects = %v", got)
		}
		// trackers.private_by_default
		if got := qs[int64](t, d, `SELECT id FROM trackers WHERE private_by_default = ? ORDER BY id`, true); len(got) != 1 || got[0] != 2 {
			t.Errorf("private_by_default trackers = %v", got)
		}
		// mail_notification / auto_watch_on
		if s := q1[string](t, d, `SELECT mail_notification FROM user_notification_settings WHERE user_id = 3`); s != "only_my_watches" {
			t.Errorf("mail_notification = %q", s)
		}
		jsonEq(t, "auto_watch_on", q1[string](t, d, `SELECT auto_watch_on FROM user_preferences WHERE user_id = 2`),
			`["issue_created","issue_contributed_to","issue_assigned_to_me"]`)
		// 7.0 の DB は設定行を持つ(フィクスチャは 0)ので補完しない
		jsonEq(t, "start date setting", q1[string](t, d, `SELECT value FROM settings WHERE name = 'default_issue_start_date_to_creation_date'`), `"0"`)
		for _, w := range rep.Warnings {
			if strings.Contains(w, "not in the archive") {
				t.Errorf("unexpected warning: %s", w)
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
