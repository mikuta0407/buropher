// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
)

// TestLoginSessionsLimitedPerUser は、ユーザーごとのログインセッションが Redmine の session トークンと
// 同じく新しい 10 件までに制限されることを確認する。上限が無いと、一度漏れたセッション Cookie が
// （既定では無期限のため）本人が何度ログインし直しても使えたままになり、sessions の行も際限なく増える。
func TestLoginSessionsLimitedPerUser(t *testing.T) {
	ts, d := newFixtureServer(t)
	var clients []*http.Client
	// 同じ秒のログインは順序が決まらないため、ログインごとに最終アクセス時刻をずらす
	base := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 11 {
		clients = append(clients, login(t, ts, "jsmith", "jsmith"))
		if _, err := d.Exec(context.Background(), `UPDATE sessions SET last_seen_at = ? WHERE user_id = 2 AND last_seen_at > ?`,
			db.NewTime(base.Add(time.Duration(i)*time.Hour)), db.NewTime(base.Add(24*time.Hour))); err != nil {
			t.Fatal(err)
		}
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM sessions WHERE user_id = 2`); n != 10 {
		t.Errorf("sessions of jsmith = %d, want 10", n)
	}
	status := func(c *http.Client) int {
		res, err := c.Get(ts.URL + "/my/account")
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if st := status(clients[0]); st != http.StatusFound {
		t.Errorf("oldest session: /my/account status %d, want redirect to login", st)
	}
	for i, c := range clients[1:] {
		if st := status(c); st != http.StatusOK {
			t.Errorf("session %d: /my/account status %d", i+1, st)
		}
	}
}
