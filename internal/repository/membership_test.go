// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository_test

// test/unit/member_test.rb, group_test.rb, project_members_inheritance_test.rb の移植。

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/testfixtures"
)

type env struct {
	t   *testing.T
	ctx context.Context
	d   *db.DB
}

func withFixtures(t *testing.T, fn func(e *env)) {
	t.Helper()
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		testfixtures.Load(t, d, testfixtures.All()...)
		fn(&env{t: t, ctx: context.Background(), d: d})
	})
}

func (e *env) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	e.must(e.d.Get(e.ctx, &n, q, args...))
	return n
}

func (e *env) members() int     { return e.count(`SELECT COUNT(*) FROM members`) }
func (e *env) memberRoles() int { return e.count(`SELECT COUNT(*) FROM member_roles`) }

func (e *env) createMember(projectID, principalID int64, roleIDs ...int64) int64 {
	e.t.Helper()
	id, err := repository.CreateMember(e.ctx, e.d, projectID, principalID, roleIDs)
	e.must(err)
	return id
}

func (e *env) member(projectID, principalID int64) *domain.Member {
	e.t.Helper()
	m, err := repository.FindMember(e.ctx, e.d, projectID, principalID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil
	}
	e.must(err)
	return m
}

func (e *env) memberOf(principalID, projectID int64) bool {
	e.t.Helper()
	m, err := repository.Membership(e.ctx, e.d, principalID, projectID)
	e.must(err)
	return m != nil
}

func roleSet(m *domain.Member) []int64 {
	if m == nil {
		return nil
	}
	ids := m.RoleIDs()
	slices.Sort(ids)
	return ids
}

func (e *env) rolesForProject(userID, projectID int64) []int64 {
	e.t.Helper()
	u, err := repository.GetUser(e.ctx, e.d, userID)
	e.must(err)
	p, err := repository.GetProject(e.ctx, e.d, projectID)
	e.must(err)
	rs, err := authz.New(e.d, u).RolesForProject(e.ctx, p)
	e.must(err)
	ids := []int64{}
	for _, r := range rs {
		ids = append(ids, r.ID)
	}
	slices.Sort(ids)
	return ids
}

var projectSeq int

// generateProject は Project.generate! / generate_with_parent! 相当。
func (e *env) generateProject(parent int64, inherit bool) int64 {
	e.t.Helper()
	projectSeq++
	p := &domain.Project{Name: fmt.Sprintf("Generated project %d", projectSeq), Identifier: fmt.Sprintf("gen-%d", projectSeq),
		IsPublic: true, InheritMembers: inherit}
	if parent != 0 {
		p.ParentID = &parent
	}
	e.must(repository.CreateProject(e.ctx, e.d, p, repository.CreateProjectOptions{EnabledModules: []string{"issue_tracking"}}))
	return p.ID
}

func (e *env) project(id int64) *domain.Project {
	e.t.Helper()
	p, err := repository.GetProject(e.ctx, e.d, id)
	e.must(err)
	return p
}

func (e *env) setInherit(id int64, v bool) {
	e.t.Helper()
	p := e.project(id)
	p.InheritMembers = v
	e.must(repository.UpdateProject(e.ctx, e.d, p))
}

func (e *env) setParent(id int64, parent int64) error {
	e.t.Helper()
	p := e.project(id)
	p.ParentID = nil
	if parent != 0 {
		p.ParentID = &parent
	}
	return repository.UpdateProject(e.ctx, e.d, p)
}

func (e *env) projectMembers(id int64) []*domain.Member {
	e.t.Helper()
	ms, err := repository.ProjectMemberships(e.ctx, e.d, id)
	e.must(err)
	return ms
}

func (e *env) diff(before int, after func() int, want int, what string) {
	e.t.Helper()
	if got := after() - before; got != want {
		e.t.Errorf("%s changed by %d, want %d", what, got, want)
	}
}

// ---------------------------------------------------------------- member_test.rb

