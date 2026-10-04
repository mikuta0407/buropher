// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package repository は DB アクセス層（テーブル単位の読み書き）。
package repository

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
)

// SettingsStore は settings テーブルを使う settings.Store 実装。
type SettingsStore struct{ DB db.Queryer }

func (s SettingsStore) LoadAll(ctx context.Context) (map[string]json.RawMessage, error) {
	var rows []struct {
		Name  string `db:"name"`
		Value string `db:"value"`
	}
	if err := s.DB.Select(ctx, &rows, `SELECT name, value FROM settings`); err != nil {
		return nil, err
	}
	out := make(map[string]json.RawMessage, len(rows))
	for _, r := range rows {
		out[r.Name] = json.RawMessage(r.Value)
	}
	return out, nil
}

func (s SettingsStore) Save(ctx context.Context, name string, value json.RawMessage) error {
	// updated_at は settings の版（Version）を兼ねるので、既存の最大値より必ず大きくする
	// （時刻を固定した環境や同じマイクロ秒内の連続した変更でも、他のプロセスが変更を検出できるように）
	now := db.Now()
	var latest db.NullTime
	if err := s.DB.Get(ctx, &latest, `SELECT MAX(updated_at) FROM settings`); err != nil {
		return err
	}
	if latest.Valid && !now.After(latest.Time) {
		now = db.NewTime(latest.Time.Add(time.Microsecond))
	}
	q := s.DB.Dialect().Upsert("settings", []string{"name", "value", "updated_at"}, []string{"name"}, []string{"value", "updated_at"})
	_, err := s.DB.Exec(ctx, q, name, string(value), now)
	return err
}

// Version は settings テーブルの版（行数と updated_at の最大値）を返す（Setting.check_cache が
// 比べる Setting.maximum(:updated_on) 相当）。他のプロセスが設定を変更すると変わる。
func (s SettingsStore) Version(ctx context.Context) (string, error) {
	var r struct {
		N      int64       `db:"n"`
		Latest db.NullTime `db:"latest"`
	}
	if err := s.DB.Get(ctx, &r, `SELECT COUNT(*) AS n, MAX(updated_at) AS latest FROM settings`); err != nil {
		return "", err
	}
	v := strconv.FormatInt(r.N, 10)
	if r.Latest.Valid {
		v += "/" + db.FormatTime(r.Latest.Time)
	}
	return v, nil
}
