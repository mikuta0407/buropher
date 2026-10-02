package repository

import (
	"context"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// このファイルは LDAP の定期同期（buropher 拡張）で使うクエリ。

// UsersByAuthSource は認証方式 sourceID を使うユーザー（id 順）。
func UsersByAuthSource(ctx context.Context, q db.Queryer, sourceID int64) ([]*domain.User, error) {
	var rows []userRow
	if err := q.Select(ctx, &rows, userSelect+` WHERE p.kind = 'user' AND ua.auth_source_id = ? ORDER BY p.id`, sourceID); err != nil {
		return nil, err
	}
	out := make([]*domain.User, len(rows))
	for i := range rows {
		out[i] = rows[i].user()
	}
	return out, nil
}

// EmailTakenByOtherUser はメールアドレスが（大文字小文字を無視して）userID 以外のユーザーに使われていれば true。
func EmailTakenByOtherUser(ctx context.Context, q db.Queryer, address string, userID int64) (bool, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM email_addresses WHERE LOWER(address) = ? AND user_id <> ?`, strings.ToLower(address), userID)
	return n > 0, err
}

// UpdateAuthSourceConfigValues は auth_sources.config の一部のキーだけを書き換える（updated_at は変えない）。
func UpdateAuthSourceConfigValues(ctx context.Context, q db.Queryer, id int64, values map[string]any) error {
	rec, err := GetAuthSource(ctx, q, id)
	if err != nil {
		return err
	}
	for k, v := range values {
		rec.Config[k] = v
	}
	_, err = q.Exec(ctx, `UPDATE auth_sources SET config = ? WHERE id = ?`, db.NewJSON(rec.Config), id)
	return err
}
