// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/auth/totp"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/repository"
)

// このファイルは使い捨ての資格情報（OAuth の認可コード・2 要素認証のコード）と、
// 楽観ロック・一意制約に頼る更新を並列の HTTP リクエストで同時に使い、二重に通らないことを確認する
// （SQLite と、BUROPHER_TEST_PG_DSN があれば PostgreSQL）。

const raceN = 10

// TestRaceOAuthCodeExchange は、同じ認可コードを並列にトークンへ交換しても 1 回しか成功しないことを確認する。
func TestRaceOAuthCodeExchange(t *testing.T) {
	raceForEachDB(t, func(t *testing.T, d *db.DB) {
		_, ts := newFixtureServerOn(t, d)
		admin := login(t, ts, "admin", "admin")
		_, uid, secret := createOAuthApp(t, ts.URL, admin, "race", "view_issues")
		code := authorizeCode(t, ts.URL, login(t, ts, "jsmith", "jsmith"), uid, "view_issues")
		var ok atomic.Int32
		raceParallel(raceN, func(int) {
			form := url.Values{"grant_type": {"authorization_code"}, "code": {code},
				"redirect_uri": {"http://127.0.0.1:12345/cb"}, "client_id": {uid}, "client_secret": {secret},
				"code_verifier": {"dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"}}
			res, err := http.PostForm(ts.URL+"/oauth/token", form)
			if err != nil {
				t.Error(err)
				return
			}
			res.Body.Close()
			if res.StatusCode == 200 {
				ok.Add(1)
			}
		})
		if n := ok.Load(); n != 1 {
			t.Errorf("authorization code exchanged %d times, want 1", n)
		}
		var n int
		if err := d.Get(context.Background(), &n, `SELECT COUNT(*) FROM oauth_access_tokens`); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("%d access tokens issued for one authorization code", n)
		}
	})
}

// raceTwofaSessions は jsmith の 2 要素認証（TOTP）を有効にし、パスワードの段階を終えた n 個の
// クライアントと、それぞれの CSRF トークンを返す。
func raceTwofaSessions(t *testing.T, d *db.DB, base string, n int) ([]*http.Client, []string) {
	t.Helper()
	ctx := context.Background()
	if err := repository.SetTwofaTotpKey(ctx, d, 2, "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"); err != nil {
		t.Fatal(err)
	}
	if err := repository.ActivateTwofa(ctx, d, 2, "totp", time.Now()); err != nil {
		t.Fatal(err)
	}
	e := &authEnv{t: t, base: base, clients: map[string]*http.Client{}, out: map[string]string{}}
	var clients []*http.Client
	var tokens []string
	for i := range n {
		name := fmt.Sprintf("s%d", i)
		e.clients[name] = newClient(t)
		_, loc, _ := e.do(name, "POST", "/login", url.Values{"username": {"jsmith"}, "password": {"jsmith"}}, "")
		if loc != "/account/twofa/confirm" {
			t.Fatalf("password step: location %q", loc)
		}
		clients = append(clients, e.clients[name])
		tokens = append(tokens, e.csrf(e.clients[name]))
	}
	return clients, tokens
}

