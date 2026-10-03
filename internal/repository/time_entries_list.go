// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository

import (
	"context"
	"database/sql"

	"github.com/mikuta0407/buropher/internal/db"
)

// このファイルは工数一覧・レポートが参照する関連（チケット・カスタム値）の読み込み。

// TimelogRefIssues は id のチケット（link_to_issue 用の属性。cond で可視性を絞る。"" なら全件）。
func TimelogRefIssues(ctx context.Context, q db.Queryer, ids []int64, cond string) (map[int64]*RefIssue, error) {
	out := map[int64]*RefIssue{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []RefIssue
	err := q.Select(ctx, &rows, `SELECT issues.id, issues.project_id, projects.name AS project_name,
  issues.tracker_id, trackers.name AS tracker_name, issues.status_id, issue_statuses.name AS status_name,
  issue_statuses.is_closed AS status_closed, issues.priority_id, issue_priorities.position_name AS priority_position_name,
  issues.subject, issues.author_id, issues.assigned_to_id, issues.parent_id,
  EXISTS (SELECT 1 FROM issues c WHERE c.parent_id = issues.id) AS has_children,
  issues.is_private, issues.start_date, issues.due_date, issues.done_ratio
FROM issues
JOIN projects ON projects.id = issues.project_id
JOIN trackers ON trackers.id = issues.tracker_id
JOIN issue_statuses ON issue_statuses.id = issues.status_id
JOIN issue_priorities ON issue_priorities.id = issues.priority_id
WHERE issues.id IN (`+int64List(ids)+`) AND (`+condOr(cond)+`)`)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		out[rows[i].ID] = &rows[i]
	}
	return out, nil
}

// TimelogIssueExtra はチケットの分類・対象バージョン（工数一覧の関連列用）。
type TimelogIssueExtra struct {
	ID                 int64          `db:"id"`
	CategoryName       sql.NullString `db:"category_name"`
	FixedVersionID     sql.NullInt64  `db:"fixed_version_id"`
	VersionName        sql.NullString `db:"version_name"`
	VersionProjectID   sql.NullInt64  `db:"version_project_id"`
	VersionProjectName sql.NullString `db:"version_project_name"`
	VersionDate        db.NullDate    `db:"version_date"`
}

// TimelogIssueExtras は id → 分類・対象バージョン。
func TimelogIssueExtras(ctx context.Context, q db.Queryer, ids []int64) (map[int64]*TimelogIssueExtra, error) {
	out := map[int64]*TimelogIssueExtra{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []TimelogIssueExtra
	err := q.Select(ctx, &rows, `SELECT issues.id, issue_categories.name AS category_name, issues.fixed_version_id,
  versions.name AS version_name, versions.project_id AS version_project_id, vp.name AS version_project_name, versions.effective_date AS version_date
FROM issues
LEFT JOIN issue_categories ON issue_categories.id = issues.category_id
LEFT JOIN versions ON versions.id = issues.fixed_version_id
LEFT JOIN projects vp ON vp.id = versions.project_id
WHERE issues.id IN (`+int64List(ids)+`)`)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		out[rows[i].ID] = &rows[i]
	}
	return out, nil
}

// TimelogCustomValues は customized_kind の複数の所有者のカスタム値（owner id → custom_field_id → 値）。
func TimelogCustomValues(ctx context.Context, q db.Queryer, kind string, ids []int64) (map[int64]map[int64][]string, error) {
	out := map[int64]map[int64][]string{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID    int64          `db:"customized_id"`
		CFID  int64          `db:"custom_field_id"`
		Value sql.NullString `db:"value"`
	}
	if err := q.Select(ctx, &rows, `SELECT customized_id, custom_field_id, value FROM custom_values
WHERE customized_kind = ? AND customized_id IN (`+int64List(ids)+`) ORDER BY id`, kind); err != nil {
		return nil, err
	}
	for _, r := range rows {
		m := out[r.ID]
		if m == nil {
			m = map[int64][]string{}
			out[r.ID] = m
		}
		if r.Value.Valid {
			m[r.CFID] = append(m[r.CFID], r.Value.String)
		} else if _, ok := m[r.CFID]; !ok {
			m[r.CFID] = []string{}
		}
	}
	return out, nil
}
