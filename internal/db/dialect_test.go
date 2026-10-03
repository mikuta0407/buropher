// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package db_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
)

func TestDialectHelpers(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		dl := d.Dialect()
		now := db.Now()

		// Upsert
		up := dl.Upsert("settings", []string{"name", "value", "updated_at"}, []string{"name"}, []string{"value", "updated_at"})
		for _, v := range []string{`{"a": "x", "n": 1}`, `{"a": "Ünïcode_100%", "n": 2}`} {
			if _, err := d.Exec(ctx, up, "k", v, now); err != nil {
				t.Fatalf("upsert: %v", err)
			}
		}
		var n int
		if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM settings`); err != nil || n != 1 {
			t.Fatalf("count after upsert = %d, %v", n, err)
		}
		// DO NOTHING
		if _, err := d.Exec(ctx, dl.Upsert("settings", []string{"name", "value", "updated_at"}, []string{"name"}, nil), "k", `1`, now); err != nil {
			t.Fatal(err)
		}

		// JSON 抽出
		var a string
		if err := d.Get(ctx, &a, `SELECT `+dl.JSONExtractText("value", "a")+` FROM settings WHERE name = ?`, "k"); err != nil {
			t.Fatal(err)
		}
		if a != "Ünïcode_100%" {
			t.Errorf("json extract = %q", a)
		}

		// ILIKE + エスケープ
		var cnt int
		if err := d.Get(ctx, &cnt, `SELECT COUNT(*) FROM settings WHERE `+dl.ILike("name"), "%"+db.EscapeLike("K")+"%"); err != nil || cnt != 1 {
			t.Errorf("ilike = %d, %v", cnt, err)
		}
		if err := d.Get(ctx, &cnt, `SELECT COUNT(*) FROM settings WHERE `+dl.ILike(dl.JSONExtractText("value", "a")), "%"+db.EscapeLike("_100%")); err != nil || cnt != 1 {
			t.Errorf("ilike escaped = %d, %v", cnt, err)
		}
		if err := d.Get(ctx, &cnt, `SELECT COUNT(*) FROM settings WHERE `+dl.ILike("name"), db.EscapeLike("_")); err != nil || cnt != 0 {
			t.Errorf("ilike literal underscore = %d, %v", cnt, err)
		}

		// 真偽リテラル
		now2 := db.Now()
		for i, b := range []bool{true, false, true} {
			mustExec(t, d, `INSERT INTO issue_statuses (name, is_closed) VALUES (?, ?)`, fmt.Sprintf("s%d", i), b)
		}
		if err := d.Get(ctx, &cnt, `SELECT COUNT(*) FROM issue_statuses WHERE is_closed = `+dl.BoolLiteral(true)); err != nil || cnt != 2 {
			t.Errorf("bool literal = %d, %v", cnt, err)
		}
		var closed []bool
		if err := d.Select(ctx, &closed, `SELECT is_closed FROM issue_statuses ORDER BY id `+dl.LimitOffset(2, 1)); err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(closed) != "[false true]" {
			t.Errorf("limit/offset bools = %v", closed)
		}
		if err := d.Select(ctx, &closed, `SELECT is_closed FROM issue_statuses ORDER BY id `+dl.LimitOffset(-1, 2)); err != nil || len(closed) != 1 {
			t.Errorf("unlimited offset = %v, %v", closed, err)
		}

		// NowExpr はアプリの Time と同じ形式で読める
		mustExec(t, d, `INSERT INTO settings (name, value, updated_at) VALUES ('now', '1', `+dl.NowExpr()+`)`)
		var ts db.Time
		if err := d.Get(ctx, &ts, `SELECT updated_at FROM settings WHERE name = 'now'`); err != nil {
			t.Fatal(err)
		}
		if diff := ts.Sub(now2.Time); diff < -time.Minute || diff > time.Minute {
			t.Errorf("NowExpr = %v, now = %v", ts, now2)
		}

		// 明示 ID 挿入後のシーケンス更新
		mustExec(t, d, `INSERT INTO issue_statuses (id, name) VALUES (100, 'explicit')`)
		if err := dl.ResetSequence(ctx, d, "issue_statuses"); err != nil {
			t.Fatal(err)
		}
		id, err := d.InsertReturningID(ctx, `INSERT INTO issue_statuses (name) VALUES ('next')`)
		if err != nil {
			t.Fatal(err)
		}
		if id != 101 {
			t.Errorf("id after reset = %d, want 101", id)
		}
		if err := dl.ResetSequence(ctx, d, "trackers"); err != nil {
			t.Errorf("reset empty table: %v", err)
		}

		// squirrel ビルダ + sqlx.In
		var names []string
		sb := db.SQ.Select("name").From("issue_statuses").Where("id IN (?, ?)", 100, 101).OrderBy("id")
		if err := d.SelectSQ(ctx, &names, sb); err != nil || fmt.Sprint(names) != "[explicit next]" {
			t.Errorf("squirrel select = %v, %v", names, err)
		}
		q, args, err := db.In(`SELECT name FROM issue_statuses WHERE id IN (?) ORDER BY id`, []int64{100, 101})
		if err != nil {
			t.Fatal(err)
		}
		names = nil
		if err := d.Select(ctx, &names, q, args...); err != nil || len(names) != 2 {
			t.Errorf("In select = %v, %v", names, err)
		}

		// 遅延制約: 通常の FK も含め一括で遅延できる
		err = d.WithTx(ctx, func(tx *db.Tx) error {
			if err := tx.DeferConstraints(ctx); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO trackers (id, name, default_status_id) VALUES (5, 'Feature', 500)`); err != nil {
				return fmt.Errorf("insert tracker: %w", err)
			}
			_, err := tx.Exec(ctx, `INSERT INTO issue_statuses (id, name) VALUES (500, 'Late')`)
			return err
		})
		// PG では DEFERRABLE でない FK は SET CONSTRAINTS ALL DEFERRED の対象外なので即時エラーになる。
		if d.Dialect().Name() == db.SQLite && err != nil {
			t.Errorf("deferred tx (sqlite): %v", err)
		}
	})
}

