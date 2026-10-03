package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

type roleRow struct {
	ID                         int64         `db:"id"`
	Name                       string        `db:"name"`
	Position                   int           `db:"position"`
	Assignable                 bool          `db:"assignable"`
	Builtin                    int           `db:"builtin"`
	IssuesVisibility           string        `db:"issues_visibility"`
	UsersVisibility            string        `db:"users_visibility"`
	TimeEntriesVisibility      string        `db:"time_entries_visibility"`
	AllRolesManaged            bool          `db:"all_roles_managed"`
	DefaultTimeEntryActivityID sql.NullInt64 `db:"default_time_entry_activity_id"`
}

const roleCols = `id, name, position, assignable, builtin, issues_visibility, users_visibility,
  time_entries_visibility, all_roles_managed, default_time_entry_activity_id`

// loadRoles は roles を where 条件で読み込み、権限・トラッカー制限・管理対象ロールを付ける。
// 並びは Role.sorted (builtin, position, id)。
func loadRoles(ctx context.Context, q db.Queryer, where string, args ...any) ([]*domain.Role, error) {
	if where == "" {
		where = "1=1"
	}
	var rows []roleRow
	if err := q.Select(ctx, &rows, `SELECT `+roleCols+` FROM roles WHERE `+where+` ORDER BY builtin, position, id`, args...); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	out := make([]*domain.Role, len(rows))
	byID := make(map[int64]*domain.Role, len(rows))
	ids := make([]int64, len(rows))
	for i, r := range rows {
		role := &domain.Role{
			ID: r.ID, Name: r.Name, Position: r.Position, Assignable: r.Assignable, Builtin: r.Builtin,
			IssuesVisibility: r.IssuesVisibility, UsersVisibility: r.UsersVisibility,
			TimeEntriesVisibility: r.TimeEntriesVisibility, AllRolesManaged: r.AllRolesManaged,
			Permissions: []string{},
		}
		if r.DefaultTimeEntryActivityID.Valid {
			v := r.DefaultTimeEntryActivityID.Int64
			role.DefaultTimeEntryActivityID = &v
		}
		out[i] = role
		byID[r.ID] = role
		ids[i] = r.ID
	}
	for _, chunk := range chunkIDs(ids) {
		query, a, err := db.In(`SELECT role_id, permission, all_trackers FROM role_permissions WHERE role_id IN (?) ORDER BY role_id, position, permission`, chunk)
		if err != nil {
			return nil, err
		}
		var perms []struct {
			RoleID      int64  `db:"role_id"`
			Permission  string `db:"permission"`
			AllTrackers bool   `db:"all_trackers"`
		}
		if err := q.Select(ctx, &perms, query, a...); err != nil {
			return nil, err
		}
		for _, p := range perms {
			r := byID[p.RoleID]
			r.Permissions = append(r.Permissions, p.Permission)
			if !p.AllTrackers {
				if r.RestrictedTrackers == nil {
					r.RestrictedTrackers = map[string][]int64{}
				}
				r.RestrictedTrackers[p.Permission] = []int64{}
			}
		}
		query, a, err = db.In(`SELECT role_id, permission, tracker_id FROM role_permission_trackers WHERE role_id IN (?) ORDER BY role_id, permission, tracker_id`, chunk)
		if err != nil {
			return nil, err
		}
		var trs []struct {
			RoleID     int64  `db:"role_id"`
			Permission string `db:"permission"`
			TrackerID  int64  `db:"tracker_id"`
		}
		if err := q.Select(ctx, &trs, query, a...); err != nil {
			return nil, err
		}
		for _, t := range trs {
			r := byID[t.RoleID]
			if _, ok := r.RestrictedTrackers[t.Permission]; ok {
				r.RestrictedTrackers[t.Permission] = append(r.RestrictedTrackers[t.Permission], t.TrackerID)
			}
		}
		query, a, err = db.In(`SELECT role_id, managed_role_id FROM roles_managed_roles WHERE role_id IN (?) ORDER BY role_id, managed_role_id`, chunk)
		if err != nil {
			return nil, err
		}
		var mrs []struct {
			RoleID        int64 `db:"role_id"`
			ManagedRoleID int64 `db:"managed_role_id"`
		}
		if err := q.Select(ctx, &mrs, query, a...); err != nil {
			return nil, err
		}
		for _, m := range mrs {
			r := byID[m.RoleID]
			r.ManagedRoleIDs = append(r.ManagedRoleIDs, m.ManagedRoleID)
		}
	}
	return out, nil
}