// raceTwofaSubmit は各セッションから同じコードを並列に送り、ログインに成功した数を返す。
func raceTwofaSubmit(t *testing.T, base string, clients []*http.Client, tokens []string, code string) int {
	t.Helper()
	var ok atomic.Int32
	raceParallel(len(clients), func(i int) {
		form := url.Values{"twofa_code": {code}, "authenticity_token": {tokens[i]}}
		req, _ := http.NewRequest("POST", base+"/account/twofa", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res, err := clients[i].Do(req)
		if err != nil {
			t.Error(err)
			return
		}
		res.Body.Close()
		loc := res.Header.Get("Location")
		if res.StatusCode == 302 && !strings.Contains(loc, "/account/twofa") && !strings.HasSuffix(loc, "/login") && loc != base+"/" {
			ok.Add(1)
		}
	})
	return int(ok.Load())
}

// TestRaceTwofaBackupCodeParallel は、同じバックアップコードを別々のセッションから同時に送っても
// ログインできるのは 1 回だけであることを確認する。
func TestRaceTwofaBackupCodeParallel(t *testing.T) {
	raceForEachDB(t, func(t *testing.T, d *db.DB) {
		_, ts := newFixtureServerOn(t, d)
		clients, tokens := raceTwofaSessions(t, d, ts.URL, raceN)
		sum := sha256.Sum256([]byte("abcd1234efgh"))
		if err := repository.ReplaceTwofaBackupCodes(context.Background(), d, 2, []string{hex.EncodeToString(sum[:])}, time.Now()); err != nil {
			t.Fatal(err)
		}
		if n := raceTwofaSubmit(t, ts.URL, clients, tokens, "abcd 1234 efgh"); n != 1 {
			t.Errorf("one backup code logged in %d sessions, want 1", n)
		}
	})
}

// TestRaceTwofaTotpParallel は、同じ TOTP コードを別々のセッションから同時に送っても
// ログインできるのは 1 回だけであることを確認する（サーバの現在時刻は frozenTime）。
func TestRaceTwofaTotpParallel(t *testing.T) {
	raceForEachDB(t, func(t *testing.T, d *db.DB) {
		_, ts := newFixtureServerOn(t, d)
		clients, tokens := raceTwofaSessions(t, d, ts.URL, raceN)
		code := totp.Now("JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP", frozenTime)
		if n := raceTwofaSubmit(t, ts.URL, clients, tokens, code); n != 1 {
			t.Errorf("one TOTP code logged in %d sessions, want 1", n)
		}
	})
}

// raceCount は SQL の COUNT(*) を返す。
func raceCount(t *testing.T, d *db.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := d.Get(context.Background(), &n, q, args...); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestRaceIssueLockVersion は、同じ lock_version で並列にチケットを更新すると 1 件だけが成功し、
// 残りは衝突（422）になって黙って上書きされないことを確認する。
func TestRaceIssueLockVersion(t *testing.T) {
	raceForEachDB(t, func(t *testing.T, d *db.DB) {
		_, ts := newFixtureServerOn(t, d)
		var lv int
		if err := d.Get(context.Background(), &lv, `SELECT lock_version FROM issues WHERE id = 1`); err != nil {
			t.Fatal(err)
		}
		before := raceCount(t, d, `SELECT COUNT(*) FROM issue_journals WHERE issue_id = 1`)
		var mu sync.Mutex
		codes := map[int]int{}
		raceParallel(raceN, func(i int) {
			code, _ := raceAPI(t, ts.URL, "PUT", "/issues/1.json", "jsmith", "jsmith",
				fmt.Sprintf(`{"issue":{"subject":"race %d","lock_version":%d}}`, i, lv))
			mu.Lock()
			codes[code]++
			mu.Unlock()
		})
		t.Logf("status codes: %v", codes)
		ok := codes[200] + codes[204]
		if ok != 1 || ok+codes[422]+codes[409] != raceN {
			t.Errorf("status codes %v: want exactly one success and conflicts for the rest", codes)
		}
		after := raceCount(t, d, `SELECT COUNT(*) FROM issue_journals WHERE issue_id = 1`)
		if after-before != ok {
			t.Errorf("journals added %d, successes %d", after-before, ok)
		}
	})
}

// TestRaceWikiVersion は、同じ版で並列に Wiki ページを更新すると 1 件だけが成功し、残りは 409 になることを確認する。
func TestRaceWikiVersion(t *testing.T) {
	raceForEachDB(t, func(t *testing.T, d *db.DB) {
		_, ts := newFixtureServerOn(t, d)
		var page struct {
			ID      int64 `db:"id"`
			Version int   `db:"current_version"`
		}
		if err := d.Get(context.Background(), &page, `SELECT id, current_version FROM wiki_pages WHERE wiki_id = 1 AND title = 'CookBook_documentation'`); err != nil {
			t.Fatal(err)
		}
		before := raceCount(t, d, `SELECT COUNT(*) FROM wiki_page_versions WHERE page_id = ?`, page.ID)
		var mu sync.Mutex
		codes := map[int]int{}
		raceParallel(raceN, func(i int) {
			code, _ := raceAPI(t, ts.URL, "PUT", "/projects/ecookbook/wiki/CookBook_documentation.json", "jsmith", "jsmith",
				fmt.Sprintf(`{"wiki_page":{"text":"race %d","version":%d}}`, i, page.Version))
			mu.Lock()
			codes[code]++
			mu.Unlock()
		})
		t.Logf("status codes: %v", codes)
		ok := codes[200] + codes[204]
		if ok != 1 || ok+codes[409] != raceN {
			t.Errorf("status codes %v: want exactly one success and 409 for the rest", codes)
		}
		after := raceCount(t, d, `SELECT COUNT(*) FROM wiki_page_versions WHERE page_id = ?`, page.ID)
		if after-before != ok {
			t.Errorf("versions added %d, successes %d", after-before, ok)
		}
	})
}

// TestRaceRegisterSameLogin は、同じログイン名の自己登録を並列に送っても利用者が 1 人しか作られないことを確認する。
func TestRaceRegisterSameLogin(t *testing.T) {
	raceForEachDB(t, func(t *testing.T, d *db.DB) {
		srv, ts := newFixtureServerOn(t, d)
		if err := srv.App().Settings.Set(context.Background(), "self_registration", "3"); err != nil {
			t.Fatal(err)
		}
		clients := make([]*http.Client, raceN)
		tokens := make([]string, raceN)
		for i := range clients {
			clients[i] = newClient(t)
			_, page := get(t, clients[i], ts.URL+"/account/register")
			tokens[i] = csrfToken(t, page)
		}
		var mu sync.Mutex
		codes := map[int]int{}
		raceParallel(raceN, func(i int) {
			res, err := clients[i].PostForm(ts.URL+"/account/register", url.Values{"authenticity_token": {tokens[i]},
				"user[login]": {"racer"}, "user[password]": {"racepass1"}, "user[password_confirmation]": {"racepass1"},
				"user[firstname]": {"R"}, "user[lastname]": {"Acer"}, "user[mail]": {fmt.Sprintf("racer%d@example.net", i)}})
			if err != nil {
				t.Error(err)
				return
			}
			res.Body.Close()
			mu.Lock()
			codes[res.StatusCode]++
			mu.Unlock()
		})
		t.Logf("status codes: %v", codes)
		if n := raceCount(t, d, `SELECT COUNT(*) FROM user_accounts WHERE lower(login) = 'racer'`); n != 1 {
			t.Errorf("%d users registered with the same login", n)
		}
	})
}
