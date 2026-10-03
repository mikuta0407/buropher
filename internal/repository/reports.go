// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// このファイルはチケットのレポート（ReportsController / Issue.count_and_group_by）の読み込み。

// ReportCount は Issue.count_and_group_by の 1 行（status_id・closed・集計列の値・件数）。
type ReportCount struct {
	StatusID int64
	Closed   bool
	// Value は集計列の値（NULL は ""。Ruby の to_s）。
	Value string
	Total int
}

// reportFields は count_and_group_by の :association → 外部キー。
var reportFields = map[string]bool{
	"tracker_id": true, "fixed_version_id": true, "priority_id": true, "category_id": true,
	"assigned_to_id": true, "author_id": true, "project_id": true,
}

// IssueReportCounts は Issue.visible(...).joins(:status).group(:status_id, :is_closed, field).count。
// cond は IssueVisibleCondition（project / with_subprojects を指定したもの）。
func IssueReportCounts(ctx context.Context, q db.Queryer, cond, field string) ([]ReportCount, error) {
	if !reportFields[field] {
		return nil, nil
	}
	var rows []struct {
		StatusID int64         `db:"status_id"`
		Closed   bool          `db:"is_closed"`
		Value    sql.NullInt64 `db:"value"`
		Total    int           `db:"total"`
	}
	if err := q.Select(ctx, &rows, `SELECT issues.status_id, issue_statuses.is_closed, issues.`+field+` AS value, COUNT(*) AS total
FROM issues JOIN projects ON projects.id = issues.project_id JOIN issue_statuses ON issue_statuses.id = issues.status_id
WHERE `+condOr(cond)+`
GROUP BY issues.status_id, issue_statuses.is_closed, issues.`+field+`
ORDER BY issues.status_id, issue_statuses.is_closed, issues.`+field); err != nil {
		return nil, err
	}
	out := make([]ReportCount, len(rows))
	for i, r := range rows {
		out[i] = ReportCount{StatusID: r.StatusID, Closed: r.Closed, Total: r.Total}
		if r.Value.Valid {
			out[i].Value = strconv.FormatInt(r.Value.Int64, 10)
		}
	}
	return out, nil
}

// ReportRow はレポートの行（トラッカー・バージョン・優先度・カテゴリ・ユーザー・プロジェクト）。
type ReportRow struct {
	// ID は行の id（「なし」の行は 0）。
	ID   int64
	Name string
	// Project は行がプロジェクト（サブプロジェクト）のとき。
	Project *domain.Project
}

type idName struct {
	ID   int64  `db:"id"`
	Name string `db:"name"`
}

func toReportRows(rs []idName) []ReportRow {
	out := make([]ReportRow, len(rs))
	for i, r := range rs {
		out[i] = ReportRow{ID: r.ID, Name: r.Name}
	}
	return out
}

// rolledUpProjectsCondition は project.rolled_up_trackers(include_subprojects) のプロジェクト条件（p を参照）。
func rolledUpProjectsCondition(projectID int64, withSubprojects bool) string {
	s := "p.status <> " + strconv.Itoa(domain.ProjectStatusArchived) +
		" AND EXISTS (SELECT 1 FROM project_modules em WHERE em.project_id = p.id AND em.name = 'issue_tracking')"
	if withSubprojects {
		return s + " AND p.id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = " + strconv.FormatInt(projectID, 10) + ")"
	}
	return s + " AND p.id = " + strconv.FormatInt(projectID, 10)
}

// ReportTrackers は project.rolled_up_trackers(with_subprojects).visible（sorted）。
// visibleCond は Tracker.visible の allowed_to_condition（projects を参照する SQL）。
func ReportTrackers(ctx context.Context, q db.Queryer, projectID int64, withSubprojects bool, visibleCond string) ([]ReportRow, error) {
	var rs []idName
	err := q.Select(ctx, &rs, `SELECT trackers.id, trackers.name FROM trackers WHERE EXISTS (
SELECT 1 FROM project_trackers pt JOIN projects p ON p.id = pt.project_id JOIN projects ON projects.id = p.id
WHERE pt.tracker_id = trackers.id AND `+rolledUpProjectsCondition(projectID, withSubprojects)+` AND (`+condOr(visibleCond)+`))
ORDER BY trackers.position, trackers.id`)
	return toReportRows(rs), err
}

