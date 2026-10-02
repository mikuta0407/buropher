package repository

import (
	"context"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// このファイルは WatchersController（acts_as_watchable）のための読み書き。

// WatchedByUserOrGroups は watched_by?(user)（本人またはそのグループがウォッチしているか）。
func WatchedByUserOrGroups(ctx context.Context, q db.Queryer, kind string, id, userID int64) (bool, error) {
	var n int
	err := q.Get(ctx, &n, `SELECT COUNT(*) FROM watchers WHERE watchable_kind = ? AND watchable_id = ?
  AND (principal_id = ? OR principal_id IN (SELECT group_id FROM group_users WHERE user_id = ?))`, kind, id, userID, userID)
	return n > 0, err
}

// RemoveWatcher は remove_watcher(user)（watchers.where(user_id: user.id).delete_all）。
func RemoveWatcher(ctx context.Context, q db.Queryer, kind string, id, principalID int64) error {
	_, err := q.Exec(ctx, `DELETE FROM watchers WHERE watchable_kind = ? AND watchable_id = ? AND principal_id = ?`, kind, id, principalID)
	return err
}

// WatcherCandidateOptions は Principal.assignable_watchers の絞り込み。
type WatcherCandidateOptions struct {
	// VisibleCond は Principal.visible の条件（authz の PrincipalVisibleCondition。principals を参照）。
	VisibleCond string
	// ProjectIDs が空でなければそのプロジェクトのメンバー（project.principals）に限る。
	ProjectIDs []int64
	// IDs が空でなければその id に限る。
	IDs []int64
	// Like は Principal.like(q)。
	Like string
	// Limit が正なら件数の上限。
	Limit int
	// UserFormat は Setting.user_format（sorted の並び）。
	UserFormat string
}

// AssignableWatchers は Principal.assignable_watchers（有効で見えるユーザーとグループ）を sorted の順
// （ユーザーが先、名前順）で返す。要素は *domain.User か *domain.Group。
func AssignableWatchers(ctx context.Context, q db.Queryer, o WatcherCandidateOptions) ([]any, error) {
	vis := o.VisibleCond
	if vis == "" {
		vis = "1=1"
	}
	vis = strings.ReplaceAll(vis, "principals.", "p.")
	like, args := PrincipalLikeCondition(o.Like)
	where := "p.status = 1 AND p.kind IN ('user', 'group') AND (" + vis + ") AND " + like
	if len(o.ProjectIDs) > 0 {
		in, inArgs, err := db.In(`p.id IN (SELECT principal_id FROM members m JOIN projects pr ON pr.id = m.project_id WHERE m.project_id IN (?))`, o.ProjectIDs)
		if err != nil {
			return nil, err
		}
		where += " AND " + in
		args = append(args, inArgs...)
	}
	if len(o.IDs) > 0 {
		in, inArgs, err := db.In(`p.id IN (?)`, o.IDs)
		if err != nil {
			return nil, err
		}
		where += " AND " + in
		args = append(args, inArgs...)
	}
	query := `SELECT p.id, p.kind FROM principals p LEFT JOIN user_accounts ua ON ua.principal_id = p.id WHERE ` + where +
		` ORDER BY ` + principalOrder(o.UserFormat)
	if o.Limit > 0 {
		query += " " + q.Dialect().LimitOffset(o.Limit, 0)
	}
	var rows []struct {
		ID   int64  `db:"id"`
		Kind string `db:"kind"`
	}
	if err := q.Select(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	var uids []int64
	for _, r := range rows {
		if r.Kind == string(domain.KindUser) {
			uids = append(uids, r.ID)
		}
	}
	users, err := UsersByIDs(ctx, q, uids)
	if err != nil {
		return nil, err
	}
	var out []any
	for _, r := range rows {
		if r.Kind == string(domain.KindUser) {
			if u := users[r.ID]; u != nil {
				out = append(out, u)
			}
			continue
		}
		g, err := GetGroup(ctx, q, r.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
}

// principalOrder は Principal.sorted（fields_for_order_statement: type DESC, 名前の書式の列, lastname, id）。
// principals を p として参照する。
func principalOrder(userFormat string) string {
	order := []string{"CASE WHEN p.kind = 'user' THEN 1 ELSE 0 END DESC"}
	for _, col := range UserOrderColumns(userFormat) {
		if col == "p.id" {
			continue
		}
		if col == "p.lastname" {
			col = "CASE WHEN p.kind = 'user' THEN p.lastname ELSE p.name END"
		}
		order = append(order, col)
	}
	order = append(order, "CASE WHEN p.kind = 'user' THEN p.lastname ELSE p.name END", "p.id")
	return strings.Join(order, ", ")
}

// WatcherPrincipals は watchable.watcher_users.sorted（ユーザーとグループ）。
func WatcherPrincipals(ctx context.Context, q db.Queryer, kind string, id int64, userFormat string) ([]any, error) {
	var rows []struct {
		ID   int64  `db:"principal_id"`
		Kind string `db:"kind"`
	}
	if err := q.Select(ctx, &rows, `SELECT w.principal_id, p.kind FROM watchers w JOIN principals p ON p.id = w.principal_id
LEFT JOIN user_accounts ua ON ua.principal_id = p.id
WHERE w.watchable_kind = ? AND w.watchable_id = ? ORDER BY `+principalOrder(userFormat), kind, id); err != nil {
		return nil, err
	}
	var out []any
	for _, r := range rows {
		if domain.PrincipalKind(r.Kind).IsGroup() {
			g, err := GetGroup(ctx, q, r.ID)
			if err != nil {
				return nil, err
			}
			out = append(out, g)
			continue
		}
		u, err := GetUser(ctx, q, r.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, nil
}
