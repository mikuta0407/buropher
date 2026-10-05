// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// Redmine 7.0 #43640: ユーザー一覧のコンテキストメニューからのグループへの追加・削除と groups#remove_users
// （test/functional/groups_controller_test.rb、test/functional/context_menus/users_controller_test.rb）。

func TestGroupsRemoveUsers(t *testing.T) {
	ts, d := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")
	count := func() int { return uQueryInt(t, d, `SELECT COUNT(*) FROM group_users WHERE group_id = 10`) }

	// test_edit: ユーザーとメンバーシップのタブの削除リンクは「Remove」（icon-link-break）
	_, body := get(t, c, ts.URL+"/groups/10/edit")
	for _, want := range []string{
		`<a class="icon icon-link-break" rel="nofollow" data-method="delete" href="/groups/10/users?user_id=8">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("edit: missing %s", want)
		}
	}

	// test_remove_users_without_confirmation: 確認画面
	res, body := send(t, c, ts.URL, http.MethodDelete, "/groups/10/users", url.Values{"user_id": {"8"}})
	if res.StatusCode != 200 || count() != 1 || !strings.Contains(body, `name="confirm"`) {
		t.Fatalf("without confirmation: %d count=%d", res.StatusCode, count())
	}
	for _, want := range []string{
		"<h2>Confirmation</h2>",
		`<p><p>Are you sure you want to remove the selected user(s) from the group <strong>A Team</strong>?</p></p>`,
		"<p><strong>User Misc</strong> (miscuser8)</p>",
		`<a href="/users">Cancel</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("confirmation page: missing %s", want)
		}
	}

	// test_remove_users_should_only_include_group_members
	_, body = send(t, c, ts.URL, http.MethodDelete, "/groups/10/users", url.Values{"user_ids[]": {"3", "8"}})
	if !strings.Contains(body, "<strong>User Misc</strong>") || strings.Contains(body, "<strong>Dave Lopper</strong>") {
		t.Error("only group members should be listed")
	}
	// メンバーでないユーザーだけなら 404
	if res, _ := send(t, c, ts.URL, http.MethodDelete, "/groups/10/users", url.Values{"user_ids[]": {"3"}}); res.StatusCode != 404 {
		t.Errorf("non member: %d", res.StatusCode)
	}

	// test_remove_users_should_preserve_back_url_through_confirmation
	_, body = send(t, c, ts.URL, http.MethodDelete, "/groups/10/users", url.Values{"user_id": {"8"}, "back_url": {"/users"}})
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	assertXMLCount(t, doc, `form[action="/groups/10/users?user_ids%5B%5D=8"] input[name=back_url][value="/users"]`, 1)
	// test_remove_users_without_confirmation_with_unsafe_back_url_should_ignore_back_url
	_, body = send(t, c, ts.URL, http.MethodDelete, "/groups/10/users", url.Values{"user_id": {"8"}, "back_url": {"http://example.com/issues"}})
	doc, _ = html.Parse(strings.NewReader(body))
	assertXMLCount(t, doc, `form[action="/groups/10/users?user_ids%5B%5D=8"]`, 1)
	assertXMLCount(t, doc, `input[name=back_url]`, 0)

	// test_remove_users_with_unsafe_back_url_should_redirect_to_default_url
	res, _ = send(t, c, ts.URL, http.MethodDelete, "/groups/10/users", url.Values{"user_id": {"8"}, "confirm": {"Yes"}, "back_url": {"http://example.com/issues"}})
	if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/groups/10/edit?tab=users") || count() != 0 {
		t.Fatalf("remove: %d %s count=%d", res.StatusCode, res.Header.Get("Location"), count())
	}
	_, body = get(t, c, ts.URL+"/groups/10/edit?tab=users")
	if !strings.Contains(body, "Successful deletion.") {
		t.Error("flash notice missing after remove")
	}

	// test_add_users_from_context_menu_should_redirect_to_back_url
	res, _ = send(t, c, ts.URL, http.MethodPost, "/groups/10/users", url.Values{"user_ids[]": {"2", "8"}, "back_url": {"/users"}})
	if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/users") || count() != 2 {
		t.Fatalf("add users: %d %s count=%d", res.StatusCode, res.Header.Get("Location"), count())
	}
	_, body = get(t, c, ts.URL+"/users")
	if !strings.Contains(body, "Successful update.") {
		t.Error("flash notice missing after add")
	}

	// test_remove_users_plural（back_url は確認後も保持される）
	res, _ = send(t, c, ts.URL, http.MethodDelete, "/groups/10/users", url.Values{"user_ids[]": {"2", "8"}, "confirm": {"Yes"}, "back_url": {"/users"}})
	if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/users") || count() != 0 {
		t.Fatalf("remove plural: %d %s count=%d", res.StatusCode, res.Header.Get("Location"), count())
	}

	// 非推奨の remove_user（DELETE groups/:id/users/:user_id）も確認が必要
	mastersExec(t, d, `INSERT INTO group_users (group_id, user_id) VALUES (10, 8)`)
	if res, _ := send(t, c, ts.URL, http.MethodDelete, "/groups/10/users/8", nil); res.StatusCode != 200 || count() != 1 {
		t.Errorf("remove_user without confirmation: %d count=%d", res.StatusCode, count())
	}
	// js では確認画面が無く 204（暗黙の head :no_content）
	if res, _ := send(t, c, ts.URL, http.MethodDelete, "/groups/10/users/8.js", nil); res.StatusCode != 204 || count() != 1 {
		t.Errorf("remove_user js without confirmation: %d count=%d", res.StatusCode, count())
	}
	if res, _ := send(t, c, ts.URL, http.MethodDelete, "/groups/10/users/8", url.Values{"confirm": {"Yes"}});res.StatusCode != 302 || count() != 0 {
		t.Errorf("remove_user: %d count=%d", res.StatusCode, count())
	}
}

