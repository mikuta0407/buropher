// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/handler"
	"github.com/mikuta0407/buropher/internal/repository"
)

// TestRaceActivationVsLock は、登録の有効化リンクの処理中に管理者がそのユーザーをロックしても、
// 有効化がロックを上書きしてユーザーを有効にしないことを確認する。
// 修正前は「トークンとユーザー（登録済み）を読む → status を無条件に有効へ更新」だったため、
// 読んだ後にコミットされたロックが失われた。管理者のロックのトランザクションを、有効化が
// ユーザーを読んだ後もコミットせずに保持して再現する。
func TestRaceActivationVsLock(t *testing.T) {
	raceForEachDB(t, func(t *testing.T, d *db.DB) {
		srv, ts := newFixtureServerOn(t, d)
		ctx := context.Background()
		if err := srv.App().Settings.Set(ctx, "self_registration", "1"); err != nil {
			t.Fatal(err)
		}
		const uid = 7 // someone
		if _, err := d.Exec(ctx, `UPDATE principals SET status = 2 WHERE id = ?`, uid); err != nil {
			t.Fatal(err)
		}
		tok, err := repository.CreateToken(ctx, d, uid, repository.TokenRegister)
		if err != nil {
			t.Fatal(err)
		}
		// 管理者のロック: 行をロックしたトランザクションを開いたままにする
		tx, err := d.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.Exec(ctx, `SELECT id FROM principals WHERE id = ?`+db.ForUpdate(tx), uid); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			res, err := newClient(t).Get(ts.URL + "/account/activate?token=" + tok.Value)
			if err == nil {
				res.Body.Close()
			}
		}()
		time.Sleep(700 * time.Millisecond) // 有効化がトークンとユーザーを読み、更新で待つまで
		if _, err := tx.Exec(ctx, `UPDATE principals SET status = 3 WHERE id = ?`, uid); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		<-done
		var status int
		if err := d.Get(ctx, &status, `SELECT status FROM principals WHERE id = ?`, uid); err != nil {
			t.Fatal(err)
		}
		if status != 3 {
			t.Fatalf("status = %d after the admin locked the user during activation, want 3 (locked)", status)
		}
	})
}

// TestRaceAutologinSessionAfterPasswordChange は、盗んだ autologin クッキーで処理中だったリクエストが、
// その間に行われたパスワード変更（全セッション・autologin トークンの破棄）の後で
// 新しいログインセッションを作れないことを確認する。
// 修正前はログインセッションの行をレスポンス送出時に初めて保存していたため、時間のかかる
// リクエストを autologin で送り続けるだけで、被害者がパスワードを変えても有効なセッションが残った。
func TestRaceAutologinSessionAfterPasswordChange(t *testing.T) {
	raceForEachDB(t, func(t *testing.T, d *db.DB) {
		entered := make(chan struct{}, 1)
		release := make(chan struct{})
		srv, ts := newFixtureServerOn(t, d, func(a *handler.App, r chi.Router) {
			// 時間のかかる画面の代わり（認証・セッション開始の後でブロックする）
			a.Handle(r, http.MethodGet, "/race/slow", handler.WelcomeController, "index", func(c *handler.Req) {
				entered <- struct{}{}
				<-release
				c.W.WriteHeader(http.StatusOK)
			})
		})
		ctx := context.Background()
		if err := srv.App().Settings.Set(ctx, "autologin", "7"); err != nil {
			t.Fatal(err)
		}
		// 被害者: autologin 付きでログイン
		victim := newClient(t)
		_, body := get(t, victim, ts.URL+"/login")
		res, _ := post(t, victim, ts.URL+"/login", url.Values{
			"authenticity_token": {csrfToken(t, body)}, "username": {"jsmith"}, "password": {"jsmith"}, "autologin": {"1"},
		})
		if res.StatusCode != 302 {
			t.Fatalf("login: %d", res.StatusCode)
		}
		u, _ := url.Parse(ts.URL)
		var autologin *http.Cookie
		for _, ck := range victim.Jar.Cookies(u) {
			if ck.Name == "autologin" {
				autologin = ck
			}
		}
		if autologin == nil {
			t.Fatal("no autologin cookie")
		}
		// 攻撃者: autologin クッキーだけを持つ
		jar, _ := cookiejar.New(nil)
		jar.SetCookies(u, []*http.Cookie{{Name: "autologin", Value: autologin.Value}})
		attacker := &http.Client{Jar: jar, Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		done := make(chan error, 1)
		go func() {
			res, err := attacker.Get(ts.URL + "/race/slow")
			if err == nil {
				res.Body.Close()
			}
			done <- err
		}()
		select {
		case <-entered:
		case <-time.After(20 * time.Second):
			t.Fatal("slow request did not start")
		}
		// 攻撃者のリクエストの処理中に、被害者がパスワードを変更する
		_, body = get(t, victim, ts.URL+"/my/password")
		res, _ = post(t, victim, ts.URL+"/my/password", url.Values{
			"authenticity_token": {csrfToken(t, body)}, "password": {"jsmith"},
			"new_password": {"newpassword1"}, "new_password_confirmation": {"newpassword1"},
		})
		if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/my/account") {
			t.Fatalf("password change: %d %s", res.StatusCode, res.Header.Get("Location"))
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		// 攻撃者のクッキー（autologin トークンは削除済み）でまだログインできてはならない
		res, _ = get(t, attacker, ts.URL+"/my/account")
		if res.StatusCode == 200 {
			t.Fatal("attacker kept a logged-in session created by an in-flight request after the password change")
		}
	})
}
