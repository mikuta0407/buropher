// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package query

// test/unit/query_test.rb (1114 行目 test_filter_any_searchable_with_my_projects 〜
// 2246 行目 test_default_sort) の移植。
//
// 移植方針:
//   - find_issues_with_query (可視性を見ずに statement だけで絞る) は findIssueIDs、
//     query.issues / issue_ids は queryIDs / Issues を使う。
//   - User.current が未設定 (setup で nil) のテストは匿名ユーザ (id 0) で評価する。
//   - モデル経由のデータ作成 (Issue.generate!, CustomField.create!, Journal.create! 等) は SQL で行う。
//
// 意図的な差異 (アサーションを buropher の表現に合わせたもの):
//   - test_grouped_with_valid_column: group_by_statement は Redmine では関連名 'status'
//     (Rails が外部キーに読み替える) だが、buropher は SQL 式 'issues.status_id' を持つ。
//   - test_sortable_columns_should_sort_*_according_to_user_format_setting: 旧 users.lastname は
//     グループ名を含むため buropher では "(users.lastname || users.name)" で表す。

import (
	"context"
	"regexp"
	"slices"
	"strconv"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/testfixtures"
)

// ---------------------------------------------------------------- 補助 (接尾辞 B)

// issueQueryB は User.current = userID のチケットクエリで、filters = {} にしたもの。
func issueQueryB(tdb *testDB, userID, projectID int64) *Query {
	q := tdb.newQuery(userID, KindIssue, projectID)
	q.Filters = NewFilters()
	return q
}

func insertB(tdb *testDB, sql string, args ...any) int64 {
	tdb.t.Helper()
	id, err := tdb.d.InsertReturningID(context.Background(), sql, args...)
	if err != nil {
		tdb.t.Fatalf("%s: %v", sql, err)
	}
	return id
}

type cfOptsB struct {
	kind, name, format       string
	forAll, filter, multiple bool
	trackers, projects       []int64
}

// createCFB はカスタムフィールドを作成する (CustomField.create! / generate!)。
func createCFB(tdb *testDB, o cfOptsB) int64 {
	if o.format == "" {
		o.format = "string"
	}
	if o.name == "" {
		o.name = "Custom field B" + strconv.Itoa(int(testfixturesSeqB()))
	}
	id := insertB(tdb, `INSERT INTO custom_fields (owner_kind, name, field_format, is_for_all, is_filter, multiple, position)
VALUES (?, ?, ?, ?, ?, ?, 1)`, o.kind, o.name, o.format, o.forAll, o.filter, o.multiple)
	for _, t := range o.trackers {
		tdb.exec(`INSERT INTO custom_fields_trackers (custom_field_id, tracker_id) VALUES (?, ?)`, id, t)
	}
	for _, p := range o.projects {
		tdb.exec(`INSERT INTO custom_fields_projects (custom_field_id, project_id) VALUES (?, ?)`, id, p)
	}
	return id
}

var seqB int64

func testfixturesSeqB() int64 { seqB++; return seqB }

func setCVB(tdb *testDB, kind string, customizedID, cfID int64, value string) {
	var v any
	if value != "" {
		v = value
	}
	tdb.exec(`INSERT INTO custom_values (customized_kind, customized_id, custom_field_id, value) VALUES (?, ?, ?, ?)`, kind, customizedID, cfID, v)
}

func journalB(tdb *testDB, userID, issueID int64, notes string, private bool) {
	insertB(tdb, `INSERT INTO issue_journals (issue_id, user_id, notes, private_notes, created_at) VALUES (?, ?, ?, ?, ?)`,
		issueID, userID, notes, private, db.NewTime(frozenNow))
}

func watchB(tdb *testDB, issueID, principalID int64) {
	tdb.exec(`INSERT INTO watchers (watchable_kind, watchable_id, principal_id) VALUES ('issue', ?, ?)`, issueID, principalID)
}

func relationB(tdb *testDB, from, to int64, typ string) {
	// IssueRelation#handle_issue_order: 逆向きの種別は向きを入れ替えて保存する
	if rt := relationType(typ); rt != nil && rt.Reverse != "" {
		from, to, typ = to, from, rt.Reverse
	}
	tdb.exec(`INSERT INTO issue_relations (issue_from_id, issue_to_id, relation_type) VALUES (?, ?, ?)`, from, to, typ)
}

func genIssueB(tdb *testDB, a testfixtures.IssueAttrs) int64 {
	return testfixtures.GenerateIssue(tdb.t, tdb.d, a)
}

// withDescendantsB は Issue.generate_with_descendants!: parent, child1, child2, child11 を返す。
func withDescendantsB(tdb *testDB) (parent, child1, child2, child11 int64) {
	parent = genIssueB(tdb, testfixtures.IssueAttrs{})
	child1 = genIssueB(tdb, testfixtures.IssueAttrs{Subject: "Child1", ParentID: parent})
	child2 = genIssueB(tdb, testfixtures.IssueAttrs{Subject: "Child2", ParentID: parent})
	child11 = genIssueB(tdb, testfixtures.IssueAttrs{Subject: "Child11", ParentID: child1})
	return
}

// envForUserB は任意の User オブジェクト (admin フラグを書き換えたもの等) で Env を作る。
func envForUserB(tdb *testDB, u *domain.User) *Env {
	tdb.t.Helper()
	e := tdb.env(1)
	e2, err := NewEnv(context.Background(), tdb.d, u, tdb.st)
	if err != nil {
		tdb.t.Fatal(err)
	}
	e2.Now, e2.ServerLocation, e2.ProjectNestedSet = e.Now, e.ServerLocation, e.ProjectNestedSet
	return e2
}

func filterValueIncludesB(t *testing.T, q *Query, field, value string) bool {
	t.Helper()
	af, err := q.AvailableFilters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	d := af.Get(field)
	if d == nil {
		t.Fatalf("filter %s not available", field)
	}
	vals, err := d.LoadValues(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return slices.ContainsFunc(vals, func(o Option) bool { return o.Value == value })
}

func hasAvailableFilterB(t *testing.T, q *Query, field string) bool {
	t.Helper()
	af, err := q.AvailableFilters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return af.Has(field)
}

func allIssueIDsB(tdb *testDB) []int64 { return tdb.ints(`SELECT id FROM issues ORDER BY id`) }

func minusB(a, b []int64) []int64 {
	return slices.DeleteFunc(slices.Clone(a), func(x int64) bool { return slices.Contains(b, x) })
}

func columnNamesB(cols []*Column) []string {
	out := []string{}
	for _, c := range cols {
		out = append(out, c.Name)
	}
	return out
}

// ---------------------------------------------------------------- any_searchable

func TestQueryFilterAnySearchableWithMyProjects(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := issueQueryB(tdb, 3, 0) // dlopper (ecookbook のみ)
		mustFilter(t, q, "any_searchable", "~", "issue")
		mustFilter(t, q, "project_id", "=", "mine")
		assertIDs(t, findIssueIDs(t, q), []int64{7, 8, 11, 12})
	})
}

