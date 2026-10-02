package repository

// ウォッチャー（WatchersController）とチケットの自動補完（AutoCompletesController#issues）の読み書き。

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// AssignableWatchersOptions は Principal.assignable_watchers（active.visible.where(type: User / Group)）の絞り込み。
type AssignableWatchersOptions struct {
	// VisibleCond は authz の PrincipalVisibleCondition（principals テーブルを名前で参照する断片）。
	VisibleCond string
	// ProjectIDs が nil でなければ、そのプロジェクトのメンバー（project.principals）に限る。
	ProjectIDs []int64
	// Like は Principal.like(q)。
	Like string
	// UsersOnly は where(type: ['User'])（グループを除く）。
	UsersOnly bool
	// Limit は 0 なら無制限。
	Limit int
	// UserFormat は Setting.user_format（sorted の並び）。
	UserFormat string
}

// principalSortedOrder は Principal.sorted（type DESC → User.name_formatter[:order] - id → lastname → id）。
// グループ名は principals.name（Redmine の lastname）。
func principalSortedOrder(format string) string {
	lastname := "CASE WHEN p.kind IN ('user', 'anonymous_user') THEN p.lastname ELSE p.name END"
	cols := map[string]string{
		"firstname": "COALESCE(p.firstname, '')",
		"lastname":  lastname,
		"login":     "COALESCE(ua.login, '')",
	}
	var order []string
	switch format {
	case "firstname":
		order = []string{"firstname"}
	case "lastname_firstname", "lastnamefirstname", "lastname_comma_firstname":
		order = []string{"lastname", "firstname"}
	case "lastname":
		order = []string{"lastname"}
	case "username":
		order = []string{"login"}
	default:
		order = []string{"firstname", "lastname"}
	}
	order = append(order, "lastname")
	out := []string{"CASE WHEN p.kind IN ('user', 'anonymous_user') THEN 0 ELSE 1 END"}
	seen := map[string]bool{}
	for _, k := range order {
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, cols[k])
	}
	return strings.Join(append(out, "p.id"), ", ")
}

// AssignableWatchers は Principal.assignable_watchers（[project.principals.]assignable_watchers.sorted.like(q)[.limit(n)]）。
// グループも domain.User（Kind がグループ）で返す。
func AssignableWatchers(ctx context.Context, q db.Queryer, o AssignableWatchersOptions) ([]*domain.User, error) {
	where := []string{fmt.Sprintf("p.status = %d", domain.StatusActive)}
	if o.UsersOnly {
		where = append(where, "p.kind = 'user'")
	} else {
		where = append(where, "p.kind IN ('user', 'group')")
	}
	if o.VisibleCond != "" {
		where = append(where, "p.id IN (SELECT principals.id FROM principals WHERE "+o.VisibleCond+")")
	}
	if o.ProjectIDs != nil {
		if len(o.ProjectIDs) == 0 {
			where = append(where, "1=0")
		} else {
			where = append(where, "p.id IN (SELECT principal_id FROM members WHERE project_id IN ("+idsSQL(o.ProjectIDs)+"))")
		}
	}
	like, args := PrincipalLikeCondition(o.Like)
	where = append(where, like)
	query := userSelect + " WHERE " + strings.Join(where, " AND ") + " ORDER BY " + principalSortedOrder(o.UserFormat)
	if o.Limit > 0 {
		query += " LIMIT " + strconv.Itoa(o.Limit)
	}
	var rows []userRow
	if err := q.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	out := make([]*domain.User, len(rows))
	for i := range rows {
		out[i] = rows[i].user()
	}
	return out, nil
}

// AssignableWatchersByIDs は Principal.assignable_watchers.where(id: ids)（id 順）。
func AssignableWatchersByIDs(ctx context.Context, q db.Queryer, visibleCond string, ids []int64) ([]*domain.User, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	where := fmt.Sprintf("p.status = %d AND p.kind IN ('user', 'group') AND p.id IN (%s)", domain.StatusActive, idsSQL(ids))
	if visibleCond != "" {
		where += " AND p.id IN (SELECT principals.id FROM principals WHERE " + visibleCond + ")"
	}
	var rows []userRow
	if err := q.Select(ctx, &rows, userSelect+" WHERE "+where+" ORDER BY p.id"); err != nil {
		return nil, err
	}
	out := make([]*domain.User, len(rows))
	for i := range rows {
		out[i] = rows[i].user()
	}
	return out, nil
}

// WatcherPrincipals は watchable の watcher_users（グループを含む。watchers.id 順）。
func WatcherPrincipals(ctx context.Context, q db.Queryer, kind string, id int64) ([]*domain.User, error) {
	var rows []userRow
	err := q.Select(ctx, &rows, userSelect+` JOIN watchers w ON w.principal_id = p.id
WHERE w.watchable_kind = ? AND w.watchable_id = ? ORDER BY w.id`, kind, id)
	if err != nil {
		return nil, err
	}
	out := make([]*domain.User, len(rows))
	for i := range rows {
		out[i] = rows[i].user()
	}
	return out, nil
}

