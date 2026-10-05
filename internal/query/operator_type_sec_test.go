// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package query

import (
	"context"
	"slices"
	"testing"
)

// TestFilterOperatorMustMatchType は、フィルタの型の演算子に含まれない演算子（tracker_id に "~" など）を
// 持つクエリが valid? にならないことを確かめる。PostgreSQL ではそのような条件（整数の列に LIKE など）が
// SQL エラーになり、公開・既定のクエリとして保存されると誰が開いても 500 になり続けた。
func TestFilterOperatorMustMatchType(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		ctx := context.Background()
		for _, kind := range []Kind{KindIssue, KindTimeEntry, KindProject, KindUser} {
			userID := int64(2)
			if kind == KindUser {
				userID = 1
			}
			base := tdb.newQuery(userID, kind, 0)
			af, err := base.AvailableFilters(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, field := range af.Keys() {
				typ := af.Get(field).Type
				for _, o := range Operators {
					for _, v := range []string{"1", "abc"} {
						q := tdb.newQuery(userID, kind, 0)
						q.Filters = NewFilters()
						if err := q.AddFilter(ctx, field, o.Op, []string{v}); err != nil {
							t.Fatal(err)
						}
						valid, err := q.Valid(ctx)
						if err != nil {
							t.Fatal(err)
						}
						if !slices.Contains(OperatorsByFilterType[typ], o.Op) && !slices.Contains(anyTypeOperators, o.Op) {
							if valid {
								t.Errorf("%s %s (%s) %q: valid, want invalid", kind, field, typ, o.Op)
							}
							continue
						}
						// 許される演算子の条件は SQL エラーにならない
						if valid {
							if _, err := q.Count(ctx); err != nil {
								t.Errorf("%s %s (%s) %q %q: %v", kind, field, typ, o.Op, v, err)
							}
						}
					}
				}
			}
		}
	})
}
