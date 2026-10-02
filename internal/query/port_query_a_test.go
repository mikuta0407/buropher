package query

// test/unit/query_test.rb (1〜1099 行) の移植。
//
// Redmine の find_issues_with_query は可視性を見ない (statement のみ) ため findIssueIDs を使う。
// User.current = nil は匿名ユーザ (id 0 を指定)。日付は frozenNow (2026-01-15) を「今日」とする。
// SQL 文字列を検査するテストは、Redmine が値を埋め込むのに対し buropher はプレースホルダで渡すため、
// SQL 断片と引数の両方を確認する。
//
// 移植しなかったテスト: なし (範囲内の全テストを移植)。

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/testfixtures"
)

// ---------------------------------------------------------------- 補助

// stmtA は statement の SQL と引数。
func stmtA(t *testing.T, q *Query) (string, []any) {
	t.Helper()
	s, args, err := q.Statement(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s, args
}

func assertStmtIncludesA(t *testing.T, q *Query, sub string) {
	t.Helper()
	s, _ := stmtA(t, q)
	if !strings.Contains(s, sub) {
		t.Errorf("statement %q does not include %q", s, sub)
	}
}

func assertArgsA(t *testing.T, q *Query, want ...any) {
	t.Helper()
	_, args := stmtA(t, q)
	for _, w := range want {
		if !slices.Contains(args, w) {
			t.Errorf("args %v do not include %v", args, w)
		}
	}
}

// issueCFA はトラッカー 1〜3 に有効な全プロジェクト用チケットカスタムフィールドを作る。
func issueCFA(tdb *testDB, format string, possible string, multiple, withTrackers bool) int64 {
	tdb.t.Helper()
	var pv any
	if possible != "" {
		pv = possible
	}
	id, err := tdb.d.InsertReturningID(context.Background(), `INSERT INTO custom_fields (owner_kind, name, field_format, possible_values, is_for_all, is_filter, multiple, position)
VALUES ('issue', 'filter', ?, ?, ?, ?, ?, 20)`, format, pv, true, true, multiple)
	if err != nil {
		tdb.t.Fatal(err)
	}
	if withTrackers {
		for _, tr := range []int64{1, 2, 3} {
			tdb.exec(`INSERT INTO custom_fields_trackers (custom_field_id, tracker_id) VALUES (?, ?)`, id, tr)
		}
	}
	return id
}

// cvA はカスタム値を作る (空文字列もそのまま保存する。D-17)。
func cvA(tdb *testDB, cf int64, kind string, id int64, value string) {
	tdb.t.Helper()
	tdb.exec(`INSERT INTO custom_values (customized_kind, customized_id, custom_field_id, value) VALUES (?, ?, ?, ?)`, kind, id, cf, value)
}

func cfFilterA(cf int64) string { return "cf_" + itoa(cf) }

// dueDateA はチケットの期日 (無ければ nil)。
func dueDateA(tdb *testDB, id int64) *time.Time {
	tdb.t.Helper()
	var d db.NullDate
	if err := tdb.d.Get(context.Background(), &d, `SELECT due_date FROM issues WHERE id = ?`, id); err != nil {
		tdb.t.Fatal(err)
	}
	if !d.Valid {
		return nil
	}
	t := d.Date.Time
	return &t
}

func todayA(days int) time.Time { return time.Date(2026, 1, 15+days, 0, 0, 0, 0, time.UTC) }

func setDueDateA(tdb *testDB, id int64, d time.Time) {
	tdb.t.Helper()
	tdb.exec(`UPDATE issues SET due_date = ? WHERE id = ?`, d.Format("2006-01-02"), id)
}

// replaceFiltersA は IssueQuery.new(:filters => {...}) 相当 (既定フィルタを置き換え、利用可能かは見ない)。
func replaceFiltersA(q *Query, field, op string, values ...string) {
	q.Filters = NewFilters()
	q.Filters.Set(field, Filter{Operator: op, Values: values})
}

func journalA(tdb *testDB, issue, user int64, notes string, private bool) int64 {
	tdb.t.Helper()
	id, err := tdb.d.InsertReturningID(context.Background(), `INSERT INTO issue_journals (issue_id, user_id, notes, private_notes, created_at) VALUES (?, ?, ?, ?, ?)`,
		issue, user, notes, private, db.Now())
	if err != nil {
		tdb.t.Fatal(err)
	}
	return id
}

// changeStatusA は init_journal + update(status_id: s) (ジャーナル詳細を残してステータスを変更)。
func changeStatusA(tdb *testDB, issue, user, status int64) {
	tdb.t.Helper()
	var old int64
	if err := tdb.d.Get(context.Background(), &old, `SELECT status_id FROM issues WHERE id = ?`, issue); err != nil {
		tdb.t.Fatal(err)
	}
	jid, err := tdb.d.InsertReturningID(context.Background(), `INSERT INTO issue_journals (issue_id, user_id, private_notes, created_at) VALUES (?, ?, ?, ?)`,
		issue, user, false, db.Now())
	if err != nil {
		tdb.t.Fatal(err)
	}
	tdb.exec(`INSERT INTO issue_journal_details (journal_id, property, prop_key, old_value, value) VALUES (?, 'attr', 'status_id', ?, ?)`, jid, itoa(old), itoa(status))
	tdb.exec(`UPDATE issues SET status_id = ? WHERE id = ?`, status, issue)
}

func timeEntryA(tdb *testDB, issue int64) int64 {
	tdb.t.Helper()
	now := db.Now()
	id, err := tdb.d.InsertReturningID(context.Background(), `INSERT INTO time_entries (project_id, user_id, author_id, issue_id, hours, activity_id, spent_on, tyear, tmonth, tweek, created_at, updated_at)
VALUES (1, 2, 2, ?, 1.0, 9, '2026-01-15', 2026, 1, 3, ?, ?)`, issue, now, now)
	if err != nil {
		tdb.t.Fatal(err)
	}
	return id
}

func timeEntriesWhereA(t *testing.T, tdb *testDB, q *Query) []int64 {
	t.Helper()
	s, args := stmtA(t, q)
	return tdb.ints(`SELECT time_entries.id FROM time_entries WHERE `+s+` ORDER BY time_entries.id`, args...)
}

func filterNamesA(t *testing.T, q *Query) []string {
	t.Helper()
	af, err := q.AvailableFilters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range af.Defs() {
		out = append(out, d.Name)
	}
	return out
}

func filterValuesA(t *testing.T, q *Query, field string) []string {
	t.Helper()
	af, err := q.AvailableFilters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	d := af.Get(field)
	if d == nil {
		t.Fatalf("filter %s is not available", field)
	}
	vals, err := d.LoadValues(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, o := range vals {
		out = append(out, o.Value)
	}
	return out
}

func intersectA(a, b []string) []string {
	var out []string
	for _, x := range a {
		if slices.Contains(b, x) {
			out = append(out, x)
		}
	}
	return out
}

func validA(t *testing.T, q *Query) bool {
	t.Helper()
	ok, err := q.Valid(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// ---------------------------------------------------------------- テスト

func TestQueryQueryWithRolesVisibilityShouldValidateRoles(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ctx := context.Background()
		q := tdb.newQuery(0, KindIssue, 0)
		q.Name, q.Visibility = "Query", VisibilityRoles
		err := q.Save(ctx)
		inv, ok := err.(*ErrInvalid)
		if !ok || !slices.Contains(inv.Messages, "Roles cannot be blank") {
			t.Fatalf("save error = %v", err)
		}
		q.RoleIDs = []int64{1, 2}
		if err := q.Save(ctx); err != nil {
			t.Fatal(err)
		}
	})
}

func TestQueryChangingRolesVisibilityShouldClearRoles(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ctx := context.Background()
		q := tdb.newQuery(0, KindIssue, 0)
		q.Name, q.Visibility, q.RoleIDs = "Query", VisibilityRoles, []int64{1, 2}
		if err := q.Save(ctx); err != nil {
			t.Fatal(err)
		}
		if n := tdb.ints(`SELECT COUNT(*) FROM queries_roles WHERE query_id = ?`, q.ID); n[0] != 2 {
			t.Fatalf("roles = %d", n[0])
		}
		q.Visibility = VisibilityPublic
		if err := q.Save(ctx); err != nil {
			t.Fatal(err)
		}
		if n := tdb.ints(`SELECT COUNT(*) FROM queries_roles WHERE query_id = ?`, q.ID); n[0] != 0 {
			t.Errorf("roles = %d", n[0])
		}
	})
}

