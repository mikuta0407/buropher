// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// このファイルはグループ管理（GroupsController）と Principal.like / User.sorted の読み書き。

var spacesRe = regexp.MustCompile(`\s+`)

// PrincipalLikeCondition は Principal.like(q) の WHERE 断片（principals を p として参照）。q が空なら "1=1"。
func PrincipalLikeCondition(q string) (string, []any) {
	if strings.TrimSpace(q) == "" {
		return "1=1", nil
	}
	pattern := "%" + likeEscape(q) + "%"
	sql := `(LOWER(COALESCE(ua.login, '')) LIKE LOWER(?) ESCAPE '\'` +
		` OR p.id IN (SELECT user_id FROM email_addresses WHERE LOWER(address) LIKE LOWER(?) ESCAPE '\')`
	args := []any{pattern, pattern}
	var toks []string
	for _, t := range spacesRe.Split(q, -1) {
		if strings.TrimSpace(t) != "" {
			toks = append(toks, t)
		}
	}
	if len(toks) > 0 {
		var parts []string
		for _, t := range toks {
			tp := "%" + likeEscape(t) + "%"
			// グループ名は principals.name（Redmine の lastname）
			parts = append(parts, `(LOWER(p.firstname) LIKE LOWER(?) ESCAPE '\' OR LOWER(CASE WHEN p.kind IN ('user', 'anonymous_user') THEN p.lastname ELSE p.name END) LIKE LOWER(?) ESCAPE '\')`)
			args = append(args, tp, tp)
		}
		sql += " OR (" + strings.Join(parts, " AND ") + ")"
	}
	return sql + ")", args
}

// UserOrderColumns は User.fields_for_order_statement（Setting.user_format の :order）。
func UserOrderColumns(format string) []string {
	switch format {
	case "firstname":
		return []string{"p.firstname", "p.id"}
	case "lastname_firstname", "lastnamefirstname", "lastname_comma_firstname":
		return []string{"p.lastname", "p.firstname", "p.id"}
	case "lastname":
		return []string{"p.lastname", "p.id"}
	case "username":
		return []string{"ua.login", "p.id"}
	}
	return []string{"p.firstname", "p.lastname", "p.id"}
}

// CountGroups は Group.sorted.like(name) の件数（組込グループを含む）。
func CountGroups(ctx context.Context, q db.Queryer, like string) (int, error) {
	cond, args := PrincipalLikeCondition(like)
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM principals p LEFT JOIN user_accounts ua ON ua.principal_id = p.id
WHERE p.kind IN ('group', 'group_anonymous', 'group_non_member') AND `+cond, args...)
	return n, err
}

// SearchGroups は Group.sorted.like(name).limit(limit).offset(offset)。
func SearchGroups(ctx context.Context, q db.Queryer, like string, limit, offset int) ([]*domain.Group, error) {
	cond, args := PrincipalLikeCondition(like)
	query := `SELECT ` + principalCols + ` FROM principals p LEFT JOIN user_accounts ua ON ua.principal_id = p.id
WHERE p.kind IN ('group', 'group_anonymous', 'group_non_member') AND ` + cond + `
ORDER BY CASE p.kind WHEN 'group' THEN 0 WHEN 'group_anonymous' THEN 1 ELSE 2 END, p.name, p.id`
	if limit >= 0 {
		query += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
	}
	var rows []principalRow
	if err := q.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	out := make([]*domain.Group, len(rows))
	for i := range rows {
		out[i] = &domain.Group{Principal: rows[i].principal()}
	}
	return out, nil
}

// GroupNameTaken はグループ名が（大文字小文字を無視して）他のグループに使われていれば true
// （validates_uniqueness_of :lastname, case_sensitive: false。組込グループを含む）。
func GroupNameTaken(ctx context.Context, q db.Queryer, name string, exceptID int64) (bool, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM principals WHERE kind IN ('group', 'group_anonymous', 'group_non_member')
AND LOWER(name) = ? AND id <> ?`, strings.ToLower(name), exceptID)
	return n > 0, err
}

