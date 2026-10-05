// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package settings

import (
	"context"
	"testing"
)

// test/unit/setting_test.rb の test_default_issue_due_date_offset_should_validate_values（Redmine 7.0 #31518）。
func TestDefaultIssueDueDateOffsetValidation(t *testing.T) {
	s := newTest(t, nil)
	ctx := context.Background()
	for _, v := range []string{"", "0", "5", "+5"} {
		if _, errs, err := s.SetAllFromParams(ctx, map[string]any{"default_issue_due_date_offset": v}, nil); err != nil || len(errs) > 0 {
			t.Errorf("%q: errs=%v err=%v", v, errs, err)
		}
	}
	for v, key := range map[string]string{
		"foo": "activerecord.errors.messages.not_a_number",
		"-1":  "activerecord.errors.messages.greater_than_or_equal_to",
	} {
		_, errs, err := s.SetAllFromParams(ctx, map[string]any{"default_issue_due_date_offset": v}, nil)
		if err != nil || len(errs) != 1 || errs[0].Name != "default_issue_due_date_offset" || errs[0].Key != key {
			t.Errorf("%q: errs=%v err=%v", v, errs, err)
		}
	}
}

func TestDefaultIssueDueDateOffsetInDays(t *testing.T) {
	for v, want := range map[string]int{`""`: -1, `"0"`: 0, `" 5 "`: 5, `"+3"`: 3, `"1_0"`: 10, `"-1"`: -1, `"x"`: -1} {
		s := newTest(t, map[string]string{"default_issue_due_date_offset": v})
		got, ok := s.DefaultIssueDueDateOffsetInDays()
		if !ok {
			got = -1
		}
		if got != want {
			t.Errorf("%s: got %d, want %d", v, got, want)
		}
	}
	if d := newTest(t, nil); d.String("default_issue_start_date_to_creation_date") != "0" ||
		d.String("assignee_dropdown_display_format") != "users_then_groups" {
		t.Error("Redmine 7.0 defaults")
	}
}
