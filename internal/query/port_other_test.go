// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package query

// Redmine の test/unit/time_entry_query_test.rb, project_query_test.rb, project_admin_query_test.rb,
// user_query_test.rb と lib/redmine/search_test.rb (Tokenizer) の移植。
//
// 移植しなかったテスト:
//   - ProjectQueryTest#test_results_scope_has_last_activity_date,
//     ProjectAdminQueryTest#test_results_scope_has_last_activity_date:
//     last_activity_date の読み込み (Redmine::Activity::Fetcher) はクエリパッケージの範囲外。
//   - ProjectQueryTest#test_project_statuses_values_should_equal_ancestors_return:
//     Query 基底クラスのインスタンスが無い (ProjectQuery の値が active/closed であることは別テストで確認)。
//   - ProjectQueryTest#test_default_columns の inline/block の確認は移植済み。

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/repository"
)

// ---------------------------------------------------------------- 補助

// loadAllValuesO はすべてのフィルタの選択肢を評価する (filter values should be arrays の移植)。
func loadAllValuesO(t *testing.T, q *Query) {
	t.Helper()
	ctx := context.Background()
	af, err := q.AvailableFilters(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range af.Defs() {
		if _, err := d.LoadValues(ctx); err != nil {
			t.Errorf("values for %s: %v", d.Field, err)
		}
	}
}

func filterKeysO(t *testing.T, q *Query) []string {
	t.Helper()
	af, err := q.AvailableFilters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return af.Keys()
}

func columnNamesO(t *testing.T, q *Query) []string {
	t.Helper()
	cols, err := q.AvailableColumns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range cols {
		out = append(out, c.Name)
	}
	return out
}

// createActivityO は TimeEntryActivity.create!。
func createActivityO(t *testing.T, tdb *testDB, name string, active bool, parentID, projectID any) int64 {
	t.Helper()
	id, err := tdb.d.InsertReturningID(context.Background(),
		`INSERT INTO time_entry_activities (name, position, is_default, active, project_id, parent_id) VALUES (?, (SELECT COALESCE(MAX(position), 0) + 1 FROM time_entry_activities), ?, ?, ?, ?)`,
		name, false, active, projectID, parentID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// teAttrsO は TimeEntry.generate! の属性 (ゼロ値は既定値)。
type teAttrsO struct {
	userID, projectID, issueID, activityID int64
	hours                                  float64
}

// generateTimeEntryO は TimeEntry.generate!: user 2、issue 1 (project はそのプロジェクト)、先頭の作業分類、今日、1.0 時間。
func generateTimeEntryO(t *testing.T, tdb *testDB, a teAttrsO) int64 {
	t.Helper()
	ctx := context.Background()
	if a.userID == 0 {
		a.userID = 2
	}
	var issue any
	if a.projectID == 0 {
		if a.issueID == 0 {
			a.issueID = 1
		}
		if err := tdb.d.Get(ctx, &a.projectID, `SELECT project_id FROM issues WHERE id = ?`, a.issueID); err != nil {
			t.Fatal(err)
		}
	}
	if a.issueID != 0 {
		issue = a.issueID
	}
	if a.activityID == 0 {
		if err := tdb.d.Get(ctx, &a.activityID, `SELECT id FROM time_entry_activities ORDER BY position, id LIMIT 1`); err != nil {
			t.Fatal(err)
		}
	}
	if a.hours == 0 {
		a.hours = 1.0
	}
	now := db.NewTime(frozenNow)
	y, w := frozenNow.ISOWeek()
	id, err := tdb.d.InsertReturningID(ctx, `INSERT INTO time_entries (project_id, user_id, author_id, issue_id, hours, activity_id, spent_on, tyear, tmonth, tweek, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, a.projectID, a.userID, a.userID, issue, a.hours, a.activityID, "2026-01-15", y, 1, w, now, now)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// sumHoursO は results_scope.sum(:hours)。
func sumHoursO(t *testing.T, tdb *testDB, q *Query) float64 {
	t.Helper()
	ids := queryIDs(t, q)
	if len(ids) == 0 {
		return 0
	}
	var s float64
	if err := tdb.d.Get(context.Background(), &s, `SELECT SUM(hours) FROM time_entries WHERE id IN (`+idList(ids)+`)`); err != nil {
		t.Fatal(err)
	}
	return s
}

var cfSeqO int

// createIssueCFO は IssueCustomField.generate! (string、全トラッカー)。
func createIssueCFO(t *testing.T, tdb *testDB, isForAll, isFilter bool, projectIDs ...int64) int64 {
	t.Helper()
	cfSeqO++
	id, err := tdb.d.InsertReturningID(context.Background(),
		`INSERT INTO custom_fields (owner_kind, name, field_format, is_for_all, is_filter, position) VALUES ('issue', ?, 'string', ?, ?, 100)`,
		fmt.Sprintf("Custom field O%d", cfSeqO), isForAll, isFilter)
	if err != nil {
		t.Fatal(err)
	}
	tdb.exec(`INSERT INTO custom_fields_trackers (custom_field_id, tracker_id) SELECT ?, id FROM trackers`, id)
	for _, p := range projectIDs {
		tdb.exec(`INSERT INTO custom_fields_projects (custom_field_id, project_id) VALUES (?, ?)`, id, p)
	}
	return id
}

// ---------------------------------------------------------------- TimeEntryQuery

func TestTimeEntryQueryFilterValuesWithoutProjectShouldBeArrays(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindTimeEntry, 0)
		if q.Project != nil {
			t.Fatal("project")
		}
		loadAllValuesO(t, q)
	})
}

func TestTimeEntryQueryFilterValuesWithProjectShouldBeArrays(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		loadAllValuesO(t, tdb.newQuery(0, KindTimeEntry, 1))
	})
}

func TestTimeEntryQueryCrossProjectActivityFilterShouldProposeNonActiveActivities(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		id := createActivityO(t, tdb, "Disabled", false, nil, nil)
		q := tdb.newQuery(0, KindTimeEntry, 0)
		af, err := q.AvailableFilters(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		d := af.Get("activity_id")
		if d == nil {
			t.Fatal("activity_id filter")
		}
		vals, _ := d.LoadValues(context.Background())
		if !slices.Contains(vals, Option{Label: "Disabled", Value: itoa(id)}) {
			t.Errorf("values %v should include Disabled", vals)
		}
	})
}

func TestTimeEntryQueryActivityFilterShouldConsiderSystemAndProjectActivities(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`DELETE FROM time_entries`)
		system := createActivityO(t, tdb, "Foo", true, nil, nil)
		generateTimeEntryO(t, tdb, teAttrsO{activityID: system, hours: 1.0})
		override := createActivityO(t, tdb, "Foo", true, system, int64(1))
		other := createActivityO(t, tdb, "Bar", true, nil, nil)
		generateTimeEntryO(t, tdb, teAttrsO{activityID: override, hours: 2.0})
		generateTimeEntryO(t, tdb, teAttrsO{activityID: other, hours: 4.0})

		q := tdb.newQuery(2, KindTimeEntry, 0)
		mustFilter(t, q, "activity_id", "=", itoa(system))
		if s := sumHoursO(t, tdb, q); s != 3.0 {
			t.Errorf("= sum %v, want 3.0", s)
		}
		q = tdb.newQuery(2, KindTimeEntry, 0)
		mustFilter(t, q, "activity_id", "!", itoa(system))
		if s := sumHoursO(t, tdb, q); s != 4.0 {
			t.Errorf("! sum %v, want 4.0", s)
		}
	})
}

func TestTimeEntryQueryProjectQueryShouldIncludeProjectIssueCustomFieldsOnly(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		global := createIssueCFO(t, tdb, true, true)
		onProject := createIssueCFO(t, tdb, false, true, 3)
		notOnProject := createIssueCFO(t, tdb, false, true, 1, 2)
		q := tdb.newQuery(0, KindTimeEntry, 3)
		keys := filterKeysO(t, q)
		cols := columnNamesO(t, q)
		for _, set := range [][]string{keys, cols} {
			if !slices.Contains(set, "issue.cf_"+itoa(global)) || !slices.Contains(set, "issue.cf_"+itoa(onProject)) {
				t.Errorf("should include global and project fields: %v", set)
			}
			if slices.Contains(set, "issue.cf_"+itoa(notOnProject)) {
				t.Errorf("should not include field not on project: %v", set)
			}
		}
	})
}

func TestTimeEntryQueryFilterAvailability(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		has := func(pid int64, f string) bool {
			return slices.Contains(filterKeysO(t, tdb.newQuery(0, KindTimeEntry, pid)), f)
		}
		// test_issue_category_filter_should_not_be_available_in_global_queries
		if has(0, "issue.category_id") {
			t.Error("issue.category_id in global query")
		}
		// test_project_status_filter_should_be_available_in_global_queries
		if !has(0, "project.status") {
			t.Error("project.status in global query")
		}
		// test_project_status_filter_should_be_available_when_project_has_subprojects
		if !has(1, "project.status") {
			t.Error("project.status in project 1")
		}
		// test_project_status_filter_should_not_be_available_when_project_is_leaf
		if has(2, "project.status") {
			t.Error("project.status in leaf project 2")
		}
	})
}

func TestTimeEntryQueryUserGroupFilterShouldConsiderSpecifiedGroupsTimeEntries(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ctx := context.Background()
		if err := repository.AddUserToGroup(ctx, tdb.d, 10, 2); err != nil {
			t.Fatal(err)
		}
		if err := repository.AddUserToGroup(ctx, tdb.d, 11, 3); err != nil {
			t.Fatal(err)
		}
		tdb.exec(`DELETE FROM time_entries`)
		t1 := generateTimeEntryO(t, tdb, teAttrsO{hours: 1.0, userID: 2})
		t2 := generateTimeEntryO(t, tdb, teAttrsO{hours: 2.0, userID: 2})
		t3 := generateTimeEntryO(t, tdb, teAttrsO{hours: 4.0, userID: 3})
		q := tdb.newQuery(0, KindTimeEntry, 0)
		assertIDs(t, queryIDs(t, q), []int64{t1, t2, t3})
		if s := sumHoursO(t, tdb, q); s != 7.0 {
			t.Errorf("sum %v", s)
		}
		mustFilter(t, q, "user.group", "=", "10")
		assertIDs(t, queryIDs(t, q), []int64{t1, t2})
		if s := sumHoursO(t, tdb, q); s != 3.0 {
			t.Errorf("sum %v", s)
		}
		mustFilter(t, q, "user.group", "=", "10", "11")
		assertIDs(t, queryIDs(t, q), []int64{t1, t2, t3})
		if s := sumHoursO(t, tdb, q); s != 7.0 {
			t.Errorf("sum %v", s)
		}
	})
}

func TestTimeEntryQueryUserRoleFilterShouldConsiderSpecifiedRolesTimeEntries(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		// フィクスチャで user 2 は project 1 の Manager (1)、user 3 は Developer (2)
		tdb.exec(`DELETE FROM time_entries`)
		t1 := generateTimeEntryO(t, tdb, teAttrsO{projectID: 1, hours: 1.0, userID: 2})
		t2 := generateTimeEntryO(t, tdb, teAttrsO{projectID: 1, hours: 2.0, userID: 2})
		t3 := generateTimeEntryO(t, tdb, teAttrsO{projectID: 1, hours: 4.0, userID: 3})
		q := tdb.newQuery(0, KindTimeEntry, 1)
		assertIDs(t, queryIDs(t, q), []int64{t1, t2, t3})
		if s := sumHoursO(t, tdb, q); s != 7.0 {
			t.Errorf("sum %v", s)
		}
		mustFilter(t, q, "user.role", "=", "1")
		assertIDs(t, queryIDs(t, q), []int64{t1, t2})
		if s := sumHoursO(t, tdb, q); s != 3.0 {
			t.Errorf("sum %v", s)
		}
		mustFilter(t, q, "user.role", "=", "1", "2")
		assertIDs(t, queryIDs(t, q), []int64{t1, t2, t3})
		if s := sumHoursO(t, tdb, q); s != 7.0 {
			t.Errorf("sum %v", s)
		}
	})
}

func TestTimeEntryQueryResultsScopeShouldBeInTheSameOrderWhenPaginating(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		for i := 0; i < 4; i++ {
			generateTimeEntryO(t, tdb, teAttrsO{})
		}
		q := tdb.newQuery(0, KindTimeEntry, 0)
		q.SetSortCriteria(SortCriteria{{"user", "asc"}})
		all := queryIDs(t, q)
		n, err := q.Count(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var paged []int64
		for i := 0; i < int(n)/2+1; i++ {
			ids, err := q.IDs(context.Background(), ListOptions{Offset: i * 2, Limit: 2})
			if err != nil {
				t.Fatal(err)
			}
			paged = append(paged, ids...)
		}
		if !slices.Equal(all, paged) {
			t.Errorf("paginated %v, want %v", paged, all)
		}
	})
}

// ---------------------------------------------------------------- ProjectQuery / ProjectAdminQuery

func TestProjectQueryFilterValuesBeArrays(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		loadAllValuesO(t, tdb.newQuery(0, KindProject, 0))
		loadAllValuesO(t, tdb.newQuery(0, KindProjectAdmin, 0))
	})
}

func statusValuesO(t *testing.T, q *Query) []Option {
	t.Helper()
	q.Filters = NewFilters()
	q.Filters.Set("status", Filter{Operator: "=", Values: []string{}})
	af, err := q.AvailableFilters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	vals, err := af.Get("status").LoadValues(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return vals
}

func TestProjectQueryProjectStatusesFilterShouldReturnProjectStatuses(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		got := statusValuesO(t, tdb.newQuery(0, KindProject, 0))
		want := []Option{{Label: "active", Value: "1"}, {Label: "closed", Value: "5"}}
		if !slices.Equal(got, want) {
			t.Errorf("values %v", got)
		}
	})
}

func TestProjectAdminQueryProjectStatusesFilterShouldReturnProjectStatuses(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(1, KindProjectAdmin, 0)
		got := statusValuesO(t, q)
		want := []Option{{Label: "active", Value: "1"}, {Label: "closed", Value: "5"},
			{Label: "archived", Value: "9"}, {Label: "scheduled for deletion", Value: "10"}}
		if !slices.Equal(got, want) {
			t.Errorf("values %v", got)
		}
		// test_project_statuses_values_should_return_all_statuses
		v, _ := q.projectStatusesValues(context.Background())
		if !slices.Equal(v, want) {
			t.Errorf("project_statuses_values %v", v)
		}
	})
}

func TestProjectQueryDefaultColumns(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ctx := context.Background()
		for _, k := range []Kind{KindProject, KindProjectAdmin} {
			q := tdb.newQuery(0, k, 0)
			cols, _ := q.Columns(ctx)
			inl, _ := q.InlineColumns(ctx)
			blk, _ := q.BlockColumns(ctx)
			if len(cols) == 0 || len(inl) == 0 || len(blk) != 0 {
				t.Errorf("%s: columns %d inline %d block %d", k, len(cols), len(inl), len(blk))
			}
			// test_available_columns_should_include_project_custom_fields
			if !slices.Contains(columnNamesO(t, q), "cf_3") {
				t.Errorf("%s: cf_3 column", k)
			}
		}
	})
}

func TestProjectQueryDisplayTypes(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		// test_available_display_types_should_returns_bord_and_list
		q := tdb.newQuery(0, KindProject, 0)
		if got := q.AvailableDisplayTypes(); !slices.Equal(got, []string{"board", "list"}) {
			t.Errorf("display types %v", got)
		}
		// test_display_type_default_should_equal_with_setting_project_list_display_type
		for _, typ := range []string{"board", "list"} {
			tdb.setting("project_list_display_type", typ)
			if got := tdb.newQuery(0, KindProject, 0).DisplayType(); got != typ {
				t.Errorf("display type %q, want %q", got, typ)
			}
			// ProjectAdminQuery#test_display_type_should_returns_list
			if got := tdb.newQuery(1, KindProjectAdmin, 0).DisplayType(); got != "list" {
				t.Errorf("admin display type %q", got)
			}
		}
		// ProjectAdminQuery#test_available_display_types_should_always_returns_list
		if got := tdb.newQuery(1, KindProjectAdmin, 0).AvailableDisplayTypes(); !slices.Equal(got, []string{"list"}) {
			t.Errorf("admin display types %v", got)
		}
	})
}

func defaultIDO(t *testing.T, tdb *testDB, userID int64, kind Kind) int64 {
	t.Helper()
	q, err := Default(context.Background(), tdb.env(userID), kind, nil)
	if err != nil {
		t.Fatal(err)
	}
	if q == nil {
		return 0
	}
	return q.ID
}

func TestProjectQueryShouldDetermineDefaultProjectQuery(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`UPDATE queries SET visibility = 2 WHERE id = 12`)
		users := []int64{0, 1}
		tdb.setting("default_project_query", "")
		for _, u := range users {
			if id := defaultIDO(t, tdb, u, KindProject); id != 0 {
				t.Errorf("user %d: default %d, want none", u, id)
			}
		}
		// 全体の既定のみ
		tdb.setting("default_project_query", "11")
		for _, u := range users {
			if id := defaultIDO(t, tdb, u, KindProject); id != 11 {
				t.Errorf("user %d: default %d, want 11", u, id)
			}
		}
		// 個人設定の既定が全体の既定より優先
		tdb.exec(`UPDATE user_preferences SET default_project_query_id = 12 WHERE user_id = 1`)
		if id := defaultIDO(t, tdb, 1, KindProject); id != 12 {
			t.Errorf("user default %d, want 12", id)
		}
	})
}

func TestProjectQueryDefaultShouldReturnNilIfDefaultQueryDestroyed(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.setting("default_project_query", "11")
		tdb.exec(`DELETE FROM queries WHERE id = 11`)
		if id := defaultIDO(t, tdb, 0, KindProject); id != 0 {
			t.Errorf("default %d", id)
		}
	})
}

func TestProjectAdminQueryNoDefaultProjectAdminQuery(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`UPDATE queries SET visibility = 2 WHERE id = 12`)
		check := func() {
			for _, u := range []int64{0, 1} {
				if id := defaultIDO(t, tdb, u, KindProjectAdmin); id != 0 {
					t.Errorf("user %d: admin default %d", u, id)
				}
			}
		}
		check()
		tdb.setting("default_project_query", "11")
		check()
		tdb.exec(`UPDATE user_preferences SET default_project_query_id = 12 WHERE user_id = 1`)
		check()
	})
}

func TestProjectQueryBaseScopeShouldReturnVisibleProjects(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindProject, 0)
		vis, err := q.env.Auth.VisibleProjectIDs(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, queryIDs(t, q), vis)
	})
}

func TestProjectAdminQueryBaseScopeShouldReturnAllProjects(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		assertIDs(t, queryIDs(t, tdb.newQuery(1, KindProjectAdmin, 0)), tdb.ints(`SELECT id FROM projects`))
	})
}

func TestProjectQueryResultsScopeWithOffsetAndLimit(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		for _, k := range []Kind{KindProject, KindProjectAdmin} {
			q := tdb.newQuery(1, k, 0)
			all := queryIDs(t, q)
			for i := 0; i < len(all)/2+1; i++ {
				ids, err := q.IDs(context.Background(), ListOptions{Offset: i * 2, Limit: 2})
				if err != nil {
					t.Fatal(err)
				}
				want := all[min(i*2, len(all)):min(i*2+2, len(all))]
				if !slices.Equal(ids, want) && !(len(ids) == 0 && len(want) == 0) {
					t.Errorf("%s page %d: %v, want %v", k, i, ids, want)
				}
			}
		}
	})
}

// ---------------------------------------------------------------- UserQuery

// findUsersO は User.where(query.statement) (状態の既定条件は statement 側)。
func findUsersO(t *testing.T, q *Query) []int64 {
	t.Helper()
	ctx := context.Background()
	st, args, err := q.Statement(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s := `SELECT users.id FROM principals users JOIN user_accounts ON user_accounts.principal_id = users.id
LEFT JOIN email_addresses ON email_addresses.user_id = users.id AND email_addresses.is_default = ` + q.env.boolLit(true) + `
WHERE users.kind IN ('user', 'anonymous_user')`
	if st != "" {
		s += " AND (" + st + ")"
	}
	s += " ORDER BY users.id"
	var ids []int64
	if err := q.env.Q.Select(ctx, &ids, s, args...); err != nil {
		t.Fatalf("%v\n%s", err, s)
	}
	return ids
}

func TestUserQueryAvailableColumnsShouldIncludeUserCustomFields(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		if !slices.Contains(columnNamesO(t, tdb.newQuery(1, KindUser, 0)), "cf_4") {
			t.Error("cf_4")
		}
	})
}

func TestUserQueryFilterValuesShouldBeArrays(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		loadAllValuesO(t, tdb.newQuery(1, KindUser, 0))
	})
}

func adminFlagsO(t *testing.T, tdb *testDB, ids []int64) []bool {
	t.Helper()
	var out []bool
	for _, id := range ids {
		var a bool
		if err := tdb.d.Get(context.Background(), &a, `SELECT admin FROM user_accounts WHERE principal_id = ?`, id); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out
}

func TestUserQueryFilterByAdmin(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		for _, c := range []struct {
			op, v string
			want  bool
		}{{"=", "1", true}, {"!", "1", false}, {"!", "0", true}, {"!", "1", false}} {
			q := tdb.newQuery(1, KindUser, 0)
			q.Filters = NewFilters()
			q.Filters.Set("admin", Filter{Operator: c.op, Values: []string{c.v}})
			if got := adminFlagsO(t, tdb, findUsersO(t, q)); !slices.Equal(got, []bool{c.want}) {
				t.Errorf("admin %s %s: %v", c.op, c.v, got)
			}
		}
	})
}

func TestUserQueryFilterByStatus(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(1, KindUser, 0)
		q.Filters = NewFilters()
		q.Filters.Set("status", Filter{Operator: "=", Values: []string{"3"}})
		if got := findUsersO(t, q); !slices.Equal(got, []int64{5}) {
			t.Errorf("locked users %v", got)
		}
	})
}

type userFilterCaseO struct {
	op, v string
	want  []int64
}

func runUserFilterO(t *testing.T, tdb *testDB, field string, cases []userFilterCaseO) {
	t.Helper()
	for _, c := range cases {
		q := tdb.newQuery(1, KindUser, 0)
		mustFilter(t, q, field, c.op, c.v)
		got := sorted(findUsersO(t, q))
		if !slices.Equal(got, c.want) && !(len(got) == 0 && len(c.want) == 0) {
			t.Errorf("%s %s %q: %v, want %v", field, c.op, c.v, got, c.want)
		}
	}
}

func TestUserQueryLoginFirstnameLastnameFilters(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		runUserFilterO(t, tdb, "login", []userFilterCaseO{{"~", "jsmith", []int64{2}}, {"^", "jsm", []int64{2}}, {"$", "ith", []int64{2}}})
		runUserFilterO(t, tdb, "firstname", []userFilterCaseO{{"~", "john", []int64{2}}})
		runUserFilterO(t, tdb, "lastname", []userFilterCaseO{{"~", "smith", []int64{2}}})
	})
}

var mailCasesO = []userFilterCaseO{
	{"~", "somenet", []int64{1, 2, 3, 4}},
	{"!~", "somenet", []int64{7, 8, 9}},
	{"^", "dlop", []int64{3}},
	{"$", "bar", []int64{7, 8, 9}},
	{"=", "bar", nil},
	{"=", "someone@foo.bar", []int64{7}},
	{"*", "", []int64{1, 2, 3, 4, 7, 8, 9}},
	{"!*", "", nil},
}

func TestUserQueryMailFilter(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) { runUserFilterO(t, tdb, "mail", mailCasesO) })
}

func TestUserQueryNameOrEmailOrLoginFilter(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		cases := append([]userFilterCaseO{
			{"~", "jsmith", []int64{2}}, {"^", "jsm", []int64{2}}, {"$", "ith", []int64{2}},
			{"~", "john", []int64{2}}, {"~", "smith", []int64{2}},
		}, mailCasesO...)
		runUserFilterO(t, tdb, "name", cases)
	})
}

func TestUserQueryGroupFilter(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		run := func(op string, values ...string) []int64 {
			q := tdb.newQuery(1, KindUser, 0)
			mustFilter(t, q, "is_member_of_group", op, values...)
			return findUsersO(t, q)
		}
		if got := run("=", "10", "99"); !slices.Equal(got, []int64{8}) {
			t.Errorf("= %v", got)
		}
		if got := run("!", "10"); len(got) == 0 || slices.Contains(got, 8) {
			t.Errorf("! %v", got)
		}
		if got := run("*", ""); !slices.Equal(got, []int64{8}) {
			t.Errorf("* %v", got)
		}
		if got := run("!*", ""); len(got) == 0 || slices.Contains(got, 8) {
			t.Errorf("!* %v", got)
		}
	})
}

func insertAuthSourceO(t *testing.T, tdb *testDB, id int64, name string) {
	t.Helper()
	now := db.NewTime(frozenNow)
	tdb.exec(`INSERT INTO auth_sources (id, kind, name, created_at, updated_at) VALUES (?, 'ldap', ?, ?, ?)`, id, name, now, now)
}

func TestUserQueryAuthSourceFilter(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		insertAuthSourceO(t, tdb, 1, "LDAP test server")
		tdb.exec(`UPDATE user_accounts SET auth_source_id = 1 WHERE principal_id = 1`)
		run := func(op string, values ...string) []int64 {
			q := tdb.newQuery(1, KindUser, 0)
			mustFilter(t, q, "auth_source_id", op, values...)
			return findUsersO(t, q)
		}
		if got := run("=", "1"); !slices.Equal(got, []int64{1}) {
			t.Errorf("= %v", got)
		}
		if got := run("*", ""); !slices.Equal(got, []int64{1}) {
			t.Errorf("* %v", got)
		}
		if got := run("!*", ""); len(got) == 0 || slices.Contains(got, 1) {
			t.Errorf("!* %v", got)
		}
	})
}

func TestUserQueryAuthSourceOrdering(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		insertAuthSourceO(t, tdb, 1, "LDAP test server")
		insertAuthSourceO(t, tdb, 2, "Auth")
		tdb.exec(`UPDATE user_accounts SET auth_source_id = 1 WHERE principal_id = 1`)
		tdb.exec(`UPDATE user_accounts SET auth_source_id = 2 WHERE principal_id = 2`)
		q := tdb.newQuery(1, KindUser, 0)
		mustFilter(t, q, "auth_source_id", "*", "")
		if err := q.SetColumnNames(context.Background(), []string{"id", "auth_source.name"}); err != nil {
			t.Fatal(err)
		}
		q.SetSortCriteria(SortCriteria{{"auth_source.name", "asc"}})
		if got := queryIDs(t, q); !slices.Equal(got, []int64{2, 1}) {
			t.Errorf("ids %v, want [2 1]", got)
		}
	})
}

func TestUserQueryIsOnlyVisibleAndEditableByAdmins(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ctx := context.Background()
		q := tdb.newQuery(1, KindUser, 0)
		if err := q.Save(ctx); err != nil {
			t.Fatal(err)
		}
		admin, user := tdb.env(1).Auth, tdb.env(2).Auth
		if ok, _ := q.VisibleTo(ctx, admin); !ok {
			t.Error("visible to admin")
		}
		if ok, _ := q.VisibleTo(ctx, user); ok {
			t.Error("visible to user")
		}
		has := func(a *Env) bool {
			l, err := ListVisible(ctx, a.Auth, KindUser, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			return slices.ContainsFunc(l, func(s SavedQuery) bool { return s.ID == q.ID })
		}
		if !has(tdb.env(1)) || has(tdb.env(2)) {
			t.Error("UserQuery.visible")
		}
		// test_user_query_is_only_editable_by_admins
		if ok, _ := q.EditableBy(ctx, admin); !ok {
			t.Error("editable by admin")
		}
		if ok, _ := q.EditableBy(ctx, user); ok {
			t.Error("editable by user")
		}
	})
}

// ---------------------------------------------------------------- Tokenizer (lib/redmine/search_test.rb)

func TestTokenizerTokenize(t *testing.T) {
	for in, want := range map[string][]string{
		`hello "bye bye"`:           {"hello", "bye bye"},
		"全角　スペース":                   {"全角", "スペース"},
		`"phrase one" "phrase two"`: {"phrase one", "phrase two"},
		"a 漢 bb cc dd ee ff gg":     {"漢", "bb", "cc", "dd", "ee"},
		"x x yy yy":                 {"yy"},
	} {
		if got := Tokenize(in); !slices.Equal(got, want) {
			t.Errorf("Tokenize(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---------------------------------------------------------------- version_field_format_test.rb

// test_query_filter_options_should_include_versions_with_any_status /
// test_query_filter_options_should_include_version_status_for_grouping
// (possible_values_options 側の確認は書式の編集 UI の範囲なので省略)
func TestFieldFormatVersionQueryFilterOptionsShouldIncludeAnyStatus(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		now := db.NewTime(frozenNow)
		vid, err := tdb.d.InsertReturningID(context.Background(),
			`INSERT INTO versions (project_id, name, status, created_at, updated_at) VALUES (1, 'Version O1', 'locked', ?, ?)`, now, now)
		if err != nil {
			t.Fatal(err)
		}
		cf := &customfield.CustomField{ID: 999, OwnerKind: customfield.KindIssue, FieldFormat: "version",
			Settings: map[string]any{"version_status": []any{"open"}}}
		q := tdb.newQuery(0, KindIssue, 1)
		vals, err := q.customFieldFilterValues(context.Background(), cf)
		if err != nil {
			t.Fatal(err)
		}
		want := Option{Label: "eCookbook - Version O1", Value: itoa(vid), Group: "locked"}
		if !slices.Contains(vals, want) {
			t.Errorf("values %v should include %v", vals, want)
		}
	})
}

// ---------------------------------------------------------------- SortCriteria (Redmine::SortCriteria)

func TestSortCriteriaNormalize(t *testing.T) {
	c := ParseSortCriteria("priority:desc,id,,priority,tracker:asc,subject:desc")
	want := SortCriteria{{"priority", "desc"}, {"id", "asc"}, {"tracker", "asc"}}
	if !slices.Equal(c, want) {
		t.Errorf("ParseSortCriteria = %v", c)
	}
	if p := c.ToParam(); p != "priority:desc,id,tracker" {
		t.Errorf("ToParam = %q", p)
	}
	added := c.Add("id", "desc")
	if !slices.Equal(added, SortCriteria{{"id", "desc"}, {"priority", "desc"}, {"tracker", "asc"}}) {
		t.Errorf("Add = %v", added)
	}
	if c.FirstKey() != "priority" || c.FirstAsc() || c.OrderFor("tracker") != "asc" || c.OrderFor("x") != "" {
		t.Error("accessors")
	}
	if got := c.sortClause(map[string][]string{"priority": {"p.position"}, "id": {"issues.id"}, "tracker": {"t.position", "t.name ASC"}}); !slices.Equal(got,
		[]string{"p.position DESC", "issues.id ASC", "t.position ASC", "t.name ASC"}) {
		t.Errorf("sortClause = %v", got)
	}
}
