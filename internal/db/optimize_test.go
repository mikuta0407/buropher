// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package db_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
)

func TestOptimizeSQLite(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, "sqlite", filepath.Join(t.TempDir(), "opt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, q := range []string{
		`CREATE TABLE a (id INTEGER PRIMARY KEY, x INTEGER)`,
		`CREATE INDEX a_x ON a (x)`,
		`INSERT INTO a (x) VALUES (1), (2), (2), (3)`,
	} {
		if _, err := d.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	statIdx := func() map[string]bool {
		var idx []string
		if err := d.Select(ctx, &idx, `SELECT idx FROM sqlite_stat1 WHERE tbl = 'a'`); err != nil {
			t.Fatal(err)
		}
		m := map[string]bool{}
		for _, i := range idx {
			m[i] = true
		}
		return m
	}
	if err := d.Optimize(ctx, false); err != nil {
		t.Fatal(err)
	}
	if !statIdx()["a_x"] {
		t.Fatalf("a_x not analyzed")
	}
	// 統計のあるテーブルに後からインデックスを追加すると、そのテーブルを解析し直す
	if _, err := d.Exec(ctx, `CREATE INDEX a_x2 ON a (x, id)`); err != nil {
		t.Fatal(err)
	}
	if err := d.Optimize(ctx, false); err != nil {
		t.Fatal(err)
	}
	if !statIdx()["a_x2"] {
		t.Fatalf("a_x2 not analyzed")
	}
	// 統計を更新したらスキーマのバージョンを進め、他の接続にも統計を読み直させる
	var before, after int64
	if err := d.Get(ctx, &before, `PRAGMA schema_version`); err != nil {
		t.Fatal(err)
	}
	if err := d.Optimize(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := d.Get(ctx, &after, `PRAGMA schema_version`); err != nil {
		t.Fatal(err)
	}
	if after <= before {
		t.Errorf("schema_version %d -> %d; other connections would keep stale statistics", before, after)
	}
	var n int
	if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM sqlite_schema WHERE name = 'buropher_reload_stats'`); err != nil || n != 0 {
		t.Errorf("temporary table left: n=%d err=%v", n, err)
	}
}

func TestSQLiteConnectionPragmas(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, "sqlite", filepath.Join(t.TempDir(), "pragma.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for pragma, want := range map[string]int64{
		"mmap_size":  256 << 20,
		"cache_size": -32 * 1024,
		"temp_store": 2, // MEMORY
	} {
		var got int64
		if err := d.Get(ctx, &got, "PRAGMA "+pragma); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("PRAGMA %s = %d, want %d", pragma, got, want)
		}
	}
}

func TestOptimizeMemoryNoop(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, "sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.Optimize(ctx, false); err != nil {
		t.Fatal(err)
	}
}
