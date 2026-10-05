// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestUserShowAPIHidesMailWhenPreferenceUnreadable は、表示するユーザーの設定（hide_mail）を読めないとき、
// users/show の API がメールアドレスを出さないことを確認する（読めない設定を「隠さない」として扱わない）。
func TestUserShowAPIHidesMailWhenPreferenceUnreadable(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	key := authCreateToken(t, d, 3, "api")
	if _, err := d.Exec(ctx, `UPDATE user_preferences SET hide_mail = ? WHERE user_id = 2`, true); err != nil {
		t.Fatal(err)
	}
	// 設定の読み込みを失敗させる
	if _, err := d.Exec(ctx, `ALTER TABLE user_preferences RENAME TO user_preferences_broken`); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/users/2.json?key="+key, nil)
	res, err := newClient(t).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := readUnbranded(res.Body)
	res.Body.Close()
	if strings.Contains(string(b), "jsmith@somenet.foo") {
		t.Errorf("hidden mail exposed when the preference cannot be read: %d %s", res.StatusCode, b)
	}
}

// TestGroupShowFailsClosedWhenVisibilityUnknown は、管理者以外に対するグループの可視性の条件を作れないとき、
// groups/show がグループを表示しない（確認を飛ばさない）ことを確認する。
func TestGroupShowFailsClosedWhenVisibilityUnknown(t *testing.T) {
	ts, d := newFixtureServer(t)
	key := authCreateToken(t, d, 3, "api")
	// 可視性の条件（閲覧者のメンバーシップ）の読み込みを失敗させる
	if _, err := d.Exec(context.Background(), `ALTER TABLE member_roles RENAME TO member_roles_broken`); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/groups/10.json?key="+key, nil)
	res, err := newClient(t).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := readUnbranded(res.Body)
	res.Body.Close()
	if res.StatusCode == http.StatusOK {
		t.Errorf("group shown without a visibility check: %s", b)
	}
}
