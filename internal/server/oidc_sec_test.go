// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/handler"
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

// SSO 必須モードでは、自己登録（自動有効化）で作ったパスワードのアカウントでもログインしたままにさせない
// （登録の直後にセッションを開始すると、SSO を経ないパスワードログインと同じになる）。
func TestOIDCSSORequiredRegisterDoesNotLogIn(t *testing.T) {
	var app *handler.App
	e := newSSOEnv(t, url.Values{"auth_source[sso_required]": {"1"}}, func(a *handler.App, _ chi.Router) { app = a })
	if err := app.Settings.Set(context.Background(), "self_registration", "3"); err != nil {
		t.Fatal(err)
	}
	c := newClient(t)
	_, page := get(t, c, e.ts.URL+"/account/register")
	res, body := post(t, c, e.ts.URL+"/account/register", url.Values{"authenticity_token": {csrfToken(t, page)},
		"user[login]": {"selfreg"}, "user[password]": {"selfreg123"}, "user[password_confirmation]": {"selfreg123"},
		"user[firstname]": {"Self"}, "user[lastname]": {"Reg"}, "user[mail]": {"selfreg@example.net"}, "user[language]": {"en"}})
	if res.StatusCode != http.StatusFound {
		t.Fatalf("register: %d\n%s", res.StatusCode, body)
	}
	if n := queryInt(t, e.d, `SELECT COUNT(*) FROM user_accounts WHERE login = 'selfreg'`); n != 1 {
		t.Fatalf("account not created: %d", n)
	}
	if res, _ := get(t, c, e.ts.URL+"/my/account"); res.StatusCode != http.StatusFound {
		t.Errorf("logged in after self-registration under sso_required: /my/account status %d", res.StatusCode)
	}
}