func TestQueryAvailableFiltersShouldBeOrdered(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		af, _ := q.AvailableFilters(context.Background())
		if af.Keys()[0] != "status_id" {
			t.Errorf("first filter = %s", af.Keys()[0])
		}
		want := []string{"Status", "Project", "Tracker", "Priority"}
		if got := intersectA(filterNamesA(t, q), want); !slices.Equal(got, want) {
			t.Errorf("order = %v", got)
		}
	})
}

func TestQueryAvailableFiltersWithCustomFieldsShouldBeOrdered(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`INSERT INTO custom_fields (owner_kind, name, field_format, is_for_all, is_filter, position) VALUES ('user', 'order test', 'string', ?, ?, 4)`, true, true)
		q := tdb.newQuery(0, KindIssue, 0)
		want := []string{"Searchable field", "Database", "Project's Development status", "Author's order test", "Assignee's order test"}
		if got := intersectA(filterNamesA(t, q), want); !slices.Equal(got, want) {
			t.Errorf("order = %v", got)
		}
	})
}

func TestQueryCustomFieldsForAllProjectsShouldBeAvailableInGlobalQueries(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		af, _ := q.AvailableFilters(context.Background())
		if !af.Has("cf_1") || af.Has("cf_3") {
			t.Errorf("cf_1=%v cf_3=%v", af.Has("cf_1"), af.Has("cf_3"))
		}
	})
}

func TestQuerySystemSharedVersionsShouldBeAvailableInGlobalQueries(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`UPDATE versions SET sharing = 'system' WHERE id = 2`)
		q := tdb.newQuery(0, KindIssue, 0)
		if !slices.Contains(filterValuesA(t, q, "fixed_version_id"), "2") {
			t.Error("version 2 not in values")
		}
	})
}

func TestQueryProjectFilterInGlobalQueries(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		ids := filterValuesA(t, q, "project_id")
		if !slices.Contains(ids, "1") || slices.Contains(ids, "2") {
			t.Errorf("project values = %v", ids)
		}
	})
}

func TestQueryAvailableFiltersShouldNotIncludeFieldsDisabledOnAllTrackers(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`UPDATE trackers SET disabled_core_fields = '["start_date"]'`)
		q := tdb.newQuery(0, KindIssue, 0)
		af, _ := q.AvailableFilters(context.Background())
		if !af.Has("due_date") || af.Has("start_date") {
			t.Errorf("due_date=%v start_date=%v", af.Has("due_date"), af.Has("start_date"))
		}
	})
}

func testFilterValuesAreArraysA(t *testing.T, project int64) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, project)
		af, _ := q.AvailableFilters(context.Background())
		for _, d := range af.Defs() {
			if _, err := d.LoadValues(context.Background()); err != nil {
				t.Errorf("values for %s: %v", d.Field, err)
			}
		}
	})
}

