package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
)

// このファイルは管理画面のユーザー・グループ（UsersController / GroupsController /
// PrincipalMembershipsController / EmailAddressesController / ContextMenusController#users）と
// users / groups の REST API のテスト。
// testdata/admin_users/* は参照 Redmine 6.1.2（公式フィクスチャ、私用インスタンスをリセットした直後）の
// `compat fetch -raw` の出力に CSRF・フォーム名・ダイジェスト・ベース URL・キーの置換をしたもの。

var (
	namedFormRe = regexp.MustCompile(`name="([a-z_0-9-]+)-[0-9a-f]{8}"`)
	apiKeyJSON  = regexp.MustCompile(`("api_key":")[0-9a-f]{40}"`)
	apiKeyXML   = regexp.MustCompile(`<api_key>[0-9a-f]{40}</api_key>`)
	pngDigestRe = regexp.MustCompile(`-[0-9a-f]{8}\.png`)
)

// normalizeUsersAdmin は normalize / normalizeFixture に加えて名前付きフォームの乱数と API キーを伏せる。
func normalizeUsersAdmin(s, base string) string {
	s = normalize(normalizeFixture(s, base))
	s = namedFormRe.ReplaceAllString(s, `name="$1-RANDOM"`)
	s = pngDigestRe.ReplaceAllString(s, "-DIGEST.png")
	s = apiKeyJSON.ReplaceAllString(s, `${1}KEY"`)
	return apiKeyXML.ReplaceAllString(s, `<api_key>KEY</api_key>`)
}

func compareUsersGolden(t *testing.T, name, got string) {
	t.Helper()
	want, err := os.ReadFile("testdata/admin_users/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
		for i := 0; i < len(gl) || i < len(wl); i++ {
			var a, b string
			if i < len(gl) {
				a = gl[i]
			}
			if i < len(wl) {
				b = wl[i]
			}
			if a != b {
				t.Fatalf("%s mismatch at line %d:\n got: %q\nwant: %q", name, i+1, a, b)
			}
		}
	}
}

// TestAdminUsersPagesMatchRedmine は管理画面の GET が参照 Redmine とバイト単位で一致することを確認する。
func TestAdminUsersPagesMatchRedmine(t *testing.T) {
	ts, _ := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")
	cases := []struct{ path, golden string }{
		{"/users", "users_index_admin.html"},
		{"/groups", "groups_index_admin.html"},
		{"/groups/10/edit", "groups_10_edit_admin.html"},
		{"/groups/10", "groups_10_show_admin.html"},
		{"/users/2/email_addresses", "users_2_email_addresses_admin.html"},
		{"/users/context_menu?ids[]=2&ids[]=3", "users_context_menu_bulk_admin.html"},
		{"/users/context_menu?ids[]=2", "users_context_menu_one_admin.html"},
		{"/users/2/memberships/1/edit", "users_2_membership_edit_admin.html"},
		{"/users.json", "users_admin.json"},
		{"/users/2.xml?include=memberships,groups", "users_2_admin.xml"},
		{"/groups/10.xml?include=users,memberships", "groups_10_admin.xml"},
		{"/groups.json?builtin=1", "groups_builtin_admin.json"},
	}
	for _, tc := range cases {
		t.Run(tc.golden, func(t *testing.T) {
			var res *http.Response
			var body string
			if strings.Contains(tc.path, ".json") || strings.Contains(tc.path, ".xml") {
				// API は HTTP Basic 認証（API 形式ではセッションを使わない）
				res, body = apiRequest(t, "GET", ts.URL+tc.path, "", "")
			} else {
				res, body = get(t, c, ts.URL+tc.path)
			}
			if res.StatusCode != 200 {
				t.Fatalf("status %d", res.StatusCode)
			}
			compareUsersGolden(t, tc.golden, normalizeUsersAdmin(body, ts.URL))
		})
	}
}

