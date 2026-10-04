// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"net/http"
	"net/url"
	"testing"
)

// SSO 必須モードでは、管理者以外のローカルのパスワードによる HTTP Basic 認証も受け付けない
// （ログイン画面だけ拒否しても、IdP 側で無効化されたユーザーがパスワードで API を使い続けられる）。
func TestOIDCSSORequiredBlocksBasicAuth(t *testing.T) {
	e := newSSOEnv(t, url.Values{"auth_source[sso_required]": {"1"}})
	basic := func(user, pw string) int {
		req, err := http.NewRequest(http.MethodGet, e.ts.URL+"/users/current.json", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.SetBasicAuth(user, pw)
		res, err := newClient(t).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := basic("dlopper", "foo"); code != http.StatusUnauthorized {
		t.Fatalf("non-admin basic auth with password under sso_required: %d, want 401", code)
	}
	if code := basic("admin", "admin"); code != http.StatusOK {
		t.Fatalf("admin basic auth: %d, want 200", code)
	}
	// API キーによる Basic 認証は引き続き使える
	key := authCreateToken(t, e.d, 3, "api")
	if code := basic(key, "x"); code != http.StatusOK {
		t.Fatalf("api key basic auth: %d, want 200", code)
	}
}
