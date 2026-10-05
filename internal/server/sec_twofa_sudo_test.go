// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/handler"
	"github.com/mikuta0407/buropher/internal/repository"
)

// このファイルは 2 要素認証のログイン 2 段目と sudo モードに対する攻撃（リプレイ・並列試行・
// sudo を経ない外部 ID 連携）を再現するテスト。

// secCloneClient は c のクッキー（base の分）を写した新しいクライアントを返す（盗んだクッキーの再生に使う）。
func secCloneClient(t *testing.T, c *http.Client, base string) *http.Client {
	t.Helper()
	u, _ := url.Parse(base)
	jar, _ := cookiejar.New(nil)
	jar.SetCookies(u, c.Jar.Cookies(u))
	return &http.Client{Jar: jar, CheckRedirect: c.CheckRedirect}
}

// secTwofaPost は 2 段目に誤ったコードを送り、Location を返す。
func secTwofaPost(t *testing.T, e *authEnv, c *http.Client, token string) string {
	t.Helper()
	form := url.Values{"twofa_code": {"000000"}, "authenticity_token": {token}}
	req, _ := http.NewRequest("POST", e.base+"/account/twofa", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := c.Do(req)
	if err != nil {
		t.Error(err)
		return ""
	}
	res.Body.Close()
	return strings.Replace(res.Header.Get("Location"), e.base, "", 1)
}

func secTwofaSetup(t *testing.T) (*authEnv, func() *http.Client) {
	t.Helper()
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	if err := repository.SetTwofaTotpKey(ctx, d, 2, "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"); err != nil {
		t.Fatal(err)
	}
	if err := repository.ActivateTwofa(ctx, d, 2, "totp", time.Now()); err != nil {
		t.Fatal(err)
	}
	e := &authEnv{t: t, base: ts.URL, clients: map[string]*http.Client{}, out: map[string]string{}}
	n := 0
	login := func() *http.Client {
		n++
		name := "attacker" + string(rune('a'+n))
		_, loc, _ := e.do(name, "POST", "/login", url.Values{"username": {"jsmith"}, "password": {"jsmith"}}, "")
		if loc != "/account/twofa/confirm" {
			t.Fatalf("password step: location %q", loc)
		}
		return e.clients[name]
	}
	return e, login
}

// TestTwofaSessionCookieReplay は、パスワード入力直後のセッションクッキーを保存しておき、
// 試行回数の上限に達した後に再生しても OTP の試行を続けられないことを確認する。
func TestTwofaSessionCookieReplay(t *testing.T) {
	e, login := secTwofaSetup(t)
	c := login()
	tok := e.csrf(c)
	saved := secCloneClient(t, c, e.base)
	if loc := secTwofaPost(t, e, c, tok); loc != "/account/twofa/confirm" {
		t.Fatalf("first attempt: %q", loc)
	}
	// 保存しておいたクッキー（試行回数 1 の状態）を再生する
	if loc := secTwofaPost(t, e, saved, tok); loc == "/account/twofa/confirm" {
		t.Fatalf("replayed twofa session cookie was accepted for another OTP attempt")
	}
}

// TestTwofaParallelAttempts は、同じクッキーで並列に OTP を送っても 1 回分しか試行として
// 受け付けない（twofa_session トークンが 1 回で使い捨てになる）ことを確認する。
func TestTwofaParallelAttempts(t *testing.T) {
	e, login := secTwofaSetup(t)
	c := login()
	tok := e.csrf(c)
	const n = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	start := make(chan struct{})
	for range n {
		cl := secCloneClient(t, c, e.base)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if secTwofaPost(t, e, cl, tok) == "/account/twofa/confirm" {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	t.Logf("accepted parallel attempts: %d", accepted)
	if accepted > 1 {
		t.Fatalf("%d parallel OTP attempts were accepted with a single twofa session token", accepted)
	}
}

// TestSudoModeOIDCLinkAndUnlink は sudo モードが有効なとき、外部 ID の連携開始・解除と
// Discord の連携開始が（メールアドレスの追加・削除と同じく）パスワードの再確認を求めることを確認する。
// sudo を経ずに攻撃者の IdP アカウントを連携できると、乗っ取ったセッションからパスワード変更後も残る
// ログイン手段を作れてしまう。
func TestSudoModeOIDCLinkAndUnlink(t *testing.T) {
	var app *handler.App
	e := newSSOEnv(t, nil, func(a *handler.App, _ chi.Router) { a.SudoMode = true; app = a })
	if err := app.Settings.Set(context.Background(), "autologin", "7"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.d.Exec(context.Background(), `INSERT INTO user_identities (user_id, provider, auth_source_id, subject, created_at) VALUES (2, ?, ?, 'js-sub', ?)`,
		"oidc:"+e.id, e.id, db.NewTime(time.Now())); err != nil {
		t.Fatal(err)
	}
	var identID string
	_ = e.d.Get(context.Background(), &identID, `SELECT CAST(id AS TEXT) FROM user_identities WHERE user_id = 2`)
	ae := &authEnv{t: t, base: e.ts.URL, clients: map[string]*http.Client{}, out: map[string]string{}}
	// 自動ログインで始まったセッション（sudo の時刻なし）= 乗っ取られたセッションを想定
	ae.autologinClient("victim", "jsmith")
	for _, tc := range []struct {
		method, path string
		form         url.Values
	}{
		{"POST", "/auth/oidc/" + e.id + "/start?mode=link", nil},
		{"POST", "/my/sso/" + identID, url.Values{"_method": {"delete"}}},
		{"GET", "/my/discord/link", nil},
	} {
		st, loc, body := ae.do("victim", tc.method, tc.path, tc.form, "")
		if !strings.Contains(body, `name="sudo_password"`) {
			t.Errorf("%s %s: sudo form not shown (status %d, location %q)", tc.method, tc.path, st, loc)
		}
	}
	var n int
	_ = e.d.Get(context.Background(), &n, `SELECT COUNT(*) FROM user_identities WHERE user_id = 2`)
	if n != 1 {
		t.Errorf("identity unlinked without sudo")
	}
	// パスワードを再入力すれば連携を開始できる（IdP へリダイレクト）
	st, loc, _ := ae.do("victim", "POST", "/auth/oidc/"+e.id+"/start?mode=link", url.Values{"sudo_password": {"jsmith"}}, "")
	if st != 302 || !strings.HasPrefix(loc, e.idp.URL()+"/authorize") {
		t.Errorf("link after sudo: %d %q", st, loc)
	}
}

// TestSudoModeOIDCSourceCreate は sudo モードが有効なとき、OIDC 認証方式の作成がパスワードの再確認を求めることを
// 確認する。match_by = mail（既定）の OIDC 認証方式は既存ユーザー（管理者を含む）にメールアドレスで紐付くため、
// 乗っ取った管理者セッションから攻撃者の IdP を登録できると全アカウントへのログイン手段を作れてしまう
// （更新・削除は Redmine と同じく require_sudo_mode 済み）。
func TestSudoModeOIDCSourceCreate(t *testing.T) {
	var app *handler.App
	e := newSSOEnv(t, nil, func(a *handler.App, _ chi.Router) { a.SudoMode = true; app = a })
	if err := app.Settings.Set(context.Background(), "autologin", "7"); err != nil {
		t.Fatal(err)
	}
	ae := &authEnv{t: t, base: e.ts.URL, clients: map[string]*http.Client{}, out: map[string]string{}}
	ae.autologinClient("hijacked", "admin")
	form := url.Values{
		"type": {"AuthSourceOidc"}, "auth_source[name]": {"Evil IdP"}, "auth_source[enabled]": {"1"},
		"auth_source[preset]": {"generic"}, "auth_source[issuer]": {"https://evil.example"}, "auth_source[client_id]": {"x"},
		"auth_source[match_by]": {"mail"},
	}
	st, loc, body := ae.do("hijacked", "POST", "/auth_sources", form, "")
	if !strings.Contains(body, `name="sudo_password"`) {
		t.Errorf("create OIDC source: sudo form not shown (status %d, location %q)", st, loc)
	}
	var n int
	_ = e.d.Get(context.Background(), &n, `SELECT COUNT(*) FROM auth_sources WHERE name = 'Evil IdP'`)
	if n != 0 {
		t.Fatal("OIDC auth source created without sudo")
	}
	// パスワードを再入力すれば作成できる
	form.Set("sudo_password", "admin")
	if st, _, _ := ae.do("hijacked", "POST", "/auth_sources", form, ""); st != 302 {
		t.Errorf("create after sudo: %d", st)
	}
	_ = e.d.Get(context.Background(), &n, `SELECT COUNT(*) FROM auth_sources WHERE name = 'Evil IdP'`)
	if n != 1 {
		t.Error("OIDC auth source not created after sudo")
	}
}
