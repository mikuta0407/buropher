// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package importer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/redmineimport/verify"
)

// ドライランは何も残さず、2 回目のインポート(空でない DB)は失敗する。
func TestDryRunAndNonEmpty(t *testing.T) {
	ref, srcDB, files := fixturePaths(t)
	dir := workDir(t, ref)
	archivePath := exportFixture(t, dir, srcDB, files, "UTC", true)
	d := newTarget(t, dir)
	ctx := context.Background()
	filesDir := filepath.Join(dir, "files")

	rep, err := Run(ctx, d, archivePath, Options{DryRun: true, FilesDir: filesDir, NewCipherKey: "k", TempDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Committed || !rep.DryRun || rep.Lookup("issues").Imported["issues"] != 14 {
		t.Errorf("dry-run report: committed=%v issues=%v", rep.Committed, rep.Lookup("issues").Imported)
	}
	for _, tb := range []string{"principals", "issues", "settings", "projects_closure_dummy"} {
		if tb == "projects_closure_dummy" {
			tb = "project_closure"
		}
		if n := q1[int](t, d, "SELECT COUNT(*) FROM "+tb); n != 0 {
			t.Errorf("dry run left %d rows in %s", n, tb)
		}
	}
	if ents, _ := os.ReadDir(filesDir); len(ents) != 0 {
		t.Errorf("dry run wrote files: %v", ents)
	}

	if _, err := Run(ctx, d, archivePath, Options{FilesDir: filesDir, NewCipherKey: "k", TempDir: dir}); err != nil {
		t.Fatal(err)
	}
	_, err = Run(ctx, d, archivePath, Options{FilesDir: filesDir, NewCipherKey: "k", TempDir: dir})
	if !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("second import: %v", err)
	}
	if n := q1[int](t, d, "SELECT COUNT(*) FROM issues"); n != 14 {
		t.Errorf("issues after failed second import = %d", n)
	}
	// ステージング用ディレクトリは残らない
	ents, _ := os.ReadDir(filesDir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".buropher-import-") {
			t.Errorf("staging dir left: %s", e.Name())
		}
	}
	// 一時展開ディレクトリも残らない
	tmp, _ := filepath.Glob(filepath.Join(dir, "buropher-import-*"))
	if len(tmp) != 0 {
		t.Errorf("temp dirs left: %v", tmp)
	}
}

