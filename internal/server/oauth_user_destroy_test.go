// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"testing"

	"github.com/mikuta0407/buropher/internal/repository"
)

// TestDestroyUserWithOAuthAuthorization は test/unit/user_test.rb の
// test_destroy_should_delete_oauth_access_grants / tokens（#44343）の移植。
// OAuth アプリを承認したユーザーも削除でき、承認（グラント）とアクセストークンも消える。
func TestDestroyUserWithOAuthAuthorization(t *testing.T) {
	ts, d := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	jsmith := login(t, ts, "jsmith", "jsmith")
	if tok := oauthTokenFor(t, ts.URL, admin, jsmith, "view_issues"); tok == "" {
		t.Fatal("no access token")
	}
	ctx := context.Background()
	count := func(q string) int {
		t.Helper()
		var n int
		if err := d.Get(ctx, &n, q); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count(`SELECT COUNT(*) FROM oauth_access_grants WHERE resource_owner_id = 2`) == 0 ||
		count(`SELECT COUNT(*) FROM oauth_access_tokens WHERE resource_owner_id = 2`) == 0 {
		t.Fatal("grant / token not created")
	}
	if err := repository.DestroyUser(ctx, d, 2, 6); err != nil {
		t.Fatalf("destroy user: %v", err)
	}
	if n := count(`SELECT COUNT(*) FROM principals WHERE id = 2`); n != 0 {
		t.Errorf("user not deleted")
	}
	if n := count(`SELECT COUNT(*) FROM oauth_access_grants WHERE resource_owner_id = 2`); n != 0 {
		t.Errorf("grants left: %d", n)
	}
	if n := count(`SELECT COUNT(*) FROM oauth_access_tokens WHERE resource_owner_id = 2`); n != 0 {
		t.Errorf("tokens left: %d", n)
	}
}
