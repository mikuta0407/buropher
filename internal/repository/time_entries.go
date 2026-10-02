package repository

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// このファイルは工数（TimeEntry）と作業分類（TimeEntryActivity）の読み書き。

type timeEntryRow struct {
	ID         int64          `db:"id"`
	ProjectID  int64          `db:"project_id"`
	UserID     int64          `db:"user_id"`
	AuthorID   int64          `db:"author_id"`
	IssueID    sql.NullInt64  `db:"issue_id"`
	Hours      float64        `db:"hours"`
	Comments   sql.NullString `db:"comments"`
	ActivityID int64          `db:"activity_id"`
	SpentOn    db.Date        `db:"spent_on"`
	TYear      int            `db:"tyear"`
	TMonth     int            `db:"tmonth"`
	TWeek      int            `db:"tweek"`
	CreatedAt  db.Time        `db:"created_at"`
	UpdatedAt  db.Time        `db:"updated_at"`
}

const timeEntryColumns = `id, project_id, user_id, author_id, issue_id, hours, comments, activity_id, spent_on,
  tyear, tmonth, tweek, created_at, updated_at`

func (r *timeEntryRow) entry() *domain.TimeEntry {
	t := &domain.TimeEntry{ID: r.ID, ProjectID: r.ProjectID, UserID: r.UserID, AuthorID: r.AuthorID,
		IssueID: nullID(r.IssueID), Hours: r.Hours, ActivityID: r.ActivityID, SpentOn: r.SpentOn.Time,
		TYear: r.TYear, TMonth: r.TMonth, TWeek: r.TWeek, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}
	if r.Comments.Valid {
		s := r.Comments.String
		t.Comments = &s
	}
	return t
}

func int64List(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}

// TimeEntryByID は TimeEntry.find(id)。
func TimeEntryByID(ctx context.Context, q db.Queryer, id int64) (*domain.TimeEntry, error) {
	var r timeEntryRow
	if err := q.Get(ctx, &r, `SELECT `+timeEntryColumns+` FROM time_entries WHERE id = ?`, id); err != nil {
		return nil, notFound(err)
	}
	return r.entry(), nil
}

// TimeEntriesByIDs は TimeEntry.where(id: ids)（id 順）。
func TimeEntriesByIDs(ctx context.Context, q db.Queryer, ids []int64) ([]*domain.TimeEntry, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []timeEntryRow
	if err := q.Select(ctx, &rows, `SELECT `+timeEntryColumns+` FROM time_entries WHERE id IN (`+int64List(ids)+`) ORDER BY id`); err != nil {
		return nil, err
	}
	out := make([]*domain.TimeEntry, len(rows))
	for i := range rows {
		out[i] = rows[i].entry()
	}
	return out, nil
}

func nullableID(id *int64) any {
	if id == nil {
		return nil
	}
	return *id
}