// 未マイグレーションの DB は拒否する。
func TestRequiresMigratedDB(t *testing.T) {
	ref := findReference(t)
	dir := workDir(t, ref)
	d, err := db.Open(context.Background(), "sqlite", filepath.Join(dir, "raw.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := Run(context.Background(), d, filepath.Join(dir, "none.tar.zst"), Options{}); err == nil {
		t.Fatal("expected error")
	}
}

// CLI(Main 相当)でインポートと検証を通す。
func TestMainCLI(t *testing.T) {
	ref, srcDB, files := fixturePaths(t)
	dir := workDir(t, ref)
	archivePath := exportFixture(t, dir, srcDB, files, "UTC", false)
	dsn := filepath.Join(dir, "cli.db")
	var out, errOut bytes.Buffer
	jsonPath := filepath.Join(dir, "report.json")
	err := mainWith([]string{"--driver", "sqlite", "--dsn", dsn, "--migrate", "--quiet", "--files-dir", filepath.Join(dir, "files"),
		"--source-files-dir", files, "--secret-key", "k", "--temp-dir", dir, "--report-json", jsonPath, archivePath}, &out, &errOut)
	if err != nil {
		t.Fatalf("import CLI: %v\n%s\n%s", err, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "committed") || !strings.Contains(out.String(), "issues") {
		t.Errorf("text report:\n%s", out.String())
	}
	var rep Report
	b, _ := os.ReadFile(jsonPath)
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatal(err)
	}
	if !rep.Committed || rep.Lookup("issues").Imported["issues"] != 14 || rep.Files.Source != "dir" {
		t.Errorf("json report: %+v", rep)
	}
	out.Reset()
	if err := verify.Main([]string{"--driver", "sqlite", "--dsn", dsn, "--files-dir", filepath.Join(dir, "files"), "--password", "admin:admin", archivePath}); err != nil {
		t.Fatalf("verify CLI: %v", err)
	}
	// 2 回目は空でないので失敗する
	if err := mainWith([]string{"--dsn", dsn, "--quiet", "--files-dir", "", "--temp-dir", dir, archivePath}, &out, &errOut); err == nil {
		t.Fatal("second import should fail")
	}
}

func TestParseWallAndTZ(t *testing.T) {
	for in, want := range map[string]string{
		"2026-01-15 12:00:00":        "2026-01-15 12:00:00.000000",
		"2026-01-15 12:00:00.123456": "2026-01-15 12:00:00.123456",
		"2026-01-15":                 "2026-01-15 00:00:00.000000",
		"2026-01-15T01:02":           "2026-01-15 01:02:00.000000",
	} {
		w, ok := parseWall(in)
		if !ok || w.Format("2006-01-02 15:04:05.000000") != want {
			t.Errorf("parseWall(%q) = %v %v", in, w, ok)
		}
	}
	for _, bad := range []string{"", "0000-00-00 00:00:00", "2026-02-30 00:00:00", "yesterday"} {
		if _, ok := parseWall(bad); ok {
			t.Errorf("parseWall(%q) should fail", bad)
		}
	}
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	c := &tzconv{loc: tokyo}
	if s, _ := c.ts("2026-10-03 00:18:03.703306"); s != "2026-10-02T15:18:03.703306Z" {
		t.Errorf("tokyo = %s", s)
	}
	ber, _ := time.LoadLocation("Europe/Berlin")
	c = &tzconv{loc: ber}
	// 2025-10-26 02:30 は CEST/CET の両方 → 早い方 (CEST, 00:30Z)
	if s, _ := c.ts("2025-10-26 02:30:00"); s != "2025-10-26T00:30:00.000000Z" || c.ambiguous != 1 {
		t.Errorf("berlin ambiguous = %s (%d)", s, c.ambiguous)
	}
	// 2025-03-30 02:30 は存在しない → 03:30 CEST (01:30Z)
	if s, _ := c.ts("2025-03-30 02:30:00"); s != "2025-03-30T01:30:00.000000Z" || c.nonexistent != 1 {
		t.Errorf("berlin gap = %s (%d)", s, c.nonexistent)
	}
	if s, _ := c.ts("2025-07-01 12:00:00"); s != "2025-07-01T10:00:00.000000Z" {
		t.Errorf("berlin summer = %s", s)
	}
}

func TestValueHelpers(t *testing.T) {
	for v, want := range map[any]bool{"t": true, "f": false, int64(1): true, "0": false, true: true, "true": true} {
		if b, ok := toBool(v); !ok || b != want {
			t.Errorf("toBool(%v) = %v %v", v, b, ok)
		}
	}
	if s, _ := toStr([]byte{0xe9, 't', 0xe9}); s != "été" {
		t.Errorf("latin1 = %q", s)
	}
	if got := hierPath([]int64{12, 45}); got != "0000000012/0000000045/" {
		t.Errorf("hierPath = %q", got)
	}
	parent := map[int64]int64{1: 2, 2: 3, 3: 1, 4: 1, 5: 0}
	if b := breakCycles(parent); len(b) != 1 || b[0] != 1 || parent[1] != 0 {
		t.Errorf("breakCycles = %v %v", b, parent)
	}
	if v, changed := normalizeCFValue("bool", "t"); v != "t" || changed {
		t.Errorf("bool = %v", v)
	}
	if v, _ := normalizeCFValue("int", " 12 "); v != "12" {
		t.Errorf("int = %v", v)
	}
	if v, _ := normalizeCFValue("string", ""); v != "" {
		t.Errorf("empty = %v", v)
	}
	if v, _ := normalizeCFValue("string", nil); v != nil {
		t.Errorf("nil = %v", v)
	}
	if s, err := inflate([]byte("plain"), ""); err != nil || s != "plain" {
		t.Errorf("inflate = %q %v", s, err)
	}
	if _, err := inflate([]byte("x"), "lz4"); err == nil {
		t.Error("unknown compression should fail")
	}
	if rubyToI("12abc") != 12 || rubyToI("x") != 0 {
		t.Error("rubyToI")
	}
	if _, err := cleanRelPath("../etc/passwd"); err == nil {
		t.Error("cleanRelPath should reject ..")
	}
}
