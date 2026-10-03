// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/handler"
)

// sendForm は任意のヘッダ付きでフォームを送る。
func sendForm(t *testing.T, c *http.Client, method, u string, form url.Values, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, u, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

func userFirstname(t *testing.T, d *db.DB, login string) string {
	t.Helper()
	var fn string
	if err := d.Get(context.Background(), &fn, `SELECT p.firstname FROM principals p JOIN user_accounts u ON u.principal_id = p.id WHERE u.login = ?`, login); err != nil {
		t.Fatal(err)
	}
	return fn
}

func sessionCookie(c *http.Client, u *url.URL) string {
	for _, ck := range c.Jar.Cookies(u) {
		if ck.Name == "_redmine_session" {
			return ck.Value
		}
	}
	return ""
}

// TestCSRFNotSkippedByAuthHeadersWithSessionCookie は、ログイン済みのセッションクッキーを持つ
// ブラウザに対するクロスサイトのフォーム送信で、偽の API キーヘッダ・Basic・Bearer・key パラメータを
// 付けても CSRF 検証が省略されない（かつセッションで認証された更新が行われない）ことを確認する。
func TestCSRFNotSkippedByAuthHeadersWithSessionCookie(t *testing.T) {
	ts, d := newFixtureServer(t)
	before := userFirstname(t, d, "jsmith")
	cases := []struct {
		name string
		hdr  map[string]string
		path string
	}{
		{"api-key-header", map[string]string{"X-Redmine-API-Key": "bogus"}, "/my/account"},
		{"buropher-api-key-header", map[string]string{"X-Buropher-API-Key": "bogus"}, "/my/account"},
		{"basic", map[string]string{"Authorization": "Basic Ym9ndXM6Ym9ndXM="}, "/my/account"},
		{"bearer", map[string]string{"Authorization": "Bearer bogus"}, "/my/account"},
		{"key-param", nil, "/my/account?key=bogus"},
		{"method-override-header", map[string]string{"X-HTTP-Method-Override": "PUT"}, "/my/account"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := login(t, ts, "jsmith", "jsmith")
			res, _ := sendForm(t, c, "POST", ts.URL+tc.path, url.Values{"_method": {"put"}, "user[firstname]": {"Pwned"}}, tc.hdr)
			if res.StatusCode != http.StatusUnprocessableEntity {
				t.Errorf("status %d, want 422 (CSRF)", res.StatusCode)
			}
			if got := userFirstname(t, d, "jsmith"); got != before {
				t.Fatalf("firstname changed to %q without CSRF token", got)
			}
		})
	}
}

// TestAPIRequestDoesNotUseSessionCookie は、.json / .xml の API リクエスト（CSRF 検証なし）が
// セッションクッキーで認証されないことを確認する（CSRF 省略とセッション認証の組み合わせの防止）。
func TestAPIRequestDoesNotUseSessionCookie(t *testing.T) {
	ts, d := newFixtureServer(t)
	c := login(t, ts, "jsmith", "jsmith")
	before := userFirstname(t, d, "jsmith")
	for _, p := range []string{"/my/account.json", "/my/account.xml", "/my/account?format=json"} {
		res, _ := sendForm(t, c, "PUT", ts.URL+p, url.Values{"user[firstname]": {"Pwned"}}, nil)
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", p, res.StatusCode)
		}
	}
	if got := userFirstname(t, d, "jsmith"); got != before {
		t.Fatalf("firstname changed to %q via API with session cookie", got)
	}
}

