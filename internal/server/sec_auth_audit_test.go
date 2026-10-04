// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/handler"
)

// TestOAuthSwitchUserKeepsTokenScope は、admin スコープの OAuth トークンで X-Redmine-Switch-User を使っても、
// 切り替え先のユーザーにトークンのスコープが引き継がれることを確認する。
func TestOAuthSwitchUserKeepsTokenScope(t *testing.T) {
	ts, _ := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	access := oauthTokenFor(t, ts.URL, admin, admin, "admin")
	for _, name := range []string{"admin", "jsmith"} {
		h := bearer(access)
		h["X-Redmine-Switch-User"] = name
		res, body := oauthDo(t, newClient(t), http.MethodGet, ts.URL+"/my/account.json", nil, h)
		if res.StatusCode != 200 {
			t.Fatalf("switch to %s: my/account.json: %d %s", name, res.StatusCode, body)
		}
		u, _ := decodeJSON(t, body)["user"].(map[string]any)
		if u["login"] != name {
			t.Errorf("switch to %s: login = %v", name, u["login"])
		}
		if k, ok := u["api_key"]; ok {
			t.Errorf("switch to %s: api_key leaked to OAuth token: %v", name, k)
		}
		if name == "admin" {
			// 管理者は admin スコープがあれば何でもできる（User#allowed_to? の admin?）
			continue
		}
		// view_issues スコープが無いので、管理者でない切り替え先ではチケットを見られない
		res, body = oauthDo(t, newClient(t), http.MethodGet, ts.URL+"/issues/1.json", nil, h)
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("switch to %s: issues/1.json without view_issues scope: %d %s", name, res.StatusCode, body)
		}
	}
}

// TestSudoModeBulkUserAndRoleActions は sudo モードが有効なとき、ユーザーの一括削除・一括ロック／ロック解除と
// ロールの権限の一括編集が（destroy・update と同じく）パスワードの再確認を求めることを確認する。
func TestSudoModeBulkUserAndRoleActions(t *testing.T) {
	var app *handler.App
	ts, d := newFixtureServer(t, func(a *handler.App, _ chi.Router) { a.SudoMode = true; app = a })
	ctx := context.Background()
	if err := app.Settings.Set(ctx, "autologin", "7"); err != nil {
		t.Fatal(err)
	}
	ae := &authEnv{t: t, base: ts.URL, clients: map[string]*http.Client{}, out: map[string]string{}}
	ae.autologinClient("hijacked", "admin")
	status := func() int {
		var st int
		if err := d.Get(ctx, &st, `SELECT status FROM principals WHERE id = 2`); err != nil {
			t.Fatal(err)
		}
		return st
	}
	perms := func() int {
		var n int
		if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM role_permissions WHERE role_id = 1`); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := perms()
	for _, tc := range []struct {
		path string
		form url.Values
	}{
		{"/users/bulk_lock", url.Values{"ids[]": {"2"}}},
		{"/users/bulk_unlock", url.Values{"ids[]": {"2"}}},
		{"/users/bulk_destroy", url.Values{"_method": {"delete"}, "ids[]": {"2"}, "confirm": {"Yes"}}},
		{"/roles/permissions", url.Values{"permissions[1][]": {"view_issues"}}},
	} {
		st, loc, body := ae.do("hijacked", "POST", tc.path, tc.form, "")
		if !strings.Contains(body, `name="sudo_password"`) {
			t.Errorf("POST %s: sudo form not shown (status %d, location %q)", tc.path, st, loc)
		}
	}
	if st := status(); st != 1 {
		t.Errorf("user 2 status = %d after bulk actions without sudo", st)
	}
	if n := perms(); n != before {
		t.Errorf("role 1 permissions changed without sudo: %d -> %d", before, n)
	}
	// パスワードを再入力すればロックできる
	if st, _, _ := ae.do("hijacked", "POST", "/users/bulk_lock", url.Values{"ids[]": {"2"}, "sudo_password": {"admin"}}, ""); st != 302 {
		t.Errorf("bulk_lock after sudo: %d", st)
	}
	if st := status(); st != 3 {
		t.Errorf("user 2 status = %d after sudo bulk_lock, want 3", st)
	}
}
