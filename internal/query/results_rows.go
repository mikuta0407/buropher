package query

import (
	"context"
	"slices"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

// TimeEntryRow は工数一覧の行 (TimeEntryQuery#results_scope の要素)。
type TimeEntryRow struct {
	ID         int64
	ProjectID  int64
	UserID     int64
	AuthorID   int64
	IssueID    *int64
	Hours      float64
	Comments   string
	ActivityID int64
	SpentOn    time.Time
	TYear      int
	TMonth     int
	TWeek      int
	CreatedAt  time.Time
	UpdatedAt  time.Time
	// CustomValues は CF 列があるとき (custom_field_id → 値)。
	CustomValues map[int64][]string
}

type timeEntryRowDB struct {
	ID         int64   `db:"id"`
	ProjectID  int64   `db:"project_id"`
	UserID     int64   `db:"user_id"`
	AuthorID   int64   `db:"author_id"`
	IssueID    *int64  `db:"issue_id"`
	Hours      float64 `db:"hours"`
	Comments   *string `db:"comments"`
	ActivityID int64   `db:"activity_id"`
	SpentOn    db.Date `db:"spent_on"`
	TYear      int     `db:"tyear"`
	TMonth     int     `db:"tmonth"`
	TWeek      int     `db:"tweek"`
	CreatedAt  db.Time `db:"created_at"`
	UpdatedAt  db.Time `db:"updated_at"`
}

// TimeEntries は TimeEntryQuery#results_scope の行を並び順どおりに返す。
func (q *Query) TimeEntries(ctx context.Context, opts ListOptions) ([]*TimeEntryRow, error) {
	ids, err := q.IDs(ctx, opts)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	byID := map[int64]*TimeEntryRow{}
	for _, c := range chunk(ids, 500) {
		var rs []timeEntryRowDB
		if err := q.env.Q.Select(ctx, &rs, `SELECT id, project_id, user_id, author_id, issue_id, hours, comments, activity_id, spent_on,
  tyear, tmonth, tweek, created_at, updated_at FROM time_entries WHERE id IN (`+idList(c)+`)`); err != nil {
			return nil, err
		}
		for _, r := range rs {
			row := &TimeEntryRow{ID: r.ID, ProjectID: r.ProjectID, UserID: r.UserID, AuthorID: r.AuthorID, IssueID: r.IssueID,
				Hours: r.Hours, ActivityID: r.ActivityID, SpentOn: r.SpentOn.Time, TYear: r.TYear, TMonth: r.TMonth, TWeek: r.TWeek,
				CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}
			if r.Comments != nil {
				row.Comments = *r.Comments
			}
			byID[r.ID] = row
		}
	}
	out := make([]*TimeEntryRow, 0, len(ids))
	for _, id := range ids {
		if r := byID[id]; r != nil {
			out = append(out, r)
		}
	}
	if ok, err := q.HasCustomFieldColumn(ctx); err != nil {
		return nil, err
	} else if ok {
		var cvs []struct {
			ID    int64   `db:"customized_id"`
			CFID  int64   `db:"custom_field_id"`
			Value *string `db:"value"`
		}
		if err := q.env.Q.Select(ctx, &cvs, `SELECT customized_id, custom_field_id, value FROM custom_values
WHERE customized_kind = 'time_entry' AND customized_id IN (`+idList(ids)+`) ORDER BY id`); err != nil {
			return nil, err
		}
		for _, r := range out {
			r.CustomValues = map[int64][]string{}
		}
		for _, cv := range cvs {
			if r := byID[cv.ID]; r != nil {
				v := ""
				if cv.Value != nil {
					v = *cv.Value
				}
				r.CustomValues[cv.CFID] = append(r.CustomValues[cv.CFID], v)
			}
		}
	}
	return out, nil
}

// Projects は ProjectQuery#results_scope のプロジェクトを並び順どおりに返す。
func (q *Query) Projects(ctx context.Context, opts ListOptions) ([]*domain.Project, error) {
	ids, err := q.IDs(ctx, opts)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	ps, err := repository.LoadProjects(ctx, q.env.Q, "projects.id IN ("+idList(ids)+")")
	if err != nil {
		return nil, err
	}
	slices.SortFunc(ps, func(a, b *domain.Project) int {
		return slices.Index(ids, a.ID) - slices.Index(ids, b.ID)
	})
	return ps, nil
}