func TestMemberCreate(t *testing.T) {
	withFixtures(t, func(e *env) {
		id := e.createMember(1, 4, 1, 2)
		m, err := repository.GetMember(e.ctx, e.d, id)
		e.must(err)
		if !slices.Equal(roleSet(m), []int64{1, 2}) {
			t.Errorf("roles = %v", roleSet(m))
		}
	})
}

func TestMemberUpdateRoles(t *testing.T) {
	withFixtures(t, func(e *env) {
		e.must(repository.SetMemberRoles(e.ctx, e.d, 1, []int64{1, 2}))
		m, err := repository.GetMember(e.ctx, e.d, 1)
		e.must(err)
		if len(m.RoleIDs()) != 2 {
			t.Errorf("roles = %v", m.RoleIDs())
		}
	})
}

func TestMemberUpdateRolesWithInheritedRoles(t *testing.T) {
	withFixtures(t, func(e *env) {
		groupA := testfixtures.GenerateGroup(t, e.d)
		groupB := testfixtures.GenerateGroup(t, e.d)
		user := testfixtures.GenerateUser(t, e.d)
		e.must(repository.AddUserToGroup(e.ctx, e.d, groupA, user))
		e.must(repository.AddUserToGroup(e.ctx, e.d, groupB, user))
		ma := e.createMember(1, groupA, 1)
		mb := e.createMember(1, groupB, 1, 2)
		mrID := func(memberID, roleID int64) int64 {
			var id int64
			e.must(e.d.Get(e.ctx, &id, `SELECT id FROM member_roles WHERE member_id = ? AND role_id = ?`, memberID, roleID))
			return id
		}
		pairs := func() []string {
			m := e.member(1, user)
			var out []string
			for _, mr := range m.MemberRoles {
				from := "nil"
				if mr.InheritedFrom != nil {
					from = fmt.Sprint(*mr.InheritedFrom)
				}
				out = append(out, fmt.Sprintf("%d:%s", mr.RoleID, from))
			}
			slices.Sort(out)
			return out
		}
		want := []string{
			fmt.Sprintf("1:%d", mrID(ma, 1)), fmt.Sprintf("1:%d", mrID(mb, 1)), fmt.Sprintf("2:%d", mrID(mb, 2)),
		}
		slices.Sort(want)
		if got := pairs(); !slices.Equal(got, want) {
			t.Errorf("inherited roles = %v, want %v", got, want)
		}
		// set_editable_role_ids([3]) (管理者による操作: 全ロールが編集可能)
		e.must(repository.SetMemberRoles(e.ctx, e.d, e.member(1, user).ID, []int64{3}))
		want = append(want, "3:nil")
		slices.Sort(want)
		if got := pairs(); !slices.Equal(got, want) {
			t.Errorf("after update = %v, want %v", got, want)
		}
	})
}

func TestMemberValidate(t *testing.T) {
	withFixtures(t, func(e *env) {
		// 同じユーザは同じプロジェクトに 2 つのメンバーシップを持てない
		if _, err := repository.CreateMember(e.ctx, e.d, 1, 2, []int64{2}); !errors.Is(err, repository.ErrMemberTaken) {
			t.Errorf("duplicate member: %v", err)
		}
		// ロールが 1 つ以上必要
		user := testfixtures.GenerateUser(t, e.d)
		if _, err := repository.CreateMember(e.ctx, e.d, 1, user, nil); !errors.Is(err, repository.ErrMemberRoleEmpty) {
			t.Errorf("empty roles: %v", err)
		}
		// test_validate_member_role: 組込ロールは付与できない
		if _, err := repository.CreateMember(e.ctx, e.d, 1, user, []int64{5}); !errors.Is(err, repository.ErrInvalidMemberRole) {
			t.Errorf("builtin role: %v", err)
		}
		if e.member(1, user) != nil {
			t.Error("member created")
		}
	})
}

