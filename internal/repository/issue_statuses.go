package repository

import (
	"context"
	"database/sql"

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

func loadIssueStatuses(ctx context.Context, q db.Queryer, where string, args ...any) ([]*domain.IssueStatus, error) {
	if where == "" {
		where = "1=1"
	}
	var rows []issueStatusRow
	if err := q.Select(ctx, &rows, `SELECT id, name, description, is_closed, position, default_done_ratio
  FROM issue_statuses WHERE `+where+` ORDER BY position, id`, args...); err != nil {
		return nil, err
	}
	out := make([]*domain.IssueStatus, 0, len(rows))
	for _, r := range rows {
		s := &domain.IssueStatus{ID: r.ID, Name: r.Name, Description: r.Description.String, IsClosed: r.IsClosed, Position: r.Position}
		if r.DefaultDoneRatio.Valid {
			v := int(r.DefaultDoneRatio.Int64)
			s.DefaultDoneRatio = &v
		}
		out = append(out, s)
	}
	return out, nil
}

// ListIssueStatuses は IssueStatus.sorted（position 順）。
func ListIssueStatuses(ctx context.Context, q db.Queryer) ([]*domain.IssueStatus, error) {
	return loadIssueStatuses(ctx, q, "")
}

// IssueStatusesByIDs は IssueStatus.where(:id => ids).sorted。
func IssueStatusesByIDs(ctx context.Context, q db.Queryer, ids []int64) ([]*domain.IssueStatus, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	where, args, err := db.In(`id IN (?)`, uniqIDs(ids))
	if err != nil {
		return nil, err
	}
	return loadIssueStatuses(ctx, q, where, args...)
}
