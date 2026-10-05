// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"strings"
	"testing"
)

// TestDocumentShowHidesInvisibleCustomFields は文書の表示が非表示（visible = false）の
// カスタムフィールドの値を出さないことを確認する（render_custom_field_values は
// visible_custom_field_values だけを表示する）。
func TestDocumentShowHidesInvisibleCustomFields(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO custom_fields (id, owner_kind, name, field_format, visible, position) VALUES (901, 'document', 'Hidden doc field', 'string', 0, 1)`,
		`INSERT INTO custom_fields (id, owner_kind, name, field_format, visible, position) VALUES (902, 'document', 'Shown doc field', 'string', 1, 2)`,
		`INSERT INTO custom_values (customized_kind, customized_id, custom_field_id, value) VALUES ('document', 1, 901, 'top-secret-value')`,
		`INSERT INTO custom_values (customized_kind, customized_id, custom_field_id, value) VALUES ('document', 1, 902, 'public-value')`,
	} {
		if _, err := d.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range [][2]string{{"jsmith", "jsmith"}, {"admin", "admin"}} {
		c := login(t, ts, u[0], u[1])
		res, body := get(t, c, ts.URL+"/documents/1")
		if res.StatusCode != 200 {
			t.Fatalf("%s: status %d", u[0], res.StatusCode)
		}
		if strings.Contains(body, "top-secret-value") || strings.Contains(body, "Hidden doc field") {
			t.Errorf("%s: invisible document custom field is shown", u[0])
		}
		if !strings.Contains(body, "public-value") {
			t.Errorf("%s: visible document custom field is missing", u[0])
		}
	}
}
