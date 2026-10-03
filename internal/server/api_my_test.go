// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

// test/integration/api_test/my_test.rb の移植。

import (
	"context"
	"net/http"
	"testing"
)

func TestAPIMy(t *testing.T) {
	t.Run("GET /my/account.json should return user", func(t *testing.T) {
		ts, _ := newFixtureServer(t)
		res := apiGet(t, ts, "/my/account.json", apiCreds("dlopper"))
		res.expectStatus(t, 200)
		if res.ContentType() != "application/json" {
			t.Errorf("media type %s", res.ContentType())
		}
		assertJSON(t, res.JSON(t), "user.login", "dlopper")
	})

	for _, path := range []string{"/my/account.xml", "/my/account.json"} {
		t.Run("PUT "+path+" with valid parameters should update the user", func(t *testing.T) {
			ts, d := newFixtureServer(t)
			// Ruby のテストは .json の場合も .xml に PUT している（そのまま移植せず両形式を確認する）
			res := apiCall(t, ts, http.MethodPut, path, "application/x-www-form-urlencoded",
				"user[firstname]=Dave&user[lastname]=Renamed&user[mail]=dave@somenet.foo", apiCreds("dlopper"))
			res.expectStatus(t, http.StatusNoContent)
			if res.Body != "" {
				t.Errorf("body %q", res.Body)
			}
			var u struct {
				ID        int64  `db:"id"`
				Firstname string `db:"firstname"`
				Admin     bool   `db:"admin"`
			}
			if err := d.Get(context.Background(), &u, `SELECT p.id, p.firstname, a.admin FROM principals p
JOIN user_accounts a ON a.principal_id = p.id WHERE p.lastname = 'Renamed'`); err != nil {
				t.Fatal(err)
			}
			var mail string
			_ = d.Get(context.Background(), &mail, `SELECT address FROM email_addresses WHERE user_id = ? AND is_default = ?`, u.ID, true)
			if u.Firstname != "Dave" || mail != "dave@somenet.foo" || u.Admin {
				t.Errorf("user %+v mail %q", u, mail)
			}
		})
	}

	ts, _ := newFixtureServer(t)
	for _, f := range []string{"xml", "json"} {
		t.Run("PUT /my/account."+f+" with invalid parameters", func(t *testing.T) {
			res := apiCall(t, ts, http.MethodPut, "/my/account."+f, "application/x-www-form-urlencoded",
				"user[login]=dlopper&user[firstname]=&user[lastname]=Lastname", apiCreds("dlopper"))
			res.expectStatus(t, http.StatusUnprocessableEntity)
			usersCheckErrors(t, f, res)
		})
	}
}