// API は確認なしで外せる（DELETE /groups/:id/users.json）。
func TestGroupsRemoveUsersAPI(t *testing.T) {
	ts, d := newFixtureServer(t)
	mastersExec(t, d, `INSERT INTO group_users (group_id, user_id) VALUES (10, 2)`)
	res, body := apiRequest(t, "DELETE", ts.URL+"/groups/10/users.json", "application/json", `{"user_ids":[2,8]}`)
	if res.StatusCode != 204 || uQueryInt(t, d, `SELECT COUNT(*) FROM group_users WHERE group_id = 10`) != 0 {
		t.Errorf("api remove_users: %d %s", res.StatusCode, body)
	}
	if res, _ := apiRequest(t, "DELETE", ts.URL+"/groups/10/users.json", "application/json", `{"user_ids":[2]}`); res.StatusCode != 404 {
		t.Errorf("api remove non member: %d", res.StatusCode)
	}
}

// context_menus/users_controller_test.rb
func TestUsersContextMenuGroups(t *testing.T) {
	ts, d := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")
	menu := func(q string) *html.Node {
		t.Helper()
		res, body := get(t, c, ts.URL+"/users/context_menu?"+q)
		if res.StatusCode != 200 {
			t.Fatalf("%s: %d", q, res.StatusCode)
		}
		doc, err := html.Parse(strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return doc
	}
	folderLinks := func(doc *html.Node, title string) []string {
		var out []string
		for _, f := range xmlSelect(t, doc, "li.folder") {
			if nodeText(xmlSelect(t, f, "a.submenu")[0]) != title {
				continue
			}
			for _, a := range xmlSelect(t, f, "a:not(.submenu)") {
				out = append(out, nodeText(a))
			}
		}
		return out
	}
	// test_users_context_menu: ユーザー 8 はグループ 10・11 に所属
	doc := menu("ids[]=8")
	if got := strings.Join(folderLinks(doc, "Add to group"), ","); got != "A Team,B Team" {
		t.Errorf("add to group: %q", got)
	}
	if got := strings.Join(folderLinks(doc, "Remove from group"), ","); got != "A Team,B Team" {
		t.Errorf("remove from group: %q", got)
	}
	assertXMLCount(t, doc, `a[data-method=post][href="/groups/10/users?user_ids%5B%5D=8"]`, 1)
	assertXMLCount(t, doc, `a[data-method=delete][href="/groups/10/users?user_ids%5B%5D=8"]`, 1)
	// params[:id] も使える
	doc = menu("id=8")
	assertXMLCount(t, doc, `a[data-method=delete][href="/groups/10/users?user_ids%5B%5D=8"]`, 1)
	// back_url はリンクに引き継ぐ
	doc = menu("ids[]=8&back_url=/users")
	assertXMLCount(t, doc, `a[data-method=post][href="/groups/11/users?back_url=%2Fusers&user_ids%5B%5D=8"]`, 1)

	// test_users_context_menu_bulk_with_different_groups: ユーザー 2 をグループ 11 へ
	mastersExec(t, d, `INSERT INTO group_users (group_id, user_id) VALUES (11, 2)`)
	doc = menu("ids[]=2&ids[]=3")
	if got := strings.Join(folderLinks(doc, "Remove from group"), ","); got != "B Team" {
		t.Errorf("bulk remove from group: %q", got)
	}
	// どのグループにも所属していなければ「グループから削除」は出ない
	doc = menu("ids[]=3")
	if got := folderLinks(doc, "Remove from group"); len(got) != 0 {
		t.Errorf("user 3 remove from group: %v", got)
	}
}
