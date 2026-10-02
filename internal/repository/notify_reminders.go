package repository

import (
	"context"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
)

// CountOpenIssuesAssignedTo は可視（visibleCond）な未完了チケットのうち担当者が ids のものの件数
// （Mailer#reminder の IssueQuery(assigned_to_id: me).issue_count）。
func CountOpenIssuesAssignedTo(ctx context.Context, q db.Queryer, visibleCond string, ids []int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := []any{false}
	for _, id := range ids {
		args = append(args, id)
	}
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM issues JOIN projects ON projects.id = issues.project_id
JOIN issue_statuses ON issue_statuses.id = issues.status_id
WHERE issue_statuses.is_closed = ? AND issues.assigned_to_id IN (`+ph+`) AND (`+visibleCond+`)`, args...)
	return n, err
}

// ReminderIssue は期日リマインダの対象チケット。
type ReminderIssue struct {
	ID           int64 `db:"id"`
	AssignedToID int64 `db:"assigned_to_id"`
}

// ReminderIssues は Mailer.reminders の対象（未完了・担当者あり・有効なプロジェクト・期日が due 以前）。
func ReminderIssues(ctx context.Context, q db.Queryer, due time.Time, userIDs []int64, projectID, trackerID int64, versionIDs []int64) ([]ReminderIssue, error) {
	sql := `SELECT issues.id, issues.assigned_to_id FROM issues JOIN projects ON projects.id = issues.project_id
JOIN issue_statuses ON issue_statuses.id = issues.status_id
WHERE issue_statuses.is_closed = ? AND issues.assigned_to_id IS NOT NULL AND projects.status = 1 AND issues.due_date <= ?`
	args := []any{false, db.DateOf(due)}
	in := func(col string, ids []int64) {
		sql += ` AND ` + col + ` IN (` + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + `)`
		for _, id := range ids {
			args = append(args, id)
		}
	}
	if len(userIDs) > 0 {
		in("issues.assigned_to_id", userIDs)
	}
	if projectID != 0 {
		sql += ` AND issues.project_id = ?`
		args = append(args, projectID)
	}
	if len(versionIDs) > 0 {
		in("issues.fixed_version_id", versionIDs)
	}
	if trackerID != 0 {
		sql += ` AND issues.tracker_id = ?`
		args = append(args, trackerID)
	}
	var rows []ReminderIssue
	err := q.Select(ctx, &rows, sql+` ORDER BY issues.id`, args...)
	return rows, err
}

// VersionIDsNamed は Version.named(name)（名前の大文字小文字を区別しない一致）。
func VersionIDsNamed(ctx context.Context, q db.Queryer, name string) ([]int64, error) {
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT id FROM versions WHERE LOWER(name) = LOWER(?) ORDER BY id`, strings.TrimSpace(name))
	return ids, err
}