func TestMemberDestroy(t *testing.T) {
	withFixtures(t, func(e *env) {
		if e.count(`SELECT assigned_to_id FROM issue_categories WHERE id = 1`) != 2 {
			t.Fatal("category 1 should be assigned to jsmith")
		}
		m, mr := e.members(), e.memberRoles()
		e.must(repository.DestroyMember(e.ctx, e.d, 1))
		e.diff(m, e.members, -1, "members")
		e.diff(mr, e.memberRoles, -1, "member_roles")
		if _, err := repository.GetMember(e.ctx, e.d, 1); !errors.Is(err, repository.ErrNotFound) {
			t.Error("member still exists")
		}
		if e.count(`SELECT COUNT(*) FROM issue_categories WHERE id = 1 AND assigned_to_id IS NULL`) != 1 {
			t.Error("category assignment not cleared")
		}
		// test_destroy_should_trigger_callbacks_only_once (件数のみ)
		id := e.createMember(1, 1, 1, 3)
		m, mr = e.members(), e.memberRoles()
		e.must(repository.DestroyMember(e.ctx, e.d, id))
		e.diff(m, e.members, -1, "members")
		e.diff(mr, e.memberRoles, -2, "member_roles")
	})
}

func TestMemberRolesShouldBeUnique(t *testing.T) {
	withFixtures(t, func(e *env) {
		id := e.createMember(1, 1, 1, 1)
		m, err := repository.GetMember(e.ctx, e.d, id)
		e.must(err)
		if !slices.Equal(m.RoleIDs(), []int64{1}) {
			t.Errorf("roles = %v", m.RoleIDs())
		}
	})
}

func TestMemberManagedRoles(t *testing.T) {
	withFixtures(t, func(e *env) {
		givable, err := repository.GivableRoles(e.ctx, e.d)
		e.must(err)
		var givableIDs []int64
		for _, r := range givable {
			givableIDs = append(givableIDs, r.ID)
		}
		newRole := func(perms []string, all bool, managed ...int64) int64 {
			r := &domain.Role{Name: fmt.Sprintf("Managed %d", e.count(`SELECT COUNT(*) FROM roles`)), Permissions: perms,
				AllRolesManaged: all, ManagedRoleIDs: managed}
			e.must(repository.SaveRole(e.ctx, e.d, r))
			return r.ID
		}
		managed := func(principal int64, roles ...int64) []int64 {
			// Member.new: 未保存のメンバーと同じく、ロールだけを持つメンバーで評価する
			m := &domain.Member{PrincipalID: principal}
			for _, r := range roles {
				m.MemberRoles = append(m.MemberRoles, domain.MemberRole{RoleID: r})
			}
			rs, err := authz.MemberManagedRoles(e.ctx, e.d, m)
			e.must(err)
			ids := []int64{}
			for _, r := range rs {
				ids = append(ids, r.ID)
			}
			slices.Sort(ids)
			return ids
		}
		user := testfixtures.GenerateUser(t, e.d)
		all := newRole([]string{"manage_members"}, true)
		givableIDs = append(givableIDs, all)
		slices.Sort(givableIDs)
		if got := managed(user, all); !slices.Equal(got, givableIDs) {
			t.Errorf("all_roles_managed: %v, want %v", got, givableIDs)
		}
		plain := newRole(nil, true)
		givableIDs = append(givableIDs, plain)
		slices.Sort(givableIDs)
		if got := managed(1, plain); !slices.Equal(got, givableIDs) {
			t.Errorf("admin: %v, want %v", got, givableIDs)
		}
		limited := newRole([]string{"manage_members"}, false, 2, 3)
		if got := managed(user, limited); !slices.Equal(got, []int64{2, 3}) {
			t.Errorf("limited: %v", got)
		}
		r3 := newRole([]string{"manage_members"}, false, 3)
		r2 := newRole([]string{"manage_members"}, false, 2)
		if got := managed(user, r3, r2); !slices.Equal(got, []int64{2, 3}) {
			t.Errorf("cumulated: %v", got)
		}
		if got := managed(user, plain); len(got) != 0 {
			t.Errorf("without permission: %v", got)
		}
	})
}