func TestQueryFilterAnySearchableWithMyBookmarks(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := issueQueryB(tdb, 1, 0)
		mustFilter(t, q, "any_searchable", "~", "issue")
		mustFilter(t, q, "project_id", "=", "bookmarks")
		assertIDs(t, findIssueIDs(t, q), []int64{6, 7, 8, 9, 10, 11, 12})
	})
}

func TestQueryFilterAnySearchableWithOpenIssuesShouldSearchOnlyOpenIssues(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := issueQueryB(tdb, 1, 0)
		mustFilter(t, q, "status_id", "o")
		f, err := q.sqlForAnySearchable(context.Background(), "~", []string{"issue"})
		if err != nil {
			t.Fatal(err)
		}
		if !regexp.MustCompile(`issues.id  IN \([\d,]+\)`).MatchString(f.SQL) {
			t.Errorf("sql = %q", f.SQL)
		}
		var ids []int64
		for _, m := range regexp.MustCompile(`\d+`).FindAllString(f.SQL, -1) {
			n, _ := strconv.ParseInt(m, 10, 64)
			ids = append(ids, n)
		}
		assertIDs(t, ids, []int64{4, 5, 6, 7, 9, 10, 13, 14})
	})
}

// ---------------------------------------------------------------- updated_by / last_updated_by

func TestQueryFilterUpdatedBy(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		user := testfixtures.GenerateUser(t, tdb.d)
		journalB(tdb, user, 2, "Notes", false)
		journalB(tdb, user, 3, "Notes", false)
		journalB(tdb, 2, 3, "Notes", false)
		q := issueQueryB(tdb, 0, 0)
		if !hasAvailableFilterB(t, q, "updated_by") {
			t.Fatal("updated_by not available")
		}
		mustFilter(t, q, "updated_by", "=", itoa(user))
		assertIDs(t, findIssueIDs(t, q), []int64{2, 3})
		q.Filters = NewFilters()
		mustFilter(t, q, "updated_by", "!", itoa(user))
		assertIDs(t, findIssueIDs(t, q), minusB(allIssueIDsB(tdb), []int64{2, 3}))
	})
}

func TestQueryFilterUpdatedByShouldIgnorePrivateNotesThatAreNotVisible(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		user := testfixtures.GenerateUser(t, tdb.d)
		journalB(tdb, user, 2, "Notes", true)
		journalB(tdb, user, 3, "Notes", false)
		q := issueQueryB(tdb, 0, 0)
		mustFilter(t, q, "updated_by", "=", itoa(user))
		assertIDs(t, findIssueIDs(t, q), []int64{3})
	})
}

func TestQueryFilterUpdatedByMe(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		user := testfixtures.GenerateUser(t, tdb.d)
		journalB(tdb, user, 2, "Notes", false)
		q := issueQueryB(tdb, user, 0)
		mustFilter(t, q, "updated_by", "=", "me")
		assertIDs(t, findIssueIDs(t, q), []int64{2})
	})
}

func TestQueryFilterLastUpdatedBy(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		user := testfixtures.GenerateUser(t, tdb.d)
		journalB(tdb, user, 2, "Notes", false)
		journalB(tdb, user, 3, "Notes", false)
		journalB(tdb, 2, 3, "Notes", false)
		q := issueQueryB(tdb, 0, 0)
		mustFilter(t, q, "last_updated_by", "=", itoa(user))
		assertIDs(t, findIssueIDs(t, q), []int64{2})
	})
}

func TestQueryFilterLastUpdatedByShouldIgnorePrivateNotesThatAreNotVisible(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		user1 := testfixtures.GenerateUser(t, tdb.d)
		user2 := testfixtures.GenerateUser(t, tdb.d)
		journalB(tdb, user1, 2, "Notes", false)
		journalB(tdb, user2, 2, "Notes", true)
		for _, c := range []struct {
			current, filter int64
			want            []int64
		}{
			{0, user1, []int64{2}}, {0, user2, nil},
			{2, user1, nil}, {2, user2, []int64{2}},
		} {
			q := issueQueryB(tdb, c.current, 0)
			mustFilter(t, q, "last_updated_by", "=", itoa(c.filter))
			assertIDs(t, findIssueIDs(t, q), c.want)
		}
	})
}

// ---------------------------------------------------------------- ユーザ CF・me・自分のプロジェクト

func TestQueryUserCustomFieldFilteredOnMe(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		cf := createCFB(tdb, cfOptsB{kind: "issue", name: "User custom field", format: "user", forAll: true, filter: true, trackers: []int64{1}})
		issue1 := genIssueB(tdb, testfixtures.IssueAttrs{ProjectID: 1, TrackerID: 1, AuthorID: 1, Subject: "Test"})
		setCVB(tdb, "issue", issue1, cf, "2")
		issue2 := genIssueB(tdb, testfixtures.IssueAttrs{ProjectID: 1, TrackerID: 1})
		setCVB(tdb, "issue", issue2, cf, "3")
		q := tdb.newQuery(2, KindIssue, 1)
		field := "cf_" + itoa(cf)
		if !filterValueIncludesB(t, q, field, "me") {
			t.Error("'me' should be in values")
		}
		q.Filters = NewFilters()
		mustFilter(t, q, field, "=", "me")
		ids := queryIDs(t, q)
		if !slices.Equal(ids, []int64{issue1}) {
			t.Errorf("ids = %v, want [%d]", ids, issue1)
		}
	})
}

func TestQueryFilterOnChainedUserCustomField(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`UPDATE custom_fields SET is_filter = ? WHERE id = 4`, true)
		cf := createCFB(tdb, cfOptsB{kind: "issue", name: "User custom field", format: "user", forAll: true, filter: true, trackers: []int64{1}})
		issue1 := genIssueB(tdb, testfixtures.IssueAttrs{ProjectID: 1, TrackerID: 1, AuthorID: 1, Subject: "Test"})
		setCVB(tdb, "issue", issue1, cf, "2")
		q := issueQueryB(tdb, 2, 1)
		mustFilter(t, q, "cf_"+itoa(cf)+".cf_4", "~", "01 42")
		ids := queryIDs(t, q)
		if !slices.Equal(ids, []int64{issue1}) {
			t.Errorf("ids = %v, want [%d]", ids, issue1)
		}
	})
}

func TestQueryFilterOnChainedUserCustomFieldOfTypeFloat(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`UPDATE custom_fields SET is_filter = ? WHERE id = 5`, true)
		cf := createCFB(tdb, cfOptsB{kind: "issue", name: "User custom field", format: "user", forAll: true, filter: true, trackers: []int64{1}})
		issue1 := genIssueB(tdb, testfixtures.IssueAttrs{ProjectID: 1, TrackerID: 1, AuthorID: 1, Subject: "Test"})
		setCVB(tdb, "issue", issue1, cf, "2")
		q := issueQueryB(tdb, 0, 1)
		mustFilter(t, q, "cf_"+itoa(cf)+".cf_5", "=", "30.1")
		if _, err := q.IDs(context.Background(), ListOptions{}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestQueryFilterOnMeByAnonymousUser(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := issueQueryB(tdb, 0, 0)
		mustFilter(t, q, "assigned_to_id", "=", "me")
		if ids := queryIDs(t, q); len(ids) != 0 {
			t.Errorf("ids = %v", ids)
		}
	})
}

