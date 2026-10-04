// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// 生成した secret_key とそのデータディレクトリは本人だけが読める権限で作ること。
func TestSecretKeyCreatesPrivateDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	c := Default()
	c.Database.DSN = filepath.Join(dir, "buropher.db")
	if _, err := SecretKey(c); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, "secret_key"): 0o600} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := st.Mode().Perm(); got&0o077 != 0 {
			t.Errorf("%s: mode %o, want %o", p, got, want)
		}
	}
}

func TestPrepareSQLite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	dsn := filepath.Join(dir, "x.db")
	if err := PrepareSQLite("sqlite", dsn); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		t.Errorf("db mode %o", st.Mode().Perm())
	}
	// 既存のファイルは触らない
	if err := os.WriteFile(dsn, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PrepareSQLite("sqlite", dsn); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dsn); string(b) != "x" {
		t.Errorf("existing file was truncated: %q", b)
	}
	// SQLite 以外・インメモリ・file: URI は何もしない
	for _, c := range [][2]string{{"postgres", filepath.Join(dir, "pg")}, {"sqlite", ":memory:"}, {"sqlite", "file:" + filepath.Join(dir, "u.db")}} {
		if err := PrepareSQLite(c[0], c[1]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "pg")); !os.IsNotExist(err) {
		t.Errorf("postgres DSN created a file: %v", err)
	}
}
