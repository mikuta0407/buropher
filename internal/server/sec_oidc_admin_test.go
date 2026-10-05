// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"net/url"
	"strings"
	"testing"
)

// TestOIDCNoAutoLinkToAdmin は、「既存ユーザーとの突合」（ログイン ID・メールアドレス）で管理者には
// 自動で紐付けないことを確かめる。preferred_username は IdP の利用者が変更できることがあり、
// IdP 側で admin に合わせるだけで管理者としてログインできた。
func TestOIDCNoAutoLinkToAdmin(t *testing.T) {
	for _, tc := range []struct {
		matchBy string
		claims  map[string]any
	}{
		{"login", map[string]any{"sub": "sub-attacker", "preferred_username": "admin", "email": "attacker@example.net"}},
		{"mail", map[string]any{"sub": "sub-attacker", "preferred_username": "attacker", "email": "admin@somenet.foo"}},
	} {
		t.Run(tc.matchBy, func(t *testing.T) {
			e := newSSOEnv(t, url.Values{"auth_source[match_by]": {tc.matchBy}})
			e.idp.SetClaims(tc.claims)
			c := newClient(t)
			msg := e.flashAfter(c, e.ssoLogin(c, ""))
			if u := e.currentUser(c); u == "Logged in as admin" {
				t.Fatalf("logged in as admin by matching %s", tc.matchBy)
			}
			if !strings.Contains(msg, "not linked to administrator accounts automatically") {
				t.Errorf("flash %q", msg)
			}
			if n := queryInt(t, e.d, `SELECT COUNT(*) FROM user_identities`); n != 0 {
				t.Errorf("identity linked: %d", n)
			}
			// 管理者以外は従来どおり突合する
			if tc.matchBy == "login" {
				e.idp.SetClaims(map[string]any{"sub": "sub-jsmith", "preferred_username": "jsmith"})
			} else {
				e.idp.SetClaims(map[string]any{"sub": "sub-jsmith", "email": "jsmith@somenet.foo"})
			}
			c2 := newClient(t)
			if res := e.ssoLogin(c2, ""); res.StatusCode != 302 || e.currentUser(c2) != "Logged in as jsmith" {
				t.Fatalf("non-admin match: %d %q", res.StatusCode, e.currentUser(c2))
			}
		})
	}
}
