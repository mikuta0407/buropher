// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"time"

	"github.com/go-chi/chi/v5"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/auth/oidc/oidctest"
	"github.com/mikuta0407/buropher/internal/auth/totp"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/handler"
)

// このファイルは OIDC シングルサインオン（buropher 拡張）の端から端までのテスト。
// テスト用の OIDC プロバイダ（oidctest。ディスカバリ・認可・トークン・JWKS）を立て、
// 管理画面で認証方式を作ってから、ログイン画面 → /auth/oidc/{id}/start → IdP → callback の流れを通す。

type ssoEnv struct {
	t   *testing.T
	ts  *httptest.Server
	d   *db.DB
	idp *oidctest.Provider
	adm *http.Client
	id  string
}

// newSSOEnv はフィクスチャのサーバ・IdP・OIDC 認証方式（id は ssoEnv.id）を用意する。
func newSSOEnv(t *testing.T, form url.Values, extra ...func(a *handler.App, r chi.Router)) *ssoEnv {
	t.Helper()
	myFreezeClock(t)
	ts, d := newFixtureServer(t, extra...)
	idp := oidctest.New()
	t.Cleanup(idp.Close)
	e := &ssoEnv{t: t, ts: ts, d: d, idp: idp, adm: login(t, ts, "admin", "admin")}
	base := url.Values{
		"type": {"AuthSourceOidc"}, "auth_source[name]": {"Corp IdP"}, "auth_source[enabled]": {"1"},
		"auth_source[preset]": {"generic"}, "auth_source[issuer]": {idp.URL()}, "auth_source[client_id]": {idp.ClientID},
		"auth_source[client_secret]": {idp.ClientSecret}, "auth_source[scopes]": {"openid profile email"},
		"auth_source[match_by]": {"mail"}, "auth_source[skip_twofa]": {"1"},
	}
	for k, v := range form {
		base[k] = v
	}
	_, page := get(t, e.adm, ts.URL+"/auth_sources/new?type=AuthSourceOidc")
	base.Set("authenticity_token", csrfToken(t, page))
	res, body := post(t, e.adm, ts.URL+"/auth_sources", base)
	if res.StatusCode != 302 {
		t.Fatalf("create oidc source: %d\n%s", res.StatusCode, body)
	}
	if err := d.Get(context.Background(), &e.id, `SELECT CAST(id AS TEXT) FROM auth_sources WHERE kind = 'oidc'`); err != nil {
		t.Fatal(err)
	}
	return e
}

