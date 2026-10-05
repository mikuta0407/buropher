// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package settings

import (
	"regexp"
	"strconv"
	"strings"
)

// reRubyInteger10 は Ruby の Integer(value, 10) が受け付ける表記（符号・数字・数字間の単一 "_"）。
var reRubyInteger10 = regexp.MustCompile(`\A[+-]?\d+(?:_\d+)*\z`)

// RubyInteger10 は Ruby の Integer(value, 10)。変換できなければ ok=false（ArgumentError 相当）。
func RubyInteger10(s string) (n int, ok bool) {
	if !reRubyInteger10.MatchString(s) {
		return 0, false
	}
	v, err := strconv.Atoi(strings.ReplaceAll(s, "_", ""))
	if err != nil {
		return 0, false
	}
	return v, true
}

// DefaultIssueDueDateOffsetInDays は Setting.default_issue_due_date_offset_in_days（Redmine 7.0 #31518）。
// 未設定・不正値・負数なら ok=false。
func (s *Settings) DefaultIssueDueDateOffsetInDays() (days int, ok bool) {
	v := strings.TrimSpace(s.String("default_issue_due_date_offset"))
	if v == "" {
		return 0, false
	}
	n, ok := RubyInteger10(v)
	if !ok || n < 0 {
		return 0, false
	}
	return n, true
}

// validateDefaultIssueDueDateOffset は Setting.validate_all_from_params の default_issue_due_date_offset 部分。
func validateDefaultIssueDueDateOffset(params map[string]any) []FieldError {
	v, ok := params["default_issue_due_date_offset"]
	if !ok {
		return nil
	}
	s := strings.TrimSpace(rubyToS(v))
	if s == "" {
		return nil
	}
	n, valid := RubyInteger10(s)
	switch {
	case !valid:
		return []FieldError{{Name: "default_issue_due_date_offset", Key: "activerecord.errors.messages.not_a_number"}}
	case n < 0:
		return []FieldError{{Name: "default_issue_due_date_offset", Key: "activerecord.errors.messages.greater_than_or_equal_to", Args: []any{0}}}
	}
	return nil
}
