package server_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/mikuta0407/buropher/internal/auth/totp"
	"github.com/mikuta0407/buropher/internal/db"
)

// このファイルはパスワード再発行・自己登録・2 要素認証の画面を参照 Redmine と比較するテスト。
//
// 参照・候補の双方に同じ操作列（authFlow）を流し、各ステップの応答（ステータス・Location・#main 等）を比べる。
// 状態を変えるため、ゴールデンは reset 直後の専用の参照インスタンスから取得する:
//
//	COMPAT_REF_DIR=_reference/ref-auth COMPAT_REF_PORT=4023 tools/compat/redmine-ref.sh reset
//	BUROPHER_AUTH_GOLDEN_REF=http://127.0.0.1:4023 \
//	BUROPHER_AUTH_GOLDEN_REF_DB=_reference/ref-auth/db/redmine.sqlite3 \
//	  go test -run TestAccountAuthPagesMatchRedmine ./internal/server
//
// パスワード再発行のトークンはメールで届くため、参照側は SQLite の tokens 表から、候補側は DB から読む。
// TOTP の鍵とバックアップコードは画面から読む（参照の database_cipher_key は未設定なので平文）。
// 両者とも時刻は 2026-01-15 12:00:00 UTC に固定されている。TOTP は 1 つ前のタイムステップ（11:59:30）の
// コードで有効化し、現在のステップのコードでバックアップコードを生成する（同じステップの再利用は拒否される）。
// QR コードの画像（rqrcode と boombuler/barcode でバイト列が異なる）・鍵・バックアップコードは伏せて比べる。

// authEnv は操作列を流す対象（参照または候補）。
type authEnv struct {
	t       *testing.T
	base    string
	clients map[string]*http.Client
	// recoveryToken は user_id の recovery トークンの値を返す。
	recoveryToken func(userID int) string
	out           map[string]string
	order         []string
}

var (
	authMetaCSRF   = regexp.MustCompile(`<meta name="csrf-token" content="([^"]+)"`)
	authQRRe       = regexp.MustCompile(`data:image/png;base64,[A-Za-z0-9+/=]+`)
	authTotpKeyRe  = regexp.MustCompile(`<code>((?:[A-Z2-7]{4} ){7}[A-Z2-7]{4})</code>`)
	authBackupRe   = regexp.MustCompile(`<li><code>([0-9a-f]{4} [0-9a-f]{4} [0-9a-f]{4})</code></li>`)
	authSessionRe  = regexp.MustCompile(`[0-9a-f]{40}`)
	authPasswords  = map[string]string{"admin": "admin", "jsmith": "jsmith", "dlopper": "foo", "rhill": "foo"}
)

func (e *authEnv) client(user string) *http.Client {
	c := e.clients[user]
	if c == nil {
		c = newClient(e.t)
		e.clients[user] = c
		if pw, ok := authPasswords[user]; ok {
			e.do(user, "POST", "/login", url.Values{"username": {user}, "password": {pw}}, "")
		}
	}
	return c
}

