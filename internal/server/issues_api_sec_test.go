// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"strings"
	"testing"
)

// GET /issues/:id?include=children は閲覧できない子チケット（非公開など）を出さない（7.0.2 #44467）。
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

// チケット一覧の PDF の「親チケット」列は、見えない親チケットの件名を出さない（HTML・CSV と同じく番号だけ）。
func TestIssuesPDFParentColumnHidesInvisible(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	if _, err := d.Exec(ctx, `UPDATE issues SET is_private = ?, author_id = 1, assigned_to_id = NULL WHERE id = 2`, true); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(ctx, `UPDATE issues SET parent_id = 2 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	u := ts.URL + "/projects/ecookbook/issues.pdf?set_filter=1&f[]=issue_id&op[issue_id]=%3D&v[issue_id][]=1&c[]=subject&c[]=parent"
	text := getPDF(t, login(t, ts, "dlopper", "foo"), u, "issues.pdf").Text()
	if strings.Contains(text, "Add ingredients categories") || !strings.Contains(text, "#2") {
		t.Errorf("invisible parent subject leaked or id missing: %s", text)
	}
	text = getPDF(t, login(t, ts, "jsmith", "jsmith"), u, "issues.pdf").Text()
	if !strings.Contains(text, "Add ingredients categories") {
		t.Errorf("visible parent subject missing: %s", text)
	}
}
