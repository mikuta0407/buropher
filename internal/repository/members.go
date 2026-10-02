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

// メンバー操作の検証エラー (Redmine のバリデーションエラーに相当)。
var (
	// ErrMemberRoleEmpty は Member#validate_role (ロールが空) 。
	ErrMemberRoleEmpty = errors.New("repository: member must have at least one role")
	// ErrMemberTaken は Member の user_id/project_id 一意性違反。
	ErrMemberTaken = errors.New("repository: principal is already a member of the project")
	// ErrInvalidMemberRole は MemberRole#validate_role_member (組込ロールや存在しないロール)。
	ErrInvalidMemberRole = errors.New("repository: invalid role for a membership")
)

type memberRow struct {
	ID          int64   `db:"id"`
	ProjectID   int64   `db:"project_id"`
	PrincipalID int64   `db:"principal_id"`
	CreatedAt   db.Time `db:"created_at"`
}

type memberRoleRow struct {
	ID            int64         `db:"id"`
	MemberID      int64         `db:"member_id"`
	RoleID        int64         `db:"role_id"`
	InheritedFrom sql.NullInt64 `db:"inherited_from"`
}

func (r memberRoleRow) domain() domain.MemberRole {
	mr := domain.MemberRole{ID: r.ID, MemberID: r.MemberID, RoleID: r.RoleID}
	if r.InheritedFrom.Valid {
		v := r.InheritedFrom.Int64
		mr.InheritedFrom = &v
	}
	return mr
}

// loadMembers は members を条件 where (members を m として参照) で読み込み、member_roles を付ける。
func loadMembers(ctx context.Context, q db.Queryer, where string, args ...any) ([]*domain.Member, error) {
	var rows []memberRow
	if err := q.Select(ctx, &rows, `SELECT m.id, m.project_id, m.principal_id, m.created_at FROM members m
JOIN projects ON projects.id = m.project_id WHERE `+where+` ORDER BY m.id`, args...); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	out := make([]*domain.Member, len(rows))
	byID := make(map[int64]*domain.Member, len(rows))
	ids := make([]int64, len(rows))
	for i, r := range rows {
		out[i] = &domain.Member{ID: r.ID, ProjectID: r.ProjectID, PrincipalID: r.PrincipalID, CreatedAt: r.CreatedAt.Time}
		byID[r.ID] = out[i]
		ids[i] = r.ID
	}
	for _, chunk := range chunkIDs(ids) {
		query, a, err := db.In(`SELECT id, member_id, role_id, inherited_from FROM member_roles WHERE member_id IN (?) ORDER BY id`, chunk)
		if err != nil {
			return nil, err
		}
		var mrs []memberRoleRow
		if err := q.Select(ctx, &mrs, query, a...); err != nil {
			return nil, err
		}
		for _, mr := range mrs {
			m := byID[mr.MemberID]
			m.MemberRoles = append(m.MemberRoles, mr.domain())
		}
	}
	return out, nil
}

// chunkIDs は IN 句のパラメータ数上限を避けるため id を分割する。
func chunkIDs(ids []int64) [][]int64 {
	const n = 500
	var out [][]int64
	for len(ids) > n {
		out = append(out, ids[:n])
		ids = ids[n:]
	}
	if len(ids) > 0 {
		out = append(out, ids)
	}
	return out
}

func oneMember(ms []*domain.Member, err error) (*domain.Member, error) {
	if err != nil {
		return nil, err
	}
	if len(ms) == 0 {
		return nil, ErrNotFound
	}
	return ms[0], nil
}

// GetMember は id のメンバー (member_roles 付き) を返す。
func GetMember(ctx context.Context, q db.Queryer, id int64) (*domain.Member, error) {
	return oneMember(loadMembers(ctx, q, `m.id = ?`, id))
}

// FindMember は (project, principal) のメンバーを返す (プロジェクトのステータスを問わない)。
func FindMember(ctx context.Context, q db.Queryer, projectID, principalID int64) (*domain.Member, error) {
	return oneMember(loadMembers(ctx, q, `m.project_id = ? AND m.principal_id = ?`, projectID, principalID))
}

