package query

import (
	"context"
	"strings"
)

// JournalIDs は IssueQuery#journals (フィルタを満たすチケットの可視なジャーナル)。
// opts.Order は ORDER BY の項目 (issue_journals / issues / projects を参照できる)。
func (q *Query) JournalIDs(ctx context.Context, opts ListOptions) ([]int64, error) {
	vis, err := q.env.Auth.IssueVisibleCondition(ctx, authzOpts())
	if err != nil {
		return nil, err
	}
	notes, err := q.visibleNotesCondition(ctx, "issue_journals")
	if err != nil {
		return nil, err
	}
	st, err := q.statement(ctx)
	if err != nil {
		return nil, err
	}
	where := joinFrags(" AND ", raw("("+vis+")"), raw(notes), paren(st))
	s := "SELECT issue_journals.id FROM issue_journals INNER JOIN issues ON issues.id = issue_journals.issue_id" +
		" INNER JOIN projects ON projects.id = issues.project_id INNER JOIN issue_statuses ON issue_statuses.id = issues.status_id" +
		" WHERE " + where.SQL
	if len(opts.Order) > 0 {
		s += " ORDER BY " + strings.Join(opts.Order, ", ")
	}
	if opts.Limit > 0 || opts.Offset > 0 {
		limit := opts.Limit
		if limit <= 0 {
			limit = -1
		}
		s += " " + q.env.dialect().LimitOffset(limit, opts.Offset)
	}
	var ids []int64
	if err := q.env.Q.Select(ctx, &ids, s, where.Args...); err != nil {
		return nil, stmtErr(err)
	}
	return ids, nil
}

// VersionIDs は IssueQuery#versions (プロジェクト条件を満たす可視なバージョン、id 順)。
func (q *Query) VersionIDs(ctx context.Context, opts ListOptions) ([]int64, error) {
	vis, err := q.env.Auth.AllowedToCondition(ctx, "view_issues", authzOpts(), nil)
	if err != nil {
		return nil, err
	}
	ps, err := q.projectStatement(ctx)
	if err != nil {
		return nil, err
	}
	where := joinFrags(" AND ", raw("("+vis+")"), paren(raw(ps)))
	if strings.TrimSpace(opts.Conditions) != "" {
		where = joinFrags(" AND ", where, sqlf("("+opts.Conditions+")", opts.ConditionArgs...))
	}
	var ids []int64
	if err := q.env.Q.Select(ctx, &ids, "SELECT versions.id FROM versions INNER JOIN projects ON projects.id = versions.project_id WHERE "+
		where.SQL+" ORDER BY versions.id", where.Args...); err != nil {
		return nil, stmtErr(err)
	}
	return ids, nil
}
