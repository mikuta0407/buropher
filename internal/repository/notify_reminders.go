// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

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

// ReminderIssue は期日リマインダの対象チケット（可視性の判定に使う列を含む）。
type ReminderIssue struct {
	ID           int64 `db:"id"`
	ProjectID    int64 `db:"project_id"`
	TrackerID    int64 `db:"tracker_id"`
	StatusID     int64 `db:"status_id"`
	AuthorID     int64 `db:"author_id"`
	AssignedToID int64 `db:"assigned_to_id"`
	IsPrivate    bool  `db:"is_private"`
}

// ReminderIssues は Mailer.reminders の対象（未完了・担当者あり・有効なプロジェクト・期日が due 以前）。
func ReminderIssues(ctx context.Context, q db.Queryer, due time.Time, userIDs []int64, projectID, trackerID int64, versionIDs []int64) ([]ReminderIssue, error) {
	sql := `SELECT issues.id, issues.project_id, issues.tracker_id, issues.status_id, issues.author_id, issues.assigned_to_id, issues.is_private FROM issues JOIN projects ON projects.id = issues.project_id
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

// DeleteStaleSessions は期限切れのセッションを消す（session_lifetime / session_timeout（分。0 なら無制限）と、
// anonymousIdle より長く使われていない未ログインのセッション）。
func DeleteStaleSessions(ctx context.Context, q db.Queryer, now time.Time, lifetimeMin, timeoutMin int, anonymousIdle time.Duration) (int64, error) {
	var total int64
	exec := func(sql string, args ...any) error {
		res, err := q.Exec(ctx, sql, args...)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		total += n
		return nil
	}
	if err := exec(`DELETE FROM sessions WHERE expires_at IS NOT NULL AND expires_at < ?`, db.NewTime(now)); err != nil {
		return total, err
	}
	if lifetimeMin > 0 {
		if err := exec(`DELETE FROM sessions WHERE user_id IS NOT NULL AND created_at < ?`, db.NewTime(now.Add(-time.Duration(lifetimeMin)*time.Minute))); err != nil {
			return total, err
		}
	}
	if timeoutMin > 0 {
		if err := exec(`DELETE FROM sessions WHERE user_id IS NOT NULL AND last_seen_at < ?`, db.NewTime(now.Add(-time.Duration(timeoutMin)*time.Minute))); err != nil {
			return total, err
		}
	}
	if anonymousIdle > 0 {
		if err := exec(`DELETE FROM sessions WHERE user_id IS NULL AND last_seen_at < ?`, db.NewTime(now.Add(-anonymousIdle))); err != nil {
			return total, err
		}
	}
	return total, nil
}

// ActiveUserIDsByLogins はログイン名の有効なユーザーのうち visibleCond（principals を参照する
// Principal.visible の条件）を満たすもの（メンションの解決）。
func ActiveUserIDsByLogins(ctx context.Context, q db.Queryer, logins []string, visibleCond string) ([]int64, error) {
	if len(logins) == 0 {
		return nil, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(logins)), ",")
	args := make([]any, len(logins))
	for i, l := range logins {
		args[i] = l
	}
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT principals.id FROM principals JOIN user_accounts ua ON ua.principal_id = principals.id
WHERE principals.kind = 'user' AND principals.status = 1 AND ua.login IN (`+ph+`) AND `+visibleCond+` ORDER BY principals.id`, args...)
	return ids, err
}
