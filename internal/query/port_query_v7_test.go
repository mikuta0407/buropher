// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package query

import (
	"context"
	"slices"
	"testing"
)

// Redmine 7.0 で追加・変更されたフィルタの移植テスト (test/unit/query_test.rb, time_entry_query_test.rb)。

// test_operator_is_on_hour (#43968)
func TestQueryOperatorIsOnHour(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.exec(`UPDATE issues SET estimated_hours = 171.2 WHERE id = 2`)
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, "estimated_hours", "=", "171:12")
		assertIDs(t, findIssueIDs(t, q), []int64{2})
	})
}

// test_hour_filter_should_not_accept_non_hour_values
func TestQueryHourFilterShouldNotAcceptNonHourValues(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, "estimated_hours", "=", "invalid")
		if validA(t, q) {
			t.Error("valid")
		}
	})
}

// test_hour_filter_should_not_accept_partially_invalid_hour_values
func TestQueryHourFilterShouldNotAcceptPartiallyInvalidHourValues(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, "estimated_hours", "><", "1:00", "")
		if validA(t, q) {
			t.Error("valid")
		}
		q = tdb.newQuery(0, KindIssue, 0)
		mustFilter(t, q, "estimated_hours", "><", "1:00", "2h30")
		if !validA(t, q) {
			t.Error("invalid")
		}
	})
}

// test_operator_greater_than_a_hour
func TestQueryOperatorGreaterThanAHour(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 1)
		mustFilter(t, q, "estimated_hours", ">=", "40:30")
		assertStmtIncludesA(t, q, "issues.estimated_hours >= ?")
		assertArgsA(t, q, 40.5)
		findIssueIDs(t, q)
	})
}

// test_filter_on_spent_time の 0:45 形式部分 (#43968)
func TestQueryIssueQueryFilterBySpentTimeHourFormat(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		for _, c := range []struct {
			op   string
			v    []string
			want []int64
		}{
			{">=", []string{"10:00"}, []int64{1}},
			{"<=", []string{"10:00"}, []int64{13, 12, 11, 8, 7, 5, 3, 2}},
			{"><", []string{"1:00", "2:00"}, []int64{3}},
		} {
			q.Filters = NewFilters()
			q.Filters.Set("spent_time", Filter{Operator: c.op, Values: c.v})
			if got := rowIDsC(issuesC(t, q, ListOptions{})); !slices.Equal(got, c.want) {
				t.Errorf("%s %v: ids = %v, want %v", c.op, c.v, got, c.want)
			}
		}
	})
}

// test_hours_filter_with_float_string / test_hours_filter_with_hhmm_string (#43948)
func TestQueryTimeEntryHoursFilterWithHHMMString(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ctx := context.Background()
		ids := func(v string) []int64 {
			q := tdb.newQuery(0, KindTimeEntry, 0)
			mustFilter(t, q, "hours", ">=", v)
			if !validA(t, q) {
				t.Fatalf("%s: invalid", v)
			}
			rows, err := q.TimeEntries(ctx, ListOptions{})
			if err != nil {
				t.Fatal(err)
			}
			var out []int64
			for _, r := range rows {
				if r.Hours < 4.5 {
					t.Errorf("%s: hours %v", v, r.Hours)
				}
				out = append(out, r.ID)
			}
			slices.Sort(out)
			return out
		}
		a, b := ids("4.5"), ids("4:30")
		if len(a) == 0 || !slices.Equal(a, b) {
			t.Errorf("4.5 => %v, 4:30 => %v", a, b)
		}
	})
}

// test_operator_does_not_contain_on_text_custom_field (#38055)
func TestQueryOperatorDoesNotContainOnTextCustomField(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(0, KindIssue, 0)
		replaceFiltersA(q, "cf_2", "!~", "125")
		// cf_2 (Searchable field) の対象トラッカーは 1:Bug と 3:Support request のみ。
		// 見える 8 件のうち "125" を含む 2 件を除いた 6 件（値の無いチケットも含む）
		if got := findIssueIDs(t, q); len(got) != 6 {
			t.Errorf("ids = %v, want 6 issues", got)
		}
	})
}

// test/unit/user_query_test.rb: default_columns_names は表示形式の姓名順に従う (#4507)
func TestUserQueryDefaultColumnsNamesFollowUserFormat(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		tdb.setting("user_format", "lastname_firstname")
		want := []string{"login", "lastname", "firstname", "mail", "admin", "created_on", "last_login_on"}
		if got := tdb.newQuery(0, KindUser, 0).DefaultColumnNames(); !slices.Equal(got, want) {
			t.Errorf("lastname_firstname: %v", got)
		}
		tdb.setting("user_format", "firstname_lastname")
		want = []string{"login", "firstname", "lastname", "mail", "admin", "created_on", "last_login_on"}
		if got := tdb.newQuery(0, KindUser, 0).DefaultColumnNames(); !slices.Equal(got, want) {
			t.Errorf("firstname_lastname: %v", got)
		}
	})
}