func TestQueryFilterMyProjects(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(2, KindIssue, 0)
		if !filterValueIncludesB(t, q, "project_id", "mine") {
			t.Error("'mine' should be in values")
		}
		q.Filters = NewFilters()
		mustFilter(t, q, "project_id", "=", "mine")
		rows, err := q.Issues(context.Background(), ListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		mine, _ := q.env.Auth.ProjectIDs(context.Background())
		if len(rows) == 0 {
			t.Error("no issues")
		}
		for _, r := range rows {
			if !slices.Contains(mine, r.ProjectID) {
				t.Errorf("issue %d in project %d is not mine", r.ID, r.ProjectID)
			}
		}
	})
}

func TestQueryFilterMyBookmarks(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(1, KindProject, 0)
		if !filterValueIncludesB(t, q, "id", "bookmarks") {
			t.Error("'bookmarks' should be in values")
		}
		q.Filters = NewFilters()
		mustFilter(t, q, "id", "=", "bookmarks")
		assertIDs(t, queryIDs(t, q), []int64{1, 5})
	})
}

func TestQueryFilterMyBookmarksForUserWithoutBookmarkedProjects(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(2, KindProject, 0)
		if filterValueIncludesB(t, q, "id", "bookmarks") {
			t.Error("'bookmarks' should not be in values")
		}
	})
}

func TestQueryFilterProjectParentIdWithMyProjects(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(1, KindProject, 0)
		if !filterValueIncludesB(t, q, "parent_id", "mine") {
			t.Error("'mine' should be in values")
		}
		q.Filters = NewFilters()
		mustFilter(t, q, "parent_id", "=", "mine")
		want := tdb.ints(`SELECT id FROM projects WHERE parent_id IN (SELECT m.project_id FROM members m JOIN projects p ON p.id = m.project_id
WHERE m.principal_id = 1 AND p.status <> 9) ORDER BY id`)
		assertIDs(t, queryIDs(t, q), want)
	})
}

func TestQueryFilterProjectParentIdWithMyBookmarks(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(1, KindProject, 0)
		if !filterValueIncludesB(t, q, "parent_id", "bookmarks") {
			t.Error("'bookmarks' should be in values")
		}
		q.Filters = NewFilters()
		mustFilter(t, q, "parent_id", "=", "bookmarks")
		want := tdb.ints(`SELECT id FROM projects WHERE parent_id IN (SELECT project_id FROM user_project_bookmarks WHERE user_id = 1) ORDER BY id`)
		assertIDs(t, queryIDs(t, q), want)
	})
}

// ---------------------------------------------------------------- ウォッチャー

func TestQueryFilterWatchedIssuesByUser(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := issueQueryB(tdb, 1, 0)
		mustFilter(t, q, "watcher_id", "=", "1")
		got := findIssueIDs(t, q)
		if len(got) == 0 {
			t.Fatal("empty")
		}
		// Issue.visible.watched_by(User.current)
		v := issueQueryB(tdb, 1, 0)
		visible := queryIDs(t, v)
		watched := tdb.ints(`SELECT watchable_id FROM watchers WHERE watchable_kind = 'issue' AND principal_id = 1`)
		var want []int64
		for _, id := range visible {
			if slices.Contains(watched, id) {
				want = append(want, id)
			}
		}
		assertIDs(t, got, want)
	})
}

func TestQueryFilterWatchedIssuesByMeShouldIncludeUserGroups(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		if err := repository.AddUserToGroup(context.Background(), tdb.d, 10, 2); err != nil {
			t.Fatal(err)
		}
		watchB(tdb, 3, 2)
		watchB(tdb, 7, 10)
		tdb.exec(`DELETE FROM role_permissions WHERE role_id = 1 AND permission = 'view_issue_watchers'`)
		q := issueQueryB(tdb, 2, 0)
		mustFilter(t, q, "watcher_id", "=", "me")
		ids := findIssueIDs(t, q)
		if !slices.Equal(ids, []int64{3, 7}) {
			t.Errorf("ids = %v, want [3 7]", ids)
		}
	})
}

func TestQueryFilterWatchedIssuesByGroupShouldIncludeOnlyProjectsWithPermission(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		watchB(tdb, 4, 10)
		watchB(tdb, 2, 10)
		tdb.exec(`DELETE FROM role_permissions WHERE role_id = 2 AND permission = 'view_issue_watchers'`)
		q := issueQueryB(tdb, 2, 0)
		mustFilter(t, q, "watcher_id", "=", "10")
		ids := findIssueIDs(t, q)
		if !slices.Equal(ids, []int64{2}) {
			t.Errorf("ids = %v, want [2]", ids)
		}
	})
}

func TestQueryFilterUnwatchedIssues(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := issueQueryB(tdb, 1, 0)
		mustFilter(t, q, "watcher_id", "!", "me")
		got := findIssueIDs(t, q)
		if len(got) == 0 {
			t.Fatal("empty")
		}
		v := issueQueryB(tdb, 1, 0)
		visible := queryIDs(t, v)
		watched := tdb.ints(`SELECT watchable_id FROM watchers WHERE watchable_kind = 'issue' AND principal_id = 1`)
		if want := len(minusB(visible, watched)); len(got) != want {
			t.Errorf("size = %d, want %d", len(got), want)
		}
	})
}

func TestQueryFilterOnWatchedIssuesWithViewIssueWatchersPermission(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		watchB(tdb, 1, 1)
		watchB(tdb, 3, 3)
		q := issueQueryB(tdb, 1, 0)
		ok, err := q.env.Auth.AllowedTo(context.Background(), domain.Perm("view_issue_watchers"), tdb.project(1))
		if err != nil || !ok {
			t.Fatal("admin should be allowed to view_issue_watchers")
		}
		mustFilter(t, q, "watcher_id", "=", "me", "3")
		ids := findIssueIDs(t, q)
		if !slices.Contains(ids, 1) || !slices.Contains(ids, 3) {
			t.Errorf("ids = %v should include 1 and 3", ids)
		}
	})
}

func TestQueryFilterOnWatchedIssuesWithoutViewIssueWatchersPermission(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		watchB(tdb, 1, 1)
		watchB(tdb, 3, 3)
		u := tdb.user(1)
		u.AdminFlag = false
		env := envForUserB(tdb, u)
		ok, err := env.Auth.AllowedTo(context.Background(), domain.Perm("view_issue_watchers"), tdb.project(1))
		if err != nil || ok {
			t.Fatal("non-admin user 1 should not be allowed to view_issue_watchers")
		}
		q, err := New(context.Background(), env, KindIssue, nil)
		if err != nil {
			t.Fatal(err)
		}
		q.Filters = NewFilters()
		mustFilter(t, q, "watcher_id", "=", "me", "3")
		ids := findIssueIDs(t, q)
		if !slices.Contains(ids, 1) || slices.Contains(ids, 3) {
			t.Errorf("ids = %v should include 1 and not 3", ids)
		}
	})
}

// ---------------------------------------------------------------- カスタムフィールド

