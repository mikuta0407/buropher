// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/handler"
)

// TestSudoModeUserImportRun は sudo モードが有効なとき、ユーザーのインポートの実行がパスワードの再確認を
// 求めることを確認する（インポートでも管理者ユーザーを作れるため、users#create と同じ扱いにする）。
func TestSudoModeUserImportRun(t *testing.T) {
	var app *handler.App
	ts, d := newFixtureServer(t, func(a *handler.App, _ chi.Router) { a.SudoMode = true; app = a })
	ctx := context.Background()
	if err := app.Settings.Set(ctx, "autologin", "7"); err != nil {
		t.Fatal(err)
	}
	ae := &authEnv{t: t, base: ts.URL, clients: map[string]*http.Client{}, out: map[string]string{}}
	ae.autologinClient("hijacked", "admin")
	c := ae.clients["hijacked"]
	usersBefore := queryInt(t, d, `SELECT COUNT(*) FROM principals`)
	id := importUpload(t, c, ts, "UserImport", "import_users.csv", "")
	mapping := map[string]string{"login": "1", "firstname": "2", "lastname": "3", "mail": "4", "admin": "6", "password": "8"}
	if res, body := importSettings(t, c, ts, id, utf8Semicolon, mapping); res.StatusCode != http.StatusFound {
		t.Fatalf("settings: status %d\n%s", res.StatusCode, body)
	}
	_, _, body := ae.do("hijacked", "POST", "/imports/"+id+"/run", nil, "")
	if !strings.Contains(body, `name="sudo_password"`) {
		t.Errorf("run user import: sudo form not shown")
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM principals`); n != usersBefore {
		t.Errorf("users created without sudo: %d -> %d", usersBefore, n)
	}
	// パスワードを再入力すれば実行できる
	if st, _, _ := ae.do("hijacked", "POST", "/imports/"+id+"/run", url.Values{"sudo_password": {"admin"}}, ""); st != http.StatusFound {
		t.Errorf("run after sudo: %d", st)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM principals`); n <= usersBefore {
		t.Errorf("no users created after sudo")
	}
}
