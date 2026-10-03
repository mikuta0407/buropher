// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package query

// test/unit/query_test.rb (test_sort_criteria_should_have_only_first_three_elements 以降、末尾まで) の移植。
//
// 移植しなかったテスト:
//   - test_query_column_should_accept_a_symbol_as_caption / test_query_column_should_accept_a_proc_as_caption:
//     QueryColumn.new のキャプションに Symbol / Proc を渡す Ruby 固有の API (Go の Column は CaptionKey / Caption)。
//     代わりに CaptionKey の翻訳だけを確認する。
//   - test_date_clause_should_respect_user_time_zone_with_utc_default:
//     ActiveRecord.default_timezone の切り替え。buropher は常に UTC 保存なので local_default と同じ結果になり、
//     その確認は TestQueryDateClauseShouldRespectUserTimeZoneWithLocalDefault で行う (同じ期待値で両方移植)。
//   - test_assigned_to_values_should_be_sorted_by_status_and_name は User.delete_all と stub を使うため、
//     principalOptions (assigned_to_values の並べ替え部分) に同じ 20 ユーザを与えて確認する。

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/testfixtures"
)

// ---------------------------------------------------------------- 補助 (接尾辞 C)

var seqC atomic.Int64

func ctxC() context.Context { return context.Background() }