// csrf は user のセッションの CSRF トークン（/login または / のメタタグ）。
func (e *authEnv) csrf(c *http.Client) string {
	for _, p := range []string{"/login", "/"} {
		res, err := c.Get(e.base + p)
		if err != nil {
			e.t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if m := authMetaCSRF.FindStringSubmatch(string(b)); m != nil {
			return m[1]
		}
	}
	e.t.Fatal("csrf token not found")
	return ""
}

// do はリクエストを送り、golden が空でなければ応答を記録する。応答の本文を返す。
func (e *authEnv) do(user, method, path string, form url.Values, golden string) (int, string, string) {
	e.t.Helper()
	c := e.clients[user]
	if c == nil {
		c = e.client(user)
	}
	var body io.Reader
	if method == "POST" {
		if form == nil {
			form = url.Values{}
		}
		form.Set("authenticity_token", e.csrf(c))
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, e.base+path, body)
	if err != nil {
		e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	res, err := c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	loc := strings.Replace(res.Header.Get("Location"), e.base, "", 1)
	if golden != "" {
		s := asNormalize(string(b), e.base)
		s = authQRRe.ReplaceAllString(s, "data:image/png;base64,QRCODE")
		s = authTotpKeyRe.ReplaceAllString(s, "<code>TOTPKEY</code>")
		s = authBackupRe.ReplaceAllString(s, "<li><code>BACKUPCODE</code></li>")
		s = authSessionRe.ReplaceAllString(s, "TOKEN40")
		s = myTimeZoneRe.ReplaceAllString(s, "<!-- time_zone -->")
		e.out[golden] = fmt.Sprintf("status: %d\nlocation: %s\n\n%s", res.StatusCode, loc, s)
		e.order = append(e.order, golden)
	}
	return res.StatusCode, loc, string(b)
}

// authFlow は比較する操作列。
func authFlow(e *authEnv) {
	t := e.t
	// ---- パスワード再発行
	e.clients["anon"] = newClient(t)
	e.do("anon", "GET", "/account/lost_password", nil, "lost_password.html")
	e.do("anon", "POST", "/account/lost_password", url.Values{"mail": {"nobody@example.net"}}, "lost_password_unknown.html")
	e.do("anon", "GET", "/login", nil, "lost_password_unknown_next.html")
	// ロックされたユーザー（dlopper2 は status 3）
	e.do("anon", "POST", "/account/lost_password", url.Values{"mail": {"dlopper2@somenet.foo"}}, "lost_password_locked.html")
	e.do("anon", "GET", "/account/lost_password", nil, "lost_password_locked_next.html")
	e.do("anon", "POST", "/account/lost_password", url.Values{"mail": {"JSmith@somenet.foo"}}, "lost_password_sent.html")
	e.do("anon", "GET", "/login", nil, "lost_password_sent_next.html")
	tok := e.recoveryToken(2)
	if tok == "" {
		t.Fatal("recovery token not found")
	}
	e.do("anon", "GET", "/account/lost_password?token="+tok, nil, "recovery_token_redirect.html")
	e.do("anon", "GET", "/account/lost_password", nil, "password_recovery.html")
	e.do("anon", "POST", "/account/lost_password", url.Values{"token": {tok}, "new_password": {"short"}, "new_password_confirmation": {"other"}}, "password_recovery_invalid.html")
	e.do("anon", "POST", "/account/lost_password", url.Values{"token": {tok}, "new_password": {"newpassw0rd"}, "new_password_confirmation": {"newpassw0rd"}}, "password_recovery_done.html")
	e.do("anon", "GET", "/login", nil, "password_recovery_done_next.html")
	e.do("anon", "GET", "/account/lost_password?token="+tok, nil, "recovery_token_used.html")
	authPasswords["jsmith"] = "newpassw0rd"
	defer func() { authPasswords["jsmith"] = "jsmith" }()

	// ---- 自己登録（既定: 管理者による手動有効化）
	e.clients["anon2"] = newClient(t)
	e.do("anon2", "GET", "/account/register", nil, "register.html")
	e.do("anon2", "POST", "/account/register", url.Values{"user[login]": {"jsmith"}, "user[password]": {"x"}, "user[password_confirmation]": {"y"},
		"user[firstname]": {""}, "user[lastname]": {"Doe"}, "user[mail]": {"bad"}, "user[language]": {"en"}, "pref[hide_mail]": {"0"}}, "register_invalid.html")
	e.do("anon2", "POST", "/account/register", url.Values{"user[login]": {"newbie"}, "user[password]": {"newbie123"}, "user[password_confirmation]": {"newbie123"},
		"user[firstname]": {"New"}, "user[lastname]": {"Bie"}, "user[mail]": {"newbie@example.net"}, "user[language]": {"en"}, "pref[hide_mail]": {"1"},
		"user[custom_field_values][4]": {"01234"}}, "register_done.html")
	e.do("anon2", "GET", "/login", nil, "register_done_next.html")
	// 登録済み（未有効化）のユーザーのログイン
	e.do("anon2", "POST", "/login", url.Values{"username": {"newbie"}, "password": {"newbie123"}}, "login_pending.html")
	e.do("anon2", "GET", "/login", nil, "login_pending_next.html")
	// ロックされたユーザーのログイン
	e.do("anon2", "POST", "/login", url.Values{"username": {"dlopper2"}, "password": {"foo"}}, "")
	e.do("anon2", "GET", "/account/activate?token=abc", nil, "activate_invalid.html")

	// ---- 2 要素認証の有効化（jsmith）
	e.do("jsmith", "GET", "/my/twofa/select_scheme", nil, "twofa_select_scheme.html")
	e.do("jsmith", "POST", "/my/twofa/totp/activate/init", nil, "twofa_activate_init.html")
	_, _, page := e.do("jsmith", "GET", "/my/twofa/totp/activate/confirm", nil, "twofa_activate_confirm.html")
	m := authTotpKeyRe.FindStringSubmatch(page)
	if m == nil {
		t.Fatal("TOTP key not found in activate_confirm")
	}
	key := strings.ReplaceAll(m[1], " ", "")
	e.do("jsmith", "POST", "/my/twofa/totp/activate", url.Values{"twofa_code": {"000000"}}, "twofa_activate_invalid.html")
	e.do("jsmith", "GET", "/my/twofa/totp/activate/confirm", nil, "")
	// 再表示で鍵は変わらない（init していない）
	prev := totp.Now(key, frozenTime.Add(-30*time.Second))
	cur := totp.Now(key, frozenTime)
	e.do("jsmith", "POST", "/my/twofa/totp/activate", url.Values{"twofa_code": {prev}}, "twofa_activate_done.html")
	e.do("jsmith", "GET", "/my/account", nil, "twofa_account_active.html")
	e.do("jsmith", "GET", "/my/twofa/select_scheme", nil, "twofa_select_scheme_already.html")
	// バックアップコード
	e.do("jsmith", "POST", "/my/twofa/backup_codes/init", nil, "backup_codes_init.html")
	e.do("jsmith", "GET", "/my/twofa/backup_codes/confirm", nil, "backup_codes_confirm.html")
	e.do("jsmith", "POST", "/my/twofa/backup_codes/create", url.Values{"twofa_code": {prev}}, "backup_codes_replay.html")
	e.do("jsmith", "POST", "/my/twofa/backup_codes/create", url.Values{"twofa_code": {cur}}, "backup_codes_create.html")
	_, _, page = e.do("jsmith", "GET", "/my/twofa/backup_codes", nil, "backup_codes_show.html")
	codes := authBackupRe.FindAllStringSubmatch(page, -1)
	if len(codes) != 10 {
		t.Fatalf("backup codes: %d", len(codes))
	}
	e.do("jsmith", "GET", "/my/twofa/backup_codes", nil, "backup_codes_show_again.html")
	e.do("jsmith", "GET", "/my/account", nil, "")

	// ---- 2 要素認証のログイン 2 段目
	e.clients["anon3"] = newClient(t)
	e.do("anon3", "POST", "/login", url.Values{"username": {"jsmith"}, "password": {"newpassw0rd"}, "back_url": {e.base + "/projects"}}, "twofa_login.html")
	e.do("anon3", "GET", "/account/twofa/confirm", nil, "twofa_confirm.html")
	e.do("anon3", "POST", "/account/twofa", url.Values{"twofa_code": {"123456"}}, "twofa_invalid.html")
	e.do("anon3", "GET", "/account/twofa/confirm", nil, "twofa_confirm_invalid.html")
	e.do("anon3", "POST", "/account/twofa", url.Values{"twofa_code": {strings.ToUpper(codes[0][1])}}, "twofa_backup_login.html")
	e.do("anon3", "GET", "/my/page", nil, "")
	// 同じバックアップコードは再利用できない・試行回数の上限
	e.clients["anon4"] = newClient(t)
	e.do("anon4", "POST", "/login", url.Values{"username": {"jsmith"}, "password": {"newpassw0rd"}}, "")
	e.do("anon4", "POST", "/account/twofa", url.Values{"twofa_code": {codes[0][1]}}, "twofa_backup_reused.html")
	e.do("anon4", "POST", "/account/twofa", url.Values{"twofa_code": {"1"}}, "")
	e.do("anon4", "POST", "/account/twofa", url.Values{"twofa_code": {"2"}}, "twofa_too_many.html")
	e.do("anon4", "GET", "/", nil, "")
	e.do("anon4", "GET", "/account/twofa/confirm", nil, "twofa_confirm_no_session.html")

	// ---- 無効化（本人）と管理者による解除
	e.do("jsmith", "POST", "/my/twofa/totp/deactivate/init", nil, "twofa_deactivate_init.html")
	e.do("jsmith", "GET", "/my/twofa/totp/deactivate/confirm", nil, "twofa_deactivate_confirm.html")
	e.do("jsmith", "POST", "/my/twofa/totp/deactivate", url.Values{"twofa_code": {"999999"}}, "twofa_deactivate_invalid.html")
	e.do("admin", "GET", "/users/2/edit", nil, "users_edit_twofa.html")
	e.do("admin", "POST", "/users/2/twofa/deactivate", nil, "admin_deactivate.html")
	e.do("admin", "GET", "/users/2/edit", nil, "users_edit_twofa_off.html")
	e.do("admin", "POST", "/users/1/twofa/deactivate", nil, "admin_deactivate_self.html")
}

// TestAccountAuthPagesMatchRedmine は authFlow の各応答が参照 Redmine と一致することを確認する。
func TestAccountAuthPagesMatchRedmine(t *testing.T) {
	dir := filepath.Join("testdata", "account_auth")
	if ref := os.Getenv("BUROPHER_AUTH_GOLDEN_REF"); ref != "" {
		refDB, err := sql.Open("sqlite", "file:"+os.Getenv("BUROPHER_AUTH_GOLDEN_REF_DB")+"?mode=ro")
		if err != nil {
			t.Fatal(err)
		}
		defer refDB.Close()
		e := &authEnv{t: t, base: ref, clients: map[string]*http.Client{}, out: map[string]string{},
			recoveryToken: func(uid int) string {
				var v string
				_ = refDB.QueryRow(`SELECT value FROM tokens WHERE user_id = ? AND action = 'recovery' ORDER BY id DESC LIMIT 1`, uid).Scan(&v)
				return v
			}}
		authFlow(e)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, s := range e.out {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	myFreezeClock(t)
	ts, d := newFixtureServer(t)
	insertFixtureAuthSource(t, d)
	e := &authEnv{t: t, base: ts.URL, clients: map[string]*http.Client{}, out: map[string]string{},
		recoveryToken: func(uid int) string { return dbRecoveryToken(t, d, uid) }}
	authFlow(e)
	if dump := os.Getenv("BUROPHER_AUTH_DUMP"); dump != "" {
		_ = os.MkdirAll(dump, 0o755)
		for name, s := range e.out {
			_ = os.WriteFile(filepath.Join(dump, name), []byte(s), 0o644)
		}
	}
	for _, name := range e.order {
		t.Run(name, func(t *testing.T) { myCompare(t, dir, name, e.out[name]) })
	}
}

func dbRecoveryToken(t *testing.T, d *db.DB, uid int) string {
	var v string
	if err := d.Get(context.Background(), &v, `SELECT value FROM tokens WHERE user_id = ? AND action = 'recovery' ORDER BY id DESC LIMIT 1`, uid); err != nil {
		return ""
	}
	return v
}
