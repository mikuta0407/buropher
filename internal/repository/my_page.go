package repository

import (
	"context"
	"database/sql"
	"strconv"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// このファイルは MyController（マイページ）の SQL:
// UserPreference#my_page_layout / my_page_settings の読み書きと、各ブロック
// （文書・ニュース・作業時間・カレンダー）の取得。

// MyPagePrefs は user_preferences.my_page_layout / my_page_settings の生の JSON。
// 行が無ければ Exists が false（列が NULL なら空文字列）。
type MyPagePrefs struct {
	Exists   bool
	Layout   string
	Settings string
}

// GetMyPagePrefs はマイページの配置と設定を返す。
func GetMyPagePrefs(ctx context.Context, q db.Queryer, userID int64) (*MyPagePrefs, error) {
	var rows []struct {
		Layout   sql.NullString `db:"my_page_layout"`
		Settings sql.NullString `db:"my_page_settings"`
	}
	if err := q.Select(ctx, &rows, `SELECT my_page_layout, my_page_settings FROM user_preferences WHERE user_id = ?`, userID); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return &MyPagePrefs{}, nil
	}
	return &MyPagePrefs{Exists: true, Layout: rows[0].Layout.String, Settings: rows[0].Settings.String}, nil
}

// SaveMyPagePrefs はマイページの配置と設定を保存する（行は存在すること）。
func SaveMyPagePrefs(ctx context.Context, q db.Queryer, userID int64, layout, settings string) error {
	_, err := q.Exec(ctx, `UPDATE user_preferences SET my_page_layout = ?, my_page_settings = ? WHERE user_id = ?`,
		nullString(layout), nullString(settings), userID)
	return err
}

// userProjectsCondition は User#projects（メンバーである、削除予定でないプロジェクト）の条件。
func userProjectsCondition(userID int64) string {
	return `projects.id IN (SELECT members.project_id FROM members WHERE members.principal_id = ` +
		strconv.FormatInt(userID, 10) + `) AND projects.status <> ` + strconv.Itoa(domain.ProjectStatusScheduledForDeletion)
}

// MyPageNews は News.visible.where(:project => User.current.projects).limit(n).order(created_on DESC)。
// visible は AllowedToCondition(view_news) の SQL。
// 同時刻のニュースは id の昇順（参照環境の SQLite は includes(:project, :author) の結合で
// news を主キー順に走査するため。News.latest とは逆）。
func MyPageNews(ctx context.Context, q db.Queryer, visible string, userID int64, limit int) ([]*domain.News, error) {
	var rows []newsRow
	if err := q.Select(ctx, &rows, `SELECT news.id, news.project_id, news.title, news.summary, news.description, news.author_id,
  news.comments_count, news.created_at
FROM news JOIN projects ON projects.id = news.project_id
WHERE (`+condOr(visible)+`) AND `+userProjectsCondition(userID)+`
ORDER BY news.created_at DESC, news.id ASC `+q.Dialect().LimitOffset(limit, 0)); err != nil {
		return nil, err
	}
	return preloadNews(ctx, q, rows)
}

// MyPageDocument は文書一覧（documents/_document）に必要な列。
type MyPageDocument struct {
	ID          int64     `db:"id"`
	ProjectID   int64     `db:"project_id"`
	Title       string    `db:"title"`
	Description string    `db:"description"`
	CreatedAt   time.Time `db:"-"`
	// UpdatedOn は Document#updated_on（最後の添付の作成日時、無ければ作成日時）。
	UpdatedOn time.Time `db:"-"`
}

