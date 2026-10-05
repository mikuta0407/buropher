// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/handler"
)

// TestSudoModeAdminMembershipTwofaLDAPSync は sudo モードが有効なとき、管理画面からのメンバーシップの
// 追加・変更・削除、他ユーザーの 2 要素認証の解除、LDAP 同期がパスワードの再確認を求めることを確認する
// （members#create 等と同じ扱い）。
func TestSudoModeAdminMembershipTwofaLDAPSync(t *testing.T) {
	var app *handler.App
	ts, d := newFixtureServer(t, func(a *handler.App, _ chi.Router) { a.SudoMode = true; app = a })
	ctx := context.Background()
	if err := app.Settings.Set(ctx, "autologin", "7"); err != nil {
		t.Fatal(err)
	}
	now := db.NewTime(time.Now())
	if _, err := d.Exec(ctx, `INSERT INTO auth_sources (id, kind, name, created_at, updated_at) VALUES (1, 'ldap', 'LDAP test server', ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	ae := &authEnv{t: t, base: ts.URL, clients: map[string]*http.Client{}, out: map[string]string{}}
	ae.autologinClient("hijacked", "admin")
	members := func() int {
		return int(queryInt(t, d, `SELECT COUNT(*) FROM members WHERE principal_id = 2`))
	}
	before := members()
	newProject := queryString(t, d, `SELECT CAST(MIN(id) AS VARCHAR(20)) FROM projects WHERE status = 1 AND id NOT IN (SELECT project_id FROM members WHERE principal_id = 2)`)
	memberID := queryString(t, d, `SELECT CAST(MIN(id) AS VARCHAR(20)) FROM members WHERE principal_id = 2`)
	for _, tc := range []struct {
		path string
		form url.Values
	}{
		{"/users/2/memberships", url.Values{"membership[project_ids][]": {newProject}, "membership[role_ids][]": {"1"}}},
		{"/users/2/memberships/" + memberID, url.Values{"_method": {"patch"}, "membership[role_ids][]": {"1"}}},
		{"/users/2/memberships/" + memberID, url.Values{"_method": {"delete"}}},
		{"/users/2/twofa/deactivate", url.Values{}},
		{"/auth_sources/1/sync", url.Values{}},
	} {
		st, loc, body := ae.do("hijacked", "POST", tc.path, tc.form, "")
		if !strings.Contains(body, `name="sudo_password"`) {
			t.Errorf("POST %s %s: sudo form not shown (status %d, location %q)", tc.form.Get("_method"), tc.path, st, loc)
		}
	}
	if n := members(); n != before {
		t.Errorf("memberships of user 2 changed without sudo: %d -> %d", before, n)
	}
	// パスワードを再入力すれば追加できる
	if st, _, _ := ae.do("hijacked", "POST", "/users/2/memberships", url.Values{"membership[project_ids][]": {newProject}, "membership[role_ids][]": {"1"},
		"sudo_password": {"admin"}}, ""); st != http.StatusFound {
		t.Errorf("create membership after sudo: %d", st)
	}
	if n := members(); n != before+1 {
		t.Errorf("memberships of user 2 after sudo = %d, want %d", n, before+1)
	}
}
