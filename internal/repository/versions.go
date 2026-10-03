package repository

// このファイルは Version モデル（app/models/version.rb）の永続化と集計（VersionsController 用）。
// 関数名は他の並行ブランチ（issues_read.go の VersionsByIDs 等）と衝突しないよう Version* / *VersionRecord とする。

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

type versionRecordRow struct {
	ID            int64          `db:"id"`
	ProjectID     int64          `db:"project_id"`
	Name          string         `db:"name"`
	Description   sql.NullString `db:"description"`
	EffectiveDate db.NullDate    `db:"effective_date"`
	WikiPageTitle sql.NullString `db:"wiki_page_title"`
	Status        string         `db:"status"`
	Sharing       string         `db:"sharing"`
	CreatedAt     db.Time        `db:"created_at"`
	UpdatedAt     db.Time        `db:"updated_at"`
}

const versionRecordCols = `versions.id, versions.project_id, versions.name, versions.description, versions.effective_date,
  versions.wiki_page_title, versions.status, versions.sharing, versions.created_at, versions.updated_at`

func (r *versionRecordRow) version() *domain.Version {
	v := &domain.Version{ID: r.ID, ProjectID: r.ProjectID, Name: r.Name, Status: r.Status, Sharing: r.Sharing,
		CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}
	if r.EffectiveDate.Valid {
		t := r.EffectiveDate.Date.Time
		v.EffectiveDate = &t
	}
	if r.Description.Valid {
		s := r.Description.String
		v.Description = &s
	}
	if r.WikiPageTitle.Valid {
		s := r.WikiPageTitle.String
		v.WikiPageTitle = &s
	}
	return v
}

// GetVersion は id のバージョン（無ければ ErrNotFound）。
func GetVersion(ctx context.Context, q db.Queryer, id int64) (*domain.Version, error) {
	var r versionRecordRow
	if err := q.Get(ctx, &r, `SELECT `+versionRecordCols+` FROM versions WHERE versions.id = ?`, id); err != nil {
		return nil, notFound(err)
	}
	return r.version(), nil
}

// LoadVersionsWhere は条件（versions と projects を参照してよい）に合うバージョンを id 順で返す
// （Redmine の順序指定のないスコープ。参照環境の SQLite は主キー順に返す）。
func LoadVersionsWhere(ctx context.Context, q db.Queryer, where string, args ...any) ([]*domain.Version, error) {
	if where == "" {
		where = "1=1"
	}
	var rows []versionRecordRow
	if err := q.Select(ctx, &rows, `SELECT `+versionRecordCols+` FROM versions JOIN projects ON projects.id = versions.project_id
WHERE `+where+` ORDER BY versions.id`, args...); err != nil {
		return nil, err
	}
	out := make([]*domain.Version, len(rows))
	for i := range rows {
		out[i] = rows[i].version()
	}
	return out, nil
}

// VersionNameTaken は validates_uniqueness_of :name, :scope => :project_id（大文字小文字を区別）。
func VersionNameTaken(ctx context.Context, q db.Queryer, projectID int64, name string, excludeID int64) (bool, error) {
	return exists(ctx, q, `SELECT 1 FROM versions WHERE project_id = ? AND name = ? AND id <> ?`, projectID, name, excludeID)
}

