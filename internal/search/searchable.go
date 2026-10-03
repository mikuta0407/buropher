// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package search

import (
	"context"
	"sort"
	"strings"

	"github.com/mikuta0407/buropher/internal/activity"
	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/db"
)

// searchable は acts_as_searchable のオプション。
type searchable struct {
	// from は FROM 句（projects を結合済み。scope と visible の joins を含む）。
	from string
	// idCol / dateCol は id と :date_column。
	idCol, dateCol string
	// columns は :columns（titles_only では先頭のみ）。
	columns []string
	// projectKey は :project_key。
	projectKey string
	// permission は :permission（空なら visible スコープ = visibleCond）。
	permission string
	// cfKind は検索可能なカスタムフィールドの種別（reflect_on_association(:custom_values)。空なら無し）。
	cfKind customfield.OwnerKind
	// customizedKind は custom_values.customized_kind / attachments.container_kind。
	customizedKind string
	// attachments は acts_as_attachable か。
	attachments bool
	// journals は has_many :journals か（Issue のみ）。
	journals bool
	// visibleCond は visible(user) の WHERE 条件。
	visibleCond func(ctx context.Context, a *authz.Authorizer) (string, error)
}

func allowed(perm string) func(ctx context.Context, a *authz.Authorizer) (string, error) {
	return func(ctx context.Context, a *authz.Authorizer) (string, error) {
		return a.AllowedToCondition(ctx, perm, authz.ConditionOptions{}, nil)
	}
}

var searchables = map[string]*searchable{
	"issues": {
		from:  "issues INNER JOIN projects ON projects.id = issues.project_id",
		idCol: "issues.id", dateCol: "issues.created_at",
		columns:    []string{"issues.subject", "issues.description"},
		projectKey: "issues.project_id",
		cfKind:     customfield.KindIssue, customizedKind: "issue", attachments: true, journals: true,
		visibleCond: func(ctx context.Context, a *authz.Authorizer) (string, error) {
			return a.IssueVisibleCondition(ctx, authz.ConditionOptions{})
		},
	},
	"news": {
		from:  "news INNER JOIN projects ON projects.id = news.project_id",
		idCol: "news.id", dateCol: "news.created_at",
		columns:        []string{"news.title", "news.summary", "news.description"},
		projectKey:     "news.project_id",
		customizedKind: "news", attachments: true,
		visibleCond: allowed("view_news"),
	},
	"documents": {
		from:  "documents INNER JOIN projects ON projects.id = documents.project_id",
		idCol: "documents.id", dateCol: "documents.created_at",
		columns:    []string{"documents.title", "documents.description"},
		projectKey: "documents.project_id",
		cfKind:     customfield.KindDocument, customizedKind: "document", attachments: true,
		visibleCond: allowed("view_documents"),
	},
	"changesets": {
		from: "changesets INNER JOIN repositories ON repositories.id = changesets.repository_id" +
			" INNER JOIN projects ON projects.id = repositories.project_id",
		idCol: "changesets.id", dateCol: "changesets.committed_at",
		columns:     []string{"changesets.comments"},
		projectKey:  "repositories.project_id",
		visibleCond: allowed("view_changesets"),
	},
	"wiki_pages": {
		from:  activity.WikiPageFrom,
		idCol: "wiki_pages.id", dateCol: "wiki_pages.created_at",
		columns:        []string{"wiki_pages.title", "wiki_contents.text"},
		projectKey:     "wikis.project_id",
		permission:     "view_wiki_pages",
		customizedKind: "wiki_page", attachments: true,
	},
	"messages": {
		from: "messages INNER JOIN boards ON boards.id = messages.board_id" +
			" INNER JOIN projects ON projects.id = boards.project_id",
		idCol: "messages.id", dateCol: "messages.created_at",
		columns:        []string{"messages.subject", "messages.content"},
		projectKey:     "boards.project_id",
		customizedKind: "message", attachments: true,
		visibleCond: allowed("view_messages"),
	},
	"projects": {
		from:  "projects",
		idCol: "projects.id", dateCol: "projects.created_at",
		columns:    []string{"projects.name", "projects.identifier", "projects.description"},
		projectKey: "projects.id",
		// :permission => nil（allowed_to_condition(user, :view_project)）
		permission: "view_project",
		cfKind:     customfield.KindProject, customizedKind: "project", attachments: true,
	},
}

type rank struct{ ts, id int64 }

// frag は SQL 断片と引数。
type frag struct {
	sql  string
	args []any
}

// searchScope は search_scope(user, projects, options)。ok = false なら結果なし（none）。
func (f *Fetcher) searchScope(ctx context.Context, def *searchable) (string, []frag, bool, error) {
	if f.projects != nil && len(f.projects) == 0 {
		return "", nil, false, nil
	}
	from := def.from
	var conds []frag
	if def == searchables["issues"] && f.opts.OpenIssues {
		from += " INNER JOIN issue_statuses ON issue_statuses.id = issues.status_id"
		conds = append(conds, frag{sql: "issue_statuses.is_closed = " + f.Q.Dialect().BoolLiteral(false)})
	}
	var vis string
	var err error
	if def.permission == "" {
		vis, err = def.visibleCond(ctx, f.Auth)
	} else {
		vis, err = f.Auth.AllowedToCondition(ctx, def.permission, authz.ConditionOptions{}, nil)
	}
	if err != nil {
		return "", nil, false, err
	}
	conds = append(conds, frag{sql: vis})
	if f.projects != nil {
		conds = append(conds, frag{sql: def.projectKey + " IN (" + idList(f.projects) + ")"})
	}
	return from, conds, true, nil
}

