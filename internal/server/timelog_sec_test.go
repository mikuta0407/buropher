// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"strings"
	"testing"
)

// GET /time_entries/:id は閲覧できない（visible=false でロール限定の）カスタムフィールドの値を出さない
// （7.0.1 #44146）。
func TestTimeEntryShowAPIHidesInvisibleCustomFields(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	for _, q := range []string{
		`UPDATE custom_fields SET visible = 0 WHERE id = 10`,
		`INSERT INTO custom_fields_roles (custom_field_id, role_id) VALUES (10, 2)`,
		`INSERT INTO custom_values (customized_kind, customized_id, custom_field_id, value) VALUES ('time_entry', 1, 10, 'secret-cf-value')`,
	} {
		if _, err := d.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	// jsmith は Manager（ロール 2 ではない）なので見えない
	for _, path := range []string{"/time_entries/1.json", "/time_entries/1.xml"} {
		res := apiGet(t, ts, path, apiCreds("jsmith"))
		res.expectStatus(t, 200)
		if strings.Contains(res.Body, "secret-cf-value") {
			t.Errorf("%s leaked invisible custom field: %s", path, res.Body)
		}
	}
	res := apiGet(t, ts, "/time_entries/1.json", apiCreds("admin"))
	res.expectStatus(t, 200)
	if !strings.Contains(res.Body, "secret-cf-value") {
		t.Errorf("admin should see the value: %s", res.Body)
	}
}

// 工数一覧のチケットの属性列（issue.category 等。QueryAssociationColumn）とチケットのカスタムフィールド列は、
// 工数は見えてもチケットが見えなければ空にする。
func TestTimeEntryListHidesInvisibleIssueAttributes(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	// #1（カテゴリ Printing、カスタムフィールド 2 = 125）を dlopper が見られない非公開チケットにする
	if _, err := d.Exec(ctx, `UPDATE issues SET is_private = ?, author_id = 1, assigned_to_id = NULL, category_id = 1 WHERE id = 1`, true); err != nil {
		t.Fatal(err)
	}
	u := ts.URL + "/projects/ecookbook/time_entries.csv?set_filter=1&f[]=issue_id&op[issue_id]=%3D&v[issue_id][]=1" +
		"&c[]=issue&c[]=issue.category&c[]=issue.status&c[]=issue.cf_2&c[]=hours"
	res, body := get(t, login(t, ts, "dlopper", "foo"), u)
	if res.StatusCode != 200 || !strings.Contains(body, "#1") {
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
	for _, s := range []string{"Printing", "125", "New"} {
		if strings.Contains(body, s) {
			t.Errorf("invisible issue attribute %q leaked: %s", s, body)
		}
	}
	_, body = get(t, login(t, ts, "jsmith", "jsmith"), u)
	if !strings.Contains(body, "Printing") || !strings.Contains(body, "125") {
		t.Errorf("visible issue attribute missing: %s", body)
	}
}