func versionNullable(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func versionDate(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format("2006-01-02")
}

// InsertVersion はバージョンを作成し、v.ID・CreatedAt・UpdatedAt を設定する。
func InsertVersion(ctx context.Context, q db.Queryer, v *domain.Version) error {
	now := db.Now()
	id, err := q.InsertReturningID(ctx, `INSERT INTO versions (project_id, name, description, effective_date, wiki_page_title, status, sharing, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, v.ProjectID, v.Name, versionNullable(v.Description), versionDate(v.EffectiveDate),
		versionNullable(v.WikiPageTitle), v.Status, v.Sharing, now, now)
	if err != nil {
		return err
	}
	v.ID = id
	v.CreatedAt, v.UpdatedAt = now.Time, now.Time
	return nil
}

// UpdateVersion はバージョンの属性を保存する（updated_at を更新する）。
func UpdateVersion(ctx context.Context, q db.Queryer, v *domain.Version) error {
	now := db.Now()
	_, err := q.Exec(ctx, `UPDATE versions SET name = ?, description = ?, effective_date = ?, wiki_page_title = ?, status = ?, sharing = ?, updated_at = ?
WHERE id = ?`, v.Name, versionNullable(v.Description), versionDate(v.EffectiveDate), versionNullable(v.WikiPageTitle),
		v.Status, v.Sharing, now, v.ID)
	if err == nil {
		v.UpdatedAt = now.Time
	}
	return err
}

// TouchVersion は updated_at だけを更新する（カスタムフィールド値だけが変わった保存）。
func TouchVersion(ctx context.Context, q db.Queryer, v *domain.Version) error {
	now := db.Now()
	_, err := q.Exec(ctx, `UPDATE versions SET updated_at = ? WHERE id = ?`, now, v.ID)
	if err == nil {
		v.UpdatedAt = now.Time
	}
	return err
}

// SetVersionStatus は update_attribute(:status, status)。
func SetVersionStatus(ctx context.Context, q db.Queryer, id int64, status string) error {
	_, err := q.Exec(ctx, `UPDATE versions SET status = ?, updated_at = ? WHERE id = ?`, status, db.Now(), id)
	return err
}

// DeleteVersion はバージョンを削除する（before_destroy :nullify_projects_default_version、
// fixed_issues の :dependent => :nullify、カスタムフィールド値の削除）。
func DeleteVersion(ctx context.Context, q db.Queryer, id int64) error {
	if _, err := q.Exec(ctx, `UPDATE projects SET default_version_id = NULL WHERE default_version_id = ?`, id); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `UPDATE issues SET fixed_version_id = NULL WHERE fixed_version_id = ?`, id); err != nil {
		return err
	}
	if err := DeleteCustomValues(ctx, q, "version", id); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `DELETE FROM versions WHERE id = ?`, id)
	return err
}

// SetProjectDefaultVersion は project.update_columns :default_version_id => id。
func SetProjectDefaultVersion(ctx context.Context, q db.Queryer, projectID, versionID int64) error {
	_, err := q.Exec(ctx, `UPDATE projects SET default_version_id = ? WHERE id = ?`, versionID, projectID)
	return err
}

// VersionHasFixedIssues は fixed_issues.exists?。
func VersionHasFixedIssues(ctx context.Context, q db.Queryer, id int64) (bool, error) {
	return exists(ctx, q, `SELECT 1 FROM issues WHERE fixed_version_id = ?`, id)
}

// VersionReferencedByCustomField は referenced_by_a_custom_field?。
func VersionReferencedByCustomField(ctx context.Context, q db.Queryer, id int64) (bool, error) {
	return exists(ctx, q, `SELECT 1 FROM custom_values JOIN custom_fields ON custom_fields.id = custom_values.custom_field_id
WHERE custom_values.value = ? AND custom_fields.field_format = 'version'`, strconv.FormatInt(id, 10))
}

// VersionHasAttachments は attachments.any?。
func VersionHasAttachments(ctx context.Context, q db.Queryer, id int64) (bool, error) {
	return exists(ctx, q, `SELECT 1 FROM attachments WHERE container_kind = 'version' AND container_id = ?`, id)
}

// VersionIssue は fixed_issues の 1 件（進捗・一覧の計算に必要な列）。
type VersionIssue struct {
	RefIssue
	EstimatedHours sql.NullFloat64 `db:"estimated_hours"`
	RootID         int64           `db:"root_id"`
	HierPath       string          `db:"hier_path"`
	TrackerPos     int             `db:"tracker_position"`
	FixedVersionID int64           `db:"fixed_version_id"`
	// Visible は VersionIssues の visibleCond を満たすか（visibleCond を渡したときだけ設定される）。
	Visible bool `db:"visible"`
}

// VersionIssues は versionIDs を対象バージョンとするチケットを返す。cond は可視性の SQL（空なら全件）、
// extra は追加の条件。並びは order（空なら issues.id）。
func VersionIssues(ctx context.Context, q db.Queryer, versionIDs []int64, cond, extra, order string, args ...any) ([]*VersionIssue, error) {
	return versionIssues(ctx, q, versionIDs, cond, extra, order, "", args...)
}

// VersionIssuesWithVisibility は VersionIssues(versionIDs, "", "", "") の各行に、visibleCond を満たすか
// （VersionIssue.Visible）を付けて返す（fixed_issues と visible_fixed_issues を 1 回で読む）。
func VersionIssuesWithVisibility(ctx context.Context, q db.Queryer, versionIDs []int64, visibleCond string) ([]*VersionIssue, error) {
	if visibleCond == "" {
		visibleCond = "1=1"
	}
	return versionIssues(ctx, q, versionIDs, "", "", "", ",\n  CASE WHEN ("+visibleCond+") THEN 1 ELSE 0 END AS visible")
}

func versionIssues(ctx context.Context, q db.Queryer, versionIDs []int64, cond, extra, order, extraCols string, args ...any) ([]*VersionIssue, error) {
	if len(versionIDs) == 0 {
		return nil, nil
	}
	where := "issues.fixed_version_id IN (" + versionJoinIDs(versionIDs) + ")"
	if cond != "" {
		where += " AND (" + cond + ")"
	}
	if extra != "" {
		where += " AND (" + extra + ")"
	}
	if order == "" {
		order = "issues.id"
	}
	var rows []*VersionIssue
	err := q.Select(ctx, &rows, `SELECT issues.id, issues.project_id, projects.name AS project_name,
  issues.tracker_id, trackers.name AS tracker_name, issues.status_id, issue_statuses.name AS status_name,
  issue_statuses.is_closed AS status_closed, issues.priority_id, issue_priorities.position_name AS priority_position_name,
  issues.subject, issues.author_id, issues.assigned_to_id, issues.parent_id,
  EXISTS (SELECT 1 FROM issues c WHERE c.parent_id = issues.id) AS has_children,
  issues.is_private, issues.start_date, issues.due_date, issues.done_ratio,
  issues.estimated_hours, issues.root_id, issues.hier_path, trackers.position AS tracker_position, issues.fixed_version_id`+extraCols+`
FROM issues
JOIN projects ON projects.id = issues.project_id
JOIN trackers ON trackers.id = issues.tracker_id
JOIN issue_statuses ON issue_statuses.id = issues.status_id
JOIN issue_priorities ON issue_priorities.id = issues.priority_id
WHERE `+where+` ORDER BY `+order, args...)
	return rows, err
}

// IssueSubtreeEstimatedHours は self_and_descendants（cond で可視なもの）の estimated_hours の合計。
func IssueSubtreeEstimatedHours(ctx context.Context, q db.Queryer, rootID int64, hierPath, cond string) (float64, error) {
	if cond == "" {
		cond = "1=1"
	}
	var h sql.NullFloat64
	err := q.Get(ctx, &h, `SELECT SUM(issues.estimated_hours) FROM issues JOIN projects ON projects.id = issues.project_id
WHERE issues.root_id = ? AND issues.hier_path LIKE ? AND (`+cond+`)`, rootID, hierPath+"%")
	return h.Float64, err
}

// IssuesSubtreeEstimatedHours は ids の各チケットについて IssueSubtreeEstimatedHours を一度に求める。
// 可視な子孫が無いチケットは結果に含まれない (値 0 として扱うこと)。
func IssuesSubtreeEstimatedHours(ctx context.Context, q db.Queryer, ids []int64, cond string) (map[int64]float64, error) {
	if cond == "" {
		cond = "1=1"
	}
	out := map[int64]float64{}
	for _, chunk := range chunkIDs(uniqIDs(ids)) {
		var rows []struct {
			ID  int64           `db:"id"`
			Sum sql.NullFloat64 `db:"total"`
		}
		if err := q.Select(ctx, &rows, `SELECT parent.id AS id, SUM(issues.estimated_hours) AS total FROM issues parent
JOIN issues ON issues.root_id = parent.root_id AND issues.hier_path LIKE (parent.hier_path || '%')
JOIN projects ON projects.id = issues.project_id
WHERE parent.id IN (`+versionJoinIDs(chunk)+`) AND (`+cond+`) GROUP BY parent.id`); err != nil {
			return nil, err
		}
		for _, r := range rows {
			out[r.ID] = r.Sum.Float64
		}
	}
	return out, nil
}

// VersionEstimatedHours は visible_fixed_issues の estimated_hours と estimated_remaining_hours
// （IssueQuery::ESTIMATED_REMAINING_HOURS_SQL）の合計。
func VersionEstimatedHours(ctx context.Context, q db.Queryer, id int64, cond string) (est, remaining float64, err error) {
	if cond == "" {
		cond = "1=1"
	}
	var r struct {
		Est sql.NullFloat64 `db:"est"`
		Rem sql.NullFloat64 `db:"rem"`
	}
	err = q.Get(ctx, &r, `SELECT SUM(issues.estimated_hours) AS est,
  SUM(COALESCE(issues.estimated_hours, 0) * (100 - COALESCE(issues.done_ratio, 0)) / 100) AS rem
FROM issues JOIN projects ON projects.id = issues.project_id
WHERE issues.fixed_version_id = ? AND (`+cond+`)`, id)
	return r.Est.Float64, r.Rem.Float64, err
}

// ProjectDescendantsExist は project.descendants（cond に合うもの）.any?。
func ProjectDescendantsExist(ctx context.Context, q db.Queryer, projectID int64, cond string) (bool, error) {
	if cond == "" {
		cond = "1=1"
	}
	return exists(ctx, q, `SELECT 1 FROM projects WHERE projects.id IN
  (SELECT descendant_id FROM project_closure WHERE ancestor_id = ? AND depth > 0) AND (`+cond+`)`, projectID)
}

// VersionSpentHours は Version#spent_hours（TimeEntry.joins(:issue).where(fixed_version_id).sum(:hours)）。
func VersionSpentHours(ctx context.Context, q db.Queryer, id int64) (float64, error) {
	var h sql.NullFloat64
	err := q.Get(ctx, &h, `SELECT SUM(time_entries.hours) FROM time_entries JOIN issues ON issues.id = time_entries.issue_id
WHERE issues.fixed_version_id = ?`, id)
	return h.Float64, err
}

// VersionStatusByCount は render_issue_status_by の集計 1 行（グループの値と件数）。
type VersionStatusByCount struct {
	Key   sql.NullInt64 `db:"k"`
	Name  string        `db:"-"`
	Count int           `db:"n"`
}

// VersionIssueCountsBy は visible_fixed_issues(.open).group(criteria).count。
// column は issues の列（tracker_id 等）。
func VersionIssueCountsBy(ctx context.Context, q db.Queryer, versionID int64, column, cond string, openOnly bool) ([]VersionStatusByCount, error) {
	if cond == "" {
		cond = "1=1"
	}
	where := "issues.fixed_version_id = ? AND (" + cond + ")"
	if openOnly {
		where += " AND issue_statuses.is_closed = " + q.Dialect().BoolLiteral(false)
	}
	var rows []VersionStatusByCount
	err := q.Select(ctx, &rows, `SELECT issues.`+column+` AS k, COUNT(*) AS n FROM issues
JOIN projects ON projects.id = issues.project_id
JOIN issue_statuses ON issue_statuses.id = issues.status_id
WHERE `+where+` GROUP BY issues.`+column, versionID)
	return rows, err
}

// ProjectWikiExists は project.wiki が存在するか（wiki モジュールの有無によらない）。
func ProjectWikiExists(ctx context.Context, q db.Queryer, projectID int64) (bool, error) {
	return exists(ctx, q, `SELECT 1 FROM wikis WHERE project_id = ?`, projectID)
}

// VersionWikiPageText は project.wiki.find_page(title) の本文（無ければ ok=false）。
// find_page は大文字小文字を区別せず、リダイレクトは辿らない簡易版。
func VersionWikiPageText(ctx context.Context, q db.Queryer, projectID int64, title string) (pageID int64, text string, ok bool, err error) {
	var r struct {
		ID   int64          `db:"id"`
		Text sql.NullString `db:"text"`
	}
	err = q.Get(ctx, &r, `SELECT wiki_pages.id, (SELECT v.text FROM wiki_page_versions v WHERE v.page_id = wiki_pages.id ORDER BY v.version DESC LIMIT 1) AS text
FROM wiki_pages JOIN wikis ON wikis.id = wiki_pages.wiki_id
WHERE wikis.project_id = ? AND LOWER(wiki_pages.title) = LOWER(?) ORDER BY wiki_pages.id LIMIT 1`, projectID, title)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, "", false, nil
		}
		return 0, "", false, err
	}
	return r.ID, r.Text.String, true, nil
}

// VersionStatusByObject は render_issue_status_by のグループのオブジェクト（名前と並び順）。
type VersionStatusByObject struct {
	ID       int64  `db:"id"`
	Name     string `db:"name"`
	Position int    `db:"position"`
}

// VersionStatusByObjects は criteria（tracker / status / priority / author / assigned_to / category）の
// オブジェクトを ids で読み込む。author / assigned_to の名前は呼び出し側で整える。
func VersionStatusByObjects(ctx context.Context, q db.Queryer, criteria string, ids []int64) ([]VersionStatusByObject, error) {
	var query string
	switch criteria {
	case "tracker":
		query = `SELECT id, name, position FROM trackers`
	case "status":
		query = `SELECT id, name, position FROM issue_statuses`
	case "priority":
		query = `SELECT id, name, position FROM issue_priorities`
	case "category":
		query = `SELECT id, name, 0 AS position FROM issue_categories`
	default:
		query = `SELECT id, '' AS name, 0 AS position FROM principals`
	}
	var rows []VersionStatusByObject
	err := q.Select(ctx, &rows, query+` WHERE id IN (`+versionJoinIDs(ids)+`)`)
	return rows, err
}

func versionJoinIDs(ids []int64) string {
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(s, ",")
}
