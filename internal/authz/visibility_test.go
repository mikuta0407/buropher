// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package authz_test

// test/unit/issue_test.rb の test_visible_scope_* と test/unit/principal_test.rb の
// visible スコープ、test/unit/project_test.rb の override_roles / visible の移植。

import (
	"slices"
	"testing"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/testfixtures"
)

var noOpts = authz.ConditionOptions{}

// checkPublicNonPrivate は「公開プロジェクトの非公開でないチケットのみ」を確認する。
func checkPublicNonPrivate(e *env, ids []int64) {
	e.t.Helper()
	for _, is := range e.issues("") {
		if !slices.Contains(ids, is.ID) {
			continue
		}
		if !e.project(is.ProjectID).IsPublic {
			e.t.Errorf("issue %d of private project is visible", is.ID)
		}
		if is.IsPrivate {
			e.t.Errorf("private issue %d is visible", is.ID)
		}
	}
}

func TestVisibleScopeForAnonymous(t *testing.T) {
	withFixtures(t, func(e *env) {
		u := e.anonymous()
		ids := e.visibleIssues(u, noOpts, "")
		if len(ids) == 0 {
			t.Fatal("no issues")
		}
		checkPublicNonPrivate(e, ids)
		e.assertVisibilityMatch(u, ids)
	})
}

func TestVisibleScopeForAnonymousWithoutViewIssuesPermissions(t *testing.T) {
	withFixtures(t, func(e *env) {
		e.removePermission(e.builtinRoleID(domain.RoleBuiltinAnonymous), "view_issues")
		u := e.anonymous()
		ids := e.visibleIssues(u, noOpts, "")
		if len(ids) != 0 {
			t.Errorf("visible = %v", ids)
		}
		e.assertVisibilityMatch(u, ids)
	})
}

func TestVisibleScopeForAnonymousWithoutViewIssuesPermissionsAndMembership(t *testing.T) {
	withFixtures(t, func(e *env) {
		e.removePermission(e.builtinRoleID(domain.RoleBuiltinAnonymous), "view_issues")
		e.createMember(1, e.builtinGroup(domain.KindGroupAnonymous), 2)
		u := e.anonymous()
		ids := e.visibleIssues(u, noOpts, "")
		if len(ids) == 0 || !slices.Equal(e.issueProjects(ids), []int64{1}) {
			t.Errorf("visible = %v (projects %v)", ids, e.issueProjects(ids))
		}
		e.assertVisibilityMatch(u, ids)
	})
}

func TestAnonymousShouldNotSeePrivateIssues(t *testing.T) {
	for _, vis := range []string{"default", "own"} {
		t.Run(vis, func(t *testing.T) {
			withFixtures(t, func(e *env) {
				e.updateRole(e.builtinRoleID(domain.RoleBuiltinAnonymous), func(r *domain.Role) { r.IssuesVisibility = vis })
				u := e.anonymous()
				id := testfixtures.GenerateIssue(t, e.d, testfixtures.IssueAttrs{AuthorID: u.ID, IsPrivate: true})
				if slices.Contains(e.visibleIssues(u, noOpts, ""), id) {
					t.Error("private issue visible in scope")
				}
				if e.issueVisible(u, id) {
					t.Error("private issue visible?")
				}
			})
		})
	}
}

func TestVisibleScopeForNonMember(t *testing.T) {
	withFixtures(t, func(e *env) {
		u := e.user(9)
		ids := e.visibleIssues(u, noOpts, "")
		if len(ids) == 0 {
			t.Fatal("no issues")
		}
		checkPublicNonPrivate(e, ids)
		e.assertVisibilityMatch(u, ids)
	})
}

func TestVisibleScopeForNonMemberWithOwnIssuesVisibility(t *testing.T) {
	withFixtures(t, func(e *env) {
		e.updateRole(e.builtinRoleID(domain.RoleBuiltinNonMember), func(r *domain.Role) { r.IssuesVisibility = "own" })
		testfixtures.GenerateIssue(t, e.d, testfixtures.IssueAttrs{ProjectID: 1, AuthorID: 9, Subject: "Issue by non member"})
		u := e.user(9)
		ids := e.visibleIssues(u, noOpts, "")
		if len(ids) == 0 {
			t.Fatal("no issues")
		}
		for _, is := range e.issues("") {
			if slices.Contains(ids, is.ID) && is.AuthorID != 9 {
				t.Errorf("issue %d by %d visible", is.ID, is.AuthorID)
			}
		}
		e.assertVisibilityMatch(u, ids)
	})
}

