// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"net"
	"net/url"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/auth/ldap/ldaptest"
)

// SSO 必須モードでは、LDAP のオンザフライ作成に失敗したときの登録画面（auth_source_registration）からも
// パスワードでログインさせない（そのセッションで API キーを作れば IdP の MFA・無効化を迂回し続けられる）。
func TestSSORequiredRefusesLDAPOntheflyRegistration(t *testing.T) {
	e := newSSOEnv(t, url.Values{"auth_source[sso_required]": {"1"}})
	srv := &ldaptest.Server{Entries: []*ldaptest.Entry{
		{DN: "uid=nomail,ou=Person,dc=redmine,dc=org", Password: "123456", Attrs: map[string][]string{
			"uid": {"nomail"}, "givenName": {"No"}, "sn": {"Mail"}}},
	}}
	addr, err := srv.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	_, p, _ := net.SplitHostPort(addr)
	if _, err := e.d.Exec(context.Background(), `INSERT INTO auth_sources (id, kind, name, enabled, position, onthefly_register, config, created_at, updated_at)
VALUES (90, 'ldap', 'LDAP', TRUE, 2, TRUE, ?, '2026-01-15T12:00:00.000000Z', '2026-01-15T12:00:00.000000Z')`,
		`{"host":"127.0.0.1","port":`+p+`,"base_dn":"ou=Person,dc=redmine,dc=org","tls":false,"attr_login":"uid","attr_firstname":"givenName","attr_lastname":"sn","attr_mail":"mail"}`); err != nil {
		t.Fatal(err)
	}

	c := newClient(t)
	_, body := get(t, c, e.ts.URL+"/login?local=1")
	_, body = post(t, c, e.ts.URL+"/login", url.Values{"authenticity_token": {csrfToken(t, body)}, "username": {"nomail"}, "password": {"123456"}})
	tok := csrfToken(t, body)
	res, _ := post(t, c, e.ts.URL+"/account/register", url.Values{"authenticity_token": {tok},
		"user[firstname]": {"No"}, "user[lastname]": {"Mail"}, "user[mail]": {"nomail@example.net"}})
	if res.StatusCode == 302 && strings.Contains(res.Header.Get("Location"), "/my/account") {
		t.Fatal("LDAP user logged in through the registration form under sso_required")
	}
	if _, body := get(t, c, e.ts.URL+"/my/account"); strings.Contains(body, "Logged in as") {
		t.Fatal("session is logged in")
	}
	if n := queryInt(t, e.d, `SELECT COUNT(*) FROM user_accounts WHERE login = 'nomail'`); n != 0 {
		t.Errorf("user created: %d", n)
	}
}

// SSO 必須モードでは、自己登録の自動有効化でもパスワードで作ったアカウントのままログインさせない。
func TestSSORequiredAutomaticRegistrationDoesNotLogIn(t *testing.T) {
	e := newSSOEnv(t, url.Values{"auth_source[sso_required]": {"1"}})
	_, page := get(t, e.adm, e.ts.URL+"/settings?tab=authentication")
	if res, _ := post(t, e.adm, e.ts.URL+"/settings/edit?tab=authentication", url.Values{"authenticity_token": {csrfToken(t, page)},
		"settings[self_registration]": {"3"}}); res.StatusCode != 302 {
		t.Fatalf("settings: %d", res.StatusCode)
	}
	c := newClient(t)
	_, body := get(t, c, e.ts.URL+"/account/register")
	res, _ := post(t, c, e.ts.URL+"/account/register", url.Values{"authenticity_token": {csrfToken(t, body)},
		"user[login]": {"autouser"}, "user[password]": {"autouser1"}, "user[password_confirmation]": {"autouser1"},
		"user[firstname]": {"Auto"}, "user[lastname]": {"User"}, "user[mail]": {"autouser@example.net"}, "user[language]": {"en"}})
	if res.StatusCode != 302 {
		t.Fatalf("register: %d", res.StatusCode)
	}
	if _, body := get(t, c, e.ts.URL+"/my/page"); strings.Contains(body, "Logged in as") {
		t.Fatal("automatically registered user is logged in under sso_required")
	}
}

// Discord の連携はログイン手段ではないため、SSO だけでログインするユーザーの最後の OIDC 連携の解除を
// 許す理由にならない（解除するとログインできなくなる）。
func TestMySSOUnlinkIgnoresDiscordIdentity(t *testing.T) {
	e := newSSOEnv(t, nil)
	c := login(t, e.ts, "dlopper", "foo")
	ctx := context.Background()
	if _, err := e.d.Exec(ctx, `UPDATE user_accounts SET auth_source_id = `+e.id+`, password_hash = NULL WHERE principal_id = 3`); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO user_identities (user_id, provider, auth_source_id, subject, created_at) VALUES (3, 'oidc:` + e.id + `', ` + e.id + `, 'dl-sub', '2026-01-15T12:00:00.000000Z')`,
		`INSERT INTO user_identities (user_id, provider, subject, raw_claims, created_at) VALUES (3, 'discord', '1234', '{}', '2026-01-15T12:00:00.000000Z')`,
	} {
		if _, err := e.d.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	var id string
	_ = e.d.Get(ctx, &id, `SELECT CAST(id AS TEXT) FROM user_identities WHERE user_id = 3 AND provider LIKE 'oidc:%'`)
	_, page := get(t, c, e.ts.URL+"/my/sso")
	res, _ := post(t, c, e.ts.URL+"/my/sso/"+id, url.Values{"_method": {"delete"}, "authenticity_token": {metaCSRF(t, page)}})
	if msg := e.flashAfter(c, res); !strings.Contains(msg, "only way to log in") {
		t.Fatalf("unlink: %q", msg)
	}
	if n := queryInt(t, e.d, `SELECT COUNT(*) FROM user_identities WHERE user_id = 3 AND provider LIKE 'oidc:%'`); n != 1 {
		t.Fatalf("oidc identity deleted")
	}
}
