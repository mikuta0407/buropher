package repository

import (
	"context"
	"database/sql"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// このファイルはカレンダー（CalendarsController#show・マイページのカレンダーブロック）に置く
// チケット・バージョンの読み込み。

// CalendarIssue はカレンダーのチケット（link_to_issue / css_classes / render_issue_tooltip に必要な列）。
type CalendarIssue struct {
	RefIssue
	PriorityName string      `db:"priority_name"`
	ClosedOn     db.NullTime `db:"closed_at"`
}

// CalendarIssuesByIDs は ids のチケットを ids の順に返す（見つからない id は除く）。
func CalendarIssuesByIDs(ctx context.Context, q db.Queryer, ids []int64) ([]*CalendarIssue, error) {
	byID := map[int64]*CalendarIssue{}
	for _, chunk := range chunkIDs(uniqIDs(ids)) {
		query, args, err := db.In(`SELECT issues.id, issues.project_id, projects.name AS project_name,
  issues.tracker_id, trackers.name AS tracker_name, issues.status_id, issue_statuses.name AS status_name,
  issue_statuses.is_closed AS status_closed, issues.priority_id, issue_priorities.position_name AS priority_position_name,
  issue_priorities.name AS priority_name,
  issues.subject, issues.author_id, issues.assigned_to_id, issues.parent_id,
  EXISTS (SELECT 1 FROM issues c WHERE c.parent_id = issues.id) AS has_children,
  issues.is_private, issues.start_date, issues.due_date, issues.done_ratio, issues.closed_at
FROM issues
JOIN projects ON projects.id = issues.project_id
JOIN trackers ON trackers.id = issues.tracker_id
JOIN issue_statuses ON issue_statuses.id = issues.status_id
JOIN issue_priorities ON issue_priorities.id = issues.priority_id
WHERE issues.id IN (?)`, chunk)
		if err != nil {
			return nil, err
		}
		var rows []*CalendarIssue
		if err := q.Select(ctx, &rows, query, args...); err != nil {
			return nil, err
		}
		for _, r := range rows {
			byID[r.ID] = r
		}
	}
	out := make([]*CalendarIssue, 0, len(ids))
	for _, id := range ids {
		if r := byID[id]; r != nil {
			out = append(out, r)
		}
	}
	return out, nil
}

// CalendarVersion はカレンダーのバージョン。StartDate は Version#start_date（対象チケットの最小の開始日）。
type CalendarVersion struct {
	ID            int64       `db:"id"`
	ProjectID     int64       `db:"project_id"`
	Name          string      `db:"name"`
	EffectiveDate db.NullDate `db:"effective_date"`
	StartDate     db.NullDate `db:"start_date"`
}

// CalendarVersionsByIDs は ids のバージョンを ids の順に返す。
func CalendarVersionsByIDs(ctx context.Context, q db.Queryer, ids []int64) ([]*CalendarVersion, error) {
	byID := map[int64]*CalendarVersion{}
	for _, chunk := range chunkIDs(uniqIDs(ids)) {
		query, args, err := db.In(`SELECT versions.id, versions.project_id, versions.name, versions.effective_date,
  (SELECT MIN(i.start_date) FROM issues i WHERE i.fixed_version_id = versions.id) AS start_date
FROM versions WHERE versions.id IN (?)`, chunk)
		if err != nil {
			return nil, err
		}
		var rows []*CalendarVersion
		if err := q.Select(ctx, &rows, query, args...); err != nil {
			return nil, err
		}
		for _, r := range rows {
			byID[r.ID] = r
		}
	}
	out := make([]*CalendarVersion, 0, len(ids))
	for _, id := range ids {
		if r := byID[id]; r != nil {
			out = append(out, r)
		}
	}
	return out, nil
}

// PrincipalsByIDs は id の Principal（ユーザー・グループ）を id -> Principal の map で返す。
func PrincipalsByIDs(ctx context.Context, q db.Queryer, ids []int64) (map[int64]*domain.Principal, error) {
	out := map[int64]*domain.Principal{}
	for _, id := range uniqIDs(ids) {
		p, err := GetPrincipal(ctx, q, id)
		if err != nil {
			if err == ErrNotFound || err == sql.ErrNoRows {
				continue
			}
			return nil, err
		}
		out[id] = p
	}
	return out, nil
}