// TestAdminUsersAccess は require_admin（一覧・編集は管理者のみ、詳細は可視なら誰でも）を確認する。
func TestAdminUsersAccess(t *testing.T) {
	ts, _ := newFixtureServer(t)
	anon := newClient(t)
	if res, _ := get(t, anon, ts.URL+"/users"); res.StatusCode != 302 || !strings.Contains(res.Header.Get("Location"), "/login?back_url=") {
		t.Errorf("anonymous /users: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	js := login(t, ts, "jsmith", "jsmith")
	for _, p := range []string{"/users", "/users/new", "/users/2/edit", "/groups", "/groups/10/edit", "/users/2/memberships/new"} {
		if res, _ := get(t, js, ts.URL+p); res.StatusCode != 403 {
			t.Errorf("jsmith %s: status %d", p, res.StatusCode)
		}
	}
	if res, _ := get(t, js, ts.URL+"/users/2"); res.StatusCode != 200 {
		t.Errorf("jsmith /users/2: %d", res.StatusCode)
	}
	if res, _ := get(t, js, ts.URL+"/users/current"); res.StatusCode != 200 {
		t.Errorf("jsmith /users/current: %d", res.StatusCode)
	}
	// 自分以外のメールアドレスは管理者のみ
	if res, _ := get(t, js, ts.URL+"/users/3/email_addresses"); res.StatusCode != 403 {
		t.Errorf("jsmith other email_addresses: %d", res.StatusCode)
	}
	if res, _ := get(t, js, ts.URL+"/users/2/email_addresses"); res.StatusCode != 200 {
		t.Errorf("jsmith own email_addresses: %d", res.StatusCode)
	}
	admin := login(t, ts, "admin", "admin")
	if res, _ := get(t, admin, ts.URL+"/users/999"); res.StatusCode != 404 {
		t.Errorf("/users/999: %d", res.StatusCode)
	}
	// 匿名ユーザー（logged でない）は edit できない
	if res, _ := get(t, admin, ts.URL+"/users/6/edit"); res.StatusCode != 404 {
		t.Errorf("/users/6/edit: %d", res.StatusCode)
	}
}

// send は CSRF トークン付きでフォームを送る（method は _method で上書きする）。
func send(t *testing.T, c *http.Client, ts string, method, path string, form url.Values) (*http.Response, string) {
	t.Helper()
	_, page := get(t, c, ts+"/my/page")
	tok := tokenRe.FindStringSubmatch(page)
	if tok == nil {
		_, page = get(t, c, ts+"/")
		tok = tokenRe.FindStringSubmatch(page)
	}
	if form == nil {
		form = url.Values{}
	}
	if tok != nil {
		form.Set("authenticity_token", tok[1])
	} else if m := regexp.MustCompile(`<meta name="csrf-token" content="([^"]+)"`).FindStringSubmatch(page); m != nil {
		form.Set("authenticity_token", m[1])
	}
	if method != http.MethodPost {
		form.Set("_method", strings.ToLower(method))
	}
	return post(t, c, ts+path, form)
}

func uQueryInt(t *testing.T, d *db.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := d.Get(context.Background(), &n, q, args...); err != nil {
		t.Fatal(err)
	}
	return n
}

func uQueryString(t *testing.T, d *db.DB, q string, args ...any) string {
	t.Helper()
	var s string
	if err := d.Get(context.Background(), &s, q, args...); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestUsersCreateUpdateDestroy は users#create / update / destroy / bulk_* の振る舞いを確認する。
func TestUsersCreateUpdateDestroy(t *testing.T) {
	ts, d := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")

	// 検証エラー: 再描画（200）とエラーメッセージ（Redmine と同じ順序）
	res, body := send(t, c, ts.URL, "POST", "/users", url.Values{
		"user[login]": {""}, "user[firstname]": {""}, "user[lastname]": {"X"}, "user[mail]": {"bad"},
		"user[password]": {"short"}, "user[password_confirmation]": {"other"},
	})
	if res.StatusCode != 200 {
		t.Fatalf("invalid create: status %d", res.StatusCode)
	}
	for _, m := range []string{"<li>Email is invalid</li>", "<li>Login cannot be blank</li>", "<li>First name cannot be blank</li>",
		"<li>Password is too short (minimum is 8 characters)</li>", "<li>Password doesn&#39;t match confirmation</li>"} {
		if !strings.Contains(body, m) {
			t.Errorf("missing error %s", m)
		}
	}
	if i, j := strings.Index(body, "Email is invalid"), strings.Index(body, "Login cannot be blank"); i < 0 || j < i {
		t.Error("email error should come first (has_one :email_address autosave)")
	}

	// 作成
	res, _ = send(t, c, ts.URL, "POST", "/users", url.Values{
		"user[login]": {"newuser"}, "user[firstname]": {"New"}, "user[lastname]": {"User"}, "user[mail]": {"newuser@example.net"},
		"user[language]": {"fr"}, "user[password]": {"secret123"}, "user[password_confirmation]": {"secret123"},
		"user[mail_notification]": {"none"}, "pref[time_zone]": {"Tokyo"}, "pref[hide_mail]": {"0"},
	})
	if res.StatusCode != 302 {
		t.Fatalf("create: status %d", res.StatusCode)
	}
	id := uQueryInt(t, d, `SELECT principal_id FROM user_accounts WHERE login = 'newuser'`)
	if loc := res.Header.Get("Location"); !strings.HasSuffix(loc, "/users/"+uitoa(id)+"/edit") {
		t.Errorf("redirect %s", loc)
	}
	if got := uQueryString(t, d, `SELECT address FROM email_addresses WHERE user_id = ? AND is_default = TRUE`, id); got != "newuser@example.net" {
		t.Errorf("mail %q", got)
	}
	if got := uQueryString(t, d, `SELECT mail_notification FROM user_notification_settings WHERE user_id = ?`, id); got != "none" {
		t.Errorf("mail_notification %q", got)
	}
	if got := uQueryString(t, d, `SELECT time_zone FROM user_preferences WHERE user_id = ?`, id); got != "Tokyo" {
		t.Errorf("time_zone %q", got)
	}
	// 作成したユーザーでログインできる
	login(t, ts, "newuser", "secret123")
	// flash
	_, body = get(t, c, ts.URL+"/users/"+uitoa(id)+"/edit")
	if !strings.Contains(body, `User <a href="/users/`+uitoa(id)+`">newuser</a> created.`) {
		t.Error("create flash missing")
	}

	// ログイン名の重複（大文字小文字を無視）
	_, body = send(t, c, ts.URL, "POST", "/users", url.Values{
		"user[login]": {"NEWUSER"}, "user[firstname]": {"D"}, "user[lastname]": {"U"}, "user[mail]": {"NewUser@example.net"},
		"user[password]": {"secret123"}, "user[password_confirmation]": {"secret123"},
	})
	if !strings.Contains(body, "Email has already been taken") || !strings.Contains(body, "Login has already been taken") {
		t.Error("uniqueness errors missing")
	}

	// 更新（グループ・管理者フラグ・パスワード）
	res, _ = send(t, c, ts.URL, "PUT", "/users/2", url.Values{
		"user[firstname]": {"Johnny"}, "user[admin]": {"1"}, "user[password]": {"newpassword1"}, "user[password_confirmation]": {"newpassword1"},
	})
	if res.StatusCode != 302 {
		t.Fatalf("update: status %d", res.StatusCode)
	}
	if got := uQueryString(t, d, `SELECT firstname FROM principals WHERE id = 2`); got != "Johnny" {
		t.Errorf("firstname %q", got)
	}
	if uQueryInt(t, d, `SELECT admin FROM user_accounts WHERE principal_id = 2`) != 1 {
		t.Error("admin flag not set")
	}
	login(t, ts, "jsmith", "newpassword1")
	res, _ = send(t, c, ts.URL, "PUT", "/users/2", url.Values{"user[group_ids][]": {"10", ""}})
	if res.StatusCode != 302 {
		t.Fatalf("update groups: %d", res.StatusCode)
	}
	if uQueryInt(t, d, `SELECT COUNT(*) FROM group_users WHERE group_id = 10 AND user_id = 2`) != 1 {
		t.Error("group not added")
	}
	// グループのメンバーシップ（Private child: Manager, Developer）が継承される
	if uQueryInt(t, d, `SELECT COUNT(*) FROM member_roles mr JOIN members m ON m.id = mr.member_id WHERE m.principal_id = 2 AND mr.inherited_from IS NOT NULL`) == 0 {
		t.Error("inherited member roles not created")
	}

	// ロックとロック解除
	send(t, c, ts.URL, "PUT", "/users/3", url.Values{"user[status]": {"3"}})
	if uQueryInt(t, d, `SELECT status FROM principals WHERE id = 3`) != 3 {
		t.Error("lock failed")
	}
	send(t, c, ts.URL, "POST", "/users/bulk_unlock", url.Values{"ids[]": {"3"}})
	if uQueryInt(t, d, `SELECT status FROM principals WHERE id = 3`) != 1 {
		t.Error("bulk unlock failed")
	}
	// 自分自身は一括ロックの対象外
	send(t, c, ts.URL, "POST", "/users/bulk_lock", url.Values{"ids[]": {"1", "8"}})
	if uQueryInt(t, d, `SELECT status FROM principals WHERE id = 1`) != 1 || uQueryInt(t, d, `SELECT status FROM principals WHERE id = 8`) != 3 {
		t.Error("bulk lock")
	}

	// 削除の確認画面（confirm なし）と削除
	res, body = send(t, c, ts.URL, "DELETE", "/users/2", nil)
	if res.StatusCode != 200 || !strings.Contains(body, "text_user_destroy_confirmation") && !strings.Contains(body, `<label for="confirm">Login</label>`) {
		t.Fatalf("destroy confirmation: %d", res.StatusCode)
	}
	anon := uQueryInt(t, d, `SELECT id FROM principals WHERE kind = 'anonymous_user'`)
	authored := uQueryInt(t, d, `SELECT COUNT(*) FROM issues WHERE author_id = 2`)
	res, _ = send(t, c, ts.URL, "DELETE", "/users/2", url.Values{"confirm": {"jsmith"}})
	if res.StatusCode != 302 {
		t.Fatalf("destroy: %d", res.StatusCode)
	}
	if uQueryInt(t, d, `SELECT COUNT(*) FROM principals WHERE id = 2`) != 0 {
		t.Error("user not deleted")
	}
	if authored > 0 && uQueryInt(t, d, `SELECT COUNT(*) FROM issues WHERE author_id = ?`, anon) < authored {
		t.Error("issues not reassigned to anonymous")
	}
	if uQueryInt(t, d, `SELECT COUNT(*) FROM members WHERE principal_id = 2`) != 0 {
		t.Error("memberships not deleted")
	}

	// 一括削除（確認 → 実行）
	res, _ = send(t, c, ts.URL, "DELETE", "/users/bulk_destroy", url.Values{"ids[]": {"7", "8"}})
	if res.StatusCode != 200 {
		t.Fatalf("bulk destroy confirmation: %d", res.StatusCode)
	}
	res, _ = send(t, c, ts.URL, "DELETE", "/users/bulk_destroy", url.Values{"ids[]": {"7", "8"}, "confirm": {"Yes"}})
	if res.StatusCode != 302 || uQueryInt(t, d, `SELECT COUNT(*) FROM principals WHERE id IN (7, 8)`) != 0 {
		t.Errorf("bulk destroy: %d", res.StatusCode)
	}
}

// TestGroupsAndMemberships はグループの作成・ユーザー追加・削除とメンバーシップの操作を確認する。
func TestGroupsAndMemberships(t *testing.T) {
	ts, d := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")

	_, body := send(t, c, ts.URL, "POST", "/groups", url.Values{"group[name]": {"a team"}})
	if !strings.Contains(body, "<li>Name has already been taken</li>") {
		t.Error("group uniqueness error missing")
	}
	res, _ := send(t, c, ts.URL, "POST", "/groups", url.Values{"group[name]": {"C Team"}})
	if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/groups") {
		t.Fatalf("group create: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	gid := uQueryInt(t, d, `SELECT id FROM principals WHERE kind = 'group' AND name = 'C Team'`)

	res, _ = send(t, c, ts.URL, "POST", "/groups/"+uitoa(gid)+"/users", url.Values{"user_ids[]": {"2", "3"}})
	if res.StatusCode != 302 || uQueryInt(t, d, `SELECT COUNT(*) FROM group_users WHERE group_id = ?`, gid) != 2 {
		t.Fatalf("add users: %d", res.StatusCode)
	}
	// XHR（js）の追加・削除
	res, body = send(t, c, ts.URL, "DELETE", "/groups/"+uitoa(gid)+"/users/3.js", nil)
	if res.StatusCode != 200 || !strings.Contains(body, "$('#tab-content-users').html(") {
		t.Fatalf("remove user js: %d %s", res.StatusCode, body)
	}
	if uQueryInt(t, d, `SELECT COUNT(*) FROM group_users WHERE group_id = ?`, gid) != 1 {
		t.Error("user not removed")
	}

	// グループのメンバーシップ → ユーザーに継承
	res, _ = send(t, c, ts.URL, "POST", "/groups/"+uitoa(gid)+"/memberships", url.Values{"membership[project_ids][]": {"1"}, "membership[role_ids][]": {"2"}})
	if res.StatusCode != 302 {
		t.Fatalf("membership create: %d", res.StatusCode)
	}
	mid := uQueryInt(t, d, `SELECT id FROM members WHERE principal_id = ? AND project_id = 1`, gid)
	if uQueryInt(t, d, `SELECT COUNT(*) FROM member_roles mr JOIN members m ON m.id = mr.member_id WHERE m.principal_id = 2 AND m.project_id = 1 AND mr.inherited_from IS NOT NULL`) != 1 {
		t.Error("group membership not inherited by user")
	}
	res, body = send(t, c, ts.URL, "PUT", "/groups/"+uitoa(gid)+"/memberships/"+uitoa(mid)+".js", url.Values{"membership[role_ids][]": {"2", "3"}})
	if res.StatusCode != 200 || !strings.Contains(body, `-roles").html("Developer, Reporter")`) {
		t.Fatalf("membership update js: %d %s", res.StatusCode, body)
	}
	res, body = send(t, c, ts.URL, "PUT", "/groups/"+uitoa(gid)+"/memberships/"+uitoa(mid)+".js", url.Values{"membership[role_ids][]": {""}})
	if !strings.Contains(body, "alert('Failed to save member(s): Role cannot be empty.');") && !strings.Contains(body, "alert(") {
		t.Errorf("empty roles: %s", body)
	}
	res, _ = send(t, c, ts.URL, "DELETE", "/groups/"+uitoa(gid)+"/memberships/"+uitoa(mid), nil)
	if res.StatusCode != 302 || uQueryInt(t, d, `SELECT COUNT(*) FROM members WHERE id = ?`, mid) != 0 {
		t.Fatalf("membership destroy: %d", res.StatusCode)
	}
	// 継承されたメンバーシップは削除できない
	inheritedMember := uQueryInt(t, d, `SELECT m.id FROM members m JOIN member_roles mr ON mr.member_id = m.id WHERE mr.inherited_from IS NOT NULL ORDER BY m.id LIMIT 1`)
	_, body = get(t, c, ts.URL+"/users/8/edit?tab=memberships")
	if strings.Contains(body, `href="/users/8/memberships/`+uitoa(inheritedMember)+`" `) && strings.Contains(body, `data-method="delete" href="/users/8/memberships/`+uitoa(inheritedMember)+`"`) {
		t.Error("inherited membership should not be deletable")
	}

	// 組込グループは削除されない
	res, _ = send(t, c, ts.URL, "DELETE", "/groups/12", nil)
	if res.StatusCode != 302 || uQueryInt(t, d, `SELECT COUNT(*) FROM principals WHERE id = 12`) != 1 {
		t.Error("builtin group destroyed")
	}
	res, _ = send(t, c, ts.URL, "DELETE", "/groups/"+uitoa(gid), nil)
	if res.StatusCode != 302 || uQueryInt(t, d, `SELECT COUNT(*) FROM principals WHERE id = ?`, gid) != 0 {
		t.Error("group not destroyed")
	}
	if uQueryInt(t, d, `SELECT COUNT(*) FROM member_roles WHERE inherited_from IS NOT NULL AND member_id IN (SELECT id FROM members WHERE principal_id = 2 AND project_id = 1)`) != 0 {
		t.Error("inherited roles remain after group destroy")
	}
}

// TestEmailAddresses は追加のメールアドレスの追加・通知切り替え・削除を確認する。
func TestEmailAddresses(t *testing.T) {
	ts, d := newFixtureServer(t)
	c := login(t, ts, "jsmith", "jsmith")
	res, _ := send(t, c, ts.URL, "POST", "/users/2/email_addresses", url.Values{"email_address[address]": {"jsmith2@example.net"}})
	if res.StatusCode != 302 {
		t.Fatalf("create: %d", res.StatusCode)
	}
	id := uQueryInt(t, d, `SELECT id FROM email_addresses WHERE address = 'jsmith2@example.net'`)
	res, body := send(t, c, ts.URL, "POST", "/users/2/email_addresses.js", url.Values{"email_address[address]": {"JSMITH2@example.net"}})
	if res.StatusCode != 200 || !strings.Contains(body, "Email has already been taken") {
		t.Errorf("duplicate: %d %s", res.StatusCode, body)
	}
	send(t, c, ts.URL, "PUT", "/users/2/email_addresses/"+uitoa(id), url.Values{"notify": {"0"}})
	if uQueryInt(t, d, `SELECT notify FROM email_addresses WHERE id = ?`, id) != 0 {
		t.Error("notify not disabled")
	}
	send(t, c, ts.URL, "DELETE", "/users/2/email_addresses/"+uitoa(id), nil)
	if uQueryInt(t, d, `SELECT COUNT(*) FROM email_addresses WHERE id = ?`, id) != 0 {
		t.Error("not deleted")
	}
	// 既定のアドレスは削除できない（404）
	def := uQueryInt(t, d, `SELECT id FROM email_addresses WHERE user_id = 2 AND is_default = TRUE`)
	if res, _ := send(t, c, ts.URL, "DELETE", "/users/2/email_addresses/"+uitoa(def), nil); res.StatusCode != 404 {
		t.Errorf("default address delete: %d", res.StatusCode)
	}
}

// apiRequest は HTTP Basic 認証で API を呼ぶ。
func apiRequest(t *testing.T, method, u, contentType, body string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, u, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("admin", "admin")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := readUnbranded(res.Body)
	return res, string(b)
}

// TestUsersGroupsAPI は REST API の create / update / destroy を確認する。
func TestUsersGroupsAPI(t *testing.T) {
	ts, d := newFixtureServer(t)
	res, body := apiRequest(t, "POST", ts.URL+"/users.json", "application/json",
		`{"user":{"login":"apiuser","firstname":"Api","lastname":"User","mail":"apiuser@example.net","password":"secret123"}}`)
	if res.StatusCode != 201 {
		t.Fatalf("create: %d %s", res.StatusCode, body)
	}
	var created struct {
		User struct {
			ID    int    `json:"id"`
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil || created.User.Login != "apiuser" {
		t.Fatalf("create body: %s", body)
	}
	if !strings.HasSuffix(res.Header.Get("Location"), "/users/"+uitoa(created.User.ID)) {
		t.Errorf("location %s", res.Header.Get("Location"))
	}
	res, body = apiRequest(t, "POST", ts.URL+"/users.json", "application/json", `{"user":{"login":"apiuser","lastname":"User","mail":"apiuser@example.net"}}`)
	if res.StatusCode != 422 || body != `{"errors":["Email has already been taken","First name cannot be blank","Login has already been taken"]}` {
		t.Errorf("invalid create: %d %s", res.StatusCode, body)
	}
	res, _ = apiRequest(t, "PUT", ts.URL+"/users/4.json", "application/json", `{"user":{"lastname":"Hilly"}}`)
	if res.StatusCode != 204 || uQueryString(t, d, `SELECT lastname FROM principals WHERE id = 4`) != "Hilly" {
		t.Errorf("update: %d", res.StatusCode)
	}
	res, _ = apiRequest(t, "DELETE", ts.URL+"/users/"+uitoa(created.User.ID)+".json", "", "")
	if res.StatusCode != 204 || uQueryInt(t, d, `SELECT COUNT(*) FROM principals WHERE id = ?`, created.User.ID) != 0 {
		t.Errorf("destroy: %d", res.StatusCode)
	}

	res, body = apiRequest(t, "POST", ts.URL+"/groups.xml", "application/xml",
		`<?xml version="1.0"?><group><name>Api Group</name><user_ids type="array"><user_id>2</user_id><user_id>4</user_id></user_ids></group>`)
	if res.StatusCode != 201 || !strings.Contains(body, "<name>Api Group</name>") {
		t.Fatalf("group create: %d %s", res.StatusCode, body)
	}
	gid := uQueryInt(t, d, `SELECT id FROM principals WHERE name = 'Api Group'`)
	if uQueryInt(t, d, `SELECT COUNT(*) FROM group_users WHERE group_id = ?`, gid) != 2 {
		t.Error("group users not set")
	}
	res, _ = apiRequest(t, "POST", ts.URL+"/groups/"+uitoa(gid)+"/users.json", "application/json", `{"user_id":3}`)
	if res.StatusCode != 204 || uQueryInt(t, d, `SELECT COUNT(*) FROM group_users WHERE group_id = ?`, gid) != 3 {
		t.Errorf("add user: %d", res.StatusCode)
	}
	res, body = apiRequest(t, "POST", ts.URL+"/groups/"+uitoa(gid)+"/users.json", "application/json", `{"user_id":3}`)
	if res.StatusCode != 422 || body != `{"errors":["User is invalid"]}` {
		t.Errorf("add existing user: %d %s", res.StatusCode, body)
	}
	res, _ = apiRequest(t, "DELETE", ts.URL+"/groups/"+uitoa(gid)+"/users/3.json", "", "")
	if res.StatusCode != 204 {
		t.Errorf("remove user: %d", res.StatusCode)
	}
	res, _ = apiRequest(t, "DELETE", ts.URL+"/groups/"+uitoa(gid)+".json", "", "")
	if res.StatusCode != 204 || uQueryInt(t, d, `SELECT COUNT(*) FROM principals WHERE id = ?`, gid) != 0 {
		t.Errorf("group destroy: %d", res.StatusCode)
	}
}

func uitoa(n int) string { return strconv.Itoa(n) }
