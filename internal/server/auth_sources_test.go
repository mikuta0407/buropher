// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/auth/ldap/ldaptest"
	"github.com/mikuta0407/buropher/internal/crypto/secretbox"
	"github.com/mikuta0407/buropher/internal/db"
)

// 認証方式（LDAP）の互換テスト。testdata/auth_sources/ の期待値は共用の参照 Redmine（3998）から
// `go run ./tools/compat fetch -base http://127.0.0.1:3998 -raw -user admin <path>` で取得したもの。
// testfixtures は auth_sources を投入しないため、参照環境と同じ行（auth_sources.yml。host は空文字列）を SQL で作る。

func insertFixtureAuthSource(t *testing.T, d *db.DB) {
	t.Helper()
	if _, err := d.Exec(context.Background(), `INSERT INTO auth_sources (id, kind, name, enabled, position, onthefly_register, config, created_at, updated_at)
VALUES (1, 'ldap', 'LDAP test server', TRUE, 1, FALSE, ?, '2026-01-15T12:00:00.000000Z', '2026-01-15T12:00:00.000000Z')`,
		`{"host":"","port":389,"base_dn":"OU=Person,DC=redmine,DC=org","tls":false,"verify_peer":true,"attr_login":"uid","attr_firstname":"givenName","attr_lastname":"sn","attr_mail":"mail"}`); err != nil {
		t.Fatal(err)
	}
}

var recentlyUsedRe = regexp.MustCompile(`<strong>Recently used</strong>.*?(<strong>All Projects</strong>)`)

func stripRecentlyUsed(s string) string { return recentlyUsedRe.ReplaceAllString(s, "$1") }

var buropherExtraRe = regexp.MustCompile(`\n<a class="icon icon-add buropher-extra"[^>]*>[^<]*</a>`)

// buropherExtraBlockRe は buropher 拡張の LDAP の追加設定・同期の欄（<!-- /buropher-extra --> で終わる要素）。
var buropherExtraBlockRe = regexp.MustCompile(`(?s)\n[ ]*<div class="(box )?buropher-extra".*?<!-- /buropher-extra -->\n?`)

// stripBuropherExtra は buropher 拡張のリンク（class に buropher-extra を持つ a 要素）と追加の欄を除く。
func stripBuropherExtra(s string) string {
	return buropherExtraBlockRe.ReplaceAllString(buropherExtraRe.ReplaceAllString(s, ""), "")
}

func TestAuthSourcesPagesMatchRedmine(t *testing.T) {
	ts, d := newFixtureServer(t)
	insertFixtureAuthSource(t, d)
	c := login(t, ts, "admin", "admin")
	for _, tc := range []struct{ name, path string }{
		{"index.html", "/auth_sources"},
		{"new.html", "/auth_sources/new"},
		{"edit_1.html", "/auth_sources/1/edit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// ジャンプボックスの「最近使ったもの」は共用の参照環境の閲覧履歴に依存するため除く
			// buropher 拡張の「新しい OpenID Connect プロバイダ」リンク（buropher-extra）は参照に無いため除く
			compareAdminGolden(t, "../auth_sources/"+tc.name, stripBuropherExtra(adminGet(t, c, ts.URL+tc.path)), ts.URL, stripRecentlyUsed)
		})
	}
	// 管理者以外は 403、存在しない id は 404、type が LDAP 以外は 404
	jsmith := login(t, ts, "jsmith", "jsmith")
	if res, _ := get(t, jsmith, ts.URL+"/auth_sources"); res.StatusCode != 403 {
		t.Errorf("jsmith: %d", res.StatusCode)
	}
	if res, _ := get(t, c, ts.URL+"/auth_sources/99/edit"); res.StatusCode != 404 {
		t.Errorf("missing: %d", res.StatusCode)
	}
	if res, _ := get(t, c, ts.URL+"/auth_sources/new?type=AuthSourceFoo"); res.StatusCode != 404 {
		t.Errorf("bad type: %d", res.StatusCode)
	}
	if res, _ := get(t, c, ts.URL+"/auth_sources/new"); res.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("new cache-control %q", res.Header.Get("Cache-Control"))
	}
}

