// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"strings"
	"testing"
)

// GET /issues/:id?include=children は閲覧できない子チケット（非公開など）を出さない
// （本家の render_api_issue_children は issue.children をすべて出し、件名が漏れる）。
func TestIssueAPIChildrenHidesInvisible(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	if _, err := d.Exec(ctx, `UPDATE issues SET is_private = ?, author_id = 1, assigned_to_id = NULL, parent_id = 1 WHERE id = 2`, true); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/issues/1.json?include=children", "/issues/1.xml?include=children"} {
		res := apiGet(t, ts, path, apiCreds("dlopper"))
		res.expectStatus(t, 200)
		if strings.Contains(res.Body, "Add ingredients categories") {
			t.Errorf("%s leaked private child: %s", path, res.Body)
		}
	}
	res := apiGet(t, ts, "/issues/1.json?include=children", apiCreds("jsmith"))
	res.expectStatus(t, 200)
	if !strings.Contains(res.Body, "Add ingredients categories") {
		t.Errorf("visible child missing: %s", res.Body)
	}
}