func TestQueryFilterValuesWithoutProjectShouldBeArrays(t *testing.T) {
	testFilterValuesAreArraysA(t, 0)
}
func TestQueryFilterValuesWithProjectShouldBeArrays(t *testing.T) { testFilterValuesAreArraysA(t, 1) }

func TestQueryQueryShouldAllowSharedVersionsForAProjectQuery(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 1)
		if !slices.Contains(filterValuesA(t, q, "fixed_version_id"), "4") {
			t.Error("subproject version 4 not in values")
		}
	})
}

func TestQueryQueryWithMultipleCustomFields(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q, err := Load(context.Background(), tdb.env(0), 1, KindIssue)
		if err != nil || q == nil {
			t.Fatal(q, err)
		}
		if !validA(t, q) {
			t.Fatal("invalid")
		}
		assertIDs(t, findIssueIDs(t, q), []int64{3})
	})
}

func TestQueryOperatorNone(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 1)
		mustFilter(t, q, "fixed_version_id", "!*", "")
		mustFilter(t, q, "cf_1", "!*", "")
		assertStmtIncludesA(t, q, "issues.fixed_version_id IS NULL")
		assertStmtIncludesA(t, q, "custom_values.value IS NULL OR custom_values.value = ''")
		findIssueIDs(t, q)
	})
}

func TestQueryOperatorNoneForInteger(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 1)
		mustFilter(t, q, "estimated_hours", "!*", "")
		ids := findIssueIDs(t, q)
		if len(ids) == 0 {
			t.Fatal("empty")
		}
		if n := tdb.ints(`SELECT COUNT(*) FROM issues WHERE estimated_hours IS NOT NULL AND id IN (` + idList(ids) + `)`); n[0] != 0 {
			t.Error("issue with estimated hours")
		}
	})
}

func TestQueryOperatorNoneForDate(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 1)
		mustFilter(t, q, "start_date", "!*", "")
		ids := findIssueIDs(t, q)
		if len(ids) == 0 {
			t.Fatal("empty")
		}
		if n := tdb.ints(`SELECT COUNT(*) FROM issues WHERE start_date IS NOT NULL AND id IN (` + idList(ids) + `)`); n[0] != 0 {
			t.Error("issue with start date")
		}
	})
}

func TestQueryOperatorNoneForStringCustomField(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`UPDATE custom_fields SET default_value = NULL WHERE id = 2`)
		q := tdb.newQuery(0, KindIssue, 1)
		mustFilter(t, q, "cf_2", "!*", "")
		ids := findIssueIDs(t, q)
		if len(ids) == 0 {
			t.Fatal("empty")
		}
		if n := tdb.ints(`SELECT COUNT(*) FROM custom_values WHERE custom_field_id = 2 AND customized_kind = 'issue' AND value <> '' AND customized_id IN (` + idList(ids) + `)`); n[0] != 0 {
			t.Errorf("issue with cf_2 value in %v", ids)
		}
	})
}

func TestQueryOperatorNoneForBlankText(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, "status_id", "*", "")
		mustFilter(t, q, "description", "!*", "")
		assertIDs(t, findIssueIDs(t, q), []int64{11, 12})
	})
}

func TestQueryOperatorAnyForBlankText(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		// update_all(description: '')
		tdb.exec(`UPDATE issues SET description = '' WHERE id IN (1, 2)`)
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, "status_id", "*", "")
		mustFilter(t, q, "description", "*", "")
		ids := findIssueIDs(t, q)
		if len(ids) == 0 || slices.Contains(ids, 1) || slices.Contains(ids, 2) {
			t.Errorf("ids = %v", ids)
		}
	})
}

func TestQueryOperatorAll(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 1)
		mustFilter(t, q, "fixed_version_id", "*", "")
		mustFilter(t, q, "cf_1", "*", "")
		assertStmtIncludesA(t, q, "issues.fixed_version_id IS NOT NULL")
		assertStmtIncludesA(t, q, "custom_values.value IS NOT NULL AND custom_values.value <> ''")
		findIssueIDs(t, q)
	})
}

func TestQueryOperatorAllForDate(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 1)
		mustFilter(t, q, "start_date", "*", "")
		ids := findIssueIDs(t, q)
		if len(ids) == 0 {
			t.Fatal("empty")
		}
		if n := tdb.ints(`SELECT COUNT(*) FROM issues WHERE start_date IS NULL AND id IN (` + idList(ids) + `)`); n[0] != 0 {
			t.Error("issue without start date")
		}
	})
}

func TestQueryOperatorAllForStringCustomField(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 1)
		mustFilter(t, q, "cf_2", "*", "")
		ids := findIssueIDs(t, q)
		if len(ids) == 0 {
			t.Fatal("empty")
		}
		for _, id := range ids {
			if n := tdb.ints(`SELECT COUNT(*) FROM custom_values WHERE custom_field_id = 2 AND customized_kind = 'issue' AND value <> '' AND customized_id = ?`, id); n[0] == 0 {
				t.Errorf("issue %d has blank cf_2", id)
			}
		}
	})
}

func TestQueryNumericFilterShouldNotAcceptNonNumericValues(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, "estimated_hours", "=", "a")
		if validA(t, q) {
			t.Error("valid")
		}
	})
}

func TestQueryOperatorIsOnFloat(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`UPDATE issues SET estimated_hours = 171.2 WHERE id = 2`)
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, "estimated_hours", "=", "171.20")
		assertIDs(t, findIssueIDs(t, q), []int64{2})
	})
}

func TestQueryOperatorIsOnIssueIDShouldAcceptCommaSeparatedValues(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, "issue_id", "=", "1,3")
		assertIDs(t, findIssueIDs(t, q), []int64{1, 3})
	})
}

