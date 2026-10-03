// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/auth/totp"
	"github.com/mikuta0407/buropher/internal/crypto/secretbox"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/handler"
)

// このファイルはアカウント関連の振る舞いのテスト（参照 Redmine との画面比較は account_auth_test.go）。

// recMailer は送信されたメールを記録する AccountMailer。
type recMailer struct {
	handler.NopAccountMailer
	mu   sync.Mutex
	sent []string
}

func (m *recMailer) add(s string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, s)
}

func (m *recMailer) LostPassword(_ context.Context, u *domain.User, to, link string) error {
	m.add("lost_password " + u.Login + " " + to + " " + link)
	return nil
}
func (m *recMailer) Register(_ context.Context, u *domain.User, link string) error {
	m.add("register " + u.Login + " " + u.Mail + " " + link)
	return nil
}
func (m *recMailer) AccountActivationRequest(_ context.Context, u *domain.User, link string) error {
	m.add("activation_request " + u.Login + " " + link)
	return nil
}
func (m *recMailer) PasswordUpdated(_ context.Context, u, _ *domain.User) error {
	m.add("password_updated " + u.Login)
	return nil
}
func (m *recMailer) SecurityNotification(_ context.Context, u, _ *domain.User, n handler.SecurityNotice) error {
	m.add("security " + u.Login + " " + n.Message)
	return nil
}

func (m *recMailer) all() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return strings.Join(m.sent, "\n")
}

func TestAccountMailerCalls(t *testing.T) {
	myFreezeClock(t)
	mailer := &recMailer{}
	var app *handler.App
	ts, d := newFixtureServer(t, func(a *handler.App, _ chi.Router) { a.Mailer = mailer; app = a })
	ctx := context.Background()
	// パスワード再発行: 入力したアドレス（大文字小文字を無視して一致したユーザーのアドレス）へ、トークン付きの URL
	c := newClient(t)
	_, page := get(t, c, ts.URL+"/account/lost_password")
	post(t, c, ts.URL+"/account/lost_password", url.Values{"authenticity_token": {csrfToken(t, page)}, "mail": {"JSMITH@somenet.foo"}})
	var tok string
	if err := d.Get(ctx, &tok, `SELECT value FROM tokens WHERE user_id = 2 AND action = 'recovery'`); err != nil {
		t.Fatal(err)
	}
	want := "lost_password jsmith jsmith@somenet.foo http://localhost:3000/account/lost_password?token=" + tok
	if got := mailer.all(); got != want {
		t.Fatalf("lost password mail:\n got %q\nwant %q", got, want)
	}
	// 自己登録（管理者による有効化）→ 管理者への依頼メール
	_, page = get(t, c, ts.URL+"/account/register")
	post(t, c, ts.URL+"/account/register", url.Values{"authenticity_token": {csrfToken(t, page)}, "user[login]": {"reg1"}, "user[password]": {"reg1pass1"},
		"user[password_confirmation]": {"reg1pass1"}, "user[firstname]": {"R"}, "user[lastname]": {"One"}, "user[mail]": {"reg1@example.net"}})
	if !strings.Contains(mailer.all(), "activation_request reg1 http://localhost:3000/users?") {
		t.Fatalf("activation request mail:\n%s", mailer.all())
	}
	// メールによる有効化 → 登録メール
	if err := app.Settings.Set(ctx, "self_registration", "1"); err != nil {
		t.Fatal(err)
	}
	c2 := newClient(t)
	_, page = get(t, c2, ts.URL+"/account/register")
	post(t, c2, ts.URL+"/account/register", url.Values{"authenticity_token": {csrfToken(t, page)}, "user[login]": {"reg2"}, "user[password]": {"reg2pass1"},
		"user[password_confirmation]": {"reg2pass1"}, "user[firstname]": {"R"}, "user[lastname]": {"Two"}, "user[mail]": {"reg2@example.net"}})
	if !strings.Contains(mailer.all(), "register reg2 reg2@example.net http://localhost:3000/account/activate?token=") {
		t.Fatalf("register mail:\n%s", mailer.all())
	}
}

