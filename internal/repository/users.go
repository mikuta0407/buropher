// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// FindActiveUser は User.active.find_by_id(id) の移植。有効なユーザが無ければ (nil, ErrNotFound)。
func FindActiveUser(ctx context.Context, q db.Queryer, id int64) (*domain.User, error) {
	var r userRow
	if err := q.Get(ctx, &r, userSelect+` WHERE p.id = ? AND p.kind = 'user' AND p.status = ?`, id, domain.StatusActive); err != nil {
		return nil, notFound(err)
	}
	return r.user(), nil
}

// UsersByIDs は id の User / AnonymousUser を id -> User の map で返す (preload(:author) 等に使う)。
func UsersByIDs(ctx context.Context, q db.Queryer, ids []int64) (map[int64]*domain.User, error) {
	out := make(map[int64]*domain.User, len(ids))
	for _, chunk := range chunkIDs(uniqIDs(ids)) {
		query, args, err := db.In(userSelect+` WHERE p.id IN (?) AND p.kind IN ('user', 'anonymous_user')`, chunk)
		if err != nil {
			return nil, err
		}
		var rows []userRow
		if err := q.Select(ctx, &rows, query, args...); err != nil {
			return nil, err
		}
		for i := range rows {
			out[rows[i].ID] = rows[i].user()
		}
	}
	return out, nil
}

// UpdateLastLogin は User#update_last_login_on!。
func UpdateLastLogin(ctx context.Context, q db.Queryer, userID int64, t time.Time) error {
	_, err := q.Exec(ctx, `UPDATE user_accounts SET last_login_at = ? WHERE principal_id = ?`, db.NewTime(t), userID)
	return err
}

// UpdatePasswordHash はパスワードハッシュだけを置き換える (ログイン時の再ハッシュ用。
// password_changed_at は変えない)。
func UpdatePasswordHash(ctx context.Context, q db.Queryer, userID int64, hash string) error {
	_, err := q.Exec(ctx, `UPDATE user_accounts SET password_hash = ? WHERE principal_id = ?`, hash, userID)
	return err
}

// UserInTwofaRequiredGroup は user.groups.any?(&:twofa_required?)。
func UserInTwofaRequiredGroup(ctx context.Context, q db.Queryer, userID int64) (bool, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM group_users gu JOIN principals g ON g.id = gu.group_id
WHERE gu.user_id = ? AND g.twofa_required = TRUE`, userID)
	return n > 0, err
}

// ---------------------------------------------------------------- 個人設定 (User#pref)

type preferenceRow struct {
	UserID               int64          `db:"user_id"`
	HideMail             bool           `db:"hide_mail"`
	TimeZone             sql.NullString `db:"time_zone"`
	CommentsSorting      string         `db:"comments_sorting"`
	WarnOnLeavingUnsaved bool           `db:"warn_on_leaving_unsaved"`
	TextareaFont         sql.NullString `db:"textarea_font"`
	RecentlyUsedProjects int            `db:"recently_used_projects"`
	HistoryDefaultTab    sql.NullString `db:"history_default_tab"`
}

// GetUserPreference は User#pref の移植。行が無ければ既定値 (domain.DefaultUserPreference) を返す。
// 匿名ユーザ (userID 0 を含む) も既定値になる。
func GetUserPreference(ctx context.Context, q db.Queryer, userID int64) (*domain.UserPreference, error) {
	if userID == 0 {
		return domain.DefaultUserPreference(0), nil
	}
	var r preferenceRow
	err := q.Get(ctx, &r, `SELECT user_id, hide_mail, time_zone, comments_sorting, warn_on_leaving_unsaved, textarea_font,
  recently_used_projects, history_default_tab FROM user_preferences WHERE user_id = ?`, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DefaultUserPreference(userID), nil
	}
	if err != nil {
		return nil, err
	}
	return &domain.UserPreference{
		UserID: r.UserID, HideMail: r.HideMail, TimeZone: r.TimeZone.String, CommentsSorting: r.CommentsSorting,
		WarnOnLeavingUnsaved: r.WarnOnLeavingUnsaved, TextareaFont: r.TextareaFont.String,
		RecentlyUsedProjects: r.RecentlyUsedProjects, HistoryDefaultTab: r.HistoryDefaultTab.String,
	}, nil
}
