// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGitPathRegexp は scm.git_path_regexp（Redmine の scm_git_path_regexp）で登録できるパスを制限できること。
func TestGitPathRegexp(t *testing.T) {
	e := setupGit(t)
	e.app.GitPathRegexp = `/var/git/%project%(\.[a-z0-9_-]+)?\.git`
	_, body := post(t, e.admin, e.ts.URL+"/projects/subproject1/repositories", contentForm(t, e.admin, e.ts,
		"repository_scm", "Git", "repository[url]", "/etc", "repository[identifier]", "evil"))
	if !strings.Contains(body, "Path to repository is invalid") {
		t.Errorf("url outside of git_path_regexp should be invalid\n%s", contentMain(body))
	}
	res, body := post(t, e.admin, e.ts.URL+"/projects/subproject1/repositories", contentForm(t, e.admin, e.ts,
		"repository_scm", "Git", "repository[url]", "/var/git/subproject1.foo.git", "repository[identifier]", "ok"))
	expectContentRedirect(t, res, body, "/projects/subproject1/settings/repositories")
}

// TestGitRevArgumentInjection は rev / rev_to にオプション（--output=...）を渡しても git に渡らないこと。
func TestGitRevArgumentInjection(t *testing.T) {
	e := setupGit(t)
	out := filepath.Join(t.TempDir(), "out")
	for _, q := range []string{
		"/diff?rev=" + url.QueryEscape("--output="+out),
		"/diff?rev=master&rev_to=" + url.QueryEscape("--output="+out),
		"/diff.diff?rev=" + url.QueryEscape("--output="+out),
		"/entry/README?rev=" + url.QueryEscape("--output="+out),
		"/annotate/README?rev=" + url.QueryEscape("--output="+out),
		"/raw/README?rev=" + url.QueryEscape("--output="+out),
	} {
		res, _ := get(t, e.admin, e.base()+q)
		if res.StatusCode != 404 {
			t.Errorf("%s: status %d", q, res.StatusCode)
		}
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("git wrote --output file")
	}
}

// TestGitRawDownloadSandboxed はリポジトリの raw / diff のダウンロードに、添付ファイルと同じ CSP の sandbox が付くこと
// （リポジトリに置かれた PDF は inline で表示されるため、このオリジンでスクリプトを動かさせない）。
func TestGitRawDownloadSandboxed(t *testing.T) {
	e := setupGit(t)
	for _, p := range []string{
		"/raw/sources/watchers_controller.rb",
		"/revisions/2f9c0091c754a91af7a9c478e36556b4bde8dcf7/diff.diff",
	} {
		res, _ := get(t, e.admin, e.base()+p)
		if res.StatusCode != 200 {
			t.Fatalf("%s: status %d", p, res.StatusCode)
		}
		if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") || !strings.Contains(csp, "default-src 'none'") {
			t.Errorf("%s: Content-Security-Policy = %q", p, csp)
		}
	}
	// 見つからない場合は通常のエラーページ（CSP は残さない）
	res, _ := get(t, e.admin, e.base()+"/raw/no_such_file")
	if res.StatusCode != 404 || res.Header.Get("Content-Security-Policy") != "" {
		t.Errorf("missing raw: status %d, csp %q", res.StatusCode, res.Header.Get("Content-Security-Policy"))
	}
}
