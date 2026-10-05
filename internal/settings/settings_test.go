// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package settings

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
)

func newTest(t *testing.T, vals map[string]string) *Settings {
	t.Helper()
	st := &MemoryStore{Values: map[string]json.RawMessage{}}
	for k, v := range vals {
		st.Values[k] = json.RawMessage(v)
	}
	s, err := New(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDefaults(t *testing.T) {
	s := newTest(t, nil)
	if got := s.String("app_title"); got != "Buropher" {
		t.Errorf("app_title = %q", got)
	}
	// 既定値は Ruby の to_s 済みの文字列になる
	if got := s.Get("login_required"); got != "0" {
		t.Errorf("login_required = %#v", got)
	}
	if got := s.String("user_format"); got != "firstname_lastname" {
		t.Errorf("user_format = %q", got)
	}
	if s.Bool("login_required") || !s.Bool("lost_password") {
		t.Error("bool mismatch")
	}
	if got := s.Strings("password_required_char_classes"); len(got) != 0 {
		t.Errorf("char classes = %v", got)
	}
	if got := s.PerPageOptionsArray(); !slices.Equal(got, []int{25, 50, 100}) {
		t.Errorf("per_page = %v", got)
	}
	if Lookup("password_min_length").Format != "int" || !Lookup("twofa").SecurityNotifications {
		t.Error("definition flags mismatch")
	}
}

func TestStoredValues(t *testing.T) {
	s := newTest(t, map[string]string{
		"app_title":                      `"My Tracker"`,
		"password_required_char_classes": `["uppercase","digits"]`,
		"login_required":                 `1`,
		"unknown_plugin_setting":         `"x"`,
	})
	if s.String("app_title") != "My Tracker" || !s.Bool("login_required") {
		t.Error("stored values not loaded")
	}
	if got := s.Strings("password_required_char_classes"); !slices.Equal(got, []string{"uppercase", "digits"}) {
		t.Errorf("char classes = %v", got)
	}
}

func TestSetAllFromParams(t *testing.T) {
	s := newTest(t, nil)
	ctx := context.Background()
	changed, errs, err := s.SetAllFromParams(ctx, map[string]any{
		"app_title":                  "X",
		"password_min_length":        "10",
		"lost_password":              "0",
		"issue_list_default_columns": []any{"tracker", "", "status"},
	}, nil)
	if err != nil || len(errs) > 0 {
		t.Fatal(err, errs)
	}
	if !slices.Equal(changed, []string{"lost_password", "password_min_length"}) {
		t.Errorf("changed = %v", changed)
	}
	if got := s.Strings("issue_list_default_columns"); !slices.Equal(got, []string{"tracker", "status"}) {
		t.Errorf("columns = %v", got)
	}
	_, errs, _ = s.SetAllFromParams(ctx, map[string]any{"mail_from": "not an address"}, nil)
	if len(errs) != 1 || errs[0].Name != "mail_from" {
		t.Errorf("errs = %v", errs)
	}
	_, errs, _ = s.SetAllFromParams(ctx, map[string]any{
		"mail_handler_enable_regex_delimiters": "1",
		"mail_handler_body_delimiters":         "ok\n(unclosed",
	}, nil)
	if len(errs) != 1 || errs[0].Key != "activerecord.errors.messages.not_a_regexp" {
		t.Errorf("errs = %v", errs)
	}
	if err := s.Set(ctx, "password_min_length", "abc"); err == nil {
		t.Error("int validation expected")
	}
}

func TestCommitUpdateKeywords(t *testing.T) {
	v := CommitUpdateKeywordsFromParams(map[string]any{
		"keywords":   []any{"fixes, closes", "", "refs"},
		"status_id":  []any{"3", "", ""},
		"done_ratio": []any{"100", "", ""},
	})
	s := newTest(t, nil)
	if err := s.Set(context.Background(), "commit_update_keywords", v); err != nil {
		t.Fatal(err)
	}
	a := s.CommitUpdateKeywordsArray()
	if len(a) != 2 || !slices.Equal(a[0]["keywords"].([]string), []string{"fixes", "closes"}) || a[0]["status_id"] != "3" {
		t.Errorf("array = %#v", a)
	}
}

func TestRubyToI(t *testing.T) {
	for in, want := range map[string]int{"": 0, "1": 1, " 12abc": 12, "-3": -3, "abc": 0, "1_000": 1000, "0x1A": 0} {
		if got := RubyToI(in); got != want {
			t.Errorf("RubyToI(%q) = %d, want %d", in, got, want)
		}
	}
}

// Redmine 7.0.1 の settings.yml の既定値
func TestDefaults70(t *testing.T) {
	s := newTest(t, nil)
	for name, want := range map[string]string{
		"default_issue_start_date_to_creation_date": "0",
		"default_issue_due_date_offset":             "",
		"assignee_dropdown_display_format":          "users_then_groups",
		"webhooks_enabled":                          "0",
	} {
		if Lookup(name) == nil {
			t.Errorf("%s is not a known setting", name)
		}
		if got := s.String(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if !Lookup("webhooks_enabled").SecurityNotifications {
		t.Error("webhooks_enabled should trigger security notifications")
	}
	if _, ok := s.DefaultIssueDueDateOffsetInDays(); ok {
		t.Error("offset should be unset by default")
	}
}

// Setting.validate_all_from_params / default_issue_due_date_offset_from_params（Redmine 7.0）
func TestDefaultIssueDueDateOffset(t *testing.T) {
	s := newTest(t, nil)
	ctx := context.Background()
	for _, c := range []struct {
		in, key string
	}{
		{"abc", "activerecord.errors.messages.not_a_number"},
		{"1.5", "activerecord.errors.messages.not_a_number"},
		{"-1", "activerecord.errors.messages.greater_than_or_equal_to"},
	} {
		_, errs, _ := s.SetAllFromParams(ctx, map[string]any{"default_issue_due_date_offset": c.in}, nil)
		if len(errs) != 1 || errs[0].Name != "default_issue_due_date_offset" || errs[0].Key != c.key {
			t.Errorf("%q: errs = %v", c.in, errs)
		}
	}
	if _, errs, err := s.SetAllFromParams(ctx, map[string]any{"default_issue_due_date_offset": " 7 "}, nil); err != nil || len(errs) > 0 {
		t.Fatal(err, errs)
	}
	if got := s.String("default_issue_due_date_offset"); got != "7" {
		t.Errorf("stored = %q", got)
	}
	if n, ok := s.DefaultIssueDueDateOffsetInDays(); !ok || n != 7 {
		t.Errorf("offset = %d %v", n, ok)
	}
	if _, errs, _ := s.SetAllFromParams(ctx, map[string]any{"default_issue_due_date_offset": "  "}, nil); len(errs) > 0 {
		t.Errorf("blank should be accepted: %v", errs)
	}
	if _, ok := s.DefaultIssueDueDateOffsetInDays(); ok {
		t.Error("blank offset should be unset")
	}
}
