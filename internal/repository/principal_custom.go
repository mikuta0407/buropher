// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/mikuta0407/buropher/internal/db"
)

// このファイルはユーザー・グループのカスタムフィールド（UserCustomField / GroupCustomField）の
// 表示・単一値の保存に必要な最小限の読み書き。
// TODO(custom_fields): カスタムフィールドの汎用実装（internal/customfield）に置き換える。

// PrincipalCustomField は UserCustomField / GroupCustomField の表示・入力に使う列。
type PrincipalCustomField struct {
	ID           int64          `db:"id"`
	Name         string         `db:"name"`
	Description  sql.NullString `db:"description"`
	FieldFormat  string         `db:"field_format"`
	IsRequired   bool           `db:"is_required"`
	IsFilter     bool           `db:"is_filter"`
	Visible      bool           `db:"visible"`
	Editable     bool           `db:"editable"`
	Multiple     bool           `db:"multiple"`
	DefaultValue sql.NullString `db:"default_value"`
	Regexp       sql.NullString `db:"regexp"`
	MinLength    sql.NullInt64  `db:"min_length"`
	MaxLength    sql.NullInt64  `db:"max_length"`
	Position     int            `db:"position"`
	FormatJSON   string         `db:"format_settings"`
}

// CSSClasses は CustomField#css_classes。
func (f *PrincipalCustomField) CSSClasses() string {
	return f.FieldFormat + "_cf cf_" + fmt.Sprint(f.ID)
}

// FormatSetting は format_store の値（無ければ ""）。
func (f *PrincipalCustomField) FormatSetting(key string) string {
	var m map[string]any
	if json.Unmarshal([]byte(f.FormatJSON), &m) != nil {
		return ""
	}
	if v, ok := m[key]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
}

// PrincipalCustomFields は owner_kind（user / group）のカスタムフィールドを position 順に返す（CustomField.sorted）。
func PrincipalCustomFields(ctx context.Context, q db.Queryer, ownerKind string) ([]*PrincipalCustomField, error) {
	var rows []*PrincipalCustomField
	err := q.Select(ctx, &rows, `SELECT id, name, description, field_format, is_required, is_filter, visible, editable, multiple,
  default_value, regexp, min_length, max_length, position, format_settings
FROM custom_fields WHERE owner_kind = ? ORDER BY position, id`, ownerKind)
	return rows, err
}

// PrincipalCustomValues は principals の custom_values を principal id → custom_field_id → 値（id 順）で返す。
func PrincipalCustomValues(ctx context.Context, q db.Queryer, ids []int64) (map[int64]map[int64][]sql.NullString, error) {
	out := map[int64]map[int64][]sql.NullString{}
	for _, chunk := range chunkIDs(uniqIDs(ids)) {
		query, args, err := db.In(`SELECT customized_id, custom_field_id, value FROM custom_values
WHERE customized_kind = 'principal' AND customized_id IN (?) ORDER BY id`, chunk)
		if err != nil {
			return nil, err
		}
		var rows []struct {
			CustomizedID  int64          `db:"customized_id"`
			CustomFieldID int64          `db:"custom_field_id"`
			Value         sql.NullString `db:"value"`
		}
		if err := q.Select(ctx, &rows, query, args...); err != nil {
			return nil, err
		}
		for _, r := range rows {
			if out[r.CustomizedID] == nil {
				out[r.CustomizedID] = map[int64][]sql.NullString{}
			}
			out[r.CustomizedID][r.CustomFieldID] = append(out[r.CustomizedID][r.CustomFieldID], r.Value)
		}
	}
	return out, nil
}

// SetPrincipalCustomValue は単一値のカスタム値を保存する（行が無ければ作成）。変更があれば true。
func SetPrincipalCustomValue(ctx context.Context, q db.Queryer, principalID, fieldID int64, value string) (bool, error) {
	var cur []sql.NullString
	if err := q.Select(ctx, &cur, `SELECT value FROM custom_values WHERE customized_kind = 'principal' AND customized_id = ? AND custom_field_id = ? ORDER BY id`,
		principalID, fieldID); err != nil {
		return false, err
	}
	if len(cur) == 1 && cur[0].Valid && cur[0].String == value {
		return false, nil
	}
	if _, err := q.Exec(ctx, `DELETE FROM custom_values WHERE customized_kind = 'principal' AND customized_id = ? AND custom_field_id = ?`, principalID, fieldID); err != nil {
		return false, err
	}
	_, err := q.Exec(ctx, `INSERT INTO custom_values (customized_kind, customized_id, custom_field_id, value) VALUES ('principal', ?, ?, ?)`,
		principalID, fieldID, value)
	return err == nil, err
}

// SetPrincipalCustomValues は複数値のカスタム値を保存する（1 値 1 行。値が無ければ NULL の 1 行。
// save_custom_field_values と同じく、既存の値と同じ集合なら何もしない）。変更があれば true。
func SetPrincipalCustomValues(ctx context.Context, q db.Queryer, principalID, fieldID int64, values []string) (bool, error) {
	var cur []sql.NullString
	if err := q.Select(ctx, &cur, `SELECT value FROM custom_values WHERE customized_kind = 'principal' AND customized_id = ? AND custom_field_id = ? ORDER BY id`,
		principalID, fieldID); err != nil {
		return false, err
	}
	var curVals []string
	for _, c := range cur {
		if c.String != "" {
			curVals = append(curVals, c.String)
		}
	}
	if len(cur) > 0 && slices.Equal(curVals, values) {
		return false, nil
	}
	if _, err := q.Exec(ctx, `DELETE FROM custom_values WHERE customized_kind = 'principal' AND customized_id = ? AND custom_field_id = ?`, principalID, fieldID); err != nil {
		return false, err
	}
	if len(values) == 0 {
		values = []string{""}
	}
	for _, v := range values {
		if _, err := q.Exec(ctx, `INSERT INTO custom_values (customized_kind, customized_id, custom_field_id, value) VALUES ('principal', ?, ?, ?)`,
			principalID, fieldID, nullString(v)); err != nil {
			return false, err
		}
	}
	return true, nil
}

// HighPriorityAfterDefault は users/_mail_notifications の
// IssuePriority.where('position > ?', IssuePriority.default_or_middle.position).first の名前（無ければ ""）。
func HighPriorityAfterDefault(ctx context.Context, q db.Queryer) (string, error) {
	var prios []struct {
		ID        int64  `db:"id"`
		Name      string `db:"name"`
		Position  int    `db:"position"`
		IsDefault bool   `db:"is_default"`
		Active    bool   `db:"active"`
	}
	if err := q.Select(ctx, &prios, `SELECT id, name, position, is_default, active FROM issue_priorities ORDER BY position, id`); err != nil {
		return "", err
	}
	// IssuePriority.default_or_middle: 既定の優先度、無ければ active なものの中央
	def := -1
	var active []int
	for i := range prios {
		if prios[i].Active {
			active = append(active, i)
		}
		if prios[i].IsDefault && def < 0 {
			def = i
		}
	}
	if def < 0 {
		if len(active) == 0 {
			return "", nil
		}
		def = active[(len(active)-1)/2]
	}
	// .first は主キー順
	pos := prios[def].Position
	name, best := "", int64(-1)
	for _, p := range prios {
		if p.Position > pos && (best < 0 || p.ID < best) {
			name, best = p.Name, p.ID
		}
	}
	return name, nil
}
