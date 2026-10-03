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
	if err := d.Optimize(ctx, true); err != nil {
		t.Fatal(err)
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
