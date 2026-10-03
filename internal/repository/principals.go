// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// ErrNotFound は対象行が存在しない。
var ErrNotFound = errors.New("repository: not found")

// principalRow は principals (+ user_accounts) の読み取り用。
type principalRow struct {
	ID            int64   `db:"id"`
	Kind          string  `db:"kind"`
	Status        int     `db:"status"`
	Firstname     string  `db:"firstname"`
	Lastname      string  `db:"lastname"`
	Name          string  `db:"name"`
	TwofaRequired bool    `db:"twofa_required"`
	CreatedAt     db.Time `db:"created_at"`
	UpdatedAt     db.Time `db:"updated_at"`
}

func (r *principalRow) principal() domain.Principal {
	return domain.Principal{
		ID: r.ID, Kind: domain.PrincipalKind(r.Kind), Status: r.Status,
		Firstname: r.Firstname, Lastname: r.Lastname, Name: r.Name, TwofaRequired: r.TwofaRequired,
		CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time,
	}
}

type userRow struct {
	principalRow
	Login              sql.NullString `db:"login"`
	PasswordHash       sql.NullString `db:"password_hash"`
	PasswordChangedAt  db.NullTime    `db:"password_changed_at"`
	MustChangePassword sql.NullBool   `db:"must_change_password"`
	Admin              sql.NullBool   `db:"admin"`
	Language           sql.NullString `db:"language"`
	AuthSourceID       sql.NullInt64  `db:"auth_source_id"`
	LastLoginAt        db.NullTime    `db:"last_login_at"`
	TwofaScheme        sql.NullString `db:"twofa_scheme"`
	Mail               sql.NullString `db:"mail"`
}

func (r *userRow) user() *domain.User {
	u := &domain.User{
		Principal:          r.principal(),
		Login:              r.Login.String,
		PasswordHash:       r.PasswordHash.String,
		PasswordChangedAt:  r.PasswordChangedAt.Ptr(),
		MustChangePassword: r.MustChangePassword.Bool,
		AdminFlag:          r.Admin.Bool,
		Language:           r.Language.String,
		LastLoginAt:        r.LastLoginAt.Ptr(),
		TwofaScheme:        r.TwofaScheme.String,
		Mail:               r.Mail.String,
	}
	if r.AuthSourceID.Valid {
		v := r.AuthSourceID.Int64
		u.AuthSourceID = &v
	}
	return u
}

const principalCols = `p.id, p.kind, p.status, p.firstname, p.lastname, p.name, p.twofa_required, p.created_at, p.updated_at`

const userSelect = `SELECT ` + principalCols + `, ua.login, ua.password_hash, ua.password_changed_at, ua.must_change_password,
  ua.admin, ua.language, ua.auth_source_id, ua.last_login_at, ua.twofa_scheme,
  (SELECT e.address FROM email_addresses e WHERE e.user_id = p.id AND e.is_default = TRUE) AS mail
FROM principals p LEFT JOIN user_accounts ua ON ua.principal_id = p.id`

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// GetPrincipal は id の Principal を返す (種別を問わない)。
func GetPrincipal(ctx context.Context, q db.Queryer, id int64) (*domain.Principal, error) {
	var r principalRow
	if err := q.Get(ctx, &r, `SELECT `+principalCols+` FROM principals p WHERE p.id = ?`, id); err != nil {
		return nil, notFound(err)
	}
	p := r.principal()
	return &p, nil
}

// GetUser は id の User / AnonymousUser を返す (User.find)。
func GetUser(ctx context.Context, q db.Queryer, id int64) (*domain.User, error) {
	var r userRow
	if err := q.Get(ctx, &r, userSelect+` WHERE p.id = ? AND p.kind IN ('user', 'anonymous_user')`, id); err != nil {
		return nil, notFound(err)
	}
	return r.user(), nil
}

