package server_test

// Redmine の test/integration/api_test/journals_test.rb の移植。

import (
	"net/http"
	"testing"
)

func TestAPIJournals(t *testing.T) {
	for _, f := range []string{"xml", "json"} {
		t.Run("PUT /journals/:id."+f+" with valid parameters should update the journal notes", func(t *testing.T) {
			ts, d := newFixtureServer(t)
			res := apiCall(t, ts, http.MethodPut, "/journals/1."+f, "application/x-www-form-urlencoded",
				"journal[notes]=changed+notes", apiCreds("admin"))
			res.expectStatus(t, http.StatusNoContent)
			if res.Body != "" {
				t.Errorf("body %q", res.Body)
			}
			if n := issuesAPIStrings(t, d, `SELECT COALESCE(notes, '') FROM issue_journals WHERE id = 1`); len(n) != 1 || n[0] != "changed notes" {
				t.Errorf("notes %v", n)
			}
		})
		t.Run("PUT /journals/:id."+f+" without journal details should destroy journal", func(t *testing.T) {
			ts, d := newFixtureServer(t)
			if n := issuesAPIInt(t, d, `SELECT COUNT(*) FROM issue_journal_details WHERE journal_id = 5`); n != 0 {
				t.Fatalf("precondition: journal 5 has %d details", n)
			}
			before := issuesAPIInt(t, d, `SELECT COUNT(*) FROM issue_journals`)
			res := apiCall(t, ts, http.MethodPut, "/journals/5."+f, "application/x-www-form-urlencoded",
				"journal[notes]=", apiCreds("admin"))
			res.expectStatus(t, http.StatusNoContent)
			if res.Body != "" {
				t.Errorf("body %q", res.Body)
			}
			if after := issuesAPIInt(t, d, `SELECT COUNT(*) FROM issue_journals`); after != before-1 {
				t.Errorf("journals %d -> %d", before, after)
			}
			if n := issuesAPIInt(t, d, `SELECT COUNT(*) FROM issue_journals WHERE id = 5`); n != 0 {
				t.Error("journal 5 not destroyed")
			}
		})
	}
}
