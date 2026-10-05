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

// memberOf モードでも group_base_dn の外にある同じ CN のグループは対応表に一致させない
// （ディレクトリの別の OU にグループを作れるユーザーが、対応する buropher のグループに入れた）。
func TestLDAPMemberOfRespectsGroupBase(t *testing.T) {
	ts, d := newFixtureServer(t)
	srv := newLDAPGroupFixture(t, d, true, `,"group_mode":"memberof","group_base_dn":"`+ldapGroupBase+`"`)
	ctx := context.Background()
	if _, err := d.Exec(ctx, `UPDATE user_accounts SET auth_source_id = 1 WHERE principal_id = 2`); err != nil {
		t.Fatal(err)
	}
	for _, e := range srv.Entries {
		if strings.HasPrefix(e.DN, "uid=jsmith,") {
			e.Attrs["memberOf"] = []string{"cn=devs,ou=selfservice,dc=redmine,dc=org"}
		}
		if strings.HasPrefix(e.DN, "uid=example1,") {
			e.Attrs["memberOf"] = []string{"CN=Staff," + strings.ToUpper(ldapGroupBase)}
		}
	}
	if _, res, _ := tryLogin(t, ts.URL, "jsmith", "ldappw"); res.StatusCode != 302 {
		t.Fatalf("login: %d", res.StatusCode)
	}
	if got := userGroupIDs(t, d, "jsmith"); len(got) != 0 {
		t.Errorf("jsmith groups = %v, want none (group outside group_base_dn)", got)
	}
	// 検索ベースの下のグループは従来どおり一致する（DN の大文字小文字は区別しない）
	if _, res, _ := tryLogin(t, ts.URL, "example1", "123456"); res.StatusCode != 302 {
		t.Fatalf("onthefly login: %d", res.StatusCode)
	}
	if got := userGroupIDs(t, d, "example1"); len(got) != 1 || got[0] != 11 {
		t.Errorf("example1 groups = %v, want [11]", got)
	}
}
