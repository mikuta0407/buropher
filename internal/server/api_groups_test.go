package server_test

// test/integration/api_test/groups_test.rb の移植。

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
)

var groupsSeq int

// groupsGenerate は Group.generate!（名前 "Group N"）を API で作り id を返す。
func groupsGenerate(t *testing.T, ts *httptest.Server) int64 {
	t.Helper()
	groupsSeq++
	res := apiCall(t, ts, http.MethodPost, "/groups.json", "", fmt.Sprintf(`{"group":{"name":"Group %d"}}`, groupsSeq), apiCreds("admin"))
	res.expectStatus(t, http.StatusCreated)
	v, _ := jsonPath(res.JSON(t), "group.id")
	var id int64
	fmt.Sscan(fmt.Sprint(v), &id)
	return id
}

// groupsUserIDs は group.users の id（昇順）。
func groupsUserIDs(t *testing.T, d *db.DB, gid int64) []int64 {
	t.Helper()
	var ids []int64
	if err := d.Select(context.Background(), &ids, `SELECT user_id FROM group_users WHERE group_id = ? ORDER BY user_id`, gid); err != nil {
		t.Fatal(err)
	}
	return ids
}

// groupsCount は Group.count（givable なグループ）。
func groupsCount(t *testing.T, d *db.DB) int {
	t.Helper()
	var n int
	if err := d.Get(context.Background(), &n, `SELECT COUNT(*) FROM principals WHERE kind = 'group'`); err != nil {
		t.Fatal(err)
	}
	return n
}

