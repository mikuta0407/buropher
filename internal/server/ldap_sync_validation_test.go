// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/handler"
	"github.com/mikuta0407/buropher/internal/repository"
)

// 定期同期はディレクトリの氏名・メールアドレスをユーザーのモデルと同じ規則で検証してから保存する
// （ディレクトリの自分のエントリを編集できるユーザーが、カンマ区切りの複数宛先や拒否ドメインのアドレス、
// 長すぎる氏名を入れられた）。
func TestLDAPSyncValidatesDirectoryAttributes(t *testing.T) {
	var app *handler.App
	ts, d := newFixtureServer(t, func(a *handler.App, _ chi.Router) { app = a })
	srv := newLDAPGroupFixture(t, d, false, `,"sync_enabled":true,"sync_update_attrs":true`)
	_ = ts
	ctx := context.Background()
	if _, err := d.Exec(ctx, `UPDATE user_accounts SET auth_source_id = 1 WHERE principal_id = 2`); err != nil {
		t.Fatal(err)
	}
	for _, e := range srv.Entries {
		if strings.HasPrefix(e.DN, "uid=jsmith,") {
			e.Attrs["givenName"] = []string{strings.Repeat("G", 31)}
			e.Attrs["sn"] = []string{"Smithers"}
			e.Attrs["mail"] = []string{"me@example.net,boss@example.com"}
		}
	}
	attrs := func() (string, string, string) {
		var first, last, mail string
		_ = d.Get(ctx, &first, `SELECT firstname FROM principals WHERE id = 2`)
		_ = d.Get(ctx, &last, `SELECT lastname FROM principals WHERE id = 2`)
		_ = d.Get(ctx, &mail, `SELECT address FROM email_addresses WHERE user_id = 2 AND is_default = TRUE`)
		return first, last, mail
	}
	_, _, mail0 := attrs()
	if err := app.LDAPSyncJob(ctx, nil); err != nil {
		t.Fatal(err)
	}
	first, last, mail := attrs()
	if first != "John" || last != "Smithers" || mail != mail0 {
		t.Fatalf("attrs after sync: %q %q %q", first, last, mail)
	}
	rec, err := repository.GetAuthSource(ctx, d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if log, _ := rec.ConfigString("sync_last_log"); !strings.Contains(log, "jsmith: invalid email") || !strings.Contains(log, "jsmith: invalid firstname") {
		t.Errorf("log:\n%s", log)
	}

	// 拒否ドメインのアドレスも保存しない
	if err := app.Settings.Set(ctx, "email_domains_denied", "example.com"); err != nil {
		t.Fatal(err)
	}
	for _, e := range srv.Entries {
		if strings.HasPrefix(e.DN, "uid=jsmith,") {
			e.Attrs["mail"] = []string{"boss@example.com"}
		}
	}
	if err := repository.UpdateAuthSourceConfigValues(ctx, d, 1, map[string]any{"sync_last_at": ""}); err != nil {
		t.Fatal(err)
	}
	if err := app.LDAPSyncJob(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, mail := attrs(); mail != mail0 {
		t.Fatalf("denied domain saved: %q", mail)
	}
}
