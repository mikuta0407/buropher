// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository

import (
	"context"
	"encoding/json"

	"github.com/mikuta0407/buropher/internal/db"
)

// UserPrefExtra は user_preferences.extra（UserPreference#others の未知キー: activity_scope 等）の値を返す。
// 行もキーも無ければ nil。
func UserPrefExtra(ctx context.Context, q db.Queryer, userID int64, key string) (any, error) {
	m, _, err := userPrefExtraMap(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	return m[key], nil
}

// UserPrefExtraStrings は extra[key] を文字列の配列として返す（配列でなければ nil）。
func UserPrefExtraStrings(ctx context.Context, q db.Queryer, userID int64, key string) ([]string, error) {
	v, err := UserPrefExtra(ctx, q, userID, key)
	if err != nil {
		return nil, err
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out, nil
}

// SetUserPrefExtra は extra[key] = value を保存する（pref.save。行が無ければ既定値で作る）。
func SetUserPrefExtra(ctx context.Context, q db.Queryer, userID int64, key string, value any) error {
	m, exists, err := userPrefExtraMap(ctx, q, userID)
	if err != nil {
		return err
	}
	if m == nil {
		m = map[string]any{}
	}
	m[key] = value
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if exists {
		_, err = q.Exec(ctx, `UPDATE user_preferences SET extra = ? WHERE user_id = ?`, string(b), userID)
		return err
	}
	_, err = q.Exec(ctx, `INSERT INTO user_preferences (user_id, extra) VALUES (?, ?)`, userID, string(b))
	return err
}

func userPrefExtraMap(ctx context.Context, q db.Queryer, userID int64) (map[string]any, bool, error) {
	var raw []string
	if err := q.Select(ctx, &raw, `SELECT extra FROM user_preferences WHERE user_id = ?`, userID); err != nil {
		return nil, false, err
	}
	if len(raw) == 0 {
		return nil, false, nil
	}
	m := map[string]any{}
	if raw[0] != "" {
		if err := json.Unmarshal([]byte(raw[0]), &m); err != nil {
			return nil, true, nil //nolint:nilerr // 壊れた JSON は空の設定として扱う
		}
	}
	return m, true, nil
}