// Membership は User#membership(project) の移植: アーカイブされていないプロジェクトの
// メンバーシップを返す。無ければ (nil, nil)。匿名ユーザは呼び出し側で nil とすること。
func Membership(ctx context.Context, q db.Queryer, principalID, projectID int64) (*domain.Member, error) {
	m, err := oneMember(loadMembers(ctx, q, `m.principal_id = ? AND m.project_id = ? AND projects.status <> ?`,
		principalID, projectID, domain.ProjectStatusArchived))
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return m, err
}

// Memberships は Principal#memberships (アーカイブされていないプロジェクトのメンバーシップ) を返す。
func Memberships(ctx context.Context, q db.Queryer, principalID int64) ([]*domain.Member, error) {
	return membersOfPrincipal(ctx, q, principalID, true)
}

// membersOfPrincipal は Principal#members (excludeArchived=false) / #memberships (true)。
func membersOfPrincipal(ctx context.Context, q db.Queryer, principalID int64, excludeArchived bool) ([]*domain.Member, error) {
	if excludeArchived {
		return loadMembers(ctx, q, `m.principal_id = ? AND projects.status <> ?`, principalID, domain.ProjectStatusArchived)
	}
	return loadMembers(ctx, q, `m.principal_id = ?`, principalID)
}

// ProjectMemberships は Project#memberships (プロジェクトの全メンバー) を返す。
func ProjectMemberships(ctx context.Context, q db.Queryer, projectID int64) ([]*domain.Member, error) {
	return loadMembers(ctx, q, `m.project_id = ?`, projectID)
}

