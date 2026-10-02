package importer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/redmineimport/verify"
)

// messySQL はフィクスチャ DB(のコピー)へ壊れたデータ・境界値を追加する。
var messySQL = []string{
	// チケット: 親子の循環、親の付け替え、存在しないプロジェクト
	`UPDATE issues SET parent_id = 10 WHERE id = 9`,
	`UPDATE issues SET parent_id = 9 WHERE id = 10`,
	`UPDATE issues SET parent_id = 13 WHERE id = 5`,
	`UPDATE issues SET parent_id = 5 WHERE id = 14`,
	`INSERT INTO issues (id, tracker_id, project_id, subject, status_id, priority_id, author_id, lock_version, done_ratio, is_private, root_id, lft, rgt)
	   VALUES (100, 1, 999, 'orphan', 1, 4, 2, 0, 0, 0, 100, 1, 2)`,
	`INSERT INTO issues (id, tracker_id, project_id, subject, status_id, priority_id, author_id, lock_version, done_ratio, is_private, root_id, lft, rgt, created_on, updated_on)
	   VALUES (101, 99, 1, 'bad refs', 99, 99, 999, 0, 150, 0, 101, 1, 2, '2025-11-02 01:30:00', '2025-03-09 02:30:00')`,
	// ユーザー: ログイン/メールの大文字小文字違いの重複
	`UPDATE users SET login = 'JSmith' WHERE id = 7`,
	`UPDATE email_addresses SET address = 'JSMITH@somenet.foo' WHERE id = 7`,
	// ジャーナル: 孤児・他種別
	`INSERT INTO journals (id, journalized_id, journalized_type, user_id, notes, created_on, private_notes) VALUES (100, 999, 'Issue', 2, 'x', '2026-01-01 00:00:00', 0)`,
	`INSERT INTO journals (id, journalized_id, journalized_type, user_id, notes, created_on, private_notes) VALUES (101, 1, 'Plugin', 2, 'x', '2026-01-01 00:00:00', 0)`,
	`INSERT INTO journals (id, journalized_id, journalized_type, user_id, notes, created_on, private_notes) VALUES (102, 1, 'Issue', 999, '', '2026-01-01 00:00:00', 0)`,
	// 工数: 記録者の欠落・孤児
	`UPDATE time_entries SET author_id = NULL WHERE id = 1`,
	`UPDATE time_entries SET author_id = 999 WHERE id = 2`,
	`UPDATE time_entries SET activity_id = 999 WHERE id = 3`,
	// 文書カテゴリ 0
	`UPDATE documents SET category_id = 0 WHERE id = 2`,
	// ウォッチャ: 未知種別・user NULL・EnabledModule
	`INSERT INTO watchers (id, watchable_type, watchable_id, user_id) VALUES (1, 'Plugin', 1, 1), (2, 'Issue', 1, NULL), (3, 'EnabledModule', 3, 2), (4, 'Issue', 999, 2)`,
	// モジュール: 未知名・プロジェクト NULL
	`INSERT INTO enabled_modules (id, project_id, name) VALUES (100, 1, 'agile'), (101, NULL, 'wiki')`,
	// プロジェクト: identifier NULL、存在しない親
	`UPDATE projects SET identifier = NULL WHERE id = 6`,
	`UPDATE projects SET parent_id = 999 WHERE id = 4`,
	// 名前の重複
	`UPDATE issue_statuses SET name = 'New' WHERE id = 4`,
	// 関係: 逆向き型
	`INSERT INTO issue_relations (id, issue_from_id, issue_to_id, relation_type, delay) VALUES (10, 7, 8, 'follows', 2), (11, 3, 2, 'relates', NULL), (12, 1, 1, 'relates', NULL)`,
	// カスタム値: 孤児・種別不一致
	`INSERT INTO custom_values (id, customized_type, customized_id, custom_field_id, value) VALUES (100, 'Issue', 999, 2, 'x'), (101, 'Project', 1, 2, 'x'), (102, 'Issue', 1, 999, 'x')`,
	// ワークフロー
	`INSERT INTO workflows (id, tracker_id, old_status_id, new_status_id, role_id, assignee, author, type, field_name, rule) VALUES
	   (1000, 1, 99, 1, 1, 0, 0, 'WorkflowTransition', NULL, NULL),
	   (1001, 1, 1, 0, 1, 0, 0, 'WorkflowPermission', '2', 'required'),
	   (1002, 1, 1, 0, 1, 0, 0, 'WorkflowPermission', 'due_date', 'readonly'),
	   (1003, 1, 1, 0, 1, 0, 0, 'WorkflowPermission', 'agile_points', 'readonly')`,
	// 添付: コンテナ不在・未紐付け
	`INSERT INTO attachments (id, container_id, container_type, filename, disk_filename, filesize, digest, downloads, author_id, created_on, disk_directory) VALUES
	   (100, 999, 'Issue', 'a.txt', 'x_a.txt', 1, '', 0, 2, '2026-01-01 00:00:00', '2026/01'),
	   (101, NULL, NULL, 'b.txt', 'x_b.txt', 1, '0123', 0, 999, NULL, '')`,
	// メッセージ: 返信への返信
	`INSERT INTO messages (id, board_id, parent_id, subject, content, author_id, replies_count, created_on, updated_on, locked, sticky) VALUES
	   (100, 1, 2, 'RE: RE', 'nested', 999, 0, '2026-01-01 00:00:00', '2026-01-01 00:00:00', NULL, 1)`,
	// Wiki: 大文字小文字違いの重複タイトル
	`INSERT INTO wiki_pages (id, wiki_id, title, created_on, protected, parent_id) VALUES (100, 1, 'another_page', '2026-01-01 00:00:00', 0, NULL)`,
	// member_roles: 継承元の欠落
	`INSERT INTO member_roles (id, member_id, role_id, inherited_from) VALUES (100, 1, 2, 999)`,
	// トークン
	`INSERT INTO tokens (id, user_id, action, value, created_on, updated_on) VALUES
	   (100, 1, 'session', 'sess', '2026-01-15 11:00:00', NULL),
	   (101, 2, 'api', 'apikey-jsmith', '2020-01-01 00:00:00', NULL),
	   (102, 1, 'twofa_backup_code', '123456789abc', '2026-01-15 11:00:00', NULL),
	   (103, 2, 'recovery', 'fresh-recovery', '2026-01-15 11:00:00', NULL)`,
	// クエリ: 存在しないユーザー
	`UPDATE queries SET user_id = 999 WHERE id = 2`,
	// ロールのトラッカー限定
	`UPDATE roles SET settings = '---
permissions_all_trackers:
  view_issues: ''0''
  add_issues: ''1''
permissions_tracker_ids:
  view_issues:
  - ''1''
  - ''99''
  - ''''
' WHERE id = 2`,
	`UPDATE user_preferences SET others = others || ':default_issue_query: ''5''
:recently_used_project_ids: ''2,999,2''
:gantt_months: 6
' WHERE user_id = 2`,
	`UPDATE trackers SET fields_bits = 5 WHERE id = 2`,
	`INSERT INTO settings (id, name, value, updated_on) VALUES (100, 'plugin_redmine_agile', '--- {}', NULL), (101, 'text_formatting', 'textile', NULL)`,
}

