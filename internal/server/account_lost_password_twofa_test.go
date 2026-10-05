// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

// TestLostPasswordAllowedWhenTwofaSetupRequired は test/integration/twofa_test.rb の
// 'should allow lost password even if twofa setup is required'（#44360）の移植。
func TestLostPasswordAllowedWhenTwofaSetupRequired(t *testing.T) {
	myFreezeClock(t)
	srv, ts, d := newFixtureServerFull(t)
	ctx := context.Background()
	if err := srv.App().Settings.Set(ctx, "twofa", "2"); err != nil {
		t.Fatal(err)
	}
	c := login(t, ts, "jsmith", "jsmith")
	res, _ := get(t, c, ts.URL+"/my/page")
	if loc := res.Header.Get("Location"); !strings.HasSuffix(loc, "/my/twofa/totp/activate/confirm") {
		t.Fatalf("2FA activation not required: %d %q", res.StatusCode, loc)
	}
	res, page := get(t, c, ts.URL+"/account/lost_password")
	if res.StatusCode != 200 || !strings.Contains(page, `name="mail"`) {
		t.Fatalf("lost_password: status %d location %q", res.StatusCode, res.Header.Get("Location"))
	}
	res, _ = post(t, c, ts.URL+"/account/lost_password", url.Values{"authenticity_token": {csrfToken(t, page)}, "mail": {"jSmith@somenet.foo"}})
	if loc := res.Header.Get("Location"); !strings.HasSuffix(loc, "/login") {
		t.Fatalf("post lost_password: %d %q", res.StatusCode, loc)
	}
	var tok string
	if err := d.Get(ctx, &tok, `SELECT value FROM tokens WHERE user_id = 2 AND action = 'recovery'`); err != nil {
		t.Fatal(err)
	}
	res, _ = get(t, c, ts.URL+"/account/lost_password?token="+tok)
	if loc := res.Header.Get("Location"); !strings.HasSuffix(loc, "/account/lost_password") {
		t.Fatalf("token redirect: %d %q", res.StatusCode, loc)
	}
	res, page = get(t, c, ts.URL+"/account/lost_password")
	if res.StatusCode != 200 {
		t.Fatalf("password_recovery: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	res, _ = post(t, c, ts.URL+"/account/lost_password", url.Values{"authenticity_token": {csrfToken(t, page)},
		"token": {tok}, "new_password": {"newpass123"}, "new_password_confirmation": {"newpass123"}})
	if loc := res.Header.Get("Location"); !strings.HasSuffix(loc, "/login") {
		t.Fatalf("password update: %d %q", res.StatusCode, loc)
	}
}