func TestVisibleScopeForNonMemberWithoutViewIssuesPermissions(t *testing.T) {
	withFixtures(t, func(e *env) {
		e.removePermission(e.builtinRoleID(domain.RoleBuiltinNonMember), "view_issues")
		u := e.user(9)
		ids := e.visibleIssues(u, noOpts, "")
		if len(ids) != 0 {
			t.Errorf("visible = %v", ids)
		}
		e.assertVisibilityMatch(u, ids)
	})
}

func TestVisibleScopeForNonMemberWithoutViewIssuesPermissionsAndMembership(t *testing.T) {
	withFixtures(t, func(e *env) {
		e.removePermission(e.builtinRoleID(domain.RoleBuiltinNonMember), "view_issues")
		e.createMember(1, e.builtinGroup(domain.KindGroupNonMember), 2)
		u := e.user(9)
		ids := e.visibleIssues(u, noOpts, "")
		if len(ids) == 0 || !slices.Equal(e.issueProjects(ids), []int64{1}) {
			t.Errorf("visible = %v", ids)
		}
		e.assertVisibilityMatch(u, ids)
	})
}

func TestVisibleScopeForMember(t *testing.T) {
	withFixtures(t, func(e *env) {
		e.removePermission(e.builtinRoleID(domain.RoleBuiltinNonMember), "view_issues")
		e.createMember(3, 9, 2)
		u := e.user(9)
		ids := e.visibleIssues(u, noOpts, "")
		if len(ids) == 0 || !slices.Equal(e.issueProjects(ids), []int64{3}) {
			t.Errorf("visible = %v", ids)
		}
		for _, is := range e.issues("") {
			if slices.Contains(ids, is.ID) && is.IsPrivate {
				t.Errorf("private issue %d visible", is.ID)
			}
		}
		e.assertVisibilityMatch(u, ids)
	})
}

func TestVisibleScopeForMemberWithoutViewIssuesPermissionAndNonMemberRoleHavingThePermission(t *testing.T) {
	withFixtures(t, func(e *env) {
		e.addPermission(e.builtinRoleID(domain.RoleBuiltinNonMember), "view_issues")
		e.removePermission(1, "view_issues")
		u := e.user(2)
		if ids := e.visibleIssues(u, noOpts, "issues.project_id = 1"); len(ids) != 0 {
			t.Errorf("visible = %v", ids)
		}
		first := e.issues("project_id = 1")[0]
		if e.issueVisible(u, first.ID) {
			t.Error("first issue of project 1 visible")
		}
	})
}

func TestVisibleScopeWithCustomNonMemberRole(t *testing.T) {
	t.Run("restricted permission", func(t *testing.T) {
		withFixtures(t, func(e *env) {
			role := e.generateRole("view_project")
			uid := testfixtures.GenerateUser(t, e.d)
			e.createMember(1, e.builtinGroup(domain.KindGroupNonMember), role)
			ids := e.visibleIssues(e.user(uid), noOpts, "")
			if len(ids) == 0 || slices.Contains(e.issueProjects(ids), 1) {
				t.Errorf("visible = %v", ids)
			}
		})
	})
	t.Run("extended permission", func(t *testing.T) {
		withFixtures(t, func(e *env) {
			role := e.generateRole("view_project", "view_issues")
			e.removePermission(e.builtinRoleID(domain.RoleBuiltinNonMember), "view_issues")
			uid := testfixtures.GenerateUser(t, e.d)
			e.createMember(1, e.builtinGroup(domain.KindGroupNonMember), role)
			ids := e.visibleIssues(e.user(uid), noOpts, "")
			if !slices.Contains(e.issueProjects(ids), 1) {
				t.Errorf("visible = %v", ids)
			}
		})
	})
}

func TestVisibleScopeForMemberWithGroupsShouldReturnAssignedIssues(t *testing.T) {
	withFixtures(t, func(e *env) {
		groups, err := repository.UserGroupIDs(e.ctx, e.d, 8)
		e.must(err)
		group := groups[0]
		e.createMember(1, group, 2)
		e.removePermission(e.builtinRoleID(domain.RoleBuiltinNonMember), "view_issues")
		id := testfixtures.GenerateIssue(t, e.d, testfixtures.IssueAttrs{ProjectID: 1, AuthorID: 3, AssignedToID: group, IsPrivate: true, Subject: "Assignment test"})
		for _, vis := range []string{"default", "own"} {
			e.updateRole(2, func(r *domain.Role) { r.IssuesVisibility = vis })
			if ids := e.visibleIssues(e.user(8), noOpts, ""); !slices.Contains(ids, id) {
				t.Errorf("%s: assigned issue not visible: %v", vis, ids)
			}
			if !e.issueVisible(e.user(8), id) {
				t.Errorf("%s: assigned issue not visible?", vis)
			}
		}
	})
}

