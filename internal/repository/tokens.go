// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"regexp"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// Token のアクション (Token.add_action)。session は sessions テーブル、
// twofa_backup_code は twofa_backup_codes テーブルが担う。
const (
	TokenAPI          = "api"
	TokenAutologin    = "autologin"
	TokenFeeds        = "feeds"
	TokenRecovery     = "recovery"
	TokenRegister     = "register"
	TokenTwofaSession = "twofa_session"
)

// tokenMaxInstances は Token.add_action の max_instances。未知のアクションは 1。
var tokenMaxInstances = map[string]int{
	TokenAPI: 1, TokenAutologin: 10, TokenFeeds: 1, TokenRecovery: 1, TokenRegister: 1, TokenTwofaSession: 1,
}

// TokenMaxInstances は Token#max_instances。
func TokenMaxInstances(action string) int {
	if n, ok := tokenMaxInstances[action]; ok {
		return n
	}
	return 1
}

// TokenValidity は Token.invalid_when_created_before が使う有効期間。0 は無期限。
// autologinDays は Setting.autologin.to_i (autologin の有効日数)。
func TokenValidity(action string, autologinDays int) time.Duration {
	switch action {
	case TokenAPI, TokenFeeds:
		return 0
	case TokenAutologin:
		return time.Duration(autologinDays) * 24 * time.Hour
	}
	// recovery / register / 未知のアクションは Token.validity_time (1 日)
	return 24 * time.Hour
}

