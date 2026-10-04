// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestSecretKeyConcurrentFirstStart は鍵ファイルの無い状態で同時に SecretKey を呼んでも
// （serve と redmine import の同時の初回起動）、全員が同じ鍵を得て、それがファイルの内容と一致することを確認する。
func TestSecretKeyConcurrentFirstStart(t *testing.T) {
	for round := 0; round < 20; round++ {
		dir := t.TempDir()
		c := Default()
		c.Database.Driver = "sqlite"
		c.Database.DSN = filepath.Join(dir, "data", "buropher.db")
		const n = 16
		keys := make([]string, n)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				k, err := SecretKey(c)
				if err != nil {
					t.Error(err)
					return
				}
				keys[i] = string(k)
			}()
		}
		close(start)
		wg.Wait()
		b, err := os.ReadFile(filepath.Join(dir, "data", "secret_key"))
		if err != nil {
			t.Fatal(err)
		}
		want := string(b[:len(b)-1])
		for i, k := range keys {
			if k != want {
				t.Fatalf("round %d: caller %d got a key different from the stored one", round, i)
			}
		}
		fi, err := os.Stat(filepath.Join(dir, "data", "secret_key"))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Fatalf("secret_key mode %o", fi.Mode().Perm())
		}
	}
}

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
