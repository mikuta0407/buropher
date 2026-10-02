package domain

import (
	"slices"
	"time"
)

// Member は members 行 (Redmine Member)。principal はユーザ・グループ・組込グループのいずれか。
type Member struct {
	ID          int64
	ProjectID   int64
	PrincipalID int64
	CreatedAt   time.Time
	// MemberRoles はこのメンバーの member_roles (継承行を含む)。
	MemberRoles []MemberRole
}

// MemberRole は member_roles 行。InheritedFrom は派生元の member_roles.id
// (グループのメンバーシップ、または inherit_members な親プロジェクトのメンバーシップ)。
type MemberRole struct {
	ID            int64
	MemberID      int64
	RoleID        int64
	InheritedFrom *int64
}

// Inherited は MemberRole#inherited?。
func (mr *MemberRole) Inherited() bool { return mr.InheritedFrom != nil }

// RoleIDs は重複を除いたロール ID (Member#role_ids, roles は distinct)。出現順。
func (m *Member) RoleIDs() []int64 {
	var ids []int64
	for _, mr := range m.MemberRoles {
		if !slices.Contains(ids, mr.RoleID) {
			ids = append(ids, mr.RoleID)
		}
	}
	return ids
}

// AnyInheritedRole は Member#any_inherited_role?。
func (m *Member) AnyInheritedRole() bool {
	for _, mr := range m.MemberRoles {
		if mr.Inherited() {
			return true
		}
	}
	return false
}

// HasInheritedRole は Member#has_inherited_role?(role)。
func (m *Member) HasInheritedRole(roleID int64) bool {
	for _, mr := range m.MemberRoles {
		if mr.RoleID == roleID && mr.Inherited() {
			return true
		}
	}
	return false
}

// Issue はチケット (権限・可視性判定に必要な列のみ)。
type Issue struct {
	ID           int64
	ProjectID    int64
	TrackerID    int64
	StatusID     int64
	AuthorID     int64
	AssignedToID *int64
	IsPrivate    bool
}