func TestQueryOperatorIsOnParentIDShouldAcceptCommaSeparatedValues(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`UPDATE issues SET parent_id = 1 WHERE id IN (2, 4)`)
		tdb.exec(`UPDATE issues SET parent_id = 3 WHERE id = 5`)
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, "parent_id", "=", "1,3")
		assertIDs(t, findIssueIDs(t, q), []int64{2, 4, 5})
	})
}

func TestQueryOperatorIsOnChildIDShouldAcceptCommaSeparatedValues(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`UPDATE issues SET parent_id = 1 WHERE id IN (2, 4)`)
		tdb.exec(`UPDATE issues SET parent_id = 3 WHERE id = 5`)
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, "child_id", "=", "2,4,5")
		assertIDs(t, findIssueIDs(t, q), []int64{1, 3})
	})
}

func TestQueryOperatorBetweenOnIssueIDShouldReturnRange(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, "issue_id", "><", "2", "3")
		assertIDs(t, findIssueIDs(t, q), []int64{2, 3})
	})
}

func testCFValueFilterA(t *testing.T, format string, values [3]string, op string, filterValues []string, project int64, want []int64, checkValid bool) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		cf := issueCFA(tdb, format, "", false, true)
		for i, v := range values {
			cvA(tdb, cf, "issue", int64(i+1), v)
		}
		q := tdb.newQuery(0, KindIssue, project)
		mustFilter(t, q, cfFilterA(cf), op, filterValues...)
		if checkValid && !validA(t, q) {
			t.Fatal("invalid")
		}
		assertIDs(t, findIssueIDs(t, q), want)
	})
}

func TestQueryOperatorIsOnIntegerCustomField(t *testing.T) {
	testCFValueFilterA(t, "int", [3]string{"7", "12", ""}, "=", []string{"12"}, 0, []int64{2}, false)
}

func TestQueryOperatorIsOnIntegerCustomFieldShouldAcceptNegativeValue(t *testing.T) {
	testCFValueFilterA(t, "int", [3]string{"7", "-12", ""}, "=", []string{"-12"}, 0, []int64{2}, true)
}

func TestQueryOperatorIsOnFloatCustomField(t *testing.T) {
	testCFValueFilterA(t, "float", [3]string{"7.3", "12.7", ""}, "=", []string{"12.7"}, 0, []int64{2}, false)
}

func TestQueryOperatorIsOnFloatCustomFieldShouldAcceptNegativeValue(t *testing.T) {
	testCFValueFilterA(t, "float", [3]string{"7.3", "-12.7", ""}, "=", []string{"-12.7"}, 0, []int64{2}, true)
}

func multiListCFA(tdb *testDB) int64 {
	cf := issueCFA(tdb, "list", `["value1","value2","value3"]`, true, true)
	cvA(tdb, cf, "issue", 1, "value1")
	cvA(tdb, cf, "issue", 1, "value2")
	cvA(tdb, cf, "issue", 3, "value1")
	return cf
}

func TestQueryOperatorIsOnMultiListCustomField(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		cf := multiListCFA(tdb)
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, cfFilterA(cf), "=", "value1")
		assertIDs(t, findIssueIDs(t, q), []int64{1, 3})
		q = tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, cfFilterA(cf), "=", "value2")
		assertIDs(t, findIssueIDs(t, q), []int64{1})
	})
}

func TestQueryOperatorIsNotOnMultiListCustomField(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		cf := multiListCFA(tdb)
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, cfFilterA(cf), "!", "value1")
		ids := findIssueIDs(t, q)
		if slices.Contains(ids, 1) || slices.Contains(ids, 3) {
			t.Errorf("ids = %v", ids)
		}
		q = tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, cfFilterA(cf), "!", "value2")
		ids = findIssueIDs(t, q)
		if slices.Contains(ids, 1) || !slices.Contains(ids, 3) {
			t.Errorf("ids = %v", ids)
		}
	})
}

func TestQueryOperatorIsOnStringCustomFieldWithUtf8Value(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		cf := issueCFA(tdb, "string", "", false, true)
		cvA(tdb, cf, "issue", 1, "Kiểm")
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, cfFilterA(cf), "=", "Kiểm")
		assertIDs(t, findIssueIDs(t, q), []int64{1})
	})
}

func testIsPrivateFilterA(t *testing.T, op string, wantPrivate bool) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(2, KindIssue, 0)
		mustFilter(t, q, "is_private", op, "1")
		ids := findIssueIDs(t, q)
		if len(ids) == 0 {
			t.Fatal("empty")
		}
		if n := tdb.ints(`SELECT COUNT(*) FROM issues WHERE is_private = ? AND id IN (`+idList(ids)+`)`, !wantPrivate); n[0] != 0 {
			t.Errorf("ids = %v", ids)
		}
	})
}

func TestQueryOperatorIsOnIsPrivateField(t *testing.T)    { testIsPrivateFilterA(t, "=", true) }
func TestQueryOperatorIsNotOnIsPrivateField(t *testing.T) { testIsPrivateFilterA(t, "!", false) }

func TestQueryOperatorGreaterThan(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 1)
		mustFilter(t, q, "done_ratio", ">=", "40")
		assertStmtIncludesA(t, q, "issues.done_ratio >= ?")
		assertArgsA(t, q, 40.0)
		findIssueIDs(t, q)
	})
}

func TestQueryOperatorGreaterThanAFloat(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 1)
		mustFilter(t, q, "estimated_hours", ">=", "40.5")
		assertStmtIncludesA(t, q, "issues.estimated_hours >= ?")
		assertArgsA(t, q, 40.5)
		findIssueIDs(t, q)
	})
}

func TestQueryOperatorGreaterThanOnIntCustomField(t *testing.T) {
	testCFValueFilterA(t, "int", [3]string{"7", "12", ""}, ">=", []string{"8"}, 1, []int64{2}, false)
}

