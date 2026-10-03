// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package query

import (
	"net/url"
	"testing"
)

func FuzzParseParams(f *testing.F) {
	for _, s := range []string{
		"set_filter=1&f[]=status_id&op[status_id]=o&v[status_id][]=1&c[]=subject&sort=id:desc,subject&group_by=tracker&t[]=estimated_hours",
		"status_id=*&assigned_to_id=me&created_on=><2026-01-01|2026-02-01&query[sort_criteria][0][]=id&per_page=x&page=-1",
		"fields[]=x&operators[x]=~&values[x][]=y&query_id=abc&draw_relations=1",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, qs string) {
		v, err := url.ParseQuery(qs)
		if err != nil {
			return
		}
		_ = ParseParams(v)
		_ = ParseSortCriteria(v.Get("sort"))
	})
}
