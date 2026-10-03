// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// 列挙（Enumeration の STI）の読み書き。種類ごとに別テーブルだが id は種類をまたいで一意に保つ。

// EnumerationTable は種類に対応するテーブル名（不明なら空）。
func EnumerationTable(kind string) string {
	switch kind {
	case domain.EnumIssuePriority:
		return "issue_priorities"
	case domain.EnumTimeEntryActivity:
		return "time_entry_activities"
	case domain.EnumDocumentCategory:
		return "document_categories"
	}
	return ""
}

func enumerationSelect(kind string) string {
	switch kind {
	case domain.EnumIssuePriority:
		return `SELECT id, name, position, is_default, active, NULL AS project_id, NULL AS parent_id, position_name FROM issue_priorities`
	case domain.EnumTimeEntryActivity:
		return `SELECT id, name, position, is_default, active, project_id, parent_id, NULL AS position_name FROM time_entry_activities`
	default:
		return `SELECT id, name, position, is_default, active, NULL AS project_id, NULL AS parent_id, NULL AS position_name FROM document_categories`
	}
}

type enumerationRow struct {
	ID           int64          `db:"id"`
	Name         string         `db:"name"`
	Position     int            `db:"position"`
	IsDefault    bool           `db:"is_default"`
	Active       bool           `db:"active"`
	ProjectID    sql.NullInt64  `db:"project_id"`
	ParentID     sql.NullInt64  `db:"parent_id"`
	PositionName sql.NullString `db:"position_name"`
}

func (r *enumerationRow) enumeration(kind string) *domain.Enumeration {
	e := &domain.Enumeration{ID: r.ID, Kind: kind, Name: r.Name, Position: r.Position, IsDefault: r.IsDefault,
		Active: r.Active, ProjectID: nullID(r.ProjectID), ParentID: nullID(r.ParentID)}
	if r.PositionName.Valid {
		s := r.PositionName.String
		e.PositionName = &s
	}
	return e
}

// ListEnumerations は種類の列挙を position 順で返す。shared なら project_id IS NULL（klass.shared / system）のみ。
func ListEnumerations(ctx context.Context, q db.Queryer, kind string, shared bool) ([]*domain.Enumeration, error) {
	if EnumerationTable(kind) == "" {
		return nil, fmt.Errorf("repository: unknown enumeration kind %q", kind)
	}
	query := enumerationSelect(kind)
	if shared && kind == domain.EnumTimeEntryActivity {
		query += ` WHERE project_id IS NULL`
	}
	var rows []enumerationRow
	if err := q.Select(ctx, &rows, query+` ORDER BY position, id`); err != nil {
		return nil, err
	}
	out := make([]*domain.Enumeration, len(rows))
	for i := range rows {
		out[i] = rows[i].enumeration(kind)
	}
	return out, nil
}

