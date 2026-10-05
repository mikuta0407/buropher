// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

// Redmine 7.0.1 の test/integration/api_test/custom_fields_test.rb の追加分（#44129, #44152, #44153）。

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

// apiCustomFieldByID は GET /custom_fields.json の結果から id のカスタムフィールドを返す。
func apiCustomFieldByID(t *testing.T, m map[string]any, id int64) map[string]any {
	t.Helper()
	list, _ := m["custom_fields"].([]any)
	for _, e := range list {
		f, _ := e.(map[string]any)
		if fmt.Sprint(f["id"]) == fmt.Sprint(id) {
			return f
		}
	}
	t.Fatalf("custom field %d not found", id)
	return nil
}

func TestAPICustomFieldsV7(t *testing.T) {
	ts, d := newFixtureServer(t)
	// IssueCustomField.generate!(:field_format => 'date', :default_value_mode => 'date_offset', :default_value => '-3')
	mastersExec(t, d, `INSERT INTO custom_fields (owner_kind, name, field_format, default_value, position, format_settings)
VALUES ('issue', 'Date offset', 'date', '-3', 100, '{"default_value_mode":"date_offset"}')`)
	dateID := mastersInt(t, d, `SELECT id FROM custom_fields WHERE name = 'Date offset'`)
	// role で表示を制限したカスタムフィールド（Issue / TimeEntry / Project / Version / User）
	roleCF := map[string]int64{}
	for i, kind := range []string{"issue", "time_entry", "project", "version", "user"} {
		mastersExec(t, d, `INSERT INTO custom_fields (owner_kind, name, field_format, visible, position) VALUES (?, ?, 'string', ?, ?)`,
			kind, "Role CF "+kind, false, 200+i)
		id := mastersInt(t, d, `SELECT id FROM custom_fields WHERE name = ?`, "Role CF "+kind)
		mastersExec(t, d, `INSERT INTO custom_fields_roles (custom_field_id, role_id) VALUES (?, 1), (?, 2)`, id, id)
		roleCF[kind] = id
	}

	r := apiGet(t, ts, "/custom_fields.json", apiCreds("admin"))
	r.expectStatus(t, 200)
	m := r.JSON(t)

	t.Run("should include is_for_all and projects", func(t *testing.T) {
		f := apiCustomFieldByID(t, m, 2)
		if f["is_for_all"] != true {
			t.Errorf("cf 2 is_for_all = %v", f["is_for_all"])
		}
		if _, ok := f["projects"].([]any); !ok {
			t.Errorf("cf 2 projects = %v", f["projects"])
		}
	})
	t.Run("should include projects for issue custom fields limited to projects", func(t *testing.T) {
		f := apiCustomFieldByID(t, m, 9)
		if f["is_for_all"] != false {
			t.Errorf("is_for_all = %v", f["is_for_all"])
		}
		want := []any{map[string]any{"id": json.Number("1"), "name": "eCookbook"}}
		if !reflect.DeepEqual(f["projects"], want) {
			t.Errorf("projects = %v", f["projects"])
		}
	})
	t.Run("should include empty projects for issue custom fields for all projects", func(t *testing.T) {
		f := apiCustomFieldByID(t, m, 1)
		if f["is_for_all"] != true || !reflect.DeepEqual(f["projects"], []any{}) {
			t.Errorf("is_for_all = %v projects = %v", f["is_for_all"], f["projects"])
		}
	})
	t.Run("should not include projects for custom fields that are not issue custom fields", func(t *testing.T) {
		f := apiCustomFieldByID(t, m, 3)
		if _, ok := f["projects"]; ok {
			t.Errorf("projects present: %v", f["projects"])
		}
	})
	t.Run("should include roles for custom fields visible by role", func(t *testing.T) {
		for _, kind := range []string{"issue", "time_entry", "project", "version"} {
			f := apiCustomFieldByID(t, m, roleCF[kind])
			want := []any{map[string]any{"id": json.Number("1"), "name": "Manager"}, map[string]any{"id": json.Number("2"), "name": "Developer"}}
			if !reflect.DeepEqual(f["roles"], want) {
				t.Errorf("%s roles = %v", kind, f["roles"])
			}
		}
	})
	t.Run("should not include roles for custom fields that do not support role visibility", func(t *testing.T) {
		f := apiCustomFieldByID(t, m, roleCF["user"])
		if _, ok := f["roles"]; ok {
			t.Errorf("roles present: %v", f["roles"])
		}
	})
	t.Run("should include date offset default value mode", func(t *testing.T) {
		f := apiCustomFieldByID(t, m, dateID)
		if f["default_value_mode"] != "date_offset" || f["default_value"] != "-3" {
			t.Errorf("default_value_mode = %v default_value = %v", f["default_value_mode"], f["default_value"])
		}
		// 日付書式以外には default_value_mode を出さない
		if _, ok := apiCustomFieldByID(t, m, 1)["default_value_mode"]; ok {
			t.Error("default_value_mode on list field")
		}
	})
	t.Run("XML", func(t *testing.T) {
		x := apiGet(t, ts, "/custom_fields.xml", apiCreds("admin"))
		x.expectStatus(t, 200)
		doc := x.XML(t)
		assertXMLCount(t, doc, `custom_fields > custom_field:first-child > projects[type=array]`, 1)
		assertXMLText(t, doc, `custom_fields > custom_field:first-child > is_for_all`, "true")
		// 日付書式のカスタムフィールドだけに出る（fixtures の日付書式は fixed_date）
		assertXMLCount(t, doc, `custom_field > default_value_mode:contains("date_offset")`, 1)
		assertXMLCount(t, doc, `custom_field > default_value_mode`, int(mastersInt(t, d, `SELECT COUNT(*) FROM custom_fields WHERE field_format = 'date'`)))
	})
}
