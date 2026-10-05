// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/bootstrap"
	"github.com/mikuta0407/buropher/internal/config"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/server"
	"github.com/mikuta0407/buropher/internal/settings"
)

// newTestServer は init 済みの DB（参照 Redmine と同じく REST API を有効化）でサーバを起動する。
func newTestServer(t *testing.T, extra map[string]any) (*httptest.Server, *db.DB) {
	t.Helper()
	ctx := context.Background()
	d := dbtest.New(t)
	if err := bootstrap.Init(ctx, d, bootstrap.Options{AdminPassword: "admin"}); err != nil {
		t.Fatal(err)
	}
	st, err := settings.New(ctx, repository.SettingsStore{DB: d})
	if err != nil {
		t.Fatal(err)
	}
	vals := map[string]any{"rest_api_enabled": "1"}
	for k, v := range extra {
		vals[k] = v
	}
	for k, v := range vals {
		if err := st.Set(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := server.CheckInitialized(ctx, d); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Server.SecretKey = "test-secret"
	srv, err := server.New(cfg, d, server.Options{TempDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	setRecaptureFrom(ts.URL)
	return ts, d
}

func newClient(t *testing.T) *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

var (
	csrfMetaRe  = regexp.MustCompile(`content="[A-Za-z0-9_-]{40,}"`)
	csrfInputRe = regexp.MustCompile(`name="authenticity_token" value="[A-Za-z0-9_-]{40,}"`)
	formNameRe  = regexp.MustCompile(`form-[0-9a-f]{8}"`)
	digestRe    = regexp.MustCompile(`-[0-9a-f]{8}\.(css|js|svg|ico)`)
	tokenRe     = regexp.MustCompile(`name="authenticity_token" value="([A-Za-z0-9_-]+)"`)
)

// normalize は CSRF トークン・form の乱数名・アセットのダイジェストを伏せる
// （testdata の期待値は参照 Redmine 7.0.1 の生の出力に同じ置換をしたもの）。
func normalize(s string) string {
	s = csrfMetaRe.ReplaceAllString(s, `content="{{CSRF}}"`)
	s = csrfInputRe.ReplaceAllString(s, `name="authenticity_token" value="{{CSRF}}"`)
	s = formNameRe.ReplaceAllString(s, `form-RANDOM"`)
	return digestRe.ReplaceAllString(s, "-DIGEST.$1")
}

func get(t *testing.T, c *http.Client, u string) (*http.Response, string) {
	t.Helper()
	res, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := readUnbranded(res.Body)
	return res, string(b)
}

func post(t *testing.T, c *http.Client, u string, form url.Values) (*http.Response, string) {
	t.Helper()
	res, err := c.PostForm(u, form)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := readUnbranded(res.Body)
	return res, string(b)
}

func compareGolden(t *testing.T, name, got string) {
	t.Helper()
	if recaptureGolden(t, "testdata/"+name, normalize(got), recaptureBase()) {
		return
	}
	want, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if g := normalize(got); g != string(want) {
		gl, wl := strings.Split(g, "\n"), strings.Split(string(want), "\n")
		for i := 0; i < len(gl) || i < len(wl); i++ {
			var a, b string
			if i < len(gl) {
				a = gl[i]
			}
			if i < len(wl) {
				b = wl[i]
			}
			if a != b {
				t.Fatalf("%s: line %d differs\n got: %q\nwant: %q", name, i+1, a, b)
			}
		}
	}
}

func csrfToken(t *testing.T, body string) string {
	t.Helper()
	m := tokenRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("authenticity_token not found")
	}
	return m[1]
}

// TestLoginPageMatchesRedmine は匿名の /login が参照 Redmine とバイト単位で一致することを確認する。
func TestLoginPageMatchesRedmine(t *testing.T) {
	ts, _ := newTestServer(t, nil)
	c := newClient(t)
	res, body := get(t, c, ts.URL+"/login")
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	if cc := res.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	compareGolden(t, "login_anonymous.html", body)

	// 誤ったパスワード: flash.now のエラーと入力値・back_url の保持
	res, body = post(t, c, ts.URL+"/login", url.Values{
		"authenticity_token": {csrfToken(t, body)}, "username": {"admin"}, "password": {"wrong"}, "back_url": {"/projects"},
	})
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	compareGolden(t, "login_invalid_credentials.html", body)
}

func TestLoginLogoutFlow(t *testing.T) {
	ts, d := newTestServer(t, map[string]any{"autologin": "7"})
	c := newClient(t)
	_, body := get(t, c, ts.URL+"/login")
	if !strings.Contains(body, `<label for="autologin"><input type="checkbox" name="autologin" id="autologin" value="1" tabindex="4" /> Stay logged in</label>`) {
		t.Error("autologin checkbox missing")
	}
	res, _ := post(t, c, ts.URL+"/login", url.Values{
		"authenticity_token": {csrfToken(t, body)}, "username": {"admin"}, "password": {"admin"}, "autologin": {"1"},
	})
	if res.StatusCode != 302 || res.Header.Get("Location") != ts.URL+"/my/page" {
		t.Fatalf("login: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	var autologin bool
	for _, ck := range res.Cookies() {
		if ck.Name == "autologin" && ck.Value != "" && ck.HttpOnly {
			autologin = true
		}
	}
	if !autologin {
		t.Error("autologin cookie not set")
	}
	var n int
	if err := d.Get(context.Background(), &n, `SELECT COUNT(*) FROM user_accounts WHERE login = 'admin' AND last_login_at IS NOT NULL`); err != nil || n != 1 {
		t.Errorf("last_login_at not updated (%d, %v)", n, err)
	}
	if err := d.Get(context.Background(), &n, `SELECT COUNT(*) FROM tokens WHERE action = 'autologin'`); err != nil || n != 1 {
		t.Errorf("autologin token count = %d, %v", n, err)
	}

	res, body = get(t, c, ts.URL+"/")
	if res.StatusCode != 200 {
		t.Fatalf("home status %d", res.StatusCode)
	}
	for _, want := range []string{
		`<span class="user-login">@admin</span>`,
		`<a class="my-page" href="/my/page">My page</a>`,
		`<a class="administration" href="/admin">Administration</a>`,
		`<a class="logout" rel="nofollow" data-method="post" href="/logout">Sign out</a>`,
		`<span role="img" class="avatar-color-`,
		`/news.atom?key=`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("home page lacks %q", want)
		}
	}

	// ログイン済みで /login は back_url / Referer / ホームへ
	res, _ = get(t, c, ts.URL+"/login")
	if res.StatusCode != 302 || res.Header.Get("Location") != ts.URL+"/" {
		t.Errorf("logged-in /login: %d %s", res.StatusCode, res.Header.Get("Location"))
	}

	// GET /logout はフォームを表示する
	res, body = get(t, c, ts.URL+"/logout")
	if res.StatusCode != 200 || !strings.Contains(body, `<input type="submit" name="commit" value="Sign out" data-disable-with="Sign out" />`) {
		t.Fatalf("logout form: %d", res.StatusCode)
	}
	res, _ = post(t, c, ts.URL+"/logout", url.Values{"authenticity_token": {csrfToken(t, body)}})
	if res.StatusCode != 302 || res.Header.Get("Location") != ts.URL+"/" {
		t.Fatalf("logout: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if err := d.Get(context.Background(), &n, `SELECT COUNT(*) FROM tokens WHERE action = 'autologin'`); err != nil || n != 0 {
		t.Errorf("autologin token not deleted (%d, %v)", n, err)
	}
	_, body = get(t, c, ts.URL+"/")
	if !strings.Contains(body, `<a class="login" href="/login">Sign in</a>`) {
		t.Error("not logged out")
	}
}

func TestLockedUser(t *testing.T) {
	ts, d := newTestServer(t, nil)
	if _, err := d.Exec(context.Background(), `UPDATE principals SET status = 3 WHERE kind = 'user'`); err != nil {
		t.Fatal(err)
	}
	c := newClient(t)
	_, body := get(t, c, ts.URL+"/login")
	res, _ := post(t, c, ts.URL+"/login", url.Values{"authenticity_token": {csrfToken(t, body)}, "username": {"admin"}, "password": {"admin"}})
	if res.StatusCode != 302 || res.Header.Get("Location") != ts.URL+"/login" {
		t.Fatalf("locked: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	_, body = get(t, c, ts.URL+"/login")
	if !strings.Contains(body, `<div class="flash error" id="flash_error">`) || !strings.Contains(body, "Your account is locked.") {
		t.Error("locked message missing")
	}
}

func TestLoginRequiredAndErrors(t *testing.T) {
	ts, _ := newTestServer(t, map[string]any{"login_required": "1"})
	c := newClient(t)
	res, _ := get(t, c, ts.URL+"/")
	if want := ts.URL + "/login?back_url=" + url.QueryEscape(ts.URL+"/"); res.StatusCode != 302 || res.Header.Get("Location") != want {
		t.Errorf("login_required: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	// login_required でもログイン画面は表示され、検索・トップメニューは出ない
	res, body := get(t, c, ts.URL+"/login")
	if res.StatusCode != 200 || strings.Contains(body, `id="quick-search"`) || strings.Contains(body, `class="home"`) {
		t.Errorf("login page with login_required: %d", res.StatusCode)
	}
	res, body = get(t, c, ts.URL+"/no/such/route")
	if res.StatusCode != 404 || !strings.Contains(body, "Page not found") {
		t.Errorf("404: %d", res.StatusCode)
	}
	res, body = get(t, c, ts.URL+"/healthz")
	if res.StatusCode != 200 || body != "ok" {
		t.Errorf("healthz: %d %q", res.StatusCode, body)
	}
	// CSRF トークンなしの POST は 422 のエラーページ（common/error）
	res, body = post(t, c, ts.URL+"/login", url.Values{"username": {"admin"}, "password": {"admin"}})
	if res.StatusCode != 422 || !strings.Contains(body, "<h2>422</h2>") || !strings.Contains(body, "Invalid form authenticity token.") ||
		!strings.Contains(body, `<title>422 - Redmine</title>`) || !strings.Contains(body, `class="controller-account action-login"`) {
		t.Errorf("csrf failure: %d\n%s", res.StatusCode, body)
	}
}

func TestLocalization(t *testing.T) {
	ts, _ := newTestServer(t, nil)
	req, _ := http.NewRequest("GET", ts.URL+"/login", nil)
	req.Header.Set("Accept-Language", "ja,en-US;q=0.8")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := readUnbranded(res.Body)
	res.Body.Close()
	if !strings.Contains(string(b), `<html lang="ja" dir="ltr">`) || !strings.Contains(string(b), "ログイン") {
		t.Error("Accept-Language not applied")
	}
}
