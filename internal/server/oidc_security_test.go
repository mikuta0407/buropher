// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/auth/totp"
)

// このファイルは OIDC ログインのセキュリティ上の性質（2 要素認証の迂回・オープンリダイレクト・
// セッション固定化）のテスト。

// IdP の多要素認証を信頼する設定（skip_twofa、既定で有効）でも、メールアドレスの一致だけで
// buropher の 2 要素認証を有効にしているユーザーに自動で紐付けてはならない
// （紐付けると、そのメールアドレスを名乗れる IdP のアカウントで以後 2 要素認証を迂回してログインできる）。
func TestOIDCAutoLinkDoesNotBypassLocalTwofa(t *testing.T) {
	e := newSSOEnv(t, nil)
	ctx := context.Background()
	if _, err := e.d.Exec(ctx, `UPDATE user_accounts SET twofa_scheme = 'totp', twofa_totp_key = ? WHERE principal_id = 2`, totp.RandomKey()); err != nil {
		t.Fatal(err)
	}
	e.idp.SetClaims(map[string]any{"sub": "sub-attacker", "email": "jsmith@somenet.foo"})
	c := newClient(t)
	res := e.ssoLogin(c, "")
	if msg := e.flashAfter(c, res); !strings.Contains(msg, "two-factor authentication") {
		t.Fatalf("flash %q (Location %s)", msg, res.Header.Get("Location"))
	}
	if u := e.currentUser(c); u != "" {
		t.Fatalf("logged in as %q", u)
	}
	var n int
	if err := e.d.Get(ctx, &n, `SELECT COUNT(*) FROM user_identities WHERE user_id = 2`); err != nil || n != 0 {
		t.Fatalf("identity created: %d %v", n, err)
	}
	// ログイン中に「連携する」で紐付けたあとは IdP の多要素認証を信頼してログインできる
	if _, err := e.d.Exec(ctx, `UPDATE user_accounts SET twofa_scheme = NULL WHERE principal_id = 2`); err != nil {
		t.Fatal(err)
	}
	jc := login(t, e.ts, "jsmith", "jsmith")
	e.ssoLogin(jc, "?mode=link")
	if _, err := e.d.Exec(ctx, `UPDATE user_accounts SET twofa_scheme = 'totp' WHERE principal_id = 2`); err != nil {
		t.Fatal(err)
	}
	c2 := newClient(t)
	e.ssoLogin(c2, "")
	if u := e.currentUser(c2); u != "Logged in as jsmith" {
		t.Fatalf("after manual link: %q", u)
	}
}

// back_url は外部のサイトへのリダイレクトに使えない。
func TestOIDCBackURLIsNotAnOpenRedirect(t *testing.T) {
	e := newSSOEnv(t, nil)
	e.idp.SetClaims(map[string]any{"sub": "sub-jsmith", "email": "jsmith@somenet.foo"})
	for _, back := range []string{"https://evil.example/", "//evil.example/", "/\\evil.example/", "javascript:alert(1)"} {
		c := newClient(t)
		res := e.ssoLogin(c, "?back_url="+url.QueryEscape(back))
		loc := res.Header.Get("Location")
		lu, err := url.Parse(loc)
		// 自サイトの絶対 URL（パスに文字列が残るのは可）であること
		if res.StatusCode != http.StatusFound || err != nil || !strings.HasPrefix(loc, e.ts.URL+"/") || lu.Host != strings.TrimPrefix(e.ts.URL, "http://") {
			t.Fatalf("back_url %q: %d %s", back, res.StatusCode, loc)
		}
	}
}

// SSO ログインでセッションは作り直され、ログイン前のクッキーはログイン状態にならない。
func TestOIDCLoginRotatesSession(t *testing.T) {
	e := newSSOEnv(t, nil)
	e.idp.SetClaims(map[string]any{"sub": "sub-jsmith", "email": "jsmith@somenet.foo"})
	c := newClient(t)
	res, _ := get(t, c, e.ts.URL+"/auth/oidc/"+e.id+"/start")
	u, _ := url.Parse(e.ts.URL)
	var before string
	for _, ck := range c.Jar.Cookies(u) {
		if ck.Name == "_redmine_session" {
			before = ck.Value
		}
	}
	if before == "" {
		t.Fatal("no session cookie before login")
	}
	res2, _ := get(t, c, res.Header.Get("Location"))
	get(t, c, res2.Header.Get("Location"))
	if e.currentUser(c) != "Logged in as jsmith" {
		t.Fatal("sso login failed")
	}
	old := newClient(t)
	old.Jar.SetCookies(u, []*http.Cookie{{Name: "_redmine_session", Value: before, Path: "/"}})
	if cu := e.currentUser(old); cu != "" {
		t.Fatalf("pre-login cookie is logged in as %q", cu)
	}
}
