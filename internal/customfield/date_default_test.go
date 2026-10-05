// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package customfield

// Redmine 7.0.1 の test/unit/custom_field_test.rb の追加分（#44129 日付の相対既定値）。

import (
	"testing"
	"time"
)

func TestDateDefaultValueShouldReturnDateOffsetFromToday(t *testing.T) {
	today := time.Date(2026, 3, 21, 0, 0, 0, 0, time.UTC)
	cf := field("date", withSetting("default_value_mode", "date_offset"))
	for v, want := range map[string]string{"0": "2026-03-21", "5": "2026-03-26", "-3": "2026-03-18", "+5": "2026-03-26"} {
		cf.DefaultValue = str(v)
		if got := cf.DefaultValueOn(today); got == nil || *got != want {
			t.Errorf("%s: got %v, want %s", v, got, want)
		}
	}
	// 解析できない値・空はそのまま
	cf.DefaultValue = str("invalid")
	if got := cf.DefaultValueOn(today); *got != "invalid" {
		t.Errorf("invalid: %v", *got)
	}
	cf.DefaultValue = nil
	if cf.DefaultValueOn(today) != nil {
		t.Error("nil")
	}
	// fixed_date / 他の書式は保存値
	cf = field("date", withSetting("default_value_mode", "fixed_date"))
	cf.DefaultValue = str("5")
	if got := cf.DefaultValueOn(today); *got != "5" {
		t.Errorf("fixed: %v", *got)
	}
	cf = field("string", withSetting("default_value_mode", "date_offset"))
	cf.DefaultValue = str("5")
	if got := cf.DefaultValueOn(today); *got != "5" {
		t.Errorf("string: %v", *got)
	}
}

func TestDateDefaultValueShouldBeValidatedWhenDateOffsetModeIsSelected(t *testing.T) {
	env := testEnv(t)
	cf := field("date", withSetting("default_value_mode", "date_offset"))
	for v, valid := range map[string]bool{"invalid": false, "1.5": false, "+5": true, "-3": true, "": true, " 7 ": true} {
		cf.DefaultValue = str(v)
		if got := !ValidateField(env, cf, false).Any(); got != valid {
			t.Errorf("%q: valid = %v, want %v", v, got, valid)
		}
	}
	cf.DefaultValue = str("x")
	human := func(a string) string { return HumanAttributeName(env, a) }
	if got := ValidateField(env, cf, false).FullMessages(human); len(got) != 1 || got[0] != "Default value is not a number" {
		t.Errorf("message: %v", got)
	}
}

func TestDateDefaultValueShouldBeValidatedWhenFixedDateModeIsSelected(t *testing.T) {
	env := testEnv(t)
	cf := field("date", withSetting("default_value_mode", "fixed_date"))
	cf.DefaultValue = str("invalid")
	if !ValidateField(env, cf, false).Any() {
		t.Error("invalid should be invalid")
	}
	cf.DefaultValue = str("2026-03-21")
	if ValidateField(env, cf, false).Any() {
		t.Error("date should be valid")
	}
}

func TestDateBeforeSaveNormalizesDefaultValueMode(t *testing.T) {
	env := testEnv(t)
	cf := field("date")
	cf.DefaultValue = str("2026-03-21")
	Find("date").BeforeSave(env, cf)
	if cf.DefaultValueMode() != "fixed_date" || *cf.DefaultValue != "2026-03-21" {
		t.Errorf("mode = %q default = %q", cf.DefaultValueMode(), *cf.DefaultValue)
	}
	cf = field("date", withSetting("default_value_mode", "foo"))
	Find("date").BeforeSave(env, cf)
	if cf.DefaultValueMode() != "fixed_date" {
		t.Errorf("mode = %q", cf.DefaultValueMode())
	}
	cf = field("date", withSetting("default_value_mode", "date_offset"))
	cf.DefaultValue = str("  5 ")
	Find("date").BeforeSave(env, cf)
	if cf.DefaultValueMode() != "date_offset" || *cf.DefaultValue != "5" {
		t.Errorf("mode = %q default = %q", cf.DefaultValueMode(), *cf.DefaultValue)
	}
}
