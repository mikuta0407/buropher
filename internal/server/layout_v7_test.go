// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"strings"
	"testing"
)

// TestClosedProjectWarningOnAllProjectPages は Redmine 7.0（#43818）の「終了したプロジェクトの警告を
// 概要だけでなく全プロジェクトページに表示する」を確認する（layouts/base で 1 回だけ出す）。
func TestClosedProjectWarningOnAllProjectPages(t *testing.T) {
	ts, d := newFixtureServer(t)
	if _, err := d.Exec(context.Background(), `UPDATE projects SET status = 5 WHERE identifier = 'ecookbook'`); err != nil {
		t.Fatal(err)
	}
	c := login(t, ts, "admin", "admin")
	const warning = `<p class="warning"><span class="icon icon-lock">`
	for _, path := range []string{"/projects/ecookbook", "/projects/ecookbook/issues", "/projects/ecookbook/wiki", "/projects/ecookbook/activity"} {
		res, body := get(t, c, ts.URL+path)
		if res.StatusCode != 200 {
			t.Fatalf("%s: status %d", path, res.StatusCode)
		}
		if n := strings.Count(body, warning); n != 1 {
			t.Errorf("%s: warning count = %d, want 1", path, n)
		}
		i := strings.Index(body, `<div id="content">`)
		if i < 0 || !strings.Contains(body[i:i+200], warning) {
			t.Errorf("%s: warning is not at the top of #content", path)
		}
	}
	// 稼働中のプロジェクトには出ない
	_, body := get(t, c, ts.URL+"/projects/onlinestore")
	if strings.Contains(body, warning) {
		t.Error("warning shown for an active project")
	}
}

// TestAccountDropdownMenu は Redmine 7.0（#31353 / #44293）のアカウントメニュー（ドロップダウン）を確認する。
func TestAccountDropdownMenu(t *testing.T) {
	ts, _ := newFixtureServer(t)
	c := login(t, ts, "jsmith", "jsmith")
	_, body := get(t, c, ts.URL+"/")
	for _, want := range []string{
		`<html lang="en" dir="ltr">`,
		`<nav class="top-menu" id="top-menu">`,
		`<div id="account" class="dropdown" data-controller="dropdown">`,
		`<span class="user-name">John Smith</span>`,
		`<span class="user-login">@jsmith</span>`,
		`<ul><li><a class="my-profile" href="/users/current">Profile</a></li><li><a class="my-account"`,
		`#icon--angle-down"></use></svg>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, `id="loggedas"`) || strings.Contains(body, "avatars-o") {
		t.Error("6.1 header markup remains")
	}
	_, body = get(t, newClient(t), ts.URL+"/")
	if !strings.Contains(body, `<div id="account" class="top-menu__links">`) {
		t.Error("anonymous account menu is not a plain link list")
	}
}