// ListUsers は id 順に User (匿名ユーザを除く) を返す。statuses が空なら全ステータス。
func ListUsers(ctx context.Context, q db.Queryer, statuses ...int) ([]*domain.User, error) {
	query := userSelect + ` WHERE p.kind = 'user'`
	var args []any
	if len(statuses) > 0 {
		s, a, err := db.In(` AND p.status IN (?)`, statuses)
		if err != nil {
			return nil, err
		}
		query += s
		args = a
	}
	var rows []userRow
	if err := q.Select(ctx, &rows, query+` ORDER BY p.id`, args...); err != nil {
		return nil, err
	}
	out := make([]*domain.User, len(rows))
	for i := range rows {
		out[i] = rows[i].user()
	}
	return out, nil
}

// FindUserByLogin は User.find_by_login の移植。完全一致を優先し、無ければ大文字小文字を無視して探す。
// 空文字なら (nil, ErrNotFound)。
func FindUserByLogin(ctx context.Context, q db.Queryer, login string) (*domain.User, error) {
	login = strings.ToValidUTF8(login, "?")
	if strings.TrimSpace(login) == "" {
		return nil, ErrNotFound
	}
	var rows []userRow
	if err := q.Select(ctx, &rows, userSelect+` WHERE p.kind = 'user' AND ua.login = ?`, login); err != nil {
		return nil, err
	}
	for i := range rows {
		if rows[i].Login.String == login {
			return rows[i].user(), nil
		}
	}
	rows = nil
	if err := q.Select(ctx, &rows, userSelect+` WHERE p.kind = 'user' AND LOWER(ua.login) = ? ORDER BY p.id`, strings.ToLower(login)); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	return rows[0].user(), nil
}

