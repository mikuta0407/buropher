// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

// test/integration/api_test/authentication_test.rb の移植。

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/repository"
)

var authGeneratedSeq atomic.Int64

// authGenerateUser は User.generate!（login は user1, user2 ...、名前は Bob Doe）を API 経由で作り、id と login を返す。
func authGenerateUser(t *testing.T, ts *httptest.Server, password string) (int64, string) {
	t.Helper()
	login := fmt.Sprintf("genuser%d", authGeneratedSeq.Add(1))
	if password == "" {
		password = "generated_pw1"
	}
	body := fmt.Sprintf(`{"user":{"login":%q,"firstname":"Bob","lastname":"Doe","mail":"%s@example.com","password":%q}}`, login, login, password)
	res := apiCall(t, ts, http.MethodPost, "/users.json", "", body, apiCreds("admin"))
	res.expectStatus(t, http.StatusCreated)
	id, ok := jsonPath(res.JSON(t), "user.id")
	if !ok {
		t.Fatalf("no user.id: %s", res.Body)
	}
	var n int64
	fmt.Sscan(fmt.Sprint(id), &n)
	return n, login
}

// authCreateToken は Token.create!(user:, action:) の値を返す。
func authCreateToken(t *testing.T, d *db.DB, userID int64, action string) string {
	t.Helper()
	tok, err := repository.CreateToken(context.Background(), d, userID, action)
	if err != nil {
		t.Fatal(err)
	}
	return tok.Value
}

