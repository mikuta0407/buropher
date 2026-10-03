// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository

import (
	"context"
	"database/sql"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// このファイルは auth_sources（AuthSource / AuthSourceLdap）の読み書き。

type authSourceRow struct {
	ID               int64                   `db:"id"`
	Kind             string                  `db:"kind"`
	Name             string                  `db:"name"`
	Enabled          bool                    `db:"enabled"`
	Position         int                     `db:"position"`
	OntheflyRegister bool                    `db:"onthefly_register"`
	Config           db.JSON[map[string]any] `db:"config"`
	Secret           sql.NullString          `db:"secret"`
	CreatedAt        db.Time                 `db:"created_at"`
	UpdatedAt        db.Time                 `db:"updated_at"`
}

const authSourceCols = `id, kind, name, enabled, position, onthefly_register, config, secret, created_at, updated_at`

func (r authSourceRow) domain() *domain.AuthSourceRecord {
	out := &domain.AuthSourceRecord{ID: r.ID, Kind: r.Kind, Name: r.Name, Enabled: r.Enabled, Position: r.Position,
		OntheflyRegister: r.OntheflyRegister, Config: r.Config.V, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}
	if out.Config == nil {
		out.Config = map[string]any{}
	}
	if r.Secret.Valid {
		s := r.Secret.String
		out.Secret = &s
	}
	return out
}

func selectAuthSources(ctx context.Context, q db.Queryer, where string, args ...any) ([]*domain.AuthSourceRecord, error) {
	var rows []authSourceRow
	if err := q.Select(ctx, &rows, `SELECT `+authSourceCols+` FROM auth_sources `+where, args...); err != nil {
		return nil, err
	}
	out := make([]*domain.AuthSourceRecord, len(rows))
	for i, r := range rows {
		out[i] = r.domain()
	}
	return out, nil
}

// CountAuthSources は AuthSource.count。
func CountAuthSources(ctx context.Context, q db.Queryer) (int, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM auth_sources`)
	return n, err
}

// AuthSourcePage は paginate AuthSource（id 順の limit / offset）。
func AuthSourcePage(ctx context.Context, q db.Queryer, limit, offset int) ([]*domain.AuthSourceRecord, error) {
	return selectAuthSources(ctx, q, `ORDER BY id LIMIT ? OFFSET ?`, limit, offset)
}

// GetAuthSource は AuthSource.find(id)。
func GetAuthSource(ctx context.Context, q db.Queryer, id int64) (*domain.AuthSourceRecord, error) {
	rs, err := selectAuthSources(ctx, q, `WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(rs) == 0 {
		return nil, ErrNotFound
	}
	return rs[0], nil
}

// AllAuthSources は AuthSource.all（id 順。無効化されたものを除く）。
func AllAuthSources(ctx context.Context, q db.Queryer) ([]*domain.AuthSourceRecord, error) {
	return selectAuthSources(ctx, q, `WHERE enabled = TRUE ORDER BY id`)
}

// OntheflyAuthSources は AuthSource.where(:onthefly_register => true)（id 順。無効化されたものを除く）。
func OntheflyAuthSources(ctx context.Context, q db.Queryer) ([]*domain.AuthSourceRecord, error) {
	return selectAuthSources(ctx, q, `WHERE onthefly_register = TRUE AND enabled = TRUE ORDER BY id`)
}

// AuthSourceNameTaken は validates_uniqueness_of :name, :case_sensitive => true。
func AuthSourceNameTaken(ctx context.Context, q db.Queryer, name string, exceptID int64) (bool, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM auth_sources WHERE name = ? AND id <> ?`, name, exceptID)
	return n > 0, err
}

// AuthSourceUserCounts は source.users.count（認証方式ごとのユーザー数）。
func AuthSourceUserCounts(ctx context.Context, q db.Queryer) (map[int64]int, error) {
	var rows []struct {
		ID int64 `db:"auth_source_id"`
		N  int   `db:"n"`
	}
	if err := q.Select(ctx, &rows, `SELECT auth_source_id, COUNT(*) AS n FROM user_accounts WHERE auth_source_id IS NOT NULL GROUP BY auth_source_id`); err != nil {
		return nil, err
	}
	out := make(map[int64]int, len(rows))
	for _, r := range rows {
		out[r.ID] = r.N
	}
	return out, nil
}

// SaveAuthSource は auth_sources 行を作成・更新する（ID が 0 なら作成して ID を設定する）。
func SaveAuthSource(ctx context.Context, q db.Queryer, r *domain.AuthSourceRecord) error {
	if r.Config == nil {
		r.Config = map[string]any{}
	}
	var secret any
	if r.Secret != nil {
		secret = *r.Secret
	}
	cfg := db.NewJSON(r.Config)
	if r.ID == 0 {
		if r.Position == 0 {
			var max sql.NullInt64
			if err := q.Get(ctx, &max, `SELECT MAX(position) FROM auth_sources`); err != nil {
				return err
			}
			r.Position = int(max.Int64) + 1
		}
		id, err := q.InsertReturningID(ctx, `INSERT INTO auth_sources (kind, name, enabled, position, onthefly_register, config, secret, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.Kind, r.Name, r.Enabled, r.Position, r.OntheflyRegister, cfg, secret,
			db.NewTime(r.CreatedAt), db.NewTime(r.UpdatedAt))
		if err != nil {
			return err
		}
		r.ID = id
		return nil
	}
	_, err := q.Exec(ctx, `UPDATE auth_sources SET name = ?, enabled = ?, position = ?, onthefly_register = ?, config = ?, secret = ?, updated_at = ? WHERE id = ?`,
		r.Name, r.Enabled, r.Position, r.OntheflyRegister, cfg, secret, db.NewTime(r.UpdatedAt), r.ID)
	return err
}

// DeleteAuthSource は auth_sources 行を削除する。
func DeleteAuthSource(ctx context.Context, q db.Queryer, id int64) error {
	_, err := q.Exec(ctx, `DELETE FROM auth_sources WHERE id = ?`, id)
	return err
}
