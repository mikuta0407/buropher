package repository

import (
	"context"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// このファイルは WatchersController（acts_as_watchable）のための読み書き。

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
