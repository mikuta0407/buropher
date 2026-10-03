// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

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