// GetRole は id のロールを返す。
func GetRole(ctx context.Context, q db.Queryer, id int64) (*domain.Role, error) {
	rs, err := loadRoles(ctx, q, `id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(rs) == 0 {
		return nil, ErrNotFound
	}
	return rs[0], nil
}

// RolesByIDs は指定 id のロールを Role.sorted の順で返す (存在しない id は無視)。
func RolesByIDs(ctx context.Context, q db.Queryer, ids []int64) ([]*domain.Role, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []*domain.Role
	for _, chunk := range chunkIDs(uniqIDs(ids)) {
		where, args, err := db.In(`id IN (?)`, chunk)
		if err != nil {
			return nil, err
		}
		rs, err := loadRoles(ctx, q, where, args...)
		if err != nil {
			return nil, err
		}
		out = append(out, rs...)
	}
	domain.SortRoles(out)
	return out, nil
}

// ListRoles は全ロールを Role.sorted (builtin, position) の順で返す。
func ListRoles(ctx context.Context, q db.Queryer) ([]*domain.Role, error) {
	return loadRoles(ctx, q, "")
}

// GivableRoles は Role.givable (builtin = 0, position 順) を返す。
func GivableRoles(ctx context.Context, q db.Queryer) ([]*domain.Role, error) {
	return loadRoles(ctx, q, `builtin = 0`)
}

// BuiltinRole は Role.non_member / Role.anonymous の移植。無ければ作成する。
func BuiltinRole(ctx context.Context, q db.Queryer, builtin int) (*domain.Role, error) {
	var name string
	switch builtin {
	case domain.RoleBuiltinNonMember:
		name = "Non member"
	case domain.RoleBuiltinAnonymous:
		name = "Anonymous"
	default:
		return nil, fmt.Errorf("repository: invalid builtin role %d", builtin)
	}
	rs, err := loadRoles(ctx, q, `builtin = ?`, builtin)
	if err != nil {
		return nil, err
	}
	if len(rs) > 0 {
		return rs[0], nil
	}
	// acts_as_positioned (scope: builtin) で末尾に追加
	var pos int
	if err := q.Get(ctx, &pos, `SELECT COALESCE(MAX(position), 0) + 1 FROM roles WHERE builtin = ?`, builtin); err != nil {
		return nil, err
	}
	id, err := q.InsertReturningID(ctx, `INSERT INTO roles (name, position, builtin) VALUES (?, ?, ?)`, name, pos, builtin)
	if err != nil {
		return nil, fmt.Errorf("repository: unable to create the %s role: %w", name, err)
	}
	return GetRole(ctx, q, id)
}

// SaveRole はロールの行と権限・トラッカー制限・管理対象ロールを保存する。
// r.ID == 0 なら作成して id を設定する。position が 0 なら同じ builtin 内の末尾。
func SaveRole(ctx context.Context, q db.Queryer, r *domain.Role) error {
	if r.IssuesVisibility == "" {
		r.IssuesVisibility = domain.IssuesVisibilityDefault
	}
	if r.UsersVisibility == "" {
		r.UsersVisibility = domain.UsersVisibilityMembersOfVisibleProjects
	}
	if r.TimeEntriesVisibility == "" {
		r.TimeEntriesVisibility = domain.TimeEntriesVisibilityAll
	}
	if r.Position == 0 {
		if err := q.Get(ctx, &r.Position, `SELECT COALESCE(MAX(position), 0) + 1 FROM roles WHERE builtin = ?`, r.Builtin); err != nil {
			return err
		}
	}
	if r.ID == 0 {
		id, err := q.InsertReturningID(ctx, `INSERT INTO roles (name, position, assignable, builtin, issues_visibility, users_visibility,
  time_entries_visibility, all_roles_managed, default_time_entry_activity_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.Name, r.Position, r.Assignable, r.Builtin, r.IssuesVisibility, r.UsersVisibility,
			r.TimeEntriesVisibility, r.AllRolesManaged, r.DefaultTimeEntryActivityID)
		if err != nil {
			return err
		}
		r.ID = id
	} else {
		if _, err := q.Exec(ctx, `UPDATE roles SET name = ?, position = ?, assignable = ?, issues_visibility = ?, users_visibility = ?,
  time_entries_visibility = ?, all_roles_managed = ?, default_time_entry_activity_id = ? WHERE id = ?`,
			r.Name, r.Position, r.Assignable, r.IssuesVisibility, r.UsersVisibility,
			r.TimeEntriesVisibility, r.AllRolesManaged, r.DefaultTimeEntryActivityID, r.ID); err != nil {
			return err
		}
	}
	if err := SetRolePermissions(ctx, q, r.ID, r.Permissions, r.RestrictedTrackers); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `DELETE FROM roles_managed_roles WHERE role_id = ?`, r.ID); err != nil {
		return err
	}
	for _, m := range uniqIDs(r.ManagedRoleIDs) {
		if _, err := q.Exec(ctx, `INSERT INTO roles_managed_roles (role_id, managed_role_id) VALUES (?, ?)`, r.ID, m); err != nil {
			return err
		}
	}
	return nil
}