// genProjectC は Project.generate! (公開・有効、issue_tracking / time_tracking、全トラッカー)。
func genProjectC(t *testing.T, tdb *testDB, status int) int64 {
	t.Helper()
	n := seqC.Add(1)
	now := db.Now()
	id, err := tdb.d.InsertReturningID(ctxC(), `INSERT INTO projects (name, identifier, is_public, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		fmt.Sprintf("project-c%04d", n), fmt.Sprintf("project-c%04d", n), true, status, now, now)
	if err != nil {
		t.Fatal(err)
	}
	tdb.exec(`INSERT INTO project_closure (ancestor_id, descendant_id, depth) VALUES (?, ?, 0)`, id, id)
	for _, m := range []string{"issue_tracking", "time_tracking"} {
		tdb.exec(`INSERT INTO project_modules (project_id, name) VALUES (?, ?)`, id, m)
	}
	for _, tr := range []int64{1, 2, 3} {
		tdb.exec(`INSERT INTO project_trackers (project_id, tracker_id) VALUES (?, ?)`, id, tr)
	}
	return id
}

// addMemberC は User.add_to_project(principal, project, roles)。
func addMemberC(t *testing.T, tdb *testDB, principal, project int64, roles ...int64) {
	t.Helper()
	mid, err := tdb.d.InsertReturningID(ctxC(), `INSERT INTO members (project_id, principal_id, created_at) VALUES (?, ?, ?)`, project, principal, db.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range roles {
		tdb.exec(`INSERT INTO member_roles (member_id, role_id) VALUES (?, ?)`, mid, r)
	}
}

// genRoleC は Role.generate!。
func genRoleC(t *testing.T, tdb *testDB) int64 {
	t.Helper()
	id, err := tdb.d.InsertReturningID(ctxC(), `INSERT INTO roles (name, position, builtin) VALUES (?, 100, 0)`, fmt.Sprintf("Role C%d", seqC.Add(1)))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// genCFC は CustomField.generate! (IssueCustomField は全トラッカー)。
func genCFC(t *testing.T, tdb *testDB, owner, format string, position int, forAll, filter, visible bool) int64 {
	t.Helper()
	id, err := tdb.d.InsertReturningID(ctxC(), `INSERT INTO custom_fields (owner_kind, name, field_format, is_for_all, is_filter, visible, position)
VALUES (?, ?, ?, ?, ?, ?, ?)`, owner, fmt.Sprintf("Custom field C%d", seqC.Add(1)), format, forAll, filter, visible, position)
	if err != nil {
		t.Fatal(err)
	}
	if owner == "issue" {
		for _, tr := range []int64{1, 2, 3} {
			tdb.exec(`INSERT INTO custom_fields_trackers (custom_field_id, tracker_id) VALUES (?, ?)`, id, tr)
		}
	}
	return id
}

// addCVC はカスタム値を追加する (空文字列もそのまま保存する。D-17)。
func addCVC(tdb *testDB, kind string, customizedID, cfID int64, value string) {
	tdb.exec(`INSERT INTO custom_values (customized_kind, customized_id, custom_field_id, value) VALUES (?, ?, ?, ?)`, kind, customizedID, cfID, value)
}

// genTimeEntryC は TimeEntry.generate! (user 2、今日、アクティビティ 9)。
func genTimeEntryC(tdb *testDB, issueID int64, hours float64) {
	var pid int64
	if err := tdb.d.Get(ctxC(), &pid, `SELECT project_id FROM issues WHERE id = ?`, issueID); err != nil {
		tdb.t.Fatal(err)
	}
	now := db.NewTime(frozenNow)
	tdb.exec(`INSERT INTO time_entries (project_id, user_id, author_id, issue_id, hours, activity_id, spent_on, tyear, tmonth, tweek, created_at, updated_at)
VALUES (?, 2, 2, ?, ?, 9, '2026-01-15', 2026, 1, 3, ?, ?)`, pid, issueID, hours, now, now)
}

func issueIDsC(t *testing.T, q *Query) []int64 {
	t.Helper()
	ids, err := q.IssueIDs(ctxC(), ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

func issueCountC(t *testing.T, q *Query) int64 {
	t.Helper()
	n, err := q.IssueCount(ctxC())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func issuesC(t *testing.T, q *Query, o ListOptions) []*IssueRow {
	t.Helper()
	rows, err := q.Issues(ctxC(), o)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func rowIDsC(rows []*IssueRow) []int64 {
	out := make([]int64, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

func colNamesC(cols []*Column) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.Name
	}
	return out
}

func filterValuesC(t *testing.T, q *Query, field string) []Option {
	t.Helper()
	af, err := q.AvailableFilters(ctxC())
	if err != nil {
		t.Fatal(err)
	}
	d := af.Get(field)
	if d == nil {
		t.Fatalf("filter %s not available", field)
	}
	v, err := d.LoadValues(ctxC())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func optValuesC(opts []Option) []string {
	out := make([]string, len(opts))
	for i, o := range opts {
		out[i] = o.Value
	}
	return out
}

func hasFilterC(t *testing.T, q *Query, field string) bool {
	t.Helper()
	af, err := q.AvailableFilters(ctxC())
	if err != nil {
		t.Fatal(err)
	}
	return af.Has(field)
}

func eqFloatC(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }

func gk(v string) GroupKey { return GroupKey{Value: v} }

var nilKeyC = GroupKey{Null: true}

// ---------------------------------------------------------------- ソート

func TestQuerySortCriteriaShouldHaveOnlyFirstThreeElements(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		q.SetSortCriteria(SortCriteria{{"priority", "desc"}, {"tracker", "asc"}, {"priority", "asc"}, {"id", "asc"}, {"project", "asc"}, {"subject", "asc"}})
		want := SortCriteria{{"priority", "desc"}, {"tracker", "asc"}, {"id", "asc"}}
		if got := q.SortCriteria(); !slices.Equal(got, want) {
			t.Errorf("sort = %v", got)
		}
	})
}

func TestQuerySortCriteriaShouldRemoveBlankOrDuplicateKeys(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		q.SetSortCriteria(SortCriteria{{"priority", "desc"}, {"", "desc"}, {"", "asc"}, {"priority", "asc"}, {"project", "asc"}})
		want := SortCriteria{{"priority", "desc"}, {"project", "asc"}}
		if got := q.SortCriteria(); !slices.Equal(got, want) {
			t.Errorf("sort = %v", got)
		}
	})
}

func TestQuerySetSortCriteriaWithHash(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		// {'0' => ['priority', 'desc'], '2' => ['tracker']} の values
		q.SetSortCriteria(SortCriteria{{"priority", "desc"}, {"tracker", ""}})
		want := SortCriteria{{"priority", "desc"}, {"tracker", "asc"}}
		if got := q.SortCriteria(); !slices.Equal(got, want) {
			t.Errorf("sort = %v", got)
		}
	})
}

func TestQuerySetSortCriteriaWithArray(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		q.SetSortCriteria(SortCriteria{{"priority", "desc"}, {"tracker", "tracker"}})
		want := SortCriteria{{"priority", "desc"}, {"tracker", "asc"}}
		if got := q.SortCriteria(); !slices.Equal(got, want) {
			t.Errorf("sort = %v", got)
		}
	})
}

func TestQueryCreateQueryWithSort(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		q.Name = "Sorted"
		q.SetSortCriteria(SortCriteria{{"priority", "desc"}, {"tracker", "tracker"}})
		if err := q.Save(ctxC()); err != nil {
			t.Fatal(err)
		}
		r, err := Load(ctxC(), tdb.env(0), q.ID, KindIssue)
		if err != nil || r == nil {
			t.Fatal(r, err)
		}
		want := SortCriteria{{"priority", "desc"}, {"tracker", "asc"}}
		if got := r.SortCriteria(); !slices.Equal(got, want) {
			t.Errorf("sort = %v", got)
		}
	})
}

// cfValuesC は custom_value_for(cf).to_s を並び順どおりに返す。
func cfValuesC(t *testing.T, tdb *testDB, rows []*IssueRow, cfID int64) []string {
	t.Helper()
	var out []string
	for _, r := range rows {
		var vs []sql.NullString
		if err := tdb.d.Select(ctxC(), &vs, `SELECT value FROM custom_values WHERE customized_kind = 'issue' AND customized_id = ? AND custom_field_id = ? ORDER BY id`, r.ID, cfID); err != nil {
			t.Fatal(err)
		}
		s := ""
		if len(vs) > 0 {
			s = vs[0].String
		}
		out = append(out, s)
	}
	return out
}

func sortByCFColumnC(t *testing.T, tdb *testDB, format, order string) (*Column, []*IssueRow) {
	t.Helper()
	q := tdb.newQuery(0, KindIssue, 0)
	q.Name = ""
	cols, err := q.AvailableColumns(ctxC())
	if err != nil {
		t.Fatal(err)
	}
	var c *Column
	for _, col := range cols {
		if col.Kind == ColumnCustomField && col.CustomField.FieldFormat == format {
			c = col
			break
		}
	}
	if c == nil || !c.IsSortable() {
		t.Fatalf("no sortable %s cf column", format)
	}
	q.SetSortCriteria(SortCriteria{{c.Name, order}})
	return c, issuesC(t, q, ListOptions{})
}

func TestQuerySortByStringCustomFieldAsc(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		c, rows := sortByCFColumnC(t, tdb, "string", "asc")
		values := cfValuesC(t, tdb, rows, c.CustomField.ID)
		if len(values) == 0 || !slices.IsSorted(values) {
			t.Errorf("values = %q", values)
		}
	})
}

func TestQuerySortByStringCustomFieldDesc(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		c, rows := sortByCFColumnC(t, tdb, "string", "desc")
		values := cfValuesC(t, tdb, rows, c.CustomField.ID)
		rev := slices.Clone(values)
		sort.Sort(sort.Reverse(sort.StringSlice(rev)))
		if len(values) == 0 || !slices.Equal(values, rev) {
			t.Errorf("values = %q", values)
		}
	})
}

func TestQuerySortByFloatCustomFieldAsc(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		c, rows := sortByCFColumnC(t, tdb, "float", "asc")
		var values []float64
		for _, s := range cfValuesC(t, tdb, rows, c.CustomField.ID) {
			if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
				values = append(values, f)
			}
		}
		if len(values) == 0 || !slices.IsSorted(values) {
			t.Errorf("values = %v", values)
		}
	})
}

func TestQuerySortWithGroupByTimestampQueryColumnShouldSortAfterDateValue(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		// Issue#10 を最後に更新されたチケットにする
		tdb.exec(`UPDATE issues SET updated_at = ? WHERE id = 10`, db.NewTime(frozenNow.Add(-time.Minute+time.Second)))
		q := tdb.newQuery(1, KindIssue, 0)
		q.Filters = NewFilters()
		q.Filters.Set("updated_on", Filter{Operator: "t", Values: []string{""}})
		q.GroupBy = "updated_on"
		q.SetSortCriteria(SortCriteria{{"subject", "asc"}})
		if got := rowIDsC(issuesC(t, q, ListOptions{})); !slices.Equal(got, []int64{9, 10, 6}) {
			t.Errorf("ids = %v", got)
		}
	})
}

func TestQuerySortByTotalForEstimatedHours(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`UPDATE issues SET estimated_hours = 1 WHERE id = 1`)
		tdb.exec(`UPDATE issues SET estimated_hours = 2, parent_id = 1, root_id = 1, hier_path = '0000000001/0000000002/' WHERE id = 2`)
		tdb.exec(`UPDATE issues SET estimated_hours = 4, parent_id = 1, root_id = 1, hier_path = '0000000001/0000000003/', is_private = ? WHERE id = 3`, true)
		tdb.exec(`UPDATE issues SET estimated_hours = 5 WHERE id = 7`)
		build := func(uid int64) *Query {
			q := tdb.newQuery(uid, KindIssue, 0)
			q.Filters = NewFilters()
			q.Filters.Set("issue_id", Filter{Operator: "=", Values: []string{"1,7"}})
			q.SetSortCriteria(SortCriteria{{"total_estimated_hours", "asc"}})
			return q
		}
		if got := issueIDsC(t, build(1)); !slices.Equal(got, []int64{7, 1}) {
			t.Errorf("admin ids = %v, private issue was not used to calculate sort order", got)
		}
		if got := issueIDsC(t, build(0)); !slices.Equal(got, []int64{1, 7}) {
			t.Errorf("anonymous ids = %v, private issue was used to calculate sort order", got)
		}
	})
}

// ---------------------------------------------------------------- 合計列

func TestQuerySetTotalableNames(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		q.SetTotalableNames([]string{"estimated_hours", "spent_hours", ""})
		cols, err := q.TotalableColumns(ctxC())
		if err != nil {
			t.Fatal(err)
		}
		if got := colNamesC(cols); !slices.Equal(got, []string{"estimated_hours", "spent_hours"}) {
			t.Errorf("totalable = %v", got)
		}
	})
}

func TestQueryTotalableColumnsShouldDefaultToSettings(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.setting("issue_list_default_totals", []any{"estimated_hours"})
		q := tdb.newQuery(0, KindIssue, 0)
		cols, err := q.TotalableColumns(ctxC())
		if err != nil {
			t.Fatal(err)
		}
		if got := colNamesC(cols); !slices.Equal(got, []string{"estimated_hours"}) {
			t.Errorf("totalable = %v", got)
		}
	})
}

func availableTotalableNamesC(t *testing.T, q *Query) []string {
	t.Helper()
	cols, err := q.AvailableTotalableColumns(ctxC())
	if err != nil {
		t.Fatal(err)
	}
	return colNamesC(cols)
}

func TestQueryAvailableTotalableColumnsShouldIncludeEstimatedHours(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		if !slices.Contains(availableTotalableNamesC(t, tdb.newQuery(0, KindIssue, 0)), "estimated_hours") {
			t.Error("estimated_hours missing")
		}
	})
}

func TestQueryAvailableTotalableColumnsShouldIncludeEstimatedRemainingHours(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		if !slices.Contains(availableTotalableNamesC(t, tdb.newQuery(0, KindIssue, 0)), "estimated_remaining_hours") {
			t.Error("estimated_remaining_hours missing")
		}
	})
}

func TestQueryAvailableTotalableColumnsShouldIncludeSpentHours(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		if !slices.Contains(availableTotalableNamesC(t, tdb.newQuery(1, KindIssue, 0)), "spent_hours") {
			t.Error("spent_hours missing")
		}
	})
}

func TestQueryAvailableTotalableColumnsShouldIncludeIntCustomField(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		id := genCFC(t, tdb, "issue", "int", 1, true, false, true)
		if !slices.Contains(availableTotalableNamesC(t, tdb.newQuery(0, KindIssue, 0)), "cf_"+itoa(id)) {
			t.Error("int cf missing")
		}
	})
}

func TestQueryAvailableTotalableColumnsShouldIncludeFloatCustomField(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		id := genCFC(t, tdb, "issue", "float", 1, true, false, true)
		if !slices.Contains(availableTotalableNamesC(t, tdb.newQuery(0, KindIssue, 0)), "cf_"+itoa(id)) {
			t.Error("float cf missing")
		}
	})
}

func TestQueryAvailableTotalableColumnsShouldSortInPositionOrderForCustomField(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		check := func(owner string, kind Kind) {
			t.Helper()
			p3 := genCFC(t, tdb, owner, "int", 3, true, false, true)
			p4 := genCFC(t, tdb, owner, "float", 4, true, false, true)
			p1 := genCFC(t, tdb, owner, "float", 1, true, false, true)
			p2 := genCFC(t, tdb, owner, "int", 2, true, false, true)
			cols, err := tdb.newQuery(0, kind, 0).AvailableTotalableColumns(ctxC())
			if err != nil {
				t.Fatal(err)
			}
			var got []int64
			for _, c := range cols {
				if c.Kind == ColumnCustomField {
					got = append(got, c.CustomField.ID)
				}
			}
			if want := []int64{p1, p2, p3, p4}; !slices.Equal(got, want) {
				t.Errorf("%s: cf columns = %v, want %v", kind, got, want)
			}
		}
		tdb.exec(`DELETE FROM custom_fields WHERE owner_kind = 'project'`)
		check("project", KindProject)
		tdb.exec(`DELETE FROM custom_fields WHERE owner_kind = 'issue'`)
		check("issue", KindIssue)
		tdb.exec(`DELETE FROM custom_fields WHERE owner_kind IN ('project', 'issue', 'time_entry')`)
		check("time_entry", KindTimeEntry)
	})
}

// genIssuesC は Issue.delete_all の後に (estimated_hours, assigned_to, done_ratio) のチケットを作る。
func genIssuesC(t *testing.T, tdb *testDB, specs [][3]any) {
	t.Helper()
	tdb.exec(`DELETE FROM issues`)
	for _, s := range specs {
		a := testfixtures.IssueAttrs{}
		if s[1] != nil {
			a.AssignedToID = s[1].(int64)
		}
		id := testfixtures.GenerateIssue(t, tdb.d, a)
		if s[0] != nil {
			tdb.exec(`UPDATE issues SET estimated_hours = ? WHERE id = ?`, s[0], id)
		}
		if s[2] != nil {
			tdb.exec(`UPDATE issues SET done_ratio = ? WHERE id = ?`, s[2], id)
		}
	}
}

func totalForC(t *testing.T, q *Query, name string) float64 {
	t.Helper()
	v, err := q.TotalFor(ctxC(), name)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func totalByGroupC(t *testing.T, q *Query, name string, want map[GroupKey]float64) {
	t.Helper()
	got, err := q.TotalByGroupFor(ctxC(), name)
	if err != nil {
		t.Fatal(err)
	}
	ok := len(got) == len(want)
	for k, v := range want {
		if g, found := got[k]; !found || !eqFloatC(g, v) {
			ok = false
		}
	}
	if !ok {
		t.Errorf("total by group %s = %v, want %v", name, got, want)
	}
}

func TestQueryTotalForEstimatedHours(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		genIssuesC(t, tdb, [][3]any{{5.5, nil, nil}, {1.1, nil, nil}, {nil, nil, nil}})
		q := tdb.newQuery(0, KindIssue, 0)
		q.Name = ""
		if got := totalForC(t, q, "estimated_hours"); !eqFloatC(got, 6.6) {
			t.Errorf("total = %v", got)
		}
	})
}

func TestQueryTotalByGroupForEstimatedHours(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		genIssuesC(t, tdb, [][3]any{{5.5, int64(2), nil}, {1.1, int64(3), nil}, {3.5, nil, nil}})
		q := tdb.newQuery(0, KindIssue, 0)
		q.Name, q.GroupBy = "", "assigned_to"
		totalByGroupC(t, q, "estimated_hours", map[GroupKey]float64{nilKeyC: 3.5, gk("2"): 5.5, gk("3"): 1.1})
	})
}

func TestQueryTotalForEstimatedRemainingHours(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		genIssuesC(t, tdb, [][3]any{{5.5, nil, 50}, {1.1, nil, 100}, {nil, nil, nil}})
		q := tdb.newQuery(0, KindIssue, 0)
		q.Name = ""
		if got := totalForC(t, q, "estimated_remaining_hours"); !eqFloatC(got, 2.75) {
			t.Errorf("total = %v", got)
		}
	})
}

func TestQueryTotalByGroupForEstimatedRemainingHours(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		genIssuesC(t, tdb, [][3]any{{5.5, int64(2), 50}, {1.1, int64(3), 100}, {3.5, nil, 0}})
		q := tdb.newQuery(0, KindIssue, 0)
		q.Name, q.GroupBy = "", "assigned_to"
		totalByGroupC(t, q, "estimated_remaining_hours", map[GroupKey]float64{nilKeyC: 3.5, gk("2"): 2.75, gk("3"): 0})
	})
}

func TestQueryTotalForSpentHours(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`DELETE FROM time_entries`)
		genTimeEntryC(tdb, 1, 5.5)
		genTimeEntryC(tdb, 1, 1.1)
		q := tdb.newQuery(0, KindIssue, 0)
		q.Name = ""
		if got := totalForC(t, q, "spent_hours"); !eqFloatC(got, 6.6) {
			t.Errorf("total = %v", got)
		}
	})
}

func TestQueryTotalByGroupForSpentHours(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`DELETE FROM time_entries`)
		genTimeEntryC(tdb, 1, 5.5)
		genTimeEntryC(tdb, 2, 1.1)
		tdb.exec(`UPDATE issues SET assigned_to_id = 2 WHERE id = 1`)
		tdb.exec(`UPDATE issues SET assigned_to_id = 3 WHERE id = 2`)
		q := tdb.newQuery(0, KindIssue, 0)
		q.Name, q.GroupBy = "", "assigned_to"
		totalByGroupC(t, q, "spent_hours", map[GroupKey]float64{gk("2"): 5.5, gk("3"): 1.1})
	})
}

func TestQueryTotalByProjectGroupForSpentHours(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`DELETE FROM time_entries`)
		genTimeEntryC(tdb, 1, 5.5)
		genTimeEntryC(tdb, 2, 1.1)
		tdb.exec(`UPDATE issues SET assigned_to_id = 2 WHERE id = 1`)
		tdb.exec(`UPDATE issues SET assigned_to_id = 3 WHERE id = 2`)
		q := tdb.newQuery(0, KindIssue, 0)
		q.Name, q.GroupBy = "", "project"
		totalByGroupC(t, q, "spent_hours", map[GroupKey]float64{gk("1"): 6.6})
	})
}

func TestQueryTotalForIntCustomField(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		cf := genCFC(t, tdb, "issue", "int", 1, true, false, true)
		addCVC(tdb, "issue", 1, cf, "2")
		addCVC(tdb, "issue", 2, cf, "7")
		addCVC(tdb, "issue", 3, cf, "")
		q := tdb.newQuery(0, KindIssue, 0)
		q.Name = ""
		if got := totalForC(t, q, "cf_"+itoa(cf)); got != 9 {
			t.Errorf("total = %v", got)
		}
	})
}

func TestQueryTotalByGroupForIntCustomField(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		cf := genCFC(t, tdb, "issue", "int", 1, true, false, true)
		addCVC(tdb, "issue", 1, cf, "2")
		addCVC(tdb, "issue", 2, cf, "7")
		tdb.exec(`UPDATE issues SET assigned_to_id = 2 WHERE id = 1`)
		tdb.exec(`UPDATE issues SET assigned_to_id = 3 WHERE id = 2`)
		q := tdb.newQuery(0, KindIssue, 0)
		q.Name, q.GroupBy = "", "assigned_to"
		totalByGroupC(t, q, "cf_"+itoa(cf), map[GroupKey]float64{gk("2"): 2, gk("3"): 7})
	})
}

func TestQueryTotalForFloatCustomField(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		cf := genCFC(t, tdb, "issue", "float", 1, true, false, true)
		addCVC(tdb, "issue", 1, cf, "2.3")
		addCVC(tdb, "issue", 2, cf, "7")
		addCVC(tdb, "issue", 3, cf, "")
		q := tdb.newQuery(0, KindIssue, 0)
		q.Name = ""
		if got := totalForC(t, q, "cf_"+itoa(cf)); !eqFloatC(got, 9.3) {
			t.Errorf("total = %v", got)
		}
	})
}

// ---------------------------------------------------------------- 件数・グループ

func TestQueryInvalidQueryShouldRaiseQueryStatementInvalidError(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		q.Name = ""
		_, err := q.Issues(ctxC(), ListOptions{Conditions: "foo = 1"})
		var qe *QueryError
		if !errors.As(err, &qe) {
			t.Errorf("err = %v, want *QueryError", err)
		}
	})
}

func TestQueryIssueCount(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		if n := issueCountC(t, q); n != int64(len(issuesC(t, q, ListOptions{}))) {
			t.Errorf("count = %d", n)
		}
	})
}

func TestQueryIssueCountWithArchivedIssues(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		p := genProjectC(t, tdb, 9)
		testfixtures.GenerateIssue(t, tdb.d, testfixtures.IssueAttrs{ProjectID: p})
		q := tdb.newQuery(0, KindIssue, 0)
		rows := issuesC(t, q, ListOptions{})
		if n := issueCountC(t, q); n != int64(len(rows)) {
			t.Errorf("count = %d, issues = %d", n, len(rows))
		}
	})
}

func countByGroupC(t *testing.T, tdb *testDB, groupBy string) map[GroupKey]int64 {
	t.Helper()
	q := tdb.newQuery(0, KindIssue, 0)
	q.GroupBy = groupBy
	m, err := q.ResultCountByGroup(ctxC())
	if err != nil {
		t.Fatal(err)
	}
	if m == nil {
		t.Fatalf("not grouped by %s", groupBy)
	}
	return m
}

func TestQueryIssueCountByAssociationGroup(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		m := countByGroupC(t, tdb, "assigned_to")
		if _, ok := m[nilKeyC]; !ok {
			t.Errorf("no nil group: %v", m)
		}
		if _, ok := m[gk("3")]; !ok {
			t.Errorf("no user 3 group: %v", m)
		}
	})
}

func TestQueryIssueCountByListCustomFieldGroup(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		m := countByGroupC(t, tdb, "cf_1")
		if _, ok := m[nilKeyC]; !ok {
			t.Errorf("no nil group: %v", m)
		}
		if _, ok := m[gk("MySQL")]; !ok {
			t.Errorf("no MySQL group: %v", m)
		}
	})
}

func TestQueryIssueCountByDateCustomFieldGroup(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		m := countByGroupC(t, tdb, "cf_8")
		reDate := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
		hasNil, hasDate := false, false
		for k := range m {
			switch {
			case k.Null:
				hasNil = true
			case reDate.MatchString(k.Value):
				hasDate = true
			default:
				t.Errorf("unexpected key %v", k)
			}
		}
		if !hasNil || !hasDate {
			t.Errorf("groups = %v", m)
		}
	})
}

func TestQueryIssueCountWithNilGroupOnly(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`UPDATE issues SET assigned_to_id = NULL`)
		m := countByGroupC(t, tdb, "assigned_to")
		if len(m) != 1 {
			t.Fatalf("groups = %v", m)
		}
		if _, ok := m[nilKeyC]; !ok {
			t.Errorf("groups = %v", m)
		}
	})
}

func TestQueryIssueIds(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		q.SetSortCriteria(SortCriteria{{"subject", "subject"}, {"id", "id"}})
		if a, b := rowIDsC(issuesC(t, q, ListOptions{})), issueIDsC(t, q); !slices.Equal(a, b) {
			t.Errorf("issues %v, issue_ids %v", a, b)
		}
	})
}

func TestQueryLabelFor(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		l, err := tdb.newQuery(0, KindIssue, 0).LabelFor(ctxC(), "assigned_to_id")
		if err != nil || l != "Assignee" {
			t.Errorf("label = %q, %v", l, err)
		}
	})
}

func TestQueryLabelForFr(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		e := tdb.env(0)
		e.L = i18n.Default().NewLocalizer("fr", i18n.Settings{}, nil)
		q, err := New(ctxC(), e, KindIssue, nil)
		if err != nil {
			t.Fatal(err)
		}
		l, err := q.LabelFor(ctxC(), "assigned_to_id")
		if err != nil || l != "Assigné à" {
			t.Errorf("label = %q, %v", l, err)
		}
	})
}

// ---------------------------------------------------------------- 編集・可視性

func loadQueryC(t *testing.T, tdb *testDB, id int64) *Query {
	t.Helper()
	q, err := Load(ctxC(), tdb.env(0), id, KindIssue)
	if err != nil || q == nil {
		t.Fatalf("load %d: %v %v", id, q, err)
	}
	return q
}

func editableC(t *testing.T, tdb *testDB, q *Query, uid int64) bool {
	t.Helper()
	ok, err := q.EditableBy(ctxC(), tdb.env(uid).Auth)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestQueryEditableBy(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		cases := []struct {
			id                  int64
			admin, manager, dev bool
		}{
			{1, true, true, false}, // プロジェクト 1 の公開クエリ
			{2, true, false, true}, // プロジェクト 1 の非公開クエリ
			{3, true, false, true}, // 全プロジェクトの非公開クエリ
		}
		for _, c := range cases {
			q := loadQueryC(t, tdb, c.id)
			if editableC(t, tdb, q, 1) != c.admin || editableC(t, tdb, q, 2) != c.manager || editableC(t, tdb, q, 3) != c.dev {
				t.Errorf("query %d editable mismatch", c.id)
			}
		}
	})
}

func TestQueryEditableByForGlobalQuery(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := loadQueryC(t, tdb, 4)
		if !editableC(t, tdb, q, 1) || editableC(t, tdb, q, 2) || editableC(t, tdb, q, 3) {
			t.Error("editable mismatch")
		}
	})
}

func TestQueryEditableByForGlobalQueryWithProjectSet(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := loadQueryC(t, tdb, 4)
		q.SetProject(tdb.project(1))
		if !editableC(t, tdb, q, 1) || editableC(t, tdb, q, 2) || editableC(t, tdb, q, 3) {
			t.Error("editable mismatch")
		}
	})
}

func visibleQueryIDsC(t *testing.T, tdb *testDB, uid int64) []int64 {
	t.Helper()
	qs, err := ListVisible(ctxC(), tdb.env(uid).Auth, KindIssue, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, q := range qs {
		ids = append(ids, q.ID)
	}
	return ids
}

func TestQueryVisibleScope(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ids := visibleQueryIDsC(t, tdb, 0)
		if !slices.Contains(ids, 1) || !slices.Contains(ids, 4) {
			t.Errorf("public queries not visible: %v", ids)
		}
		if slices.Contains(ids, 2) || slices.Contains(ids, 3) || slices.Contains(ids, 7) {
			t.Errorf("private queries visible: %v", ids)
		}
	})
}

func createQueryC(t *testing.T, tdb *testDB, visibility int, roles []int64, user *int64) *Query {
	t.Helper()
	q := tdb.newQuery(0, KindIssue, 0)
	q.Name, q.Visibility, q.RoleIDs, q.UserID = "Query", visibility, roles, user
	if err := q.Save(ctxC()); err != nil {
		t.Fatal(err)
	}
	return q
}

// checkVisibleC は visible?(user) と IssueQuery.visible(user).find_by_id の両方を確認する。
func checkVisibleC(t *testing.T, tdb *testDB, q *Query, uid int64, wantVisible, wantScope bool) {
	t.Helper()
	ok, err := q.VisibleTo(ctxC(), tdb.env(uid).Auth)
	if err != nil {
		t.Fatal(err)
	}
	if ok != wantVisible {
		t.Errorf("user %d: visible? = %v", uid, ok)
	}
	if got := slices.Contains(visibleQueryIDsC(t, tdb, uid), q.ID); got != wantScope {
		t.Errorf("user %d: in visible scope = %v", uid, got)
	}
}

func TestQueryQueryWithPublicVisibilityShouldBeVisibleToAnyone(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := createQueryC(t, tdb, VisibilityPublic, nil, nil)
		for _, u := range []int64{0, 7, 2, 1} {
			checkVisibleC(t, tdb, q, u, true, true)
		}
	})
}

func TestQueryQueryWithRolesVisibilityShouldBeVisibleToUserWithRole(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := createQueryC(t, tdb, VisibilityRoles, []int64{1, 2}, nil)
		checkVisibleC(t, tdb, q, 0, false, false)
		checkVisibleC(t, tdb, q, 7, false, false)
		checkVisibleC(t, tdb, q, 2, true, true)
		checkVisibleC(t, tdb, q, 1, true, true)
		// アーカイブされたプロジェクトのメンバーシップは無視する
		if _, err := repository.ArchiveProject(ctxC(), tdb.d, 1); err != nil {
			t.Fatal(err)
		}
		checkVisibleC(t, tdb, q, 3, false, false)
	})
}

func TestQueryQueryWithPrivateVisibilityShouldBeVisibleToOwner(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		u := int64(7)
		q := createQueryC(t, tdb, VisibilityPrivate, nil, &u)
		checkVisibleC(t, tdb, q, 0, false, false)
		checkVisibleC(t, tdb, q, 7, true, true)
		checkVisibleC(t, tdb, q, 2, false, false)
		checkVisibleC(t, tdb, q, 1, true, false)
	})
}

func TestQueryBuildFromParamsShouldNotUpdateQueryWithNilParamValues(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		u := int64(7)
		q := tdb.newQuery(0, KindIssue, 0)
		q.Name, q.UserID = "Query", &u
		q.Filters = NewFilters()
		q.Filters.Set("status_id", Filter{Operator: "o", Values: []string{"1"}})
		if err := q.SetColumnNames(ctxC(), []string{"tracker", "status"}); err != nil {
			t.Fatal(err)
		}
		q.SetSortCriteria(SortCriteria{{"id", "id"}, {"asc", "asc"}})
		q.GroupBy = "project"
		q.SetTotalableNames([]string{"estimated_hours"})
		q.Options["draw_relations"], q.Options["draw_progress_line"] = "1", "1"
		if err := q.Save(ctxC()); err != nil {
			t.Fatal(err)
		}
		q = loadQueryC(t, tdb, q.ID)
		snap := func() string {
			return fmt.Sprint(q.Filters.Keys(), q.ValuesFor("status_id"), q.OperatorFor("status_id"), q.ColumnNames(),
				q.SortCriteria(), q.GroupBy, q.TotalableNames())
		}
		before := snap()
		if err := q.BuildFromParams(ctxC(), Params{}, nil); err != nil {
			t.Fatal(err)
		}
		if after := snap(); after != before {
			t.Errorf("attributes changed: %s -> %s", before, after)
		}
	})
}

// ---------------------------------------------------------------- フィルタの選択肢

func TestQueryAvailableFiltersShouldIncludeUsersOfVisibleProjectsInCrossProjectView(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		if v := optValuesC(filterValuesC(t, tdb.newQuery(0, KindIssue, 0), "assigned_to_id")); !slices.Contains(v, "3") {
			t.Errorf("values = %v", v)
		}
	})
}

func TestQueryAvailableFiltersShouldIncludeUsersOfSubprojects(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		user1 := testfixtures.GenerateUser(t, tdb.d)
		user2 := testfixtures.GenerateUser(t, tdb.d)
		// project.children.visible.first (匿名ユーザから見える最初の子 = プロジェクト 3)
		addMemberC(t, tdb, user1, 3, 1)
		v := optValuesC(filterValuesC(t, tdb.newQuery(0, KindIssue, 1), "assigned_to_id"))
		if !slices.Contains(v, itoa(user1)) || slices.Contains(v, itoa(user2)) {
			t.Errorf("values = %v (user1 %d, user2 %d)", v, user1, user2)
		}
	})
}

func TestQueryAvailableFiltersShouldIncludeVisibleProjectsInCrossProjectView(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		if v := optValuesC(filterValuesC(t, tdb.newQuery(0, KindIssue, 0), "project_id")); !slices.Contains(v, "1") {
			t.Errorf("values = %v", v)
		}
	})
}

func groupFilterC(t *testing.T, tdb *testDB, field, wantType string) {
	t.Helper()
	q := tdb.newQuery(0, KindIssue, 0)
	af, err := q.AvailableFilters(ctxC())
	if err != nil {
		t.Fatal(err)
	}
	d := af.Get(field)
	if d == nil || d.Type != wantType {
		t.Fatalf("filter %s = %+v", field, d)
	}
	got := filterValuesC(t, q, field)
	want := []Option{{Label: "A Team", Value: "10"}, {Label: "B Team", Value: "11"}}
	sort.Slice(got, func(i, j int) bool { return got[i].Label < got[j].Label })
	if !slices.Equal(got, want) {
		t.Errorf("values = %v", got)
	}
}

func roleFilterC(t *testing.T, tdb *testDB, field, wantType string) {
	t.Helper()
	q := tdb.newQuery(0, KindIssue, 0)
	af, err := q.AvailableFilters(ctxC())
	if err != nil {
		t.Fatal(err)
	}
	if d := af.Get(field); d == nil || d.Type != wantType {
		t.Fatalf("filter %s = %+v", field, d)
	}
	v := filterValuesC(t, q, field)
	for _, o := range []Option{{Label: "Manager", Value: "1"}, {Label: "Developer", Value: "2"}, {Label: "Reporter", Value: "3"}} {
		if !slices.Contains(v, o) {
			t.Errorf("%v missing in %v", o, v)
		}
	}
	for _, o := range []Option{{Label: "Non member", Value: "4"}, {Label: "Anonymous", Value: "5"}} {
		if slices.Contains(v, o) {
			t.Errorf("%v present in %v", o, v)
		}
	}
}

func TestQueryAvailableFiltersShouldIncludeMemberOfGroupFilter(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) { groupFilterC(t, tdb, "member_of_group", "list_optional") })
}

func TestQueryAvailableFiltersShouldIncludeAssignedToRoleFilter(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) { roleFilterC(t, tdb, "assigned_to_role", "list_optional") })
}

func TestQueryAvailableFiltersShouldIncludeAuthorGroupFilter(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) { groupFilterC(t, tdb, "author.group", "list") })
}

func TestQueryAvailableFiltersShouldIncludeAuthorRoleFilter(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) { roleFilterC(t, tdb, "author.role", "list") })
}

func visibilityCFsC(t *testing.T, tdb *testDB) (visible, hidden int64) {
	t.Helper()
	visible = genCFC(t, tdb, "issue", "string", 1, true, true, true)
	hidden = genCFC(t, tdb, "issue", "string", 1, true, true, false)
	tdb.exec(`INSERT INTO custom_fields_roles (custom_field_id, role_id) VALUES (?, 1)`, hidden)
	return
}

func TestQueryAvailableFiltersShouldIncludeCustomFieldAccordingToUserVisibility(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		v, h := visibilityCFsC(t, tdb)
		q := tdb.newQuery(3, KindIssue, 0)
		if !hasFilterC(t, q, "cf_"+itoa(v)) || hasFilterC(t, q, "cf_"+itoa(h)) {
			t.Error("custom field filter visibility mismatch")
		}
	})
}

func TestQueryAvailableColumnsShouldIncludeCustomFieldAccordingToUserVisibility(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		v, h := visibilityCFsC(t, tdb)
		cols, err := tdb.newQuery(3, KindIssue, 0).AvailableColumns(ctxC())
		if err != nil {
			t.Fatal(err)
		}
		names := colNamesC(cols)
		if !slices.Contains(names, "cf_"+itoa(v)) || slices.Contains(names, "cf_"+itoa(h)) {
			t.Errorf("columns = %v", names)
		}
	})
}

func TestQueryAvailableColumnsShouldNotIncludeTotalEstimatedHoursWhenTrackersDisabledEstimatedHours(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`UPDATE trackers SET disabled_core_fields = '["estimated_hours"]'`)
		names := func() []string {
			cols, err := tdb.newQuery(0, KindIssue, 0).AvailableColumns(ctxC())
			if err != nil {
				t.Fatal(err)
			}
			return colNamesC(cols)
		}
		n := names()
		if slices.Contains(n, "estimated_hours") || slices.Contains(n, "total_estimated_hours") {
			t.Errorf("columns = %v", n)
		}
		// Tracker.visible.first の core_fields を ['estimated_hours'] にする
		tdb.exec(`UPDATE trackers SET disabled_core_fields = '["assigned_to_id","category_id","fixed_version_id","parent_issue_id","start_date","due_date","done_ratio","description","priority_id"]' WHERE id = 1`)
		n = names()
		if !slices.Contains(n, "estimated_hours") || !slices.Contains(n, "total_estimated_hours") {
			t.Errorf("columns = %v", n)
		}
	})
}

// ---------------------------------------------------------------- member_of_group

type memberOfGroupC struct {
	group, group2 int64
}

func setupMemberOfGroupC(t *testing.T, tdb *testDB) memberOfGroupC {
	t.Helper()
	tdb.exec(`DELETE FROM principals WHERE kind = 'group'`)
	u1 := testfixtures.GenerateUser(t, tdb.d)
	u2 := testfixtures.GenerateUser(t, tdb.d)
	u3 := testfixtures.GenerateUser(t, tdb.d)
	testfixtures.GenerateUser(t, tdb.d)
	g := testfixtures.GenerateGroup(t, tdb.d)
	tdb.exec(`INSERT INTO group_users (group_id, user_id) VALUES (?, ?), (?, ?)`, g, u1, g, u2)
	g2 := testfixtures.GenerateGroup(t, tdb.d)
	tdb.exec(`INSERT INTO group_users (group_id, user_id) VALUES (?, ?)`, g2, u3)
	return memberOfGroupC{g, g2}
}

func TestQueryMemberOfGroupFilterShouldSearchAssignedToForUsersInTheGroup(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		s := setupMemberOfGroupC(t, tdb)
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, "member_of_group", "=", itoa(s.group))
		findIssueIDs(t, q)
	})
}

func TestQueryMemberOfGroupFilterShouldSearchNotAssignedToAnyGroupMemberNone(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		setupMemberOfGroupC(t, tdb)
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, "member_of_group", "!*", "")
		findIssueIDs(t, q)
	})
}

func TestQueryMemberOfGroupFilterShouldSearchAssignedToAnyGroupMemberAll(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		setupMemberOfGroupC(t, tdb)
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, "member_of_group", "*", "")
		findIssueIDs(t, q)
	})
}

func TestQueryMemberOfGroupFilterShouldReturnAnEmptySetWithEmptyGroup(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		setupMemberOfGroupC(t, tdb)
		empty := testfixtures.GenerateGroup(t, tdb.d)
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, "member_of_group", "=", itoa(empty))
		if ids := findIssueIDs(t, q); len(ids) != 0 {
			t.Errorf("ids = %v", ids)
		}
	})
}

func TestQueryMemberOfGroupFilterShouldReturnIssuesWithEmptyGroup(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		setupMemberOfGroupC(t, tdb)
		empty := testfixtures.GenerateGroup(t, tdb.d)
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, "member_of_group", "!", itoa(empty))
		findIssueIDs(t, q)
	})
}

// ---------------------------------------------------------------- assigned_to_role

type assignedToRoleC struct {
	project, developer                     int64
	issue1, issue2, issue3, issue4, issue5 int64
}

func setupAssignedToRoleC(t *testing.T, tdb *testDB) assignedToRoleC {
	t.Helper()
	const manager, developer = 1, 2
	s := assignedToRoleC{project: genProjectC(t, tdb, 1)}
	m := testfixtures.GenerateUser(t, tdb.d)
	s.developer = testfixtures.GenerateUser(t, tdb.d)
	boss := testfixtures.GenerateUser(t, tdb.d)
	guest := testfixtures.GenerateUser(t, tdb.d)
	addMemberC(t, tdb, m, s.project, manager)
	addMemberC(t, tdb, s.developer, s.project, developer)
	addMemberC(t, tdb, boss, s.project, manager, developer)
	gen := func(a testfixtures.IssueAttrs) int64 {
		a.ProjectID = s.project
		return testfixtures.GenerateIssue(t, tdb.d, a)
	}
	s.issue1 = gen(testfixtures.IssueAttrs{AssignedToID: m})
	s.issue2 = gen(testfixtures.IssueAttrs{AssignedToID: s.developer})
	s.issue3 = gen(testfixtures.IssueAttrs{AssignedToID: boss})
	s.issue4 = gen(testfixtures.IssueAttrs{AuthorID: guest, AssignedToID: guest})
	s.issue5 = gen(testfixtures.IssueAttrs{})
	return s
}

// assertQueryResultC は assert_query_result。
func assertQueryResultC(t *testing.T, q *Query, want ...int64) {
	t.Helper()
	assertIDs(t, rowIDsC(issuesC(t, q, ListOptions{})), want)
	if n := issueCountC(t, q); n != int64(len(want)) {
		t.Errorf("issue_count = %d, want %d", n, len(want))
	}
}

func roleQueryC(t *testing.T, tdb *testDB, s assignedToRoleC, op, value string) *Query {
	t.Helper()
	q := tdb.newQuery(0, KindIssue, s.project)
	mustFilter(t, q, "assigned_to_role", op, value)
	return q
}

func TestQueryAssignedToRoleFilterShouldSearchAssignedToForUsersWithTheRole(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		s := setupAssignedToRoleC(t, tdb)
		assertQueryResultC(t, roleQueryC(t, tdb, s, "=", "1"), s.issue1, s.issue3)
	})
}

func TestQueryAssignedToRoleFilterShouldSearchAssignedToForUsersWithTheRoleOnTheIssueProject(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		s := setupAssignedToRoleC(t, tdb)
		other := genProjectC(t, tdb, 1)
		addMemberC(t, tdb, s.developer, other, 1)
		assertQueryResultC(t, roleQueryC(t, tdb, s, "=", "1"), s.issue1, s.issue3)
	})
}

func TestQueryAssignedToRoleFilterShouldReturnAnEmptySetWithEmptyRole(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		s := setupAssignedToRoleC(t, tdb)
		r := genRoleC(t, tdb)
		assertQueryResultC(t, roleQueryC(t, tdb, s, "=", itoa(r)))
	})
}

func TestQueryAssignedToRoleFilterShouldSearchAssignedToForUsersWithoutTheRole(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		s := setupAssignedToRoleC(t, tdb)
		assertQueryResultC(t, roleQueryC(t, tdb, s, "!", "1"), s.issue2, s.issue4, s.issue5)
	})
}

func TestQueryAssignedToRoleFilterShouldSearchAssignedToForUsersNotAssignedToAnyRoleNone(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		s := setupAssignedToRoleC(t, tdb)
		assertQueryResultC(t, roleQueryC(t, tdb, s, "!*", ""), s.issue4, s.issue5)
	})
}

func TestQueryAssignedToRoleFilterShouldSearchAssignedToForUsersAssignedToAnyRoleAll(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		s := setupAssignedToRoleC(t, tdb)
		assertQueryResultC(t, roleQueryC(t, tdb, s, "*", ""), s.issue1, s.issue2, s.issue3)
	})
}

func TestQueryAssignedToRoleFilterShouldReturnIssuesWithEmptyRole(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		s := setupAssignedToRoleC(t, tdb)
		r := genRoleC(t, tdb)
		assertQueryResultC(t, roleQueryC(t, tdb, s, "!", itoa(r)), s.issue1, s.issue2, s.issue3, s.issue4, s.issue5)
	})
}

func TestQueryAuthorGroupFilterShouldReturnIssuesWithOrWithoutAuthorInGroup(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		p := genProjectC(t, tdb, 1)
		author := testfixtures.GenerateUser(t, tdb.d)
		ag := testfixtures.GenerateGroup(t, tdb.d)
		ng := testfixtures.GenerateGroup(t, tdb.d)
		tdb.exec(`INSERT INTO group_users (group_id, user_id) VALUES (?, ?)`, ag, author)
		var issues []int64
		for range 3 {
			issues = append(issues, testfixtures.GenerateIssue(t, tdb.d, testfixtures.IssueAttrs{ProjectID: p, AuthorID: author}))
		}
		q := tdb.newQuery(0, KindIssue, p)
		mustFilter(t, q, "author.group", "=", itoa(ag))
		assertQueryResultC(t, q, issues...)
		mustFilter(t, q, "author.group", "!", itoa(ag))
		assertQueryResultC(t, q)

		q = tdb.newQuery(0, KindIssue, p)
		mustFilter(t, q, "author.group", "!", itoa(ng))
		assertQueryResultC(t, q, issues...)
		mustFilter(t, q, "author.group", "=", itoa(ng))
		assertQueryResultC(t, q)
	})
}

func TestQueryAuthorRoleFilterShouldReturnIssuesWithOrWithoutAuthorInRole(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		p := genProjectC(t, tdb, 1)
		author := testfixtures.GenerateUser(t, tdb.d)
		addMemberC(t, tdb, author, p, 1)
		var issues []int64
		for range 3 {
			issues = append(issues, testfixtures.GenerateIssue(t, tdb.d, testfixtures.IssueAttrs{ProjectID: p, AuthorID: author}))
		}
		// Redmine のテストは存在しないフィルタ名 'author_role' を使う (無視され、全件になる)
		q := tdb.newQuery(0, KindIssue, p)
		q.Name = "issues generated by manager"
		if err := q.AddFilter(ctxC(), "author_role", "=", []string{"1"}); err != nil {
			t.Fatal(err)
		}
		assertQueryResultC(t, q, issues...)
		q = tdb.newQuery(0, KindIssue, p)
		q.Name = "issues does not generated by developer"
		if err := q.AddFilter(ctxC(), "author_role", "!", []string{"2"}); err != nil {
			t.Fatal(err)
		}
		assertQueryResultC(t, q, issues...)
		// 正しいフィルタ名でも同じ結果になること
		q = tdb.newQuery(0, KindIssue, p)
		mustFilter(t, q, "author.role", "=", "1")
		assertQueryResultC(t, q, issues...)
		q = tdb.newQuery(0, KindIssue, p)
		mustFilter(t, q, "author.role", "!", "2")
		assertQueryResultC(t, q, issues...)
	})
}

func TestQueryQueryColumnShouldAcceptASymbolAsCaption(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		c := newColumn("foo", ColumnPlain, colOpt{caption: "general_text_Yes"})
		if got := c.CaptionText(tdb.env(0)); got != "Yes" {
			t.Errorf("caption = %q", got)
		}
	})
}

// ---------------------------------------------------------------- 日付条件・プロジェクト条件

func TestQueryDateClauseShouldRespectUserTimeZoneWithLocalDefault(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`UPDATE user_preferences SET time_zone = 'Hawaii' WHERE user_id = 1`)
		q := tdb.newQuery(1, KindIssue, 0)
		from := dateArg{date: time.Date(2016, 3, 20, 0, 0, 0, 0, time.UTC)}
		to := dateArg{date: time.Date(2016, 3, 22, 0, 0, 0, 0, time.UTC)}
		c := q.dateClause("table", "field", from, true, to, true, false)
		if c.SQL != "table.field > ? AND table.field <= ?" {
			t.Errorf("sql = %q", c.SQL)
		}
		// ハワイの 3/20 は UTC 10:00 に始まる
		want := []any{"2016-03-20T09:59:59.999999Z", "2016-03-23T09:59:59.999999Z"}
		if !slices.Equal(c.Args, want) {
			t.Errorf("args = %v", c.Args)
		}
	})
}

func TestQueryProjectStatementWithClosedSubprojects(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`UPDATE projects SET status = 5 WHERE id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = 1 AND depth > 0)`)
		tdb.setting("display_subprojects_issues", "1")
		q := tdb.newQuery(0, KindIssue, 1)
		s, err := q.projectStatement(ctxC())
		if err != nil {
			t.Fatal(err)
		}
		if s != "projects.id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = 1)" {
			t.Errorf("statement = %q", s)
		}
	})
}

func TestQueryFilterOnSubprojects(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 1)
		if !hasFilterC(t, q, "subproject_id") {
			t.Fatal("subproject_id not available")
		}
		q.Filters = NewFilters()
		q.Filters.Set("subproject_id", Filter{Operator: "=", Values: []string{"3"}})
		if got := findIssueIDs(t, q); !slices.Equal(got, []int64{1, 2, 3, 5, 7, 8, 11, 12, 13, 14}) {
			t.Errorf("= ids = %v", got)
		}
		q = tdb.newQuery(0, KindIssue, 1)
		q.Filters = NewFilters()
		q.Filters.Set("subproject_id", Filter{Operator: "!", Values: []string{"3"}})
		if got := findIssueIDs(t, q); !slices.Equal(got, []int64{1, 2, 3, 6, 7, 8, 9, 10, 11, 12}) {
			t.Errorf("! ids = %v", got)
		}
	})
}

func TestQueryFilterUpdatedOnNoneShouldReturnIssuesWithUpdatedOnEqualWithCreatedOn(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 1)
		q.Filters = NewFilters()
		q.Filters.Set("updated_on", Filter{Operator: "!*", Values: []string{""}})
		if got := findIssueIDs(t, q); !slices.Equal(got, []int64{3, 6, 7, 8, 9, 10, 14}) {
			t.Errorf("ids = %v", got)
		}
	})
}

func TestQueryFilterUpdatedOnAnyShouldReturnIssuesWithUpdatedOnGreaterThanCreatedOn(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 1)
		q.Filters = NewFilters()
		q.Filters.Set("updated_on", Filter{Operator: "*", Values: []string{""}})
		if got := findIssueIDs(t, q); !slices.Equal(got, []int64{1, 2, 5, 11, 12, 13}) {
			t.Errorf("ids = %v", got)
		}
	})
}

func setWorkflowsC(tdb *testDB) {
	tdb.exec(`DELETE FROM workflow_transitions`)
	for _, w := range [][3]int64{{1, 1, 3}, {1, 1, 4}, {1, 2, 3}, {2, 1, 3}} {
		tdb.exec(`INSERT INTO workflow_transitions (tracker_id, role_id, old_status_id, new_status_id) VALUES (?, 1, ?, ?)`, w[0], w[1], w[2])
	}
}

func TestQueryIssueStatusesShouldReturnOnlyStatusesUsedByThatProject(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 1)
		q.Filters = NewFilters()
		q.Filters.Set("status_id", Filter{Operator: "=", Values: []string{}})
		setWorkflowsC(tdb)
		if got := optValuesC(filterValuesC(t, q, "status_id")); !slices.Equal(got, []string{"1", "2", "3", "4"}) {
			t.Errorf("values = %v", got)
		}
	})
}

func TestQueryIssueStatusesWithoutProjectShouldReturnAllStatuses(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		q.Filters = NewFilters()
		q.Filters.Set("status_id", Filter{Operator: "=", Values: []string{}})
		setWorkflowsC(tdb)
		if got := optValuesC(filterValuesC(t, q, "status_id")); !slices.Equal(got, []string{"1", "2", "3", "4", "5", "6"}) {
			t.Errorf("values = %v", got)
		}
	})
}

func TestQueryProjectStatusFilterShouldBeAvailableInGlobalQueries(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		if !hasFilterC(t, tdb.newQuery(0, KindIssue, 0), "project.status") {
			t.Error("project.status missing")
		}
	})
}

func TestQueryProjectStatusFilterShouldBeAvailableWhenProjectHasSubprojects(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		if !hasFilterC(t, tdb.newQuery(0, KindIssue, 1), "project.status") {
			t.Error("project.status missing")
		}
	})
}

func TestQueryProjectStatusFilterShouldNotBeAvailableWhenProjectIsLeaf(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		if hasFilterC(t, tdb.newQuery(0, KindIssue, 2), "project.status") {
			t.Error("project.status present")
		}
	})
}

func TestQueryProjectStatusesValuesShouldReturnOnlyActiveAndClosedStatuses(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		got := filterValuesC(t, tdb.newQuery(0, KindIssue, 0), "project.status")
		want := []Option{{Label: "active", Value: "1"}, {Label: "closed", Value: "5"}}
		if !slices.Equal(got, want) {
			t.Errorf("values = %v", got)
		}
	})
}

func TestQueryAsParamsShouldSerializeQuery(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, "subject", "!~", "asdf")
		q.GroupBy = "tracker"
		q.SetTotalableNames([]string{"estimated_hours"})
		if err := q.SetColumnNames(ctxC(), []string{"id", "subject", "estimated_hours"}); err != nil {
			t.Fatal(err)
		}
		n := tdb.newQuery(0, KindIssue, 0)
		if err := n.BuildFromParams(ctxC(), ParseParams(q.AsParams()), nil); err != nil {
			t.Fatal(err)
		}
		dump := func(x *Query) string {
			var fs []string
			for _, k := range x.Filters.Keys() {
				f, _ := x.Filters.Get(k)
				fs = append(fs, fmt.Sprint(k, f.Operator, f.Values))
			}
			return fmt.Sprint(fs, x.GroupBy, x.ColumnNames(), x.TotalableNames())
		}
		if a, b := dump(q), dump(n); a != b {
			t.Errorf("query %s, rebuilt %s", a, b)
		}
	})
}

func TestQueryIssueQueryFilterBySpentTime(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		for _, c := range []struct {
			op   string
			v    []string
			want []int64
		}{
			{"*", []string{""}, []int64{3, 1}},
			{"!*", []string{""}, []int64{13, 12, 11, 8, 7, 5, 2}},
			{">=", []string{"10"}, []int64{1}},
			{"<=", []string{"10"}, []int64{13, 12, 11, 8, 7, 5, 3, 2}},
			{"><", []string{"1", "2"}, []int64{3}},
		} {
			q.Filters = NewFilters()
			q.Filters.Set("spent_time", Filter{Operator: c.op, Values: c.v})
			if got := rowIDsC(issuesC(t, q, ListOptions{})); !slices.Equal(got, c.want) {
				t.Errorf("%s %v: ids = %v, want %v", c.op, c.v, got, c.want)
			}
		}
	})
}

func TestQueryIssuesShouldBeInTheSameOrderWhenPaginating(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		q.Name = ""
		q.SetSortCriteria(SortCriteria{{"priority", "desc"}})
		all := rowIDsC(issuesC(t, q, ListOptions{}))
		var paged []int64
		n := issueCountC(t, q)
		for i := 0; i < int(n/2)+1; i++ {
			paged = append(paged, rowIDsC(issuesC(t, q, ListOptions{Offset: i * 2, Limit: 2}))...)
		}
		if !slices.Equal(all, paged) {
			t.Errorf("all %v, paged %v", all, paged)
		}
	})
}

func TestQueryDestructionOfDefaultQueryShouldRemoveReferenceFromProject(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`UPDATE projects SET default_issue_query_id = 1 WHERE id = 1`)
		if err := loadQueryC(t, tdb, 1).Delete(ctxC()); err != nil {
			t.Fatal(err)
		}
		if p := tdb.project(1); p.DefaultIssueQueryID != nil {
			t.Errorf("default_issue_query_id = %d", *p.DefaultIssueQueryID)
		}
	})
}

func TestQueryShouldDetermineDefaultIssueQuery(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		const user = 2 // project.users.first
		tdb.exec(`UPDATE queries SET visibility = 2, user_id = ? WHERE id = 3`, user)
		def := func(uid, pid int64) int64 {
			t.Helper()
			var p = tdb.project(1)
			if pid == 0 {
				p = nil
			}
			q, err := Default(ctxC(), tdb.env(uid), KindIssue, p)
			if err != nil {
				t.Fatal(err)
			}
			if q == nil {
				return 0
			}
			return q.ID
		}
		users := []int64{0, user} // nil (= User.current = 匿名) / user / User.anonymous
		expect := func(label string, f func(u, p int64) int64) {
			t.Helper()
			for _, u := range users {
				for _, p := range []int64{0, 1} {
					if got, want := def(u, p), f(u, p); got != want {
						t.Errorf("%s: default(user %d, project %d) = %d, want %d", label, u, p, got, want)
					}
				}
			}
		}
		expect("none", func(_, _ int64) int64 { return 0 })
		tdb.setting("default_issue_query", "4")
		expect("global", func(_, _ int64) int64 { return 4 })
		tdb.setting("default_issue_query", "")
		tdb.exec(`UPDATE projects SET default_issue_query_id = 1 WHERE id = 1`)
		expect("project", func(_, p int64) int64 {
			if p == 0 {
				return 0
			}
			return 1
		})
		tdb.setting("default_issue_query", "4")
		expect("project+global", func(_, p int64) int64 {
			if p == 0 {
				return 4
			}
			return 1
		})
		tdb.exec(`UPDATE user_preferences SET default_issue_query_id = 3 WHERE user_id = ?`, user)
		for _, p := range []int64{0, 1} {
			if got := def(user, p); got != 3 {
				t.Errorf("user default (project %d) = %d, want 3", p, got)
			}
		}
	})
}

// ---------------------------------------------------------------- sql_contains

func TestQuerySqlContainsShouldEscapeValue(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		id := testfixtures.GenerateIssue(t, tdb.d, testfixtures.IssueAttrs{Subject: "Sanitize test"})
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, "subject", "~", "te%t")
		if n := issueCountC(t, q); n != 0 {
			t.Errorf("count = %d", n)
		}
		tdb.exec(`UPDATE issues SET subject = 'Sanitize te%t' WHERE id = ?`, id)
		if n := issueCountC(t, q); n != 1 {
			t.Errorf("count = %d", n)
		}
		tdb.exec(`UPDATE issues SET subject = 'Sanitize te_t' WHERE id = ?`, id)
		q = tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, "subject", "~", "te_t")
		if n := issueCountC(t, q); n != 1 {
			t.Errorf("count = %d", n)
		}
	})
}

func TestQuerySqlContainsShouldTokenize(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, "subject", "~", "issue today")
		if n := issueCountC(t, q); n != 1 {
			t.Errorf("count = %d", n)
		}
	})
}

func tokenizeAffixC(t *testing.T, tdb *testDB, op, value string, re *regexp.Regexp) {
	t.Helper()
	q := tdb.newQuery(0, KindIssue, 0)
	q.Filters = NewFilters()
	q.Filters.Set("subject", Filter{Operator: op, Values: []string{value}})
	if n := issueCountC(t, q); n != 4 {
		t.Errorf("count = %d", n)
	}
	for _, r := range issuesC(t, q, ListOptions{}) {
		if !re.MatchString(r.Subject) {
			t.Errorf("subject %q does not match", r.Subject)
		}
	}
}

func TestQuerySqlContainsShouldTokenizeForStartsWith(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tokenizeAffixC(t, tdb, "^", "issue closed", regexp.MustCompile(`(?i)^(issue|closed)`))
	})
}

func TestQuerySqlContainsShouldTokenizeForEndsWith(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tokenizeAffixC(t, tdb, "$", "version issue", regexp.MustCompile(`(?i)(version|issue)$`))
	})
}

// ---------------------------------------------------------------- 表示形式・担当者の選択肢

func TestQueryDisplayTypeShouldAcceptKnownTypes(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindProject, 0)
		q.SetDisplayType("list")
		if q.DisplayType() != "list" {
			t.Errorf("display_type = %q", q.DisplayType())
		}
	})
}

func TestQueryDisplayTypeShouldNotAcceptUnknownTypes(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindProject, 0)
		q.SetDisplayType("invalid")
		if q.DisplayType() != "board" {
			t.Errorf("display_type = %q", q.DisplayType())
		}
	})
}

func TestQueryAssignedToValuesShouldBeSortedByStatusAndName(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		var users []principalInfo
		for i := 19; i >= 0; i-- {
			s := fmt.Sprintf("%03d", i)
			status := 1
			if i%2 == 1 {
				status = 3
			}
			users = append(users, principalInfo{ID: int64(100 + i), Kind: "user", Status: status, Firstname: s, Lastname: s})
		}
		var want []string
		for _, st := range []int{0, 1} {
			for i := st; i < 20; i += 2 {
				s := fmt.Sprintf("%03d", i)
				want = append(want, s+" "+s)
			}
		}
		var got []string
		for _, o := range tdb.newQuery(0, KindIssue, 0).principalOptions(users) {
			got = append(got, o.Label)
		}
		if !slices.Equal(got, want) {
			t.Errorf("names = %v", got)
		}
	})
}
