package server_test

// test/integration/api_test/users_test.rb の移植。

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/repository"
)

// usersActiveByLogin は User.active.order('login') の id 列。
func usersActiveByLogin(t *testing.T, d *db.DB) []int64 {
	t.Helper()
	var ids []int64
	if err := d.Select(context.Background(), &ids, `SELECT p.id FROM principals p JOIN user_accounts u ON u.principal_id = p.id
WHERE p.kind = 'user' AND p.status = 1 ORDER BY u.login`); err != nil {
		t.Fatal(err)
	}
	return ids
}

// usersExec は SQL を実行する（Ruby のテストの update / update_all 相当）。
func usersExec(t *testing.T, d *db.DB, q string, args ...any) {
	t.Helper()
	if _, err := d.Exec(context.Background(), q, args...); err != nil {
		t.Fatal(err)
	}
}

// usersCount は User.count（匿名ユーザーを含む users テーブルの行数に相当）。
func usersCount(t *testing.T, d *db.DB) int {
	t.Helper()
	var n int
	if err := d.Get(context.Background(), &n, `SELECT COUNT(*) FROM principals WHERE kind IN ('user', 'anonymous_user')`); err != nil {
		t.Fatal(err)
	}
	return n
}

// usersEnsureAuthSource は auth_sources.yml の 1 件目（LDAP test server）を投入する（testfixtures は auth_sources を投入しない）。
func usersEnsureAuthSource(t *testing.T, d *db.DB) {
	t.Helper()
	usersExec(t, d, `INSERT INTO auth_sources (id, kind, name, created_at, updated_at) SELECT 1, 'ldap', 'LDAP test server', ?, ?
WHERE NOT EXISTS (SELECT 1 FROM auth_sources WHERE id = 1)`, db.NewTime(frozenTime), db.NewTime(frozenTime))
}

var usersGravatarRe = regexp.MustCompile(`\Ahttps://(www\.)?gravatar\.com/avatar/[0-9a-f]{64}\?default=`)

