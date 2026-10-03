// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

// test/integration/api_test/{api_test,disabled_rest_api_test,jsonp_test}.rb の移植。

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// TestAPIGeneral は api_test.rb。
func TestAPIGeneral(t *testing.T) {
	ts, _ := newFixtureServer(t)

	t.Run("api_should_work_with_protect_from_forgery", func(t *testing.T) {
		// API 形式の POST は CSRF 検証を行わない（authenticity_token なしで作成できる）
		res := apiCall(t, ts, http.MethodPost, "/users.xml", "application/x-www-form-urlencoded",
			"user[login]=foo&user[firstname]=Firstname&user[lastname]=Lastname&user[mail]=foo@example.net&user[password]=secret123",
			apiCreds("admin"))
		res.expectStatus(t, http.StatusCreated)
	})
	t.Run("json_datetime_format", func(t *testing.T) {
		res := apiGet(t, ts, "/users/1.json", apiCreds("admin"))
		if !strings.Contains(res.Body, `"created_on":"2006-07-19T17:12:21Z"`) {
			t.Errorf("created_on: %s", res.Body)
		}
	})
	t.Run("xml_datetime_format", func(t *testing.T) {
		res := apiGet(t, ts, "/users/1.xml", apiCreds("admin"))
		if !strings.Contains(res.Body, "<created_on>2006-07-19T17:12:21Z</created_on>") {
			t.Errorf("created_on: %s", res.Body)
		}
	})
	t.Run("head_response_should_have_empty_body", func(t *testing.T) {
		res := apiCall(t, ts, http.MethodPut, "/users/7.xml", "application/x-www-form-urlencoded", "user[login]=foo7", apiCreds("admin"))
		res.expectStatus(t, http.StatusNoContent)
		if res.Body != "" {
			t.Errorf("body = %q", res.Body)
		}
	})
	t.Run("api_with_invalid_format_should_return_406", func(t *testing.T) {
		res := apiGet(t, ts, "/users/1", apiCreds("admin"), apiHeader("Accept", "application/xml"), apiHeader("Content-Type", "application/xml"))
		res.expectStatus(t, http.StatusNotAcceptable)
		want := "We couldn't handle your request, sorry. If you were trying to access the API, make sure to append .json or .xml to your request URL.\n"
		if res.Body != want {
			t.Errorf("body = %q", res.Body)
		}
		if got := res.Header.Get("Content-Type"); got != "application/xml; charset=utf-8" {
			t.Errorf("Content-Type = %q", got)
		}
	})
}

// TestAPIDisabledRestAPI は disabled_rest_api_test.rb（REST API 無効 + ログイン必須では API 認証を受け付けず 403）。
func TestAPIDisabledRestAPI(t *testing.T) {
	srv, ts, d := newFixtureServerFull(t)
	// 有効なうちにユーザーとキーを作っておく
	id, login := authGenerateUser(t, ts, "my_password")
	key := authCreateToken(t, d, id, "api")
	ctx := context.Background()
	st := srv.App().Settings
	if err := st.Set(ctx, "rest_api_enabled", "0"); err != nil {
		t.Fatal(err)
	}
	if err := st.Set(ctx, "login_required", "1"); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"xml", "json"} {
		t.Run("valid_api_token_"+f, func(t *testing.T) {
			apiGet(t, ts, "/news."+f+"?key="+key).expectStatus(t, http.StatusForbidden)
		})
		t.Run("valid_username_password_"+f, func(t *testing.T) {
			apiGet(t, ts, "/news."+f, apiBasic(login, "my_password")).expectStatus(t, http.StatusForbidden)
		})
		t.Run("valid_token_http_authentication_"+f, func(t *testing.T) {
			apiGet(t, ts, "/news."+f, apiBasic(key, "X")).expectStatus(t, http.StatusForbidden)
		})
	}
}

// TestAPIJSONP は jsonp_test.rb。
func TestAPIJSONP(t *testing.T) {
	srv, ts, _ := newFixtureServerFull(t)
	setJSONP := func(t *testing.T, v string) {
		t.Helper()
		if err := srv.App().Settings.Set(context.Background(), "jsonp_enabled", v); err != nil {
			t.Fatal(err)
		}
	}
	plain := regexp.MustCompile(`^\{"trackers":.+\}$`)
	wrapped := func(cb string) *regexp.Regexp {
		return regexp.MustCompile(`^` + regexp.QuoteMeta(cb) + `\(\{"trackers":.+\}\)$`)
	}
	check := func(t *testing.T, res apiResp, re *regexp.Regexp, ctype string) {
		t.Helper()
		res.expectStatus(t, http.StatusOK)
		if !re.MatchString(res.Body) {
			t.Errorf("body %.200q does not match %s", res.Body, re)
		}
		if got := res.Header.Get("Content-Type"); got != ctype {
			t.Errorf("Content-Type = %q, want %q", got, ctype)
		}
	}
	t.Run("ignore_callback_with_jsonp_disabled", func(t *testing.T) {
		setJSONP(t, "0")
		check(t, apiGet(t, ts, "/trackers.json?jsonp=handler"), plain, "application/json; charset=utf-8")
	})
	t.Run("accept_callback_param", func(t *testing.T) {
		setJSONP(t, "1")
		check(t, apiGet(t, ts, "/trackers.json?callback=handler"), wrapped("handler"), "application/javascript; charset=utf-8")
	})
	t.Run("accept_jsonp_param", func(t *testing.T) {
		setJSONP(t, "1")
		check(t, apiGet(t, ts, "/trackers.json?jsonp=handler"), wrapped("handler"), "application/javascript; charset=utf-8")
	})
	t.Run("strip_invalid_characters_from_callback", func(t *testing.T) {
		setJSONP(t, "1")
		// Ruby のテストはクエリに "+-aA$1_." をそのまま渡す（+ は空白にデコードされる）
		check(t, apiGet(t, ts, "/trackers.json?callback=+-aA$1_."), wrapped("aA1_."), "application/javascript; charset=utf-8")
	})
	t.Run("without_callback_should_return_json", func(t *testing.T) {
		setJSONP(t, "1")
		check(t, apiGet(t, ts, "/trackers.json?callback="), plain, "application/json; charset=utf-8")
	})
}
