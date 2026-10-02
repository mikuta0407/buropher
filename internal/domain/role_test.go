package domain

// test/unit/role_test.rb の移植 (DB を要しない部分)。

import (
	"slices"
	"testing"
)

func newRole(perms ...string) *Role { return &Role{Name: "Test", Permissions: perms} }

// setPermissionTrackers は Role#set_permission_trackers (nil = :all)。
func setPermissionTrackers(r *Role, perm string, trackers []int64) {
	if r.RestrictedTrackers == nil {
		r.RestrictedTrackers = map[string][]int64{}
	}
	if trackers == nil {
		delete(r.RestrictedTrackers, perm)
		return
	}
	r.RestrictedTrackers[perm] = trackers
}

func removePermission(r *Role, perm string) {
	r.Permissions = slices.DeleteFunc(r.Permissions, func(p string) bool { return p == perm })
}

func TestRoleHasPermission(t *testing.T) {
	r := newRole("view_issues", "edit_issues")
	if !r.HasPermission("view_issues") || r.HasPermission("delete_issues") {
		t.Error("has_permission? mismatch")
	}
	if newRole().HasPermission("delete_issues") {
		t.Error("role without permissions")
	}
	// public 権限は has_permission? には含まれないが allowed_to? には含まれる
	if r.HasPermission("view_project") || !r.AllowedTo(Perm("view_project"), nil) {
		t.Error("public permission handling")
	}
}

func TestRolePermissionsAllTrackers(t *testing.T) {
	r := newRole("view_issues")
	if !r.PermissionsAllTrackers("view_issues") || r.PermissionsAllTrackers("edit_issues") {
		t.Error("initial")
	}
	setPermissionTrackers(r, "view_issues", []int64{1})
	setPermissionTrackers(r, "edit_issues", []int64{1})
	if r.PermissionsAllTrackers("view_issues") || r.PermissionsAllTrackers("edit_issues") {
		t.Error("restricted")
	}
	setPermissionTrackers(r, "view_issues", nil)
	setPermissionTrackers(r, "edit_issues", nil)
	if !r.PermissionsAllTrackers("view_issues") || r.PermissionsAllTrackers("edit_issues") {
		t.Error("all")
	}
	// test_permissions_all_trackers_considers_base_permission
	r = newRole("view_issues")
	removePermission(r, "view_issues")
	if r.PermissionsAllTrackers("view_issues") {
		t.Error("base permission")
	}
}

func TestRolePermissionsTrackerIDs(t *testing.T) {
	r := newRole("view_issues")
	if r.PermissionsTrackerIDsInclude("view_issues", 1) || r.PermissionsTrackerIDsInclude("edit_issues", 1) {
		t.Error("initial")
	}
	setPermissionTrackers(r, "view_issues", []int64{1, 2, 3})
	setPermissionTrackers(r, "edit_issues", []int64{1, 2, 3})
	if !r.PermissionsTrackerIDsInclude("view_issues", 1) || r.PermissionsTrackerIDsInclude("edit_issues", 1) {
		t.Error("restricted")
	}
	removePermission(r, "view_issues")
	if r.PermissionsTrackerIDsInclude("view_issues", 1) {
		t.Error("base permission")
	}
	// test_permissions_tracker_considers_base_permission
	r = newRole("edit_isues")
	setPermissionTrackers(r, "view_issues", []int64{1, 2, 3})
	if r.PermissionsTrackerIDsInclude("view_issues", 1) {
		t.Error("typo permission")
	}
}

func TestRolePermissionsTracker(t *testing.T) {
	r := newRole("view_issues")
	if !r.PermissionsTracker("view_issues", 1) || r.PermissionsTracker("edit_issues", 1) {
		t.Error("initial")
	}
	setPermissionTrackers(r, "view_issues", []int64{1})
	setPermissionTrackers(r, "edit_issues", []int64{1})
	if !r.PermissionsTracker("view_issues", 1) || r.PermissionsTracker("edit_issues", 1) {
		t.Error("tracker 1")
	}
	setPermissionTrackers(r, "view_issues", []int64{2})
	setPermissionTrackers(r, "edit_issues", []int64{2})
	if r.PermissionsTracker("view_issues", 1) || r.PermissionsTracker("edit_issues", 1) {
		t.Error("tracker 2")
	}
	setPermissionTrackers(r, "view_issues", nil)
	setPermissionTrackers(r, "edit_issues", nil)
	if !r.PermissionsTracker("view_issues", 1) || r.PermissionsTracker("edit_issues", 1) {
		t.Error("all")
	}
}