// TestTwofaSealedKeyLogin は secretbox 形式（インポート時に再暗号化された Redmine の鍵）の TOTP 鍵でログインできることと、
// 同じタイムステップのコードの再利用（リプレイ）が拒否されることを確認する。
func TestTwofaSealedKeyLogin(t *testing.T) {
	myFreezeClock(t)
	ts, d := newFixtureServer(t)
	box, err := secretbox.New("test-secret")
	if err != nil {
		t.Fatal(err)
	}
	key := totp.RandomKey()
	sealed, _ := box.Seal(key)
	if _, err := d.Exec(context.Background(), `UPDATE user_accounts SET twofa_scheme = 'totp', twofa_totp_key = ? WHERE principal_id = 2`, sealed); err != nil {
		t.Fatal(err)
	}
	loginTwofa := func(code string, autologin bool) (*http.Client, *http.Response) {
		c := newClient(t)
		_, page := get(t, c, ts.URL+"/login")
		form := url.Values{"authenticity_token": {csrfToken(t, page)}, "username": {"jsmith"}, "password": {"jsmith"}}
		if autologin {
			form.Set("autologin", "1")
		}
		res, _ := post(t, c, ts.URL+"/login", form)
		if !strings.HasSuffix(res.Header.Get("Location"), "/account/twofa/confirm") {
			t.Fatalf("login: %s", res.Header.Get("Location"))
		}
		_, page = get(t, c, ts.URL+"/account/twofa/confirm")
		res, _ = post(t, c, ts.URL+"/account/twofa", url.Values{"authenticity_token": {csrfToken(t, page)}, "twofa_code": {code}})
		return c, res
	}
	code := totp.Now(key, frozenTime)
	if _, res := loginTwofa(code, false); !strings.HasSuffix(res.Header.Get("Location"), "/my/page") {
		t.Fatalf("sealed key login: %s", res.Header.Get("Location"))
	}
	if _, res := loginTwofa(code, false); !strings.HasSuffix(res.Header.Get("Location"), "/account/twofa/confirm") {
		t.Fatalf("replayed code must be rejected: %s", res.Header.Get("Location"))
	}
	var last int64
	_ = d.Get(context.Background(), &last, `SELECT twofa_totp_last_used_at FROM user_accounts WHERE principal_id = 2`)
	if last != frozenTime.Unix()/30*30 {
		t.Fatalf("last used step: %d", last)
	}
}

// TestTwofaSessionExpires は 2 段目のトークンが約 2 分で失効することを確認する。
func TestTwofaSessionExpires(t *testing.T) {
	myFreezeClock(t)
	now := frozenTime
	var app *handler.App
	ts, d := newFixtureServer(t, func(a *handler.App, _ chi.Router) { app = a })
	app.Now = func() time.Time { return now }
	key := totp.RandomKey()
	if _, err := d.Exec(context.Background(), `UPDATE user_accounts SET twofa_scheme = 'totp', twofa_totp_key = ? WHERE principal_id = 2`, key); err != nil {
		t.Fatal(err)
	}
	c := newClient(t)
	_, page := get(t, c, ts.URL+"/login")
	post(t, c, ts.URL+"/login", url.Values{"authenticity_token": {csrfToken(t, page)}, "username": {"jsmith"}, "password": {"jsmith"}})
	now = frozenTime.Add(3 * time.Minute)
	res, _ := get(t, c, ts.URL+"/account/twofa/confirm")
	if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/") {
		t.Fatalf("expired twofa session: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
}

// TestSudoModeDisabledByDefault は既定（config の sudo_mode 無効）ではパスワード再入力を求めないことを確認する。
func TestSudoModeDisabledByDefault(t *testing.T) {
	ts, _ := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")
	_, body := get(t, c, ts.URL+"/settings")
	if strings.Contains(body, "sudo-form") || !strings.Contains(body, `id="tab-general"`) {
		t.Fatal("sudo form must not be shown when sudo mode is disabled")
	}
}
