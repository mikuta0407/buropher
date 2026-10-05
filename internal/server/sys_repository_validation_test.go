// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

// sys#create_project_repository も Repository の検証（予約済みの識別子・有効な SCM・長さ）を通す
// （以前は diff / raw などのリポジトリのサブルートと衝突する識別子や、無効にした Git で作成できた）。
func TestSysCreateRepositoryValidates(t *testing.T) {
	e := setupGit(t)
	e.set("sys_api_enabled", "1")
	e.set("sys_api_key", "secret")
	c := newClient(t)
	var ident string
	if err := e.d.Get(context.Background(), &ident, `SELECT identifier FROM projects WHERE status = 1 AND id NOT IN (SELECT project_id FROM repositories) ORDER BY id LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	create := func(form url.Values) int {
		form.Set("key", "secret")
		form.Set("vendor", "Git")
		form.Set("repository[url]", "/tmp/repo.git")
		res, _ := post(t, c, e.ts.URL+"/sys/projects/"+ident+"/repository", form)
		// 既定のリポジトリがあると 409 になるため、作成されたものは消しておく
		if _, err := e.d.Exec(context.Background(), `DELETE FROM repositories WHERE project_id = (SELECT id FROM projects WHERE identifier = ?)`, ident); err != nil {
			t.Fatal(err)
		}
		return res.StatusCode
	}
	for name, form := range map[string]url.Values{
		"reserved identifier": {"repository[identifier]": {"diff"}},
		"long identifier":     {"repository[identifier]": {strings.Repeat("a", 256)}},
		"long login":          {"repository[identifier]": {"r1"}, "repository[login]": {strings.Repeat("l", 61)}},
	} {
		if code := create(form); code != 422 {
			t.Errorf("%s: %d, want 422", name, code)
		}
	}
	e.set("enabled_scm", []string{"Subversion"})
	if code := create(url.Values{"repository[identifier]": {"r2"}}); code != 422 {
		t.Errorf("git disabled: %d, want 422", code)
	}
	e.set("enabled_scm", []string{"Subversion", "Git"})
	if code := create(url.Values{"repository[identifier]": {"r3"}}); code != 201 {
		t.Errorf("valid: %d, want 201", code)
	}
}