func TestRoleAllowedTo(t *testing.T) {
	r := newRole("view_issues")
	if !r.AllowedTo(Perm("view_issues"), nil) || r.AllowedTo(Perm("add_issues"), nil) {
		t.Error("symbol")
	}
	if !r.AllowedTo(ControllerAction("issues", "show"), nil) || r.AllowedTo(ControllerAction("issues", "create"), nil) {
		t.Error("hash")
	}
	r = newRole("view_issues", "delete_issues")
	scope := []string{"view_issues", "add_issues"}
	if !r.AllowedTo(Perm("view_issues"), scope) || r.AllowedTo(Perm("add_issues"), scope) || r.AllowedTo(Perm("delete_issues"), scope) {
		t.Error("symbol and scope")
	}
	if !r.AllowedTo(ControllerAction("issues", "show"), scope) ||
		r.AllowedTo(ControllerAction("issues", "create"), scope) ||
		r.AllowedTo(ControllerAction("issues", "destroy"), scope) {
		t.Error("hash and scope")
	}
	// スコープが空なら制限なし (scope.present? が false)
	if !r.AllowedTo(Perm("delete_issues"), []string{}) {
		t.Error("empty scope")
	}
}

func TestRoleBuiltin(t *testing.T) {
	nm := &Role{Builtin: RoleBuiltinNonMember}
	an := &Role{Builtin: RoleBuiltinAnonymous}
	m := &Role{}
	if !nm.IsBuiltin() || nm.IsMember() || nm.IsAnonymous() || !an.IsAnonymous() || !m.IsMember() || !m.Givable() {
		t.Error("builtin predicates")
	}
	// setable_permissions: public を除き、非メンバーは member 必須、匿名は loggedin 必須の権限を除く
	has := func(r *Role, name string) bool {
		for _, p := range r.SetablePermissions() {
			if p.Name == name {
				return true
			}
		}
		return false
	}
	if has(m, "view_project") || !has(m, "edit_project") || !has(m, "add_project") {
		t.Error("member setable permissions")
	}
	if has(nm, "edit_project") || !has(nm, "add_project") {
		t.Error("non member setable permissions")
	}
	if has(an, "add_project") || has(an, "edit_project") || !has(an, "view_issues") {
		t.Error("anonymous setable permissions")
	}
}

func TestSortRoles(t *testing.T) {
	rs := []*Role{{ID: 5, Builtin: 2, Position: 1}, {ID: 3, Position: 3}, {ID: 4, Builtin: 1, Position: 1}, {ID: 1, Position: 1}}
	SortRoles(rs)
	var ids []int64
	for _, r := range rs {
		ids = append(ids, r.ID)
	}
	if !slices.Equal(ids, []int64{1, 3, 4, 5}) {
		t.Errorf("sorted = %v", ids)
	}
}

func TestProjectAllowsTo(t *testing.T) {
	p := &Project{Status: ProjectStatusActive, EnabledModuleNames: []string{"issue_tracking"}}
	if !p.AllowsTo(Perm("add_issues")) || p.AllowsTo(Perm("view_wiki_pages")) || !p.AllowsTo(Perm("edit_project")) {
		t.Error("modules")
	}
	if !p.AllowsTo(ControllerAction("issues", "index")) || p.AllowsTo(ControllerAction("wiki", "show")) {
		t.Error("actions")
	}
	if p.AllowsTo(Perm("no_such_permission")) {
		t.Error("unknown permission")
	}
	p.Status = ProjectStatusClosed
	if p.AllowsTo(Perm("add_issues")) || !p.AllowsTo(Perm("view_issues")) || p.AllowsTo(Perm("edit_project")) || !p.AllowsTo(Perm("view_project")) {
		t.Error("closed")
	}
	p.Status = ProjectStatusScheduledForDeletion
	if p.AllowsTo(Perm("add_issues")) || !p.AllowsTo(Perm("view_issues")) {
		t.Error("scheduled for deletion")
	}
	p.Status = ProjectStatusArchived
	if p.AllowsTo(Perm("view_project")) {
		t.Error("archived")
	}
}

func TestUserPredicates(t *testing.T) {
	anon := &User{Principal: Principal{Kind: KindAnonymousUser}, AdminFlag: true}
	if anon.Logged() || anon.IsAdmin() || anon.BuiltinRoleBuiltin() != RoleBuiltinAnonymous || anon.BuiltinGroupKind() != KindGroupAnonymous {
		t.Error("anonymous")
	}
	u := &User{Principal: Principal{Kind: KindUser}}
	if !u.Logged() || u.IsAdmin() || u.BuiltinRoleBuiltin() != RoleBuiltinNonMember || u.BuiltinGroupKind() != KindGroupNonMember {
		t.Error("user")
	}
	g := &Group{Principal: Principal{Kind: KindGroupNonMember}}
	if !g.Builtin() || g.Givable() || g.BuiltinType() != "non_member" {
		t.Error("builtin group")
	}
	for _, k := range []PrincipalKind{KindUser, KindAnonymousUser, KindGroup, KindGroupAnonymous, KindGroupNonMember} {
		if KindFromRedmineType(k.RedmineType()) != k {
			t.Errorf("round trip %s", k)
		}
	}
}
