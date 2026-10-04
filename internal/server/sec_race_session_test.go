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

// TestRaceDemotionVsAccountSave は、ユーザー自身の「個人設定」の保存の処理中に管理者がそのユーザーの
// 管理者権限を外し・ロックしても、保存が読み込み時の admin / status で上書きして元に戻さないことを確認する。
// 修正前の UpdateUser は変更していない列も含めて全列を書き戻していたため（Redmine は変更した属性だけを
// 更新する）、降格される管理者が /my/account の保存を送り続けるだけで降格・ロックを取り消せた。
func TestRaceDemotionVsAccountSave(t *testing.T) {
	raceForEachDB(t, func(t *testing.T, d *db.DB) {
		_, ts := newFixtureServerOn(t, d)
		ctx := context.Background()
		const uid = 2 // jsmith
		if _, err := d.Exec(ctx, `UPDATE user_accounts SET admin = TRUE WHERE principal_id = ?`, uid); err != nil {
			t.Fatal(err)
		}
		c := login(t, ts, "jsmith", "jsmith")
		_, body := get(t, c, ts.URL+"/my/account")
		token := csrfToken(t, body)
		// 管理者による降格・ロック: 行をロックしたトランザクションを開いたままにする
		tx, err := d.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		for _, q := range []string{`SELECT id FROM principals WHERE id = ?`, `SELECT principal_id FROM user_accounts WHERE principal_id = ?`} {
			if _, err := tx.Exec(ctx, q+db.ForUpdate(tx), uid); err != nil {
				t.Fatal(err)
			}
		}
		done := make(chan int, 1)
		go func() {
			res, err := c.PostForm(ts.URL+"/my/account", url.Values{"_method": {"put"}, "authenticity_token": {token}, "user[firstname]": {"Renamed"}})
			if err != nil {
				t.Error(err)
				done <- 0
				return
			}
			res.Body.Close()
			done <- res.StatusCode
		}()
		time.Sleep(700 * time.Millisecond) // 保存がユーザーを読み、書き込みで待つまで
		for _, q := range []string{`UPDATE user_accounts SET admin = FALSE WHERE principal_id = ?`, `UPDATE principals SET status = 3 WHERE id = ?`} {
			if _, err := tx.Exec(ctx, q, uid); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if code := <-done; code != 302 {
			t.Fatalf("account save: %d", code)
		}
		var r struct {
			Admin     bool   `db:"admin"`
			Status    int    `db:"status"`
			Firstname string `db:"firstname"`
		}
		if err := d.Get(ctx, &r, `SELECT a.admin, p.status, p.firstname FROM principals p JOIN user_accounts a ON a.principal_id = p.id WHERE p.id = ?`, uid); err != nil {
			t.Fatal(err)
		}
		if r.Admin || r.Status != 3 {
			t.Fatalf("after demotion+lock during the user's own save: admin=%v status=%d (want false, 3)", r.Admin, r.Status)
		}
		if r.Firstname != "Renamed" {
			t.Errorf("firstname = %q, want Renamed", r.Firstname)
		}
	})
}

// TestRaceArchiveVsProjectSave は、プロジェクトの設定の保存の処理中に管理者がプロジェクトをアーカイブし
// 非公開にしても、保存が読み込み時の status / is_public で上書きして元に戻さないことを確認する
// （Redmine は変更した属性だけを更新する）。
func TestRaceArchiveVsProjectSave(t *testing.T) {
	raceForEachDB(t, func(t *testing.T, d *db.DB) {
		_, ts := newFixtureServerOn(t, d)
		ctx := context.Background()
		c := login(t, ts, "jsmith", "jsmith") // eCookbook の管理者
		_, body := get(t, c, ts.URL+"/projects/ecookbook/settings")
		token := csrfToken(t, body)
		tx, err := d.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.Exec(ctx, `SELECT id FROM projects WHERE id = 1`+db.ForUpdate(tx)); err != nil {
			t.Fatal(err)
		}
		done := make(chan int, 1)
		go func() {
			res, err := c.PostForm(ts.URL+"/projects/ecookbook", url.Values{"_method": {"patch"}, "authenticity_token": {token},
				"project[description]": {"edited"}, "project[is_public]": {"1"}})
			if err != nil {
				t.Error(err)
				done <- 0
				return
			}
			res.Body.Close()
			done <- res.StatusCode
		}()
		time.Sleep(700 * time.Millisecond)
		if _, err := tx.Exec(ctx, `UPDATE projects SET status = 9, is_public = FALSE WHERE id = 1`); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if code := <-done; code != 302 {
			t.Fatalf("project save: %d", code)
		}
		var r struct {
			Status      int    `db:"status"`
			IsPublic    bool   `db:"is_public"`
			Description string `db:"description"`
		}
		if err := d.Get(ctx, &r, `SELECT status, is_public, description FROM projects WHERE id = 1`); err != nil {
			t.Fatal(err)
		}
		if r.Status != 9 || r.IsPublic {
			t.Fatalf("after archiving during a settings save: status=%d is_public=%v (want 9, false)", r.Status, r.IsPublic)
		}
		if r.Description != "edited" {
			t.Errorf("description = %q", r.Description)
		}
	})
}

// TestRaceProtectVsWikiEdit は、Wiki ページの編集の保存の処理中に管理者がページを保護しても、
// 保存が読み込み時の protected（保護なし）で上書きして保護を外さないことを確認する。
// 修正前の編集は wiki_pages の protected も含めて書き戻していたため、保護の権限の無い編集者の
// 処理中の保存で保護が外れ、以後も保護されたページを編集できた。
func TestRaceProtectVsWikiEdit(t *testing.T) {
	raceForEachDB(t, func(t *testing.T, d *db.DB) {
		_, ts := newFixtureServerOn(t, d)
		ctx := context.Background()
		c := login(t, ts, "jsmith", "jsmith")
		form := wikiForm(t, c, ts, "_method", "put", "content[text]", "# Another page\n\nedited", "content[version]", "1")
		var pageID int64
		if err := d.Get(ctx, &pageID, `SELECT id FROM wiki_pages WHERE wiki_id = 1 AND title = 'Another_page'`); err != nil {
			t.Fatal(err)
		}
		tx, err := d.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.Exec(ctx, `SELECT id FROM wiki_pages WHERE id = ?`+db.ForUpdate(tx), pageID); err != nil {
			t.Fatal(err)
		}
		done := make(chan int, 1)
		go func() {
			res, err := c.PostForm(ts.URL+"/projects/ecookbook/wiki/Another_page", form)
			if err != nil {
				t.Error(err)
				done <- 0
				return
			}
			res.Body.Close()
			done <- res.StatusCode
		}()
		time.Sleep(700 * time.Millisecond)
		if _, err := tx.Exec(ctx, `UPDATE wiki_pages SET protected = TRUE WHERE id = ?`, pageID); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if code := <-done; code != 302 {
			t.Fatalf("wiki save: %d", code)
		}
		var protected bool
		if err := d.Get(ctx, &protected, `SELECT protected FROM wiki_pages WHERE id = ?`, pageID); err != nil {
			t.Fatal(err)
		}
		if !protected {
			t.Fatal("protection set during an edit was removed by the edit's save")
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