func TestCreatePrincipalMembershipsWithInheritance(t *testing.T) {
	withFixtures(t, func(e *env) {
		parent := e.generateProject(0, false)
		child := e.generateProject(parent, true)
		user := testfixtures.GenerateUser(t, e.d)
		before := e.members()
		ids, err := repository.CreatePrincipalMemberships(e.ctx, e.d, user, []int64{parent, child}, []int64{1})
		e.must(err)
		e.diff(before, e.members, 2, "members")
		if len(ids) != 2 {
			t.Errorf("members = %v", ids)
		}
	})
}

// ---------------------------------------------------------------- group_test.rb

func TestGroupRolesShouldBeGivenToAddedUser(t *testing.T) {
	withFixtures(t, func(e *env) {
		e.createMember(1, 11, 1, 2)
		e.must(repository.AddUserToGroup(e.ctx, e.d, 11, 9))
		if !e.memberOf(9, 1) {
			t.Error("user 9 should be a member of project 1")
		}
	})
}

func TestNewRolesShouldBeGivenToExistingUser(t *testing.T) {
	withFixtures(t, func(e *env) {
		e.must(repository.AddUserToGroup(e.ctx, e.d, 11, 9))
		e.createMember(1, 11, 1, 2)
		if !e.memberOf(9, 1) {
			t.Error("user 9 should be a member of project 1")
		}
	})
}

func TestUserRolesShouldBeUpdatedWhenUpdatingUserIDs(t *testing.T) {
	withFixtures(t, func(e *env) {
		e.createMember(1, 11, 1, 2)
		e.must(repository.SetGroupUsers(e.ctx, e.d, 11, []int64{9}))
		if !e.memberOf(9, 1) {
			t.Error("user 9 should be a member")
		}
		e.must(repository.SetGroupUsers(e.ctx, e.d, 11, []int64{1}))
		if e.memberOf(9, 1) {
			t.Error("user 9 should not be a member any more")
		}
	})
}

func TestUserRolesShouldBeUpdatedWhenUpdatingGroupRoles(t *testing.T) {
	withFixtures(t, func(e *env) {
		e.must(repository.AddUserToGroup(e.ctx, e.d, 11, 9))
		m := e.createMember(1, 11, 1)
		if got := e.rolesForProject(9, 1); !slices.Equal(got, []int64{1}) {
			t.Errorf("roles = %v", got)
		}
		for _, roles := range [][]int64{{1, 2}, {2}, {1}} {
			e.must(repository.SetMemberRoles(e.ctx, e.d, m, roles))
			if got := e.rolesForProject(9, 1); !slices.Equal(got, roles) {
				t.Errorf("after %v: roles = %v", roles, got)
			}
		}
	})
}

func TestUserMembershipsShouldBeRemovedWhenRemovingGroupMembership(t *testing.T) {
	withFixtures(t, func(e *env) {
		if !e.memberOf(8, 5) {
			t.Fatal("user 8 should be a member of project 5")
		}
		e.must(repository.DestroyMember(e.ctx, e.d, e.member(5, 10).ID))
		if e.memberOf(8, 5) {
			t.Error("user 8 should not be a member of project 5")
		}
	})
}

func TestUserRolesShouldBeRemovedWhenRemovingUserFromGroup(t *testing.T) {
	withFixtures(t, func(e *env) {
		groups, err := repository.UserGroupIDs(e.ctx, e.d, 8)
		e.must(err)
		for _, g := range groups {
			e.must(repository.RemoveUserFromGroup(e.ctx, e.d, g, 8))
		}
		if e.memberOf(8, 5) {
			t.Error("user 8 should not be a member of project 5")
		}
	})
}

