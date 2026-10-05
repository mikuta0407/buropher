// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package query

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// sqliPayloads は SQL インジェクションを試みる値。
var sqliPayloads = []string{
	`' OR 1=1 -- ZQX`,
	`1) OR (1=1 ZQX`,
	`1 OR 1=1 ZQX`,
	`1; DROP TABLE issues; -- ZQX`,
	`%' OR 'ZQX'='ZQX`,
	`\' OR 1=1 -- ZQX`,
	`2024-01-01' OR 'ZQX'='ZQX`,
	`2024-01-01T00:00:00' OR 'ZQX'='ZQX`,
	`1e309`,
	`99999999999999999999999`,
	`-1|' OR 1=1 -- ZQX`,
	"\x00ZQX",
}

// TestQuerySQLInjection は全フィルタ・全演算子（不正な演算子を含む）に攻撃用の値を与え、
// sort / group_by / c[] / t[] にも攻撃用の名前を与えても、SQL エラーにならず
// 既定の可視範囲より多くの行が返らないことを確かめる。
func TestQuerySQLInjection(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ctx := context.Background()
		for _, kind := range []Kind{KindIssue, KindTimeEntry, KindProject, KindUser} {
			userID := int64(2)
			if kind == KindUser {
				userID = 1
			}
			base := tdb.newQuery(userID, kind, 0)
			base.Filters = NewFilters()
			all, err := base.IDs(ctx, ListOptions{})
			if err != nil {
				t.Fatalf("%s: base: %v", kind, err)
			}
			af, err := base.AvailableFilters(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, field := range af.Keys() {
				// その型の演算子と不正な演算子（型に合わない演算子の結果は Redmine と同じく SQL エラーになり得るので除く）
				ops := append([]string{"'", "= OR 1=1", "~'", ""}, OperatorsByFilterType[af.Get(field).Type]...)
				for _, op := range ops {
					{
						v := url.Values{}
						v.Set("set_filter", "1")
						v.Add("f[]", field)
						v.Set("op["+field+"]", op)
						for _, p := range sqliPayloads {
							v.Add("v["+field+"][]", p)
						}
						v.Set("sort", field+":desc,"+`id;DROP TABLE issues,(SELECT 1):asc`)
						v.Set("group_by", "id) OR (1=1")
						v.Add("c[]", "subject) FROM issues --")
						v.Add("t[]", "estimated_hours) --")
						q := tdb.newQuery(userID, kind, 0)
						if err := q.BuildFromParams(ctx, ParseParams(v), nil); err != nil {
							t.Fatalf("%s %s %q: build: %v", kind, field, op, err)
						}
						ids, err := q.IDs(ctx, ListOptions{})
						if err != nil && strings.Contains(err.Error(), "unknown query operator") {
							// 未知の演算子はエラー（Redmine の QueryError と同じ）で、SQL は組み立てない
							continue
						}
						var qe *QueryError
						if errors.As(err, &qe) && !strings.Contains(err.Error(), "ZQX") && !strings.Contains(err.Error(), "DROP") {
							// 型に合わない演算子で条件が空になる SQL エラーは Redmine と同じ（query_statement_invalid）。
							// 攻撃用の値が SQL 文に現れていないことだけを確かめる
							continue
						}
						if err != nil {
							t.Fatalf("%s %s %q: %v", kind, field, op, err)
						}
						for _, id := range ids {
							if !slices.Contains(all, id) {
								t.Fatalf("%s %s %q: id %d outside visible set", kind, field, op, id)
							}
						}
						if _, err := q.Count(ctx); err != nil {
							t.Fatalf("%s %s %q: count: %v", kind, field, op, err)
						}
					}
				}
			}
		}
		// 表が消えていないこと
		tdb.ints(`SELECT id FROM issues WHERE id = 1`)
	})
}

// TestStoredFilterKeyIsNotSQL は DB に直接入った filters のキー（Redmine からのインポート等で
// AddFilter の検査を経ないもの）が列名として SQL に連結されないことを確かめる。
func TestStoredFilterKeyIsNotSQL(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ctx := context.Background()
		base := tdb.newQuery(2, KindIssue, 0)
		base.Filters = NewFilters()
		all, err := base.IDs(ctx, ListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{
			"id IS NULL) OR 1=1 OR (issues.id",
			"id IS NULL OR 1=1",
			"subject) OR (1=1",
		} {
			q := tdb.newQuery(2, KindIssue, 0)
			q.Filters = NewFilters()
			q.Filters.Set(key, Filter{Operator: "*", Values: []string{""}})
			q.Filters.Set("status_id", Filter{Operator: "c", Values: []string{""}})
			ids, err := q.IDs(ctx, ListOptions{})
			if err != nil {
				t.Fatalf("%q: %v", key, err)
			}
			for _, id := range ids {
				if !slices.Contains(all, id) {
					t.Fatalf("%q: id %d outside visible set", key, id)
				}
			}
			stmt, _, err := q.Statement(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(stmt, "1=1") {
				t.Fatalf("%q: key reached SQL: %s", key, stmt)
			}
		}
	})
}