func TestVisibleScopeForMemberWithLimitedTrackerIDs(t *testing.T) {
	withFixtures(t, func(e *env) {
		e.updateRole(1, func(r *domain.Role) { r.RestrictedTrackers = map[string][]int64{"view_issues": {2}} })
		u := e.user(2)
		ids := e.visibleIssues(u, noOpts, "issues.project_id = 1")
		if len(ids) == 0 {
			t.Fatal("no issues")
		}
		for _, is := range e.issues("project_id = 1") {
			if slices.Contains(ids, is.ID) != (is.TrackerID == 2) {
				t.Errorf("issue %d (tracker %d) scope visibility mismatch", is.ID, is.TrackerID)
			}
			if e.issueVisible(u, is.ID) != (is.TrackerID == 2) {
				t.Errorf("issue %d (tracker %d) visible? mismatch", is.ID, is.TrackerID)
			}
		}
	})
}

func TestVisibleScopeShouldConsiderTrackerIDsOnEachProject(t *testing.T) {
	withFixtures(t, func(e *env) {
		uid := testfixtures.GenerateUser(t, e.d)
		p1 := &domain.Project{Name: "P1", Identifier: "p1", IsPublic: true}
		e.must(repository.CreateProject(e.ctx, e.d, p1, repository.CreateProjectOptions{EnabledModules: []string{"issue_tracking"}}))
		role1 := e.generateRole("view_issues")
		e.createMember(p1.ID, uid, role1)
		p2 := &domain.Project{Name: "P2", Identifier: "p2", IsPublic: true}
		e.must(repository.CreateProject(e.ctx, e.d, p2, repository.CreateProjectOptions{EnabledModules: []string{"issue_tracking"}}))
		role2 := e.generateRole("view_issues")
		e.updateRole(role2, func(r *domain.Role) { r.RestrictedTrackers = map[string][]int64{"view_issues": {2}} })
		e.createMember(p2.ID, uid, role2)

		visible := []int64{
			testfixtures.GenerateIssue(t, e.d, testfixtures.IssueAttrs{ProjectID: p1.ID, TrackerID: 1}),
			testfixtures.GenerateIssue(t, e.d, testfixtures.IssueAttrs{ProjectID: p1.ID, TrackerID: 2}),
			testfixtures.GenerateIssue(t, e.d, testfixtures.IssueAttrs{ProjectID: p2.ID, TrackerID: 2}),
		}
		hidden := testfixtures.GenerateIssue(t, e.d, testfixtures.IssueAttrs{ProjectID: p2.ID, TrackerID: 1})
		u := e.user(uid)
		ids := e.visibleIssues(u, noOpts, "issues.project_id IN ("+itoa(p1.ID)+", "+itoa(p2.ID)+")")
		if !slices.Equal(ids, visible) {
			t.Errorf("visible = %v, want %v", ids, visible)
		}
		for _, id := range visible {
			if !e.issueVisible(u, id) {
				t.Errorf("issue %d not visible?", id)
			}
		}
		if e.issueVisible(u, hidden) {
			t.Error("hidden issue visible?")
		}
	})
}

func TestVisibleScopeShouldNotConsiderRolesWithoutViewIssuesPermission(t *testing.T) {
	withFixtures(t, func(e *env) {
		uid := testfixtures.GenerateUser(t, e.d)
		role1 := e.generateRole("view_project")
		role2 := e.generateRole("view_issues")
		e.updateRole(role2, func(r *domain.Role) { r.RestrictedTrackers = map[string][]int64{"view_issues": {2}} })
		e.createMember(1, uid, role1, role2)
		u := e.user(uid)
		ids := e.visibleIssues(u, noOpts, "issues.project_id = 1")
		if len(ids) == 0 {
			t.Fatal("no issues")
		}
		for _, is := range e.issues("project_id = 1") {
			if slices.Contains(ids, is.ID) != (is.TrackerID == 2) || e.issueVisible(u, is.ID) != (is.TrackerID == 2) {
				t.Errorf("issue %d (tracker %d) visibility mismatch", is.ID, is.TrackerID)
			}
		}
	})
}

