// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package scm

import "testing"

// 7.0.2 #44476: ブランチ名に 40 桁の 16 進数を含んでもリビジョンと取り違えない
// （git_adapter_test.rb test_branches_with_branch_name_containing_hash）。
func TestGitBranchLineWithBranchNameContainingHash(t *testing.T) {
	name := "feature-0123456789abcdef0123456789abcdef01234567"
	m := reBranch.FindStringSubmatch("* " + name + " 2a682156a3b6e77a8bf9cd4590e8db757f3c6c78 Add foo.txt")
	if m == nil {
		t.Fatal("no match")
	}
	if m[1] != "*" || m[2] != name || m[3] != "2a682156a3b6e77a8bf9cd4590e8db757f3c6c78" {
		t.Errorf("default=%q name=%q rev=%q", m[1], m[2], m[3])
	}
	// 通常の行
	m = reBranch.FindStringSubmatch("  master 83ca5fd546063a3c7dc2e568ba3355661a9e2b2c Merge")
	if m == nil || m[1] != "" || m[2] != "master" || m[3] != "83ca5fd546063a3c7dc2e568ba3355661a9e2b2c" {
		t.Errorf("plain line: %q", m)
	}
}
