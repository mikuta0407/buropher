// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package query

import (
	"context"
	"strings"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/db"
)

func authzOpts() authz.ConditionOptions { return authz.ConditionOptions{} }

// sqlForAnySearchable は sql_for_any_searchable_field (Redmine::Search::Fetcher で
// チケット (件名・説明・検索可能な CF・注記。添付は除く) を検索し、その id で絞り込む)。
func (q *Query) sqlForAnySearchable(ctx context.Context, operator string, v []string) (frag, error) {
	question := first(v)
	projCond, err := q.env.Auth.AllowedToCondition(ctx, "view_issues", authz.ConditionOptions{}, nil)
	if err != nil {
		return frag{}, err
	}
	var projects *frag
	switch {
	case q.Project != nil:
		ps, err := q.projectStatement(ctx)
		if err != nil {
			return frag{}, err
		}
		f := raw("(" + projCond + ") AND (" + ps + ")")
		projects = &f
	case q.HasFilter("project_id"):
		var ids []string
		switch q.ValueFor("project_id", 0) {
		case "mine":
			pids, err := q.env.Auth.ProjectIDs(ctx)
			if err != nil {
				return frag{}, err
			}
			for _, id := range pids {
				ids = append(ids, itoa(id))
			}
		case "bookmarks":
			pids, err := q.env.BookmarkedProjectIDs(ctx)
			if err != nil {
				return frag{}, err
			}
			for _, id := range pids {
				ids = append(ids, itoa(id))
			}
		default:
			ids = q.ValuesFor("project_id")
		}
		pf, err := q.sqlForField(ctx, "project_id", q.OperatorFor("project_id"), ids, "projects", "id", false)
		if err != nil {
			return frag{}, err
		}
		f := concat(raw("("+projCond+") AND ("), pf, raw(")"))
		projects = &f
	}
	allWords := operator == "~"
	openIssues := q.HasFilter("status_id") && q.OperatorFor("status_id") == "o"
	ids, err := q.searchIssueIDs(ctx, question, projects, allWords, openIssues)
	if err != nil {
		return frag{}, err
	}
	if len(ids) == 0 {
		if operator == "!~" {
			return raw("1=1"), nil
		}
		return raw("1=0"), nil
	}
	sw := ""
	if operator == "!~" {
		sw = "NOT"
	}
	return raw("issues.id " + sw + " IN (" + idList(ids) + ")"), nil
}

// searchIssueIDs は Issue.search_result_ranks_and_ids (attachments: '0') の id (順序は問わない)。
func (q *Query) searchIssueIDs(ctx context.Context, question string, projects *frag, allWords, openIssues bool) ([]int64, error) {
	tokens := Tokenize(strings.TrimSpace(question))
	vis, err := q.env.Auth.IssueVisibleCondition(ctx, authz.ConditionOptions{})
	if err != nil {
		return nil, err
	}
	scope := frag{SQL: "(" + vis + ")"}
	if openIssues {
		scope = joinFrags(" AND ", scope, raw("issues.status_id IN (SELECT id FROM issue_statuses WHERE is_closed = "+q.env.boolLit(false)+")"))
	}
	if projects != nil {
		var pids []int64
		if err := q.env.Q.Select(ctx, &pids, "SELECT projects.id FROM projects WHERE "+projects.SQL, projects.Args...); err != nil {
			return nil, err
		}
		if len(pids) == 0 {
			return nil, nil
		}
		scope = joinFrags(" AND ", scope, raw("issues.project_id IN ("+idList(pids)+")"))
	}
	d := q.env.dialect()
	seen := map[int64]bool{}
	var ids []int64
	run := func(joins string, where frag) error {
		var got []int64
		w := joinFrags(" AND ", scope, where)
		if err := q.env.Q.Select(ctx, &got, "SELECT DISTINCT issues.id FROM issues INNER JOIN projects ON projects.id = issues.project_id "+joins+" WHERE "+w.SQL, w.Args...); err != nil {
			return err
		}
		for _, id := range got {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		return nil
	}
	if err := run("", searchTokensCondition(d, []string{"issues.subject", "issues.description"}, tokens, allWords)); err != nil {
		return nil, err
	}
	cfs, err := customfield.Load(ctx, q.env.Q, "custom_fields.owner_kind = 'issue' AND custom_fields.searchable = ?", true)
	if err != nil {
		return nil, err
	}
	if len(cfs) > 0 {
		var clauses []string
		groups := map[string][]int64{}
		var order []string
		for _, cf := range cfs {
			v, err := customfield.VisibilityByProjectCondition(ctx, q.env.Auth, cf, "issues.project_id", "custom_values.custom_field_id")
			if err != nil {
				return nil, err
			}
			if _, ok := groups[v]; !ok {
				order = append(order, v)
			}
			groups[v] = append(groups[v], cf.ID)
		}
		for _, v := range order {
			clauses = append(clauses, "(custom_values.custom_field_id IN ("+idList(groups[v])+") AND ("+v+"))")
		}
		where := joinFrags(" AND ", raw("("+strings.Join(clauses, " OR ")+")"), searchTokensCondition(d, []string{"custom_values.value"}, tokens, allWords))
		if err := run("INNER JOIN custom_values ON custom_values.customized_kind = 'issue' AND custom_values.customized_id = issues.id", where); err != nil {
			return nil, err
		}
	}
	perm, err := q.env.Auth.AllowedToCondition(ctx, "view_private_notes", authz.ConditionOptions{}, nil)
	if err != nil {
		return nil, err
	}
	where := joinFrags(" AND ", raw("(issue_journals.private_notes = "+q.env.boolLit(false)+" OR ("+perm+"))"),
		searchTokensCondition(d, []string{"issue_journals.notes"}, tokens, allWords))
	if err := run("INNER JOIN issue_journals ON issue_journals.issue_id = issues.id", where); err != nil {
		return nil, err
	}
	sortInt64(ids)
	return ids, nil
}

// searchTokensCondition は search_tokens_condition (各トークンについて列のいずれかに LIKE)。
func searchTokensCondition(d db.Dialect, columns, tokens []string, allWords bool) frag {
	if len(tokens) == 0 {
		return frag{}
	}
	var perToken []frag
	for _, t := range tokens {
		var cols []frag
		for _, c := range columns {
			cols = append(cols, sqlf("("+d.ILike(c)+")", "%"+db.EscapeLike(t)+"%"))
		}
		perToken = append(perToken, joinFrags(" OR ", cols...).wrap("(", ")"))
	}
	sep := " OR "
	if allWords {
		sep = " AND "
	}
	return joinFrags(sep, perToken...).wrap("(", ")")
}
