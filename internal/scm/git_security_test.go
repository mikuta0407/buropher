// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package scm_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/scm"
)

// runGit はテスト用リポジトリの作成に使う git 実行（失敗したらテストを止める）。
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return runGitIn(t, dir, "", args...)
}

// runGitIn は標準入力を与えて git を実行する。
func runGitIn(t *testing.T, dir, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=a@example.com",
		"GIT_COMMITTER_NAME=a", "GIT_COMMITTER_EMAIL=a@example.com", "GIT_CONFIG_NOSYSTEM=1", "HOME="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// maliciousRepo は、設定でコマンドを実行させようとするベアリポジトリを作る
// （textconv・fsmonitor・pager・外部 diff・blame の ignoreRevsFile 等）。marker の下にファイルができれば実行された。
func maliciousRepo(t *testing.T) (repo, marker string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	marker = filepath.Join(base, "pwned")
	if err := os.Mkdir(marker, 0o755); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(base, "w")
	runGit(t, base, "init", "-q", work)
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-qm", "1")
	if err := os.WriteFile(filepath.Join(work, "f.txt"), []byte("a\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "commit", "-qam", "2")
	repo = filepath.Join(base, "r.git")
	runGit(t, base, "clone", "-q", "--bare", work, repo)
	if err := os.WriteFile(filepath.Join(repo, "info", "attributes"), []byte("* diff=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sh := func(name string) string {
		return "sh -c 'touch " + filepath.Join(marker, name) + "; cat \"$1\"' --"
	}
	cfg := map[string]string{
		"diff.x.textconv":   sh("textconv"),
		"diff.x.command":    sh("diffcmd"),
		"diff.external":     sh("external"),
		"core.fsmonitor":    sh("fsmonitor"),
		"core.pager":        sh("pager"),
		"core.hooksPath":    marker,
		"core.sshCommand":   sh("ssh"),
		"gpg.program":       sh("gpg"),
		"log.showSignature": "true",
	}
	for k, v := range cfg {
		runGit(t, base, "--git-dir", repo, "config", k, v)
	}
	// 署名付きに見えるコミットを先端に置く（log.showSignature で gpg.program が起動されないこと）
	head := strings.TrimSpace(runGit(t, base, "--git-dir", repo, "rev-parse", "HEAD"))
	raw := runGit(t, base, "--git-dir", repo, "cat-file", "commit", head)
	hdr, msg, _ := strings.Cut(raw, "\n\n")
	lines := append(strings.Split(hdr, "\n"),
		"gpgsig -----BEGIN PGP SIGNATURE-----", " ", " iQEzBAABCAAdFiEE", " -----END PGP SIGNATURE-----")
	signed := strings.TrimSpace(runGitIn(t, base, strings.Join(lines, "\n")+"\n\n"+msg,
		"--git-dir", repo, "hash-object", "-t", "commit", "-w", "--stdin"))
	runGit(t, base, "--git-dir", repo, "update-ref", "HEAD", signed)
	// 部分クローンの遅延取得（promisor リモート）で ext:: トランスポートのコマンドが起動されないこと
	for k, v := range map[string]string{
		"extensions.partialClone": "origin",
		"remote.origin.promisor":  "true",
		"remote.origin.url":       "ext::" + sh("ext"),
		"protocol.ext.allow":      "always",
		"protocol.allow":          "always",
	} {
		runGit(t, base, "--git-dir", repo, "config", k, v)
	}
	runGit(t, base, "--git-dir", repo, "config", "core.repositoryformatversion", "1")
	return repo, marker
}

// TestGitMaliciousRepoConfigDoesNotExecute は、リポジトリの設定（管理者以外も中身を用意できる）で
// 任意コマンドが実行されないこと。
func TestGitMaliciousRepoConfigDoesNotExecute(t *testing.T) {
	repo, marker := maliciousRepo(t)
	g := scm.NewGit("", repo, "", "")
	ctx := context.Background()
	bs := g.BranchNames(ctx)
	if len(bs) == 0 {
		t.Fatal("no branches")
	}
	heads := []string{}
	for _, b := range g.Branches(ctx) {
		heads = append(heads, b.Scmid)
	}
	g.Tags(ctx)
	g.Entries(ctx, "", "", true)
	revs := g.Revisions(ctx, "", "", "", scm.RevisionsOptions{Includes: heads})
	if len(revs) != 2 {
		t.Fatalf("revisions = %d", len(revs))
	}
	g.Diff(ctx, "", revs[0].Scmid, "")
	g.Diff(ctx, "f.txt", revs[0].Scmid, revs[1].Scmid)
	if a := g.Annotate(ctx, "f.txt", ""); a == nil || len(a.Lines) != 2 {
		t.Errorf("annotate = %+v", a)
	}
	if b, ok := g.Cat(ctx, "f.txt", ""); !ok || string(b) != "a\nc\n" {
		t.Errorf("cat = %q %v", b, ok)
	}
	var buf strings.Builder
	if ok := g.CatTo(ctx, "f.txt", "", &buf); !ok || buf.String() != "a\nc\n" {
		t.Errorf("cat to = %q %v", buf.String(), ok)
	}
	if g.CatTo(ctx, "nope.txt", "", &buf) {
		t.Error("cat to of a missing path succeeded")
	}
	g.Lastrev(ctx, "f.txt", "")
	g.ValidName(ctx, bs[0])
	// 存在しないオブジェクト（promisor リモートからの遅延取得を誘う）
	missing := strings.Repeat("1", 40)
	g.Cat(ctx, "f.txt", missing)
	g.Diff(ctx, "", missing, "")
	g.Annotate(ctx, "f.txt", missing)
	ents, _ := os.ReadDir(marker)
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	if len(names) > 0 {
		t.Errorf("commands from repository config were executed: %s", strings.Join(names, ", "))
	}
}

// TestGitArgumentInjection は、リビジョンやパスに "-" で始まる値を渡してもオプションとして解釈されないこと
// （git diff --output=<file> でのファイル書き込み等）。
func TestGitArgumentInjection(t *testing.T) {
	repo, marker := maliciousRepo(t)
	g := scm.NewGit("", repo, "", "")
	ctx := context.Background()
	out := filepath.Join(marker, "output")
	inj := "--output=" + out
	g.Diff(ctx, "", inj, "")
	g.Diff(ctx, "", "HEAD", inj)
	g.Diff(ctx, inj, "HEAD", "HEAD~1")
	g.Annotate(ctx, "f.txt", inj)
	g.Annotate(ctx, inj, "HEAD")
	g.Cat(ctx, "f.txt", inj)
	g.Entries(ctx, "", inj, false)
	g.Lastrev(ctx, "f.txt", inj)
	g.Revisions(ctx, "", inj, "HEAD", scm.RevisionsOptions{})
	g.Revisions(ctx, "", "", "", scm.RevisionsOptions{Includes: []string{inj}, Excludes: []string{inj}})
	if g.ValidName(ctx, inj) {
		t.Error("ValidName accepted an option")
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("argument injection: git wrote --output file")
	}
}
