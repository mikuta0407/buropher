// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package permission

import (
	"slices"
	"testing"
)

func TestDefinitions(t *testing.T) {
	if n := len(All()); n != 79 {
		t.Fatalf("permissions = %d, want 79", n)
	}
	want := []string{"issue_tracking", "time_tracking", "news", "documents", "files", "wiki", "repository", "boards", "calendar", "gantt"}
	if got := AvailableProjectModules(); !slices.Equal(got, want) {
		t.Errorf("modules = %v, want %v", got, want)
	}
}

func TestLookups(t *testing.T) {
	p := Get("view_issues")
	if p == nil || !p.Read || p.Module != "issue_tracking" || !p.Allows("issues", "index") {
		t.Fatalf("view_issues = %+v", p)
	}
	if !Get("view_project").Public {
		t.Error("view_project should be public")
	}
	if !Get("edit_project").RequireMember() || !Get("add_project").RequireLoggedin() || Get("add_project").RequireMember() {
		t.Error("require flags mismatch")
	}
	if !IsReadAction("issues", "show") || IsReadAction("issues", "create") {
		t.Error("IsReadAction mismatch")
	}
	// Redmine の定義バグ（第2引数がハッシュとして解釈される）をそのまま再現している。
	l := Get("log_time_for_other_users")
	if l.RequireMember() || !slices.Equal(l.Actions, []string{"require/member"}) {
		t.Errorf("log_time_for_other_users = %+v", l)
	}
}

func TestModulesPermissions(t *testing.T) {
	ps := ModulesPermissions([]string{"wiki"})
	for _, p := range ps {
		if p.Module != "" && p.Module != "wiki" {
			t.Fatalf("unexpected module %q", p.Module)
		}
	}
	if !slices.ContainsFunc(ps, func(p *Permission) bool { return p.Name == "edit_wiki_pages" }) {
		t.Error("edit_wiki_pages missing")
	}
}
