// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

// test/functional/my_controller_test.rb の test_my_account_should_show_*_used_*_key（#43938）の移植。

import (
	"context"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
)

func TestMyAccountShowsTokenLastUsage(t *testing.T) {
	myFreezeClock(t)
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	api := authCreateToken(t, d, 2, "api")
	authCreateToken(t, d, 2, "feeds")
	c := login(t, ts, "jsmith", "jsmith")

	_, body := get(t, c, ts.URL+"/my/account")
	for _, want := range []string{
		"Atom access key created less than a minute ago<br />\n    Never used",
		"API access key created less than a minute ago<br />\n      Never used",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}

	// 作成 2 日前・最終利用 1 日前
	if _, err := d.Exec(ctx, `UPDATE tokens SET created_at = ?, updated_at = ? WHERE user_id = 2 AND action = 'feeds'`,
		db.NewTime(frozenTime.AddDate(0, 0, -2)), db.NewTime(frozenTime.AddDate(0, 0, -1))); err != nil {
		t.Fatal(err)
	}
	// API キーを使うと最終利用日時が記録される（作成から 1 分以上経っている場合）
	if _, err := d.Exec(ctx, `UPDATE tokens SET created_at = ?, updated_at = ? WHERE user_id = 2 AND action = 'api'`,
		db.NewTime(frozenTime.AddDate(0, 0, -2)), db.NewTime(frozenTime.AddDate(0, 0, -2))); err != nil {
		t.Fatal(err)
	}
	apiGet(t, ts, "/users/current.json?key="+api).expectStatus(t, 200)

	_, body = get(t, c, ts.URL+"/my/account")
	for _, want := range []string{
		"Atom access key created 2 days ago<br />\n    Last used: 1 day ago",
		"API access key created 2 days ago<br />\n      Last used: less than a minute ago",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
}
