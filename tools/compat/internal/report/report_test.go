// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAllowlist(t *testing.T) {
	p := filepath.Join(t.TempDir(), "allow.yml")
	src := `entries:
  - case: "smoke/settings__*"
    reason: not implemented
    ignore: true
  - case: "smoke/issues_1__*"
    reason: extra button
    lines: ['buropher-extra']
`
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	al, err := LoadAllowlist(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := al.Allowed("smoke", "settings__admin", "--- a\n+++ b\n-x\n"); !ok {
		t.Error("ignore entry should allow")
	}
	extra := "--- a\n+++ b\n@@ -1 +1 @@\n ctx\n+<a class=\"buropher-extra\">\n"
	if _, ok := al.Allowed("smoke", "issues_1__admin", extra); !ok {
		t.Error("line entry should allow")
	}
	other := extra + "-<p>gone</p>\n"
	if _, ok := al.Allowed("smoke", "issues_1__admin", other); ok {
		t.Error("unmatched line must not be allowed")
	}
	if _, ok := al.Allowed("smoke", "projects__admin", extra); ok {
		t.Error("other case must not be allowed")
	}
}

func TestAllowlistValidation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "allow.yml")
	_ = os.WriteFile(p, []byte("entries:\n  - case: x\n"), 0o644)
	if _, err := LoadAllowlist(p); err == nil {
		t.Error("missing reason should be rejected")
	}
	al, err := LoadAllowlist(filepath.Join(t.TempDir(), "missing.yml"))
	if err != nil || len(al.Entries) != 0 {
		t.Errorf("missing file should be empty list: %v", err)
	}
}

func TestReportWrite(t *testing.T) {
	r := &Report{Title: "t", Results: []Result{
		{Scenario: "s", ID: "a__admin", Status: Pass},
		{Scenario: "s", ID: "b__admin", Status: Fail, Diff: "--- a\n+++ b\n-x\n+<y>\n"},
	}}
	if !r.Failed() {
		t.Error("should be failed")
	}
	dir := t.TempDir()
	if err := r.WriteDir(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "diffs", "s", "b__admin.diff")); err != nil {
		t.Error(err)
	}
	h, _ := os.ReadFile(filepath.Join(dir, "summary.html"))
	if !strings.Contains(string(h), "+&lt;y&gt;") {
		t.Error("html diff not escaped")
	}
	if !strings.Contains(r.SummaryLine(), "pass=1 fail=1") {
		t.Error(r.SummaryLine())
	}
}