func TestAPIAuthentication(t *testing.T) {
	ts, d := newFixtureServer(t)

	t.Run("deny_without_credentials", func(t *testing.T) {
		res := apiGet(t, ts, "/users/current.xml")
		res.expectStatus(t, http.StatusUnauthorized)
		if got := res.Header.Get("WWW-Authenticate"); got != `Basic realm="Redmine API"` {
			t.Errorf("WWW-Authenticate = %q", got)
		}
		// 参照 Redmine は本文なし・Content-Type は形式のみ（charset なし）
		if res.Body != "" || res.Header.Get("Content-Type") != "application/xml" {
			t.Errorf("body %q, Content-Type %q", res.Body, res.Header.Get("Content-Type"))
		}
	})
	t.Run("accept_basic_username_password", func(t *testing.T) {
		_, login := authGenerateUser(t, ts, "my_password")
		apiGet(t, ts, "/users/current.xml", apiBasic(login, "my_password")).expectStatus(t, http.StatusOK)
	})
	t.Run("deny_basic_wrong_password", func(t *testing.T) {
		_, login := authGenerateUser(t, ts, "my_password")
		apiGet(t, ts, "/users/current.xml", apiBasic(login, "wrong_password")).expectStatus(t, http.StatusUnauthorized)
	})
	t.Run("deny_basic_if_twofa_active", func(t *testing.T) {
		id, login := authGenerateUser(t, ts, "my_password")
		if _, err := d.Exec(context.Background(), `UPDATE user_accounts SET twofa_scheme = 'totp' WHERE principal_id = ?`, id); err != nil {
			t.Fatal(err)
		}
		apiGet(t, ts, "/users/current.xml", apiBasic(login, "my_password")).expectStatus(t, http.StatusUnauthorized)
	})
	t.Run("accept_basic_api_key", func(t *testing.T) {
		id, _ := authGenerateUser(t, ts, "")
		key := authCreateToken(t, d, id, "api")
		apiGet(t, ts, "/users/current.xml", apiBasic(key, "X")).expectStatus(t, http.StatusOK)
	})
	t.Run("deny_basic_wrong_api_key", func(t *testing.T) {
		id, _ := authGenerateUser(t, ts, "")
		key := authCreateToken(t, d, id, "feeds")
		apiGet(t, ts, "/users/current.xml", apiBasic(key, "X")).expectStatus(t, http.StatusUnauthorized)
	})
	t.Run("accept_api_key_parameter", func(t *testing.T) {
		id, _ := authGenerateUser(t, ts, "")
		key := authCreateToken(t, d, id, "api")
		apiGet(t, ts, "/users/current.xml?key="+key).expectStatus(t, http.StatusOK)
	})
	t.Run("deny_wrong_api_key_parameter", func(t *testing.T) {
		id, _ := authGenerateUser(t, ts, "")
		key := authCreateToken(t, d, id, "feeds")
		apiGet(t, ts, "/users/current.xml?key="+key).expectStatus(t, http.StatusUnauthorized)
	})
	t.Run("accept_api_key_header", func(t *testing.T) {
		id, _ := authGenerateUser(t, ts, "")
		key := authCreateToken(t, d, id, "api")
		apiGet(t, ts, "/users/current.xml", apiKeyHeader(key)).expectStatus(t, http.StatusOK)
	})
	t.Run("deny_wrong_api_key_header", func(t *testing.T) {
		id, _ := authGenerateUser(t, ts, "")
		key := authCreateToken(t, d, id, "feeds")
		apiGet(t, ts, "/users/current.xml", apiKeyHeader(key)).expectStatus(t, http.StatusUnauthorized)
	})
	t.Run("basic_header_with_wrong_password", func(t *testing.T) {
		// credentials('jsmith') は password = 'jsmith'（fixtures の jsmith のパスワードと一致）だが、
		// Redmine のテストでは authenticate_with_http_basic をモックしているため 401 になる。
		// ここでは誤ったパスワードで 401 になることを確認する。
		apiGet(t, ts, "/users/current.xml", apiBasic("jsmith", "wrong")).expectStatus(t, http.StatusUnauthorized)
	})
	t.Run("non_basic_authorization_header", func(t *testing.T) {
		apiGet(t, ts, "/users/current.xml", apiHeader("Authorization", "Digest foo bar")).expectStatus(t, http.StatusUnauthorized)
	})
	t.Run("invalid_utf8_credentials", func(t *testing.T) {
		res := apiGet(t, ts, "/users/current.xml", apiBasic("\x82", "foo"))
		if res.Status >= 500 {
			t.Fatalf("status %d", res.Status)
		}
	})
	t.Run("api_request_should_not_use_user_session", func(t *testing.T) {
		c := login(t, ts, "jsmith", "jsmith")
		res, _ := get(t, c, ts.URL+"/users/current")
		if res.StatusCode != 200 {
			t.Fatalf("html status %d", res.StatusCode)
		}
		res, _ = get(t, c, ts.URL+"/users/current.json")
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("json status %d, want 401", res.StatusCode)
		}
	})

	adminKey := authCreateToken(t, d, 1, "api")
	jsmithKey := authCreateToken(t, d, 2, "api")
	t.Run("switch_user_header_for_admin", func(t *testing.T) {
		res := apiGet(t, ts, "/users/current", apiKeyHeader(adminKey), apiHeader("X-Redmine-Switch-User", "rhill"))
		res.expectStatus(t, http.StatusOK)
		if !strings.Contains(res.Body, "RH</span> Robert Hill</h2>") {
			t.Errorf("h2 not switched: %s", extract(res.Body, "<h2", "</h2>"))
		}
		// API 形式でも切り替わる
		res = apiGet(t, ts, "/users/current.json", apiKeyHeader(adminKey), apiHeader("X-Redmine-Switch-User", "rhill"))
		res.expectStatus(t, http.StatusOK)
		assertJSON(t, res.JSON(t), "user.login", "rhill")
	})
	t.Run("switch_to_invalid_user_412", func(t *testing.T) {
		apiGet(t, ts, "/users/current", apiKeyHeader(adminKey), apiHeader("X-Redmine-Switch-User", "foobar")).expectStatus(t, http.StatusPreconditionFailed)
		res := apiGet(t, ts, "/users/current.json", apiKeyHeader(adminKey), apiHeader("X-Redmine-Switch-User", "foobar"))
		res.expectStatus(t, http.StatusPreconditionFailed)
		if res.Body != "" || res.Header.Get("Content-Type") != "application/json" {
			t.Errorf("body %q, Content-Type %q", res.Body, res.Header.Get("Content-Type"))
		}
	})
	t.Run("switch_to_locked_user_412", func(t *testing.T) {
		// users(5) dlopper2 はロック済み
		apiGet(t, ts, "/users/current", apiKeyHeader(adminKey), apiHeader("X-Redmine-Switch-User", "dlopper2")).expectStatus(t, http.StatusPreconditionFailed)
	})
	// buropher 拡張: X-Buropher-* ヘッダも同じ意味で受け付け、両方あれば X-Buropher-* を優先する
	t.Run("buropher_api_key_header", func(t *testing.T) {
		id, _ := authGenerateUser(t, ts, "")
		key := authCreateToken(t, d, id, "api")
		apiGet(t, ts, "/users/current.xml", apiHeader("X-Buropher-API-Key", key)).expectStatus(t, http.StatusOK)
		apiGet(t, ts, "/users/current.json", apiHeader("X-Buropher-API-Key", jsmithKey), apiKeyHeader("wrong")).expectStatus(t, http.StatusOK)
		res := apiGet(t, ts, "/users/current.json", apiHeader("X-Buropher-API-Key", jsmithKey), apiKeyHeader(adminKey))
		assertJSON(t, res.JSON(t), "user.login", "jsmith")
	})
	t.Run("buropher_switch_user_header", func(t *testing.T) {
		res := apiGet(t, ts, "/users/current.json", apiKeyHeader(adminKey), apiHeader("X-Buropher-Switch-User", "rhill"))
		res.expectStatus(t, http.StatusOK)
		assertJSON(t, res.JSON(t), "user.login", "rhill")
		res = apiGet(t, ts, "/users/current.json", apiKeyHeader(adminKey), apiHeader("X-Buropher-Switch-User", "jsmith"), apiHeader("X-Redmine-Switch-User", "rhill"))
		assertJSON(t, res.JSON(t), "user.login", "jsmith")
		apiGet(t, ts, "/users/current.json", apiKeyHeader(adminKey), apiHeader("X-Buropher-Switch-User", "foobar")).expectStatus(t, http.StatusPreconditionFailed)
	})
	t.Run("switch_user_header_ignored_for_non_admin", func(t *testing.T) {
		res := apiGet(t, ts, "/users/current", apiKeyHeader(jsmithKey), apiHeader("X-Redmine-Switch-User", "rhill"))
		res.expectStatus(t, http.StatusOK)
		if !strings.Contains(res.Body, "JS</span> John Smith</h2>") {
			t.Errorf("h2: %s", extract(res.Body, "<h2", "</h2>"))
		}
	})
}

// TestAtomKeyRequiresAtomFormatParam は、Atom のキー（key パラメータ）による認証が params[:format] == 'atom'
// （.atom の拡張子か format パラメータ）のときだけ行われ、Accept ヘッダだけでは行われないことを確認する（Redmine と同じ）。
func TestAtomKeyRequiresAtomFormatParam(t *testing.T) {
	ts, d := newFixtureServer(t)
	key := authCreateToken(t, d, 2, "feeds") // jsmith（非公開プロジェクト onlinestore のメンバー）
	res := apiGet(t, ts, "/projects/onlinestore/issues.atom?key="+key)
	res.expectStatus(t, http.StatusOK)
	if !strings.Contains(res.Body, "<feed") {
		t.Fatalf("atom with key: %.200s", res.Body)
	}
	res = apiGet(t, ts, "/projects/onlinestore/issues?key="+key, apiHeader("Accept", "application/atom+xml"))
	if res.Status == http.StatusOK || strings.Contains(res.Body, "<feed") {
		t.Errorf("atom key accepted via Accept header: %d %.200s", res.Status, res.Body)
	}
}