// TestLogoutInvalidatesServerSession は、ログイン時にセッション ID が変わることと、ログアウト後に
// 以前のセッションクッキーを再送してもログイン状態に戻らない（サーバ側で破棄されている）ことを確認する。
func TestLogoutInvalidatesServerSession(t *testing.T) {
	ts, _ := newFixtureServer(t)
	c := newClient(t)
	_, body := get(t, c, ts.URL+"/login")
	u, _ := url.Parse(ts.URL)
	pre := sessionCookie(c, u)
	res, _ := post(t, c, ts.URL+"/login", url.Values{"authenticity_token": {csrfToken(t, body)}, "username": {"jsmith"}, "password": {"jsmith"}})
	if res.StatusCode != http.StatusFound {
		t.Fatalf("login status %d", res.StatusCode)
	}
	loggedIn := sessionCookie(c, u)
	if loggedIn == "" || loggedIn == pre {
		t.Fatalf("session cookie not rotated on login: %q -> %q", pre, loggedIn)
	}
	_, body = get(t, c, ts.URL+"/my/page")
	res, _ = post(t, c, ts.URL+"/logout", url.Values{"authenticity_token": {csrfMeta(t, body)}})
	if res.StatusCode != http.StatusFound {
		t.Fatalf("logout status %d", res.StatusCode)
	}
	// 盗んだクッキーの再利用
	c2 := newClient(t)
	c2.Jar.SetCookies(u, []*http.Cookie{{Name: "_redmine_session", Value: loggedIn}})
	res, _ = get(t, c2, ts.URL+"/my/page")
	if res.StatusCode == http.StatusOK {
		t.Fatal("old session cookie still authenticates after logout")
	}
}

// このファイルは認証・セッション・トークンまわりの攻撃を再現するテスト（セキュリティ監査で追加）。

// minLoginDuration は POST /login の応答時間の最小値（n 回試行）を返す。
func minLoginDuration(t *testing.T, base, user, pw string, n int) time.Duration {
	t.Helper()
	best := time.Duration(1<<63 - 1)
	for range n {
		c := newClient(t)
		_, body := get(t, c, base+"/login")
		tok := csrfToken(t, body)
		start := time.Now()
		res, _ := post(t, c, base+"/login", url.Values{"authenticity_token": {tok}, "username": {user}, "password": {pw}})
		d := time.Since(start)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("login %s: status %d", user, res.StatusCode)
		}
		best = min(best, d)
	}
	return best
}

// TestLoginTimingDoesNotRevealAccounts は、存在しないログイン名・ローカルのパスワードを持たない
// ユーザーへのログイン試行が、実在ユーザー（argon2id）の誤パスワードと同程度の時間をかけることを確認する。
// 以前は存在しないログイン名ではパスワード照合を行わず即座に応答したため、応答時間（argon2id 1 回分、
// 数十 ms）の差でアカウントの有無を列挙できた。
func TestLoginTimingDoesNotRevealAccounts(t *testing.T) {
	ts, _ := newFixtureServer(t)
	// jsmith のハッシュを argon2id に移行させる（Redmine 形式はログイン成功時に再ハッシュされる）
	login(t, ts, "jsmith", "jsmith")
	existing := minLoginDuration(t, ts.URL, "jsmith", "wrong-password", 5)
	missing := minLoginDuration(t, ts.URL, "no-such-user-xyz", "wrong-password", 5)
	// 未移行（Redmine 形式 SHA1）のユーザーの誤パスワードも同程度
	legacy := minLoginDuration(t, ts.URL, "dlopper", "wrong-password", 5)
	t.Logf("existing=%v missing=%v legacy=%v", existing, missing, legacy)
	if missing*3 < existing {
		t.Errorf("non-existent login answered much faster (%v) than existing user (%v): account enumeration by timing", missing, existing)
	}
	if legacy*3 < existing {
		t.Errorf("legacy-hash user answered much faster (%v) than argon2id user (%v)", legacy, existing)
	}
}

// TestLoginRequiresCSRFToken は、ログインフォームの送信に CSRF トークンが必要なこと
// （攻撃者のアカウントで被害者をログインさせるログイン CSRF の防止）を確認する。
func TestLoginRequiresCSRFToken(t *testing.T) {
	ts, _ := newFixtureServer(t)
	c := newClient(t)
	get(t, c, ts.URL+"/login")
	res, _ := post(t, c, ts.URL+"/login", url.Values{"username": {"jsmith"}, "password": {"jsmith"}})
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("login without token: status %d, want 422", res.StatusCode)
	}
	if res, _ := get(t, c, ts.URL+"/my/page"); res.StatusCode == http.StatusOK {
		t.Error("logged in without CSRF token")
	}
}

