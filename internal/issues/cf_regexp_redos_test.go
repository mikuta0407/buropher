// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package issues

import (
	"strings"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/customfield"
)

// TestValidateSingleValueRegexpTimeout は管理者が設定した指数時間の正規表現でも、
// 値の検証が照合の時間切れで打ち切られて invalid になることを確かめる（修正前は終わらない）。
func TestValidateSingleValueRegexpTimeout(t *testing.T) {
	re := `^(a+)+$`
	cf := &customfield.CustomField{FieldFormat: "string", Regexp: &re}
	start := time.Now()
	errs := validateSingleValue(cf, strings.Repeat("a", 64)+"!")
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("took %v", d)
	}
	if len(errs) != 1 || errs[0].Key != "invalid" {
		t.Fatalf("errs = %+v, want invalid", errs)
	}
	// 通常の照合は従来どおり
	if errs := validateSingleValue(cf, "aaa"); len(errs) != 0 {
		t.Fatalf("errs = %+v, want none", errs)
	}
}