func TestVisibleScopeForAdmin(t *testing.T) {
	withFixtures(t, func(e *env) {
		ms, err := repository.Memberships(e.ctx, e.d, 1)
		e.must(err)
		for _, m := range ms {
			e.must(repository.DestroyMember(e.ctx, e.d, m.ID))
		}
		u := e.user(1)
		ids := e.visibleIssues(u, noOpts, "")
		var privProject, privOther bool
		for _, is := range e.issues("") {
			if !slices.Contains(ids, is.ID) {
				continue
			}
			if !e.project(is.ProjectID).IsPublic {
				privProject = true
			}
			if is.IsPrivate && is.AuthorID != 1 {
				privOther = true
			}
		}
		if !privProject || !privOther {
			t.Errorf("admin should see private projects (%v) and private issues (%v)", privProject, privOther)
		}
		e.assertVisibilityMatch(u, ids)
	})
}

func TestVisibleScopeWithProject(t *testing.T) {
	withFixtures(t, func(e *env) {
		u := e.user(2)
		ids := e.visibleIssues(u, authz.ConditionOptions{Project: e.project(1)}, "")
		if !slices.Equal(e.issueProjects(ids), []int64{1}) {
			t.Errorf("projects = %v", e.issueProjects(ids))
		}
		ids = e.visibleIssues(u, authz.ConditionOptions{Project: e.project(1), WithSubprojects: true}, "")
		ps := e.issueProjects(ids)
		if len(ps) < 2 {
			t.Errorf("projects = %v", ps)
		}
		sub, err := repository.ProjectSelfAndDescendantIDs(e.ctx, e.d, 1)
		e.must(err)
		for _, p := range ps {
			if !slices.Contains(sub, p) {
				t.Errorf("project %d is not a descendant of 1", p)
			}
		}
	})
}

func TestVisibleAndNestedSetScopes(t *testing.T) {
	withFixtures(t, func(e *env) {
		uid := testfixtures.GenerateUser(t, e.d)
		e.createMember(1, uid, 1)
		parent := testfixtures.GenerateIssue(t, e.d, testfixtures.IssueAttrs{AssignedToID: uid})
		u := e.user(uid)
		if !e.issueVisible(u, parent) {
			t.Error("parent not visible")
		}
		c1 := testfixtures.GenerateIssue(t, e.d, testfixtures.IssueAttrs{ParentID: parent, AssignedToID: uid})
		c2 := testfixtures.GenerateIssue(t, e.d, testfixtures.IssueAttrs{ParentID: parent, AssignedToID: uid})
		if !e.issueVisible(u, c1) || !e.issueVisible(u, c2) {
			t.Error("children not visible")
		}
		var path string
		e.must(e.d.Get(e.ctx, &path, `SELECT hier_path FROM issues WHERE id = ?`, parent))
		desc := e.visibleIssues(u, noOpts, "issues.hier_path LIKE '"+path+"%' AND issues.id <> "+itoa(parent))
		if !slices.Equal(desc, []int64{c1, c2}) {
			t.Errorf("visible descendants = %v", desc)
		}
	})
}

func TestVisibleScopeWithUnsavedUserShouldNotRaiseAnError(t *testing.T) {
	withFixtures(t, func(e *env) {
		u := &domain.User{Principal: domain.Principal{Kind: domain.KindUser, Status: domain.StatusActive}}
		e.visibleIssues(u, noOpts, "")
		cond, err := e.az(u).PrincipalVisibleCondition(e.ctx)
		e.must(err)
		var n int
		e.must(e.d.Get(e.ctx, &n, `SELECT COUNT(*) FROM principals WHERE `+cond))
	})
}

