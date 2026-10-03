package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/auth/doorkeeper"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/handler"
)

// OAuth2 プロバイダ（Doorkeeper 互換）のテスト。画面の期待値（testdata/oauth/*.html）は参照 Redmine
// （tools/compat の専用インスタンス）から `compat fetch -raw` で取得し、normalizeFixture と同じ置換に加えて
// labelled_form_for の乱数付き form name を伏せたもの。網羅的な比較は testdata/compat/scenarios/oauth2*.yml。

func compareOAuthGolden(t *testing.T, name, body, base string) {
	t.Helper()
	compareGolden(t, "oauth/"+name, namedFormRe.ReplaceAllString(normalizeFixture(body, base), `name="$1-RANDOM"`))
}

func TestOAuthPagesMatchRedmine(t *testing.T) {
	ts, _ := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	jsmith := login(t, ts, "jsmith", "jsmith")
	cases := []struct {
		name   string
		client *http.Client
		path   string
		status int
	}{
		{"applications_new_admin.html", admin, "/oauth/applications/new", 200},
		{"authorized_applications_jsmith.html", jsmith, "/oauth/authorized_applications", 200},
		{"authorize_bad_client_jsmith.html", jsmith, "/oauth/authorize?client_id=unknown", 401},
		{"authorize_native_jsmith.html", jsmith, "/oauth/authorize/native?code=abc", 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, body := get(t, c.client, ts.URL+c.path)
			if res.StatusCode != c.status {
				t.Fatalf("status %d, want %d", res.StatusCode, c.status)
			}
			compareOAuthGolden(t, c.name, body, ts.URL)
		})
	}
}

// oauthDo はリクエストを送り、ステータス・ヘッダー・本文を返す。
func oauthDo(t *testing.T, c *http.Client, method, u string, form url.Values, header map[string]string) (*http.Response, string) {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := readUnbranded(res.Body)
	return res, string(b)
}

func bearer(tok string) map[string]string { return map[string]string{"Authorization": "Bearer " + tok} }

func decodeJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("invalid json %q: %v", s, err)
	}
	return m
}

var (
	oauthUIDRe    = regexp.MustCompile(`UID:</span>\s*<code>([A-Za-z0-9_-]{43})</code>`)
	oauthSecretRe = regexp.MustCompile(`Secret:</span>\s*<code>\s*([A-Za-z0-9_-]{43})`)
	oauthCodeRe   = regexp.MustCompile(`[?&]code=([A-Za-z0-9_-]{43})`)
)

// createOAuthApp は管理画面からアプリケーションを作り、id・UID・平文のシークレットを返す。
func createOAuthApp(t *testing.T, ts string, admin *http.Client, name string, scopes ...string) (string, string, string) {
	t.Helper()
	_, page := get(t, admin, ts+"/oauth/applications/new")
	form := url.Values{"authenticity_token": {csrfMeta(t, page)}, "doorkeeper_application[name]": {name},
		"doorkeeper_application[redirect_uri]": {"http://127.0.0.1:12345/cb\nurn:ietf:wg:oauth:2.0:oob"}}
	form["doorkeeper_application[scopes][]"] = append(scopes, "")
	res, _ := post(t, admin, ts+"/oauth/applications", form)
	if res.StatusCode != http.StatusFound {
		t.Fatalf("create: status %d", res.StatusCode)
	}
	loc := res.Header.Get("Location")
	id := loc[strings.LastIndex(loc, "/")+1:]
	_, show := get(t, admin, loc)
	uid, secret := oauthUIDRe.FindStringSubmatch(show), oauthSecretRe.FindStringSubmatch(show)
	if uid == nil || secret == nil {
		t.Fatalf("uid/secret not shown:\n%s", extract(show, `<div class="box">`, `</div>`))
	}
	// シークレットは作成直後の 1 回だけ表示される
	if _, again := get(t, admin, loc); !strings.Contains(again, "Secret hashed") || oauthSecretRe.MatchString(again) {
		t.Error("secret shown again")
	}
	return id, uid[1], secret[1]
}

