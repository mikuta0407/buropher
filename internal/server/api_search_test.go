// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

// test/integration/api_test/search_test.rb の移植。

import (
	"encoding/json"
	"net/http"
	"testing"
)

// searchGenerateIssue は Issue.generate!(:subject => subject) 相当（プロジェクト 1・トラッカー Bug）で作成し id を返す。
func searchGenerateIssue(t *testing.T, f *contentFixture, subject string) int64 {
	t.Helper()
	res := apiCall(t, f.ts, http.MethodPost, "/issues.json", "application/json",
		`{"issue":{"project_id":1,"tracker_id":1,"subject":`+jsonQuote(subject)+`}}`, apiCreds("admin"))
	res.expectStatus(t, 201)
	id, _ := jsonPath(res.JSON(t), "issue.id")
	n, _ := id.(json.Number).Int64()
	return n
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestAPISearch(t *testing.T) {
	f := newContentFixture(t)
	ts := f.ts

	t.Run("GET /search.xml should return xml content", func(t *testing.T) {
		res := apiGet(t, ts, "/search.xml")
		res.expectStatus(t, 200)
		if res.ContentType() != "application/xml" {
			t.Errorf("content type %s", res.ContentType())
		}
	})

	t.Run("GET /search.json should return json content", func(t *testing.T) {
		res := apiGet(t, ts, "/search.json")
		res.expectStatus(t, 200)
		if res.ContentType() != "application/json" {
			t.Errorf("content type %s", res.ContentType())
		}
		if _, ok := res.JSON(t)["results"].([]any); !ok {
			t.Errorf("results is not an array: %s", res.Body)
		}
	})

	t.Run("GET /search.xml without query strings should return empty results", func(t *testing.T) {
		res := apiGet(t, ts, "/search.xml?q=&all_words=")
		res.expectStatus(t, 200)
		assertXMLCount(t, res.XML(t), "result", 0)
	})

	t.Run("GET /search.xml with query strings should return results", func(t *testing.T) {
		f := newContentFixture(t)
		id := cItoa(searchGenerateIssue(t, f, "searchapi"))
		res := apiGet(t, f.ts, "/search.xml?q=searchapi&all_words=")
		res.expectStatus(t, 200)
		x := res.XML(t)
		assertXMLCount(t, x, "results[type=array] result", 1)
		assertXMLText(t, x, "result id", id)
		assertXMLText(t, x, "result title", "Bug #"+id+" (New): searchapi")
		assertXMLText(t, x, "result type", "issue")
		assertXMLText(t, x, "result url", f.ts.URL+"/issues/"+id)
		assertXMLText(t, x, "result description", "")
		assertXMLCount(t, x, "result datetime", 1)
	})

	t.Run("GET /search.xml should paginate", func(t *testing.T) {
		f := newContentFixture(t)
		var ids []int64
		for range 11 {
			ids = append([]int64{searchGenerateIssue(t, f, "search_with_limited_results")}, ids...)
		}
		check := func(path string, offset, limit int, want []int64) {
			t.Helper()
			m := apiGet(t, f.ts, path).JSON(t)
			assertJSON(t, m, "total_count", "11")
			assertJSON(t, m, "offset", cItoa(int64(offset)))
			assertJSON(t, m, "limit", cItoa(int64(limit)))
			results, _ := m["results"].([]any)
			var got []int64
			for _, r := range results {
				n, _ := r.(map[string]any)["id"].(json.Number).Int64()
				got = append(got, n)
			}
			if len(got) != len(want) {
				t.Fatalf("ids %v, want %v", got, want)
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("ids %v, want %v", got, want)
				}
			}
		}
		check("/search.json?q=search_with_limited_results&limit=4", 0, 4, ids[0:4])
		check("/search.json?q=search_with_limited_results&offset=8&limit=4", 8, 4, ids[8:11])
	})

	t.Run("GET /search.xml should not quick jump to the issue with given id", func(t *testing.T) {
		apiGet(t, ts, "/search.xml?q=3").expectStatus(t, 200)
	})
}