func TestQueryFilterOnCustomFieldShouldIgnoreProjectsWithFieldDisabled(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		cf := createCFB(tdb, cfOptsB{kind: "issue", filter: true, trackers: []int64{1, 2, 3}, projects: []int64{1, 3, 5}})
		i1 := genIssueB(tdb, testfixtures.IssueAttrs{ProjectID: 3, TrackerID: 2})
		setCVB(tdb, "issue", i1, cf, "Foo")
		i2 := genIssueB(tdb, testfixtures.IssueAttrs{ProjectID: 5, TrackerID: 2})
		setCVB(tdb, "issue", i2, cf, "Foo")
		q := issueQueryB(tdb, 1, 1)
		mustFilter(t, q, "cf_"+itoa(cf), "=", "Foo")
		if n := len(findIssueIDs(t, q)); n != 2 {
			t.Errorf("size = %d, want 2", n)
		}
		tdb.exec(`DELETE FROM custom_fields_projects WHERE custom_field_id = ? AND project_id = 5`, cf)
		q2 := issueQueryB(tdb, 1, 1)
		mustFilter(t, q2, "cf_"+itoa(cf), "=", "Foo")
		if n := len(findIssueIDs(t, q2)); n != 1 {
			t.Errorf("size = %d, want 1", n)
		}
	})
}

func TestQueryFilterOnCustomFieldShouldIgnoreTrackersWithFieldDisabled(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		cf := createCFB(tdb, cfOptsB{kind: "issue", forAll: true, filter: true, trackers: []int64{1, 2}})
		i1 := genIssueB(tdb, testfixtures.IssueAttrs{ProjectID: 1, TrackerID: 1})
		setCVB(tdb, "issue", i1, cf, "Foo")
		i2 := genIssueB(tdb, testfixtures.IssueAttrs{ProjectID: 1, TrackerID: 2})
		setCVB(tdb, "issue", i2, cf, "Foo")
		q := issueQueryB(tdb, 0, 1)
		mustFilter(t, q, "cf_"+itoa(cf), "=", "Foo")
		if n := len(findIssueIDs(t, q)); n != 2 {
			t.Errorf("size = %d, want 2", n)
		}
		tdb.exec(`DELETE FROM custom_fields_trackers WHERE custom_field_id = ? AND tracker_id = 2`, cf)
		q2 := issueQueryB(tdb, 0, 1)
		mustFilter(t, q2, "cf_"+itoa(cf), "=", "Foo")
		if n := len(findIssueIDs(t, q2)); n != 1 {
			t.Errorf("size = %d, want 1", n)
		}
	})
}

// issueColumnB は statement に一致するチケットの列値 (重複除去・昇順)。
func issueColumnB(t *testing.T, tdb *testDB, q *Query, col string) []int64 {
	ids := findIssueIDs(t, q)
	if len(ids) == 0 {
		return nil
	}
	return tdb.ints(`SELECT DISTINCT ` + col + ` FROM issues WHERE id IN (` + idList(ids) + `) ORDER BY 1`)
}

func TestQueryFilterOnProjectCustomField(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		cf := createCFB(tdb, cfOptsB{kind: "project", name: "Client", filter: true})
		setCVB(tdb, "project", 3, cf, "Foo")
		setCVB(tdb, "project", 5, cf, "Foo")
		q := issueQueryB(tdb, 0, 0)
		mustFilter(t, q, "project.cf_"+itoa(cf), "=", "Foo")
		if got := issueColumnB(t, tdb, q, "project_id"); !slices.Equal(got, []int64{3, 5}) {
			t.Errorf("project ids = %v", got)
		}
	})
}

func TestQueryFilterOnAuthorCustomField(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		cf := createCFB(tdb, cfOptsB{kind: "user", name: "Client", filter: true})
		setCVB(tdb, "principal", 3, cf, "Foo")
		q := issueQueryB(tdb, 0, 0)
		mustFilter(t, q, "author.cf_"+itoa(cf), "=", "Foo")
		if got := issueColumnB(t, tdb, q, "author_id"); !slices.Equal(got, []int64{3}) {
			t.Errorf("author ids = %v", got)
		}
	})
}

func TestQueryFilterOnAssignedToCustomField(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		cf := createCFB(tdb, cfOptsB{kind: "user", name: "Client", filter: true})
		setCVB(tdb, "principal", 3, cf, "Foo")
		q := issueQueryB(tdb, 0, 0)
		mustFilter(t, q, "assigned_to.cf_"+itoa(cf), "=", "Foo")
		if got := issueColumnB(t, tdb, q, "assigned_to_id"); !slices.Equal(got, []int64{3}) {
			t.Errorf("assigned_to ids = %v", got)
		}
	})
}

func TestQueryFilterOnFixedVersionCustomField(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		cf := createCFB(tdb, cfOptsB{kind: "version", name: "Client", filter: true})
		setCVB(tdb, "version", 2, cf, "Foo")
		q := issueQueryB(tdb, 0, 0)
		mustFilter(t, q, "fixed_version.cf_"+itoa(cf), "=", "Foo")
		if got := issueColumnB(t, tdb, q, "fixed_version_id"); !slices.Equal(got, []int64{2}) {
			t.Errorf("fixed_version ids = %v", got)
		}
	})
}

func TestQueryFilterOnFixedVersionDueDate(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := issueQueryB(tdb, 0, 0)
		mustFilter(t, q, "fixed_version.due_date", "=", frozenNow.AddDate(0, 0, 20).Format("2006-01-02"))
		if got := issueColumnB(t, tdb, q, "fixed_version_id"); !slices.Equal(got, []int64{2}) {
			t.Errorf("fixed_version ids = %v", got)
		}
		assertIDs(t, findIssueIDs(t, q), []int64{2, 12})
		q2 := issueQueryB(tdb, 0, 0)
		mustFilter(t, q2, "fixed_version.due_date", ">=", frozenNow.AddDate(0, 0, 21).Format("2006-01-02"))
		if ids := findIssueIDs(t, q2); len(ids) != 0 {
			t.Errorf("ids = %v", ids)
		}
	})
}

func TestQueryFilterOnFixedVersionStatus(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := issueQueryB(tdb, 0, 0)
		mustFilter(t, q, "fixed_version.status", "=", "closed")
		if got := issueColumnB(t, tdb, q, "fixed_version_id"); !slices.Equal(got, []int64{1}) {
			t.Errorf("fixed_version ids = %v", got)
		}
		assertIDs(t, findIssueIDs(t, q), []int64{11})
		// "is not" は対象バージョンの無いチケットも含む
		q2 := issueQueryB(tdb, 0, 0)
		mustFilter(t, q2, "fixed_version.status", "!", "open", "closed", "locked")
		mustFilter(t, q2, "project_id", "=", "1")
		assertIDs(t, findIssueIDs(t, q2), []int64{1, 3, 7, 8})
	})
}

func TestQueryFilterOnFixedVersionStatusRespectsSharing(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		issue := genIssueB(tdb, testfixtures.IssueAttrs{ProjectID: 1})
		tdb.exec(`UPDATE issues SET fixed_version_id = 7 WHERE id = ?`, issue)
		q := issueQueryB(tdb, 0, 1)
		mustFilter(t, q, "fixed_version.status", "=", "open")
		if !slices.Contains(findIssueIDs(t, q), issue) {
			t.Error("open: issue should be included")
		}
		q2 := issueQueryB(tdb, 0, 1)
		mustFilter(t, q2, "fixed_version.status", "=", "closed")
		if slices.Contains(findIssueIDs(t, q2), issue) {
			t.Error("closed: issue should not be included")
		}
	})
}