func TestAuthSourcesWrite(t *testing.T) {
	ts, d := newFixtureServer(t)
	insertFixtureAuthSource(t, d)
	c := login(t, ts, "admin", "admin")
	ctx := context.Background()

	// 検証エラー（宣言順）
	res, body := send(t, c, ts.URL, http.MethodPost, "/auth_sources", url.Values{
		"type": {"AuthSourceLdap"}, "auth_source[name]": {"LDAP test server"}, "auth_source[host]": {""},
		"auth_source[port]": {"abc"}, "auth_source[ldap_mode]": {"ldap"}, "auth_source[base_dn]": {""},
		"auth_source[filter]": {"(uid=x"}, "auth_source[timeout]": {"1.5"}, "auth_source[attr_login]": {"  "},
	})
	if res.StatusCode != 200 {
		t.Fatalf("create invalid: %d", res.StatusCode)
	}
	want := "<ul>\n<li>Name has already been taken</li>\n<li>Host cannot be blank</li>\n<li>Login attribute cannot be blank</li>\n" +
		"<li>Port is not a number</li>\n<li>Timeout (in seconds) must be an integer</li>\n<li>LDAP filter is invalid</li>\n</ul>"
	if !strings.Contains(body, want) {
		t.Errorf("errors:\n%s", extract(body, "<div id='errorExplanation'>", "</div>"))
	}
	if !strings.Contains(body, `<input size="6" type="text" value="abc" name="auth_source[port]" id="auth_source_port" />`) {
		t.Error("raw port value not kept")
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Error("no_store")
	}

	// 作成（パスワードは暗号化して保存）
	res, _ = send(t, c, ts.URL, http.MethodPost, "/auth_sources", url.Values{
		"type": {"AuthSourceLdap"}, "auth_source[name]": {"Corp"}, "auth_source[host]": {"ldap.example.com"},
		"auth_source[port]": {"636"}, "auth_source[ldap_mode]": {"ldaps_verify_none"}, "auth_source[account]": {"cn=admin"},
		"auth_source[account_password]": {"s3cret"}, "auth_source[base_dn]": {"dc=example"}, "auth_source[filter]": {""},
		"auth_source[timeout]": {""}, "auth_source[onthefly_register]": {"1"}, "auth_source[attr_login]": {" uid "},
		"auth_source[attr_firstname]": {"givenName"}, "auth_source[attr_lastname]": {""}, "auth_source[attr_mail]": {"mail"},
	})
	if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/auth_sources") {
		t.Fatalf("create: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	var cfg, secret string
	if err := d.Get(ctx, &cfg, `SELECT config FROM auth_sources WHERE name = 'Corp'`); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{`"attr_login":"uid"`, `"port":636`, `"tls":true`, `"verify_peer":false`, `"filter":""`, `"attr_lastname":""`} {
		if !strings.Contains(cfg, s) {
			t.Errorf("config %s lacks %s", cfg, s)
		}
	}
	if strings.Contains(cfg, `"timeout"`) {
		t.Errorf("blank timeout stored: %s", cfg)
	}
	if err := d.Get(ctx, &secret, `SELECT secret FROM auth_sources WHERE name = 'Corp'`); err != nil {
		t.Fatal(err)
	}
	box, _ := secretbox.New("test-secret")
	if p, err := box.Open(secret); err != nil || p != "s3cret" {
		t.Errorf("secret %q: %v %v", secret, p, err)
	}
	id := int(queryInt(t, d, `SELECT id FROM auth_sources WHERE name = 'Corp'`))
	body = adminGet(t, c, ts.URL+"/auth_sources")
	if !strings.Contains(body, "Successful creation.") || !strings.Contains(body, "<td>ldap.example.com</td>") {
		t.Error("index after create")
	}

	// 編集画面: パスワードは x*15、LDAPS（検証なし）が選択済み
	body = adminGet(t, c, ts.URL+"/auth_sources/"+strconv.Itoa(id)+"/edit")
	if !strings.Contains(body, `<input value="xxxxxxxxxxxxxxx" name="dummy_password"`) ||
		!strings.Contains(body, `<option selected="selected" value="ldaps_verify_none">`) ||
		!strings.Contains(body, `<input type="checkbox" value="1" checked="checked" name="auth_source[onthefly_register]"`) {
		t.Errorf("edit form:\n%s", extract(body, `<div class="box tabular">`, "</div>"))
	}

	// 更新: dummy_password のままならパスワードは変わらない
	res, _ = send(t, c, ts.URL, http.MethodPatch, "/auth_sources/"+strconv.Itoa(id), url.Values{
		"auth_source[name]": {"Corp 2"}, "dummy_password": {"xxxxxxxxxxxxxxx"}, "auth_source[ldap_mode]": {"ldap"},
	})
	if res.StatusCode != 302 {
		t.Fatalf("update: %d", res.StatusCode)
	}
	var secret2 string
	_ = d.Get(ctx, &secret2, `SELECT secret FROM auth_sources WHERE id = ?`, id)
	if secret2 != secret {
		t.Error("password changed by dummy field")
	}
	// パスワードを空にすると削除
	res, _ = send(t, c, ts.URL, http.MethodPatch, "/auth_sources/"+strconv.Itoa(id), url.Values{"auth_source[account_password]": {""}})
	if res.StatusCode != 302 {
		t.Fatalf("update password: %d", res.StatusCode)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM auth_sources WHERE id = ? AND secret IS NULL`, id); n != 1 {
		t.Error("password not cleared")
	}
	// 更新の検証エラー（タイトルは入力中の名前）
	res, body = send(t, c, ts.URL, http.MethodPatch, "/auth_sources/"+strconv.Itoa(id), url.Values{"auth_source[name]": {""}, "auth_source[port]": {""}})
	if res.StatusCode != 200 || !strings.Contains(body, "<li>Name cannot be blank</li>\n<li>Port cannot be blank</li>\n<li>Port is not a number</li>") {
		t.Errorf("update invalid: %d\n%s", res.StatusCode, extract(body, "<div id='errorExplanation'>", "</div>"))
	}

	// 接続テスト（接続できない）
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if _, err := d.Exec(ctx, `UPDATE auth_sources SET config = ? WHERE id = ?`, `{"host":"127.0.0.1","port":`+strconv.Itoa(port)+`,"attr_login":"uid"}`, id); err != nil {
		t.Fatal(err)
	}
	res, _ = get(t, c, ts.URL+"/auth_sources/"+strconv.Itoa(id)+"/test_connection")
	if res.StatusCode != 302 {
		t.Fatalf("test_connection: %d", res.StatusCode)
	}
	body = adminGet(t, c, ts.URL+"/auth_sources")
	if !strings.Contains(body, "Unable to connect (LDAP: Connection refused - connect(2) for 127.0.0.1:"+strconv.Itoa(port)+")") {
		t.Errorf("test_connection flash:\n%s", extract(body, `<div class="flash`, "</div>"))
	}
	// 接続エラーの文言（ホスト名や LDAP サーバ由来の診断メッセージを含みうる）はエスケープしてフラッシュに出す
	// （フラッシュは raw HTML として描画される）
	if _, err := d.Exec(ctx, `UPDATE auth_sources SET config = ? WHERE id = ?`, `{"host":"x<img src=x onerror=alert(1)>","port":389,"attr_login":"uid"}`, id); err != nil {
		t.Fatal(err)
	}
	get(t, c, ts.URL+"/auth_sources/"+strconv.Itoa(id)+"/test_connection")
	body = adminGet(t, c, ts.URL+"/auth_sources")
	if flash := extract(body, `<div class="flash`, "</div>"); strings.Contains(flash, "<img") || !strings.Contains(flash, "&lt;img") {
		t.Errorf("test_connection flash not escaped:\n%s", flash)
	}
	// 接続テスト（成功）
	srv := &ldaptest.Server{}
	addr, err := srv.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	_, p, _ := net.SplitHostPort(addr)
	_, _ = d.Exec(ctx, `UPDATE auth_sources SET config = ? WHERE id = ?`, `{"host":"127.0.0.1","port":`+p+`,"attr_login":"uid"}`, id)
	get(t, c, ts.URL+"/auth_sources/"+strconv.Itoa(id)+"/test_connection")
	if body = adminGet(t, c, ts.URL+"/auth_sources"); !strings.Contains(body, "Successful connection.") {
		t.Error("test_connection success flash")
	}

	// 削除: 使用中は不可
	if _, err := d.Exec(ctx, `UPDATE user_accounts SET auth_source_id = ? WHERE principal_id = 2`, id); err != nil {
		t.Fatal(err)
	}
	send(t, c, ts.URL, http.MethodDelete, "/auth_sources/"+strconv.Itoa(id), nil)
	if body = adminGet(t, c, ts.URL+"/auth_sources"); !strings.Contains(body, "This authentication mode is in use and cannot be deleted.") {
		t.Error("in use")
	}
	_, _ = d.Exec(ctx, `UPDATE user_accounts SET auth_source_id = NULL WHERE principal_id = 2`)
	send(t, c, ts.URL, http.MethodDelete, "/auth_sources/"+strconv.Itoa(id), nil)
	if n := queryInt(t, d, `SELECT COUNT(*) FROM auth_sources WHERE id = ?`, id); n != 0 {
		t.Error("not deleted")
	}
}

// newLDAPFixture はテスト用 LDAP サーバと、それを指す認証方式（id 1 を書き換え）を用意する。
func newLDAPFixture(t *testing.T, d *db.DB, onthefly bool, account string) *ldaptest.Server {
	t.Helper()
	srv := &ldaptest.Server{Entries: []*ldaptest.Entry{
		{DN: "cn=admin,dc=redmine,dc=org", Password: "secret"},
		{DN: "uid=example1,ou=Person,dc=redmine,dc=org", Password: "123456", Attrs: map[string][]string{
			"uid": {"example1"}, "givenName": {"Example"}, "sn": {"One"}, "mail": {"example1@redmine.org"}}},
		{DN: "uid=nomail,ou=Person,dc=redmine,dc=org", Password: "123456", Attrs: map[string][]string{
			"uid": {"nomail"}, "givenName": {"No"}, "sn": {"Mail"}}},
		{DN: "uid=jsmith,ou=Person,dc=redmine,dc=org", Password: "ldappw", Attrs: map[string][]string{"uid": {"jsmith"}}},
	}}
	addr, err := srv.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	_, p, _ := net.SplitHostPort(addr)
	acc := ""
	if account != "" {
		acc = `"account":"` + account + `",`
	}
	if _, err := d.Exec(context.Background(), `INSERT INTO auth_sources (id, kind, name, enabled, position, onthefly_register, config, created_at, updated_at)
VALUES (1, 'ldap', 'LDAP test server', TRUE, 1, ?, ?, '2026-01-15T12:00:00.000000Z', '2026-01-15T12:00:00.000000Z')`, onthefly,
		`{"host":"127.0.0.1","port":`+p+`,`+acc+`"base_dn":"ou=Person,dc=redmine,dc=org","tls":false,"verify_peer":true,"attr_login":"uid","attr_firstname":"givenName","attr_lastname":"sn","attr_mail":"mail"}`); err != nil {
		t.Fatal(err)
	}
	return srv
}

func tryLogin(t *testing.T, ts string, user, pw string) (*http.Client, *http.Response, string) {
	t.Helper()
	c := newClient(t)
	_, body := get(t, c, ts+"/login")
	res, body := post(t, c, ts+"/login", url.Values{"authenticity_token": {csrfToken(t, body)}, "username": {user}, "password": {pw}})
	return c, res, body
}

func TestLDAPLogin(t *testing.T) {
	ts, d := newFixtureServer(t)
	newLDAPFixture(t, d, true, "")
	ctx := context.Background()

	// 既存ユーザー（auth_source_id 付き）は LDAP の bind で認証する
	if _, err := d.Exec(ctx, `UPDATE user_accounts SET auth_source_id = 1 WHERE principal_id = 2`); err != nil {
		t.Fatal(err)
	}
	if _, res, _ := tryLogin(t, ts.URL, "jsmith", "jsmith"); res.StatusCode == 302 {
		t.Error("local password accepted for LDAP user")
	}
	if _, res, _ := tryLogin(t, ts.URL, "jsmith", "ldappw"); res.StatusCode != 302 {
		t.Errorf("LDAP user login: %d", res.StatusCode)
	}

	// 未登録ユーザーのオンザフライ作成（言語は Setting.default_language）
	c, res, _ := tryLogin(t, ts.URL, "example1", "123456")
	if res.StatusCode != 302 {
		t.Fatalf("onthefly login: %d", res.StatusCode)
	}
	var row struct {
		ID        int64  `db:"principal_id"`
		Firstname string `db:"firstname"`
		Lastname  string `db:"lastname"`
		Language  string `db:"language"`
		Src       int64  `db:"auth_source_id"`
		Mail      string `db:"address"`
		Notif     string `db:"mail_notification"`
	}
	if err := d.Get(ctx, &row, `SELECT ua.principal_id, p.firstname, p.lastname, ua.language, ua.auth_source_id, e.address, n.mail_notification
FROM user_accounts ua JOIN principals p ON p.id = ua.principal_id JOIN email_addresses e ON e.user_id = p.id
JOIN user_notification_settings n ON n.user_id = p.id WHERE ua.login = 'example1'`); err != nil {
		t.Fatal(err)
	}
	if row.Firstname != "Example" || row.Lastname != "One" || row.Language != "en" || row.Src != 1 || row.Mail != "example1@redmine.org" {
		t.Errorf("created user %+v", row)
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM user_accounts WHERE login = 'example1' AND last_login_at IS NOT NULL AND password_hash IS NULL`); n != 1 {
		t.Error("last_login / password hash")
	}
	if _, body := get(t, c, ts.URL+"/"); !strings.Contains(body, `Logged in as <a class="user active" href="/users/`+strconv.FormatInt(row.ID, 10)+`">example1</a>`) {
		t.Error("not logged in after onthefly creation")
	}
	// 2 回目は既存ユーザーとして LDAP で認証
	if _, res, _ := tryLogin(t, ts.URL, "example1", "123456"); res.StatusCode != 302 {
		t.Error("second login")
	}
	if _, res, body := tryLogin(t, ts.URL, "example1", "bad"); res.StatusCode != 200 || !strings.Contains(body, "Invalid user or password") {
		t.Error("wrong password")
	}

	// 必須属性（メール）が無い → 保存できず登録画面へ（未実装のため資格情報エラー扱い）
	if _, res, _ := tryLogin(t, ts.URL, "nomail", "123456"); res.StatusCode == 302 {
		t.Error("user without mail logged in")
	}
	if n := queryInt(t, d, `SELECT COUNT(*) FROM user_accounts WHERE login = 'nomail'`); n != 0 {
		t.Error("user without mail created")
	}
}

func TestLDAPLoginErrors(t *testing.T) {
	ts, d := newFixtureServer(t)
	insertFixtureAuthSource(t, d)
	ctx := context.Background()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	_, _ = d.Exec(ctx, `UPDATE auth_sources SET config = ? WHERE id = 1`, `{"host":"127.0.0.1","port":`+strconv.Itoa(port)+`,"attr_login":"uid","base_dn":"dc=x"}`)
	_, _ = d.Exec(ctx, `UPDATE user_accounts SET auth_source_id = 1 WHERE principal_id = 2`)
	// 既存の LDAP ユーザーで接続できない → render_error（500 ではなくエラー画面にメッセージ）
	_, res, body := tryLogin(t, ts.URL, "jsmith", "x")
	if res.StatusCode != 500 || !strings.Contains(body, "LDAP: Connection refused") {
		t.Errorf("connection error: %d\n%s", res.StatusCode, extract(body, `<div id="content">`, "</div>"))
	}
	// 未登録ユーザー: オンザフライのソースが無ければ普通の資格情報エラー（AuthSource.authenticate は例外を握りつぶす）
	_, _ = d.Exec(ctx, `UPDATE auth_sources SET onthefly_register = TRUE WHERE id = 1`)
	if _, res, body := tryLogin(t, ts.URL, "unknown", "x"); res.StatusCode != 200 || !strings.Contains(body, "Invalid user or password") {
		t.Errorf("unknown user: %d", res.StatusCode)
	}
}

func TestLDAPLoginSubstitutionAndAutocomplete(t *testing.T) {
	ts, d := newFixtureServer(t)
	srv := newLDAPFixture(t, d, true, "")
	c := login(t, ts, "admin", "admin")
	res, body := get(t, c, ts.URL+"/auth_sources/autocomplete_for_new_user?term=ex")
	want := `[{"value":"example1","label":"example1 (Example One)","login":"example1","firstname":"Example","lastname":"One","mail":"example1@redmine.org","auth_source_id":"1"}]`
	if res.StatusCode != 200 || body != want || !strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
		t.Errorf("autocomplete: %d %s %s", res.StatusCode, res.Header.Get("Content-Type"), body)
	}
	if _, body := get(t, c, ts.URL+"/auth_sources/autocomplete_for_new_user?term="); body != "[]" {
		t.Errorf("blank term: %s", body)
	}
	_ = srv

	// $login 置換（ユーザー自身で bind）
	_, _ = d.Exec(context.Background(), `UPDATE auth_sources SET config = json_set(config, '$.account', 'uid=$login,ou=Person,dc=redmine,dc=org') WHERE id = 1`)
	if _, res, _ := tryLogin(t, ts.URL, "example1", "123456"); res.StatusCode != 302 {
		t.Errorf("$login login: %d", res.StatusCode)
	}
	if b := srv.Binds(); len(b) == 0 || b[len(b)-2] != "uid=example1,ou=Person,dc=redmine,dc=org" {
		t.Errorf("binds %v", b)
	}
	// $login のソースは検索できない
	if _, body := get(t, c, ts.URL+"/auth_sources/autocomplete_for_new_user?term=ex"); body != "[]" {
		t.Errorf("$login search: %s", body)
	}
}
