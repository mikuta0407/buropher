// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package scm

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestGitConcurrencyLimit は同時に動かす git の数が gitSem で制限され、空きを待つ間に要求が
// 取り消されれば git を起動せずに失敗すること。
func TestGitConcurrencyLimit(t *testing.T) {
	n := cap(gitSem)
	for range n {
		gitSem <- struct{}{}
	}
	defer func() {
		for range n {
			<-gitSem
		}
	}()
	g := NewGit("", t.TempDir(), "", "")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := g.gitCmd(ctx, []string{"--version"}, nil)
	if !errors.Is(err, ErrCommandAborted) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) < 90*time.Millisecond {
		t.Error("git should wait for a free slot")
	}
}
