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
