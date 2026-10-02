package repository

import (
	"context"
	"errors"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// CreatePrincipalMembership は Member.create_principal_memberships の 1 プロジェクト分
// （Member.find_or_initialize_by(project, principal); member.role_ids |= role_ids; member.save）。
// 作成・更新したメンバーの id を返す。検証エラー（ロールが空など）は ErrMemberRoleEmpty 等。
func CreatePrincipalMembership(ctx context.Context, q db.Queryer, principalID, projectID int64, roleIDs []int64) (int64, error) {
	m, err := FindMember(ctx, q, projectID, principalID)
	switch {
	case errors.Is(err, ErrNotFound):
		return CreateMember(ctx, q, projectID, principalID, roleIDs)
	case err != nil:
		return 0, err
	}
	if err := AddMemberRoles(ctx, q, m.ID, roleIDs); err != nil {
		return 0, err
	}
	return m.ID, nil
}

// MemberOfMemberRole は member_roles.id のメンバー（MemberRole#member）を返す。
func MemberOfMemberRole(ctx context.Context, q db.Queryer, memberRoleID int64) (*domain.Member, error) {
	var memberID int64
	if err := q.Get(ctx, &memberID, `SELECT member_id FROM member_roles WHERE id = ?`, memberRoleID); err != nil {
		return nil, notFound(err)
	}
	return GetMember(ctx, q, memberID)
}
