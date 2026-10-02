package domain

import (
	"slices"

	"github.com/mikuta0407/buropher/internal/permission"
)

// ロールの builtin 値 (Role::BUILTIN_*)。
const (
	RoleBuiltinNone      = 0
	RoleBuiltinNonMember = 1
	RoleBuiltinAnonymous = 2
)

// 可視性オプションの値。
const (
	IssuesVisibilityAll     = "all"
	IssuesVisibilityDefault = "default"
	IssuesVisibilityOwn     = "own"

	UsersVisibilityAll                      = "all"
	UsersVisibilityMembersOfVisibleProjects = "members_of_visible_projects"

	TimeEntriesVisibilityAll = "all"
	TimeEntriesVisibilityOwn = "own"
)

// TrackerPermissions はトラッカー単位で制限できる権限
// (app/views/roles/_form.html.erb の permissions_all_trackers 対象)。
var TrackerPermissions = []string{"view_issues", "add_issues", "edit_issues", "add_issue_notes", "delete_issues"}

// Role は roles 行 + role_permissions + role_permission_trackers + roles_managed_roles。
type Role struct {
	ID                         int64
	Name                       string // DB 上の名前 (組込ロールの表示名はビュー層で翻訳する)
	Position                   int
	Assignable                 bool
	Builtin                    int
	IssuesVisibility           string
	UsersVisibility            string
	TimeEntriesVisibility      string
	AllRolesManaged            bool
	DefaultTimeEntryActivityID *int64

	// Permissions は付与された権限名 (Role#permissions)。順序は保存順。
	Permissions []string
	// RestrictedTrackers はトラッカー限定された権限 (permissions_all_trackers[perm] == '0') と
	// その許可トラッカー ID。キーが無い権限は全トラッカー。
	RestrictedTrackers map[string][]int64
	// ManagedRoleIDs は roles_managed_roles の managed_role_id。
	ManagedRoleIDs []int64
}

// IsBuiltin は Role#builtin?。
func (r *Role) IsBuiltin() bool { return r.Builtin != RoleBuiltinNone }

// IsAnonymous は Role#anonymous?。
func (r *Role) IsAnonymous() bool { return r.Builtin == RoleBuiltinAnonymous }

// IsMember は Role#member? (組込ロールでなければ true)。
func (r *Role) IsMember() bool { return !r.IsBuiltin() }

// Givable は Role.givable に含まれるなら true。
func (r *Role) Givable() bool { return r.Builtin == RoleBuiltinNone }

// HasPermission は Role#has_permission? (public 権限は含まない)。
func (r *Role) HasPermission(perm string) bool { return slices.Contains(r.Permissions, perm) }

// ConsiderWorkflow は Role#consider_workflow?。
func (r *Role) ConsiderWorkflow() bool {
	return r.HasPermission("add_issues") || r.HasPermission("edit_issues")
}

// AllowedPermissions は Role#allowed_permissions(scope): 付与権限 + public 権限。
// scope が空でなければ scope との積集合。
func (r *Role) AllowedPermissions(scope []string) []string {
	out := slices.Clone(r.Permissions)
	for _, p := range permission.PublicPermissions() {
		out = append(out, p.Name)
	}
	if len(scope) > 0 {
		out = slices.DeleteFunc(out, func(p string) bool { return !slices.Contains(scope, p) })
	}
	return out
}

// AllowedTo は Role#allowed_to?(action, scope)。
// scope は OAuth スコープ (nil/空なら制限なし)。
func (r *Role) AllowedTo(a Action, scope []string) bool {
	perms := r.AllowedPermissions(scope)
	if a.IsPermission() {
		return slices.Contains(perms, a.Permission)
	}
	for _, p := range perms {
		if pd := permission.Get(p); pd != nil && pd.Allows(a.Controller, a.Action) {
			return true
		}
	}
	return false
}

// SetablePermissions は Role#setable_permissions。
func (r *Role) SetablePermissions() []*permission.Permission {
	var out []*permission.Permission
	for _, p := range permission.All() {
		if p.Public {
			continue
		}
		if r.Builtin == RoleBuiltinNonMember && p.RequireMember() {
			continue
		}
		if r.Builtin == RoleBuiltinAnonymous && p.RequireLoggedin() {
			continue
		}
		out = append(out, p)
	}
	return out
}

// PermissionsAllTrackers は Role#permissions_all_trackers?(permission)。
func (r *Role) PermissionsAllTrackers(perm string) bool {
	if !r.HasPermission(perm) {
		return false
	}
	_, restricted := r.RestrictedTrackers[perm]
	return !restricted
}

// PermissionsTrackerIDs は Role#permissions_tracker_ids(permission)。
func (r *Role) PermissionsTrackerIDs(perm string) []int64 { return r.RestrictedTrackers[perm] }

// PermissionsTrackerIDsInclude は Role#permissions_tracker_ids?(permission, tracker_id)。
func (r *Role) PermissionsTrackerIDsInclude(perm string, trackerID int64) bool {
	if !r.HasPermission(perm) {
		return false
	}
	return slices.Contains(r.RestrictedTrackers[perm], trackerID)
}

// PermissionsTracker は Role#permissions_tracker?(permission, tracker)。
func (r *Role) PermissionsTracker(perm string, trackerID int64) bool {
	return r.PermissionsAllTrackers(perm) || r.PermissionsTrackerIDsInclude(perm, trackerID)
}

// Compare は Role#<=> (builtin, position の順)。
func (r *Role) Compare(o *Role) int {
	if r.Builtin != o.Builtin {
		return r.Builtin - o.Builtin
	}
	return r.Position - o.Position
}

// SortRoles はロールを Role#<=> の順に並べる (Role.sorted と同じ)。
func SortRoles(rs []*Role) {
	slices.SortStableFunc(rs, func(a, b *Role) int { return a.Compare(b) })
}
