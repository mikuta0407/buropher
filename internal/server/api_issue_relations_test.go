// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

// Redmine の test/integration/api_test/issue_relations_test.rb の移植。

import (
	"context"
	"net/http"
	"regexp"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
)

// issueRelationsAPILast は最後に作られた関連（issue_from_id, issue_to_id, relation_type, id）。
func issueRelationsAPILast(t *testing.T, d *db.DB) (from, to int64, typ string, id int64) {
	t.Helper()
	var r struct {
		ID   int64  `db:"id"`
		From int64  `db:"issue_from_id"`
		To   int64  `db:"issue_to_id"`
		Type string `db:"relation_type"`
	}
	if err := d.Get(context.Background(), &r, `SELECT id, issue_from_id, issue_to_id, relation_type FROM issue_relations ORDER BY id DESC LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	return r.From, r.To, r.Type, r.ID
}

func TestAPIIssueRelations(t *testing.T) {
	ro, _ := newFixtureServer(t)
	count := func(t *testing.T, d *db.DB) int64 { return issuesAPIInt(t, d, `SELECT COUNT(*) FROM issue_relations`) }

	t.Run("GET /issues/:issue_id/relations.xml should return issue relations", func(t *testing.T) {
		res := apiGet(t, ro, "/issues/9/relations.xml", apiCreds("jsmith"))
		res.expectStatus(t, 200)
		if res.ContentType() != "application/xml" {
			t.Errorf("content type %s", res.ContentType())
		}
		issuesAPIAnyText(t, res.XML(t), "relations[type=array] relation id", "1")
	})
	t.Run("POST /issues/:issue_id/relations.xml should create the relation", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		before := count(t, d)
		res := apiCall(t, ts, http.MethodPost, "/issues/2/relations.xml", "application/x-www-form-urlencoded",
			"relation[issue_to_id]=7&relation[relation_type]=relates", apiCreds("jsmith"))
		if count(t, d) != before+1 {
			t.Fatalf("relation not created: %d %s", res.Status, res.Body)
		}
		from, to, typ, id := issueRelationsAPILast(t, d)
		if from != 2 || to != 7 || typ != "relates" {
			t.Errorf("relation %d %d %s", from, to, typ)
		}
		res.expectStatus(t, http.StatusCreated)
		if res.ContentType() != "application/xml" {
			t.Errorf("content type %s", res.ContentType())
		}
		assertXMLText(t, res.XML(t), "relation id", itoaTest(id))
		if loc := res.Header.Get("Location"); loc != ts.URL+"/relations/"+itoaTest(id) {
			t.Errorf("Location %q", loc)
		}
	})
	t.Run("POST /issues/:issue_id/relations.json with numeric issue to id should create the relation", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		before := count(t, d)
		res := apiCall(t, ts, http.MethodPost, "/issues/2/relations.json", "application/json",
			`{"relation":{"issue_to_id":7,"relation_type":"relates"}}`, apiCreds("jsmith"))
		if count(t, d) != before+1 {
			t.Fatalf("relation not created: %d %s", res.Status, res.Body)
		}
		from, to, typ, id := issueRelationsAPILast(t, d)
		if from != 2 || to != 7 || typ != "relates" {
			t.Errorf("relation %d %d %s", from, to, typ)
		}
		res.expectStatus(t, http.StatusCreated)
		if res.ContentType() != "application/json" {
			t.Errorf("content type %s", res.ContentType())
		}
		assertJSON(t, res.JSON(t), "relation.id", itoaTest(id))
	})
	t.Run("POST /issues/:issue_id/relations.xml with failure should return errors", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		before := count(t, d)
		res := apiCall(t, ts, http.MethodPost, "/issues/2/relations.xml", "application/x-www-form-urlencoded",
			"relation[issue_to_id]=7&relation[relation_type]=foo", apiCreds("jsmith"))
		if count(t, d) != before {
			t.Error("relation created")
		}
		res.expectStatus(t, http.StatusUnprocessableEntity)
		re := regexp.MustCompile(`Relation type is not included in the list`)
		ok := false
		for _, e := range xmlSelect(t, res.XML(t), "errors error") {
			ok = ok || re.MatchString(nodeText(e))
		}
		if !ok {
			t.Errorf("errors: %s", res.Body)
		}
	})
	t.Run("GET /relations/:id.xml should return the relation", func(t *testing.T) {
		res := apiGet(t, ro, "/relations/2.xml", apiCreds("jsmith"))
		res.expectStatus(t, 200)
		if res.ContentType() != "application/xml" {
			t.Errorf("content type %s", res.ContentType())
		}
		assertXMLText(t, res.XML(t), "relation id", "2")
	})
	t.Run("DELETE /relations/:id.xml should delete the relation", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		before := count(t, d)
		res := apiCall(t, ts, http.MethodDelete, "/relations/2.xml", "", "", apiCreds("jsmith"))
		if count(t, d) != before-1 {
			t.Error("relation not deleted")
		}
		res.expectStatus(t, http.StatusNoContent)
		if res.Body != "" {
			t.Errorf("body %q", res.Body)
		}
		if n := issuesAPIInt(t, d, `SELECT COUNT(*) FROM issue_relations WHERE id = 2`); n != 0 {
			t.Error("relation 2 remains")
		}
	})
}