func TestQueryFilterOnVersionCustomField(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		cf := createCFB(tdb, cfOptsB{kind: "issue", format: "version", forAll: true, filter: true, trackers: []int64{1, 2, 3}})
		issue := genIssueB(tdb, testfixtures.IssueAttrs{ProjectID: 1, TrackerID: 1})
		setCVB(tdb, "issue", issue, cf, "2")
		q := issueQueryB(tdb, 0, 0)
		mustFilter(t, q, "cf_"+itoa(cf), "=", "2")
		assertIDs(t, findIssueIDs(t, q), []int64{issue})
	})
}

func TestQueryFilterOnAttributeOfVersionCustomField(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		cf := createCFB(tdb, cfOptsB{kind: "issue", format: "version", forAll: true, filter: true, trackers: []int64{1, 2, 3}})
		ver := insertB(tdb, `INSERT INTO versions (project_id, name, effective_date, created_at, updated_at) VALUES (1, 'Version B', '2017-01-14', ?, ?)`,
			db.NewTime(frozenNow), db.NewTime(frozenNow))
		issue := genIssueB(tdb, testfixtures.IssueAttrs{ProjectID: 1, TrackerID: 1})
		setCVB(tdb, "issue", issue, cf, itoa(ver))
		q := issueQueryB(tdb, 0, 0)
		mustFilter(t, q, "cf_"+itoa(cf)+".due_date", "=", "2017-01-14")
		assertIDs(t, findIssueIDs(t, q), []int64{issue})
	})
}

func TestQueryFilterOnCustomFieldOfVersionCustomField(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		cf := createCFB(tdb, cfOptsB{kind: "issue", format: "version", forAll: true, filter: true, trackers: []int64{1, 2, 3}})
		attr := createCFB(tdb, cfOptsB{kind: "version", filter: true})
		ver := insertB(tdb, `INSERT INTO versions (project_id, name, created_at, updated_at) VALUES (1, 'Version B', ?, ?)`,
			db.NewTime(frozenNow), db.NewTime(frozenNow))
		setCVB(tdb, "version", ver, attr, "ABC")
		issue := genIssueB(tdb, testfixtures.IssueAttrs{ProjectID: 1, TrackerID: 1})
		setCVB(tdb, "issue", issue, cf, itoa(ver))
		q := issueQueryB(tdb, 0, 0)
		mustFilter(t, q, "cf_"+itoa(cf)+".cf_"+itoa(attr), "=", "ABC")
		assertIDs(t, findIssueIDs(t, q), []int64{issue})
	})
}

// ---------------------------------------------------------------- 関連

func relatesB(t *testing.T, tdb *testDB, field, op string, values ...string) []int64 {
	q := issueQueryB(tdb, 0, 0)
	mustFilter(t, q, field, op, values...)
	return findIssueIDs(t, q)
}

func TestQueryFilterOnRelationsWithASpecificIssue(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`DELETE FROM issue_relations`)
		relationB(tdb, 1, 2, "relates")
		relationB(tdb, 3, 1, "relates")
		assertIDs(t, relatesB(t, tdb, "relates", "=", "1"), []int64{2, 3})
		assertIDs(t, relatesB(t, tdb, "relates", "=", "2"), []int64{1})
		assertIDs(t, relatesB(t, tdb, "relates", "=", "1,2"), []int64{1, 2, 3})
		assertIDs(t, relatesB(t, tdb, "relates", "=", "invalid"), nil)
		all := allIssueIDsB(tdb)
		assertIDs(t, relatesB(t, tdb, "relates", "!", "1"), minusB(all, []int64{2, 3}))
		assertIDs(t, relatesB(t, tdb, "relates", "!", "1,2"), minusB(all, []int64{1, 2, 3}))
	})
}

func TestQueryFilterOnRelationsWithAnyIssuesInAProject(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`DELETE FROM issue_relations`)
		relationB(tdb, 1, 4, "relates") // Project.find(2).issues.first
		relationB(tdb, 2, 4, "relates")
		relationB(tdb, 1, 5, "relates") // Project.find(3).issues.first
		assertIDs(t, relatesB(t, tdb, "relates", "=p", "2"), []int64{1, 2})
		assertIDs(t, relatesB(t, tdb, "relates", "=p", "3"), []int64{1})
		assertIDs(t, relatesB(t, tdb, "relates", "=p", "4"), nil)
	})
}

func TestQueryFilterOnRelationsWithAnyIssuesNotInAProject(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`DELETE FROM issue_relations`)
		relationB(tdb, 1, 4, "relates")
		relationB(tdb, 1, 5, "relates")
		assertIDs(t, relatesB(t, tdb, "relates", "=!p", "1"), []int64{1})
	})
}

func TestQueryFilterOnRelationsWithNoIssuesInAProject(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`DELETE FROM issue_relations`)
		relationB(tdb, 1, 4, "relates")
		relationB(tdb, 2, 5, "relates")
		relationB(tdb, 3, 4, "relates")
		ids := relatesB(t, tdb, "relates", "!p", "2")
		if !slices.Contains(ids, 2) || slices.Contains(ids, 1) || slices.Contains(ids, 3) {
			t.Errorf("ids = %v", ids)
		}
	})
}

func TestQueryFilterOnRelationsWithAnyOpenIssues(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`DELETE FROM issue_relations`)
		relationB(tdb, 1, 8, "blocked") // 8 は終了
		relationB(tdb, 2, 3, "blocked") // 3 は未完了
		ids := relatesB(t, tdb, "blocked", "*o")
		if slices.Contains(ids, 1) || !slices.Contains(ids, 2) {
			t.Errorf("ids = %v", ids)
		}
	})
}

func TestQueryFilterOnBlockedByNoOpenIssues(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`DELETE FROM issue_relations`)
		relationB(tdb, 1, 8, "blocked")
		relationB(tdb, 2, 3, "blocked")
		ids := relatesB(t, tdb, "blocked", "!o")
		if slices.Contains(ids, 2) || !slices.Contains(ids, 1) {
			t.Errorf("ids = %v", ids)
		}
	})
}

func TestQueryFilterOnRelatedWithNoOpenIssues(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`DELETE FROM issue_relations`)
		relationB(tdb, 1, 8, "relates")
		relationB(tdb, 2, 3, "relates")
		ids := relatesB(t, tdb, "relates", "!o")
		if slices.Contains(ids, 2) || !slices.Contains(ids, 1) {
			t.Errorf("ids = %v", ids)
		}
	})
}

func TestQueryFilterOnRelationsWithNoIssues(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`DELETE FROM issue_relations`)
		relationB(tdb, 1, 2, "relates")
		relationB(tdb, 3, 1, "relates")
		ids := relatesB(t, tdb, "relates", "!*")
		if slices.ContainsFunc(ids, func(id int64) bool { return id <= 3 }) || !slices.Contains(ids, 4) {
			t.Errorf("ids = %v", ids)
		}
	})
}

