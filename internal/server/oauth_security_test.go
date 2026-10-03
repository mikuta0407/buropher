// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// oauthTokenFor は管理者がアプリケーションを作り、user が scopes で同意したアクセストークンを返す。
func oauthTokenFor(t *testing.T, ts string, admin, user *http.Client, scopes ...string) string {
	t.Helper()
	_, uid, secret := createOAuthApp(t, ts, admin, "Sec "+strings.Join(scopes, " "), scopes...)
	code := authorizeCode(t, ts, user, uid, strings.Join(scopes, " "))
	access, _ := exchangeCode(t, ts, uid, secret, code)["access_token"].(string)
	return access
}

// TestOAuthMyAccountDoesNotLeakAPIKey は、スコープを限定した OAuth トークンで GET /my/account.json を呼んでも
// API キー（スコープの制限を受けない全権限の資格情報）が返らないことを確認する。
// 返ってしまうと view_issues だけのトークンから API キーを得て、スコープ外の操作（管理者なら admin スコープ無しで
// 管理操作）ができてしまう（users/show.api.rsb は authorized_by_oauth? のときに api_key を出さない）。
func TestOAuthMyAccountDoesNotLeakAPIKey(t *testing.T) {
	ts, _ := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	access := oauthTokenFor(t, ts.URL, admin, admin, "view_issues")

	res, body := oauthDo(t, newClient(t), http.MethodGet, ts.URL+"/my/account.json", nil, bearer(access))
	if res.StatusCode != 200 {
		t.Fatalf("my/account.json: %d %s", res.StatusCode, body)
	}
	u, _ := decodeJSON(t, body)["user"].(map[string]any)
	if k, ok := u["api_key"]; ok {
		t.Errorf("api_key leaked to OAuth token: %v", k)
	}
	// admin スコープが無いので管理者として表示しない（@user = User.current の admin?）
	if u["admin"] != false {
		t.Errorf("admin = %v, want false", u["admin"])
	}
	res, body = oauthDo(t, newClient(t), http.MethodGet, ts.URL+"/my/account.xml", nil, bearer(access))
	if res.StatusCode != 200 || strings.Contains(body, "<api_key>") {
		t.Errorf("my/account.xml: %d %s", res.StatusCode, body)
	}
	// HTTP Basic での取得は従来どおり api_key を含む
	res, body = oauthDo(t, newClient(t), http.MethodGet, ts.URL+"/my/account.json", nil, map[string]string{"Authorization": "Basic " + basicAuth("admin", "admin")})
	if res.StatusCode != 200 || !strings.Contains(body, `"api_key":`) {
		t.Errorf("basic auth my/account.json: %d %s", res.StatusCode, body)
	}
}

// TestOAuthMyAccountCannotChangeMail は、OAuth トークンで PUT /my/account からメールアドレスを変更できないことを確認する。
// 変更できると、任意のスコープのトークンを持つ第三者がメールアドレスを自分のものに変え、
// パスワード再設定（lost_password）でアカウントを乗っ取れる。
func TestOAuthMyAccountCannotChangeMail(t *testing.T) {
	ts, d := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	jsmith := login(t, ts, "jsmith", "jsmith")
	access := oauthTokenFor(t, ts.URL, admin, jsmith, "view_issues")

	put := func(body string) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodPut, ts.URL+"/my/account.json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+access)
		req.Header.Set("Content-Type", "application/json")
		res, err := newClient(t).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if st := put(`{"user":{"mail":"attacker@example.com"}}`); st != http.StatusForbidden {
		t.Errorf("mail change via OAuth: status %d, want 403", st)
	}
	var mail string
	if err := d.Get(context.Background(), &mail, `SELECT address FROM email_addresses WHERE user_id = 2 AND is_default = ?`, true); err != nil {
		t.Fatal(err)
	}
	if mail != "jsmith@somenet.foo" {
		t.Errorf("mail changed to %q", mail)
	}
	// メールアドレス以外（同じアドレスの再送信を含む）の更新は従来どおり可能
	if st := put(`{"user":{"firstname":"Johnny","mail":"JSmith@somenet.foo"}}`); st/100 != 2 {
		t.Errorf("firstname change via OAuth: status %d", st)
	}
}
