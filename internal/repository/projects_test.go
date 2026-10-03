// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository_test

// test/unit/project_test.rb のうち階層・ステータス・モジュール関連の移植。

import (
	"errors"
	"slices"
	"testing"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

func projectIDs(ps []*domain.Project) []int64 {
	var ids []int64
	for _, p := range ps {
		ids = append(ids, p.ID)
	}
	return ids
}

func (e *env) status(id int64) int { return e.project(id).Status }

func TestProjectArchive(t *testing.T) {
	withFixtures(t, func(e *env) {
		ok, err := repository.ArchiveProject(e.ctx, e.d, 1)
		e.must(err)
		if !ok {
			t.Fatal("archive failed")
		}
		p := e.project(1)
		if p.Active() || !p.Archived() {
			t.Error("project 1 not archived")
		}
		// user.projects (アーカイブされていないメンバーシップ) に含まれない
		ids, err := repository.MemberProjectIDs(e.ctx, e.d, 2)
		e.must(err)
		if slices.Contains(ids, 1) {
			t.Error("archived project in user.projects")
		}
		desc, err := repository.ProjectDescendants(e.ctx, e.d, 1)
		e.must(err)
		if len(desc) == 0 {
			t.Fatal("no descendants")
		}
		for _, d := range desc {
			if d.Active() {
				t.Errorf("descendant %d still active", d.ID)
			}
		}
	})
}

func TestProjectArchiveShouldFailIfVersionsAreUsedByNonDescendantProjects(t *testing.T) {
	withFixtures(t, func(e *env) {
		_, err := e.d.Exec(e.ctx, `UPDATE issues SET fixed_version_id = 4 WHERE id = 4`)
		e.must(err)
		before := e.count(`SELECT COUNT(*) FROM projects WHERE status = 9`)
		ok, err := repository.ArchiveProject(e.ctx, e.d, 1)
		e.must(err)
		if ok {
			t.Error("archive should fail")
		}
		if after := e.count(`SELECT COUNT(*) FROM projects WHERE status = 9`); after != before {
			t.Error("projects archived")
		}
		if !e.project(1).Active() {
			t.Error("project 1 not active")
		}
	})
}

func TestProjectUnarchive(t *testing.T) {
	withFixtures(t, func(e *env) {
		_, err := repository.ArchiveProject(e.ctx, e.d, 1)
		e.must(err)
		e.must(repository.UnarchiveProject(e.ctx, e.d, 1))
		if !e.project(1).Active() {
			t.Error("project 1 not active")
		}
		ids, err := repository.MemberProjectIDs(e.ctx, e.d, 2)
		e.must(err)
		if !slices.Contains(ids, 1) {
			t.Error("project 1 not in user.projects")
		}
	})
	t.Run("child should unarchive ancestors", func(t *testing.T) {
		withFixtures(t, func(e *env) {
			_, err := repository.ArchiveProject(e.ctx, e.d, 1)
			e.must(err)
			if e.status(3) != domain.ProjectStatusArchived {
				t.Fatal("project 3 not archived")
			}
			e.must(repository.UnarchiveProject(e.ctx, e.d, 3))
			if e.status(3) != domain.ProjectStatusActive || e.status(1) != domain.ProjectStatusActive {
				t.Errorf("status 3=%d 1=%d", e.status(3), e.status(1))
			}
		})
	})
	t.Run("child of a closed project should be set to closed", func(t *testing.T) {
		withFixtures(t, func(e *env) {
			e.must(repository.CloseProject(e.ctx, e.d, 1))
			if e.status(3) != domain.ProjectStatusClosed {
				t.Fatal("project 3 not closed")
			}
			_, err := repository.ArchiveProject(e.ctx, e.d, 3)
			e.must(err)
			if e.status(3) != domain.ProjectStatusArchived {
				t.Fatal("project 3 not archived")
			}
			e.must(repository.UnarchiveProject(e.ctx, e.d, 3))
			if e.status(3) != domain.ProjectStatusClosed {
				t.Errorf("status = %d", e.status(3))
			}
		})
	})
}

func TestProjectCloseReopen(t *testing.T) {
	withFixtures(t, func(e *env) {
		e.must(repository.CloseProject(e.ctx, e.d, 1))
		for _, id := range []int64{1, 3, 4, 5, 6} {
			if e.status(id) != domain.ProjectStatusClosed {
				t.Errorf("project %d not closed", id)
			}
		}
		if e.status(2) != domain.ProjectStatusActive {
			t.Error("project 2 closed")
		}
		e.must(repository.ReopenProject(e.ctx, e.d, 5))
		if e.status(5) != domain.ProjectStatusActive || e.status(6) != domain.ProjectStatusActive || e.status(1) != domain.ProjectStatusClosed {
			t.Error("reopen")
		}
	})
}

func TestProjectMoves(t *testing.T) {
	withFixtures(t, func(e *env) {
		// test_move_an_orphan_project_to_a_root_project
		e.must(e.setParent(2, 1))
		if p := e.project(2); p.ParentID == nil || *p.ParentID != 1 {
			t.Error("parent not set")
		}
		children, err := repository.ProjectChildren(e.ctx, e.d, 1)
		e.must(err)
		if len(children) != 4 {
			t.Errorf("children = %v", projectIDs(children))
		}
		// 閉包テーブルも更新されている
		anc, err := repository.ProjectAncestors(e.ctx, e.d, 2)
		e.must(err)
		if !slices.Equal(projectIDs(anc), []int64{1}) {
			t.Errorf("ancestors = %v", projectIDs(anc))
		}
		// test_move_an_orphan_project_to_a_subproject
		e.must(e.setParent(2, 3))
		anc, err = repository.ProjectAncestors(e.ctx, e.d, 2)
		e.must(err)
		if !slices.Equal(projectIDs(anc), []int64{1, 3}) {
			t.Errorf("ancestors = %v", projectIDs(anc))
		}
		// test_should_not_move_a_project_to_its_children
		if err := e.setParent(1, 3); !errors.Is(err, repository.ErrInvalidParent) {
			t.Errorf("move to child: %v", err)
		}
		if err := e.setParent(1, 1); !errors.Is(err, repository.ErrInvalidParent) {
			t.Errorf("move to self: %v", err)
		}
		// test_move_a_root_project_to_a_project
		e.must(e.setParent(2, 0))
		e.must(e.setParent(1, 2))
		desc, err := repository.ProjectDescendants(e.ctx, e.d, 2)
		e.must(err)
		if !slices.Equal(projectIDs(desc), []int64{1, 5, 6, 3, 4}) {
			t.Errorf("descendants of 2 = %v", projectIDs(desc))
		}
		var closure int
		e.must(e.d.Get(e.ctx, &closure, `SELECT depth FROM project_closure WHERE ancestor_id = 2 AND descendant_id = 6`))
		if closure != 3 {
			t.Errorf("depth 2->6 = %d", closure)
		}
	})
}

func TestProjectMoveToClosedParentIsInvalid(t *testing.T) {
	withFixtures(t, func(e *env) {
		e.must(repository.CloseProject(e.ctx, e.d, 2))
		if err := e.setParent(3, 2); !errors.Is(err, repository.ErrInvalidParent) {
			t.Errorf("move under closed project: %v", err)
		}
	})
}

func TestSetParentShouldAddInAlphabeticalOrder(t *testing.T) {
	withFixtures(t, func(e *env) {
		var roots []int64
		for _, n := range []string{"Project C", "Project B", "Project D", "Project A"} {
			p := &domain.Project{Name: n, Identifier: "r-" + n[len(n)-1:], IsPublic: true}
			e.must(repository.CreateProject(e.ctx, e.d, p, repository.CreateProjectOptions{}))
			roots = append(roots, p.ID)
		}
		all, err := repository.ListProjects(e.ctx, e.d)
		e.must(err)
		var order []int64
		for _, id := range projectIDs(all) {
			if slices.Contains(roots, id) {
				order = append(order, id)
			}
		}
		// A, B, C, D
		if want := []int64{roots[3], roots[1], roots[0], roots[2]}; !slices.Equal(order, want) {
			t.Errorf("roots order = %v, want %v", order, want)
		}
		parent := &domain.Project{Name: "Parent", Identifier: "parent", IsPublic: true}
		e.must(repository.CreateProject(e.ctx, e.d, parent, repository.CreateProjectOptions{}))
		var kids []int64
		for _, n := range []string{"Project C", "Project B", "Project D", "Project A"} {
			p := &domain.Project{Name: n, Identifier: "c-" + n[len(n)-1:], IsPublic: true}
			e.must(repository.CreateProject(e.ctx, e.d, p, repository.CreateProjectOptions{}))
			e.must(e.setParent(p.ID, parent.ID))
			kids = append(kids, p.ID)
		}
		children, err := repository.ProjectChildren(e.ctx, e.d, parent.ID)
		e.must(err)
		if want := []int64{kids[3], kids[1], kids[0], kids[2]}; !slices.Equal(projectIDs(children), want) {
			t.Errorf("children = %v, want %v", projectIDs(children), want)
		}
	})
}

func TestProjectHierarchy(t *testing.T) {
	withFixtures(t, func(e *env) {
		if p := e.project(6); p.ParentID == nil || *p.ParentID != 5 {
			t.Error("parent of 6")
		}
		check := func(name string, got []*domain.Project, err error, want []int64) {
			t.Helper()
			e.must(err)
			if !slices.Equal(projectIDs(got), want) {
				t.Errorf("%s = %v, want %v", name, projectIDs(got), want)
			}
		}
		anc, err := repository.ProjectAncestors(e.ctx, e.d, 6)
		check("ancestors", anc, err, []int64{1, 5})
		sa, err := repository.ProjectSelfAndAncestors(e.ctx, e.d, 6)
		check("self_and_ancestors", sa, err, []int64{1, 5, 6})
		ch, err := repository.ProjectChildren(e.ctx, e.d, 1)
		check("children", ch, err, []int64{5, 3, 4})
		de, err := repository.ProjectDescendants(e.ctx, e.d, 1)
		check("descendants", de, err, []int64{5, 6, 3, 4})
		sd, err := repository.ProjectSelfAndDescendants(e.ctx, e.d, 1)
		check("self_and_descendants", sd, err, []int64{1, 5, 6, 3, 4})
		leaf, err := repository.IsProjectLeaf(e.ctx, e.d, 6)
		e.must(err)
		notLeaf, err := repository.IsProjectLeaf(e.ctx, e.d, 1)
		e.must(err)
		if !leaf || notLeaf {
			t.Error("leaf?")
		}
		// Project.find: 数字は id、それ以外は識別子
		p, err := repository.FindProject(e.ctx, e.d, "ecookbook")
		e.must(err)
		p2, err := repository.FindProject(e.ctx, e.d, "1")
		e.must(err)
		if p.ID != 1 || p2.ID != 1 {
			t.Error("FindProject")
		}
		if _, err := repository.FindProject(e.ctx, e.d, "nonexistent"); !errors.Is(err, repository.ErrNotFound) {
			t.Errorf("not found: %v", err)
		}
	})
}

func TestProjectNestedSetValues(t *testing.T) {
	withFixtures(t, func(e *env) {
		ns, err := repository.ProjectNestedSet(e.ctx, e.d)
		e.must(err)
		// 兄弟順は fixtures の lft を保持する (projects.position) ので lft/rgt も fixtures と一致する
		want := map[int64]repository.NestedSetValue{
			1: {Lft: 1, Rgt: 10}, 5: {Lft: 2, Rgt: 5}, 6: {Lft: 3, Rgt: 4},
			3: {Lft: 6, Rgt: 7}, 4: {Lft: 8, Rgt: 9}, 2: {Lft: 11, Rgt: 12},
		}
		for id, w := range want {
			if ns[id] != w {
				t.Errorf("project %d = %+v, want %+v", id, ns[id], w)
			}
		}
		// 新規プロジェクトは名前 (大文字小文字無視) で挿入位置が決まる: "Mid" は eCookbook と OnlineStore の間、
		// 同名の兄弟では後から作った方が前 (Redmine の target_lft と同じ)
		a := &domain.Project{Name: "Mid", Identifier: "same-a", IsPublic: true}
		e.must(repository.CreateProject(e.ctx, e.d, a, repository.CreateProjectOptions{}))
		b := &domain.Project{Name: "Mid", Identifier: "same-b", IsPublic: true}
		e.must(repository.CreateProject(e.ctx, e.d, b, repository.CreateProjectOptions{}))
		ns, err = repository.ProjectNestedSet(e.ctx, e.d)
		e.must(err)
		if ns[b.ID].Lft >= ns[a.ID].Lft {
			t.Errorf("same-name siblings: a=%+v b=%+v", ns[a.ID], ns[b.ID])
		}
		if !(ns[1].Lft < ns[b.ID].Lft && ns[a.ID].Lft < ns[2].Lft) {
			t.Errorf("insert position: ecookbook=%+v same=%+v/%+v onlinestore=%+v", ns[1], ns[b.ID], ns[a.ID], ns[2])
		}
	})
}

func TestEnabledModules(t *testing.T) {
	withFixtures(t, func(e *env) {
		var before []int64
		e.must(e.d.Select(e.ctx, &before, `SELECT id FROM project_modules WHERE project_id = 1 ORDER BY id`))
		names := e.project(1).EnabledModuleNames
		keep := names[:len(names)-1]
		n := e.count(`SELECT COUNT(*) FROM project_modules`)
		e.must(repository.SetEnabledModules(e.ctx, e.d, 1, keep))
		if got := e.count(`SELECT COUNT(*) FROM project_modules`); got != n-1 {
			t.Errorf("modules %d -> %d", n, got)
		}
		var after []int64
		e.must(e.d.Select(e.ctx, &after, `SELECT id FROM project_modules WHERE project_id = 1 ORDER BY id`))
		if !slices.Equal(after, before[:len(before)-1]) {
			t.Errorf("ids not preserved: %v -> %v", before, after)
		}
		// enable / disable
		e.must(repository.SetEnabledModules(e.ctx, e.d, 1, nil))
		if len(e.project(1).EnabledModuleNames) != 0 {
			t.Error("modules not cleared")
		}
		e.must(repository.EnableModule(e.ctx, e.d, 1, "issue_tracking"))
		e.must(repository.EnableModule(e.ctx, e.d, 1, "gantt"))
		e.must(repository.EnableModule(e.ctx, e.d, 1, "issue_tracking"))
		if got := e.project(1).EnabledModuleNames; !slices.Equal(got, []string{"issue_tracking", "gantt"}) {
			t.Errorf("modules = %v", got)
		}
		e.must(repository.DisableModule(e.ctx, e.d, 1, "issue_tracking"))
		if e.project(1).ModuleEnabled("issue_tracking") {
			t.Error("issue_tracking still enabled")
		}
		if err := repository.SetEnabledModules(e.ctx, e.d, 1, []string{"bogus"}); err == nil {
			t.Error("unknown module accepted")
		}
	})
}

func TestRoleRepository(t *testing.T) {
	withFixtures(t, func(e *env) {
		rs, err := repository.ListRoles(e.ctx, e.d)
		e.must(err)
		var ids []int64
		for _, r := range rs {
			ids = append(ids, r.ID)
		}
		// Role.sorted: builtin, position
		if !slices.Equal(ids, []int64{1, 2, 3, 4, 5}) {
			t.Errorf("roles = %v", ids)
		}
		g, err := repository.GivableRoles(e.ctx, e.d)
		e.must(err)
		if len(g) != 3 {
			t.Errorf("givable = %d", len(g))
		}
		// test_anonymous_should_return_the_anonymous_role / test_non_member...
		n := e.count(`SELECT COUNT(*) FROM roles`)
		for _, b := range []int{domain.RoleBuiltinAnonymous, domain.RoleBuiltinNonMember} {
			r, err := repository.BuiltinRole(e.ctx, e.d, b)
			e.must(err)
			if !r.IsBuiltin() || r.Builtin != b {
				t.Errorf("builtin role %d = %+v", b, r)
			}
		}
		if e.count(`SELECT COUNT(*) FROM roles`) != n {
			t.Error("builtin role created")
		}
		// 存在しなければ作成する
		_, err = e.d.Exec(e.ctx, `DELETE FROM roles WHERE builtin = 2`)
		e.must(err)
		r, err := repository.BuiltinRole(e.ctx, e.d, domain.RoleBuiltinAnonymous)
		e.must(err)
		if r.Builtin != domain.RoleBuiltinAnonymous || r.Name != "Anonymous" || e.count(`SELECT COUNT(*) FROM roles`) != n {
			t.Errorf("recreated anonymous role = %+v", r)
		}
		// 権限とトラッカー制限の保存・読み込み
		role := &domain.Role{Name: "Test", Permissions: []string{"view_issues", "edit_issues", "", "view_issues"},
			RestrictedTrackers: map[string][]int64{"view_issues": {1, 2}, "delete_issues": {3}}, ManagedRoleIDs: []int64{2, 3}}
		e.must(repository.SaveRole(e.ctx, e.d, role))
		got, err := repository.GetRole(e.ctx, e.d, role.ID)
		e.must(err)
		// 保存順（Role#permissions= の配列順）で読み込む
		if !slices.Equal(got.Permissions, []string{"view_issues", "edit_issues"}) {
			t.Errorf("permissions = %v", got.Permissions)
		}
		if !got.PermissionsTrackerIDsInclude("view_issues", 2) || got.PermissionsAllTrackers("view_issues") || !got.PermissionsAllTrackers("edit_issues") {
			t.Error("tracker restriction")
		}
		// 持っていない権限のトラッカー制限は保存されない
		if _, ok := got.RestrictedTrackers["delete_issues"]; ok {
			t.Error("restriction of a missing permission saved")
		}
		if !slices.Equal(got.ManagedRoleIDs, []int64{2, 3}) || got.Position != 4 || got.IssuesVisibility != "default" {
			t.Errorf("role = %+v", got)
		}
		// 使用中のロールと組込ロールは削除できない
		if err := repository.DestroyRole(e.ctx, e.d, 1); !errors.Is(err, repository.ErrRoleNotDeletable) {
			t.Errorf("destroy used role: %v", err)
		}
		if err := repository.DestroyRole(e.ctx, e.d, 4); !errors.Is(err, repository.ErrRoleNotDeletable) {
			t.Errorf("destroy builtin role: %v", err)
		}
		e.must(repository.DestroyRole(e.ctx, e.d, role.ID))
	})
}

func TestPrincipalRepository(t *testing.T) {
	withFixtures(t, func(e *env) {
		u, err := repository.GetUser(e.ctx, e.d, 2)
		e.must(err)
		if u.Login != "jsmith" || u.Firstname != "John" || u.IsAdmin() || u.Kind != domain.KindUser || u.Language != "en" {
			t.Errorf("user 2 = %+v", u)
		}
		if _, err := repository.GetUser(e.ctx, e.d, 10); !errors.Is(err, repository.ErrNotFound) {
			t.Errorf("group as user: %v", err)
		}
		g, err := repository.GetGroup(e.ctx, e.d, 10)
		e.must(err)
		if g.Name != "A Team" || g.Builtin() {
			t.Errorf("group 10 = %+v", g)
		}
		a, err := repository.AnonymousUser(e.ctx, e.d)
		e.must(err)
		if a.ID != 6 || a.Logged() {
			t.Errorf("anonymous = %+v", a)
		}
		users, err := repository.ListUsers(e.ctx, e.d, domain.StatusActive)
		e.must(err)
		if len(users) != 7 {
			t.Errorf("active users = %d", len(users))
		}
		gids, err := repository.UserGroupIDs(e.ctx, e.d, 8)
		e.must(err)
		if !slices.Equal(gids, []int64{10, 11}) {
			t.Errorf("groups of 8 = %v", gids)
		}
	})
}