// GetEnumeration は Enumeration.find(id)（3 テーブルを探す）。
func GetEnumeration(ctx context.Context, q db.Queryer, id int64) (*domain.Enumeration, error) {
	for _, kind := range domain.EnumerationKinds {
		var r enumerationRow
		err := q.Get(ctx, &r, enumerationSelect(kind)+` WHERE id = ?`, id)
		if err == nil {
			return r.enumeration(kind), nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	return nil, ErrNotFound
}

// GetEnumerationOfKind は klass.find_by_id(id)（種類が違えば ErrNotFound）。
func GetEnumerationOfKind(ctx context.Context, q db.Queryer, kind string, id int64) (*domain.Enumeration, error) {
	e, err := GetEnumeration(ctx, q, id)
	if err != nil {
		return nil, err
	}
	if e.Kind != kind {
		return nil, ErrNotFound
	}
	return e, nil
}

// EnumerationNameTaken は validates_uniqueness_of :name, scope: [:type, :project_id]（大文字小文字を区別）。
func EnumerationNameTaken(ctx context.Context, q db.Queryer, e *domain.Enumeration) (bool, error) {
	table := EnumerationTable(e.Kind)
	var n int
	var err error
	if e.Kind == domain.EnumTimeEntryActivity {
		var pid int64
		if e.ProjectID != nil {
			pid = *e.ProjectID
		}
		err = q.Get(ctx, &n, `SELECT COUNT(*) FROM time_entry_activities WHERE name = ? AND COALESCE(project_id, 0) = ? AND id <> ?`, e.Name, pid, e.ID)
	} else {
		err = q.Get(ctx, &n, `SELECT COUNT(*) FROM `+table+` WHERE name = ? AND id <> ?`, e.Name, e.ID)
	}
	return n > 0, err
}

// EnumerationPositionScope は Enumeration の acts_as_positioned（scope: [:project_id, :parent_id]、種類ごと）。
func EnumerationPositionScope(e *domain.Enumeration) PositionScope {
	s := PositionScope{Table: EnumerationTable(e.Kind)}
	if e.Kind == domain.EnumTimeEntryActivity {
		s.Where = `COALESCE(project_id, 0) = ? AND COALESCE(parent_id, 0) = ?`
		var pid, parent int64
		if e.ProjectID != nil {
			pid = *e.ProjectID
		}
		if e.ParentID != nil {
			parent = *e.ParentID
		}
		s.Args = []any{pid, parent}
	}
	return s
}

// NextEnumerationID は新しい列挙の id（3 テーブルの最大値 + 1。Redmine の enumerations.id と同じく種類をまたいで一意）。
func NextEnumerationID(ctx context.Context, q db.Queryer) (int64, error) {
	var n int64
	err := q.Get(ctx, &n, `SELECT MAX(m) + 1 FROM (
  SELECT COALESCE(MAX(id), 0) AS m FROM issue_priorities
  UNION ALL SELECT COALESCE(MAX(id), 0) FROM time_entry_activities
  UNION ALL SELECT COALESCE(MAX(id), 0) FROM document_categories) t`)
	return n, err
}

// SaveEnumeration は列挙の行を保存する（e.ID == 0 なら NextEnumerationID で作成）。
// is_default / position / 子の名前などのコールバックは呼び出し側で行う。
func SaveEnumeration(ctx context.Context, q db.Queryer, e *domain.Enumeration) error {
	table := EnumerationTable(e.Kind)
	if table == "" {
		return fmt.Errorf("repository: unknown enumeration kind %q", e.Kind)
	}
	if e.ID == 0 {
		id, err := NextEnumerationID(ctx, q)
		if err != nil {
			return err
		}
		switch e.Kind {
		case domain.EnumTimeEntryActivity:
			_, err = q.Exec(ctx, `INSERT INTO time_entry_activities (id, name, position, is_default, active, project_id, parent_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				id, e.Name, e.Position, e.IsDefault, e.Active, e.ProjectID, e.ParentID)
		default:
			_, err = q.Exec(ctx, `INSERT INTO `+table+` (id, name, position, is_default, active) VALUES (?, ?, ?, ?, ?)`,
				id, e.Name, e.Position, e.IsDefault, e.Active)
		}
		if err != nil {
			return err
		}
		e.ID = id
		return q.Dialect().ResetSequence(ctx, q, table)
	}
	_, err := q.Exec(ctx, `UPDATE `+table+` SET name = ?, position = ?, is_default = ?, active = ? WHERE id = ?`,
		e.Name, e.Position, e.IsDefault, e.Active, e.ID)
	return err
}

// ClearEnumerationDefaults は check_default（同じ種類の is_default をすべて外す）。
func ClearEnumerationDefaults(ctx context.Context, q db.Queryer, kind string) error {
	_, err := q.Exec(ctx, `UPDATE `+EnumerationTable(kind)+` SET is_default = ?`, false)
	return err
}

// UpdateActivityChildrenName は update_children_name（共有の作業分類の名前変更をプロジェクト別の上書き行に反映する）。
func UpdateActivityChildrenName(ctx context.Context, q db.Queryer, id int64, oldName, newName string) error {
	_, err := q.Exec(ctx, `UPDATE time_entry_activities SET name = ? WHERE name = ? AND parent_id = ?`, newName, oldName, id)
	return err
}

// SyncActivityOverridePositions は Enumeration#update_position の後半
// （上書き行の position を親と同じにする。親が無ければ 1）。
func SyncActivityOverridePositions(ctx context.Context, q db.Queryer) error {
	_, err := q.Exec(ctx, `UPDATE time_entry_activities SET position = COALESCE(
  (SELECT p.position FROM time_entry_activities p WHERE p.id = time_entry_activities.parent_id), 1)
  WHERE parent_id IS NOT NULL`)
	return err
}

// EnumerationObjectsCount は objects_count（使用しているチケット・作業時間・文書の数）。
func EnumerationObjectsCount(ctx context.Context, q db.Queryer, e *domain.Enumeration) (int, error) {
	var n int
	var err error
	switch e.Kind {
	case domain.EnumIssuePriority:
		err = q.Get(ctx, &n, `SELECT COUNT(*) FROM issues WHERE priority_id = ?`, e.ID)
	case domain.EnumTimeEntryActivity:
		// self_and_descendants(1): 自身と直下の上書き行
		err = q.Get(ctx, &n, `SELECT COUNT(*) FROM time_entries WHERE activity_id = ? OR activity_id IN (SELECT id FROM time_entry_activities WHERE parent_id = ?)`, e.ID, e.ID)
	case domain.EnumDocumentCategory:
		err = q.Get(ctx, &n, `SELECT COUNT(*) FROM documents WHERE category_id = ?`, e.ID)
	}
	return n, err
}

// TransferEnumerationRelations は transfer_relations(to)（使用箇所を to に付け替える）。
func TransferEnumerationRelations(ctx context.Context, q db.Queryer, e *domain.Enumeration, to *domain.Enumeration) error {
	var err error
	switch e.Kind {
	case domain.EnumIssuePriority:
		_, err = q.Exec(ctx, `UPDATE issues SET priority_id = ? WHERE priority_id = ?`, to.ID, e.ID)
	case domain.EnumTimeEntryActivity:
		_, err = q.Exec(ctx, `UPDATE time_entries SET activity_id = ? WHERE activity_id = ? OR activity_id IN (SELECT id FROM time_entry_activities WHERE parent_id = ?)`, to.ID, e.ID, e.ID)
	case domain.EnumDocumentCategory:
		_, err = q.Exec(ctx, `UPDATE documents SET category_id = ? WHERE category_id = ?`, to.ID, e.ID)
	}
	return err
}

// DestroyEnumeration は列挙の行とカスタム値を削除し、position を詰める（上書き行は詰めない）。
// IssuePriority なら compute_position_names を実行する。使用中かどうかの確認は呼び出し側。
func DestroyEnumeration(ctx context.Context, q db.Queryer, e *domain.Enumeration) error {
	if e.Kind == domain.EnumTimeEntryActivity {
		var children []int64
		if err := q.Select(ctx, &children, `SELECT id FROM time_entry_activities WHERE parent_id = ?`, e.ID); err != nil {
			return err
		}
		for _, c := range children {
			if err := DeleteCustomValues(ctx, q, "enumeration", c); err != nil {
				return err
			}
		}
		if _, err := q.Exec(ctx, `DELETE FROM time_entry_activities WHERE parent_id = ?`, e.ID); err != nil {
			return err
		}
	}
	if err := DeleteCustomValues(ctx, q, "enumeration", e.ID); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `DELETE FROM `+EnumerationTable(e.Kind)+` WHERE id = ?`, e.ID); err != nil {
		return err
	}
	if e.ParentID == nil {
		if err := RemovePosition(ctx, q, EnumerationPositionScope(e), e.ID, e.Position); err != nil {
			return err
		}
	}
	if e.Kind == domain.EnumIssuePriority {
		return ComputePriorityPositionNames(ctx, q)
	}
	return nil
}