func TestGroupDestroyShouldUnassignAndUnwatchIssues(t *testing.T) {
	withFixtures(t, func(e *env) {
		_, err := e.d.Exec(e.ctx, `UPDATE issues SET assigned_to_id = 10 WHERE id = 1`)
		e.must(err)
		_, err = e.d.Exec(e.ctx, `INSERT INTO watchers (watchable_kind, watchable_id, principal_id) VALUES ('issue', 2, 10)`)
		e.must(err)
		e.must(repository.DestroyGroup(e.ctx, e.d, 10))
		if e.count(`SELECT COUNT(*) FROM principals WHERE id = 10`) != 0 {
			t.Error("group not destroyed")
		}
		if e.count(`SELECT COUNT(*) FROM issues WHERE id = 1 AND assigned_to_id IS NULL`) != 1 {
			t.Error("issue still assigned")
		}
		if e.count(`SELECT COUNT(*) FROM watchers WHERE principal_id = 10`) != 0 {
			t.Error("watcher remains")
		}
		// グループ由来のユーザのメンバーシップも消える
		if e.memberOf(8, 5) {
			t.Error("user 8 should not be a member of project 5")
		}
	})
}

func TestBuiltinGroupsShouldBeCreatedIfMissing(t *testing.T) {
	withFixtures(t, func(e *env) {
		_, err := e.d.Exec(e.ctx, `DELETE FROM principals WHERE kind IN ('group_anonymous', 'group_non_member')`)
		e.must(err)
		before := e.count(`SELECT COUNT(*) FROM principals WHERE kind LIKE 'group%'`)
		a, err := repository.BuiltinGroup(e.ctx, e.d, domain.KindGroupAnonymous)
		e.must(err)
		n, err := repository.BuiltinGroup(e.ctx, e.d, domain.KindGroupNonMember)
		e.must(err)
		if a.Kind != domain.KindGroupAnonymous || n.Kind != domain.KindGroupNonMember || !a.Builtin() {
			t.Errorf("kinds = %s %s", a.Kind, n.Kind)
		}
		if after := e.count(`SELECT COUNT(*) FROM principals WHERE kind LIKE 'group%'`); after != before+2 {
			t.Errorf("groups %d -> %d", before, after)
		}
	})
}

func TestBuiltinGroupShouldNotAcceptUsers(t *testing.T) {
	withFixtures(t, func(e *env) {
		g, err := repository.BuiltinGroup(e.ctx, e.d, domain.KindGroupAnonymous)
		e.must(err)
		if err := repository.AddUserToGroup(e.ctx, e.d, g.ID, 1); err == nil {
			t.Error("builtin group accepted a user")
		}
		if e.count(`SELECT COUNT(*) FROM group_users WHERE group_id = ?`, g.ID) != 0 {
			t.Error("user added")
		}
		if err := repository.DestroyGroup(e.ctx, e.d, g.ID); err == nil {
			t.Error("builtin group destroyed")
		}
	})
}

func TestListGroupsSorted(t *testing.T) {
	withFixtures(t, func(e *env) {
		gs, err := repository.ListGroups(e.ctx, e.d, true)
		e.must(err)
		var ids []int64
		for _, g := range gs {
			ids = append(ids, g.ID)
		}
		// Group.sorted: type (Group < GroupAnonymous < GroupNonMember), 名前
		if !slices.Equal(ids, []int64{10, 11, 13, 12}) {
			t.Errorf("groups = %v", ids)
		}
		gs, err = repository.ListGroups(e.ctx, e.d, false)
		e.must(err)
		if len(gs) != 2 {
			t.Errorf("givable groups = %d", len(gs))
		}
	})
}

// ---------------------------------------------------------------- project_members_inheritance_test.rb

// withInheritance は setup: 親プロジェクトとそのメンバー (user 2, roles [1, 2])。
func withInheritance(t *testing.T, fn func(e *env, parent int64, member int64)) {
	withFixtures(t, func(e *env) {
		parent := e.generateProject(0, false)
		m := e.createMember(parent, 2, 1, 2)
		fn(e, parent, m)
	})
}