// AnonymousUser は User.anonymous の移植。匿名ユーザが無ければ作成する。
func AnonymousUser(ctx context.Context, q db.Queryer) (*domain.User, error) {
	var r userRow
	err := q.Get(ctx, &r, userSelect+` WHERE p.kind = 'anonymous_user' ORDER BY p.id LIMIT 1`)
	if err == nil {
		return r.user(), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	now := db.Now()
	id, err := q.InsertReturningID(ctx, `INSERT INTO principals (kind, status, firstname, lastname, created_at, updated_at) VALUES ('anonymous_user', 0, '', 'Anonymous', ?, ?)`, now, now)
	if err != nil {
		return nil, fmt.Errorf("repository: create anonymous user: %w", err)
	}
	if _, err := q.Exec(ctx, `INSERT INTO user_accounts (principal_id, login) VALUES (?, '')`, id); err != nil {
		return nil, fmt.Errorf("repository: create anonymous user account: %w", err)
	}
	return GetUser(ctx, q, id)
}

// GetGroup は id の Group (組込グループを含む) を返す。
func GetGroup(ctx context.Context, q db.Queryer, id int64) (*domain.Group, error) {
	var r principalRow
	if err := q.Get(ctx, &r, `SELECT `+principalCols+` FROM principals p WHERE p.id = ? AND p.kind IN ('group', 'group_anonymous', 'group_non_member')`, id); err != nil {
		return nil, notFound(err)
	}
	return &domain.Group{Principal: r.principal()}, nil
}

// ListGroups は Group.sorted (type, 名前の順) でグループを返す。includeBuiltin が false なら
// 組込グループを除く (Group.givable)。
func ListGroups(ctx context.Context, q db.Queryer, includeBuiltin bool) ([]*domain.Group, error) {
	where := `p.kind = 'group'`
	if includeBuiltin {
		where = `p.kind IN ('group', 'group_anonymous', 'group_non_member')`
	}
	var rows []principalRow
	// Redmine の type 順は 'Group' < 'GroupAnonymous' < 'GroupNonMember'
	if err := q.Select(ctx, &rows, `SELECT `+principalCols+` FROM principals p WHERE `+where+`
ORDER BY CASE p.kind WHEN 'group' THEN 0 WHEN 'group_anonymous' THEN 1 ELSE 2 END, p.name, p.id`); err != nil {
		return nil, err
	}
	out := make([]*domain.Group, len(rows))
	for i := range rows {
		out[i] = &domain.Group{Principal: rows[i].principal()}
	}
	return out, nil
}

// BuiltinGroup は GroupBuiltin.load_instance の移植 (GroupAnonymous / GroupNonMember)。
// 存在しなければ作成する。
func BuiltinGroup(ctx context.Context, q db.Queryer, kind domain.PrincipalKind) (*domain.Group, error) {
	if !kind.IsBuiltinGroup() {
		return nil, fmt.Errorf("repository: %q is not a builtin group kind", kind)
	}
	var r principalRow
	err := q.Get(ctx, &r, `SELECT `+principalCols+` FROM principals p WHERE p.kind = ? ORDER BY p.id LIMIT 1`, string(kind))
	if err == nil {
		return &domain.Group{Principal: r.principal()}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	now := db.Now()
	id, err := q.InsertReturningID(ctx, `INSERT INTO principals (kind, status, name, created_at, updated_at) VALUES (?, 1, ?, ?, ?)`,
		string(kind), kind.RedmineType(), now, now)
	if err != nil {
		return nil, fmt.Errorf("repository: create builtin group: %w", err)
	}
	return GetGroup(ctx, q, id)
}

// BuiltinGroupID は組込グループの id を返す (無ければ作成する)。
func BuiltinGroupID(ctx context.Context, q db.Queryer, kind domain.PrincipalKind) (int64, error) {
	g, err := BuiltinGroup(ctx, q, kind)
	if err != nil {
		return 0, err
	}
	return g.ID, nil
}

// GroupUserIDs はグループに所属するユーザ ID (id 順) を返す。
func GroupUserIDs(ctx context.Context, q db.Queryer, groupID int64) ([]int64, error) {
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT user_id FROM group_users WHERE group_id = ? ORDER BY user_id`, groupID)
	return ids, err
}

// UserGroupIDs はユーザが所属するグループ ID (id 順) を返す (user.groups)。
func UserGroupIDs(ctx context.Context, q db.Queryer, userID int64) ([]int64, error) {
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT group_id FROM group_users WHERE user_id = ? ORDER BY group_id`, userID)
	return ids, err
}

// CreateGroup はグループを作成して id を返す (検証は呼び出し側で行う)。
func CreateGroup(ctx context.Context, q db.Queryer, name string, twofaRequired bool) (int64, error) {
	now := db.Now()
	return q.InsertReturningID(ctx, `INSERT INTO principals (kind, status, name, twofa_required, created_at, updated_at) VALUES ('group', 1, ?, ?, ?, ?)`,
		name, twofaRequired, now, now)
}

// AddUserToGroup はユーザをグループに追加し、Group#user_added を実行する
// (グループのメンバーシップのロールをユーザに継承ロールとして付与する)。
// 既に所属していれば何もしない。組込グループにはエラー。
func AddUserToGroup(ctx context.Context, q db.Queryer, groupID, userID int64) error {
	g, err := GetGroup(ctx, q, groupID)
	if err != nil {
		return err
	}
	if g.Builtin() {
		return errors.New("repository: cannot add users to a builtin group")
	}
	var n int
	if err := q.Get(ctx, &n, `SELECT COUNT(*) FROM group_users WHERE group_id = ? AND user_id = ?`, groupID, userID); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if _, err := q.Exec(ctx, `INSERT INTO group_users (group_id, user_id) VALUES (?, ?)`, groupID, userID); err != nil {
		return err
	}
	return groupUserAdded(ctx, q, groupID, userID)
}

// groupUserAdded は Group#user_added の移植。
func groupUserAdded(ctx context.Context, q db.Queryer, groupID, userID int64) error {
	members, err := membersOfPrincipal(ctx, q, groupID, false)
	if err != nil {
		return err
	}
	for _, m := range members {
		// ロールを持たないメンバーシップは飛ばす
		if len(m.MemberRoles) == 0 {
			continue
		}
		var add []newMemberRole
		for _, mr := range m.MemberRoles {
			from := mr.ID
			add = append(add, newMemberRole{roleID: mr.RoleID, inheritedFrom: &from})
		}
		if _, err := findOrCreateMemberWithRoles(ctx, q, m.ProjectID, userID, add); err != nil {
			return err
		}
	}
	return nil
}

// RemoveUserFromGroup はユーザをグループから外し、Group#user_removed を実行する
// (グループ由来の継承ロールを削除し、ロールが無くなったメンバーシップも削除する)。
func RemoveUserFromGroup(ctx context.Context, q db.Queryer, groupID, userID int64) error {
	res, err := q.Exec(ctx, `DELETE FROM group_users WHERE group_id = ? AND user_id = ?`, groupID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	return groupUserRemoved(ctx, q, groupID, userID)
}

// groupUserRemoved は Group#user_removed の移植。
func groupUserRemoved(ctx context.Context, q db.Queryer, groupID, userID int64) error {
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT mr.id FROM member_roles mr JOIN members m ON m.id = mr.member_id
WHERE m.principal_id = ? AND mr.inherited_from IN (
  SELECT gmr.id FROM member_roles gmr JOIN members gm ON gm.id = gmr.member_id WHERE gm.principal_id = ?)
ORDER BY mr.id`, userID, groupID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := DestroyMemberRole(ctx, q, id); err != nil {
			return err
		}
	}
	return nil
}

// SetGroupUsers はグループの所属ユーザを userIDs に置き換える (Group#user_ids=)。
// 追加分は user_added、削除分は user_removed を実行する。
func SetGroupUsers(ctx context.Context, q db.Queryer, groupID int64, userIDs []int64) error {
	cur, err := GroupUserIDs(ctx, q, groupID)
	if err != nil {
		return err
	}
	want := map[int64]bool{}
	for _, id := range userIDs {
		want[id] = true
	}
	for _, id := range cur {
		if !want[id] {
			if err := RemoveUserFromGroup(ctx, q, groupID, id); err != nil {
				return err
			}
		}
		delete(want, id)
	}
	for _, id := range userIDs {
		if want[id] {
			if err := AddUserToGroup(ctx, q, groupID, id); err != nil {
				return err
			}
			delete(want, id)
		}
	}
	return nil
}

// DestroyGroup はグループを削除する (Group#destroy)。組込グループは削除しない (false 相当でエラー)。
//   - before_destroy remove_references_before_destroy: 担当チケットを NULL、ウォッチャー削除
//   - Principal before_destroy nullify_projects_default_assigned_to
//   - has_many :members, dependent: :destroy (継承ロールの連鎖削除を含む)
//   - issue_categories.assigned_to_id は nullify
func DestroyGroup(ctx context.Context, q db.Queryer, groupID int64) error {
	g, err := GetGroup(ctx, q, groupID)
	if err != nil {
		return err
	}
	if g.Builtin() {
		return errors.New("repository: builtin groups cannot be destroyed")
	}
	if _, err := q.Exec(ctx, `UPDATE issues SET assigned_to_id = NULL WHERE assigned_to_id = ?`, groupID); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `DELETE FROM watchers WHERE principal_id = ?`, groupID); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `UPDATE projects SET default_assigned_to_id = NULL WHERE default_assigned_to_id = ?`, groupID); err != nil {
		return err
	}
	var memberIDs []int64
	if err := q.Select(ctx, &memberIDs, `SELECT id FROM members WHERE principal_id = ? ORDER BY id`, groupID); err != nil {
		return err
	}
	for _, id := range memberIDs {
		if err := DestroyMember(ctx, q, id); err != nil {
			return err
		}
	}
	if _, err := q.Exec(ctx, `UPDATE issue_categories SET assigned_to_id = NULL WHERE assigned_to_id = ?`, groupID); err != nil {
		return err
	}
	// habtm の結合行はコールバックなしで削除される
	if _, err := q.Exec(ctx, `DELETE FROM group_users WHERE group_id = ?`, groupID); err != nil {
		return err
	}
	// acts_as_customizable: カスタム値は多態参照のため明示的に削除する
	if _, err := q.Exec(ctx, `DELETE FROM custom_values WHERE customized_kind = 'principal' AND customized_id = ?`, groupID); err != nil {
		return err
	}
	_, err = q.Exec(ctx, `DELETE FROM principals WHERE id = ?`, groupID)
	return err
}
