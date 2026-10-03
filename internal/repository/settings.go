// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package repository は DB アクセス層（テーブル単位の読み書き）。
package repository

import (
	"context"
	"encoding/json"

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
	q := s.DB.Dialect().Upsert("settings", []string{"name", "value", "updated_at"}, []string{"name"}, []string{"value", "updated_at"})
	_, err := s.DB.Exec(ctx, q, name, string(value), db.Now())
	return err
}