func TestQueryOperatorLesserThan(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 1)
		mustFilter(t, q, "done_ratio", "<=", "30")
		assertStmtIncludesA(t, q, "issues.done_ratio <= ?")
		assertArgsA(t, q, 30.0)
		findIssueIDs(t, q)
	})
}

func TestQueryOperatorLesserThanOnCustomField(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		cf := issueCFA(tdb, "int", "", false, false)
		q := tdb.newQuery(0, KindIssue, 1)
		mustFilter(t, q, cfFilterA(cf), "<=", "30")
		s, _ := stmtA(t, q)
		if !strings.Contains(s, "CAST(") || !strings.Contains(s, ") <= ?") {
			t.Errorf("statement = %s", s)
		}
		assertArgsA(t, q, 30.0)
		findIssueIDs(t, q)
	})
}

func TestQueryOperatorLesserThanOnDateCustomField(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		cf := issueCFA(tdb, "date", "", false, true)
		cvA(tdb, cf, "issue", 1, "2013-04-11")
		cvA(tdb, cf, "issue", 2, "2013-05-14")
		cvA(tdb, cf, "issue", 3, "")
		q := tdb.newQuery(0, KindIssue, 1)
		mustFilter(t, q, cfFilterA(cf), "<=", "2013-05-01")
		ids := findIssueIDs(t, q)
		if !slices.Contains(ids, 1) || slices.Contains(ids, 2) || slices.Contains(ids, 3) {
			t.Errorf("ids = %v", ids)
		}
	})
}

func TestQueryOperatorBetween(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 1)
		mustFilter(t, q, "done_ratio", "><", "30", "40")
		assertStmtIncludesA(t, q, "issues.done_ratio BETWEEN ? AND ?")
		assertArgsA(t, q, 30.0, 40.0)
		findIssueIDs(t, q)
	})
}

func TestQueryOperatorBetweenOnCustomField(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		cf := issueCFA(tdb, "int", "", false, false)
		q := tdb.newQuery(0, KindIssue, 1)
		mustFilter(t, q, cfFilterA(cf), "><", "30", "40")
		s, _ := stmtA(t, q)
		if !strings.Contains(s, "CAST(") || !strings.Contains(s, " BETWEEN ? AND ?") {
			t.Errorf("statement = %s", s)
		}
		assertArgsA(t, q, 30.0, 40.0)
		findIssueIDs(t, q)
	})
}

func TestQueryTimeEntryOperatorIsOnIssueParentIDShouldAcceptCommaSeparatedValues(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		i1 := testfixtures.GenerateIssue(t, tdb.d, testfixtures.IssueAttrs{ParentID: 2})
		e1 := timeEntryA(tdb, i1)
		i2 := testfixtures.GenerateIssue(t, tdb.d, testfixtures.IssueAttrs{ParentID: 5})
		e2 := timeEntryA(tdb, i2)
		q := tdb.newQuery(0, KindTimeEntry, 0)
		mustFilter(t, q, "issue.parent_id", "=", "2,5")
		assertIDs(t, timeEntriesWhereA(t, tdb, q), []int64{e1, e2})
	})
}

func TestQueryTimeEntryContainsOperatorIsOnIssueParentID(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		i1 := testfixtures.GenerateIssue(t, tdb.d, testfixtures.IssueAttrs{ParentID: 2})
		e1 := timeEntryA(tdb, i1)
		i2 := testfixtures.GenerateIssue(t, tdb.d, testfixtures.IssueAttrs{ParentID: i1})
		e2 := timeEntryA(tdb, i2)
		q := tdb.newQuery(0, KindTimeEntry, 0)
		mustFilter(t, q, "issue.parent_id", "~", "2")
		assertIDs(t, timeEntriesWhereA(t, tdb, q), []int64{e1, e2})
	})
}

func testInvalidDateFilterA(t *testing.T, op, value string) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, "created_on", op, value)
		if validA(t, q) {
			t.Error("valid")
		}
	})
}

func TestQueryDateFilterShouldNotAcceptNonDateValues(t *testing.T) {
	testInvalidDateFilterA(t, "=", "a")
}
func TestQueryDateFilterShouldNotAcceptInvalidDateValues(t *testing.T) {
	testInvalidDateFilterA(t, "=", "2011-01-34")
}
func TestQueryRelativeDateFilterShouldNotAcceptNonIntegerValues(t *testing.T) {
	testInvalidDateFilterA(t, ">t-", "a")
}

// due_date は DATE 列なので境界は日付 (サーバのタイムゾーン UTC での境界時刻の日付部分) で比較する。
func testDateStatementA(t *testing.T, field, op string, values []string, sqlParts []string, args ...any) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, field, op, values...)
		for _, p := range sqlParts {
			assertStmtIncludesA(t, q, p)
		}
		_, got := stmtA(t, q)
		for _, a := range args {
			if !slices.Contains(got, a) {
				t.Errorf("args %v do not include %v", got, a)
			}
		}
		findIssueIDs(t, q)
	})
}

func TestQueryOperatorDateEquals(t *testing.T) {
	testDateStatementA(t, "due_date", "=", []string{"2011-07-10"},
		[]string{"issues.due_date > ? AND issues.due_date <= ?"}, "2011-07-09", "2011-07-10")
}

func TestQueryOperatorDateLesserThan(t *testing.T) {
	testDateStatementA(t, "due_date", "<=", []string{"2011-07-10"}, []string{"issues.due_date <= ?"}, "2011-07-10")
}

func TestQueryOperatorDateLesserThanWithTimestamp(t *testing.T) {
	testDateStatementA(t, "updated_on", "<=", []string{"2011-07-10T19:13:52"}, []string{"issues.updated_at <= ?"}, "2011-07-10T19:13:52.000000Z")
}