// authorizeCode は jsmith 等で同意して認可コードを得る（PKCE S256）。
func authorizeCode(t *testing.T, ts string, user *http.Client, uid, scope string) string {
	t.Helper()
	q := url.Values{"client_id": {uid}, "redirect_uri": {"http://127.0.0.1:12345/cb"}, "response_type": {"code"}, "scope": {scope},
		"state": {"xyz"}, "code_challenge": {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"}, "code_challenge_method": {"S256"}}
	res, page := get(t, user, ts+"/oauth/authorize?"+q.Encode())
	if res.StatusCode != 200 || !strings.Contains(page, "Authorization required") {
		t.Fatalf("consent page: status %d\n%s", res.StatusCode, extract(page, `<div id="content">`, `</div>`))
	}
	q.Set("authenticity_token", csrfMeta(t, page))
	res, _ = post(t, user, ts+"/oauth/authorize", q)
	loc := res.Header.Get("Location")
	m := oauthCodeRe.FindStringSubmatch(loc)
	if res.StatusCode != http.StatusFound || m == nil || !strings.HasPrefix(loc, "http://127.0.0.1:12345/cb?code=") || !strings.HasSuffix(loc, "&state=xyz") {
		t.Fatalf("approve: status %d location %q", res.StatusCode, loc)
	}
	return m[1]
}

func exchangeCode(t *testing.T, ts, uid, secret, code string) map[string]any {
	t.Helper()
	res, body := oauthDo(t, newClient(t), http.MethodPost, ts+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {"http://127.0.0.1:12345/cb"}, "client_id": {uid}, "client_secret": {secret},
		"code_verifier": {"dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"}}, nil)
	if res.StatusCode != 200 {
		t.Fatalf("token: status %d %s", res.StatusCode, body)
	}
	if res.Header.Get("Cache-Control") != "no-store" || res.Header.Get("Pragma") != "no-cache" {
		t.Errorf("token headers: %v", res.Header)
	}
	return decodeJSON(t, body)
}

// TestOAuthFlow は 認可 → トークン → API（スコープの制限）→ リフレッシュ → 失効 の一連の流れを確認する。
func TestOAuthFlow(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	admin := login(t, ts, "admin", "admin")
	jsmith := login(t, ts, "jsmith", "jsmith")
	appID, uid, secret := createOAuthApp(t, ts.URL, admin, "Flow App", "view_issues", "add_issues")

	// DB の保存形式は Redmine（hash_application_secrets BCrypt / hash_token_secrets SHA-256）と同じ
	var stored string
	if err := d.Get(ctx, &stored, `SELECT secret FROM oauth_applications WHERE uid = ?`, uid); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stored, "$2a$12$") || !doorkeeper.SecretMatches(secret, stored) {
		t.Errorf("stored secret %q", stored)
	}

	code := authorizeCode(t, ts.URL, jsmith, uid, "view_issues view_project")
	var grantToken string
	if err := d.Get(ctx, &grantToken, `SELECT token FROM oauth_access_grants`); err != nil {
		t.Fatal(err)
	}
	if grantToken != doorkeeper.HashToken(code) {
		t.Error("grant token is not stored as SHA-256")
	}

	tok := exchangeCode(t, ts.URL, uid, secret, code)
	access, _ := tok["access_token"].(string)
	refresh, _ := tok["refresh_token"].(string)
	if len(access) != 43 || len(refresh) != 43 || tok["token_type"] != "Bearer" || tok["expires_in"] != float64(7200) ||
		tok["scope"] != "view_issues view_project" || tok["created_at"] != float64(frozenTime.Unix()) {
		t.Fatalf("token response %v", tok)
	}
	// 認可コードは 1 回しか使えない
	res, body := oauthDo(t, newClient(t), http.MethodPost, ts.URL+"/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {"http://127.0.0.1:12345/cb"}, "client_id": {uid}, "client_secret": {secret},
		"code_verifier": {"dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"}}, nil)
	if res.StatusCode != 400 || decodeJSON(t, body)["error"] != "invalid_grant" {
		t.Errorf("code reuse: %d %s", res.StatusCode, body)
	}

	anon := newClient(t)
	// スコープ内（view_issues）は許可、スコープ外（view_time_entries）は 403
	if res, _ := oauthDo(t, anon, http.MethodGet, ts.URL+"/projects/onlinestore/issues.json", nil, bearer(access)); res.StatusCode != 200 {
		t.Errorf("issues.json with token: %d", res.StatusCode)
	}
	if res, _ := oauthDo(t, anon, http.MethodGet, ts.URL+"/projects/onlinestore/issues.json", nil, nil); res.StatusCode != 401 {
		t.Errorf("issues.json without token: %d", res.StatusCode)
	}
	if res, _ := oauthDo(t, anon, http.MethodGet, ts.URL+"/projects/ecookbook/time_entries.json", nil, bearer(access)); res.StatusCode != 403 {
		t.Errorf("time_entries.json out of scope: %d", res.StatusCode)
	}
	res, body = oauthDo(t, anon, http.MethodGet, ts.URL+"/users/current.json", nil, bearer(access))
	if res.StatusCode != 200 || !strings.Contains(body, `"login":"jsmith"`) {
		t.Errorf("users/current.json: %d %s", res.StatusCode, body)
	}
	if res, _ := oauthDo(t, anon, http.MethodGet, ts.URL+"/issues/1.json?access_token="+url.QueryEscape(access), nil, nil); res.StatusCode != 200 {
		t.Errorf("access_token param: %d", res.StatusCode)
	}
	res, body = oauthDo(t, anon, http.MethodGet, ts.URL+"/oauth/token/info", nil, bearer(access))
	if res.StatusCode != 200 || body != `{"resource_owner_id":2,"scope":["view_issues","view_project"],"expires_in":7200,"application":{"uid":"`+uid+`"},"created_at":`+
		itoa(frozenTime.Unix())+`}` {
		t.Errorf("token/info: %d %s", res.StatusCode, body)
	}

	// 認可済みアプリケーションの一覧と、同じスコープでの再認可（同意画面を省略）
	if _, page := get(t, jsmith, ts.URL+"/oauth/authorized_applications"); !strings.Contains(page, `<td class="name"><span>Flow App</span></td>`) {
		t.Error("authorized application not listed")
	}
	q := url.Values{"client_id": {uid}, "redirect_uri": {"http://127.0.0.1:12345/cb"}, "response_type": {"code"}, "scope": {"view_project view_issues"}}
	if res, _ := get(t, jsmith, ts.URL+"/oauth/authorize?"+q.Encode()); res.StatusCode != 302 || !oauthCodeRe.MatchString(res.Header.Get("Location")) {
		t.Errorf("re-authorize: %d %q", res.StatusCode, res.Header.Get("Location"))
	}

	// リフレッシュ: クライアント認証が必要。新しいトークンが使われるまで旧トークンは有効
	res, body = oauthDo(t, anon, http.MethodPost, ts.URL+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}, nil)
	if res.StatusCode != 400 || decodeJSON(t, body)["error"] != "invalid_grant" {
		t.Errorf("refresh without client: %d %s", res.StatusCode, body)
	}
	res, body = oauthDo(t, anon, http.MethodPost, ts.URL+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh},
		"scope": {"view_issues"}}, map[string]string{"Authorization": "Basic " + basicAuth(uid, secret)})
	if res.StatusCode != 200 {
		t.Fatalf("refresh: %d %s", res.StatusCode, body)
	}
	tok2 := decodeJSON(t, body)
	access2, _ := tok2["access_token"].(string)
	if tok2["scope"] != "view_issues" || access2 == access {
		t.Fatalf("refresh response %v", tok2)
	}
	if res, _ := oauthDo(t, anon, http.MethodGet, ts.URL+"/users/current.json", nil, bearer(access)); res.StatusCode != 200 {
		t.Errorf("old token before new token is used: %d", res.StatusCode)
	}
	if res, _ := oauthDo(t, anon, http.MethodGet, ts.URL+"/users/current.json", nil, bearer(access2)); res.StatusCode != 200 {
		t.Errorf("new token: %d", res.StatusCode)
	}
	res, _ = oauthDo(t, anon, http.MethodGet, ts.URL+"/users/current.json", nil, bearer(access))
	if res.StatusCode != 401 || res.Header.Get("WWW-Authenticate") != `Bearer realm="Redmine", error="invalid_token", error_description="The access token was revoked"` {
		t.Errorf("old token after new token is used: %d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}

	// 失効（RFC 7009）
	res, body = oauthDo(t, anon, http.MethodPost, ts.URL+"/oauth/revoke", url.Values{"token": {access2}}, nil)
	if res.StatusCode != 403 || decodeJSON(t, body)["error"] != "unauthorized_client" {
		t.Errorf("revoke without client: %d %s", res.StatusCode, body)
	}
	res, body = oauthDo(t, anon, http.MethodPost, ts.URL+"/oauth/revoke", url.Values{"token": {access2}, "client_id": {uid}, "client_secret": {secret}}, nil)
	if res.StatusCode != 200 || body != "{}" {
		t.Errorf("revoke: %d %s", res.StatusCode, body)
	}
	if res, _ := oauthDo(t, anon, http.MethodGet, ts.URL+"/users/current.json", nil, bearer(access2)); res.StatusCode != 401 {
		t.Errorf("revoked token: %d", res.StatusCode)
	}

	// マイアカウントからの取り消し → アプリケーションの削除
	code = authorizeCode(t, ts.URL, jsmith, uid, "view_issues")
	access3, _ := exchangeCode(t, ts.URL, uid, secret, code)["access_token"].(string)
	_, page := get(t, jsmith, ts.URL+"/oauth/authorized_applications")
	res, _ = post(t, jsmith, ts.URL+"/oauth/authorized_applications/"+appID, url.Values{"_method": {"delete"}, "authenticity_token": {csrfMeta(t, page)}})
	if res.StatusCode != 302 {
		t.Errorf("revoke authorized application: %d", res.StatusCode)
	}
	if res, _ := oauthDo(t, anon, http.MethodGet, ts.URL+"/users/current.json", nil, bearer(access3)); res.StatusCode != 401 {
		t.Errorf("token after revoking application: %d", res.StatusCode)
	}
	_, page = get(t, admin, ts.URL+"/oauth/applications")
	res, _ = post(t, admin, ts.URL+"/oauth/applications/"+appID, url.Values{"_method": {"delete"}, "authenticity_token": {csrfMeta(t, page)}})
	if res.StatusCode != 302 {
		t.Errorf("destroy: %d", res.StatusCode)
	}
	var n int
	if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM oauth_access_tokens`); err != nil || n != 0 {
		t.Errorf("tokens remain after destroy: %d %v", n, err)
	}
}

// TestOAuthAdminScope は管理者のトークンでも admin スコープが無ければ管理者として扱わないことを確認する（User#admin?）。
func TestOAuthAdminScope(t *testing.T) {
	ts, _ := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	for _, c := range []struct {
		scopes []string
		want   int
	}{
		{[]string{"view_issues"}, 403},
		{[]string{"view_issues", "admin"}, 200},
	} {
		_, uid, secret := createOAuthApp(t, ts.URL, admin, "Admin "+strings.Join(c.scopes, " "), c.scopes...)
		code := authorizeCode(t, ts.URL, admin, uid, strings.Join(c.scopes, " "))
		access, _ := exchangeCode(t, ts.URL, uid, secret, code)["access_token"].(string)
		if res, _ := oauthDo(t, newClient(t), http.MethodGet, ts.URL+"/users.json", nil, bearer(access)); res.StatusCode != c.want {
			t.Errorf("scopes %v: /users.json status %d, want %d", c.scopes, res.StatusCode, c.want)
		}
	}
}

// TestOAuthImportedToken は Redmine が保存したトークン（SHA-256）とシークレット（BCrypt）をそのまま使えることを確認する
// （redmineimport はこれらの列を変換せずに取り込む）。
func TestOAuthImportedToken(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	now := db.NewTime(frozenTime)
	appID, err := d.InsertReturningID(ctx, `INSERT INTO oauth_applications (name, uid, secret, redirect_uri, scopes, confidential, created_at, updated_at)
VALUES ('Imported', 'imported-uid', '$2a$12$IMXEuvPu3VJdFfIy7WWQu.TVu4qpnAwatXVEjE0UVYaFpxCQWYdGK', 'http://127.0.0.1:12345/cb', 'view_project view_issues', ?, ?, ?)`, true, now, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(ctx, `INSERT INTO oauth_access_tokens (resource_owner_id, application_id, token, refresh_token, expires_in, created_at, scopes, previous_refresh_token)
VALUES (2, ?, '4b8d4da385b213f25d57703f9cee7bac0c84918e80807f0b032d1b27ea2ee2de', ?, 7200, ?, 'view_issues', '')`,
		appID, doorkeeper.HashToken("imported-refresh"), now); err != nil {
		t.Fatal(err)
	}
	if res, body := oauthDo(t, newClient(t), http.MethodGet, ts.URL+"/users/current.json", nil, bearer("8_0EFTDnqUVlCUZ9J_doiYXbjW1QIL3zyyzeDqN-LHk")); res.StatusCode != 200 || !strings.Contains(body, `"login":"jsmith"`) {
		t.Errorf("imported token: %d %s", res.StatusCode, body)
	}
	res, body := oauthDo(t, newClient(t), http.MethodPost, ts.URL+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"imported-refresh"},
		"client_id": {"imported-uid"}, "client_secret": {"9ArJc1xqulN-245jG7rHZrxIzOQqQydnH5P07591V-g"}}, nil)
	if res.StatusCode != 200 || decodeJSON(t, body)["scope"] != "view_issues" {
		t.Errorf("refresh with imported secret: %d %s", res.StatusCode, body)
	}
}

// TestOAuthRestAPIDisabled は REST API が無効なら管理画面・認可画面とも拒否されることを確認する。
func TestOAuthRestAPIDisabled(t *testing.T) {
	var app *handler.App
	ts, _ := newFixtureServer(t, func(a *handler.App, _ chi.Router) { app = a })
	if err := app.Settings.Set(context.Background(), "rest_api_enabled", "0"); err != nil {
		t.Fatal(err)
	}
	admin := login(t, ts, "admin", "admin")
	for _, p := range []string{"/oauth/applications", "/oauth/authorize?client_id=x", "/oauth/authorized_applications"} {
		if res, _ := get(t, admin, ts.URL+p); res.StatusCode != 403 {
			t.Errorf("%s: status %d", p, res.StatusCode)
		}
	}
}

func basicAuth(user, pw string) string {
	req, _ := http.NewRequest(http.MethodGet, "http://x/", nil)
	req.SetBasicAuth(user, pw)
	return strings.TrimPrefix(req.Header.Get("Authorization"), "Basic ")
}