// InsertGroup はグループを作成して id を返す。
func InsertGroup(ctx context.Context, q db.Queryer, name string, twofaRequired bool, now time.Time) (int64, error) {
	t := db.NewTime(now)
	return q.InsertReturningID(ctx, `INSERT INTO principals (kind, status, name, twofa_required, created_at, updated_at) VALUES ('group', 1, ?, ?, ?, ?)`,
		name, twofaRequired, t, t)
}

// UpdateGroup はグループ名・2FA 必須を保存する（touch なら updated_at も）。
func UpdateGroup(ctx context.Context, q db.Queryer, id int64, name string, twofaRequired, touch bool, now time.Time) error {
	if touch {
		_, err := q.Exec(ctx, `UPDATE principals SET name = ?, twofa_required = ?, updated_at = ? WHERE id = ?`, name, twofaRequired, db.NewTime(now), id)
		return err
	}
	_, err := q.Exec(ctx, `UPDATE principals SET name = ?, twofa_required = ? WHERE id = ?`, name, twofaRequired, id)
	return err
}

// GroupUsers は group.users（id 順）。
func GroupUsers(ctx context.Context, q db.Queryer, groupID int64) ([]*domain.User, error) {
	var rows []userRow
	if err := q.Select(ctx, &rows, userSelect+` WHERE p.id IN (SELECT user_id FROM group_users WHERE group_id = ?) AND p.kind IN ('user', 'anonymous_user') ORDER BY p.id`, groupID); err != nil {
		return nil, err
	}
	out := make([]*domain.User, len(rows))
	for i := range rows {
		out[i] = rows[i].user()
	}
	return out, nil
}

// CountUsersNotInGroup は User.active.not_in_group(group).like(q).count。
func CountUsersNotInGroup(ctx context.Context, q db.Queryer, groupID int64, like string) (int, error) {
	cond, args := PrincipalLikeCondition(like)
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM principals p JOIN user_accounts ua ON ua.principal_id = p.id
WHERE p.kind = 'user' AND p.status = 1 AND p.id NOT IN (SELECT user_id FROM group_users WHERE group_id = ?) AND `+cond,
		append([]any{groupID}, args...)...)
	return n, err
}

// UsersNotInGroup は User.active.sorted.not_in_group(group).like(q).offset(offset).limit(limit)。
func UsersNotInGroup(ctx context.Context, q db.Queryer, groupID int64, like, userFormat string, limit, offset int) ([]*domain.User, error) {
	cond, args := PrincipalLikeCondition(like)
	query := userSelect + ` WHERE p.kind = 'user' AND p.status = 1 AND p.id NOT IN (SELECT user_id FROM group_users WHERE group_id = ?) AND ` + cond +
		` ORDER BY ` + strings.Join(UserOrderColumns(userFormat), ", ") + fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
	var rows []userRow
	if err := q.Select(ctx, &rows, query, append([]any{groupID}, args...)...); err != nil {
		return nil, err
	}
	out := make([]*domain.User, len(rows))
	for i := range rows {
		out[i] = rows[i].user()
	}
	return out, nil
}

// UsersNotInGroupByIDs は User.not_in_group(group).where(id: ids)（id 順）。
func UsersNotInGroupByIDs(ctx context.Context, q db.Queryer, groupID int64, ids []int64) ([]*domain.User, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	query, args, err := db.In(userSelect+` WHERE p.kind IN ('user', 'anonymous_user') AND p.id IN (?) AND p.id NOT IN (SELECT user_id FROM group_users WHERE group_id = ?) ORDER BY p.id`, ids, groupID)
	if err != nil {
		return nil, err
	}
	var rows []userRow
	if err := q.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	out := make([]*domain.User, len(rows))
	for i := range rows {
		out[i] = rows[i].user()
	}
	return out, nil
}
