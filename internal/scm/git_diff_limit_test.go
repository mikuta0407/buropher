// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package scm_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/scm"
)

// TestGitDiffHeadReadsOnlyHead は DiffHead が差分の先頭 maxLines 行だけを返し（巨大な差分を丸ごと読まない）、
// DiffTo が Diff と同じ内容を書き出すこと。
func TestGitDiffHeadReadsOnlyHead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	work := filepath.Join(base, "w")
	runGit(t, base, "init", "-q", work)
	var big strings.Builder
	for range 20000 {
		big.WriteString("line\n")
	}
	if err := os.WriteFile(filepath.Join(work, "big.txt"), []byte(big.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", ".")
	runGit(t, work, "commit", "-qm", "big")
	head := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))

	g := scm.NewGit("", filepath.Join(work, ".git"), "", "")
	ctx := context.Background()
	full, ok := g.Diff(ctx, "", head, "")
	if !ok || len(full) < 20000 {
		t.Fatalf("Diff: ok=%v lines=%d", ok, len(full))
	}
	part, ok := g.DiffHead(ctx, "", head, "", 25)
	if !ok || len(part) != 25 {
		t.Fatalf("DiffHead: ok=%v lines=%d", ok, len(part))
	}
	for i := range part {
		if part[i] != full[i] {
			t.Fatalf("line %d: %q != %q", i, part[i], full[i])
		}
	}
	// 上限より短い差分はそのまま
	if all, ok := g.DiffHead(ctx, "", head, "", len(full)+10); !ok || len(all) != len(full) {
		t.Errorf("DiffHead with a large limit: ok=%v lines=%d want %d", ok, len(all), len(full))
	}
	var buf bytes.Buffer
	if !g.DiffTo(ctx, "", head, "", &buf) || buf.String() != strings.Join(full, "") {
		t.Error("DiffTo differs from Diff")
	}
	if _, ok := g.DiffHead(ctx, "", "nonexistent", "", 25); ok {
		t.Error("DiffHead of an unknown revision should fail")
	}
	if g.DiffTo(ctx, "", "--output=x", "", &buf) {
		t.Error("DiffTo must reject option-like revisions")
	}
}