func TestQueryOperatorDateGreaterThan(t *testing.T) {
	testDateStatementA(t, "due_date", ">=", []string{"2011-07-10"}, []string{"issues.due_date > ?"}, "2011-07-09")
}

func TestQueryOperatorDateGreaterThanWithTimestamp(t *testing.T) {
	testDateStatementA(t, "updated_on", ">=", []string{"2011-07-10T19:13:52"}, []string{"issues.updated_at > ?"}, "2011-07-10T19:13:51.000000Z")
}

func TestQueryOperatorDateBetween(t *testing.T) {
	testDateStatementA(t, "due_date", "><", []string{"2011-06-23", "2011-07-10"},
		[]string{"issues.due_date > ? AND issues.due_date <= ?"}, "2011-06-22", "2011-07-10")
}

// testRelativeA は project 1 の due_date の相対日付フィルタ。check は各チケットの期日が満たす条件。
func testRelativeA(t *testing.T, setup func(tdb *testDB), op, value string, check func(d time.Time) bool) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		if setup != nil {
			setup(tdb)
		}
		q := tdb.newQuery(0, KindIssue, 1)
		mustFilter(t, q, "due_date", op, value)
		ids := findIssueIDs(t, q)
		if len(ids) == 0 {
			t.Fatal("empty")
		}
		for _, id := range ids {
			d := dueDateA(tdb, id)
			if d == nil || !check(*d) {
				t.Errorf("issue %d due_date %v", id, d)
			}
		}
	})
}

func set7A(days int) func(*testDB) {
	return func(tdb *testDB) { setDueDateA(tdb, 7, todayA(days)) }
}

func TestQueryOperatorInMoreThan(t *testing.T) {
	testRelativeA(t, set7A(15), ">t+", "15", func(d time.Time) bool { return !d.Before(todayA(15)) })
}

func TestQueryOperatorInLessThan(t *testing.T) {
	testRelativeA(t, nil, "<t+", "15", func(d time.Time) bool { return !d.After(todayA(15)) })
}

func TestQueryOperatorInTheNextDays(t *testing.T) {
	testRelativeA(t, nil, "><t+", "15", func(d time.Time) bool { return !d.Before(todayA(0)) && !d.After(todayA(15)) })
}

func TestQueryOperatorLessThanAgo(t *testing.T) {
	testRelativeA(t, set7A(-3), ">t-", "3", func(d time.Time) bool { return !d.Before(todayA(-3)) })
}

func TestQueryOperatorInThePastDays(t *testing.T) {
	testRelativeA(t, set7A(-3), "><t-", "3", func(d time.Time) bool { return !d.Before(todayA(-3)) && !d.After(todayA(0)) })
}

func TestQueryOperatorMoreThanAgo(t *testing.T) {
	testRelativeA(t, func(tdb *testDB) {
		set7A(-10)(tdb)
		q := tdb.newQuery(0, KindIssue, 1)
		mustFilter(tdb.t, q, "due_date", "<t-", "10")
		assertStmtIncludesA(tdb.t, q, "issues.due_date <=")
	}, "<t-", "10", func(d time.Time) bool { return !d.After(todayA(-10)) })
}

func TestQueryOperatorIn(t *testing.T) {
	testRelativeA(t, set7A(2), "t+", "2", func(d time.Time) bool { return d.Equal(todayA(2)) })
}

func TestQueryOperatorAgo(t *testing.T) {
	testRelativeA(t, set7A(-3), "t-", "3", func(d time.Time) bool { return d.Equal(todayA(-3)) })
}

func TestQueryOperatorToday(t *testing.T) {
	testRelativeA(t, nil, "t", "", func(d time.Time) bool { return d.Equal(todayA(0)) })
}

func TestQueryOperatorTomorrow(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		gen := func(days int) int64 {
			id := testfixtures.GenerateIssue(t, tdb.d, testfixtures.IssueAttrs{})
			setDueDateA(tdb, id, todayA(days))
			return id
		}
		issue := gen(1)
		others := []int64{gen(-1), gen(2)}
		q := tdb.newQuery(0, KindIssue, 1)
		mustFilter(t, q, "due_date", "nd", "")
		ids := findIssueIDs(t, q)
		if !slices.Contains(ids, issue) || slices.Contains(ids, others[0]) || slices.Contains(ids, others[1]) {
			t.Errorf("ids = %v", ids)
		}
	})
}

func testPeriodsA(t *testing.T, field string, ops []string) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		for _, op := range ops {
			q := tdb.newQuery(0, KindIssue, 0)
			mustFilter(t, q, field, op, "")
			if !validA(t, q) {
				t.Errorf("%s invalid", op)
			}
			if _, err := q.Issues(context.Background(), ListOptions{}); err != nil {
				t.Errorf("%s: %v", op, err)
			}
		}
	})
}

func TestQueryOperatorDatePeriods(t *testing.T) {
	testPeriodsA(t, "due_date", []string{"t", "ld", "w", "lw", "l2w", "m", "lm", "y", "nd", "nw", "nm"})
}

func TestQueryOperatorDatetimePeriods(t *testing.T) {
	testPeriodsA(t, "created_on", []string{"t", "ld", "w", "lw", "l2w", "m", "lm", "y"})
}

func subjectsA(tdb *testDB, ids []int64) []string {
	if len(ids) == 0 {
		return nil
	}
	var out []string
	if err := tdb.d.Select(context.Background(), &out, `SELECT subject FROM issues WHERE id IN (`+idList(ids)+`)`); err != nil {
		tdb.t.Fatal(err)
	}
	return out
}