// tokensCondition は search_tokens_condition(columns, tokens, all_words)。
func (f *Fetcher) tokensCondition(columns []string) frag {
	d := f.Q.Dialect()
	var clauses []string
	var args []any
	for _, t := range f.tokens {
		var cols []string
		for _, c := range columns {
			cols = append(cols, "("+d.ILike(c)+")")
			args = append(args, "%"+db.EscapeLike(t)+"%")
		}
		clauses = append(clauses, "("+strings.Join(cols, " OR ")+")")
	}
	sep := " OR "
	if f.opts.AllWords {
		sep = " AND "
	}
	return frag{sql: strings.Join(clauses, sep), args: args}
}

// fetch は fetch_ranks_and_ids(scope, limit)（[日時(秒), id] を日時・id の降順で）。
func (f *Fetcher) fetch(ctx context.Context, def *searchable, from string, conds []frag) ([]rank, error) {
	var sqls []string
	var args []any
	for _, c := range conds {
		if strings.TrimSpace(c.sql) == "" {
			continue
		}
		sqls = append(sqls, "("+c.sql+")")
		args = append(args, c.args...)
	}
	q := "SELECT DISTINCT " + def.dateCol + " AS rank_ts, " + def.idCol + " AS rank_id FROM " + from
	if len(sqls) > 0 {
		q += " WHERE " + strings.Join(sqls, " AND ")
	}
	q += " ORDER BY " + def.dateCol + " DESC, " + def.idCol + " DESC"
	var rows []struct {
		TS db.Time `db:"rank_ts"`
		ID int64   `db:"rank_id"`
	}
	if err := f.Q.Select(ctx, &rows, q, args...); err != nil {
		return nil, err
	}
	out := make([]rank, len(rows))
	for i, r := range rows {
		out[i] = rank{r.TS.Unix(), r.ID}
	}
	return out, nil
}

// rankAndIDs は search_result_ranks_and_ids(tokens, user, projects, options)。
func (f *Fetcher) rankAndIDs(ctx context.Context, def *searchable) ([]rank, error) {
	from, base, ok, err := f.searchScope(ctx, def)
	if err != nil || !ok {
		return nil, err
	}
	columns := def.columns
	if f.opts.TitlesOnly {
		columns = columns[:1]
	}
	var r []rank
	queries := 0
	union := func(rs []rank) {
		seen := map[rank]bool{}
		for _, x := range r {
			seen[x] = true
		}
		for _, x := range rs {
			if !seen[x] {
				seen[x] = true
				r = append(r, x)
			}
		}
	}
	with := func(extra ...frag) []frag { return append(append([]frag(nil), base...), extra...) }
	if f.opts.Attachments != "only" {
		rs, err := f.fetch(ctx, def, from, with(f.tokensCondition(columns)))
		if err != nil {
			return nil, err
		}
		r = rs
		queries++
		if !f.opts.TitlesOnly && def.cfKind != "" {
			cfs, err := customfield.Load(ctx, f.Q, "custom_fields.owner_kind = ? AND custom_fields.searchable = ?", string(def.cfKind), true)
			if err != nil {
				return nil, err
			}
			if len(cfs) > 0 {
				groups := map[string][]int64{}
				var order []string
				for _, cf := range cfs {
					v, err := customfield.VisibilityByProjectCondition(ctx, f.Auth, cf, def.projectKey, "custom_values.custom_field_id")
					if err != nil {
						return nil, err
					}
					if _, ok := groups[v]; !ok {
						order = append(order, v)
					}
					groups[v] = append(groups[v], cf.ID)
				}
				var clauses []string
				for _, v := range order {
					clauses = append(clauses, "(custom_values.custom_field_id IN ("+idList(groups[v])+") AND ("+v+"))")
				}
				j := from + " INNER JOIN custom_values ON custom_values.customized_id = " + def.idCol +
					" AND custom_values.customized_kind = '" + def.customizedKind + "'"
				rs, err := f.fetch(ctx, def, j, with(frag{sql: strings.Join(clauses, " OR ")}, f.tokensCondition([]string{"custom_values.value"})))
				if err != nil {
					return nil, err
				}
				union(rs)
				queries++
			}
		}
		if !f.opts.TitlesOnly && def.journals {
			perm, err := f.Auth.AllowedToCondition(ctx, "view_private_notes", authz.ConditionOptions{}, nil)
			if err != nil {
				return nil, err
			}
			j := from + " INNER JOIN issue_journals ON issue_journals.issue_id = issues.id"
			rs, err := f.fetch(ctx, def, j, with(
				frag{sql: "issue_journals.private_notes = " + f.Q.Dialect().BoolLiteral(false) + " OR (" + perm + ")"},
				f.tokensCondition([]string{"issue_journals.notes"})))
			if err != nil {
				return nil, err
			}
			union(rs)
			queries++
		}
	}
	searchAttachments := f.opts.Attachments != "0"
	if f.opts.TitlesOnly {
		searchAttachments = f.opts.Attachments == "only"
	}
	if def.attachments && searchAttachments {
		j := from + " INNER JOIN attachments ON attachments.container_id = " + def.idCol +
			" AND attachments.container_kind = '" + def.customizedKind + "'"
		rs, err := f.fetch(ctx, def, j, with(f.tokensCondition([]string{"attachments.filename", "attachments.description"})))
		if err != nil {
			return nil, err
		}
		union(rs)
		queries++
	}
	if queries > 1 {
		sort.Slice(r, func(i, j int) bool {
			if r[i].ts != r[j].ts {
				return r[i].ts > r[j].ts
			}
			return r[i].id > r[j].id
		})
	}
	return r, nil
}
