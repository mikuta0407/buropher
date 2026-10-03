// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package export

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCoreDefinitions(t *testing.T) {
	if len(coreTables) != 56 {
		t.Errorf("core tables = %d, want 56", len(coreTables))
	}
	if len(coreMigrations) != 322 {
		t.Errorf("core migrations = %d, want 322", len(coreMigrations))
	}
	if coreMigrations[len(coreMigrations)-1] != "20250611092227" {
		t.Errorf("max migration = %s", coreMigrations[len(coreMigrations)-1])
	}
	seen := map[string]bool{}
	for _, v := range coreMigrations {
		if seen[v] {
			t.Errorf("duplicate %s", v)
		}
		seen[v] = true
	}
	for i := 1; i <= 108; i++ {
		if !seen[strconv.Itoa(i)] {
			t.Errorf("missing sequential migration %d", i)
		}
	}
}

func TestResolveConn(t *testing.T) {
	cases := []struct {
		dsn, driver, kind, driverName, contains string
	}{
		{"sqlite:///var/lib/redmine/db.sqlite3", "", KindSQLite, "sqlite", "file:/var/lib/redmine/db.sqlite3?mode=ro"},
		{"sqlite3:///a b/x.db", "", KindSQLite, "sqlite", "file:/a%20b/x.db?mode=ro"},
		{"/abs/x.db", "sqlite", KindSQLite, "sqlite", "file:/abs/x.db?mode=ro"},
		{"mysql://u:p%40ss@db.example:3307/redmine?tls=skip-verify", "", KindMySQL, "mysql", "u:p@ss@tcp(db.example:3307)/redmine?"},
		{"mysql2://u@localhost/redmine", "", KindMySQL, "mysql", "u@tcp(localhost:3306)/redmine"},
		{"mysql://u:p@x/redmine?socket=/run/mysqld.sock", "", KindMySQL, "mysql", "unix(/run/mysqld.sock)/redmine"},
		{"u:p@tcp(h:3306)/db", "mysql", KindMySQL, "mysql", "u:p@tcp(h:3306)/db"},
		{"postgres://u:p@h/db?sslmode=disable", "", KindPostgres, "pgx", "postgres://u:p@h/db?sslmode=disable"},
		{"postgresql://h/db", "", KindPostgres, "pgx", "postgresql://h/db"},
		{"sqlserver://sa:x@h:1433?database=redmine", "", KindSQLServer, "sqlserver", "sqlserver://sa:x@h:1433?database=redmine"},
	}
	for _, c := range cases {
		ci, err := resolveConn(c.dsn, c.driver)
		if err != nil {
			t.Errorf("%s: %v", c.dsn, err)
			continue
		}
		if ci.Kind != c.kind || ci.DriverName != c.driverName || !strings.Contains(ci.DSN, c.contains) {
			t.Errorf("%s: got %+v, want kind %s contains %q", c.dsn, ci, c.kind, c.contains)
		}
	}
	if ci, _ := resolveConn("mysql://u@h/db?parseTime=true&loc=Local", ""); strings.Contains(ci.DSN, "parseTime=true") {
		t.Errorf("parseTime must be disabled: %s", ci.DSN)
	}
	for _, bad := range [][2]string{{"redmine.db", ""}, {"mysql://x/y", "postgres"}, {"x", "oracle"}} {
		if _, err := resolveConn(bad[0], bad[1]); err == nil {
			t.Errorf("%v: expected error", bad)
		}
	}
}