func TestQueryOperatorContains(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		issue := testfixtures.GenerateIssue(t, tdb.d, testfixtures.IssueAttrs{Subject: "AbCdEfG"})
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, "subject", "~", "cdeF")
		ids := findIssueIDs(t, q)
		if !slices.Contains(ids, issue) {
			t.Errorf("ids = %v", ids)
		}
		for _, s := range subjectsA(tdb, ids) {
			if !strings.Contains(strings.ToLower(s), "cdef") {
				t.Errorf("subject %q", s)
			}
		}
	})
}

func TestQueryOperatorContainsWithUtf8String(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		issue := testfixtures.GenerateIssue(t, tdb.d, testfixtures.IssueAttrs{Subject: "Subject contains Kiểm"})
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, "subject", "~", "Kiểm")
		assertIDs(t, findIssueIDs(t, q), []int64{issue})
	})
}

func TestQueryOperatorDoesNotContain(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		issue := testfixtures.GenerateIssue(t, tdb.d, testfixtures.IssueAttrs{Subject: "AbCdEfG"})
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, "subject", "!~", "cdeF")
		if slices.Contains(findIssueIDs(t, q), issue) {
			t.Error("issue included")
		}
	})
}

func testReplacedFilterA(t *testing.T, user int64, field, op, value string, want []int64) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(user, KindIssue, 0)
		replaceFiltersA(q, field, op, value)
		assertIDs(t, findIssueIDs(t, q), want)
	})
}

func TestQueryOperatorContainsAnyOf(t *testing.T) {
	testReplacedFilterA(t, 1, "subject", "*~", "close block", []int64{8, 9, 10, 11, 12})
}

func TestQueryOperatorContainsAnyOfWithAnySearchableText(t *testing.T) {
	testReplacedFilterA(t, 1, "any_searchable", "*~", "recipe categories", []int64{1, 2, 3})
}

func TestQueryOperatorContainsAnyOfWithAttachment(t *testing.T) {
	testReplacedFilterA(t, 1, "attachment", "*~", "source changeset", []int64{2, 3})
}

func TestQueryOperatorContsinsAnyOfWithAttachmentDescription(t *testing.T) {
	testReplacedFilterA(t, 1, "attachment_description", "*~", "ruby issue", []int64{2, 14})
}

func statusOfA(tdb *testDB, ids []int64) [][2]any {
	var out [][2]any
	for _, id := range sorted(ids) {
		var name string
		if err := tdb.d.Get(context.Background(), &name, `SELECT s.name FROM issues i JOIN issue_statuses s ON s.id = i.status_id WHERE i.id = ?`, id); err != nil {
			tdb.t.Fatal(err)
		}
		out = append(out, [2]any{id, name})
	}
	return out
}

func TestQueryOperatorChangedFrom(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		changeStatusA(tdb, 2, 1, 1)
		changeStatusA(tdb, 8, 1, 2)
		q := tdb.newQuery(1, KindIssue, 0)
		replaceFiltersA(q, "status_id", "cf", "2", "5")
		got := statusOfA(tdb, findIssueIDs(t, q))
		want := [][2]any{{int64(2), "New"}, {int64(8), "Assigned"}}
		if !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

func TestQueryOperatorHasBeen(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		changeStatusA(tdb, 8, 1, 2)
		q := tdb.newQuery(1, KindIssue, 0)
		replaceFiltersA(q, "status_id", "ev", "5")
		got := statusOfA(tdb, findIssueIDs(t, q))
		want := [][2]any{{int64(8), "Assigned"}, {int64(11), "Closed"}, {int64(12), "Closed"}}
		if !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

func TestQueryOperatorHasNeverBeen(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		changeStatusA(tdb, 8, 1, 2)
		q := tdb.newQuery(1, KindIssue, 0)
		replaceFiltersA(q, "status_id", "!ev", "5")
		want := tdb.ints(`SELECT id FROM issues WHERE id NOT IN (8, 11, 12) ORDER BY id`)
		assertIDs(t, findIssueIDs(t, q), want)
	})
}

// testWeekRangeA は Date.today = 2011-04-29 (金) での週・月の範囲。
func testWeekRangeA(t *testing.T, lang, op, from, to string) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		env := tdb.env(0)
		env.Now = func() time.Time { return time.Date(2011, 4, 29, 12, 0, 0, 0, time.UTC) }
		env.L = i18n.Default().NewLocalizer(lang, i18n.Settings{}, nil)
		q, err := New(context.Background(), env, KindIssue, tdb.project(1))
		if err != nil {
			t.Fatal(err)
		}
		mustFilter(t, q, "due_date", op, "")
		assertStmtIncludesA(t, q, "issues.due_date > ? AND issues.due_date <= ?")
		assertArgsA(t, q, from, to)
	})
}

func TestQueryRangeForThisWeekWithWeekStartingOnMonday(t *testing.T) {
	if l := i18n.Default().NewLocalizer("fr", i18n.Settings{}, nil).L("general_first_day_of_week"); l != "1" {
		t.Fatalf("fr first day = %q", l)
	}
	testWeekRangeA(t, "fr", "w", "2011-04-24", "2011-05-01")
}

func TestQueryRangeForThisWeekWithWeekStartingOnSunday(t *testing.T) {
	if l := i18n.Default().NewLocalizer("en", i18n.Settings{}, nil).L("general_first_day_of_week"); l != "7" {
		t.Fatalf("en first day = %q", l)
	}
	testWeekRangeA(t, "en", "w", "2011-04-23", "2011-04-30")
}

func TestQueryRangeForNextWeekWithWeekStartingOnMonday(t *testing.T) {
	testWeekRangeA(t, "fr", "nw", "2011-05-01", "2011-05-08")
}

