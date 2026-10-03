// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package db_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
)

func hierPath(ids ...int64) string {
	s := ""
	for _, id := range ids {
		s += fmt.Sprintf("%010d/", id)
	}
	return s
}

func mustExec(t *testing.T, q db.Queryer, query string, args ...any) {
	t.Helper()
	if _, err := q.Exec(context.Background(), query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// seedMasters は FK 検証に必要な最小限のマスタ (ユーザ, ステータス, トラッカー, 優先度) を作る。
func seedMasters(t *testing.T, q db.Queryer) {
	t.Helper()
	now := db.Now()
	mustExec(t, q, `INSERT INTO principals (id, kind, status, firstname, lastname, created_at, updated_at)
		VALUES (1, 'user', 1, 'Redmine', 'Admin', ?, ?)`, now, now)
	mustExec(t, q, `INSERT INTO principals (id, kind, status, lastname, created_at, updated_at)
		VALUES (2, 'anonymous_user', 0, 'Anonymous', ?, ?)`, now, now)
	mustExec(t, q, `INSERT INTO user_accounts (principal_id, login, admin, password_changed_at) VALUES (1, 'admin', ?, ?)`,
		true, db.NullTime{})
	mustExec(t, q, `INSERT INTO user_accounts (principal_id) VALUES (2)`)
	mustExec(t, q, `INSERT INTO email_addresses (user_id, address, is_default, created_at, updated_at)
		VALUES (1, 'admin@example.net', ?, ?, ?)`, true, now, now)
	mustExec(t, q, `INSERT INTO issue_statuses (id, name, is_closed, position) VALUES (1, 'New', ?, 1)`, false)
	mustExec(t, q, `INSERT INTO trackers (id, name, position, default_status_id, disabled_core_fields)
		VALUES (1, 'Bug', 1, 1, ?)`, db.NewJSON([]string{"done_ratio"}))
	mustExec(t, q, `INSERT INTO issue_priorities (id, name, position, is_default) VALUES (1, 'Normal', 1, ?)`, true)
}

func insertIssue(t *testing.T, q db.Queryer, id, project int64, parent, root int64, path string) {
	t.Helper()
	now := db.Now()
	var p any
	if parent != 0 {
		p = parent
	}
	mustExec(t, q, `INSERT INTO issues (id, project_id, tracker_id, status_id, priority_id, author_id,
			parent_id, root_id, hier_path, subject, start_date, created_at, updated_at)
		VALUES (?, ?, 1, 1, 1, 1, ?, ?, ?, ?, ?, ?, ?)`,
		id, project, p, root, path, fmt.Sprintf("issue %d", id), db.NewDate(2026, 10, 1), now, now)
}

func TestMinimalGraph(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		now := db.Now()

		// 1 トランザクションで、子を親より先に挿入する (自己参照 FK は DEFERRABLE INITIALLY DEFERRED)。
		err := d.WithTx(ctx, func(tx *db.Tx) error {
			seedMasters(t, tx)
			// 子プロジェクトを親より先に作る
			mustExec(t, tx, `INSERT INTO projects (id, parent_id, name, identifier, created_at, updated_at)
				VALUES (2, 1, 'Child', 'child', ?, ?)`, now, now)
			mustExec(t, tx, `INSERT INTO projects (id, name, identifier, created_at, updated_at)
				VALUES (1, 'Root', 'root', ?, ?)`, now, now)
			for _, c := range [][3]int64{{1, 1, 0}, {2, 2, 0}, {1, 2, 1}} {
				mustExec(t, tx, `INSERT INTO project_closure (ancestor_id, descendant_id, depth) VALUES (?, ?, ?)`, c[0], c[1], c[2])
			}
			mustExec(t, tx, `INSERT INTO project_modules (project_id, name) VALUES (1, 'issue_tracking')`)
			mustExec(t, tx, `INSERT INTO project_trackers (project_id, tracker_id) VALUES (1, 1)`)
			// projects <-> versions の循環参照
			mustExec(t, tx, `INSERT INTO versions (id, project_id, name, effective_date, created_at, updated_at)
				VALUES (1, 1, 'v1.0', ?, ?, ?)`, db.NewNullDate(db.NewDate(2026, 12, 31)), now, now)
			mustExec(t, tx, `UPDATE projects SET default_version_id = 1 WHERE id = 1`)
			// 子チケットを親より先に挿入
			insertIssue(t, tx, 11, 1, 10, 10, hierPath(10, 11))
			insertIssue(t, tx, 10, 1, 0, 10, hierPath(10))
			insertIssue(t, tx, 20, 2, 0, 20, hierPath(20))
			// ジャーナル
			jid, err := tx.InsertReturningID(ctx, `INSERT INTO issue_journals (issue_id, user_id, notes, created_at)
				VALUES (11, 1, 'note', ?)`, now)
			if err != nil {
				return err
			}
			mustExec(t, tx, `INSERT INTO issue_journal_details (journal_id, property, prop_key, old_value, value)
				VALUES (?, 'attr', 'status_id', '1', '2')`, jid)
			mustExec(t, tx, `INSERT INTO issue_journal_details (journal_id, property, prop_key, custom_field_id, value)
				VALUES (?, 'cf', '5', 5, 'x')`, jid)
			// メンバーと通知対象プロジェクト
			mid, err := tx.InsertReturningID(ctx, `INSERT INTO members (project_id, principal_id, created_at) VALUES (1, 1, ?)`, now)
			if err != nil {
				return err
			}
			mustExec(t, tx, `INSERT INTO roles (id, name) VALUES (3, 'Manager')`)
			mrid, err := tx.InsertReturningID(ctx, `INSERT INTO member_roles (member_id, role_id) VALUES (?, 3)`, mid)
			if err != nil {
				return err
			}
			mustExec(t, tx, `INSERT INTO member_roles (member_id, role_id, inherited_from) VALUES (?, 3, ?)`, mid, mrid)
			mustExec(t, tx, `INSERT INTO user_notified_projects (user_id, project_id) VALUES (1, 1)`)
			return nil
		})
		if err != nil {
			t.Fatalf("graph tx: %v", err)
		}

		// 子孫検索 (hier_path 前方一致) と並び順
		var ids []int64
		if err := d.Select(ctx, &ids, `SELECT id FROM issues WHERE root_id = ? ORDER BY root_id, hier_path`, 10); err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(ids) != "[10 11]" {
			t.Fatalf("issue tree order = %v", ids)
		}

		// 時刻・日付の読み戻し
		var row struct {
			CreatedAt db.Time     `db:"created_at"`
			StartDate db.NullDate `db:"start_date"`
			DueDate   db.NullDate `db:"due_date"`
			ClosedAt  db.NullTime `db:"closed_at"`
		}
		if err := d.Get(ctx, &row, `SELECT j.created_at, i.start_date, i.due_date, i.closed_at
			FROM issues i JOIN issue_journals j ON j.issue_id = i.id WHERE i.id = 11`); err != nil {
			t.Fatal(err)
		}
		if !row.CreatedAt.Equal(now.Time) || row.CreatedAt.Location() != time.UTC {
			t.Errorf("created_at = %v, want %v", row.CreatedAt, now)
		}
		if !row.StartDate.Valid || row.StartDate.Date.String() != "2026-10-01" || row.DueDate.Valid || row.ClosedAt.Valid {
			t.Errorf("dates = %+v", row)
		}
		var disabled db.JSON[[]string]
		if err := d.Get(ctx, &disabled, `SELECT disabled_core_fields FROM trackers WHERE id = 1`); err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(disabled.V) != "[done_ratio]" {
			t.Errorf("disabled_core_fields = %v", disabled.V)
		}

		// 遅延 FK 違反はコミット時にエラーになる
		err = d.WithTx(ctx, func(tx *db.Tx) error {
			insertIssue(t, tx, 30, 1, 9999, 30, hierPath(9999, 30))
			return nil
		})
		if err == nil {
			t.Error("dangling parent_id committed without error")
		}

		// 遅延不可の FK は文単位で即時エラー
		err = d.WithTx(ctx, func(tx *db.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO issues (project_id, tracker_id, status_id, priority_id, author_id,
				root_id, hier_path, subject, created_at, updated_at) VALUES (9999, 1, 1, 1, 1, 1, '', 'x', ?, ?)`, now, now)
			return err
		})
		if err == nil {
			t.Error("issue with unknown project inserted")
		}

		// ID 自動採番のルートチケット: 仮の root_id/hier_path で挿入し同一 Tx 内で確定する
		var newID int64
		err = d.WithTx(ctx, func(tx *db.Tx) error {
			if err := tx.Dialect().ResetSequence(ctx, tx, "issues"); err != nil {
				return err
			}
			id, err := tx.InsertReturningID(ctx, `INSERT INTO issues (project_id, tracker_id, status_id, priority_id, author_id,
				root_id, hier_path, subject, created_at, updated_at) VALUES (1, 1, 1, 1, 1, 0, '', 'auto', ?, ?)`, now, now)
			if err != nil {
				return err
			}
			newID = id
			_, err = tx.Exec(ctx, `UPDATE issues SET root_id = id, hier_path = ? WHERE id = ?`, hierPath(id), id)
			return err
		})
		if err != nil {
			t.Fatalf("auto id issue: %v", err)
		}
		if newID <= 20 {
			t.Errorf("auto id = %d, want > 20 (sequence not advanced)", newID)
		}

		// 親チケットだけを削除すると子の parent_id/root_id が宙に浮くので失敗する
		if _, err := d.Exec(ctx, `DELETE FROM issues WHERE id = 10`); err == nil {
			t.Error("deleting parent issue with children succeeded")
		}
		// 作成者として参照されているユーザは削除できない (RESTRICT)
		if _, err := d.Exec(ctx, `DELETE FROM principals WHERE id = 1`); err == nil {
			t.Error("deleting referenced author succeeded")
		}
		// 親プロジェクトは子があると削除できない
		if _, err := d.Exec(ctx, `DELETE FROM projects WHERE id = 1`); err == nil {
			t.Error("deleting parent project with child succeeded")
		}
		// 子プロジェクトの削除はチケット等を CASCADE で消す
		if _, err := d.Exec(ctx, `DELETE FROM projects WHERE id = 2`); err != nil {
			t.Fatalf("delete child project: %v", err)
		}
		var n int
		if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM issues WHERE project_id = 2`); err != nil || n != 0 {
			t.Errorf("issues of deleted project = %d, %v", n, err)
		}
		if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM project_closure WHERE descendant_id = 2`); err != nil || n != 0 {
			t.Errorf("closure rows of deleted project = %d, %v", n, err)
		}
		// メンバー削除で通知対象プロジェクトと member_roles (継承行含む) も消える
		if _, err := d.Exec(ctx, `DELETE FROM members WHERE principal_id = 1 AND project_id = 1`); err != nil {
			t.Fatal(err)
		}
		for _, tbl := range []string{"user_notified_projects", "member_roles"} {
			if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM `+tbl); err != nil || n != 0 {
				t.Errorf("%s after member delete = %d, %v", tbl, n, err)
			}
		}
		// チケット削除でジャーナルと明細が CASCADE 削除される
		if _, err := d.Exec(ctx, `DELETE FROM issues WHERE root_id = 10 OR id = ?`, newID); err != nil {
			t.Fatalf("delete issue tree: %v", err)
		}
		if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM issue_journal_details`); err != nil || n != 0 {
			t.Errorf("journal details after delete = %d, %v", n, err)
		}
	})
}

func TestCheckConstraints(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		now := db.Now()
		bad := []struct {
			name string
			q    string
			args []any
		}{
			{"principal kind", `INSERT INTO principals (kind, created_at, updated_at) VALUES ('robot', ?, ?)`, []any{now, now}},
			{"project status", `INSERT INTO projects (name, identifier, status, created_at, updated_at) VALUES ('p', 'p', 2, ?, ?)`, []any{now, now}},
			{"invalid json", `INSERT INTO settings (name, value, updated_at) VALUES ('x', '{', ?)`, []any{now}},
			{"second anonymous", `INSERT INTO principals (kind, created_at, updated_at) VALUES ('anonymous_user', ?, ?), ('anonymous_user', ?, ?)`, []any{now, now, now, now}},
		}
		if d.Dialect().Name() == db.SQLite {
			bad = append(bad, struct {
				name string
				q    string
				args []any
			}{"timestamp format", `INSERT INTO settings (name, value, updated_at) VALUES ('y', '1', '2026-10-01 12:00:00')`, nil})
		}
		for _, b := range bad {
			if _, err := d.Exec(ctx, b.q, b.args...); err == nil {
				t.Errorf("%s: accepted invalid row", b.name)
			}
		}
		// case-insensitive unique login
		err := d.WithTx(ctx, func(tx *db.Tx) error {
			for i, login := range []string{"Alice", "alice"} {
				id, err := tx.InsertReturningID(ctx, `INSERT INTO principals (kind, created_at, updated_at) VALUES ('user', ?, ?)`, now, now)
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO user_accounts (principal_id, login) VALUES (?, ?)`, id, login); err != nil {
					return fmt.Errorf("login %d: %w", i, err)
				}
			}
			return nil
		})
		if err == nil {
			t.Error("duplicate login (case-insensitive) accepted")
		}
	})
}

func TestWithTxRollback(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		sentinel := errors.New("boom")
		err := d.WithTx(ctx, func(tx *db.Tx) error {
			mustExec(t, tx, `INSERT INTO settings (name, value, updated_at) VALUES ('app_title', ?, ?)`, `"X"`, db.Now())
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("err = %v", err)
		}
		var n int
		if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM settings`); err != nil || n != 0 {
			t.Fatalf("rows after rollback = %d, %v", n, err)
		}
		func() {
			defer func() { _ = recover() }()
			_ = d.WithTx(ctx, func(tx *db.Tx) error {
				mustExec(t, tx, `INSERT INTO settings (name, value, updated_at) VALUES ('app_title', ?, ?)`, `"X"`, db.Now())
				panic("panic in tx")
			})
		}()
		if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM settings`); err != nil || n != 0 {
			t.Fatalf("rows after panic = %d, %v", n, err)
		}
	})
}