func assertSameMembership(e *env, got *domain.Member, principal int64, roles []int64) {
	e.t.Helper()
	if got == nil {
		e.t.Fatal("member not found")
	}
	if got.PrincipalID != principal || !slices.Equal(roleSet(got), roles) {
		e.t.Errorf("member = principal %d roles %v, want %d %v", got.PrincipalID, roleSet(got), principal, roles)
	}
}

func TestProjectMembersInheritance(t *testing.T) {
	t.Run("created with inherit_members disabled should not inherit", func(t *testing.T) {
		withInheritance(t, func(e *env, parent, _ int64) {
			before := e.members()
			p := e.generateProject(parent, false)
			e.diff(before, e.members, 0, "members")
			if len(e.projectMembers(p)) != 0 {
				t.Error("memberships exist")
			}
		})
	})
	t.Run("created with inherit_members should inherit", func(t *testing.T) {
		withInheritance(t, func(e *env, parent, _ int64) {
			before := e.members()
			p := e.generateProject(parent, true)
			e.diff(before, e.members, 1, "members")
			ms := e.projectMembers(p)
			if len(ms) != 1 {
				t.Fatalf("memberships = %d", len(ms))
			}
			assertSameMembership(e, ms[0], 2, []int64{1, 2})
		})
	})
	t.Run("turning on inherit_members should inherit", func(t *testing.T) {
		withInheritance(t, func(e *env, parent, _ int64) {
			p := e.generateProject(parent, false)
			before := e.members()
			e.setInherit(p, true)
			e.diff(before, e.members, 1, "members")
			assertSameMembership(e, e.member(p, 2), 2, []int64{1, 2})
		})
	})
	t.Run("turning off inherit_members should remove inherited members", func(t *testing.T) {
		withInheritance(t, func(e *env, parent, _ int64) {
			p := e.generateProject(parent, true)
			before := e.members()
			e.setInherit(p, false)
			e.diff(before, e.members, -1, "members")
			if len(e.projectMembers(p)) != 0 {
				t.Error("memberships remain")
			}
		})
	})
	t.Run("moving a root project under a parent should inherit", func(t *testing.T) {
		withInheritance(t, func(e *env, parent, _ int64) {
			p := e.generateProject(0, true)
			before := e.members()
			e.must(e.setParent(p, parent))
			e.diff(before, e.members, 1, "members")
			assertSameMembership(e, e.member(p, 2), 2, []int64{1, 2})
		})
	})
	t.Run("moving a subproject as root should loose inherited members", func(t *testing.T) {
		withInheritance(t, func(e *env, parent, _ int64) {
			p := e.generateProject(parent, true)
			before := e.members()
			e.must(e.setParent(p, 0))
			e.diff(before, e.members, -1, "members")
			if len(e.projectMembers(p)) != 0 {
				t.Error("memberships remain")
			}
		})
	})
	t.Run("moving a subproject to another parent should change inherited members", func(t *testing.T) {
		withInheritance(t, func(e *env, parent, _ int64) {
			other := e.generateProject(0, false)
			e.createMember(other, 4, 3)
			p := e.generateProject(parent, true)
			e.must(e.setParent(p, other))
			ms := e.projectMembers(p)
			if len(ms) != 1 {
				t.Fatalf("memberships = %d", len(ms))
			}
			assertSameMembership(e, ms[0], 4, []int64{3})
		})
	})
	t.Run("inheritance should propagate to subprojects", func(t *testing.T) {
		withInheritance(t, func(e *env, parent, _ int64) {
			p := e.generateProject(parent, false)
			sub := e.generateProject(p, true)
			before := e.members()
			e.setInherit(p, true)
			e.diff(before, e.members, 2, "members")
			if len(e.projectMembers(p)) != 1 || len(e.projectMembers(sub)) != 1 {
				t.Fatal("memberships")
			}
			assertSameMembership(e, e.projectMembers(sub)[0], 2, []int64{1, 2})
		})
	})
	t.Run("inheritance removal should propagate to subprojects", func(t *testing.T) {
		withInheritance(t, func(e *env, parent, _ int64) {
			p := e.generateProject(parent, true)
			sub := e.generateProject(p, true)
			before := e.members()
			e.setInherit(p, false)
			e.diff(before, e.members, -2, "members")
			if len(e.projectMembers(p)) != 0 || len(e.projectMembers(sub)) != 0 {
				t.Error("memberships remain")
			}
		})
	})
	t.Run("adding a member should propagate", func(t *testing.T) {
		withInheritance(t, func(e *env, parent, _ int64) {
			p := e.generateProject(parent, true)
			before := e.members()
			e.createMember(parent, 4, 1, 3)
			e.diff(before, e.members, 2, "members")
			assertSameMembership(e, e.member(p, 4), 4, []int64{1, 3})
		})
	})
	t.Run("adding a member should not propagate if child does not inherit", func(t *testing.T) {
		withInheritance(t, func(e *env, parent, _ int64) {
			p := e.generateProject(parent, false)
			before := e.members()
			e.createMember(parent, 4, 1, 3)
			e.diff(before, e.members, 1, "members")
			if e.member(p, 4) != nil {
				t.Error("member propagated")
			}
		})
	})
	t.Run("removing a member should propagate", func(t *testing.T) {
		withInheritance(t, func(e *env, parent, member int64) {
			p := e.generateProject(parent, true)
			before := e.members()
			e.must(repository.DestroyMember(e.ctx, e.d, member))
			e.diff(before, e.members, -2, "members")
			if len(e.projectMembers(p)) != 0 {
				t.Error("memberships remain")
			}
		})
	})
	t.Run("adding a group member should propagate with its users", func(t *testing.T) {
		withInheritance(t, func(e *env, parent, _ int64) {
			p := e.generateProject(parent, true)
			group := testfixtures.GenerateGroup(t, e.d)
			e.must(repository.AddUserToGroup(e.ctx, e.d, group, 4))
			m, mr := e.members(), e.memberRoles()
			e.createMember(parent, group, 1, 3)
			e.diff(m, e.members, 4, "members")
			e.diff(mr, e.memberRoles, 8, "member_roles")
			assertSameMembership(e, e.member(p, group), group, []int64{1, 3})
			assertSameMembership(e, e.member(p, 4), 4, []int64{1, 3})
		})
	})
	t.Run("removing a group member should propagate", func(t *testing.T) {
		withInheritance(t, func(e *env, parent, _ int64) {
			p := e.generateProject(parent, true)
			group := testfixtures.GenerateGroup(t, e.d)
			e.must(repository.AddUserToGroup(e.ctx, e.d, group, 4))
			gm := e.createMember(parent, group, 1, 3)
			m, mr := e.members(), e.memberRoles()
			e.must(repository.DestroyMember(e.ctx, e.d, gm))
			e.diff(m, e.members, -4, "members")
			e.diff(mr, e.memberRoles, -8, "member_roles")
			if e.member(p, group) != nil || e.member(p, 4) != nil {
				t.Error("inherited memberships remain")
			}
		})
	})
	t.Run("adding user who is already a member to parent project should merge roles", func(t *testing.T) {
		withInheritance(t, func(e *env, parent, _ int64) {
			p := e.generateProject(parent, true)
			e.createMember(p, 4, 1, 2)
			before := e.members()
			e.createMember(parent, 4, 1, 3)
			e.diff(before, e.members, 1, "members")
			if got := roleSet(e.member(p, 4)); !slices.Equal(got, []int64{1, 2, 3}) {
				t.Errorf("roles = %v", got)
			}
		})
	})
	t.Run("turning on inheritance with user who is already a member should merge roles", func(t *testing.T) {
		withInheritance(t, func(e *env, parent, _ int64) {
			p := e.generateProject(parent, false)
			e.createMember(p, 2, 1, 3)
			before := e.members()
			e.setInherit(p, true)
			e.diff(before, e.members, 0, "members")
			if got := roleSet(e.member(p, 2)); !slices.Equal(got, []int64{1, 2, 3}) {
				t.Errorf("roles = %v", got)
			}
		})
	})
}