func TestTimeOrderingAndRoundTrip(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		loc := time.FixedZone("JST", 9*3600)
		times := []time.Time{
			time.Date(2026, 1, 2, 3, 4, 5, 999_999_000, loc),
			time.Date(2026, 1, 2, 3, 4, 5, 0, loc),
			time.Date(1999, 12, 31, 23, 59, 59, 123_456_789, time.UTC),
			time.Date(2026, 1, 1, 18, 4, 5, 500_000_000, time.UTC),
		}
		for i, tm := range times {
			mustExec(t, d, `INSERT INTO settings (name, value, updated_at) VALUES (?, '0', ?)`, fmt.Sprint(i), db.NewTime(tm))
		}
		var got []db.Time
		if err := d.Select(ctx, &got, `SELECT updated_at FROM settings ORDER BY updated_at`); err != nil {
			t.Fatal(err)
		}
		want := []int{2, 1, 3, 0}
		for i, w := range want {
			exp := times[w].UTC().Truncate(time.Microsecond)
			if !got[i].Equal(exp) {
				t.Errorf("row %d = %v, want %v", i, got[i], exp)
			}
		}
		// 範囲比較 (パラメータも同形式で渡す)
		var n int
		if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM settings WHERE updated_at >= ?`, db.NewTime(time.Date(2026, 1, 1, 18, 4, 5, 500_000_000, time.UTC))); err != nil || n != 2 {
			t.Errorf("range count = %d, %v", n, err)
		}
		var nt db.NullTime
		if err := d.Get(ctx, &nt, `SELECT expires_at FROM sessions WHERE 1 = 0 UNION ALL SELECT NULL`); err != nil {
			t.Fatal(err)
		}
		if nt.Valid {
			t.Error("NULL scanned as valid")
		}
	})
}