func TestAPIUsersIndex(t *testing.T) {
	srv, ts, d := newFixtureServerFull(t)
	ctx := context.Background()

	t.Run("GET /users.xml should return users", func(t *testing.T) {
		ids := usersActiveByLogin(t, d)
		last := ids[len(ids)-1]
		usersExec(t, d, `UPDATE user_accounts SET twofa_scheme = 'totp' WHERE principal_id = ?`, last)
		defer usersExec(t, d, `UPDATE user_accounts SET twofa_scheme = NULL WHERE principal_id = ?`, last)
		st := srv.App().Settings
		_ = st.Set(ctx, "gravatar_enabled", "1")
		_ = st.Set(ctx, "gravatar_default", "mm")
		defer func() { _ = st.Set(ctx, "gravatar_enabled", "0"); _ = st.Set(ctx, "gravatar_default", "") }()
		res := apiGet(t, ts, "/users.xml", apiCreds("admin"))
		res.expectStatus(t, 200)
		if res.ContentType() != "application/xml" {
			t.Errorf("media type %s", res.ContentType())
		}
		doc := res.XML(t)
		users := xmlSelect(t, doc, "users > user")
		if len(users) != len(ids) {
			t.Fatalf("%d users, want %d", len(users), len(ids))
		}
		for i, u := range users {
			assertXMLText(t, u, "id", fmt.Sprint(ids[i]))
			assertXMLText(t, u, "passwd_changed_on", "")
			av := xmlSelect(t, u, "avatar_url")
			if len(av) != 1 || !usersGravatarRe.MatchString(nodeText(av[0])) || !strings.HasSuffix(nodeText(av[0]), "?default=mm") {
				t.Errorf("user %d avatar_url: %v", ids[i], av)
			}
			want := ""
			if i == len(users)-1 {
				want = "totp"
			}
			assertXMLText(t, u, "twofa_scheme", want)
		}
	})

	t.Run("GET /users.json should return users", func(t *testing.T) {
		ids := usersActiveByLogin(t, d)
		last := ids[len(ids)-1]
		usersExec(t, d, `UPDATE user_accounts SET twofa_scheme = 'totp' WHERE principal_id = ?`, last)
		defer usersExec(t, d, `UPDATE user_accounts SET twofa_scheme = NULL WHERE principal_id = ?`, last)
		res := apiGet(t, ts, "/users.json", apiCreds("admin"))
		res.expectStatus(t, 200)
		if res.ContentType() != "application/json" {
			t.Errorf("media type %s", res.ContentType())
		}
		m := res.JSON(t)
		users, _ := m["users"].([]any)
		if len(users) != len(ids) {
			t.Fatalf("%d users, want %d", len(users), len(ids))
		}
		for i := range users {
			p := fmt.Sprintf("users.%d.", i)
			assertJSON(t, m, p+"id", fmt.Sprint(ids[i]))
			assertJSON(t, m, p+"status", "1")
			assertJSON(t, m, p+"passwd_changed_on", "null")
			want := "null"
			if i == len(users)-1 {
				want = "totp"
			}
			assertJSON(t, m, p+"twofa_scheme", want)
		}
	})

	t.Run("GET /users.json with legacy filter params", func(t *testing.T) {
		count := func(q string) int {
			var n int
			if err := d.Get(ctx, &n, q); err != nil {
				t.Fatal(err)
			}
			return n
		}
		users := func(q string) []any {
			res := apiGet(t, ts, "/users.json?"+q, apiCreds("admin"))
			res.expectStatus(t, 200)
			u, ok := res.JSON(t)["users"].([]any)
			if !ok {
				t.Fatalf("no users: %s", res.Body)
			}
			return u
		}
		if got, want := len(users("status=3")), count(`SELECT COUNT(*) FROM principals WHERE kind = 'user' AND status = 3`); got != want {
			t.Errorf("status=3: %d, want %d", got, want)
		}
		if got, want := len(users("status=*")), count(`SELECT COUNT(*) FROM principals WHERE kind = 'user' AND status <> 0`); got != want {
			t.Errorf("status=*: %d, want %d", got, want)
		}
		if u := users("name=jsmith"); len(u) != 1 || fmt.Sprint(u[0].(map[string]any)["id"]) != "2" {
			t.Errorf("name=jsmith: %v", u)
		}
		if u := users("group_id=10"); len(u) != 1 || fmt.Sprint(u[0].(map[string]any)["id"]) != "8" {
			t.Errorf("group_id=10: %v", u)
		}
		usersExec(t, d, `UPDATE principals SET status = 3 WHERE id IN (2, 8)`)
		defer usersExec(t, d, `UPDATE principals SET status = 1 WHERE id IN (2, 8)`)
		if u := users("name=jsmith"); len(u) != 0 {
			t.Errorf("implicit status=1: name=jsmith: %d", len(u))
		}
		if u := users("group_id=10"); len(u) != 0 {
			t.Errorf("implicit status=1: group_id=10: %d", len(u))
		}
	})

	t.Run("GET /users.json with include=auth_source", func(t *testing.T) {
		usersEnsureAuthSource(t, d)
		usersExec(t, d, `UPDATE user_accounts SET auth_source_id = 1 WHERE principal_id = 2`)
		defer usersExec(t, d, `UPDATE user_accounts SET auth_source_id = NULL WHERE principal_id = 2`)
		res := apiGet(t, ts, "/users.json?include=auth_source", apiCreds("admin"))
		res.expectStatus(t, 200)
		for _, u := range res.JSON(t)["users"].([]any) {
			um := u.(map[string]any)
			src, ok := um["auth_source"].(map[string]any)
			if fmt.Sprint(um["id"]) == "2" {
				if !ok || fmt.Sprint(src["id"]) != "1" || src["name"] != "LDAP test server" {
					t.Errorf("auth_source = %v", um["auth_source"])
				}
			} else if um["auth_source"] != nil {
				t.Errorf("user %v has auth_source", um["id"])
			}
		}
	})

	t.Run("GET /users.json with short filters", func(t *testing.T) {
		var n int
		if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM principals WHERE kind = 'user' AND status IN (1, 3)`); err != nil {
			t.Fatal(err)
		}
		res := apiGet(t, ts, "/users.json?status=1%7C3", apiCreds("admin"))
		res.expectStatus(t, 200)
		if got := len(res.JSON(t)["users"].([]any)); got != n {
			t.Errorf("%d users, want %d", got, n)
		}
	})
}

func TestAPIUsersShow(t *testing.T) {
	srv, ts, d := newFixtureServerFull(t)
	ctx := context.Background()
	gravatar := func(t *testing.T, def string) {
		st := srv.App().Settings
		_ = st.Set(ctx, "gravatar_enabled", "1")
		_ = st.Set(ctx, "gravatar_default", def)
		t.Cleanup(func() { _ = st.Set(ctx, "gravatar_enabled", "0"); _ = st.Set(ctx, "gravatar_default", "") })
	}

	t.Run("GET /users/:id.xml should return the user", func(t *testing.T) {
		gravatar(t, "robohash")
		res := apiGet(t, ts, "/users/2.xml")
		res.expectStatus(t, 200)
		doc := res.XML(t)
		assertXMLText(t, doc, "user id", "2")
		assertXMLText(t, doc, "user updated_on", "2006-07-19T20:42:15Z")
		assertXMLText(t, doc, "user passwd_changed_on", "")
		av := xmlSelect(t, doc, "user avatar_url")
		if len(av) != 1 || !usersGravatarRe.MatchString(nodeText(av[0])) || !strings.HasSuffix(nodeText(av[0]), "?default=robohash") {
			t.Errorf("avatar_url: %s", res.Body)
		}
	})

	t.Run("GET /users/:id.xml should not return avatar_url when not set email address", func(t *testing.T) {
		srv2, ts2, d2 := newFixtureServerFull(t)
		usersExec(t, d2, `DELETE FROM email_addresses WHERE user_id = 2`)
		_ = srv2.App().Settings.Set(ctx, "gravatar_enabled", "1")
		_ = srv2.App().Settings.Set(ctx, "gravatar_default", "robohash")
		res := apiGet(t, ts2, "/users/2.xml")
		res.expectStatus(t, 200)
		doc := res.XML(t)
		assertXMLText(t, doc, "user id", "2")
		assertXMLText(t, doc, "user login", "jsmith")
		assertXMLCount(t, doc, "user avatar_url", 0)
	})

	t.Run("GET /users/:id.json should return the user", func(t *testing.T) {
		res := apiGet(t, ts, "/users/2.json")
		res.expectStatus(t, 200)
		m := res.JSON(t)
		assertJSON(t, m, "user.id", "2")
		assertJSON(t, m, "user.updated_on", "2006-07-19T20:42:15Z")
		assertJSON(t, m, "user.passwd_changed_on", "null")
		for _, k := range []string{"twofa_scheme", "auth_source"} {
			if v, ok := jsonPath(m, "user."+k); ok && v != nil {
				t.Errorf("%s = %v", k, v)
			}
		}
	})

	t.Run("GET /users/:id.xml with include=memberships should include memberships", func(t *testing.T) {
		res := apiGet(t, ts, "/users/2.xml?include=memberships")
		res.expectStatus(t, 200)
		assertXMLCount(t, res.XML(t), "user memberships", 1)
	})

	t.Run("GET /users/:id.json with include=memberships should include memberships", func(t *testing.T) {
		res := apiGet(t, ts, "/users/2.json?include=memberships")
		res.expectStatus(t, 200)
		if !strings.Contains(res.Body, `"memberships":[{"id":1,"project":{"id":1,"name":"eCookbook"},"roles":[{"id":1,"name":"Manager"}]}]`) {
			t.Errorf("memberships: %s", res.Body)
		}
	})

	t.Run("auth_source", func(t *testing.T) {
		usersEnsureAuthSource(t, d)
		usersExec(t, d, `UPDATE user_accounts SET auth_source_id = 1 WHERE principal_id = 2`)
		defer usersExec(t, d, `UPDATE user_accounts SET auth_source_id = NULL WHERE principal_id = 2`)
		// include=auth_source should include auth_source for administrators
		res := apiGet(t, ts, "/users/2.json?include=auth_source", apiCreds("admin"))
		res.expectStatus(t, 200)
		m := res.JSON(t)
		assertJSON(t, m, "user.auth_source.id", "1")
		assertJSON(t, m, "user.auth_source.name", "LDAP test server")
		// without include=auth_source should not include auth_source
		res = apiGet(t, ts, "/users/2.json", apiCreds("admin"))
		if _, ok := jsonPath(res.JSON(t), "user.auth_source"); ok {
			t.Error("auth_source without include")
		}
		// should not include auth_source for standard user
		res = apiGet(t, ts, "/users/2.json?include=auth_source", apiCreds("jsmith"))
		res.expectStatus(t, 200)
		m = res.JSON(t)
		assertJSON(t, m, "user.id", "2")
		if _, ok := jsonPath(m, "user.auth_source"); ok {
			t.Error("auth_source for standard user")
		}
	})

	t.Run("GET /users/current.xml should require authentication", func(t *testing.T) {
		apiGet(t, ts, "/users/current.xml").expectStatus(t, http.StatusUnauthorized)
	})
	t.Run("GET /users/current.xml should return current user", func(t *testing.T) {
		assertXMLText(t, apiGet(t, ts, "/users/current.xml", apiCreds("jsmith")).XML(t), "user id", "2")
	})
	t.Run("visibility of attributes", func(t *testing.T) {
		other := apiGet(t, ts, "/users/3.xml", apiCreds("jsmith"))
		other.expectStatus(t, 200)
		od := other.XML(t)
		assertXMLText(t, od, "user login", "dlopper")
		assertXMLCount(t, od, "user api_key", 0)
		assertXMLCount(t, od, "user status", 0)
		assertXMLCount(t, od, "user admin", 0)

		self := apiGet(t, ts, "/users/2.xml", apiCreds("jsmith"))
		self.expectStatus(t, 200)
		key, err := repository.APIKey(ctx, d, 2)
		if err != nil {
			t.Fatal(err)
		}
		sd := self.XML(t)
		assertXMLText(t, sd, "user api_key", key)
		assertXMLText(t, sd, "user admin", "false")

		admin := apiGet(t, ts, "/users/2.xml", apiCreds("admin"))
		admin.expectStatus(t, 200)
		assertXMLText(t, admin.XML(t), "user status", "1")
	})
	t.Run("twofa_scheme", func(t *testing.T) {
		token := authCreateToken(t, d, 2, "api")
		usersExec(t, d, `UPDATE user_accounts SET twofa_scheme = 'totp' WHERE principal_id = 2`)
		defer usersExec(t, d, `UPDATE user_accounts SET twofa_scheme = NULL WHERE principal_id = 2`)
		res := apiGet(t, ts, "/users/3.xml", apiBasic(token, "X"))
		res.expectStatus(t, 200)
		assertXMLCount(t, res.XML(t), "twofa_scheme", 0)
		res = apiGet(t, ts, "/users/2.xml", apiCreds("admin"))
		res.expectStatus(t, 200)
		assertXMLText(t, res.XML(t), "twofa_scheme", "totp")
	})
}

func TestAPIUsersWrite(t *testing.T) {
	t.Run("POST /users.xml with valid parameters should create the user", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		before := usersCount(t, d)
		res := apiCall(t, ts, http.MethodPost, "/users.xml", "application/x-www-form-urlencoded",
			"user[login]=foo&user[firstname]=Firstname&user[lastname]=Lastname&user[mail]=foo@example.net&user[password]=secret123&user[mail_notification]=only_assigned",
			apiCreds("admin"))
		res.expectStatus(t, http.StatusCreated)
		if usersCount(t, d) != before+1 {
			t.Error("user not created")
		}
		if res.ContentType() != "application/xml" {
			t.Errorf("media type %s", res.ContentType())
		}
		var u struct {
			ID        int64  `db:"id"`
			Login     string `db:"login"`
			Firstname string `db:"firstname"`
			Lastname  string `db:"lastname"`
			Admin     bool   `db:"admin"`
			Notif     string `db:"mail_notification"`
		}
		if err := d.Get(context.Background(), &u, `SELECT p.id, u.login, p.firstname, p.lastname, u.admin, COALESCE(n.mail_notification, '') AS mail_notification
FROM principals p JOIN user_accounts u ON u.principal_id = p.id LEFT JOIN user_notification_settings n ON n.user_id = p.id
ORDER BY p.id DESC LIMIT 1`); err != nil {
			t.Fatal(err)
		}
		if u.Login != "foo" || u.Firstname != "Firstname" || u.Lastname != "Lastname" || u.Admin || u.Notif != "only_assigned" {
			t.Errorf("created user %+v", u)
		}
		var mail string
		_ = d.Get(context.Background(), &mail, `SELECT address FROM email_addresses WHERE user_id = ? AND is_default = ?`, u.ID, true)
		if mail != "foo@example.net" {
			t.Errorf("mail %q", mail)
		}
		assertXMLText(t, res.XML(t), "user id", fmt.Sprint(u.ID))
		// check_password?('secret123')
		apiGet(t, ts, "/users/current.json", apiBasic("foo", "secret123")).expectStatus(t, 200)
	})

	t.Run("POST /users.xml with generate_password should generate password", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		res := apiCall(t, ts, http.MethodPost, "/users.xml", "application/x-www-form-urlencoded",
			"user[login]=foo&user[firstname]=Firstname&user[lastname]=Lastname&user[mail]=foo@example.net&user[generate_password]=true",
			apiCreds("admin"))
		res.expectStatus(t, http.StatusCreated)
		var hash *string
		if err := d.Get(context.Background(), &hash, `SELECT password_hash FROM user_accounts WHERE login = 'foo'`); err != nil {
			t.Fatal(err)
		}
		if hash == nil || *hash == "" {
			t.Error("password not generated")
		}
	})

	t.Run("POST /users.json with valid parameters should create the user", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		before := usersCount(t, d)
		res := apiCall(t, ts, http.MethodPost, "/users.json", "",
			`{"user":{"login":"foo","firstname":"Firstname","lastname":"Lastname","mail":"foo@example.net","password":"secret123","mail_notification":"only_assigned"}}`,
			apiCreds("admin"))
		res.expectStatus(t, http.StatusCreated)
		if usersCount(t, d) != before+1 {
			t.Error("user not created")
		}
		if res.ContentType() != "application/json" {
			t.Errorf("media type %s", res.ContentType())
		}
		var id int64
		_ = d.Get(context.Background(), &id, `SELECT principal_id FROM user_accounts WHERE login = 'foo'`)
		assertJSON(t, res.JSON(t), "user.id", fmt.Sprint(id))
	})

	t.Run("POST/PUT /users.json with custom_fields should save the custom values", func(t *testing.T) {
		// API の custom_fields（[{"id":..,"value":..}]。acts_as_customizable の custom_fields=）
		ts, _ := newFixtureServer(t)
		res := apiCall(t, ts, http.MethodPost, "/users.json", "",
			`{"user":{"login":"foo","firstname":"Firstname","lastname":"Lastname","mail":"foo@example.net","password":"secret123","custom_fields":[{"id":4,"value":"01 23 45 67"}]}}`,
			apiCreds("admin"))
		res.expectStatus(t, http.StatusCreated)
		m := res.JSON(t)
		assertJSON(t, m, "user.custom_fields.0.id", "4")
		assertJSON(t, m, "user.custom_fields.0.value", "01 23 45 67")
		id, _ := jsonPath(m, "user.id")
		path := fmt.Sprintf("/users/%v.json", id)
		apiCall(t, ts, http.MethodPut, path, "", `{"user":{"custom_fields":[{"id":4,"value":"89"}]}}`, apiCreds("admin")).expectStatus(t, http.StatusNoContent)
		assertJSON(t, apiGet(t, ts, path, apiCreds("admin")).JSON(t), "user.custom_fields.0.value", "89")
	})

	ts, d := newFixtureServer(t)
	for _, f := range []string{"xml", "json"} {
		t.Run("POST /users."+f+" with invalid parameters should return errors", func(t *testing.T) {
			before := usersCount(t, d)
			res := apiCall(t, ts, http.MethodPost, "/users."+f, "application/x-www-form-urlencoded",
				"user[login]=foo&user[lastname]=Lastname&user[mail]=foo", apiCreds("admin"))
			res.expectStatus(t, http.StatusUnprocessableEntity)
			if usersCount(t, d) != before {
				t.Error("user created")
			}
			usersCheckErrors(t, f, res)
		})
		t.Run("PUT /users/:id."+f+" with valid parameters should update the user", func(t *testing.T) {
			before := usersCount(t, d)
			res := apiCall(t, ts, http.MethodPut, "/users/2."+f, "application/x-www-form-urlencoded",
				"user[login]=jsmith&user[firstname]=John&user[lastname]=Renamed&user[mail]=jsmith@somenet.foo", apiCreds("admin"))
			res.expectStatus(t, http.StatusNoContent)
			if res.Body != "" || usersCount(t, d) != before {
				t.Errorf("body %q", res.Body)
			}
			var ln string
			_ = d.Get(context.Background(), &ln, `SELECT lastname FROM principals WHERE id = 2`)
			if ln != "Renamed" {
				t.Errorf("lastname %q", ln)
			}
			usersExec(t, d, `UPDATE principals SET lastname = 'Smith' WHERE id = 2`)
		})
		t.Run("PUT /users/:id."+f+" with invalid parameters", func(t *testing.T) {
			res := apiCall(t, ts, http.MethodPut, "/users/2."+f, "application/x-www-form-urlencoded",
				"user[login]=jsmith&user[firstname]=&user[lastname]=Lastname&user[mail]=foo", apiCreds("admin"))
			res.expectStatus(t, http.StatusUnprocessableEntity)
			usersCheckErrors(t, f, res)
		})
	}
	for _, f := range []string{"xml", "json"} {
		t.Run("DELETE /users/:id."+f+" should delete the user", func(t *testing.T) {
			ts, d := newFixtureServer(t)
			before := usersCount(t, d)
			res := apiCall(t, ts, http.MethodDelete, "/users/2."+f, "", "", apiCreds("admin"))
			res.expectStatus(t, http.StatusNoContent)
			if res.Body != "" {
				t.Errorf("body %q", res.Body)
			}
			if usersCount(t, d) != before-1 {
				t.Error("user not deleted")
			}
		})
	}
}

// usersCheckErrors は 422 の本文（errors 配列に "First name cannot be blank"）を確認する。
func usersCheckErrors(t *testing.T, f string, res apiResp) {
	t.Helper()
	if res.ContentType() != "application/"+f {
		t.Errorf("media type %s", res.ContentType())
	}
	if f == "xml" {
		found := false
		for _, n := range xmlSelect(t, res.XML(t), "errors error") {
			if nodeText(n) == "First name cannot be blank" {
				found = true
			}
		}
		if !found {
			t.Errorf("errors: %s", res.Body)
		}
		return
	}
	if _, ok := res.JSON(t)["errors"].([]any); !ok {
		t.Errorf("errors: %s", res.Body)
	}
}
