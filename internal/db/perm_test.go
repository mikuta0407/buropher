// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package db_test

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
)

// TestSQLiteFilesNotWorldReadable は新規に作る SQLite の DB ファイルと -wal / -shm が
// グループ・他者から読めないこと（0600）を確認する（umask 022 でも SQLite の既定 0644 にならない）。
func TestSQLiteFilesNotWorldReadable(t *testing.T) {
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)
	for _, tc := range []struct{ name, dsn func(dir string) string }{
		{name: func(string) string { return "path" }, dsn: func(dir string) string { return filepath.Join(dir, "a.db") }},
		{name: func(string) string { return "file-uri" }, dsn: func(dir string) string { return "file:" + filepath.Join(dir, "a.db") + "?_txlock=immediate" }},
	} {
		dir := t.TempDir()
		ctx := context.Background()
		d, err := db.Open(ctx, "sqlite", tc.dsn(dir))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.Exec(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY)`); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"a.db", "a.db-wal", "a.db-shm"} {
			fi, err := os.Stat(filepath.Join(dir, f))
			if err != nil {
				t.Fatalf("%s: %v", tc.name(dir), err)
			}
			if perm := fi.Mode().Perm(); perm&0o077 != 0 {
				t.Errorf("%s: %s mode %o, want no group/other access", tc.name(dir), f, perm)
			}
		}
		d.Close()
	}
}

// TestSQLiteExistingFileModeKept は既存の DB ファイルのモードを変えないことを確認する。
func TestSQLiteExistingFileModeKept(t *testing.T) {
	p := filepath.Join(t.TempDir(), "b.db")
	if err := os.WriteFile(p, nil, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}
	d, err := db.Open(context.Background(), "sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o640 {
		t.Errorf("mode %o, want 640", perm)
	}
}