// AnyWatched は Watcher.any_watched?(objects, user)（直接のウォッチのみ）。
func AnyWatched(ctx context.Context, q db.Queryer, kind string, ids []int64, userID int64) (bool, error) {
	if len(ids) == 0 {
		return false, nil
	}
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM watchers WHERE watchable_kind = ? AND watchable_id IN (`+idsSQL(ids)+`) AND principal_id = ?`, kind, userID)
	return n > 0, err
}

// RemoveWatcher は remove_watcher(principal)（watchers.where(user_id:).delete_all）。
func RemoveWatcher(ctx context.Context, q db.Queryer, kind string, id, principalID int64) error {
	_, err := q.Exec(ctx, `DELETE FROM watchers WHERE watchable_kind = ? AND watchable_id = ? AND principal_id = ?`, kind, id, principalID)
	return err
}

func idsSQL(ids []int64) string {
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(s, ", ")
}

// ---------------------------------------------------------------- AutoCompletesController#issues

// AutoCompleteIssue は自動補完の 1 件（id・トラッカー名・題名）。
type AutoCompleteIssue struct {
	ID      int64  `db:"id"`
	Tracker string `db:"tracker"`
	Subject string `db:"subject"`
}

// AutoCompleteIssuesScope は Issue.cross_project_scope(project, scope).visible[.open(...)][.where.not(id:)] の条件。
type AutoCompleteIssuesScope struct {
	// ProjectID が 0 なら全プロジェクト（cross_project_scope(nil)）。
	ProjectID int64
	// Scope は params[:scope]（all / system / tree / hierarchy / descendants / それ以外）。
	Scope string
	// VisibleCond は authz の IssueVisibleCondition（issues と projects を参照）。
	VisibleCond string
	// Open は nil なら絞り込まない。true なら未完了、false なら完了（scope.open(status == 'o')）。
	Open *bool
	// ExcludeID が 0 でなければその id を除く。
	ExcludeID int64
	// HasExclude は where.not(:id => issue_id.to_i)（issue_id が "0" 等でも条件を付ける）。
	HasExclude bool
}

// crossProjectCondition は Issue.cross_project_scope(project, scope) の条件。
func (s AutoCompleteIssuesScope) crossProjectCondition() string {
	if s.ProjectID == 0 {
		return ""
	}
	pid := strconv.FormatInt(s.ProjectID, 10)
	descendantsOf := func(id string) string {
		return "issues.project_id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = " + id + ")"
	}
	switch s.Scope {
	case "all", "system":
		return ""
	case "tree":
		root := "(SELECT pc.ancestor_id FROM project_closure pc JOIN projects rp ON rp.id = pc.ancestor_id WHERE pc.descendant_id = " + pid + " AND rp.parent_id IS NULL)"
		return descendantsOf(root)
	case "hierarchy":
		return "(" + descendantsOf(pid) + " OR issues.project_id IN (SELECT ancestor_id FROM project_closure WHERE descendant_id = " + pid + " AND depth > 0))"
	case "descendants":
		return descendantsOf(pid)
	}
	return "issues.project_id = " + pid
}

// AutoCompleteIssues は scope[.where(extra)].order(id: :desc).limit(limit)。
func AutoCompleteIssues(ctx context.Context, q db.Queryer, s AutoCompleteIssuesScope, extra string, extraArgs []any, limit int) ([]*AutoCompleteIssue, error) {
	where := []string{}
	if c := s.crossProjectCondition(); c != "" {
		where = append(where, c)
	}
	if s.VisibleCond != "" {
		where = append(where, "("+s.VisibleCond+")")
	}
	if s.Open != nil {
		where = append(where, "issue_statuses.is_closed = "+q.Dialect().BoolLiteral(!*s.Open))
	}
	if s.HasExclude {
		where = append(where, "issues.id <> "+strconv.FormatInt(s.ExcludeID, 10))
	}
	if extra != "" {
		where = append(where, "("+extra+")")
	}
	query := `SELECT issues.id, trackers.name AS tracker, issues.subject FROM issues
JOIN projects ON projects.id = issues.project_id
JOIN trackers ON trackers.id = issues.tracker_id
JOIN issue_statuses ON issue_statuses.id = issues.status_id`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY issues.id DESC"
	if limit > 0 {
		query += " LIMIT " + strconv.Itoa(limit)
	}
	var rows []*AutoCompleteIssue
	if err := q.Select(ctx, &rows, query, extraArgs...); err != nil {
		return nil, err
	}
	return rows, nil
}

// WikiPageProjectID は Wiki ページの project_id（ok = 存在する）。
func WikiPageProjectID(ctx context.Context, q db.Queryer, pageID int64) (int64, bool, error) {
	return projectIDOf(ctx, q, `SELECT w.project_id FROM wiki_pages p JOIN wikis w ON w.id = p.wiki_id WHERE p.id = ?`, pageID)
}

// WikiProjectID は Wiki の project_id（ok = 存在する）。
func WikiProjectID(ctx context.Context, q db.Queryer, wikiID int64) (int64, bool, error) {
	return projectIDOf(ctx, q, `SELECT project_id FROM wikis WHERE id = ?`, wikiID)
}

func projectIDOf(ctx context.Context, q db.Queryer, query string, id int64) (int64, bool, error) {
	var ids []int64
	if err := q.Select(ctx, &ids, query, id); err != nil {
		return 0, false, err
	}
	if len(ids) == 0 {
		return 0, false, nil
	}
	return ids[0], true, nil
}

// IsUniqueViolation は一意制約違反（ActiveRecord::RecordNotUnique）か（SQLite / PostgreSQL）。
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "UNIQUE constraint failed") || strings.Contains(s, "SQLSTATE 23505") ||
		strings.Contains(s, "duplicate key value violates unique constraint")
}
