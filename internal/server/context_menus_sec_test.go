// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"strings"
	"testing"
)

// 管理画面のコンテキストメニュー（プロジェクト・ユーザー）は管理者専用（Redmine 6.1.3 #44109）。
func TestAdminContextMenusRequireAdmin(t *testing.T) {
	ts, _ := newFixtureServer(t)
	// プロジェクト 2（onlinestore）は非公開で dlopper はメンバーでない
	paths := []string{"/admin/projects_context_menu?ids[]=2", "/users/context_menu?ids[]=2"}
	for _, who := range []string{"anonymous", "dlopper"} {
		client := newClient(t)
		if who == "dlopper" {
			client = login(t, ts, "dlopper", "foo")
		}
		for _, p := range paths {
			res, body := get(t, client, ts.URL+p)
			if res.StatusCode == 200 || strings.Contains(body, "onlinestore") {
				t.Errorf("%s %s: status %d body %.300s", who, p, res.StatusCode, body)
			}
		}
	}
	admin := login(t, ts, "admin", "admin")
	for _, p := range paths {
		if res, _ := get(t, admin, ts.URL+p); res.StatusCode != 200 {
			t.Errorf("admin %s: status %d", p, res.StatusCode)
		}
	}
}
