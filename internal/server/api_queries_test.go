package server_test

import (
	"net/http"
	"os"
	"testing"
)

// test/integration/api_test/queries_test.rb の移植と、GET /queries.(json|xml) の参照 Redmine との一致。
// testdata/queries/index_*.(json|xml) は共用の参照 Redmine（3998）から curl で取得した生の応答。

func TestAPIQueries(t *testing.T) {
	ts, _ := newFixtureServer(t)

	// GET /queries.xml should return queries
	t.Run("GET /queries.xml should return queries", func(t *testing.T) {
		res := apiGet(t, ts, "/queries.xml")
		res.expectStatus(t, http.StatusOK)
		if ct := res.ContentType(); ct != "application/xml" {
			t.Errorf("content type %q", ct)
		}
		doc := res.XML(t)
		found := false
		for _, n := range xmlSelect(t, doc, "queries[type=array] query id") {
			if nodeText(n) == "4" {
				found = true
				// assert_select '~ name', :text => 'Public query for all projects'
				if sib := n.NextSibling; sib == nil || nodeText(sib) != "Public query for all projects" {
					t.Error("query 4 name")
				}
			}
		}
		if !found {
			t.Error("query 4 not found")
		}
	})

	cases := []struct {
		path, name string
		opts       []apiOpt
	}{
		{"/queries.json", "index_admin.json", []apiOpt{apiCreds("admin")}},
		{"/queries.xml?limit=2&offset=1", "index_page_jsmith.xml", []apiOpt{apiCreds("jsmith")}},
		{"/queries.json?type=TimeEntryQuery", "index_time_entry_anonymous.json", nil},
		{"/queries.xml", "index_anonymous.xml", nil},
		{"/queries.json?type=UserQuery&nometa=1", "index_user_nometa_admin.json", []apiOpt{apiCreds("admin")}},
		{"/queries.json?type=ProjectQuery", "index_project_admin.json", []apiOpt{apiCreds("admin")}},
		{"/queries.json", "index_dlopper.json", []apiOpt{apiCreds("dlopper")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, err := os.ReadFile("testdata/queries/" + tc.name)
			if err != nil {
				t.Fatal(err)
			}
			res := apiGet(t, ts, tc.path, tc.opts...)
			res.expectStatus(t, http.StatusOK)
			if res.Body != string(want) {
				t.Errorf("GET %s\n got: %s\nwant: %s", tc.path, res.Body, want)
			}
		})
	}

	t.Run("unknown type", func(t *testing.T) {
		// Redmine は Query.get_subclass が nil を返し 500 になる。buropher は 404 にする。
		if res := apiGet(t, ts, "/queries.json?type=Foo"); res.Status != http.StatusNotFound {
			t.Errorf("status %d", res.Status)
		}
	})
}