func nullableStr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// TimeEntryInsert は工数を作成し、ID・作成日時を設定する。
func TimeEntryInsert(ctx context.Context, q db.Queryer, t *domain.TimeEntry, now time.Time) error {
	ts := db.NewTime(now)
	id, err := q.InsertReturningID(ctx, `INSERT INTO time_entries (project_id, user_id, author_id, issue_id, hours, comments,
  activity_id, spent_on, tyear, tmonth, tweek, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ProjectID, t.UserID, t.AuthorID, nullableID(t.IssueID), t.Hours, nullableStr(t.Comments), t.ActivityID,
		db.DateOf(t.SpentOn), t.TYear, t.TMonth, t.TWeek, ts, ts)
	if err != nil {
		return err
	}
	t.ID = id
	t.CreatedAt, t.UpdatedAt = ts.Time, ts.Time
	return nil
}

// TimeEntryUpdate は工数を更新する（updated_at は touch が真のときだけ更新する）。
func TimeEntryUpdate(ctx context.Context, q db.Queryer, t *domain.TimeEntry, now time.Time, touch bool) error {
	if touch {
		t.UpdatedAt = db.NewTime(now).Time
	}
	_, err := q.Exec(ctx, `UPDATE time_entries SET project_id = ?, user_id = ?, author_id = ?, issue_id = ?, hours = ?, comments = ?,
  activity_id = ?, spent_on = ?, tyear = ?, tmonth = ?, tweek = ?, updated_at = ? WHERE id = ?`,
		t.ProjectID, t.UserID, t.AuthorID, nullableID(t.IssueID), t.Hours, nullableStr(t.Comments), t.ActivityID,
		db.DateOf(t.SpentOn), t.TYear, t.TMonth, t.TWeek, db.NewTime(t.UpdatedAt), t.ID)
	return err
}

// TimeEntryDelete は工数とそのカスタム値を削除する。
func TimeEntryDelete(ctx context.Context, q db.Queryer, id int64) error {
	if err := DeleteCustomValues(ctx, q, "time_entry", id); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `DELETE FROM time_entries WHERE id = ?`, id)
	return err
}

// TimeEntryOtherHours は同じユーザー・同じ日の他の工数の合計（other_hours_with_same_user_and_day）。
func TimeEntryOtherHours(ctx context.Context, q db.Queryer, userID int64, spentOn time.Time, excludeID int64) (float64, error) {
	var v sql.NullFloat64
	err := q.Get(ctx, &v, `SELECT SUM(hours) FROM time_entries WHERE user_id = ? AND spent_on = ? AND id <> ?`,
		userID, db.DateOf(spentOn), excludeID)
	return v.Float64, err
}

// TimeEntryCustomValues は複数の工数のカスタム値（time entry id → custom_field_id → 値）。
func TimeEntryCustomValues(ctx context.Context, q db.Queryer, ids []int64) (map[int64]map[int64][]string, error) {
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
WHERE customized_kind = 'time_entry' AND customized_id IN (`+int64List(ids)+`) ORDER BY id`); err != nil {
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

// ---------------------------------------------------------------- 作業分類

func timelogActivities(ctx context.Context, q db.Queryer, where string, args ...any) ([]*domain.Enumeration, error) {
	var rows []enumerationRow
	if err := q.Select(ctx, &rows, enumerationSelect(domain.EnumTimeEntryActivity)+` WHERE `+where+` ORDER BY position, id`, args...); err != nil {
		return nil, err
	}
	out := make([]*domain.Enumeration, len(rows))
	for i := range rows {
		out[i] = rows[i].enumeration(domain.EnumTimeEntryActivity)
	}
	return out, nil
}

// TimelogAvailableActivities は TimeEntryActivity.available_activities(project)
// （project が nil なら shared.active、それ以外は project.activities（上書きを反映した有効なもの））。
func TimelogAvailableActivities(ctx context.Context, q db.Queryer, projectID *int64) ([]*domain.Enumeration, error) {
	if projectID == nil {
		return timelogActivities(ctx, q, `project_id IS NULL AND active = ?`, true)
	}
	return TimelogProjectActivities(ctx, q, *projectID, false)
}

// TimelogProjectActivities は Project#activities(include_inactive)。
func TimelogProjectActivities(ctx context.Context, q db.Queryer, projectID int64, includeInactive bool) ([]*domain.Enumeration, error) {
	where := `(project_id IS NULL OR project_id = ?) AND id NOT IN (SELECT parent_id FROM time_entry_activities WHERE project_id = ? AND parent_id IS NOT NULL)`
	args := []any{projectID, projectID}
	if !includeInactive {
		where += ` AND active = ?`
		args = append(args, true)
	}
	return timelogActivities(ctx, q, where, args...)
}

// TimelogActivityByID は TimeEntryActivity.find_by_id(id)（無ければ nil）。
func TimelogActivityByID(ctx context.Context, q db.Queryer, id int64) (*domain.Enumeration, error) {
	as, err := timelogActivities(ctx, q, `id = ?`, id)
	if err != nil || len(as) == 0 {
		return nil, err
	}
	return as[0], nil
}

// TimelogActivitiesByIDs は id → 作業分類。
func TimelogActivitiesByIDs(ctx context.Context, q db.Queryer, ids []int64) (map[int64]*domain.Enumeration, error) {
	out := map[int64]*domain.Enumeration{}
	if len(ids) == 0 {
		return out, nil
	}
	as, err := timelogActivities(ctx, q, `id IN (`+int64List(ids)+`)`)
	if err != nil {
		return nil, err
	}
	for _, a := range as {
		out[a.ID] = a
	}
	return out, nil
}

// TimelogDefaultActivity は TimeEntryActivity.default（共有の既定。無ければ nil）。
func TimelogDefaultActivity(ctx context.Context, q db.Queryer) (*domain.Enumeration, error) {
	as, err := timelogActivities(ctx, q, `project_id IS NULL AND is_default = ?`, true)
	if err != nil || len(as) == 0 {
		return nil, err
	}
	return as[0], nil
}

// TimelogRoleDefaultActivityIDs は user のプロジェクトでのロールの default_time_entry_activity_id
// （roles.sort の順。user_membership.roles.where.not(default_time_entry_activity_id: nil).sort.pluck）。
func TimelogRoleDefaultActivityIDs(ctx context.Context, q db.Queryer, userID, projectID int64) ([]int64, error) {
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT DISTINCT r.default_time_entry_activity_id FROM roles r
  INNER JOIN member_roles mr ON mr.role_id = r.id
  INNER JOIN members m ON m.id = mr.member_id
  WHERE m.principal_id = ? AND m.project_id = ? AND r.default_time_entry_activity_id IS NOT NULL
  ORDER BY r.position, r.id`, userID, projectID)
	return ids, err
}

// TimelogLogTimeMemberUserIDs は project.members.active のうち log_time を持つロールのメンバーの principal id。
func TimelogLogTimeMemberUserIDs(ctx context.Context, q db.Queryer, projectID int64) ([]int64, error) {
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT DISTINCT m.principal_id FROM members m
  INNER JOIN principals p ON p.id = m.principal_id
  INNER JOIN member_roles mr ON mr.member_id = m.id
  INNER JOIN roles r ON r.id = mr.role_id
  WHERE m.project_id = ? AND p.status = 1 AND EXISTS (SELECT 1 FROM role_permissions rp WHERE rp.role_id = r.id AND rp.permission = 'log_time')`, projectID)
	return ids, err
}