// MyPageDocuments は Document.visible.order(created_on DESC).limit(n)。visible は AllowedToCondition(view_documents)。
func MyPageDocuments(ctx context.Context, q db.Queryer, visible string, limit int) ([]*MyPageDocument, error) {
	var rows []struct {
		ID          int64          `db:"id"`
		ProjectID   int64          `db:"project_id"`
		Title       string         `db:"title"`
		Description sql.NullString `db:"description"`
		CreatedAt   db.Time        `db:"created_at"`
		LastAttach  db.NullTime    `db:"last_attach"`
	}
	if err := q.Select(ctx, &rows, `SELECT documents.id, documents.project_id, documents.title, documents.description, documents.created_at,
  (SELECT a.created_at FROM attachments a WHERE a.container_kind = 'document' AND a.container_id = documents.id
   ORDER BY a.created_at DESC, a.id DESC `+q.Dialect().LimitOffset(1, 0)+`) AS last_attach
FROM documents JOIN projects ON projects.id = documents.project_id
WHERE `+condOr(visible)+`
ORDER BY documents.created_at DESC, documents.id DESC `+q.Dialect().LimitOffset(limit, 0)); err != nil {
		return nil, err
	}
	out := make([]*MyPageDocument, len(rows))
	for i, r := range rows {
		d := &MyPageDocument{ID: r.ID, ProjectID: r.ProjectID, Title: r.Title, Description: r.Description.String,
			CreatedAt: r.CreatedAt.Time, UpdatedOn: r.CreatedAt.Time}
		if r.LastAttach.Valid {
			d.UpdatedOn = r.LastAttach.Time
		}
		out[i] = d
	}
	return out, nil
}

// MyPageTimeEntry はマイページの作業時間ブロックの行。
type MyPageTimeEntry struct {
	ID           int64
	ProjectID    int64
	ProjectName  string
	ActivityName string
	IssueID      *int64
	Comments     string
	Hours        float64
	SpentOn      time.Time
}

// MyPageTimeEntries は TimeEntry.where(user_id = ? AND spent_on BETWEEN from AND to)
// .order(spent_on DESC, projects.name ASC, trackers.position ASC, issues.id ASC)。
func MyPageTimeEntries(ctx context.Context, q db.Queryer, userID int64, from, to time.Time) ([]*MyPageTimeEntry, error) {
	var rows []struct {
		ID           int64          `db:"id"`
		ProjectID    int64          `db:"project_id"`
		ProjectName  string         `db:"project_name"`
		ActivityName string         `db:"activity_name"`
		IssueID      sql.NullInt64  `db:"issue_id"`
		Comments     sql.NullString `db:"comments"`
		Hours        float64        `db:"hours"`
		SpentOn      db.Date        `db:"spent_on"`
	}
	if err := q.Select(ctx, &rows, `SELECT time_entries.id, time_entries.project_id, projects.name AS project_name,
  time_entry_activities.name AS activity_name, time_entries.issue_id, time_entries.comments, time_entries.hours, time_entries.spent_on
FROM time_entries
JOIN time_entry_activities ON time_entry_activities.id = time_entries.activity_id
JOIN projects ON projects.id = time_entries.project_id
LEFT JOIN issues ON issues.id = time_entries.issue_id
LEFT JOIN trackers ON trackers.id = issues.tracker_id
WHERE time_entries.user_id = ? AND time_entries.spent_on BETWEEN ? AND ?
ORDER BY time_entries.spent_on DESC, projects.name ASC, trackers.position ASC, issues.id ASC, time_entries.id ASC`,
		userID, db.DateOf(from), db.DateOf(to)); err != nil {
		return nil, err
	}
	out := make([]*MyPageTimeEntry, len(rows))
	for i, r := range rows {
		out[i] = &MyPageTimeEntry{ID: r.ID, ProjectID: r.ProjectID, ProjectName: r.ProjectName, ActivityName: r.ActivityName,
			IssueID: nullID(r.IssueID), Comments: r.Comments.String, Hours: r.Hours, SpentOn: r.SpentOn.Time}
	}
	return out, nil
}

// RefIssuesByIDs は link_to_issue / Issue#css_classes 用のチケットを id の順で返す（可視性は確認しない）。
func RefIssuesByIDs(ctx context.Context, q db.Queryer, ids []int64) (map[int64]*RefIssue, error) {
	out := map[int64]*RefIssue{}
	for _, chunk := range chunkIDs(uniqIDs(ids)) {
		query, args, err := db.In(`SELECT issues.id, issues.project_id, projects.name AS project_name,
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
WHERE issues.id IN (?)`, chunk)
		if err != nil {
			return nil, err
		}
		var rows []RefIssue
		if err := q.Select(ctx, &rows, query, args...); err != nil {
			return nil, err
		}
		for i := range rows {
			out[rows[i].ID] = &rows[i]
		}
	}
	return out, nil
}