// GenerateTokenValue は Token.generate_token_value (Redmine::Utils.random_hex(20) = 40 桁の 16 進小文字)。
func GenerateTokenValue() string {
	var b [20]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

type tokenRow struct {
	ID        int64       `db:"id"`
	UserID    int64       `db:"user_id"`
	Action    string      `db:"action"`
	Value     string      `db:"value"`
	CreatedAt db.Time     `db:"created_at"`
	UpdatedAt db.NullTime `db:"updated_at"`
}

func (r *tokenRow) token() *domain.Token {
	t := &domain.Token{ID: r.ID, UserID: r.UserID, Action: r.Action, Value: r.Value, CreatedAt: r.CreatedAt.Time}
	if r.UpdatedAt.Valid {
		t.UpdatedAt = r.UpdatedAt.Time
	}
	return t
}

// CreateToken は Token.create!(user:, action:) の移植。before_create の delete_previous_tokens
// (同じユーザ・アクションの古いトークンを max_instances - 1 件まで残して削除) を行ってから作る。
func CreateToken(ctx context.Context, q db.Queryer, userID int64, action string) (*domain.Token, error) {
	if max := TokenMaxInstances(action); max > 1 {
		var ids []int64
		if err := q.Select(ctx, &ids, `SELECT id FROM tokens WHERE user_id = ? AND action = ? ORDER BY updated_at DESC, id DESC`, userID, action); err != nil {
			return nil, err
		}
		if len(ids) > max-1 {
			for _, chunk := range chunkIDs(ids[max-1:]) {
				query, args, err := db.In(`DELETE FROM tokens WHERE id IN (?)`, chunk)
				if err != nil {
					return nil, err
				}
				if _, err := q.Exec(ctx, query, args...); err != nil {
					return nil, err
				}
			}
		}
	} else if _, err := q.Exec(ctx, `DELETE FROM tokens WHERE user_id = ? AND action = ?`, userID, action); err != nil {
		return nil, err
	}
	now := db.Now()
	t := &domain.Token{UserID: userID, Action: action, Value: GenerateTokenValue(), CreatedAt: now.Time, UpdatedAt: now.Time}
	id, err := q.InsertReturningID(ctx, `INSERT INTO tokens (user_id, action, value, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		userID, action, t.Value, now, now)
	if err != nil {
		return nil, err
	}
	t.ID = id
	return t, nil
}

var tokenKeyRe = regexp.MustCompile(`\A[a-zA-Z0-9]+\z`)

// FindToken は Token.find_token(action, key, validity_days) の移植。
// validityDays <= 0 は期限の確認をしない (Ruby の nil)。見つからなければ (nil, ErrNotFound)。
func FindToken(ctx context.Context, q db.Queryer, action, key string, validityDays int, now time.Time) (*domain.Token, error) {
	if action == "" || !tokenKeyRe.MatchString(key) {
		return nil, ErrNotFound
	}
	var r tokenRow
	if err := q.Get(ctx, &r, `SELECT id, user_id, action, value, created_at, updated_at FROM tokens WHERE action = ? AND value = ?`, action, key); err != nil {
		return nil, notFound(err)
	}
	if r.Action != action || subtle.ConstantTimeCompare([]byte(r.Value), []byte(key)) != 1 {
		return nil, ErrNotFound
	}
	if validityDays > 0 && !r.CreatedAt.After(now.AddDate(0, 0, -validityDays)) {
		return nil, ErrNotFound
	}
	return r.token(), nil
}

// FindActiveTokenUser は Token.find_active_user(action, key, validity_days) の移植
// (User.find_by_api_key / find_by_atom_key / find_by_autologin_key)。
// トークンが無い・期限切れ・ユーザが有効でなければ (nil, ErrNotFound)。
func FindActiveTokenUser(ctx context.Context, q db.Queryer, action, key string, validityDays int, now time.Time) (*domain.User, error) {
	t, err := FindToken(ctx, q, action, key, validityDays, now)
	if err != nil {
		return nil, err
	}
	u, err := FindActiveUser(ctx, q, t.UserID)
	if err != nil {
		return nil, err
	}
	// Redmine 7.0 #43938: キーを最後に使った日時を updated_on に記録する（書き込みを減らすため 1 分以内は更新しない）
	if t.UpdatedAt.IsZero() || !t.UpdatedAt.After(now.Add(-time.Minute)) {
		if _, err := q.Exec(ctx, `UPDATE tokens SET updated_at = ? WHERE id = ?`, db.NewTime(now), t.ID); err != nil {
			return nil, err
		}
	}
	return u, nil
}

// TokenUsed は Token#used?（作成後に使われたか: updated_on > created_on）。
func TokenUsed(t *domain.Token) bool {
	return !t.UpdatedAt.IsZero() && t.UpdatedAt.After(t.CreatedAt)
}

// DeleteToken はユーザのアクション・値が一致するトークンを消す (User#delete_autologin_token 等)。
func DeleteToken(ctx context.Context, q db.Queryer, userID int64, action, value string) error {
	_, err := q.Exec(ctx, `DELETE FROM tokens WHERE user_id = ? AND action = ? AND value = ?`, userID, action, value)
	return err
}

// ConsumeToken はアクション・値が一致するトークンを削除し、このリクエストが削除した（まだ使われていなかった）
// なら true を返す。並列のリクエストが同じ使い捨てトークンを同時に使えないようにするため、
// 検索と削除を分けずに DELETE の件数で判定する。
func ConsumeToken(ctx context.Context, q db.Queryer, action, value string) (bool, error) {
	if action == "" || !tokenKeyRe.MatchString(value) {
		return false, nil
	}
	res, err := q.Exec(ctx, `DELETE FROM tokens WHERE action = ? AND value = ?`, action, value)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeleteUserTokens はユーザのアクションのトークンをすべて消す (アクション "" なら全アクション)。
func DeleteUserTokens(ctx context.Context, q db.Queryer, userID int64, action string) error {
	if action == "" {
		_, err := q.Exec(ctx, `DELETE FROM tokens WHERE user_id = ?`, userID)
		return err
	}
	_, err := q.Exec(ctx, `DELETE FROM tokens WHERE user_id = ? AND action = ?`, userID, action)
	return err
}

// UserToken はユーザのアクションのトークン (最古のもの) を返す (User#atom_token / api_token)。
// 無ければ (nil, ErrNotFound)。
func UserToken(ctx context.Context, q db.Queryer, userID int64, action string) (*domain.Token, error) {
	var r tokenRow
	if err := q.Get(ctx, &r, `SELECT id, user_id, action, value, created_at, updated_at FROM tokens
WHERE user_id = ? AND action = ? ORDER BY id LIMIT 1`, userID, action); err != nil {
		return nil, notFound(err)
	}
	return r.token(), nil
}

// userTokenValue はトークンの値を返し、無ければ作成する。
func userTokenValue(ctx context.Context, q db.Queryer, userID int64, action string) (string, error) {
	t, err := UserToken(ctx, q, userID, action)
	if err == nil {
		return t.Value, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return "", err
	}
	t, err = CreateToken(ctx, q, userID, action)
	if err != nil {
		return "", err
	}
	return t.Value, nil
}

// AtomKey は User#atom_key (feeds トークン。無ければ作成する)。
func AtomKey(ctx context.Context, q db.Queryer, userID int64) (string, error) {
	return userTokenValue(ctx, q, userID, TokenFeeds)
}

// APIKey は User#api_key (api トークン。無ければ作成する)。
func APIKey(ctx context.Context, q db.Queryer, userID int64) (string, error) {
	return userTokenValue(ctx, q, userID, TokenAPI)
}

// DestroyExpiredTokens は Token.destroy_expired の移植。削除件数を返す。
func DestroyExpiredTokens(ctx context.Context, q db.Queryer, now time.Time, autologinDays int) (int64, error) {
	var total int64
	known := []string{}
	for action := range tokenMaxInstances {
		known = append(known, action)
		v := TokenValidity(action, autologinDays)
		if v == 0 {
			continue
		}
		res, err := q.Exec(ctx, `DELETE FROM tokens WHERE action = ? AND created_at < ?`, action, db.NewTime(now.Add(-v)))
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	query, args, err := db.In(`DELETE FROM tokens WHERE action NOT IN (?) AND created_at < ?`, known, db.NewTime(now.Add(-24*time.Hour)))
	if err != nil {
		return total, err
	}
	res, err := q.Exec(ctx, query, args...)
	if err != nil {
		return total, err
	}
	n, _ := res.RowsAffected()
	return total + n, nil
}
