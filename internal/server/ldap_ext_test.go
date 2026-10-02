package server_test

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/auth/ldap/ldaptest"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/handler"
	"github.com/mikuta0407/buropher/internal/repository"
)

// LDAP 認証方式の buropher 拡張（STARTTLS・CA 証明書・フェイルオーバ・AD プリセット・グループ同期・定期同期）のテスト。

const ldapGroupBase = "ou=Groups,dc=redmine,dc=org"

// newLDAPGroupFixture はグループを持つテスト用 LDAP サーバと、それを指す認証方式（id 1）を作る。extra は config に足す JSON。
func newLDAPGroupFixture(t *testing.T, d *db.DB, onthefly bool, extra string) *ldaptest.Server {
	t.Helper()
	srv := &ldaptest.Server{Entries: []*ldaptest.Entry{
		{DN: "cn=admin,dc=redmine,dc=org", Password: "secret"},
		{DN: "uid=jsmith,ou=Person,dc=redmine,dc=org", Password: "ldappw", Attrs: map[string][]string{
			"uid": {"jsmith"}, "givenName": {"Johnny"}, "sn": {"Smithers"}, "mail": {"johnny@example.net"},
			"memberOf": {"cn=devs," + ldapGroupBase}, "userAccountControl": {"512"}}},
		{DN: "uid=example1,ou=Person,dc=redmine,dc=org", Password: "123456", Attrs: map[string][]string{
			"uid": {"example1"}, "givenName": {"Example"}, "sn": {"One"}, "mail": {"example1@redmine.org"},
			"memberOf": {"cn=staff," + ldapGroupBase}}},
		{DN: "uid=rhill,ou=Person,dc=redmine,dc=org", Password: "pw", Attrs: map[string][]string{
			"uid": {"rhill"}, "givenName": {"Robert"}, "sn": {"Hill"}, "mail": {"rhill@somenet.foo"}, "userAccountControl": {"514"}}},
		{DN: "cn=devs," + ldapGroupBase, Attrs: map[string][]string{"objectClass": {"group"}, "cn": {"devs"},
			"member": {"uid=jsmith,ou=Person,dc=redmine,dc=org"}}},
		{DN: "cn=staff," + ldapGroupBase, Attrs: map[string][]string{"objectClass": {"group"}, "cn": {"staff"},
			"member": {"uid=example1,ou=Person,dc=redmine,dc=org"}}},
	}}
	addr, err := srv.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	_, p, _ := net.SplitHostPort(addr)
	if _, err := d.Exec(context.Background(), `INSERT INTO auth_sources (id, kind, name, enabled, position, onthefly_register, config, created_at, updated_at)
VALUES (1, 'ldap', 'LDAP test server', TRUE, 1, ?, ?, '2026-01-15T12:00:00.000000Z', '2026-01-15T12:00:00.000000Z')`, onthefly,
		`{"host":"127.0.0.1","port":`+p+`,"account":"cn=admin,dc=redmine,dc=org","base_dn":"dc=redmine,dc=org","tls":false,"verify_peer":true,`+
			`"attr_login":"uid","attr_firstname":"givenName","attr_lastname":"sn","attr_mail":"mail"`+extra+`}`); err != nil {
		t.Fatal(err)
	}
	// 秘密値（サービスアカウントのパスワード）は平文でも読める
	if _, err := d.Exec(context.Background(), `UPDATE auth_sources SET secret = 'secret' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	// 対応表: devs（CN で指定）→ A Team（10）、staff（DN で指定）→ B Team（11）
	if _, err := d.Exec(context.Background(), `INSERT INTO auth_source_group_mappings (auth_source_id, external_group, group_id) VALUES (1, 'devs', 10), (1, ?, 11)`,
		"cn=staff,"+ldapGroupBase); err != nil {
		t.Fatal(err)
	}
	return srv
}

func userGroupIDs(t *testing.T, d *db.DB, login string) []int64 {
	t.Helper()
	var ids []int64
	if err := d.Select(context.Background(), &ids, `SELECT g.group_id FROM group_users g JOIN user_accounts ua ON ua.principal_id = g.user_id
WHERE ua.login = ? ORDER BY g.group_id`, login); err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestLDAPGroupSyncOnLogin(t *testing.T) {
	ts, d := newFixtureServer(t)
	newLDAPGroupFixture(t, d, true, `,"group_mode":"memberof"`)
	ctx := context.Background()
	if _, err := d.Exec(ctx, `UPDATE user_accounts SET auth_source_id = 1 WHERE principal_id = 2`); err != nil {
		t.Fatal(err)
	}
	// jsmith は B Team（staff に対応）に入っているが LDAP では devs のみ
	if _, err := d.Exec(ctx, `INSERT INTO group_users (group_id, user_id) VALUES (11, 2)`); err != nil {
		t.Fatal(err)
	}
	if _, res, _ := tryLogin(t, ts.URL, "jsmith", "ldappw"); res.StatusCode != 302 {
		t.Fatalf("login: %d", res.StatusCode)
	}
	if got := userGroupIDs(t, d, "jsmith"); len(got) != 1 || got[0] != 10 {
		t.Errorf("jsmith groups = %v, want [10]", got)
	}
	// A Team のメンバーシップのロールが継承される（Group#user_added）
	if n := queryInt(t, d, `SELECT COUNT(*) FROM members m JOIN member_roles mr ON mr.member_id = m.id
WHERE m.principal_id = 2 AND mr.inherited_from IS NOT NULL`); n == 0 {
		t.Error("inherited roles not given")
	}
	// 他のユーザーの所属は変えない
	if n := queryInt(t, d, `SELECT COUNT(*) FROM group_users WHERE user_id = 8`); n != 2 {
		t.Errorf("user 8 groups = %d", n)
	}
	// オンザフライ登録でも同期する
	if _, res, _ := tryLogin(t, ts.URL, "example1", "123456"); res.StatusCode != 302 {
		t.Fatalf("onthefly login: %d", res.StatusCode)
	}
	if got := userGroupIDs(t, d, "example1"); len(got) != 1 || got[0] != 11 {
		t.Errorf("example1 groups = %v, want [11]", got)
	}

	// group_mode が空なら同期しない
	if err := repository.UpdateAuthSourceConfigValues(ctx, d, 1, map[string]any{"group_mode": ""}); err != nil {
		t.Fatal(err)
	}
	_, _ = d.Exec(ctx, `DELETE FROM group_users WHERE user_id = 2`)
	tryLogin(t, ts.URL, "jsmith", "ldappw")
	if got := userGroupIDs(t, d, "jsmith"); len(got) != 0 {
		t.Errorf("synchronized without group_mode: %v", got)
	}
}

func TestLDAPPeriodicSync(t *testing.T) {
	var app *handler.App
	ts, d := newFixtureServer(t, func(a *handler.App, _ chi.Router) { app = a })
	newLDAPGroupFixture(t, d, false, `,"directory_type":"active_directory","group_mode":"search","group_base_dn":"`+ldapGroupBase+
		`","sync_enabled":true,"sync_lock_missing":true,"sync_update_attrs":true`)
	ctx := context.Background()
	// 管理者は LDAP に切り替える前にログインしておく
	c := login(t, ts, "admin", "admin")
	jsmith := login(t, ts, "jsmith", "jsmith")
	// admin(1), jsmith(2), dlopper(3), rhill(4) が LDAP を使う。dlopper はディレクトリに無く、rhill は AD で無効
	if _, err := d.Exec(ctx, `UPDATE user_accounts SET auth_source_id = 1 WHERE principal_id IN (1, 2, 3, 4)`); err != nil {
		t.Fatal(err)
	}
	if err := app.LDAPSyncJob(ctx, nil); err != nil {
		t.Fatal(err)
	}
	status := func(id int) int64 { return queryInt(t, d, `SELECT status FROM principals WHERE id = ?`, id) }
	if status(1) != 1 || status(2) != 1 || status(3) != 3 || status(4) != 3 {
		t.Errorf("statuses: admin %d jsmith %d dlopper %d rhill %d", status(1), status(2), status(3), status(4))
	}
	var name, mail string
	_ = d.Get(ctx, &name, `SELECT firstname || ' ' || lastname FROM principals WHERE id = 2`)
	_ = d.Get(ctx, &mail, `SELECT address FROM email_addresses WHERE user_id = 2 AND is_default = TRUE`)
	if name != "Johnny Smithers" || mail != "johnny@example.net" {
		t.Errorf("jsmith attrs: %q %q", name, mail)
	}
	if got := userGroupIDs(t, d, "jsmith"); len(got) != 1 || got[0] != 10 {
		t.Errorf("jsmith groups = %v", got)
	}
	rec, err := repository.GetAuthSource(ctx, d, 1)
	if err != nil {
		t.Fatal(err)
	}
	log, _ := rec.ConfigString("sync_last_log")
	for _, s := range []string{"users: 4, updated: 1, locked: 2, group changes: 1, errors: 0", "dlopper: not found in the directory, locked",
		"rhill: disabled in Active Directory, locked", "admin: not found in the directory (administrator, not locked)"} {
		if !strings.Contains(log, s) {
			t.Errorf("log lacks %q:\n%s", s, log)
		}
	}
	// 間隔（既定 24 時間）が過ぎるまで次の同期はしない
	_, _ = d.Exec(ctx, `UPDATE principals SET status = 1 WHERE id = 3`)
	if err := app.LDAPSyncJob(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if status(3) != 1 {
		t.Error("synchronized again before the interval")
	}

	// 「今すぐ同期」（間隔に関係なく実行）
	_, body := get(t, c, ts.URL+"/auth_sources/1/edit")
	if !strings.Contains(body, `<form class="button_to" method="post" action="/auth_sources/1/sync">`) || !strings.Contains(body, "dlopper: not found in the directory, locked") {
		t.Errorf("sync status:\n%s", extract(body, `<div class="box buropher-extra" id="ldap-sync-status">`, "</div>"))
	}
	res, _ := post(t, c, ts.URL+"/auth_sources/1/sync", url.Values{"authenticity_token": {csrfToken(t, body)}})
	if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/auth_sources/1/edit") {
		t.Fatalf("sync now: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if status(3) != 3 {
		t.Error("sync now did not lock dlopper")
	}
	_, body = get(t, c, ts.URL+"/auth_sources/1/edit")
	if !strings.Contains(body, "Synchronization completed (users: 3, updated: 0, locked: 1, group changes: 0, errors: 0).") {
		t.Errorf("flash:\n%s", extract(body, `<div class="flash`, "</div>"))
	}
	// 管理者以外は 403
	if res, _ := post(t, jsmith, ts.URL+"/auth_sources/1/sync", url.Values{"authenticity_token": {csrfToken(t, body)}}); res.StatusCode != 403 && res.StatusCode != 422 {
		t.Errorf("non-admin sync: %d", res.StatusCode)
	}
}

func TestLDAPExtForm(t *testing.T) {
	ts, d := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")
	ctx := context.Background()
	base := url.Values{
		"type": {"AuthSourceLdap"}, "auth_source[name]": {"Corp AD"}, "auth_source[host]": {"dc1.example.com"},
		"auth_source[port]": {"389"}, "auth_source[ldap_mode]": {"ldap"}, "auth_source[account]": {"cn=svc"},
		"auth_source[base_dn]": {"dc=example,dc=com"}, "auth_source[attr_login]": {"sAMAccountName"},
	}
	with := func(kv ...string) url.Values {
		v := url.Values{}
		for k, vs := range base {
			v[k] = vs
		}
		for i := 0; i < len(kv); i += 2 {
			v.Add(kv[i], kv[i+1])
		}
		return v
	}
	// 新規作成画面: 追加の欄が Redmine のフォームの後ろにある
	body := adminGet(t, c, ts.URL+"/auth_sources/new")
	for _, s := range []string{`<div class="buropher-extra" id="ldap-extra">`, `name="auth_source[starttls]"`, `name="auth_source[ca_cert]"`,
		`name="auth_source[failover_hosts]"`, `<option value="active_directory">Active Directory</option>`, `name="group_mapping_external[]"`,
		`<option value="10">A Team</option>`, `name="auth_source[sync_enabled]"`} {
		if !strings.Contains(body, s) {
			t.Errorf("new form lacks %s", s)
		}
	}
	if strings.Index(body, `id="ldap-extra"`) < strings.Index(body, `name="auth_source[attr_mail]"`) {
		t.Error("extra fields before Redmine fields")
	}

	// 検証エラー
	res, body := send(t, c, ts.URL, http.MethodPost, "/auth_sources", with("auth_source[ldap_mode]", "ldaps_verify_peer",
		"auth_source[starttls]", "1", "auth_source[ca_cert]", "-----BEGIN CERTIFICATE-----\nxx\n-----END CERTIFICATE-----",
		"auth_source[group_filter]", "(cn=x", "auth_source[sync_interval]", "0"))
	if res.StatusCode != 200 {
		t.Fatalf("invalid: %d", res.StatusCode)
	}
	for _, s := range []string{"<li>STARTTLS is invalid</li>", "<li>CA certificate is invalid</li>", "<li>Group filter is invalid</li>",
		"<li>Interval (hours) must be greater than or equal to 1</li>"} {
		if !strings.Contains(body, s) {
			t.Errorf("errors lack %s:\n%s", s, extract(body, "<div id='errorExplanation'>", "</div>"))
		}
	}

	// 作成
	srv := &ldaptest.Server{}
	if _, err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	res, _ = send(t, c, ts.URL, http.MethodPost, "/auth_sources", with("auth_source[starttls]", "1", "auth_source[starttls_verify_peer]", "1",
		"auth_source[ca_cert]", string(srv.CertPEM), "auth_source[failover_hosts]", "dc2.example.com\r\ndc3.example.com:3268",
		"auth_source[directory_type]", "active_directory", "auth_source[group_mode]", "memberof", "auth_source[group_nested]", "1",
		"auth_source[sync_enabled]", "1", "auth_source[sync_interval]", "6",
		"group_mapping_external[]", "CN=Devs,OU=Groups,DC=example,DC=com", "group_mapping_group_id[]", "10",
		"group_mapping_external[]", "", "group_mapping_group_id[]", ""))
	if res.StatusCode != 302 {
		t.Fatalf("create: %d", res.StatusCode)
	}
	id := queryInt(t, d, `SELECT id FROM auth_sources WHERE name = 'Corp AD'`)
	var cfg string
	_ = d.Get(ctx, &cfg, `SELECT config FROM auth_sources WHERE id = ?`, id)
	for _, s := range []string{`"starttls":true`, `"starttls_verify_peer":true`, `"failover_hosts":"dc2.example.com\ndc3.example.com:3268"`, `"directory_type":"active_directory"`,
		`"group_mode":"memberof"`, `"group_nested":true`, `"sync_enabled":true`, `"sync_interval":6`, `"ca_cert":"-----BEGIN CERTIFICATE-----`} {
		if !strings.Contains(cfg, s) {
			t.Errorf("config %s lacks %s", cfg, s)
		}
	}
	if strings.Contains(cfg, "sync_lock_missing") || strings.Contains(cfg, "group_base_dn") {
		t.Errorf("blank values stored: %s", cfg)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM auth_source_group_mappings WHERE auth_source_id = ? AND group_id = 10`, id); n != 1 {
		t.Error("mapping not saved")
	}
	// 編集画面に値が出る
	body = adminGet(t, c, ts.URL+"/auth_sources/"+itoa64(id)+"/edit")
	for _, s := range []string{`<input type="checkbox" value="1" checked="checked" name="auth_source[starttls]"`,
		`<option selected="selected" value="active_directory">`, `value="CN=Devs,OU=Groups,DC=example,DC=com"`,
		`<option selected="selected" value="10">A Team</option>`, `dc3.example.com:3268</textarea>`} {
		if !strings.Contains(body, s) {
			t.Errorf("edit form lacks %s", s)
		}
	}
	// 対応表を空にして、STARTTLS を外す（チェックボックスの hidden 値 0）
	res, _ = send(t, c, ts.URL, http.MethodPatch, "/auth_sources/"+itoa64(id), url.Values{
		"auth_source[starttls]": {"0"}, "group_mapping_external[]": {""}, "group_mapping_group_id[]": {""}})
	if res.StatusCode != 302 {
		t.Fatalf("update: %d", res.StatusCode)
	}
	_ = d.Get(ctx, &cfg, `SELECT config FROM auth_sources WHERE id = ?`, id)
	if strings.Contains(cfg, `"starttls":`) || !strings.Contains(cfg, `"directory_type":"active_directory"`) {
		t.Errorf("updated config: %s", cfg)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM auth_source_group_mappings WHERE auth_source_id = ?`, id); n != 0 {
		t.Error("mappings not cleared")
	}
}