// groupsHasChild は sel に一致する要素のうち、子要素 child のテキストが want のものがあるか。
func groupsHasChild(t *testing.T, res apiResp, sel string, pairs ...string) bool {
	t.Helper()
	for _, n := range xmlSelect(t, res.XML(t), sel) {
		ok := true
		for i := 0; i+1 < len(pairs); i += 2 {
			c := xmlSelect(t, n, pairs[i])
			if len(c) == 0 || nodeText(c[0]) != pairs[i+1] {
				ok = false
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func TestAPIGroupsRead(t *testing.T) {
	ts, d := newFixtureServer(t)

	t.Run("GET /groups.xml should require authentication", func(t *testing.T) {
		apiGet(t, ts, "/groups.xml").expectStatus(t, http.StatusUnauthorized)
	})
	t.Run("GET /groups.xml should return givable groups", func(t *testing.T) {
		res := apiGet(t, ts, "/groups.xml", apiCreds("admin"))
		res.expectStatus(t, 200)
		if res.ContentType() != "application/xml" {
			t.Errorf("media type %s", res.ContentType())
		}
		assertXMLCount(t, res.XML(t), "groups > group", groupsCount(t, d))
		if !groupsHasChild(t, res, "groups > group", "name", "A Team", "id", "10") {
			t.Errorf("A Team missing: %s", res.Body)
		}
	})
	t.Run("GET /groups.xml?builtin=1 should return all groups", func(t *testing.T) {
		res := apiGet(t, ts, "/groups.xml?builtin=1", apiCreds("admin"))
		res.expectStatus(t, 200)
		assertXMLCount(t, res.XML(t), "groups > group", groupsCount(t, d)+2)
		if !groupsHasChild(t, res, "groups > group", "builtin", "non_member", "id", "12") ||
			!groupsHasChild(t, res, "groups > group", "builtin", "anonymous", "id", "13") {
			t.Errorf("builtin groups: %s", res.Body)
		}
	})
	t.Run("GET /groups.json should require authentication", func(t *testing.T) {
		apiGet(t, ts, "/groups.json").expectStatus(t, http.StatusUnauthorized)
	})
	t.Run("GET /groups.json should return groups", func(t *testing.T) {
		res := apiGet(t, ts, "/groups.json", apiCreds("admin"))
		res.expectStatus(t, 200)
		if res.ContentType() != "application/json" {
			t.Errorf("media type %s", res.ContentType())
		}
		if !regexp.MustCompile(`\{"id":10,"name":"A Team"\}`).MatchString(res.Body) {
			t.Errorf("A Team: %s", res.Body)
		}
	})
	t.Run("GET /groups/:id.xml should return the group", func(t *testing.T) {
		res := apiGet(t, ts, "/groups/10.xml", apiCreds("admin"))
		res.expectStatus(t, 200)
		if !groupsHasChild(t, res, "group", "name", "A Team", "id", "10") {
			t.Errorf("group: %s", res.Body)
		}
	})
	t.Run("GET /groups/:id.xml should return the builtin group", func(t *testing.T) {
		res := apiGet(t, ts, "/groups/12.xml", apiCreds("admin"))
		res.expectStatus(t, 200)
		if !groupsHasChild(t, res, "group", "builtin", "non_member", "id", "12") {
			t.Errorf("group: %s", res.Body)
		}
	})
	t.Run("GET /groups/:id.xml should include users if requested", func(t *testing.T) {
		res := apiGet(t, ts, "/groups/10.xml?include=users", apiCreds("admin"))
		res.expectStatus(t, 200)
		doc := res.XML(t)
		assertXMLCount(t, doc, "group users user", len(groupsUserIDs(t, d, 10)))
		assertXMLCount(t, doc, `group users user[id="8"]`, 1)
	})
	t.Run("GET /groups/:id.xml include memberships if requested", func(t *testing.T) {
		res := apiGet(t, ts, "/groups/10.xml?include=memberships", apiCreds("admin"))
		res.expectStatus(t, 200)
		assertXMLCount(t, res.XML(t), "group memberships", 1)
	})
}

func TestAPIGroupsWrite(t *testing.T) {
	ts, d := newFixtureServer(t)
	nameBlank := regexp.MustCompile(`Name cannot be blank`)
	hasError := func(t *testing.T, res apiResp, re *regexp.Regexp) {
		t.Helper()
		for _, n := range xmlSelect(t, res.XML(t), "errors error") {
			if re.MatchString(nodeText(n)) {
				return
			}
		}
		t.Errorf("errors: %s", res.Body)
	}

	t.Run("POST /groups.xml with valid parameters should create the group", func(t *testing.T) {
		before := groupsCount(t, d)
		res := apiCall(t, ts, http.MethodPost, "/groups.xml", "application/x-www-form-urlencoded",
			"group[name]=Test&group[user_ids][]=2&group[user_ids][]=3", apiCreds("admin"))
		res.expectStatus(t, http.StatusCreated)
		if res.ContentType() != "application/xml" {
			t.Errorf("media type %s", res.ContentType())
		}
		if groupsCount(t, d) != before+1 {
			t.Fatal("group not created")
		}
		var id int64
		_ = d.Get(context.Background(), &id, `SELECT id FROM principals WHERE kind = 'group' ORDER BY id DESC LIMIT 1`)
		if got := groupsUserIDs(t, d, id); !slices.Equal(got, []int64{2, 3}) {
			t.Errorf("users %v", got)
		}
		assertXMLText(t, res.XML(t), "group name", "Test")
	})
	t.Run("POST /groups.xml with invalid parameters should return errors", func(t *testing.T) {
		before := groupsCount(t, d)
		res := apiCall(t, ts, http.MethodPost, "/groups.xml", "application/x-www-form-urlencoded", "group[name]=", apiCreds("admin"))
		res.expectStatus(t, http.StatusUnprocessableEntity)
		if groupsCount(t, d) != before {
			t.Error("group created")
		}
		if res.ContentType() != "application/xml" {
			t.Errorf("media type %s", res.ContentType())
		}
		hasError(t, res, nameBlank)
	})
	t.Run("PUT /groups/:id.xml with valid parameters should update the group", func(t *testing.T) {
		id := groupsGenerate(t, ts)
		res := apiCall(t, ts, http.MethodPut, fmt.Sprintf("/groups/%d.xml", id), "application/x-www-form-urlencoded",
			"group[name]=New name&group[user_ids][]=2&group[user_ids][]=3", apiCreds("admin"))
		res.expectStatus(t, http.StatusNoContent)
		if res.Body != "" {
			t.Errorf("body %q", res.Body)
		}
		var name string
		_ = d.Get(context.Background(), &name, `SELECT name FROM principals WHERE id = ?`, id)
		if name != "New name" {
			t.Errorf("name %q", name)
		}
		if got := groupsUserIDs(t, d, id); !slices.Equal(got, []int64{2, 3}) {
			t.Errorf("users %v", got)
		}
	})
	t.Run("PUT /groups/:id.xml with invalid parameters should return errors", func(t *testing.T) {
		id := groupsGenerate(t, ts)
		res := apiCall(t, ts, http.MethodPut, fmt.Sprintf("/groups/%d.xml", id), "application/x-www-form-urlencoded", "group[name]=", apiCreds("admin"))
		res.expectStatus(t, http.StatusUnprocessableEntity)
		if res.ContentType() != "application/xml" {
			t.Errorf("media type %s", res.ContentType())
		}
		hasError(t, res, nameBlank)
	})
	t.Run("DELETE /groups/:id.xml should delete the group", func(t *testing.T) {
		id := groupsGenerate(t, ts)
		before := groupsCount(t, d)
		res := apiCall(t, ts, http.MethodDelete, fmt.Sprintf("/groups/%d.xml", id), "", "", apiCreds("admin"))
		res.expectStatus(t, http.StatusNoContent)
		if res.Body != "" || groupsCount(t, d) != before-1 {
			t.Errorf("not deleted (body %q)", res.Body)
		}
	})
	t.Run("POST /groups/:id/users.xml should add user to the group", func(t *testing.T) {
		id := groupsGenerate(t, ts)
		res := apiCall(t, ts, http.MethodPost, fmt.Sprintf("/groups/%d/users.xml", id), "application/x-www-form-urlencoded", "user_id=5", apiCreds("admin"))
		res.expectStatus(t, http.StatusNoContent)
		if res.Body != "" {
			t.Errorf("body %q", res.Body)
		}
		if got := groupsUserIDs(t, d, id); !slices.Equal(got, []int64{5}) {
			t.Errorf("users %v", got)
		}
	})
	t.Run("POST /groups/:id/users.xml should not add the user if already added", func(t *testing.T) {
		id := groupsGenerate(t, ts)
		usersExec(t, d, `INSERT INTO group_users (group_id, user_id) VALUES (?, 5)`, id)
		res := apiCall(t, ts, http.MethodPost, fmt.Sprintf("/groups/%d/users.xml", id), "application/x-www-form-urlencoded", "user_id=5", apiCreds("admin"))
		res.expectStatus(t, http.StatusUnprocessableEntity)
		if got := groupsUserIDs(t, d, id); len(got) != 1 {
			t.Errorf("users %v", got)
		}
		hasError(t, res, regexp.MustCompile(`User is invalid`))
	})
	t.Run("DELETE /groups/:id/users/:user_id.xml should remove user from the group", func(t *testing.T) {
		id := groupsGenerate(t, ts)
		usersExec(t, d, `INSERT INTO group_users (group_id, user_id) VALUES (?, 8)`, id)
		res := apiCall(t, ts, http.MethodDelete, fmt.Sprintf("/groups/%d/users/8.xml", id), "", "", apiCreds("admin"))
		res.expectStatus(t, http.StatusNoContent)
		if res.Body != "" {
			t.Errorf("body %q", res.Body)
		}
		if got := groupsUserIDs(t, d, id); len(got) != 0 {
			t.Errorf("users %v", got)
		}
	})
}
