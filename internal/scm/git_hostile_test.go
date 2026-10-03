// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package scm_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mikuta0407/buropher/internal/scm"
)

// リポジトリの設定（diff.external・textconv ドライバ）で、差分・アノテートの表示時に
// サーバー上のコマンドを実行させない（リポジトリのパスはプロジェクトの管理者が指定できる）。
func TestGitDoesNotRunRepositoryConfiguredCommands(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command is unavailable")
	}
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	marker := filepath.Join(dir, "executed")
	script := filepath.Join(dir, "evil.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\ncat \"$1\" 2>/dev/null\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=a@example.net", "GIT_COMMITTER_NAME=a", "GIT_COMMITTER_EMAIL=a@example.net")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run("init", "-q")
	for _, s := range []string{"one\n", "one\ntwo\n"} {
		if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", "a.txt")
		run("commit", "-q", "-m", "c")
	}
	run("config", "diff.external", script)
	run("config", "diff.evil.textconv", script)
	if err := os.WriteFile(filepath.Join(repo, ".git", "info", "attributes"), []byte("* diff=evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	g := scm.NewGit("", filepath.Join(repo, ".git"), "", "UTF-8")
	ctx := context.Background()
	if _, ok := g.Diff(ctx, "", "HEAD", ""); !ok {
		t.Error("show failed")
	}
	if _, ok := g.Diff(ctx, "", "HEAD", "HEAD~1"); !ok {
		t.Error("diff failed")
	}
	if a := g.Annotate(ctx, "a.txt", "HEAD"); a == nil || len(a.Lines) != 2 {
		t.Errorf("annotate = %+v", a)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a command configured in the repository was executed")
	}
}
