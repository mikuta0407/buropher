// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

// test/integration/sessions_test.rb の test_api_request_should_not_change_session（#44249）の移植。
// API リクエスト（.json / .xml）はブラウザのセッションを使わず、変更もしない。

import (
	"context"
	"testing"
)

func TestAPIRequestShouldNotChangeSession(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	if _, err := d.Exec(ctx, `UPDATE user_accounts SET must_change_password = ? WHERE principal_id = 2`, true); err != nil {
		t.Fatal(err)
	}
	c := login(t, ts, "jsmith", "jsmith")
	res, _ := get(t, c, ts.URL+"/my/page")
	if res.StatusCode != 302 || res.Header.Get("Location") != ts.URL+"/my/password" {
		t.Fatalf("before: status %d location %q", res.StatusCode, res.Header.Get("Location"))
	}
	// API リクエストはセッションのユーザーを使わない（匿名として扱う）が、セッションの pwd を消してはならない
	res, _ = get(t, c, ts.URL+"/issues.json")
	if res.StatusCode != 200 {
		t.Fatalf("issues.json: status %d", res.StatusCode)
	}
	res, _ = get(t, c, ts.URL+"/issues")
	if res.StatusCode != 302 || res.Header.Get("Location") != ts.URL+"/my/password" {
		t.Errorf("after API request: status %d location %q (password change policy circumvented)", res.StatusCode, res.Header.Get("Location"))
	}
}

func TestAPIRequestShouldNotClearTwofaActivation(t *testing.T) {
	srv, ts, _ := newFixtureServerFull(t)
	// 2 要素認証を全ユーザーに必須にする
	if err := srv.App().Settings.Set(context.Background(), "twofa", "2"); err != nil {
		t.Fatal(err)
	}
	c := login(t, ts, "jsmith", "jsmith")
	res, _ := get(t, c, ts.URL+"/issues")
	if res.StatusCode != 302 {
		t.Fatalf("before: status %d", res.StatusCode)
	}
	want := res.Header.Get("Location")
	res, _ = get(t, c, ts.URL+"/issues.json")
	if res.StatusCode != 200 {
		t.Fatalf("issues.json: status %d", res.StatusCode)
	}
	res, _ = get(t, c, ts.URL+"/issues")
	if res.StatusCode != 302 || res.Header.Get("Location") != want {
		t.Errorf("after API request: status %d location %q, want redirect to %q (2FA activation circumvented)", res.StatusCode, res.Header.Get("Location"), want)
	}
}