// SetRolePermissions はロールの権限を置き換える (Role#permissions= + set_permission_trackers)。
// restricted に含まれる権限は all_trackers=false で、値のトラッカーのみ許可する。
// 空文字・重複は除く (Role#permissions=)。
func SetRolePermissions(ctx context.Context, q db.Queryer, roleID int64, perms []string, restricted map[string][]int64) error {
	if _, err := q.Exec(ctx, `DELETE FROM role_permissions WHERE role_id = ?`, roleID); err != nil {
		return err
	}
	var seen []string
	for _, p := range perms {
		if p == "" || slices.Contains(seen, p) {
			continue
		}
		seen = append(seen, p)
		trackers, isRestricted := restricted[p]
		// position は渡された順（Role#permissions= はフォームの送信順のまま YAML 配列に保存する）
		if _, err := q.Exec(ctx, `INSERT INTO role_permissions (role_id, permission, all_trackers, position) VALUES (?, ?, ?, ?)`, roleID, p, !isRestricted, len(seen)); err != nil {
			return err
		}
		for _, t := range uniqIDs(trackers) {
			if _, err := q.Exec(ctx, `INSERT INTO role_permission_trackers (role_id, permission, tracker_id) VALUES (?, ?, ?)`, roleID, p, t); err != nil {
				return err
			}
		}
	}
	return nil
}

// ErrRoleNotDeletable は Role#check_deletable (使用中または組込ロール)。
var ErrRoleNotDeletable = errors.New("repository: role cannot be deleted")

// DestroyRole はロールを削除する。メンバーが使用中、または組込ロールなら ErrRoleNotDeletable。
func DestroyRole(ctx context.Context, q db.Queryer, id int64) error {
	r, err := GetRole(ctx, q, id)
	if err != nil {
		return err
	}
	if r.IsBuiltin() {
		return ErrRoleNotDeletable
	}
	var n int
	if err := q.Get(ctx, &n, `SELECT COUNT(*) FROM member_roles WHERE role_id = ?`, id); err != nil {
		return err
	}
	if n > 0 {
		return ErrRoleNotDeletable
	}
	_, err = q.Exec(ctx, `DELETE FROM roles WHERE id = ?`, id)
	return err
}
