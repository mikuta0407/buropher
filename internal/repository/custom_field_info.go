package repository

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// このファイルはトラッカー・列挙の管理画面が使うカスタムフィールドの読み書き（軽量版）。
// TODO: カスタムフィールド管理の移植で完全な repository ができたら統合する。

type customFieldInfoRow struct {
	ID             int64          `db:"id"`
	OwnerKind      string         `db:"owner_kind"`
	Name           string         `db:"name"`
	Description    sql.NullString `db:"description"`
	FieldFormat    string         `db:"field_format"`
	IsRequired     bool           `db:"is_required"`
	IsForAll       bool           `db:"is_for_all"`
	Visible        bool           `db:"visible"`
	Multiple       bool           `db:"multiple"`
	DefaultValue   sql.NullString `db:"default_value"`
	Position       int            `db:"position"`
	PossibleValues sql.NullString `db:"possible_values"`
	FormatSettings string         `db:"format_settings"`
}

func (r *customFieldInfoRow) info() *domain.CustomFieldInfo {
	c := &domain.CustomFieldInfo{ID: r.ID, OwnerKind: r.OwnerKind, Name: r.Name, FieldFormat: r.FieldFormat,
		IsRequired: r.IsRequired, IsForAll: r.IsForAll, Visible: r.Visible, Multiple: r.Multiple, Position: r.Position}
	if r.Description.Valid {
		s := r.Description.String
		c.Description = &s
	}
	if r.DefaultValue.Valid {
		s := r.DefaultValue.String
		c.DefaultValue = &s
	}
	if r.PossibleValues.Valid {
		_ = json.Unmarshal([]byte(r.PossibleValues.String), &c.PossibleValues)
	}
	var fs map[string]any
	if json.Unmarshal([]byte(r.FormatSettings), &fs) == nil {
		if s, ok := fs["edit_tag_style"].(string); ok {
			c.EditTagStyle = s
		}
	}
	return c
}

// CustomFieldInfosByKind は owner_kind のカスタムフィールドを CustomField.sorted（position 順）で返す。
func CustomFieldInfosByKind(ctx context.Context, q db.Queryer, ownerKind string) ([]*domain.CustomFieldInfo, error) {
	var rows []customFieldInfoRow
	if err := q.Select(ctx, &rows, `SELECT id, owner_kind, name, description, field_format, is_required, is_for_all, visible,
  multiple, default_value, position, possible_values, format_settings FROM custom_fields WHERE owner_kind = ? ORDER BY position, id`, ownerKind); err != nil {
		return nil, err
	}
	out := make([]*domain.CustomFieldInfo, len(rows))
	for i := range rows {
		out[i] = rows[i].info()
	}
	return out, nil
}

// CustomFieldsTrackers は custom_fields_trackers の全行（tracker_id → custom_field_id の集合）。
func CustomFieldsTrackers(ctx context.Context, q db.Queryer) (map[int64]map[int64]bool, error) {
	var rows []struct {
		CustomFieldID int64 `db:"custom_field_id"`
		TrackerID     int64 `db:"tracker_id"`
	}
	if err := q.Select(ctx, &rows, `SELECT custom_field_id, tracker_id FROM custom_fields_trackers`); err != nil {
		return nil, err
	}
	out := map[int64]map[int64]bool{}
	for _, r := range rows {
		if out[r.TrackerID] == nil {
			out[r.TrackerID] = map[int64]bool{}
		}
		out[r.TrackerID][r.CustomFieldID] = true
	}
	return out, nil
}

// CustomValues は customized_kind / customized_id のカスタム値（custom_field_id → 値の列、id 順）。
func CustomValues(ctx context.Context, q db.Queryer, kind string, id int64) (map[int64][]string, error) {
	var rows []struct {
		CustomFieldID int64          `db:"custom_field_id"`
		Value         sql.NullString `db:"value"`
	}
	if err := q.Select(ctx, &rows, `SELECT custom_field_id, value FROM custom_values WHERE customized_kind = ? AND customized_id = ? ORDER BY id`, kind, id); err != nil {
		return nil, err
	}
	out := map[int64][]string{}
	for _, r := range rows {
		if r.Value.Valid {
			out[r.CustomFieldID] = append(out[r.CustomFieldID], r.Value.String)
		} else if _, ok := out[r.CustomFieldID]; !ok {
			out[r.CustomFieldID] = []string{}
		}
	}
	return out, nil
}

// SetCustomValues は 1 フィールド分のカスタム値を置き換える（空の配列なら NULL の 1 行）。
func SetCustomValues(ctx context.Context, q db.Queryer, kind string, id, cfID int64, values []string) error {
	if _, err := q.Exec(ctx, `DELETE FROM custom_values WHERE customized_kind = ? AND customized_id = ? AND custom_field_id = ?`, kind, id, cfID); err != nil {
		return err
	}
	if len(values) == 0 {
		_, err := q.Exec(ctx, `INSERT INTO custom_values (customized_kind, customized_id, custom_field_id, value) VALUES (?, ?, ?, NULL)`, kind, id, cfID)
		return err
	}
	for _, v := range values {
		if _, err := q.Exec(ctx, `INSERT INTO custom_values (customized_kind, customized_id, custom_field_id, value) VALUES (?, ?, ?, ?)`, kind, id, cfID, v); err != nil {
			return err
		}
	}
	return nil
}

// DeleteCustomValues は customized の全カスタム値を削除する（acts_as_customizable の dependent: :delete_all）。
func DeleteCustomValues(ctx context.Context, q db.Queryer, kind string, id int64) error {
	_, err := q.Exec(ctx, `DELETE FROM custom_values WHERE customized_kind = ? AND customized_id = ?`, kind, id)
	return err
}