func TestQueryRangeForNextWeekWithWeekStartingOnSunday(t *testing.T) {
	testWeekRangeA(t, "en", "nw", "2011-04-30", "2011-05-07")
}

func TestQueryRangeForNextMonth(t *testing.T) {
	testWeekRangeA(t, "en", "nm", "2011-04-30", "2011-05-31")
}

func TestQueryFilterAssignedToMe(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ctx := context.Background()
		tdb.exec(`INSERT INTO group_users (group_id, user_id) VALUES (10, 2)`)
		for _, g := range []int64{10, 11} {
			mid, err := tdb.d.InsertReturningID(ctx, `INSERT INTO members (project_id, principal_id, created_at) VALUES (1, ?, ?)`, g, db.Now())
			if err != nil {
				t.Fatal(err)
			}
			tdb.exec(`INSERT INTO member_roles (member_id, role_id) VALUES (?, 1)`, mid)
		}
		tdb.setting("issue_group_assignment", "1")
		i1 := testfixtures.GenerateIssue(t, tdb.d, testfixtures.IssueAttrs{ProjectID: 1, TrackerID: 1, AssignedToID: 2})
		i2 := testfixtures.GenerateIssue(t, tdb.d, testfixtures.IssueAttrs{ProjectID: 1, TrackerID: 1, AssignedToID: 10})
		i3 := testfixtures.GenerateIssue(t, tdb.d, testfixtures.IssueAttrs{ProjectID: 1, TrackerID: 1, AssignedToID: 11})
		q := tdb.newQuery(2, KindIssue, 0)
		replaceFiltersA(q, "assigned_to_id", "=", "me")
		rows, err := q.Issues(ctx, ListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		vis, err := q.env.Auth.IssueVisibleCondition(ctx, authzOpts())
		if err != nil {
			t.Fatal(err)
		}
		want := tdb.ints(`SELECT issues.id FROM issues JOIN projects ON projects.id = issues.project_id WHERE (` + vis + `) AND issues.assigned_to_id IN (2, 10)`)
		assertIDs(t, ids, want)
		if !slices.Contains(ids, i1) || !slices.Contains(ids, i2) || slices.Contains(ids, i3) {
			t.Errorf("ids = %v", ids)
		}
	})
}

func TestQueryFilterNotes(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		user := testfixtures.GenerateUser(t, tdb.d)
		journalA(tdb, 2, user, "Notes.", false)
		journalA(tdb, 3, user, "Notes.", false)
		q := tdb.newQuery(0, KindIssue, 0)
		af, _ := q.AvailableFilters(context.Background())
		if !af.Has("notes") {
			t.Fatal("notes filter missing")
		}
		all := tdb.ints(`SELECT id FROM issues WHERE id NOT IN (1, 2, 3) ORDER BY id`)
		for op, want := range map[string][]int64{"~": {1, 2, 3}, "!~": all, "^": {2, 3}, "$": {1}} {
			replaceFiltersA(q, "notes", op, "Notes")
			got := findIssueIDs(t, q)
			if !slices.Equal(sorted(got), sorted(want)) {
				t.Errorf("%s: %v, want %v", op, sorted(got), want)
			}
		}
	})
}

func TestQueryFilterNotesShouldIgnorePrivateNotesThatAreNotVisible(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		user := testfixtures.GenerateUser(t, tdb.d)
		journalA(tdb, 2, user, "Notes.", true)
		journalA(tdb, 3, user, "Notes.", false)
		q := tdb.newQuery(0, KindIssue, 0)
		replaceFiltersA(q, "notes", "~", "Notes")
		assertIDs(t, findIssueIDs(t, q), []int64{1, 3})
	})
}

func TestQueryFilterAnySearchable(t *testing.T) {
	testReplacedFilterA(t, 1, "any_searchable", "~", "recipe", []int64{1, 2, 3})
}

func TestQueryFilterAnySearchableShouldSearchSearchableCustomFields(t *testing.T) {
	testReplacedFilterA(t, 1, "any_searchable", "~", "125", []int64{1, 3})
}

func TestQueryFilterAnySearchableWithMultipleWords(t *testing.T) {
	testReplacedFilterA(t, 1, "any_searchable", "~", "recipe categories", []int64{2})
}

func TestQueryFilterAnySearchableWithMultipleWordsNegative(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		run := func(op, v string) []int64 {
			q := tdb.newQuery(1, KindIssue, 0)
			replaceFiltersA(q, "any_searchable", op, v)
			return findIssueIDs(t, q)
		}
		ids := run("!~", "recipe categories")
		w1, w2 := run("~", "recipe"), run("~", "categories")
		var want []int64
		for _, id := range tdb.ints(`SELECT id FROM issues ORDER BY id`) {
			if !slices.Contains(w1, id) && !slices.Contains(w2, id) {
				want = append(want, id)
			}
		}
		assertIDs(t, ids, want)
	})
}

func TestQueryFilterAnySearchableNoMatches(t *testing.T) {
	testReplacedFilterA(t, 1, "any_searchable", "~", "SomethingThatDoesNotExist", nil)
}

func TestQueryFilterAnySearchableNegative(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(1, KindIssue, 0)
		replaceFiltersA(q, "any_searchable", "!~", "recipe")
		ids := findIssueIDs(t, q)
		for _, id := range []int64{1, 2, 3} {
			if slices.Contains(ids, id) {
				t.Errorf("ids = %v", ids)
			}
		}
	})
}

func TestQueryFilterAnySearchableNegativeNoMatches(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(1, KindIssue, 0)
		replaceFiltersA(q, "any_searchable", "!~", "SomethingThatDoesNotExist")
		if len(findIssueIDs(t, q)) == 0 {
			t.Error("empty")
		}
	})
}

var _ = sql.ErrNoRows