func TestQueryFilterOnRelationsWithAnyIssues(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`DELETE FROM issue_relations`)
		relationB(tdb, 1, 2, "relates")
		relationB(tdb, 3, 1, "relates")
		assertIDs(t, relatesB(t, tdb, "relates", "*"), []int64{1, 2, 3})
	})
}

func TestQueryFilterOnRelationsShouldNotIgnoreOtherFilter(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		issue := genIssueB(tdb, testfixtures.IssueAttrs{})
		issue1 := genIssueB(tdb, testfixtures.IssueAttrs{StatusID: 1})
		issue2 := genIssueB(tdb, testfixtures.IssueAttrs{StatusID: 2})
		relationB(tdb, issue, issue1, "relates")
		relationB(tdb, issue, issue2, "relates")
		q := issueQueryB(tdb, 0, 0)
		mustFilter(t, q, "status_id", "=", "1")
		mustFilter(t, q, "relates", "=", itoa(issue))
		assertIDs(t, findIssueIDs(t, q), []int64{issue1})
	})
}

// ---------------------------------------------------------------- 親子

func TestQueryFilterOnParent(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`DELETE FROM issues`)
		parent, child1, child2, child11 := withDescendantsB(tdb)
		f := func(op string, v ...string) []int64 {
			q := issueQueryB(tdb, 0, 0)
			mustFilter(t, q, "parent_id", op, v...)
			return findIssueIDs(t, q)
		}
		assertIDs(t, f("=", itoa(parent)), []int64{child1, child2})
		assertIDs(t, f("~", itoa(parent)), []int64{child1, child2, child11})
		assertIDs(t, f("*"), []int64{child1, child2, child11})
		assertIDs(t, f("!*"), []int64{parent})
	})
}

func TestQueryFilterOnInvalidParentShouldReturnNoResults(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		for _, op := range []string{"=", "~"} {
			q := issueQueryB(tdb, 0, 0)
			mustFilter(t, q, "parent_id", op, "99999999999")
			if ids := findIssueIDs(t, q); len(ids) != 0 {
				t.Errorf("%s: ids = %v", op, ids)
			}
		}
	})
}

func TestQueryOperatorContainsOnParentIdShouldAcceptCommaSeparatedValues(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		p1 := genIssueB(tdb, testfixtures.IssueAttrs{})
		c1a := genIssueB(tdb, testfixtures.IssueAttrs{ParentID: p1})
		c1b := genIssueB(tdb, testfixtures.IssueAttrs{ParentID: p1})
		p2 := genIssueB(tdb, testfixtures.IssueAttrs{})
		c2a := genIssueB(tdb, testfixtures.IssueAttrs{ParentID: p2})
		c2b := genIssueB(tdb, testfixtures.IssueAttrs{ParentID: p2})
		g := genIssueB(tdb, testfixtures.IssueAttrs{ParentID: c2a})
		q := issueQueryB(tdb, 0, 0)
		mustFilter(t, q, "parent_id", "~", itoa(p1)+","+itoa(p2))
		assertIDs(t, findIssueIDs(t, q), []int64{c1a, c1b, c2a, c2b, g})
	})
}

func TestQueryFilterOnChild(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`DELETE FROM issues`)
		parent, child, leaf, grandchild := withDescendantsB(tdb)
		f := func(op string, v ...string) []int64 {
			q := issueQueryB(tdb, 0, 0)
			mustFilter(t, q, "child_id", op, v...)
			return findIssueIDs(t, q)
		}
		assertIDs(t, f("=", itoa(grandchild)), []int64{child})
		assertIDs(t, f("~", itoa(grandchild)), []int64{parent, child})
		assertIDs(t, f("*"), []int64{parent, child})
		assertIDs(t, f("!*"), []int64{grandchild, leaf})
	})
}

func TestQueryFilterOnInvalidChildShouldReturnNoResults(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		for _, op := range []string{"=", "~"} {
			q := issueQueryB(tdb, 0, 0)
			mustFilter(t, q, "child_id", op, "99999999999")
			if ids := findIssueIDs(t, q); len(ids) != 0 {
				t.Errorf("%s: ids = %v", op, ids)
			}
		}
	})
}

// ---------------------------------------------------------------- 添付・件名

func attachmentB(t *testing.T, tdb *testDB, field, op string, v ...string) []int64 {
	q := issueQueryB(tdb, 0, 0)
	mustFilter(t, q, field, op, v...)
	return findIssueIDs(t, q)
}

func TestQueryFilterOnAttachmentAny(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ids := attachmentB(t, tdb, "attachment", "*")
		with := tdb.ints(`SELECT DISTINCT container_id FROM attachments WHERE container_kind = 'issue' ORDER BY 1`)
		if len(ids) == 0 {
			t.Fatal("empty")
		}
		assertIDs(t, ids, with)
	})
}

func TestQueryFilterOnAttachmentNone(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ids := attachmentB(t, tdb, "attachment", "!*")
		with := tdb.ints(`SELECT DISTINCT container_id FROM attachments WHERE container_kind = 'issue' ORDER BY 1`)
		if len(ids) == 0 {
			t.Fatal("empty")
		}
		assertIDs(t, ids, minusB(allIssueIDsB(tdb), with))
	})
}

func TestQueryFilterOnAttachmentContains(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ids := attachmentB(t, tdb, "attachment", "~", "error281")
		want := tdb.ints(`SELECT DISTINCT container_id FROM attachments WHERE container_kind = 'issue' AND filename LIKE '%error281%' ORDER BY 1`)
		if len(ids) == 0 {
			t.Fatal("empty")
		}
		assertIDs(t, ids, want)
	})
}

func TestQueryFilterOnAttachmentNotContains(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ids := attachmentB(t, tdb, "attachment", "!~", "error281")
		if len(ids) == 0 {
			t.Fatal("empty")
		}
		bad := tdb.ints(`SELECT DISTINCT container_id FROM attachments WHERE container_kind = 'issue' AND filename LIKE '%error281%' ORDER BY 1`)
		for _, id := range ids {
			if slices.Contains(bad, id) {
				t.Errorf("issue %d has an attachment containing error281", id)
			}
		}
	})
}

func TestQueryFilterOnAttachmentWhenStartsWith(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		assertIDs(t, attachmentB(t, tdb, "attachment", "^", "testfile"), []int64{14})
	})
}

func TestQueryFilterOnAttachmentWhenEndsWith(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		assertIDs(t, attachmentB(t, tdb, "attachment", "$", "zip"), []int64{3, 4})
	})
}

func TestQueryFilterOnAttachmentDescriptionWhenAny(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		assertIDs(t, attachmentB(t, tdb, "attachment_description", "*"), []int64{2, 3, 14})
	})
}

func TestQueryFilterOnAttachmentDescriptionWhenNone(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		assertIDs(t, attachmentB(t, tdb, "attachment_description", "!*"), []int64{2, 3, 4, 14})
	})
}

func TestQueryFilterOnAttachmentDescriptionWhenContains(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		assertIDs(t, attachmentB(t, tdb, "attachment_description", "~", "attachment"), []int64{3, 14})
	})
}

func TestQueryFilterOnAttachmentDescriptionWhenDoesNotContain(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		assertIDs(t, attachmentB(t, tdb, "attachment_description", "!~", "attachment"), []int64{2})
	})
}