// MemberProjectIDs は Principal#project_ids (アーカイブされていないメンバーシップのプロジェクト ID)。
func MemberProjectIDs(ctx context.Context, q db.Queryer, principalID int64) ([]int64, error) {
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT m.project_id FROM members m JOIN projects ON projects.id = m.project_id
WHERE m.principal_id = ? AND projects.status <> ? ORDER BY m.id`, principalID, domain.ProjectStatusArchived)
	return ids, err
}

// ---------------------------------------------------------------- 書き込み

type newMemberRole struct {
	roleID        int64
	inheritedFrom *int64
}

func checkMemberRole(ctx context.Context, q db.Queryer, roleID int64) error {
	var builtin int
	err := q.Get(ctx, &builtin, `SELECT builtin FROM roles WHERE id = ?`, roleID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && builtin != domain.RoleBuiltinNone) {
		return fmt.Errorf("%w: role %d", ErrInvalidMemberRole, roleID)
	}
	return err
}

// findOrCreateMemberWithRoles は Member.find_or_initialize_by(project, principal) に
// member_roles を追加して save! する処理 (Group#user_added, MemberRole#add_role_to_* など)。
func findOrCreateMemberWithRoles(ctx context.Context, q db.Queryer, projectID, principalID int64, add []newMemberRole) (int64, error) {
	var memberID int64
	err := q.Get(ctx, &memberID, `SELECT id FROM members WHERE project_id = ? AND principal_id = ?`, projectID, principalID)
	if errors.Is(err, sql.ErrNoRows) {
		if len(add) == 0 {
			return 0, ErrMemberRoleEmpty
		}
		memberID, err = q.InsertReturningID(ctx, `INSERT INTO members (project_id, principal_id, created_at) VALUES (?, ?, ?)`,
			projectID, principalID, db.Now())
	}
	if err != nil {
		return 0, err
	}
	for _, a := range add {
		if _, err := createMemberRole(ctx, q, memberID, a.roleID, a.inheritedFrom); err != nil {
			return 0, err
		}
	}
	return memberID, nil
}

// createMemberRole は MemberRole を作成し after_create コールバック
// (add_role_to_group_users, add_role_to_subprojects) を実行する。
func createMemberRole(ctx context.Context, q db.Queryer, memberID, roleID int64, inheritedFrom *int64) (int64, error) {
	if err := checkMemberRole(ctx, q, roleID); err != nil {
		return 0, err
	}
	id, err := q.InsertReturningID(ctx, `INSERT INTO member_roles (member_id, role_id, inherited_from) VALUES (?, ?, ?)`,
		memberID, roleID, inheritedFrom)
	if err != nil {
		return 0, err
	}
	var m memberRow
	if err := q.Get(ctx, &m, `SELECT id, project_id, principal_id, created_at FROM members WHERE id = ?`, memberID); err != nil {
		return 0, err
	}
	// add_role_to_group_users: 直接付与されたロールで、プリンシパルがグループ (組込含む) の場合
	if inheritedFrom == nil {
		var kind string
		if err := q.Get(ctx, &kind, `SELECT kind FROM principals WHERE id = ?`, m.PrincipalID); err != nil {
			return 0, err
		}
		if domain.PrincipalKind(kind).IsGroup() {
			userIDs, err := GroupUserIDs(ctx, q, m.PrincipalID)
			if err != nil {
				return 0, err
			}
			for _, uid := range userIDs {
				from := id
				if _, err := findOrCreateMemberWithRoles(ctx, q, m.ProjectID, uid, []newMemberRole{{roleID: roleID, inheritedFrom: &from}}); err != nil {
					return 0, err
				}
			}
		}
	}
	// add_role_to_subprojects: inherit_members な子プロジェクトへ伝播
	var childIDs []int64
	if err := q.Select(ctx, &childIDs, `SELECT id FROM projects WHERE parent_id = ? AND inherit_members = ? ORDER BY id`, m.ProjectID, true); err != nil {
		return 0, err
	}
	for _, cid := range childIDs {
		from := id
		if _, err := findOrCreateMemberWithRoles(ctx, q, cid, m.PrincipalID, []newMemberRole{{roleID: roleID, inheritedFrom: &from}}); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// DestroyMemberRole は MemberRole#destroy の移植。
// 派生した継承ロール (inherited_from = id) を連鎖削除し (remove_inherited_roles)、
// メンバーのロールが無くなればメンバーも削除する (remove_member_if_empty)。
func DestroyMemberRole(ctx context.Context, q db.Queryer, id int64) error {
	return destroyMemberRole(ctx, q, id, true)
}

func destroyMemberRole(ctx context.Context, q db.Queryer, id int64, removeMember bool) error {
	var mr memberRoleRow
	if err := q.Get(ctx, &mr, `SELECT id, member_id, role_id, inherited_from FROM member_roles WHERE id = ?`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// 連鎖削除で既に消えている
			return nil
		}
		return err
	}
	// remove_inherited_roles: FK の ON DELETE CASCADE に任せるとコールバックが走らないため先に処理する。
	// (派生行は常に別のメンバーに属するので、自メンバーの空判定には影響しない)
	var children []int64
	if err := q.Select(ctx, &children, `SELECT id FROM member_roles WHERE inherited_from = ? ORDER BY id`, id); err != nil {
		return err
	}
	for _, c := range children {
		if err := destroyMemberRole(ctx, q, c, true); err != nil {
			return err
		}
	}
	if _, err := q.Exec(ctx, `DELETE FROM member_roles WHERE id = ?`, id); err != nil {
		return err
	}
	if removeMember {
		var n int
		if err := q.Get(ctx, &n, `SELECT COUNT(*) FROM member_roles WHERE member_id = ?`, mr.MemberID); err != nil {
			return err
		}
		if n == 0 {
			return DestroyMember(ctx, q, mr.MemberID)
		}
	}
	return nil
}

// DestroyMember は Member#destroy の移植。
// 各 member_role を destroy_without_member_removal (継承ロールの連鎖削除を含む) し、
// before_destroy (set_issue_category_nil, remove_from_project_default_assigned_to) を実行して削除する。
func DestroyMember(ctx context.Context, q db.Queryer, id int64) error {
	var m memberRow
	if err := q.Get(ctx, &m, `SELECT id, project_id, principal_id, created_at FROM members WHERE id = ?`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	var mrIDs []int64
	if err := q.Select(ctx, &mrIDs, `SELECT id FROM member_roles WHERE member_id = ? ORDER BY id`, id); err != nil {
		return err
	}
	for _, mrID := range mrIDs {
		if err := destroyMemberRole(ctx, q, mrID, false); err != nil {
			return err
		}
	}
	if _, err := q.Exec(ctx, `UPDATE issue_categories SET assigned_to_id = NULL WHERE project_id = ? AND assigned_to_id = ?`, m.ProjectID, m.PrincipalID); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `UPDATE projects SET default_assigned_to_id = NULL WHERE id = ? AND default_assigned_to_id = ?`, m.ProjectID, m.PrincipalID); err != nil {
		return err
	}
	// user_notified_projects は複合 FK の ON DELETE CASCADE で消える
	_, err := q.Exec(ctx, `DELETE FROM members WHERE id = ?`, id)
	return err
}

// CreateMember は Member.new(project, principal, role_ids).save の移植。作成したメンバーの id を返す。
func CreateMember(ctx context.Context, q db.Queryer, projectID, principalID int64, roleIDs []int64) (int64, error) {
	roleIDs = uniqIDs(roleIDs)
	if len(roleIDs) == 0 {
		return 0, ErrMemberRoleEmpty
	}
	var n int
	if err := q.Get(ctx, &n, `SELECT COUNT(*) FROM members WHERE project_id = ? AND principal_id = ?`, projectID, principalID); err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, ErrMemberTaken
	}
	for _, r := range roleIDs {
		if err := checkMemberRole(ctx, q, r); err != nil {
			return 0, err
		}
	}
	add := make([]newMemberRole, len(roleIDs))
	for i, r := range roleIDs {
		add[i] = newMemberRole{roleID: r}
	}
	return findOrCreateMemberWithRoles(ctx, q, projectID, principalID, add)
}

// SetMemberRoles は Member#role_ids= の移植。継承ロールは維持し、追加分を作成、
// 不要になった直接付与ロールを (コールバック付きで) 削除する。
// 結果のロールが空になる場合は何もせず ErrMemberRoleEmpty を返す。
func SetMemberRoles(ctx context.Context, q db.Queryer, memberID int64, roleIDs []int64) error {
	m, err := GetMember(ctx, q, memberID)
	if err != nil {
		return err
	}
	ids := uniqIDs(roleIDs)
	// 継承ロールは維持する
	for _, mr := range m.MemberRoles {
		if mr.Inherited() && !slices.Contains(ids, mr.RoleID) {
			ids = append(ids, mr.RoleID)
		}
	}
	if len(ids) == 0 {
		return ErrMemberRoleEmpty
	}
	cur := m.RoleIDs()
	for _, id := range ids {
		if !slices.Contains(cur, id) {
			if err := checkMemberRole(ctx, q, id); err != nil {
				return err
			}
		}
	}
	for _, id := range ids {
		if !slices.Contains(cur, id) {
			if _, err := createMemberRole(ctx, q, memberID, id, nil); err != nil {
				return err
			}
		}
	}
	for _, mr := range m.MemberRoles {
		if !slices.Contains(ids, mr.RoleID) {
			if err := destroyMemberRole(ctx, q, mr.ID, true); err != nil {
				return err
			}
		}
	}
	return nil
}

// AddMemberRoles は member.role_ids |= roleIDs; save の移植 (既存ロールは変更しない)。
func AddMemberRoles(ctx context.Context, q db.Queryer, memberID int64, roleIDs []int64) error {
	m, err := GetMember(ctx, q, memberID)
	if err != nil {
		return err
	}
	ids := m.RoleIDs()
	for _, r := range roleIDs {
		if !slices.Contains(ids, r) {
			ids = append(ids, r)
		}
	}
	return SetMemberRoles(ctx, q, memberID, ids)
}

// CreatePrincipalMemberships は Member.create_principal_memberships の移植。
// 各プロジェクトについてメンバーを作成するか、既存メンバーにロールを追加する。
// 作成・更新したメンバーの id を返す (検証エラーのプロジェクトは飛ばす: Redmine は save の戻り値を無視する)。
func CreatePrincipalMemberships(ctx context.Context, q db.Queryer, principalID int64, projectIDs, roleIDs []int64) ([]int64, error) {
	var out []int64
	for _, pid := range projectIDs {
		m, err := FindMember(ctx, q, pid, principalID)
		switch {
		case errors.Is(err, ErrNotFound):
			id, err := CreateMember(ctx, q, pid, principalID, roleIDs)
			if errors.Is(err, ErrMemberRoleEmpty) || errors.Is(err, ErrInvalidMemberRole) {
				continue
			}
			if err != nil {
				return nil, err
			}
			out = append(out, id)
		case err != nil:
			return nil, err
		default:
			if err := AddMemberRoles(ctx, q, m.ID, roleIDs); err != nil {
				if errors.Is(err, ErrMemberRoleEmpty) || errors.Is(err, ErrInvalidMemberRole) {
					continue
				}
				return nil, err
			}
			out = append(out, m.ID)
		}
	}
	return out, nil
}

func uniqIDs(ids []int64) []int64 {
	var out []int64
	for _, id := range ids {
		if id != 0 && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}