func TestPrincipalVisibleScope(t *testing.T) {
	t.Run("admin should return all principals", func(t *testing.T) {
		withFixtures(t, func(e *env) {
			admin := testfixtures.GenerateUserWith(t, e.d, testfixtures.UserAttrs{Admin: true})
			cond, err := e.az(e.user(admin)).PrincipalVisibleCondition(e.ctx)
			e.must(err)
			var n, all int
			e.must(e.d.Get(e.ctx, &n, `SELECT COUNT(*) FROM principals WHERE `+cond))
			e.must(e.d.Get(e.ctx, &all, `SELECT COUNT(*) FROM principals`))
			if n != all {
				t.Errorf("visible %d, all %d", n, all)
			}
		})
	})
	t.Run("users_visibility all should return active principals", func(t *testing.T) {
		withFixtures(t, func(e *env) {
			e.updateRole(e.builtinRoleID(domain.RoleBuiltinNonMember), func(r *domain.Role) { r.UsersVisibility = "all" })
			uid := testfixtures.GenerateUser(t, e.d)
			got := e.visiblePrincipals(e.user(uid))
			var want []int64
			e.must(e.d.Select(e.ctx, &want, `SELECT id FROM principals WHERE status = 1 ORDER BY id`))
			if !slices.Equal(got, want) {
				t.Errorf("visible = %v, want %v", got, want)
			}
		})
	})
	t.Run("members_of_visible_projects should return members of visible projects and self", func(t *testing.T) {
		withFixtures(t, func(e *env) {
			e.updateRole(e.builtinRoleID(domain.RoleBuiltinNonMember), func(r *domain.Role) { r.UsersVisibility = "members_of_visible_projects" })
			uid := testfixtures.GenerateUser(t, e.d)
			u := e.user(uid)
			vp, err := e.az(u).VisibleProjectIDs(e.ctx)
			e.must(err)
			// Project.visible(user).map {|p| p.memberships.active}.flatten.map(&:principal).uniq << user
			want := []int64{uid}
			for _, pid := range vp {
				ms, err := repository.ProjectMemberships(e.ctx, e.d, pid)
				e.must(err)
				for _, m := range ms {
					pr, err := repository.GetPrincipal(e.ctx, e.d, m.PrincipalID)
					e.must(err)
					if pr.Active() && !slices.Contains(want, pr.ID) {
						want = append(want, pr.ID)
					}
				}
			}
			slices.Sort(want)
			if got := e.visiblePrincipals(u); !slices.Equal(got, want) {
				t.Errorf("visible = %v, want %v", got, want)
			}
		})
	})
}

func (e *env) visiblePrincipals(u *domain.User) []int64 {
	e.t.Helper()
	cond, err := e.az(u).PrincipalVisibleCondition(e.ctx)
	e.must(err)
	var ids []int64
	e.must(e.d.Select(e.ctx, &ids, `SELECT principals.id FROM principals WHERE `+cond+` ORDER BY principals.id`))
	return ids
}

func TestOverrideRolesWithoutBuiltinGroupMemberships(t *testing.T) {
	withFixtures(t, func(e *env) {
		p := &domain.Project{Name: "Generated", Identifier: "generated", IsPublic: true}
		e.must(repository.CreateProject(e.ctx, e.d, p, repository.CreateProjectOptions{}))
		for _, b := range []int{domain.RoleBuiltinAnonymous, domain.RoleBuiltinNonMember} {
			role, err := repository.BuiltinRole(e.ctx, e.d, b)
			e.must(err)
			rs, err := e.az(e.user(2)).OverrideRoles(e.ctx, p, role)
			e.must(err)
			if len(rs) != 1 || rs[0].ID != role.ID {
				t.Errorf("override_roles(%d) = %v", b, sortedRoleIDs(rs))
			}
		}
	})
}

func TestProjectVisibleCondition(t *testing.T) {
	withFixtures(t, func(e *env) {
		for _, c := range []struct {
			user int64
			want []int64
		}{
			{1, []int64{1, 2, 3, 4, 5, 6}}, // 管理者
			{2, []int64{1, 2, 3, 4, 5, 6}}, // jsmith: 2, 5 のメンバー
			{3, []int64{1, 3, 4, 6}},       // dlopper: 非公開の 2, 5 は見えない
			{6, []int64{1, 3, 4, 6}},       // 匿名
		} {
			cond, err := e.az(e.user(c.user)).VisibleCondition(e.ctx, noOpts)
			e.must(err)
			var ids []int64
			e.must(e.d.Select(e.ctx, &ids, `SELECT projects.id FROM projects WHERE `+cond+` ORDER BY projects.id`))
			if !slices.Equal(ids, c.want) {
				t.Errorf("user %d: Project.visible = %v, want %v", c.user, ids, c.want)
			}
			for _, p := range e.projects(1, 2, 3, 4, 5, 6) {
				ok, err := e.az(e.user(c.user)).ProjectVisible(e.ctx, p)
				e.must(err)
				if ok != slices.Contains(c.want, p.ID) {
					t.Errorf("user %d: project %d visible? = %v", c.user, p.ID, ok)
				}
			}
		}
		// 管理者は条件がステータスのみ (Project.visible_condition(admin) => "projects.status IN (1, 5)")
		cond, err := e.az(e.user(1)).VisibleCondition(e.ctx, noOpts)
		e.must(err)
		if cond != "projects.status IN (1, 5)" {
			t.Errorf("admin condition = %q", cond)
		}
		// 権限を持つロールが無ければ "1=0"
		cond, err = e.az(e.anonymous()).AllowedToCondition(e.ctx, "add_project", noOpts, nil)
		e.must(err)
		if cond != "1=0" {
			t.Errorf("anonymous add_project condition = %q", cond)
		}
	})
}