// MyPageCalendarIssueIDs は Issue.visible.where(:project => User.current.projects)
// .where("(start_date>=? and start_date<=?) or (due_date>=? and due_date<=?)") の id。
// visible は IssueVisibleCondition の SQL。
func MyPageCalendarIssueIDs(ctx context.Context, q db.Queryer, visible string, userID int64, from, to time.Time) ([]int64, error) {
	var ids []int64
	f, t := db.DateOf(from), db.DateOf(to)
	err := q.Select(ctx, &ids, `SELECT issues.id FROM issues JOIN projects ON projects.id = issues.project_id
WHERE (`+condOr(visible)+`) AND `+userProjectsCondition(userID)+`
  AND ((issues.start_date >= ? AND issues.start_date <= ?) OR (issues.due_date >= ? AND issues.due_date <= ?))
ORDER BY issues.id`, f, t, f, t)
	return ids, err
}

// CountTwofaBackupCodes は Redmine::Twofa.for_user(user).backup_codes の件数。
func CountTwofaBackupCodes(ctx context.Context, q db.Queryer, userID int64) (int, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM twofa_backup_codes WHERE user_id = ?`, userID)
	return n, err
}

// MyPageNamesByIDs は name 列を持つマスタ（trackers / issue_statuses / issue_priorities / issue_categories）の
// id → name を返す（チケット一覧の列の表示用）。
func MyPageNamesByIDs(ctx context.Context, q db.Queryer, table string, ids []int64) (map[int64]string, error) {
	switch table {
	case "trackers", "issue_statuses", "issue_priorities", "issue_categories":
	default:
		return nil, ErrNotFound
	}
	out := map[int64]string{}
	for _, chunk := range chunkIDs(uniqIDs(ids)) {
		query, args, err := db.In(`SELECT id, name FROM `+table+` WHERE id IN (?)`, chunk)
		if err != nil {
			return nil, err
		}
		var rows []struct {
			ID   int64  `db:"id"`
			Name string `db:"name"`
		}
		if err := q.Select(ctx, &rows, query, args...); err != nil {
			return nil, err
		}
		for _, r := range rows {
			out[r.ID] = r.Name
		}
	}
	return out, nil
}

// MyPageVersion は link_to_version に必要な列。
type MyPageVersion struct {
	ID            int64
	ProjectID     int64
	ProjectName   string
	Name          string
	EffectiveDate *time.Time
	Sharing       string
}

// MyPageVersionsByIDs は id → 対象バージョン。
func MyPageVersionsByIDs(ctx context.Context, q db.Queryer, ids []int64) (map[int64]*MyPageVersion, error) {
	out := map[int64]*MyPageVersion{}
	for _, chunk := range chunkIDs(uniqIDs(ids)) {
		query, args, err := db.In(`SELECT versions.id, versions.project_id, projects.name AS project_name, versions.name,
  versions.effective_date, versions.sharing FROM versions JOIN projects ON projects.id = versions.project_id WHERE versions.id IN (?)`, chunk)
		if err != nil {
			return nil, err
		}
		var rows []struct {
			ID            int64       `db:"id"`
			ProjectID     int64       `db:"project_id"`
			ProjectName   string      `db:"project_name"`
			Name          string      `db:"name"`
			EffectiveDate db.NullDate `db:"effective_date"`
			Sharing       string      `db:"sharing"`
		}
		if err := q.Select(ctx, &rows, query, args...); err != nil {
			return nil, err
		}
		for _, r := range rows {
			v := &MyPageVersion{ID: r.ID, ProjectID: r.ProjectID, ProjectName: r.ProjectName, Name: r.Name, Sharing: r.Sharing}
			if r.EffectiveDate.Valid {
				t := r.EffectiveDate.Date.Time
				v.EffectiveDate = &t
			}
			out[r.ID] = v
		}
	}
	return out, nil
}