// TestBlankWSKeyRejected は、リポジトリ管理・受信メールの WS を有効にしたまま鍵を設定していない場合に、
// key なし・空の key で WS を呼べないことを確認する（未認証でのリポジトリ作成・課題作成の防止）。
func TestBlankWSKeyRejected(t *testing.T) {
	var app *handler.App
	ts, _ := newFixtureServer(t, func(a *handler.App, _ chi.Router) { app = a })
	ctx := context.Background()
	for k, v := range map[string]string{"sys_api_enabled": "1", "sys_api_key": "", "mail_handler_api_enabled": "1", "mail_handler_api_key": ""} {
		if err := app.Settings.Set(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	c := newClient(t)
	for _, p := range []string{"/sys/projects", "/sys/projects?key=", "/sys/fetch_changesets?key="} {
		if res, _ := get(t, c, ts.URL+p); res.StatusCode != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", p, res.StatusCode)
		}
	}
	res, _ := post(t, c, ts.URL+"/mail_handler", url.Values{"key": {""}, "email": {"From: jsmith@somenet.foo\r\nSubject: x\r\n\r\nProject: ecookbook\r\n"}})
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("mail_handler: status %d, want 403", res.StatusCode)
	}
}

// TestBulkLockDestroysSessions は、ユーザー一覧の一括ロックでロックしたユーザーのセッションが破棄され、
// ロック解除後に以前のセッションクッキーでログイン状態に戻らないことを確認する。
func TestBulkLockDestroysSessions(t *testing.T) {
	ts, _ := newFixtureServer(t)
	victim := login(t, ts, "jsmith", "jsmith")
	admin := login(t, ts, "admin", "admin")
	for _, action := range []string{"bulk_lock", "bulk_unlock"} {
		_, body := get(t, admin, ts.URL+"/users")
		res, _ := post(t, admin, ts.URL+"/users/"+action, url.Values{"authenticity_token": {csrfMeta(t, body)}, "ids[]": {"2"}})
		if res.StatusCode != http.StatusFound {
			t.Fatalf("%s: status %d", action, res.StatusCode)
		}
	}
	if res, _ := get(t, victim, ts.URL+"/my/page"); res.StatusCode == http.StatusOK {
		t.Fatal("session of locked user revived after unlock")
	}
}

// TestNonXHRGetJavaScriptBlocked は、XHR でない GET への JavaScript 応答（<script src> で別オリジンから
// 読み込める）が 422 になり、フォームの CSRF トークンなどを含む本文を返さないことを確認する
// （Rails の verify_same_origin_request。SameSite=Lax のクッキーは同一サイトの別オリジン
// ＝兄弟サブドメインからの <script> 読み込みにも送られるため、CSRF トークンを盗まれる）。
func TestNonXHRGetJavaScriptBlocked(t *testing.T) {
	ts, _ := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")
	for _, p := range []string{"/custom_fields/new.js?type=IssueCustomField", "/time_entries/bulk_edit.js?ids[]=1"} {
		t.Run(p, func(t *testing.T) {
			res, body := get(t, c, ts.URL+p)
			if res.StatusCode != http.StatusUnprocessableEntity || strings.Contains(body, "authenticity_token") {
				t.Errorf("status %d content-type %q, body leaks token=%v", res.StatusCode, res.Header.Get("Content-Type"), strings.Contains(body, "authenticity_token"))
			}
			// XHR なら従来どおり返す
			req, _ := http.NewRequest("GET", ts.URL+p, nil)
			req.Header.Set("X-Requested-With", "XMLHttpRequest")
			res2, err := c.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res2.Body.Close()
			if res2.StatusCode != http.StatusOK {
				t.Errorf("xhr status %d", res2.StatusCode)
			}
		})
	}
}