func TestQueryFilterOnAttachmentDescriptionWhenStartsWith(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		assertIDs(t, attachmentB(t, tdb, "attachment_description", "^", "attachment"), []int64{14})
	})
}

func TestQueryFilterOnAttachmentDescriptionWhenEndsWith(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		assertIDs(t, attachmentB(t, tdb, "attachment_description", "$", "attachment"), []int64{3})
	})
}

func TestQueryFilterOnSubjectWhenStartsWith(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		assertIDs(t, attachmentB(t, tdb, "subject", "^", "issue"), []int64{4, 6, 7, 10})
	})
}

func TestQueryFilterOnSubjectWhenEndsWith(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		assertIDs(t, attachmentB(t, tdb, "subject", "$", "issue"), []int64{5, 8, 9})
	})
}

func TestQueryStatementShouldBeNilWithNoFilters(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := issueQueryB(tdb, 0, 0)
		ctx := context.Background()
		if ok, err := q.Valid(ctx); err != nil || !ok {
			t.Fatalf("valid = %v, %v", ok, err)
		}
		st, _, err := q.Statement(ctx)
		if err != nil || st != "" {
			t.Errorf("statement = %q, %v", st, err)
		}
	})
}

// ---------------------------------------------------------------- available_filters_as_json

func jsonValuesIncludeB(t *testing.T, q *Query, field string, want []string) bool {
	t.Helper()
	_, m, err := q.AvailableFiltersAsJSON(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return slices.ContainsFunc(m[field].Values, func(v []string) bool { return slices.Equal(v, want) })
}

func TestQueryAvailableFiltersAsJsonShouldIncludeMissingAssignedToIdValues(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		user := testfixtures.GenerateUser(t, tdb.d)
		name := tdb.user(user).Firstname + " " + tdb.user(user).Lastname
		q := issueQueryB(tdb, 1, 0)
		q.Filters.Set("assigned_to_id", Filter{Operator: "=", Values: []string{itoa(user)}})
		if !jsonValuesIncludeB(t, q, "assigned_to_id", []string{name, itoa(user)}) {
			t.Error("missing value not included")
		}
	})
}

func TestQueryAvailableFiltersAsJsonShouldNotIncludeDuplicateAssignedToIdValues(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := issueQueryB(tdb, 1, 0)
		q.Filters.Set("assigned_to_id", Filter{Operator: "=", Values: []string{"3"}})
		if jsonValuesIncludeB(t, q, "assigned_to_id", []string{"Dave Lopper", "3"}) {
			t.Error("duplicate value included")
		}
		if !jsonValuesIncludeB(t, q, "assigned_to_id", []string{"Dave Lopper", "3", "active"}) {
			t.Error("value with status not included")
		}
	})
}

func TestQueryAvailableFiltersAsJsonShouldIncludeMissingAuthorIdValues(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		user := testfixtures.GenerateUser(t, tdb.d)
		name := tdb.user(user).Firstname + " " + tdb.user(user).Lastname
		q := issueQueryB(tdb, 1, 0)
		q.Filters.Set("author_id", Filter{Operator: "=", Values: []string{itoa(user)}})
		if !jsonValuesIncludeB(t, q, "author_id", []string{name, itoa(user)}) {
			t.Error("missing value not included")
		}
	})
}

// ---------------------------------------------------------------- 列

func TestQueryDefaultColumns(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ctx := context.Background()
		q := tdb.newQuery(0, KindIssue, 0)
		cols, _ := q.Columns(ctx)
		inline, _ := q.InlineColumns(ctx)
		block, err := q.BlockColumns(ctx)
		if err != nil || len(cols) == 0 || len(inline) == 0 || len(block) != 0 {
			t.Errorf("columns=%d inline=%d block=%d err=%v", len(cols), len(inline), len(block), err)
		}
	})
}

func TestQuerySetColumnNames(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ctx := context.Background()
		q := tdb.newQuery(0, KindIssue, 0)
		if err := q.SetColumnNames(ctx, []string{"tracker", "subject", "", "unknonw_column"}); err != nil {
			t.Fatal(err)
		}
		cols, _ := q.Columns(ctx)
		if got := columnNamesB(cols); !slices.Equal(got, []string{"id", "tracker", "subject"}) {
			t.Errorf("columns = %v", got)
		}
	})
}

func TestQueryHasColumnShouldAcceptAColumnName(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ctx := context.Background()
		q := tdb.newQuery(0, KindIssue, 0)
		_ = q.SetColumnNames(ctx, []string{"tracker", "subject"})
		if ok, _ := q.HasColumn(ctx, "tracker"); !ok {
			t.Error("tracker")
		}
		if ok, _ := q.HasColumn(ctx, "category"); ok {
			t.Error("category")
		}
	})
}

func TestQueryHasColumnShouldAcceptAColumn(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ctx := context.Background()
		q := tdb.newQuery(0, KindIssue, 0)
		_ = q.SetColumnNames(ctx, []string{"tracker", "subject"})
		tc, _ := q.findColumn(ctx, "tracker")
		cc, _ := q.findColumn(ctx, "category")
		if tc == nil || cc == nil {
			t.Fatal("columns not found")
		}
		if ok, _ := q.HasColumn(ctx, tc.Name); !ok {
			t.Error("tracker")
		}
		if ok, _ := q.HasColumn(ctx, cc.Name); ok {
			t.Error("category")
		}
	})
}

func TestQueryHasColumnShouldReturnTrueForDefaultColumn(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ctx := context.Background()
		tdb.setting("issue_list_default_columns", []any{"tracker", "subject"})
		q := tdb.newQuery(0, KindIssue, 0)
		if ok, _ := q.HasColumn(ctx, "tracker"); !ok {
			t.Error("tracker")
		}
		if ok, _ := q.HasColumn(ctx, "category"); ok {
			t.Error("category")
		}
	})
}

func TestQueryInlineAndBlockColumns(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ctx := context.Background()
		q := tdb.newQuery(0, KindIssue, 0)
		_ = q.SetColumnNames(ctx, []string{"subject", "description", "tracker", "last_notes"})
		inline, _ := q.InlineColumns(ctx)
		block, _ := q.BlockColumns(ctx)
		if got := columnNamesB(inline); !slices.Equal(got, []string{"id", "subject", "tracker"}) {
			t.Errorf("inline = %v", got)
		}
		if got := columnNamesB(block); !slices.Equal(got, []string{"description", "last_notes"}) {
			t.Errorf("block = %v", got)
		}
	})
}

func TestQueryCustomFieldColumnsShouldBeInline(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		cols, _ := q.AvailableColumns(context.Background())
		n := 0
		for _, c := range cols {
			if c.Kind == ColumnCustomField {
				n++
				if !c.Inline {
					t.Errorf("%s is not inline", c.Name)
				}
			}
		}
		if n == 0 {
			t.Error("no custom field columns")
		}
	})
}

