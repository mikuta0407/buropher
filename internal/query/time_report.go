package query

import (
	"context"
	"fmt"
	"strings"
)

// GroupedSums は results_scope（並びなし）を exprs でグループ化し、各グループの SUM(sumExpr) を返す
// （Redmine::Helpers::TimeReport#run の scope.group(...).joins(...).sum(:hours)）。
// 各行の Keys は exprs と同じ順の値（NULL は nil、[]byte は string に変換）。行は exprs の昇順。
func (q *Query) GroupedSums(ctx context.Context, exprs []string, joins []string, sumExpr string) ([]GroupedSum, error) {
	s, args, err := q.baseSQL(ctx, joins, nil)
	if err != nil {
		return nil, err
	}
	g := strings.Join(exprs, ", ")
	sqlText := "SELECT " + g + ", SUM(" + sumExpr + ")" + s + " GROUP BY " + g + " ORDER BY " + g
	rows, err := q.env.Q.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, stmtErr(fmt.Errorf("%w\n  sql: %s", err, sqlText))
	}
	defer rows.Close()
	var out []GroupedSum
	for rows.Next() {
		vals := make([]any, len(exprs)+1)
		ptrs := make([]any, len(vals))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				vals[i] = string(b)
			}
		}
		out = append(out, GroupedSum{Keys: vals[:len(exprs)], Sum: toFloat(vals[len(exprs)])})
	}
	return out, stmtErr(rows.Err())
}

// GroupedSum は GroupedSums の 1 行。
type GroupedSum struct {
	Keys []any
	Sum  float64
}