func TestNormalizeValue(t *testing.T) {
	ts := time.Date(2026, 10, 3, 1, 2, 3, 456789000, time.UTC)
	cases := []struct {
		typ  string
		in   any
		want any
		warn bool
	}{
		{"integer", []byte("42"), int64(42), false},
		{"integer", int32(7), int64(7), false},
		{"integer", "x", "x", true},
		{"float", []byte("1.25"), 1.25, false},
		{"float", int64(2), 2.0, false},
		{"float", float32(0.3), 0.3, false},
		{"boolean", int64(1), true, false},
		{"boolean", []byte("0"), false, false},
		{"boolean", "t", true, false},
		{"boolean", "f", false, false},
		{"boolean", "TRUE", true, false},
		{"boolean", "maybe", "maybe", true},
		{"datetime", ts, "2026-10-03 01:02:03.456789", false},
		{"datetime", time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("", 9*3600)), "2026-01-01 00:00:00", false},
		{"datetime", []byte("2026-10-02 23:28:40"), "2026-10-02 23:28:40", false},
		{"datetime", "2026-10-02T23:28:40.5", "2026-10-02 23:28:40.500000", false},
		{"datetime", "2026-10-02 23:28:40.1234567", "2026-10-02 23:28:40.123456", false},
		{"datetime", "2026-10-02 23:28:40.000000", "2026-10-02 23:28:40", false},
		{"datetime", "2026-10-02 23:28:40+09:00", "2026-10-02 23:28:40", true},
		{"datetime", "0000-00-00 00:00:00", "0000-00-00 00:00:00", true},
		{"datetime", "garbage", "garbage", true},
		{"date", ts, "2026-10-03", false},
		{"date", []byte("2026-10-03"), "2026-10-03", false},
		{"date", "2026-10-03 00:00:00", "2026-10-03", false},
		{"binary", "abc", []byte("abc"), false},
		{"text", []byte("日本語"), "日本語", false},
		{"text", []byte{0xff, 0xfe}, []byte{0xff, 0xfe}, true},
		{"string", int64(5), "5", false},
		{"string", nil, nil, false},
	}
	for _, c := range cases {
		got, w := normalizeValue(c.typ, c.in)
		if !reflect.DeepEqual(got, c.want) || (w != "") != c.warn {
			t.Errorf("normalize(%s, %#v) = %#v, %q; want %#v warn=%v", c.typ, c.in, got, w, c.want, c.warn)
		}
	}
}

func TestReadRedmineRoot(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "lib", "redmine"), 0o755)
	os.MkdirAll(filepath.Join(root, "config"), 0o755)
	os.WriteFile(filepath.Join(root, "lib", "redmine", "version.rb"), []byte("module Redmine\n  module VERSION\n    MAJOR = 6\n    MINOR = 1\n    TINY  = 2\n    BRANCH = 'stable'\n"), 0o644)
	os.WriteFile(filepath.Join(root, "config", "configuration.yml"), []byte("default:\n  database_cipher_key: secret\n  attachments_storage_path: /srv/files\nproduction:\n  attachments_storage_path: data/files\n"), 0o644)
	info, warns, err := readRedmineRoot(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "6.1.2.stable" || info.CipherKey != "secret" || !info.CipherKeyConfigured || info.AttachmentsPath != filepath.Join(root, "data/files") || len(warns) != 0 {
		t.Errorf("info = %+v warns=%v", info, warns)
	}
	os.WriteFile(filepath.Join(root, "lib", "redmine", "version.rb"), []byte("MAJOR = 5\nMINOR = 1\nTINY = 3\nBRANCH = nil\n"), 0o644)
	os.Remove(filepath.Join(root, "config", "configuration.yml"))
	info, warns, err = readRedmineRoot(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "5.1.3" || info.CipherKeyConfigured || info.AttachmentsPath != filepath.Join(root, "files") || len(warns) != 1 {
		t.Errorf("info = %+v warns=%v", info, warns)
	}
}

func TestMain_CLI(t *testing.T) {
	p := newSQLiteSynthetic(t, 1, 0)
	out := filepath.Join(t.TempDir(), "cli.tar.zst")
	var stdout, stderr bytes.Buffer
	err := mainWith([]string{"--dsn", "sqlite://" + p, "--source-timezone", "Asia/Tokyo", "--no-files", "-o", out, "--quiet"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "exported 54 tables") {
		t.Errorf("stdout = %s", stdout.String())
	}
	if !fileExists(out) || fileExists(out+".partial") {
		t.Error("output not finalized")
	}
	if err := mainWith([]string{"--dsn", "sqlite://" + p}, &stdout, &stderr); err == nil {
		t.Error("expected error for missing --source-timezone")
	}
	if err := mainWith([]string{"--dsn", "sqlite://" + p, "--source-timezone", "UTC", "-o", out + "2"}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "--files") {
		t.Errorf("expected --files error, got %v", err)
	}
}
