// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
)

// Import は imports の行（Redmine の Import / IssueImport / TimeEntryImport / UserImport）。
type Import struct {
	ID int64 `db:"id"`
	// Kind は STI の type（issue / time_entry / user）。
	Kind     string `db:"kind"`
	UserID   int64  `db:"user_id"`
	Filename string `db:"filename"`
	// Settings は serialize :settings（separator, wrapper, encoding, date_format, notifications, mapping, callbacks）。
	Settings   db.JSON[map[string]any] `db:"settings"`
	TotalItems *int                    `db:"total_items"`
	Finished   bool                    `db:"finished"`
	CreatedAt  db.Time                 `db:"created_at"`
	UpdatedAt  db.Time                 `db:"updated_at"`
}

// ImportItem は import_items の行（ImportItem）。
type ImportItem struct {
	ID       int64          `db:"id"`
	ImportID int64          `db:"import_id"`
	Position int            `db:"position"`
	ObjID    *int64         `db:"obj_id"`
	Message  sql.NullString `db:"message"`
	UniqueID sql.NullString `db:"unique_id"`
}

const importCols = `id, kind, user_id, COALESCE(filename, '') AS filename, settings, total_items, finished, created_at, updated_at`

// InsertImport はインポートを作成し id を設定する。
func InsertImport(ctx context.Context, q db.Queryer, imp *Import, now time.Time) error {
	if imp.Settings.V == nil {
		imp.Settings.V = map[string]any{}
	}
	t := db.NewTime(now)
	id, err := q.InsertReturningID(ctx, `INSERT INTO imports (kind, user_id, filename, settings, total_items, finished, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, imp.Kind, imp.UserID, nullString(imp.Filename), imp.Settings, imp.TotalItems, imp.Finished, t, t)
	if err != nil {
		return err
	}
	imp.ID, imp.CreatedAt, imp.UpdatedAt = id, t, t
	return nil
}

// UpdateImport は settings / total_items / finished を保存する（updated_at を now にする）。
func UpdateImport(ctx context.Context, q db.Queryer, imp *Import, now time.Time) error {
	if imp.Settings.V == nil {
		imp.Settings.V = map[string]any{}
	}
	t := db.NewTime(now)
	_, err := q.Exec(ctx, `UPDATE imports SET settings = ?, total_items = ?, finished = ?, updated_at = ? WHERE id = ?`,
		imp.Settings, imp.TotalItems, imp.Finished, t, imp.ID)
	if err == nil {
		imp.UpdatedAt = t
	}
	return err
}

// FindImport は Import.where(:user_id => userID, :filename => filename).first（無ければ ErrNotFound）。
func FindImport(ctx context.Context, q db.Queryer, userID int64, filename string) (*Import, error) {
	var imp Import
	err := q.Get(ctx, &imp, `SELECT `+importCols+` FROM imports WHERE user_id = ? AND filename = ? ORDER BY id LIMIT 1`, userID, filename)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &imp, nil
}

// ImportItemsMaxPosition は items.maximum(:position)（無ければ 0）。
func ImportItemsMaxPosition(ctx context.Context, q db.Queryer, importID int64) (int, error) {
	var n sql.NullInt64
	err := q.Get(ctx, &n, `SELECT MAX(position) FROM import_items WHERE import_id = ?`, importID)
	return int(n.Int64), err
}

// InsertImportItem は ImportItem を保存する。
func InsertImportItem(ctx context.Context, q db.Queryer, it *ImportItem) error {
	id, err := q.InsertReturningID(ctx, `INSERT INTO import_items (import_id, position, obj_id, message, unique_id) VALUES (?, ?, ?, ?, ?)`,
		it.ImportID, it.Position, it.ObjID, it.Message, it.UniqueID)
	if err == nil {
		it.ID = id
	}
	return err
}

// ImportItemObjIDByUniqueID は items.where(:unique_id => uid).first.try(:obj_id)。
func ImportItemObjIDByUniqueID(ctx context.Context, q db.Queryer, importID int64, uid string) (*int64, error) {
	return importItemObjID(ctx, q, `SELECT obj_id FROM import_items WHERE import_id = ? AND unique_id = ? ORDER BY id LIMIT 1`, importID, uid)
}

// ImportItemObjIDByPosition は items.where(:position => pos).first.try(:obj_id)。
func ImportItemObjIDByPosition(ctx context.Context, q db.Queryer, importID int64, pos int) (*int64, error) {
	return importItemObjID(ctx, q, `SELECT obj_id FROM import_items WHERE import_id = ? AND position = ? ORDER BY id LIMIT 1`, importID, pos)
}

func importItemObjID(ctx context.Context, q db.Queryer, query string, args ...any) (*int64, error) {
	var id sql.NullInt64
	err := q.Get(ctx, &id, query, args...)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !id.Valid {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &id.Int64, nil
}

// CountImportItems は saved_items.count（saved = true）/ unsaved_items.count。
func CountImportItems(ctx context.Context, q db.Queryer, importID int64, saved bool) (int, error) {
	cond := "obj_id IS NULL"
	if saved {
		cond = "obj_id IS NOT NULL"
	}
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM import_items WHERE import_id = ? AND `+cond, importID)
	return n, err
}

// SavedImportObjIDs は saved_items.pluck(:obj_id)。
func SavedImportObjIDs(ctx context.Context, q db.Queryer, importID int64) ([]int64, error) {
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT obj_id FROM import_items WHERE import_id = ? AND obj_id IS NOT NULL ORDER BY id`, importID)
	return ids, err
}

// UnsavedImportItems は unsaved_items（id 順）。
func UnsavedImportItems(ctx context.Context, q db.Queryer, importID int64) ([]*ImportItem, error) {
	var items []*ImportItem
	err := q.Select(ctx, &items, `SELECT id, import_id, position, obj_id, message, unique_id FROM import_items
WHERE import_id = ? AND obj_id IS NULL ORDER BY id`, importID)
	return items, err
}

// ImportItemsByImport は items（id 順）。
func ImportItemsByImport(ctx context.Context, q db.Queryer, importID int64) ([]*ImportItem, error) {
	var items []*ImportItem
	err := q.Select(ctx, &items, `SELECT id, import_id, position, obj_id, message, unique_id FROM import_items
WHERE import_id = ? ORDER BY id`, importID)
	return items, err
}