// ReportStatuses は project.rolled_up_statuses.sorted。
func ReportStatuses(ctx context.Context, q db.Queryer, projectID int64) ([]*domain.IssueStatus, error) {
	trackers := `SELECT pt.tracker_id FROM project_trackers pt JOIN projects p ON p.id = pt.project_id WHERE ` +
		rolledUpProjectsCondition(projectID, true)
	all, err := ListIssueStatuses(ctx, q)
	if err != nil {
		return nil, err
	}
	var ids []int64
	if err := q.Select(ctx, &ids, `SELECT old_status_id FROM workflow_transitions WHERE tracker_id IN (`+trackers+`) AND old_status_id IS NOT NULL AND old_status_id <> new_status_id
UNION SELECT new_status_id FROM workflow_transitions WHERE tracker_id IN (`+trackers+`) AND (old_status_id IS NULL OR old_status_id <> new_status_id)`); err != nil {
		return nil, err
	}
	set := map[int64]bool{}
	for _, id := range ids {
		set[id] = true
	}
	var out []*domain.IssueStatus
	for _, s := range all {
		if set[s.ID] {
			out = append(out, s)
		}
	}
	return out, nil
}

// ReportVersions は project.shared_versions.sorted。
func ReportVersions(ctx context.Context, q db.Queryer, p *domain.Project) ([]ReportRow, error) {
	var rs []idName
	err := q.Select(ctx, &rs, `SELECT versions.id, versions.name FROM versions JOIN projects ON projects.id = versions.project_id
WHERE `+SharedVersionsCondition(p)+`
ORDER BY (CASE WHEN versions.effective_date IS NULL THEN 1 ELSE 0 END), versions.effective_date, versions.name, versions.id`)
	return toReportRows(rs), err
}

// ReportPriorities は IssuePriority.all.reverse（position の降順）。
func ReportPriorities(ctx context.Context, q db.Queryer) ([]ReportRow, error) {
	var rs []idName
	err := q.Select(ctx, &rs, `SELECT id, name FROM issue_priorities ORDER BY position, id`)
	out := toReportRows(rs)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, err
}

// ReportCategories は project.issue_categories（name 順）。
func ReportCategories(ctx context.Context, q db.Queryer, projectID int64) ([]ReportRow, error) {
	var rs []idName
	err := q.Select(ctx, &rs, `SELECT id, name FROM issue_categories WHERE project_id = ? ORDER BY name, id`, projectID)
	return toReportRows(rs), err
}

// ReportUsers は project.users.sorted（プロジェクトのメンバーの有効なユーザー）。
func ReportUsers(ctx context.Context, q db.Queryer, projectID int64, userFormat string) ([]*domain.User, error) {
	var rows []userRow
	if err := q.Select(ctx, &rows, userSelect+` WHERE p.kind = 'user' AND p.status = ?
AND p.id IN (SELECT principal_id FROM members WHERE project_id = ?) ORDER BY `+strings.Join(UserOrderColumns(userFormat), ", "),
		domain.StatusActive, projectID); err != nil {
		return nil, err
	}
	out := make([]*domain.User, len(rows))
	for i := range rows {
		out[i] = rows[i].user()
	}
	return out, nil
}

// ReportGroups は project.principals のうちグループ（Principal.sorted の type DESC 順: GroupNonMember, GroupAnonymous, Group）。
func ReportGroups(ctx context.Context, q db.Queryer, projectID int64) ([]*domain.Principal, error) {
	var ids []int64
	if err := q.Select(ctx, &ids, `SELECT p.id FROM principals p WHERE p.kind IN ('group', 'group_anonymous', 'group_non_member')
AND p.status = ? AND p.id IN (SELECT principal_id FROM members WHERE project_id = ?)
ORDER BY CASE p.kind WHEN 'group_non_member' THEN 0 WHEN 'group_anonymous' THEN 1 ELSE 2 END, p.name, p.id`, domain.StatusActive, projectID); err != nil {
		return nil, err
	}
	out := make([]*domain.Principal, 0, len(ids))
	for _, id := range ids {
		p, err := GetPrincipal(ctx, q, id)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}
