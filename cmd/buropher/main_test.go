// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mikuta0407/buropher/internal/config"
)

// openDB が作る SQLite の DB ファイル・データディレクトリは他のローカルユーザーに読めないこと
// （以前は 0755 のディレクトリに SQLite 既定の 0644 で DB が作られていた）。
func TestOpenDBCreatesPrivateFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	dsn := filepath.Join(dir, "buropher.db")
	cfg := config.Default()
	cfg.Database.DSN = dsn
	d, err := openDB(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.Exec(context.Background(), "CREATE TABLE t (x INTEGER)"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{dir, dsn, dsn + "-wal"} {
		st, err := os.Stat(p)
		if err != nil {
			if p == dsn+"-wal" && os.IsNotExist(err) {
				continue
			}
			t.Fatal(err)
		}
		if perm := st.Mode().Perm(); perm&0o077 != 0 {
			t.Errorf("%s: mode %o is accessible by group/others", p, perm)
		}
	}
}
