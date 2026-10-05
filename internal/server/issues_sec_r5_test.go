// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// 見えないチケット（非公開）の関連は、プロジェクトの権限があっても一覧・追加できない
// （7.0.1 #44309）。
func TestIssueRelationsInvisibleIssue(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	if _, err := d.Exec(ctx, `UPDATE issues SET is_private = ?, author_id = 1, assigned_to_id = NULL WHERE id = 2`, true); err != nil {
		t.Fatal(err)
	}
	res := apiGet(t, ts, "/issues/2/relations.json", apiCreds("dlopper"))
	if res.Status != http.StatusForbidden || strings.Contains(res.Body, "issue_to_id") {
		t.Errorf("relations of invisible issue: %d %s", res.Status, res.Body)
	}
	res = apiCall(t, ts, http.MethodPost, "/issues/2/relations.json", "",
		`{"relation":{"issue_to_id":7,"relation_type":"relates"}}`, apiCreds("dlopper"))
	if res.Status != http.StatusForbidden {
		t.Errorf("create relation on invisible issue: %d %s", res.Status, res.Body)
	}
	var n int
	if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM issue_relations WHERE issue_from_id = 2 AND issue_to_id = 7`); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("relation created on invisible issue")
	}
	// 見えるチケットは従来どおり
	res = apiGet(t, ts, "/issues/2/relations.json", apiCreds("jsmith"))
	res.expectStatus(t, http.StatusOK)
	if !strings.Contains(res.Body, `"issue_to_id":3`) {
		t.Errorf("visible relations missing: %s", res.Body)
	}
}
