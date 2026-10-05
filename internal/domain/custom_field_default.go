// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package domain

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 日付書式のカスタムフィールドの既定値モード（Redmine 7.0 #44129。format_store の default_value_mode）。
const (
	DefaultValueModeFixedDate  = "fixed_date"
	DefaultValueModeDateOffset = "date_offset"
)

// reRubyInteger は Integer(str, 10) が受け付ける 10 進整数（前後の空白・符号・数字間の 1 個の "_"）。
var reRubyInteger = regexp.MustCompile(`^[ \t\n\v\f\r]*([+-]?)(\d+(?:_\d+)*)[ \t\n\v\f\r]*$`)

// ParseRubyInteger10 は Ruby の Integer(str, 10)。解析できなければ false（ArgumentError）。
func ParseRubyInteger10(s string) (int, bool) {
	m := reRubyInteger.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1] + strings.ReplaceAll(m[2], "_", ""))
	if err != nil {
		return 0, false
	}
	return n, true
}

// CustomFieldDefaultValue は CustomField#default_value（7.0.1）。日付書式で default_value_mode が
// date_offset なら、保存値（日数）を today（User.current.today）に足した日付を返す。
// 整数として解析できなければ保存値をそのまま返す（rescue ArgumentError）。
func CustomFieldDefaultValue(fieldFormat, mode string, raw *string, today time.Time) *string {
	if raw == nil || fieldFormat != "date" || mode != DefaultValueModeDateOffset || strings.TrimSpace(*raw) == "" {
		return raw
	}
	n, ok := ParseRubyInteger10(*raw)
	if !ok {
		return raw
	}
	s := today.AddDate(0, 0, n).Format("2006-01-02")
	return &s
}