func TestImportMessy(t *testing.T) {
	ref, srcDB, files := fixturePaths(t)
	dir := workDir(t, ref)
	cp := filepath.Join(dir, "messy.sqlite3")
	copyTo(t, srcDB, cp)
	sdb, err := sql.Open("sqlite", cp)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range messySQL {
		if _, err := sdb.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	sdb.Close()
	archivePath := exportFixture(t, dir, cp, files, "America/New_York", false)

	targets(t, dir, func(t *testing.T, d *db.DB, filesDir string) {
		ctx := context.Background()
		rep, err := Run(ctx, d, archivePath, Options{NewCipherKey: "k", FilesDir: filesDir, SourceFilesDir: files, Now: fixturesNow, TempDir: dir})
		var sb strings.Builder
		rep.WriteText(&sb)
		t.Log(sb.String())
		if err != nil {
			t.Fatalf("import: %v", err)
		}
		find := func(table, reason string) {
			t.Helper()
			if tr := rep.Lookup(table); tr == nil || tr.Find(reason) == nil {
				t.Errorf("%s: %q not reported", table, reason)
			}
		}
		// チケット
		find("issues", "cyclic parent_id")
		find("issues", "project_id refers to a missing project")
		find("issues", "tracker_id refers to a missing tracker")
		find("issues", "author_id refers to missing user")
		type iss struct {
			Root   int64  `db:"root_id"`
			Path   string `db:"hier_path"`
			Status int64  `db:"status_id"`
			Prio   int64  `db:"priority_id"`
			Author int64  `db:"author_id"`
			Ratio  int    `db:"done_ratio"`
		}
		if i := q1[iss](t, d, `SELECT root_id, hier_path, status_id, priority_id, author_id, done_ratio FROM issues WHERE id = 14`); i.Root != 13 || i.Path != "0000000013/0000000005/0000000014/" {
			t.Errorf("issue 14 = %+v", i)
		}
		if i := q1[iss](t, d, `SELECT root_id, hier_path, status_id, priority_id, author_id, done_ratio FROM issues WHERE id = 101`); i.Status != 1 || i.Prio != 5 || i.Author != 6 || i.Ratio != 100 {
			t.Errorf("issue 101 = %+v", i)
		}
		// DST: 2025-11-02 01:30 (America/New_York) は曖昧 → 早い方(EDT)
		if s := tsStr(t, d, `SELECT created_at FROM issues WHERE id = 101`); s != "2025-11-02T05:30:00.000000Z" {
			t.Errorf("ambiguous time = %s", s)
		}
		if s := tsStr(t, d, `SELECT updated_at FROM issues WHERE id = 101`); s != "2025-03-09T07:30:00.000000Z" {
			t.Errorf("non-existent time = %s", s)
		}
		if !strings.Contains(strings.Join(rep.Warnings, "\n"), "ambiguous local times") {
			t.Errorf("no DST warning: %v", rep.Warnings)
		}
		// ユーザー
		if l := q1[string](t, d, `SELECT login FROM user_accounts WHERE principal_id = 7`); l != "JSmith-7" {
			t.Errorf("duplicate login = %q", l)
		}
		find("email_addresses", "duplicate address")
		find("users", "duplicate login")
		// ジャーナル
		find("journals", "missing issue")
		find("journals", "is not Issue")
		if u := q1[int64](t, d, `SELECT user_id FROM issue_journals WHERE id = 102`); u != 6 {
			t.Errorf("journal 102 user = %d", u)
		}
		if n := q1[*string](t, d, `SELECT notes FROM issue_journals WHERE id = 102`); n != nil {
			t.Error("empty notes should be NULL")
		}
		// 工数
		type te struct {
			User   int64 `db:"user_id"`
			Author int64 `db:"author_id"`
			Act    int64 `db:"activity_id"`
		}
		if x := q1[te](t, d, `SELECT user_id, author_id, activity_id FROM time_entries WHERE id = 1`); x.Author != x.User {
			t.Errorf("time entry 1 = %+v", x)
		}
		if x := q1[te](t, d, `SELECT user_id, author_id, activity_id FROM time_entries WHERE id = 2`); x.Author != 1 {
			t.Errorf("time entry 2 = %+v", x)
		}
		if x := q1[te](t, d, `SELECT user_id, author_id, activity_id FROM time_entries WHERE id = 3`); x.Act != 10 {
			t.Errorf("time entry 3 activity = %d (want default 10)", x.Act)
		}
		if c := q1[int64](t, d, `SELECT category_id FROM documents WHERE id = 2`); c != 1 {
			t.Errorf("document category = %d", c)
		}
		// ウォッチャ・モジュール
		if ks := qs[string](t, d, `SELECT watchable_kind FROM watchers WHERE id < 10 ORDER BY id`); !reflect.DeepEqual(ks, []string{"project_module"}) {
			t.Errorf("watchers = %v", ks)
		}
		find("watchers", "unknown watchable_type")
		find("watchers", "user_id NULL")
		find("enabled_modules", "unknown module")
		find("enabled_modules", "project_id NULL")
		// プロジェクト
		if s := q1[string](t, d, `SELECT identifier FROM projects WHERE id = 6`); s != "project-6" {
			t.Errorf("identifier = %q", s)
		}
		if p := q1[*int64](t, d, `SELECT parent_id FROM projects WHERE id = 4`); p != nil {
			t.Error("project 4 parent should be NULL")
		}
		if n := q1[string](t, d, `SELECT name FROM issue_statuses WHERE id = 4`); n != "New (4)" {
			t.Errorf("status 4 name = %q", n)
		}
		// 関係
		type rel struct {
			From  int64  `db:"issue_from_id"`
			To    int64  `db:"issue_to_id"`
			Type  string `db:"relation_type"`
			Delay *int64 `db:"delay"`
		}
		if r := q1[rel](t, d, `SELECT issue_from_id, issue_to_id, relation_type, delay FROM issue_relations WHERE id = 10`); r.From != 8 || r.To != 7 || r.Type != "precedes" || r.Delay == nil || *r.Delay != 2 {
			t.Errorf("relation 10 = %+v", r)
		}
		find("issue_relations", "duplicate relation")
		find("issue_relations", "relation to itself")
		// カスタム値
		find("custom_values", "customized object is missing")
		find("custom_values", "does not match")
		find("custom_values", "missing custom field")
		// ワークフロー
		if n := q1[int](t, d, `SELECT COUNT(*) FROM workflow_field_rules WHERE custom_field_id = 2 AND rule = 'required'`); n != 1 {
			t.Errorf("cf field rule = %d", n)
		}
		if n := q1[int](t, d, `SELECT COUNT(*) FROM workflow_field_rules WHERE core_field = 'due_date'`); n != 1 {
			t.Errorf("core field rule = %d", n)
		}
		find("workflows", "unknown field_name")
		find("workflows", "old_status_id refers to a missing status")
		// 添付
		if k := q1[*string](t, d, `SELECT container_kind FROM attachments WHERE id = 101`); k != nil {
			t.Error("unattached attachment should have NULL container")
		}
		find("attachments", "container (Issue) is missing")
		find("attachments", "digest of unknown length")
		if rep.Files.Source != "dir" || rep.Files.Copied == 0 {
			t.Errorf("files = %+v", rep.Files)
		}
		// メッセージ
		type msg struct {
			Parent *int64 `db:"parent_id"`
			Sticky bool   `db:"sticky"`
			Locked bool   `db:"locked"`
		}
		if m := q1[msg](t, d, `SELECT parent_id, sticky, locked FROM messages WHERE id = 100`); m.Parent == nil || *m.Parent != 1 || !m.Sticky || m.Locked {
			t.Errorf("message 100 = %+v", m)
		}
		if n := q1[int](t, d, `SELECT replies_count FROM messages WHERE id = 1`); n != 2 { // 移行元の値を保持
			t.Errorf("replies_count = %d", n)
		}
		if s := q1[string](t, d, `SELECT title FROM wiki_pages WHERE id = 100`); s != "another_page_100" {
			t.Errorf("wiki title = %q", s)
		}
		find("member_roles", "inherited_from refers to a missing member role")
		// トークン
		if vs := qs[string](t, d, `SELECT action FROM tokens ORDER BY id`); !reflect.DeepEqual(vs, []string{"api", "recovery"}) {
			t.Errorf("tokens = %v", vs)
		}
		sum := sha256.Sum256([]byte("123456789abc"))
		if n := q1[int](t, d, `SELECT COUNT(*) FROM twofa_backup_codes WHERE user_id = 1 AND code_digest = ?`, hex.EncodeToString(sum[:])); n != 1 {
			t.Errorf("backup codes = %d", n)
		}
		if u := q1[int64](t, d, `SELECT user_id FROM queries WHERE id = 2`); u != 6 {
			t.Errorf("query 2 user = %d", u)
		}
		// ロールのトラッカー限定
		if b := q1[bool](t, d, `SELECT all_trackers FROM role_permissions WHERE role_id = 2 AND permission = 'view_issues'`); b {
			t.Error("view_issues should be tracker-restricted")
		}
		if b := q1[bool](t, d, `SELECT all_trackers FROM role_permissions WHERE role_id = 2 AND permission = 'add_issues'`); !b {
			t.Error("add_issues should be all trackers")
		}
		if ids := qs[int64](t, d, `SELECT tracker_id FROM role_permission_trackers WHERE role_id = 2`); !reflect.DeepEqual(ids, []int64{1}) {
			t.Errorf("role trackers = %v", ids)
		}
		// 個人設定
		if q := q1[*int64](t, d, `SELECT default_issue_query_id FROM user_preferences WHERE user_id = 2`); q == nil || *q != 5 {
			t.Errorf("default_issue_query_id = %v", q)
		}
		if ids := qs[int64](t, d, `SELECT project_id FROM user_recent_projects WHERE user_id = 2 ORDER BY position`); !reflect.DeepEqual(ids, []int64{2}) {
			t.Errorf("recent = %v", ids)
		}
		jsonEq(t, "extra", q1[string](t, d, `SELECT extra FROM user_preferences WHERE user_id = 2`), `{"gantt_months":6}`)
		jsonEq(t, "disabled_core_fields", q1[string](t, d, `SELECT disabled_core_fields FROM trackers WHERE id = 2`), `["assigned_to_id","fixed_version_id"]`)
		// 設定
		if n := q1[string](t, d, `SELECT name FROM legacy_settings`); n != "plugin_redmine_agile" {
			t.Errorf("legacy = %q", n)
		}
		jsonEq(t, "text_formatting", q1[string](t, d, `SELECT value FROM settings WHERE name = 'text_formatting'`), `"common_mark"`)
		find("settings", "duplicate setting name")

		if !rep.OK() {
			t.Errorf("checks failed: %+v", rep.Checks)
		}
		vr, err := verify.Verify(ctx, d, archivePath, verify.Options{FilesDir: filesDir})
		if err != nil {
			t.Fatal(err)
		}
		sb.Reset()
		vr.WriteText(&sb)
		if !vr.OK() {
			t.Errorf("verify failed:\n%s", sb.String())
		}
	})
}
