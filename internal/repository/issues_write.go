package repository

// チケットの書き込み系画面（新規作成フォーム等）で使う問い合わせ。

import (
	"context"

	"github.com/mikuta0407/buropher/internal/db"
)

// ProjectAssignableWatcherIDs は project.principals.assignable_watchers.limit(limit) の id
// （プロジェクトのメンバーのうち有効で閲覧できるユーザー・グループ。visibleCond は Principal.visible の条件）。
func ProjectAssignableWatcherIDs(ctx context.Context, q db.Queryer, projectID int64, visibleCond string, limit int) ([]int64, error) {
	if visibleCond == "" {
		visibleCond = "1=1"
	}
	var ids []int64
	err := q.Select(ctx, &ids, `SELECT principals.id FROM principals INNER JOIN members ON members.principal_id = principals.id
WHERE members.project_id = ? AND principals.status = 1 AND principals.kind IN ('user', 'group') AND (`+visibleCond+`)
ORDER BY members.id LIMIT ?`, projectID, limit)
	return ids, err
}
