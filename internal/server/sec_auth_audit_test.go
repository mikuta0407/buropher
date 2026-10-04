// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"net/http"
	"testing"
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