func TestQueryQueryShouldPreloadSpentHours(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ctx := context.Background()
		q := tdb.newQuery(0, KindIssue, 0)
		_ = q.SetColumnNames(ctx, []string{"subject", "spent_hours"})
		if ok, _ := q.HasColumn(ctx, "spent_hours"); !ok {
			t.Fatal("spent_hours column")
		}
		rows, err := q.Issues(ctx, ListOptions{})
		if err != nil || len(rows) == 0 {
			t.Fatalf("rows = %d, %v", len(rows), err)
		}
		if rows[0].SpentHours == nil {
			t.Error("spent hours not preloaded")
		}
	})
}

func TestQueryQueryShouldPreloadLastUpdatedBy(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ctx := context.Background()
		q := issueQueryB(tdb, 2, 0)
		_ = q.SetColumnNames(ctx, []string{"subject", "last_updated_by"})
		mustFilter(t, q, "issue_id", "=", "1,2,3")
		rows, err := q.Issues(ctx, ListOptions{Order: []string{"issues.id ASC"}})
		if err != nil {
			t.Fatal(err)
		}
		var got []any
		for _, r := range rows {
			if r.LastUpdatedByID == nil {
				got = append(got, nil)
			} else {
				got = append(got, *r.LastUpdatedByID)
			}
		}
		want := []any{int64(2), int64(2), nil}
		if !slices.Equal(got, want) {
			t.Errorf("last_updated_by = %v, want %v", got, want)
		}
	})
}

func TestQueryQueryShouldPreloadLastNotes(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ctx := context.Background()
		q := tdb.newQuery(0, KindIssue, 0)
		_ = q.SetColumnNames(ctx, []string{"subject", "last_notes"})
		rows, err := q.Issues(ctx, ListOptions{})
		if err != nil || len(rows) == 0 {
			t.Fatalf("rows = %d, %v", len(rows), err)
		}
		if rows[0].LastNotes == nil {
			t.Error("last notes not preloaded")
		}
	})
}

func groupableB(t *testing.T, q *Query, name string) *Column {
	cols, err := q.GroupableColumns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cols {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestQueryGroupableColumnsShouldIncludeCustomFields(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		c := groupableB(t, tdb.newQuery(0, KindIssue, 0), "cf_1")
		if c == nil || c.Kind != ColumnCustomField {
			t.Errorf("column = %+v", c)
		}
	})
}

func TestQueryGroupableColumnsShouldNotIncludeMultiCustomFields(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`UPDATE custom_fields SET multiple = ? WHERE id = 1`, true)
		if c := groupableB(t, tdb.newQuery(0, KindIssue, 0), "cf_1"); c != nil {
			t.Error("multi custom field should not be groupable")
		}
	})
}

func TestQueryGroupableColumnsShouldIncludeUserCustomFields(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		cf := createCFB(tdb, cfOptsB{kind: "issue", name: "User", format: "user", forAll: true, trackers: []int64{1}})
		if groupableB(t, tdb.newQuery(0, KindIssue, 0), "cf_"+itoa(cf)) == nil {
			t.Error("user custom field should be groupable")
		}
	})
}

func TestQueryGroupableColumnsShouldIncludeVersionCustomFields(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		cf := createCFB(tdb, cfOptsB{kind: "issue", name: "User", format: "version", forAll: true, trackers: []int64{1}})
		if groupableB(t, tdb.newQuery(0, KindIssue, 0), "cf_"+itoa(cf)) == nil {
			t.Error("version custom field should be groupable")
		}
	})
}

func TestQueryGroupedWithValidColumn(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ctx := context.Background()
		q := tdb.newQuery(0, KindIssue, 0)
		q.GroupBy = "status"
		if ok, _ := q.Grouped(ctx); !ok {
			t.Fatal("not grouped")
		}
		c, _ := q.GroupByColumn(ctx)
		if c == nil || c.Name != "status" {
			t.Fatalf("column = %+v", c)
		}
		// Redmine の group_by_statement は 'status' (Rails が外部キーに読み替える)
		if s, _ := q.groupByStatement(ctx); s != "issues.status_id" {
			t.Errorf("group_by_statement = %q", s)
		}
	})
}

func TestQueryGroupedWithInvalidColumn(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ctx := context.Background()
		q := tdb.newQuery(0, KindIssue, 0)
		q.GroupBy = "foo"
		ok, _ := q.Grouped(ctx)
		c, _ := q.GroupByColumn(ctx)
		s, _ := q.groupByStatement(ctx)
		if ok || c != nil || s != "" {
			t.Errorf("grouped=%v column=%v statement=%q", ok, c, s)
		}
	})
}

func sortableB(t *testing.T, q *Query) map[string][]string {
	m, err := q.SortableColumns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestQuerySortableColumnsShouldSortAssigneesAccordingToUserFormatSetting(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.setting("user_format", "lastname_comma_firstname")
		got := sortableB(t, tdb.newQuery(0, KindIssue, 0))["assigned_to"]
		want := []string{"(users.lastname || users.name)", "users.firstname", "users.id"}
		if !slices.Equal(got, want) {
			t.Errorf("assigned_to = %v", got)
		}
	})
}

func TestQuerySortableColumnsShouldSortAuthorsAccordingToUserFormatSetting(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.setting("user_format", "lastname_comma_firstname")
		got := sortableB(t, tdb.newQuery(0, KindIssue, 0))["author"]
		want := []string{"(authors.lastname || authors.name)", "authors.firstname", "authors.id"}
		if !slices.Equal(got, want) {
			t.Errorf("author = %v", got)
		}
	})
}

func TestQuerySortableColumnsShouldSortLastUpdatedByAccordingToUserFormatSetting(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.setting("user_format", "lastname_comma_firstname")
		q := tdb.newQuery(0, KindIssue, 0)
		q.SetSortCriteria(SortCriteria{{"last_updated_by", "desc"}})
		got := sortableB(t, q)["last_updated_by"]
		want := []string{"(last_journal_user.lastname || last_journal_user.name)", "last_journal_user.firstname", "last_journal_user.id"}
		if !slices.Equal(got, want) {
			t.Errorf("last_updated_by = %v", got)
		}
		// ソートが実際に実行できること
		if _, err := q.IDs(context.Background(), ListOptions{}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestQuerySortableColumnsShouldIncludeCustomField(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		if len(sortableB(t, tdb.newQuery(0, KindIssue, 0))["cf_1"]) == 0 {
			t.Error("cf_1 should be sortable")
		}
	})
}

func TestQuerySortableColumnsShouldNotIncludeMultiCustomField(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`UPDATE custom_fields SET multiple = ? WHERE id = 1`, true)
		if len(sortableB(t, tdb.newQuery(0, KindIssue, 0))["cf_1"]) != 0 {
			t.Error("cf_1 should not be sortable")
		}
	})
}

func TestQuerySortableShouldReturnFalseForMultiCustomField(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`UPDATE custom_fields SET multiple = ? WHERE id = 1`, true)
		c, err := tdb.newQuery(0, KindIssue, 0).findColumn(context.Background(), "cf_1")
		if err != nil || c == nil {
			t.Fatalf("column = %v, %v", c, err)
		}
		if c.IsSortable() {
			t.Error("should not be sortable")
		}
	})
}

func TestQueryDefaultSort(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		got := tdb.newQuery(0, KindIssue, 0).SortCriteria()
		if !slices.Equal(got, SortCriteria{{"id", "desc"}}) {
			t.Errorf("sort = %v", got)
		}
	})
}
