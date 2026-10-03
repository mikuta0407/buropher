// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

type issueStatusRow struct {
	ID               int64          `db:"id"`
	Name             string         `db:"name"`
	Description      sql.NullString `db:"description"`
	IsClosed         bool           `db:"is_closed"`
	Position         int            `db:"position"`
	DefaultDoneRatio sql.NullInt64  `db:"default_done_ratio"`
}

func (r *issueStatusRow) status() *domain.IssueStatus {
	s := &domain.IssueStatus{ID: r.ID, Name: r.Name, IsClosed: r.IsClosed, Position: r.Position}
	if r.Description.Valid {
		d := r.Description.String
		s.Description = &d
	}
	if r.DefaultDoneRatio.Valid {
		v := int(r.DefaultDoneRatio.Int64)
		s.DefaultDoneRatio = &v
	}
	return s
}

// IssueStatusPositionScope は IssueStatus の acts_as_positioned（スコープなし）。
var IssueStatusPositionScope = PositionScope{Table: "issue_statuses"}

const issueStatusCols = `id, name, description, is_closed, position, default_done_ratio`

// ListIssueStatuses は IssueStatus.sorted（position 順）を返す。
func ListIssueStatuses(ctx context.Context, q db.Queryer) ([]*domain.IssueStatus, error) {
	var rows []issueStatusRow
	if err := q.Select(ctx, &rows, `SELECT `+issueStatusCols+` FROM issue_statuses ORDER BY position, id`); err != nil {
		return nil, err
	}
	out := make([]*domain.IssueStatus, len(rows))
	for i := range rows {
		out[i] = rows[i].status()
	}
	return out, nil
}

// GetIssueStatus は id のステータスを返す。
func GetIssueStatus(ctx context.Context, q db.Queryer, id int64) (*domain.IssueStatus, error) {
	var r issueStatusRow
	if err := q.Get(ctx, &r, `SELECT `+issueStatusCols+` FROM issue_statuses WHERE id = ?`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return r.status(), nil
}

// IssueStatusNameTaken は validates_uniqueness_of :name（大文字小文字を区別）。
func IssueStatusNameTaken(ctx context.Context, q db.Queryer, name string, excludeID int64) (bool, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM issue_statuses WHERE name = ? AND id <> ?`, name, excludeID)
	return n > 0, err
}

// SaveIssueStatus はステータスの行を保存する（s.ID == 0 なら作成）。position の調整は呼び出し側。
func SaveIssueStatus(ctx context.Context, q db.Queryer, s *domain.IssueStatus) error {
	if s.ID == 0 {
		id, err := q.InsertReturningID(ctx, `INSERT INTO issue_statuses (name, description, is_closed, position, default_done_ratio)
  VALUES (?, ?, ?, ?, ?)`, s.Name, s.Description, s.IsClosed, s.Position, s.DefaultDoneRatio)
		if err != nil {
			return err
		}
		s.ID = id
		return nil
	}
	_, err := q.Exec(ctx, `UPDATE issue_statuses SET name = ?, description = ?, is_closed = ?, position = ?, default_done_ratio = ? WHERE id = ?`,
		s.Name, s.Description, s.IsClosed, s.Position, s.DefaultDoneRatio, s.ID)
	return err
}

// HandleIssueStatusClosed は IssueStatus#handle_is_closed_change（終了ステータスになった場合、
// そのステータスで closed_at が未設定のチケットに、ステータス変更の最終日時（なければ作成日時）を入れる）。
func HandleIssueStatusClosed(ctx context.Context, q db.Queryer, statusID int64) error {
	if _, err := q.Exec(ctx, `UPDATE issues SET closed_at = (
  SELECT MAX(j.created_at) FROM issue_journals j JOIN issue_journal_details d ON d.journal_id = j.id
  WHERE j.issue_id = issues.id AND d.property = 'attr' AND d.prop_key = 'status_id' AND d.value = ?)
  WHERE status_id = ? AND closed_at IS NULL`, strconv.FormatInt(statusID, 10), statusID); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `UPDATE issues SET closed_at = created_at WHERE status_id = ? AND closed_at IS NULL`, statusID)
	return err
}

// ErrIssueStatusInUse は IssueStatus#check_integrity の違反（メッセージは Redmine と同じ英文）。
type ErrIssueStatusInUse struct{ Message string }

func (e *ErrIssueStatusInUse) Error() string { return e.Message }

// DestroyIssueStatus はステータスを削除する（check_integrity → delete_workflow_rules → remove_position）。
func DestroyIssueStatus(ctx context.Context, q db.Queryer, s *domain.IssueStatus) error {
	var n int
	if err := q.Get(ctx, &n, `SELECT COUNT(*) FROM issues WHERE status_id = ?`, s.ID); err != nil {
		return err
	}
	if n > 0 {
		return &ErrIssueStatusInUse{"This status is used by some issues"}
	}
	if err := q.Get(ctx, &n, `SELECT COUNT(*) FROM trackers WHERE default_status_id = ?`, s.ID); err != nil {
		return err
	}
	if n > 0 {
		return &ErrIssueStatusInUse{"This status is used as the default status by some trackers"}
	}
	for _, stmt := range []string{
		`DELETE FROM workflow_transitions WHERE old_status_id = ? OR new_status_id = ?`,
	} {
		if _, err := q.Exec(ctx, stmt, s.ID, s.ID); err != nil {
			return err
		}
	}
	if _, err := q.Exec(ctx, `DELETE FROM workflow_field_rules WHERE status_id = ?`, s.ID); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `DELETE FROM issue_statuses WHERE id = ?`, s.ID); err != nil {
		return err
	}
	return RemovePosition(ctx, q, IssueStatusPositionScope, s.ID, s.Position)
}

// UpdateIssueDoneRatios は IssueStatus.update_issue_done_ratios の更新部分
// （default_done_ratio を持つステータスのチケットの進捗率を揃える）。
func UpdateIssueDoneRatios(ctx context.Context, q db.Queryer) error {
	_, err := q.Exec(ctx, `UPDATE issues SET done_ratio = (SELECT s.default_done_ratio FROM issue_statuses s WHERE s.id = issues.status_id)
  WHERE status_id IN (SELECT id FROM issue_statuses WHERE default_done_ratio >= 0)`)
	return err
}
