// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package scm_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/scm"
	"github.com/mikuta0407/buropher/internal/scm/scmtest"
)

// test/unit/lib/redmine/scm/adapters/git_adapter_test.rb の一部と repository_git_test.rb の adapter 部分。

func heads(ctx context.Context, g *scm.Git) []string {
	var out []string
	for _, b := range g.Branches(ctx) {
		out = append(out, b.Scmid)
	}
	return out
}

func newGit(t *testing.T) *scm.Git {
	return scm.NewGit("", scmtest.GitRepositoryPath(t), "", "ISO-8859-1")
}

func TestGitBranchesTags(t *testing.T) {
	g := newGit(t)
	ctx := context.Background()
	want := []string{"issue-8857", "latin-1-branch-Ü-01", "latin-1-branch-Ü-02", "latin-1-path-encoding",
		"master", "master-20120212", "test-latin-1", "test_branch"}
	if got := g.BranchNames(ctx); !slices.Equal(got, want) {
		t.Errorf("branches = %q", got)
	}
	if got := g.DefaultBranch(ctx); got != "master-20120212" {
		t.Errorf("default branch = %q", got)
	}
	if got := g.Tags(ctx); !slices.Equal(got, []string{"tag00.lightweight", "tag01.annotated", "tag02.lightweight.Ü.01"}) {
		t.Errorf("tags = %q", got)
	}
}

func TestGitEntries(t *testing.T) {
	g := newGit(t)
	ctx := context.Background()
	es, ok := g.Entries(ctx, "", "master", false)
	if !ok {
		t.Fatal("entries failed")
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name)
	}
	want := []string{"images", "sources", "this_is_a_really_long_and_verbose_directory_name",
		" filename with a leading space.txt ", "README", "copied_README", "filemane with spaces.txt", "new_file.txt", "renamed_test.txt"}
	if !slices.Equal(names, want) {
		t.Errorf("entries = %q", names)
	}
	// latin-1 のディレクトリ
	es, ok = g.Entries(ctx, "latin-1-dir/test-Ü-subdir", "1ca7f5ed374f3cb31a93ae5215c2e25cc6ec5127", false)
	if !ok || len(es) != 3 || es[0].Name != "test-Ü-1.txt" {
		t.Errorf("latin-1 entries = %v %v", ok, es)
	}
	if e := g.Entry(ctx, "sources/watchers_controller.rb", ""); e == nil || e.Kind != "file" {
		t.Errorf("entry = %+v", e)
	}
	if e := g.Entry(ctx, "nope", ""); e != nil {
		t.Errorf("missing entry = %+v", e)
	}
}

func TestGitRevisions(t *testing.T) {
	g := newGit(t)
	ctx := context.Background()
	revs := g.Revisions(ctx, "", "", "", scm.RevisionsOptions{Reverse: true, Includes: heads(ctx, g)})
	if len(revs) != 29 {
		t.Fatalf("revisions = %d", len(revs))
	}
	first := revs[0]
	if first.Scmid != "7234cb2750b63f47bff735edc50a1c0a433c2518" || first.Author != "jsmith <jsmith@foo.bar>" ||
		first.Message != "Initial import.\nThe repository contains 3 files.\n" || len(first.Paths) != 3 {
		t.Errorf("first = %+v", first)
	}
	if first.Time.UTC().Format("2006-01-02 15:04:05") != "2007-12-14 09:22:52" {
		t.Errorf("time = %v", first.Time)
	}
	// パス指定・件数指定
	latest := g.Revisions(ctx, "images", "", "899a15dba", scm.RevisionsOptions{Limit: 10})
	var ids []string
	for _, r := range latest {
		ids = append(ids, r.Scmid)
	}
	if !slices.Equal(ids, []string{"899a15dba03a3b350b89c3f537e4bbe02a03cdc9", "7234cb2750b63f47bff735edc50a1c0a433c2518"}) {
		t.Errorf("latest = %q", ids)
	}
}

func TestGitDiffAnnotateCat(t *testing.T) {
	g := newGit(t)
	ctx := context.Background()
	diff, ok := g.Diff(ctx, "", "2f9c0091c754a91af7a9c478e36556b4bde8dcf7", "")
	if !ok || !strings.Contains(strings.Join(diff, ""), "def remove") {
		t.Errorf("diff ok=%v", ok)
	}
	a := g.Annotate(ctx, "sources/watchers_controller.rb", "")
	if a == nil || len(a.Lines) < 23 || !strings.HasPrefix(a.Revisions[22].Identifier, "2f9c0091") || a.Revisions[22].Author != "jsmith" {
		t.Errorf("annotate = %+v", a)
	}
	if !a.HasPrevious() {
		t.Error("annotate previous")
	}
	if a := g.Annotate(ctx, "images/edit.png", ""); a != nil {
		t.Error("binary annotate should be nil")
	}
	c, ok := g.Cat(ctx, "sources/watchers_controller.rb", "")
	if !ok || !strings.Contains(string(c), "WITHOUT ANY WARRANTY") {
		t.Error("cat")
	}
	if !g.ValidName(ctx, "test_branch") || g.ValidName(ctx, "HEAD") || g.ValidName(ctx, "nope") {
		t.Error("valid_name")
	}
}
