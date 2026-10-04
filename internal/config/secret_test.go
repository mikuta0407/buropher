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
