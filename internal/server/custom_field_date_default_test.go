// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

// Redmine 7.0.1 (#44129) 日付カスタムフィールドの相対既定値。
// test/functional/issues_controller_test.rb の test_get_new_with_date_custom_field と
// test/integration/api_test/issues_test.rb の追加分の移植。

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
)

// insertDateCF は全プロジェクト・全トラッカーの日付書式のチケットのカスタムフィールドを作る。
func insertDateCF(t *testing.T, d *db.DB, name, mode, def string) int64 {
	t.Helper()
	mastersExec(t, d, `INSERT INTO custom_fields (owner_kind, name, field_format, is_for_all, default_value, position, format_settings)
VALUES ('issue', ?, 'date', ?, ?, 100, ?)`, name, true, def, `{"default_value_mode":"`+mode+`"}`)
	id := mastersInt(t, d, `SELECT id FROM custom_fields WHERE name = ?`, name)
	mastersExec(t, d, `INSERT INTO custom_fields_trackers (custom_field_id, tracker_id) SELECT ?, id FROM trackers`, id)
	return id
}

func TestGetNewIssueWithDateCustomFieldDefaults(t *testing.T) {
	// fixtures の現在時刻 2026-01-15 12:00 UTC は Nuku'alofa（UTC+13）で 2026-01-16 01:00
	// （本家のテストは travel_to 2026-05-24 23:00 UTC と Tokyo）
	ts, d := newFixtureServer(t)
	mastersExec(t, d, `UPDATE user_preferences SET time_zone = 'Nuku''alofa' WHERE user_id = 2`)
	if mastersInt(t, d, `SELECT COUNT(*) FROM user_preferences WHERE user_id = 2 AND time_zone = 'Nuku''alofa'`) == 0 {
		mastersExec(t, d, `INSERT INTO user_preferences (user_id, time_zone) VALUES (2, 'Nuku''alofa')`)
	}
	fixed := insertDateCF(t, d, "Fixed date default value", "fixed_date", "2026-03-21")
	offset := insertDateCF(t, d, "Date offset default value", "date_offset", "5")

	c := login(t, ts, "jsmith", "jsmith")
	res, body := get(t, c, ts.URL+"/projects/1/issues/new?tracker_id=1")
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	for id, want := range map[int64]string{fixed: "2026-03-21", offset: "2026-01-21"} {
		re := regexp.MustCompile(`<input[^>]*name="issue\[custom_field_values\]\[` + itoaTest(id) + `\]"[^>]*>`)
		tag := re.FindString(body)
		if !strings.Contains(tag, `value="`+want+`"`) {
			t.Errorf("cf %d: %s (want value %s)", id, tag, want)
		}
	}
}

func TestAPICreateIssueWithDateOffsetCustomField(t *testing.T) {
	// fixtures の現在時刻は 2026-01-15 12:00 UTC（jsmith のタイムゾーンは未設定）
	t.Run("omitted date custom field should set date offset default", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		id := insertDateCF(t, d, "Date offset", "date_offset", "5")
		res := apiCall(t, ts, http.MethodPost, "/issues.json", "application/json",
			`{"issue":{"project_id":1,"tracker_id":1,"subject":"API date default","custom_field_values":{}}}`, apiCreds("jsmith"))
		res.expectStatus(t, http.StatusCreated)
		issue := mastersInt(t, d, `SELECT MAX(id) FROM issues`)
		if v := mastersStr(t, d, `SELECT COALESCE(value, '') FROM custom_values WHERE customized_kind = 'issue' AND customized_id = ? AND custom_field_id = ?`, issue, id); v != "2026-01-20" {
			t.Errorf("value = %q", v)
		}
	})
	t.Run("date custom field set to blank should not set date offset default", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		id := insertDateCF(t, d, "Date offset", "date_offset", "5")
		res := apiCall(t, ts, http.MethodPost, "/issues.json", "application/json",
			`{"issue":{"project_id":1,"tracker_id":1,"subject":"API blank date default","custom_field_values":{"`+itoaTest(id)+`":""}}}`, apiCreds("jsmith"))
		res.expectStatus(t, http.StatusCreated)
		issue := mastersInt(t, d, `SELECT MAX(id) FROM issues`)
		if v := mastersStr(t, d, `SELECT COALESCE(MAX(value), '') FROM custom_values WHERE customized_kind = 'issue' AND customized_id = ? AND custom_field_id = ?`, issue, id); v != "" {
			t.Errorf("value = %q", v)
		}
	})
}

func TestAdminCreateDateOffsetCustomField(t *testing.T) {
	ts, d := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")
	_, body := get(t, c, ts.URL+"/custom_fields/new?type=IssueCustomField&custom_field%5Bfield_format%5D=date")
	for _, want := range []string{
		`<div data-controller="custom-field-default-value">`,
		`name="custom_field[default_value_mode]"`,
		`id="custom_field_default_value_offset"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("form lacks %s", want)
		}
	}
	// 不正な日数は not a number
	res, body := post(t, c, ts.URL+"/custom_fields", cfFormValues(
		"authenticity_token", csrfToken(t, body), "type", "IssueCustomField",
		"custom_field[field_format]", "date", "custom_field[name]", "Rel", "custom_field[default_value_mode]", "date_offset",
		"custom_field[default_value]", "1.5", "custom_field[is_for_all]", "1"))
	if res.StatusCode != 200 || !strings.Contains(body, "Default value is not a number") {
		t.Fatalf("status %d: invalid offset accepted", res.StatusCode)
	}
	res, _ = post(t, c, ts.URL+"/custom_fields", cfFormValues(
		"authenticity_token", csrfToken(t, body), "type", "IssueCustomField",
		"custom_field[field_format]", "date", "custom_field[name]", "Rel", "custom_field[default_value_mode]", "date_offset",
		"custom_field[default_value]", " -3 ", "custom_field[is_for_all]", "1"))
	if res.StatusCode != 302 {
		t.Fatalf("create status %d", res.StatusCode)
	}
	if v := mastersStr(t, d, `SELECT default_value FROM custom_fields WHERE name = 'Rel'`); v != "-3" {
		t.Errorf("default_value = %q", v)
	}
	if v := mastersStr(t, d, `SELECT format_settings FROM custom_fields WHERE name = 'Rel'`); !strings.Contains(v, `"default_value_mode":"date_offset"`) {
		t.Errorf("format_settings = %s", v)
	}
}

func cfFormValues(kv ...string) url.Values {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Add(kv[i], kv[i+1])
	}
	return v
}
