package server_test

// Redmine の test/integration/api_test/memberships_test.rb の移植。

import (
	"encoding/json"
	"reflect"
	"testing"
)

// membershipsJSON は memberships/show.api.rsb の 1 件（project / user / roles のみ。group は無いもの）。
type membershipsJSON struct {
	ID      int64          `json:"id"`
	Project map[string]any `json:"project"`
	User    map[string]any `json:"user"`
	Roles   []any          `json:"roles"`
}

func TestAPIMemberships(t *testing.T) {
	ts, _ := newFixtureServer(t)
	t.Run("GET /projects/:project_id/memberships.xml should return memberships", func(t *testing.T) {
		r := apiGet(t, ts, "/projects/1/memberships.xml", apiCreds("jsmith"))
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/xml")
		doc := r.XML(t)
		var found bool
		for _, m := range xmlSelect(t, doc, "memberships[type=array] > membership") {
			if nodeText(xmlSelect(t, m, "id")[0]) != "2" {
				continue
			}
			found = true
			assertXMLCount(t, m, `user[id="3"][name="Dave Lopper"]`, 1)
			assertXMLCount(t, m, `roles role[id="2"][name=Developer]`, 1)
		}
		if !found {
			t.Error("membership 2 not found")
		}
	})
	t.Run("GET /projects/:project_id/memberships.json should return memberships", func(t *testing.T) {
		r := apiGet(t, ts, "/projects/1/memberships.json", apiCreds("jsmith"))
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/json")
		m := r.JSON(t)
		assertJSON(t, m, "total_count", "3")
		assertJSON(t, m, "limit", "25")
		assertJSON(t, m, "offset", "0")
		var v struct {
			Memberships []membershipsJSON `json:"memberships"`
		}
		if err := json.Unmarshal([]byte(r.Body), &v); err != nil {
			t.Fatal(err)
		}
		want := membershipsJSON{ID: 1,
			Project: map[string]any{"name": "eCookbook", "id": float64(1)},
			Roles:   []any{map[string]any{"name": "Manager", "id": float64(1)}},
			User:    map[string]any{"name": "John Smith", "id": float64(2)},
		}
		var found bool
		for _, ms := range v.Memberships {
			found = found || reflect.DeepEqual(ms, want)
		}
		if !found {
			t.Errorf("membership 1 not included: %s", r.Body)
		}
	})
	t.Run("GET /memberships/:id.xml should return the membership", func(t *testing.T) {
		r := apiGet(t, ts, "/memberships/2.xml", apiCreds("jsmith"))
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/xml")
		doc := r.XML(t)
		assertXMLText(t, doc, "membership > id", "2")
		assertXMLCount(t, doc, `membership > user[id="3"][name="Dave Lopper"]`, 1)
		assertXMLCount(t, doc, `membership > roles role[id="2"][name=Developer]`, 1)
	})
	t.Run("GET /memberships/:id.json should return the membership", func(t *testing.T) {
		r := apiGet(t, ts, "/memberships/2.json", apiCreds("jsmith"))
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/json")
		// キー順も Redmine と同じ（id, project, user, roles）
		want := `{"membership":{"id":2,"project":{"id":1,"name":"eCookbook"},"user":{"id":3,"name":"Dave Lopper"},"roles":[{"id":2,"name":"Developer"}]}}`
		if r.Body != want {
			t.Errorf("body = %s\nwant   %s", r.Body, want)
		}
	})

	t.Run("GET /projects/:project_id/memberships.xml should succeed for closed project", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		mastersExec(t, d, `UPDATE projects SET status = 5 WHERE id = 1`)
		apiGet(t, ts, "/projects/1/memberships.json", apiCreds("jsmith")).expectStatus(t, 200)
	})
	t.Run("GET /projects/:project_id/memberships.xml should include locked users", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		mastersExec(t, d, `UPDATE principals SET status = 3 WHERE id = 3`)
		r := apiGet(t, ts, "/projects/ecookbook/memberships.xml", apiCreds("jsmith"))
		r.expectStatus(t, 200)
		var found bool
		for _, m := range xmlSelect(t, r.XML(t), "memberships[type=array] > membership") {
			if nodeText(xmlSelect(t, m, "id")[0]) == "2" {
				found = true
				assertXMLCount(t, m, `user[id="3"][name="Dave Lopper"]`, 1)
			}
		}
		if !found {
			t.Error("membership 2 not found")
		}
	})
	t.Run("POST /projects/:project_id/memberships.xml should create the membership", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		before := mastersInt(t, d, `SELECT COUNT(*) FROM members`)
		r := mastersForm(t, ts, "POST", "/projects/1/memberships.xml",
			"membership[user_id]=7&membership[role_ids][]=2&membership[role_ids][]=3", apiCreds("jsmith"))
		r.expectStatus(t, 201)
		if n := mastersInt(t, d, `SELECT COUNT(*) FROM members`); n != before+1 {
			t.Errorf("members %d -> %d", before, n)
		}
	})
	t.Run("POST /projects/:project_id/memberships.xml should create the group membership", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		before := mastersInt(t, d, `SELECT COUNT(*) FROM members`)
		users := mastersInt(t, d, `SELECT COUNT(*) FROM group_users WHERE group_id = 11`)
		r := mastersForm(t, ts, "POST", "/projects/1/memberships.xml",
			"membership[user_id]=11&membership[role_ids][]=2&membership[role_ids][]=3", apiCreds("jsmith"))
		r.expectStatus(t, 201)
		if n := mastersInt(t, d, `SELECT COUNT(*) FROM members`); n != before+1+users {
			t.Errorf("members %d -> %d (group users %d)", before, n, users)
		}
	})
	t.Run("POST /projects/:project_id/memberships.xml with invalid parameters should return errors", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		before := mastersInt(t, d, `SELECT COUNT(*) FROM members`)
		r := mastersForm(t, ts, "POST", "/projects/1/memberships.xml",
			"membership[role_ids][]=2&membership[role_ids][]=3", apiCreds("jsmith"))
		r.expectStatus(t, 422)
		mastersMedia(t, r, "application/xml")
		if len(xmlSelect(t, r.XML(t), `errors error:contains("User or Group cannot be blank")`)) == 0 {
			t.Errorf("errors: %s", r.Body)
		}
		if n := mastersInt(t, d, `SELECT COUNT(*) FROM members`); n != before {
			t.Error("member created")
		}
	})
	t.Run("PUT /memberships/:id.xml should update the membership", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		before := mastersInt(t, d, `SELECT COUNT(*) FROM members`)
		r := mastersForm(t, ts, "PUT", "/memberships/2.xml",
			"membership[user_id]=3&membership[role_ids][]=1&membership[role_ids][]=2", apiCreds("jsmith"))
		mastersNoContent(t, r)
		if n := mastersInt(t, d, `SELECT COUNT(*) FROM members`); n != before {
			t.Error("member count changed")
		}
		if s := mastersStr(t, d, `SELECT group_concat(role_id, ',') FROM (SELECT role_id FROM member_roles WHERE member_id = 2 ORDER BY role_id)`); s != "1,2" {
			t.Errorf("roles = %s", s)
		}
	})
	t.Run("PUT /memberships/:id.xml with invalid parameters should return errors", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		r := mastersForm(t, ts, "PUT", "/memberships/2.xml",
			"membership[user_id]=3&membership[role_ids][]=99", apiCreds("jsmith"))
		r.expectStatus(t, 422)
		mastersMedia(t, r, "application/xml")
		if len(xmlSelect(t, r.XML(t), `errors error:contains("Role cannot be empty")`)) == 0 {
			t.Errorf("errors: %s", r.Body)
		}
		// Member#role_ids= で外したロールの member_roles が destroy され、MemberRole#remove_member_if_empty が
		// メンバーを削除する（応答は 422 のまま）
		if n := mastersInt(t, d, `SELECT COUNT(*) FROM members WHERE id = 2`); n != 0 {
			t.Error("member 2 should be removed by remove_member_if_empty")
		}
	})
	t.Run("DELETE /memberships/:id.xml should destroy the membership", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		mastersNoContent(t, apiCall(t, ts, "DELETE", "/memberships/2.xml", "", "", apiCreds("jsmith")))
		if n := mastersInt(t, d, `SELECT COUNT(*) FROM members WHERE id = 2`); n != 0 {
			t.Error("member 2 still exists")
		}
	})
	t.Run("DELETE /memberships/:id.xml should respond with 422 on failure", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		// 継承したロールを持つメンバーは削除できない（inherited_from = 99 は存在しない member_roles を指すため FK を外して設定する）
		mastersExec(t, d, `PRAGMA foreign_keys = OFF`)
		mastersExec(t, d, `UPDATE member_roles SET inherited_from = 99 WHERE id = (SELECT MIN(id) FROM member_roles WHERE member_id = 2)`)
		mastersExec(t, d, `PRAGMA foreign_keys = ON`)
		r := apiCall(t, ts, "DELETE", "/memberships/2.xml", "", "", apiCreds("jsmith"))
		r.expectStatus(t, 422)
		if n := mastersInt(t, d, `SELECT COUNT(*) FROM members WHERE id = 2`); n != 1 {
			t.Error("member 2 deleted")
		}
	})
}
