// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package scmtest は SCM のテスト用ヘルパー（Redmine の git フィクスチャリポジトリの場所）。
//
// フィクスチャは Redmine の test/fixtures/repositories/git_repository.tar.gz を
// _reference/scm-test/ に展開したもの（tar に含まれる macOS の "._*" ファイルは削除しておく）:
//
//	mkdir -p _reference/scm-test
//	tar xzf _reference/redmine/test/fixtures/repositories/git_repository.tar.gz -C _reference/scm-test
//	find _reference/scm-test -name '._*' -delete
//
// 環境変数 BUROPHER_SCM_TEST_GIT でパスを指定することもできる。
package scmtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// GitRepositoryPath はフィクスチャの git リポジトリのパスを返す。git コマンドか
// フィクスチャが無ければテストをスキップする。
func GitRepositoryPath(t testing.TB) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command is unavailable")
	}
	if p := os.Getenv("BUROPHER_SCM_TEST_GIT"); p != "" {
		return p
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Skip("getwd failed")
	}
	for {
		p := filepath.Join(dir, "_reference", "scm-test", "git_repository")
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Skip("git fixture repository (_reference/scm-test/git_repository) not found")
	return ""
}