// ssoLogin は c で SSO ログインし、callback の応答を返す。
func (e *ssoEnv) ssoLogin(c *http.Client, query string) *http.Response {
	e.t.Helper()
	res, _ := get(e.t, c, e.ts.URL+"/auth/oidc/"+e.id+"/start"+query)
	if res.StatusCode != 302 || !strings.HasPrefix(res.Header.Get("Location"), e.idp.URL()+"/authorize") {
		e.t.Fatalf("start: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	res2, body := get(e.t, c, res.Header.Get("Location"))
	if res2.StatusCode != 302 {
		e.t.Fatalf("authorize: %d %s", res2.StatusCode, body)
	}
	res3, _ := get(e.t, c, res2.Header.Get("Location"))
	return res3
}

// flashAfter は Location を開いて flash（error / notice）の文言を返す。
func (e *ssoEnv) flashAfter(c *http.Client, res *http.Response) string {
	e.t.Helper()
	loc := res.Header.Get("Location")
	if !strings.HasPrefix(loc, "http") {
		loc = e.ts.URL + loc
	}
	_, body := get(e.t, c, loc)
	for _, k := range []string{"flash_error", "flash_notice", "flash_warning"} {
		if i := strings.Index(body, `id="`+k+`"`); i >= 0 {
			s := body[i:]
			s = s[strings.Index(s, ">")+1:]
			return k + ": " + stripTags(s[:strings.Index(s, "</div>")])
		}
	}
	return ""
}

func metaCSRF(t *testing.T, body string) string {
	t.Helper()
	m := authMetaCSRF.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("csrf meta not found")
	}
	return m[1]
}

func stripTags(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

func (e *ssoEnv) currentUser(c *http.Client) string {
	e.t.Helper()
	_, body := get(e.t, c, e.ts.URL+"/my/account")
	i := strings.Index(body, `id="loggedas"`)
	if i < 0 {
		return ""
	}
	f := strings.Fields(stripTags(body[i+len(`id="loggedas">`) : i+strings.Index(body[i:], "</div>")]))
	if len(f) == 0 {
		return ""
	}
	// 言語によらずログイン ID（最後の語）で比べる
	return "Logged in as " + f[len(f)-1]
}

func TestOIDCAdminPages(t *testing.T) {
	e := newSSOEnv(t, nil)
	_, body := get(t, e.adm, e.ts.URL+"/auth_sources")
	if !strings.Contains(body, `href="/auth_sources/`+e.id+`/edit">Corp IdP</a>`) || !strings.Contains(body, "<td>OIDC</td>") ||
		!strings.Contains(body, `href="/auth_sources/new?type=AuthSourceOidc"`) {
		t.Fatalf("index:\n%s", body)
	}
	res, body := get(t, e.adm, e.ts.URL+"/auth_sources/"+e.id+"/edit")
	if res.StatusCode != 200 || !strings.Contains(body, `value="`+e.idp.URL()+`"`) || !strings.Contains(body, "/auth/oidc/"+e.id+"/callback") ||
		!strings.Contains(body, `value="xxxxxxxxxxxxxxx"`) || strings.Contains(body, e.idp.ClientSecret) {
		t.Fatalf("edit: %d\n%s", res.StatusCode, body)
	}
	// 検証エラー
	res, body = post(t, e.adm, e.ts.URL+"/auth_sources/"+e.id, url.Values{"_method": {"patch"}, "authenticity_token": {csrfToken(t, body)},
		"auth_source[name]": {""}, "auth_source[client_id]": {""}, "auth_source[issuer]": {"ftp://x"}})
	if res.StatusCode != 200 || !strings.Contains(body, `errorExplanation`) || !strings.Contains(body, "Client ID cannot be blank") ||
		!strings.Contains(body, "Issuer URL is invalid") {
		t.Fatalf("update invalid: %d\n%s", res.StatusCode, body)
	}
	// 接続テスト（ディスカバリ）
	res, _ = get(t, e.adm, e.ts.URL+"/auth_sources/"+e.id+"/test_connection")
	if msg := e.flashAfter(e.adm, res); !strings.Contains(msg, "Successful connection") {
		t.Fatalf("test connection: %q", msg)
	}
	// client_secret は暗号化して保存する
	var secret string
	if err := e.d.Get(context.Background(), &secret, `SELECT secret FROM auth_sources WHERE id = ?`, e.id); err != nil || !strings.HasPrefix(secret, "sb1:") {
		t.Fatalf("secret not sealed: %q %v", secret, err)
	}
	// グループ対応表の保存
	_, body = get(t, e.adm, e.ts.URL+"/auth_sources/"+e.id+"/edit")
	res, _ = post(t, e.adm, e.ts.URL+"/auth_sources/"+e.id, url.Values{"_method": {"patch"}, "authenticity_token": {csrfToken(t, body)},
		"auth_source[group_sync]": {"1"}, "group_mapping_external[]": {"idp-a", "", "idp-b"}, "group_mapping_group_id[]": {"10", "", "11"}})
	if res.StatusCode != 302 {
		t.Fatalf("update mappings: %d", res.StatusCode)
	}
	var n int
	_ = e.d.Get(context.Background(), &n, `SELECT COUNT(*) FROM auth_source_group_mappings WHERE auth_source_id = ?`, e.id)
	if n != 2 {
		t.Fatalf("mappings: %d", n)
	}
	_, body = get(t, e.adm, e.ts.URL+"/auth_sources/"+e.id+"/edit")
	if !strings.Contains(body, `value="idp-a"`) || !strings.Contains(body, `<option selected="selected" value="11">B Team</option>`) {
		t.Fatalf("edit mappings:\n%s", body)
	}
}

func TestOIDCLoginLinkAndCreate(t *testing.T) {
	e := newSSOEnv(t, nil)
	// ログイン画面にボタンが出る（パスワードのフォームはそのまま）
	_, body := get(t, newClient(t), e.ts.URL+"/login?back_url=%2Fprojects")
	if !strings.Contains(body, `id="login-submit"`) || !strings.Contains(body, `href="/auth/oidc/`+e.id+`/start?back_url=%2Fprojects">Log in with Corp IdP</a>`) {
		t.Fatalf("login page:\n%s", body)
	}
	// 初回: メールアドレスで既存ユーザー（jsmith）に紐付ける
	e.idp.SetClaims(map[string]any{"sub": "sub-jsmith", "email": "JSmith@somenet.foo", "preferred_username": "john", "given_name": "John", "family_name": "Smith"})
	c := newClient(t)
	res := e.ssoLogin(c, "?back_url=%2Fprojects")
	if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/projects") {
		t.Fatalf("callback: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if u := e.currentUser(c); u != "Logged in as jsmith" {
		t.Fatalf("current user %q", u)
	}
	// 認可リクエストに PKCE（S256）・state・nonce が付く
	q := e.idp.LastAuthorize
	if q.Get("code_challenge_method") != "S256" || q.Get("state") == "" || q.Get("nonce") == "" || q.Get("redirect_uri") != e.ts.URL+"/auth/oidc/"+e.id+"/callback" {
		t.Fatalf("authorize query: %v", q)
	}
	// 2 回目以降は subject で解決する（メールが変わっても同じユーザー）
	e.idp.SetClaims(map[string]any{"sub": "sub-jsmith", "email": "changed@example.net"})
	c2 := newClient(t)
	if res := e.ssoLogin(c2, ""); res.StatusCode != 302 || e.currentUser(c2) != "Logged in as jsmith" {
		t.Fatalf("second login: %d %q", res.StatusCode, e.currentUser(c2))
	}
	// 未登録のユーザーはオンザフライ登録が無効なら拒否
	e.idp.SetClaims(map[string]any{"sub": "sub-new", "email": "newbie@example.net", "preferred_username": "newbie", "given_name": "New", "family_name": "Bie"})
	c3 := newClient(t)
	res = e.ssoLogin(c3, "")
	if msg := e.flashAfter(c3, res); !strings.Contains(msg, "No account is linked") {
		t.Fatalf("no account: %q", msg)
	}
	// オンザフライ登録を有効にすると作成する（認証方式はこの OIDC。パスワードでログインできない）
	_, page := get(t, e.adm, e.ts.URL+"/auth_sources/"+e.id+"/edit")
	post(t, e.adm, e.ts.URL+"/auth_sources/"+e.id, url.Values{"_method": {"patch"}, "authenticity_token": {csrfToken(t, page)}, "auth_source[onthefly_register]": {"1"}})
	c4 := newClient(t)
	if res := e.ssoLogin(c4, ""); res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/my/page") {
		t.Fatalf("create: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if u := e.currentUser(c4); u != "Logged in as newbie" {
		t.Fatalf("created user: %q", u)
	}
	var src, mail string
	if err := e.d.Get(context.Background(), &src, `SELECT CAST(ua.auth_source_id AS TEXT) FROM user_accounts ua WHERE ua.login = 'newbie'`); err != nil || src != e.id {
		t.Fatalf("auth_source_id %q %v", src, err)
	}
	_ = e.d.Get(context.Background(), &mail, `SELECT address FROM email_addresses e JOIN user_accounts ua ON ua.principal_id = e.user_id WHERE ua.login = 'newbie'`)
	if mail != "newbie@example.net" {
		t.Fatalf("mail %q", mail)
	}
	if _, res2, _ := tryLogin(t, e.ts.URL, "newbie", "anything"); res2.StatusCode != 200 {
		t.Fatalf("password login of SSO user should fail: %d", res2.StatusCode)
	}
	// 作成できない（ログイン ID の形式が不正）場合はエラー
	e.idp.SetClaims(map[string]any{"sub": "sub-bad", "email": "bad@example.net", "preferred_username": "bad user!"})
	c5 := newClient(t)
	if msg := e.flashAfter(c5, e.ssoLogin(c5, "")); !strings.Contains(msg, "could not be created") {
		t.Fatalf("creation failure: %q", msg)
	}
	// 既に同じ認証方式の別の ID と連携しているユーザーにはメール一致でも紐付けない
	e.idp.SetClaims(map[string]any{"sub": "sub-other", "email": "jsmith@somenet.foo"})
	c6 := newClient(t)
	if msg := e.flashAfter(c6, e.ssoLogin(c6, "")); !strings.Contains(msg, "already linked") {
		t.Fatalf("already linked: %q", msg)
	}
}

func TestOIDCGroupSyncAndRestrictions(t *testing.T) {
	e := newSSOEnv(t, url.Values{"auth_source[group_sync]": {"1"}, "group_mapping_external[]": {"grp-a", "grp-b"}, "group_mapping_group_id[]": {"10", "11"},
		"auth_source[allowed_tenants]": {"tenant-1\ntenant-2"}, "auth_source[allowed_groups]": {"grp-a\nstaff"}})
	groups := func() string {
		var ids []string
		_ = e.d.Select(context.Background(), &ids, `SELECT CAST(group_id AS TEXT) FROM group_users WHERE user_id = 8 ORDER BY group_id`)
		return strings.Join(ids, ",")
	}
	if g := groups(); g != "10,11" {
		t.Fatalf("fixture groups: %s", g)
	}
	// miscuser8 (id 8) は A Team / B Team に所属。groups クレームに grp-a だけ → B Team から外れる
	e.idp.SetClaims(map[string]any{"sub": "u8", "email": "miscuser8@foo.bar", "tid": "tenant-1", "groups": []any{"grp-a", "unrelated"}})
	c := newClient(t)
	if res := e.ssoLogin(c, ""); res.StatusCode != 302 || e.currentUser(c) != "Logged in as miscuser8" {
		t.Fatalf("login: %d %q", res.StatusCode, e.currentUser(c))
	}
	if g := groups(); g != "10" {
		t.Fatalf("after sync: %s", g)
	}
	// grp-b が増えれば追加（staff は許可グループだが対応表に無いので所属は変えない）
	e.idp.SetClaims(map[string]any{"sub": "u8", "email": "miscuser8@foo.bar", "tid": "tenant-2", "groups": []any{"staff", "grp-b"}})
	if res := e.ssoLogin(newClient(t), ""); res.StatusCode != 302 {
		t.Fatalf("login 2: %d", res.StatusCode)
	}
	if g := groups(); g != "11" {
		t.Fatalf("after sync 2: %s", g)
	}
	// テナント・グループの制限
	e.idp.SetClaims(map[string]any{"sub": "u8", "tid": "evil", "groups": []any{"grp-a"}})
	c2 := newClient(t)
	if msg := e.flashAfter(c2, e.ssoLogin(c2, "")); !strings.Contains(msg, "organization is not allowed") {
		t.Fatalf("tenant: %q", msg)
	}
	e.idp.SetClaims(map[string]any{"sub": "u8", "tid": "tenant-1", "groups": []any{"other"}})
	c3 := newClient(t)
	if msg := e.flashAfter(c3, e.ssoLogin(c3, "")); !strings.Contains(msg, "not a member of a group") {
		t.Fatalf("group: %q", msg)
	}
	if g := groups(); g != "11" {
		t.Fatalf("denied login must not sync groups: %s", g)
	}
}

func TestOIDCProtocolFailures(t *testing.T) {
	e := newSSOEnv(t, nil)
	e.idp.SetClaims(map[string]any{"sub": "sub-jsmith", "email": "jsmith@somenet.foo"})
	expect := func(name, want string, setup func(), undo func()) {
		t.Helper()
		setup()
		defer undo()
		c := newClient(t)
		msg := e.flashAfter(c, e.ssoLogin(c, ""))
		if !strings.Contains(msg, want) || e.currentUser(c) != "" {
			t.Fatalf("%s: %q (user %q)", name, msg, e.currentUser(c))
		}
	}
	expect("nonce", "nonce mismatch", func() { e.idp.TamperNonce = true }, func() { e.idp.TamperNonce = false })
	expect("audience", "ID token is invalid", func() { e.idp.TamperAudience = true }, func() { e.idp.TamperAudience = false })
	expect("signature", "ID token is invalid", func() { e.idp.SignWithOtherKey = true }, func() { e.idp.SignWithOtherKey = false })
	expect("no id_token", "Could not obtain a token", func() { e.idp.OmitIDToken = true }, func() { e.idp.OmitIDToken = false })
	expect("issuer", "ID token is invalid", func() { e.idp.Issuer = "https://evil.example" }, func() { e.idp.Issuer = "" })

	// state の不一致・セッションに要求が無い
	c := newClient(t)
	res, _ := get(t, c, e.ts.URL+"/auth/oidc/"+e.id+"/start")
	res2, _ := get(t, c, res.Header.Get("Location"))
	cb, _ := url.Parse(res2.Header.Get("Location"))
	q := cb.Query()
	q.Set("state", "forged")
	cb.RawQuery = q.Encode()
	res3, _ := get(t, c, cb.String())
	if msg := e.flashAfter(c, res3); !strings.Contains(msg, "invalid or has expired") {
		t.Fatalf("state: %q", msg)
	}
	res4, _ := get(t, newClient(t), cb.String())
	if res4.StatusCode != 302 || !strings.HasSuffix(res4.Header.Get("Location"), "/login") {
		t.Fatalf("no session: %d", res4.StatusCode)
	}
	// PKCE: 別のセッションの認可コードを使うと code_verifier が一致せずトークン交換に失敗する
	ca, cbc := newClient(t), newClient(t)
	ra, _ := get(t, ca, e.ts.URL+"/auth/oidc/"+e.id+"/start")
	ra2, _ := get(t, ca, ra.Header.Get("Location"))
	codeURL, _ := url.Parse(ra2.Header.Get("Location"))
	rb, _ := get(t, cbc, e.ts.URL+"/auth/oidc/"+e.id+"/start")
	rbLoc, _ := url.Parse(rb.Header.Get("Location"))
	cq := codeURL.Query()
	cq.Set("state", rbLoc.Query().Get("state"))
	codeURL.RawQuery = cq.Encode()
	rb3, _ := get(t, cbc, codeURL.String())
	if msg := e.flashAfter(cbc, rb3); !strings.Contains(msg, "Could not obtain a token") {
		t.Fatalf("pkce: %q", msg)
	}
	// IdP のエラー応答
	cc := newClient(t)
	rc, _ := get(t, cc, e.ts.URL+"/auth/oidc/"+e.id+"/start")
	rcLoc, _ := url.Parse(rc.Header.Get("Location"))
	rc2, _ := get(t, cc, e.ts.URL+"/auth/oidc/"+e.id+"/callback?error=access_denied&state="+rcLoc.Query().Get("state"))
	if msg := e.flashAfter(cc, rc2); !strings.Contains(msg, "returned an error") {
		t.Fatalf("idp error: %q", msg)
	}
	// 存在しない・無効な認証方式は 404
	if res, _ := get(t, newClient(t), e.ts.URL+"/auth/oidc/999/start"); res.StatusCode != 404 {
		t.Fatalf("missing source: %d", res.StatusCode)
	}
}

func TestOIDCTwofaRequiredAndSSORequired(t *testing.T) {
	e := newSSOEnv(t, url.Values{"auth_source[skip_twofa]": {"0"}, "auth_source[sso_required]": {"1"}, "auth_source[rp_logout]": {"1"}})
	ctx := context.Background()
	// jsmith は 2 要素認証が有効（skip_twofa が無効なので SSO の後に OTP を求める）
	key := totp.RandomKey()
	if _, err := e.d.Exec(ctx, `UPDATE user_accounts SET twofa_scheme = 'totp', twofa_totp_key = ? WHERE principal_id = 2`, key); err != nil {
		t.Fatal(err)
	}
	e.idp.SetClaims(map[string]any{"sub": "sub-jsmith", "email": "jsmith@somenet.foo"})
	c := newClient(t)
	res := e.ssoLogin(c, "")
	if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/account/twofa/confirm") {
		t.Fatalf("twofa after sso: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	_, page := get(t, c, e.ts.URL+"/account/twofa/confirm")
	res, _ = post(t, c, e.ts.URL+"/account/twofa", url.Values{"authenticity_token": {csrfToken(t, page)}, "twofa_code": {totp.Now(key, frozenTime)}})
	if res.StatusCode != 302 || e.currentUser(c) != "Logged in as jsmith" {
		t.Fatalf("twofa confirm: %d %q", res.StatusCode, e.currentUser(c))
	}

	// SSO 必須: ログイン画面はボタンとローカルログインのリンクだけ
	_, body := get(t, newClient(t), e.ts.URL+"/login")
	if strings.Contains(body, `id="login-submit"`) || !strings.Contains(body, `href="/login?local=1"`) || !strings.Contains(body, "sso-login-button") {
		t.Fatalf("sso required login page:\n%s", body)
	}
	_, body = get(t, newClient(t), e.ts.URL+"/login?local=1")
	if !strings.Contains(body, `id="login-submit"`) {
		t.Fatal("local login form missing")
	}
	// 管理者以外のパスワードログインは拒否、管理者は可
	localLogin := func(user, pw string) (*http.Response, string) {
		c := newClient(t)
		_, page := get(t, c, e.ts.URL+"/login?local=1")
		return post(t, c, e.ts.URL+"/login", url.Values{"authenticity_token": {csrfToken(t, page)}, "username": {user}, "password": {pw}})
	}
	res2, body2 := localLogin("dlopper", "foo")
	if res2.StatusCode != 200 || !strings.Contains(body2, "Password login is disabled") || !strings.Contains(body2, `id="login-submit"`) {
		t.Fatalf("non-admin password login: %d", res2.StatusCode)
	}
	if res3, _ := localLogin("admin", "admin"); res3.StatusCode != 302 {
		t.Fatalf("admin password login: %d", res3.StatusCode)
	}

	// RP-Initiated Logout: SSO でログインしたセッションのログアウトは IdP の end_session_endpoint へ
	if _, err := e.d.Exec(ctx, `UPDATE user_accounts SET twofa_scheme = NULL WHERE principal_id = 2`); err != nil {
		t.Fatal(err)
	}
	c2 := newClient(t)
	e.ssoLogin(c2, "")
	if u := e.currentUser(c2); u != "Logged in as jsmith" {
		t.Fatalf("sso login for logout: %q", u)
	}
	_, page = get(t, c2, e.ts.URL+"/logout")
	res, _ = post(t, c2, e.ts.URL+"/logout", url.Values{"authenticity_token": {metaCSRF(t, page)}})
	loc := res.Header.Get("Location")
	if res.StatusCode != 302 || !strings.HasPrefix(loc, e.idp.URL()+"/logout?") || !strings.Contains(loc, "id_token_hint=") ||
		!strings.Contains(loc, "post_logout_redirect_uri="+url.QueryEscape(e.ts.URL+"/")) {
		t.Fatalf("rp logout: %d %s", res.StatusCode, loc)
	}
	if e.currentUser(c2) != "" {
		t.Fatal("still logged in after logout")
	}
}

func TestOIDCMyIdentities(t *testing.T) {
	e := newSSOEnv(t, nil)
	c := login(t, e.ts, "dlopper", "foo")
	// サイドバーにリンク、一覧は空
	_, body := get(t, c, e.ts.URL+"/my/account")
	if !strings.Contains(body, `href="/my/sso">Linked accounts</a>`) {
		t.Fatal("sidebar link missing")
	}
	_, body = get(t, c, e.ts.URL+"/my/sso")
	if !strings.Contains(body, `href="/auth/oidc/`+e.id+`/start?mode=link"`) {
		t.Fatalf("link button missing:\n%s", body)
	}
	// 連携（ログイン中のユーザーに紐付ける。メールは一致しなくてよい）
	e.idp.SetClaims(map[string]any{"sub": "dl-sub", "email": "someone-else@example.net"})
	res := e.ssoLogin(c, "?mode=link")
	if msg := e.flashAfter(c, res); !strings.Contains(msg, "successfully linked") {
		t.Fatalf("link: %q", msg)
	}
	_, body = get(t, c, e.ts.URL+"/my/sso")
	if !strings.Contains(body, "<td class=\"name\">Corp IdP</td>") || !strings.Contains(body, "someone-else@example.net") {
		t.Fatalf("identity list:\n%s", body)
	}
	// 連携した ID でログインできる
	c2 := newClient(t)
	if e.ssoLogin(c2, ""); e.currentUser(c2) != "Logged in as dlopper" {
		t.Fatalf("login with linked identity: %q", e.currentUser(c2))
	}
	// 他人の ID は連携できない
	c3 := login(t, e.ts, "jsmith", "jsmith")
	if msg := e.flashAfter(c3, e.ssoLogin(c3, "?mode=link")); !strings.Contains(msg, "already linked to another account") {
		t.Fatalf("taken: %q", msg)
	}
	// 連携解除（パスワードでログインできるユーザーは最後の 1 件も解除できる）
	var id string
	_ = e.d.Get(context.Background(), &id, `SELECT CAST(id AS TEXT) FROM user_identities WHERE user_id = 3`)
	_, page := get(t, c, e.ts.URL+"/my/sso")
	res, _ = post(t, c, e.ts.URL+"/my/sso/"+id, url.Values{"_method": {"delete"}, "authenticity_token": {metaCSRF(t, page)}})
	if msg := e.flashAfter(c, res); !strings.Contains(msg, "successfully unlinked") {
		t.Fatalf("unlink: %q", msg)
	}
	// 他人の連携は 404
	if res, _ := post(t, c, e.ts.URL+"/my/sso/99999", url.Values{"_method": {"delete"}, "authenticity_token": {metaCSRF(t, page)}}); res.StatusCode != 404 {
		t.Fatalf("unlink others: %d", res.StatusCode)
	}
}

func TestOIDCEntraPresetForm(t *testing.T) {
	ts, _ := newFixtureServer(t)
	adm := login(t, ts, "admin", "admin")
	_, page := get(t, adm, ts.URL+"/auth_sources/new?type=AuthSourceOidc")
	if !strings.Contains(page, `<option value="entra">Microsoft Entra ID</option>`) {
		t.Fatal("entra preset option missing")
	}
	// Entra ID はテナント（または issuer）が必須
	res, body := post(t, adm, ts.URL+"/auth_sources", url.Values{"authenticity_token": {csrfToken(t, page)}, "type": {"AuthSourceOidc"},
		"auth_source[name]": {"Entra"}, "auth_source[preset]": {"entra"}, "auth_source[client_id]": {"abc"}})
	if res.StatusCode != 200 || !strings.Contains(body, "Tenant cannot be blank") {
		t.Fatalf("entra validation: %d\n%s", res.StatusCode, body)
	}
	res, _ = post(t, adm, ts.URL+"/auth_sources", url.Values{"authenticity_token": {csrfToken(t, page)}, "type": {"AuthSourceOidc"},
		"auth_source[name]": {"Entra"}, "auth_source[preset]": {"entra"}, "auth_source[tenant]": {"contoso.onmicrosoft.com"}, "auth_source[client_id]": {"abc"}})
	if res.StatusCode != 302 {
		t.Fatalf("entra create: %d", res.StatusCode)
	}
}

// TestOIDCSudoReauth は sudo モードのパスワード再入力の代わりに IdP での再認証（prompt=login）で sudo を有効にできることを確認する。
func TestOIDCSudoReauth(t *testing.T) {
	var app *handler.App
	e := newSSOEnv(t, nil, func(a *handler.App, _ chi.Router) { a.SudoMode = true; app = a })
	if err := app.Settings.Set(context.Background(), "autologin", "7"); err != nil {
		t.Fatal(err)
	}
	// dlopper の外部 ID を連携しておく
	if _, err := e.d.Exec(context.Background(), `INSERT INTO user_identities (user_id, provider, auth_source_id, subject, created_at) VALUES (3, ?, ?, 'dl-sub', ?)`,
		"oidc:"+e.id, e.id, db.NewTime(time.Now())); err != nil {
		t.Fatal(err)
	}
	// 自動ログインで始まったセッションは sudo の時刻を持たない
	tmp := newClient(t)
	_, page := get(t, tmp, e.ts.URL+"/login")
	post(t, tmp, e.ts.URL+"/login", url.Values{"authenticity_token": {csrfToken(t, page)}, "username": {"dlopper"}, "password": {"foo"}, "autologin": {"1"}})
	u, _ := url.Parse(e.ts.URL)
	c := newClient(t)
	for _, ck := range tmp.Jar.Cookies(u) {
		if ck.Name == "autologin" {
			c.Jar.SetCookies(u, []*http.Cookie{ck})
		}
	}
	_, body := get(t, c, e.ts.URL+"/my/account/destroy")
	if !strings.Contains(body, "sudo-form") || !strings.Contains(body, "/auth/oidc/"+e.id+"/start?mode=sudo&amp;back_url=%2Fmy%2Faccount%2Fdestroy") {
		t.Fatalf("sudo form with re-auth button:\n%s", body)
	}
	// IdP が再認証せずに（auth_time が無い・古い）応答した場合は sudo にしない
	// 認可リクエストを始める数分前の IdP ログイン（既存の IdP セッション）も再認証とみなさない
	for _, claims := range []map[string]any{{"sub": "dl-sub"}, {"sub": "dl-sub", "auth_time": frozenTime.Add(-time.Hour).Unix()},
		{"sub": "dl-sub", "auth_time": frozenTime.Add(-5 * time.Minute).Unix()}, {"sub": "dl-sub", "auth_time": frozenTime.Add(time.Hour).Unix()}} {
		e.idp.SetClaims(claims)
		e.ssoLogin(c, "?mode=sudo&back_url=%2Fmy%2Faccount%2Fdestroy")
		if _, body := get(t, c, e.ts.URL+"/my/account/destroy"); !strings.Contains(body, "sudo-form") {
			t.Fatalf("sudo granted without re-authentication (%v)", claims)
		}
	}
	e.idp.SetClaims(map[string]any{"sub": "dl-sub", "auth_time": frozenTime.Unix()})
	res := e.ssoLogin(c, "?mode=sudo&back_url=%2Fmy%2Faccount%2Fdestroy")
	if !strings.HasSuffix(res.Header.Get("Location"), "/my/account/destroy") {
		t.Fatalf("sudo reauth redirect: %s", res.Header.Get("Location"))
	}
	if e.idp.LastAuthorize.Get("prompt") != "login" {
		t.Fatalf("prompt: %v", e.idp.LastAuthorize)
	}
	_, body = get(t, c, e.ts.URL+"/my/account/destroy")
	if strings.Contains(body, "sudo-form") {
		t.Fatal("sudo should be active after re-authentication")
	}
}

// メール一致での初回紐付けは、IdP が email_verified=false を返したメールでは行わない
// （未検証のメールアドレスを名乗るだけで既存アカウントを乗っ取れないようにする）。
func TestOIDCUnverifiedEmailIsNotMatched(t *testing.T) {
	e := newSSOEnv(t, nil)
	for _, v := range []any{false, "false"} {
		e.idp.SetClaims(map[string]any{"sub": "sub-attacker", "email": "jsmith@somenet.foo", "email_verified": v})
		c := newClient(t)
		if msg := e.flashAfter(c, e.ssoLogin(c, "")); !strings.Contains(msg, "No account is linked") {
			t.Fatalf("email_verified=%v: %q", v, msg)
		}
		if u := e.currentUser(c); u == "Logged in as jsmith" {
			t.Fatalf("email_verified=%v: logged in as jsmith", v)
		}
	}
	// email_verified=true（または無し）は従来どおり紐付ける
	e.idp.SetClaims(map[string]any{"sub": "sub-jsmith", "email": "jsmith@somenet.foo", "email_verified": true})
	c := newClient(t)
	if res := e.ssoLogin(c, ""); res.StatusCode != 302 || e.currentUser(c) != "Logged in as jsmith" {
		t.Fatalf("verified: %d %q", res.StatusCode, e.currentUser(c))
	}
}
