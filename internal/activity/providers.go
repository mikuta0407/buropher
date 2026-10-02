package activity

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
)

// providerOptions は acts_as_activity_provider のオプション（:timestamp, :author_key, :permission）と
// テーブル名（reorder("#{table_name}.id DESC") に使う）。
type providerOptions struct {
	table     string
	timestamp string
	authorKey string // 空なら :author_key なし（作成者指定時は 0 件）
}

var providerOpts = map[Provider]providerOptions{
	ProviderIssue:              {"issues", "issues.created_at", "issues.author_id"},
	ProviderJournal:            {"issue_journals", "issue_journals.created_at", "issue_journals.user_id"},
	ProviderChangeset:          {"changesets", "changesets.committed_at", "changesets.user_id"},
	ProviderNews:               {"news", "news.created_at", "news.author_id"},
	ProviderDocument:           {"documents", "documents.created_at", ""},
	ProviderAttachment:         {"attachments", "attachments.created_at", "attachments.author_id"},
	ProviderWikiContentVersion: {"wiki_page_versions", "wiki_page_versions.updated_at", "wiki_page_versions.author_id"},
	ProviderMessage:            {"messages", "messages.created_at", "messages.author_id"},
	ProviderTimeEntry:          {"time_entries", "time_entries.created_at", "time_entries.user_id"},
}

// findEvents は ActivityProvider.find_events(event_type, user, from, to, options)。
func (f *Fetcher) findEvents(ctx context.Context, eventType string, pv Provider, from, to *time.Time, limit int) ([]*Event, error) {
	po := providerOpts[pv]
	var conds []string
	var args []any
	if from != nil {
		conds = append(conds, po.timestamp+" >= ?")
		args = append(args, db.NewTime(*from))
	}
	if to != nil {
		// Rails は日付と日時の文字列を比較する（'2026-01-16 00:00:00' > '2026-01-16'）ので to の 0 時ちょうどは含まない
		conds = append(conds, po.timestamp+" < ?")
		args = append(args, db.NewTime(*to))
	}
	if f.opts.Author != nil {
		if po.authorKey == "" {
			return nil, nil
		}
		conds = append(conds, po.authorKey+" = ?")
		args = append(args, f.opts.Author.ID)
	}
	copts := authz.ConditionOptions{Project: f.opts.Project, WithSubprojects: f.opts.WithSubprojects}
	vis, err := f.visibility(ctx, eventType, pv, copts)
	if err != nil {
		return nil, err
	}
	conds = append(conds, vis...)
	order := "ORDER BY " + po.timestamp + ", " + po.table + ".id"
	if limit > 0 {
		order = "ORDER BY " + po.table + ".id DESC " + f.Q.Dialect().LimitOffset(limit, 0)
	}
	cond := "(" + strings.Join(conds, ") AND (") + ")"
	l := &Loader{Q: f.Q, Auth: f.Auth, Loc: f.Loc}
	switch pv {
	case ProviderIssue:
		return l.Issues(ctx, cond, args, order)
	case ProviderJournal:
		cond = "(issue_journal_details.prop_key = 'status_id' OR issue_journals.notes <> '') AND " + cond
		return l.Journals(ctx, cond, args, order)
	case ProviderChangeset:
		return l.Changesets(ctx, cond, args, order)
	case ProviderNews:
		return l.News(ctx, cond, args, order)
	case ProviderDocument:
		return l.Documents(ctx, cond, args, order)
	case ProviderAttachment:
		if eventType == "files" {
			return l.Attachments(ctx, `attachments LEFT JOIN versions
    ON attachments.container_kind = 'version' AND versions.id = attachments.container_id
  LEFT JOIN projects ON versions.project_id = projects.id
    OR (attachments.container_kind = 'project' AND attachments.container_id = projects.id)`,
				"attachments.container_kind IN ('version', 'project') AND "+cond, args, order)
		}
		return l.Attachments(ctx, `attachments LEFT JOIN documents
    ON attachments.container_kind = 'document' AND documents.id = attachments.container_id
  LEFT JOIN projects ON documents.project_id = projects.id`, cond, args, order)
	case ProviderWikiContentVersion:
		return l.WikiContentVersions(ctx, cond, args, order)
	case ProviderMessage:
		return l.Messages(ctx, cond, args, order)
	case ProviderTimeEntry:
		return l.TimeEntries(ctx, cond, args, order)
	}
	return nil, fmt.Errorf("activity: unknown provider %s", pv)
}

// visibility は find_events の可視性条件（:permission があれば allowed_to_condition、無ければ visible スコープ）。
func (f *Fetcher) visibility(ctx context.Context, eventType string, pv Provider, copts authz.ConditionOptions) ([]string, error) {
	switch pv {
	case ProviderIssue:
		c, err := f.Auth.IssueVisibleCondition(ctx, copts)
		return []string{c}, err
	case ProviderJournal:
		c, err := f.Auth.IssueVisibleCondition(ctx, copts)
		if err != nil {
			return nil, err
		}
		n, err := VisibleNotesCondition(ctx, f.Auth)
		return []string{c, n}, err
	case ProviderTimeEntry:
		c, err := TimeEntryVisibleCondition(ctx, f.Auth, copts)
		return []string{c}, err
	case ProviderAttachment:
		c, err := f.Auth.AllowedToCondition(ctx, "view_"+eventType, copts, nil)
		return []string{c}, err
	case ProviderWikiContentVersion:
		c, err := f.Auth.AllowedToCondition(ctx, "view_wiki_edits", copts, nil)
		return []string{c}, err
	case ProviderChangeset:
		c, err := f.Auth.AllowedToCondition(ctx, "view_changesets", copts, nil)
		return []string{c}, err
	case ProviderNews:
		c, err := f.Auth.AllowedToCondition(ctx, "view_news", copts, nil)
		return []string{c}, err
	case ProviderDocument:
		c, err := f.Auth.AllowedToCondition(ctx, "view_documents", copts, nil)
		return []string{c}, err
	case ProviderMessage:
		c, err := f.Auth.AllowedToCondition(ctx, "view_messages", copts, nil)
		return []string{c}, err
	}
	return nil, fmt.Errorf("activity: unknown provider %s", pv)
}

// VisibleNotesCondition は Journal.visible_notes_condition(user, :skip_pre_condition => true)。
func VisibleNotesCondition(ctx context.Context, a *authz.Authorizer) (string, error) {
	perm, err := a.AllowedToCondition(ctx, "view_private_notes", authz.ConditionOptions{SkipPreCondition: true}, nil)
	if err != nil {
		return "", err
	}
	return "(issue_journals.private_notes = " + a.Dialect().BoolLiteral(false) +
		" OR issue_journals.user_id = " + itoa(a.User().ID) + " OR (" + perm + "))", nil
}

// TimeEntryVisibleCondition は TimeEntry.visible_condition(user, options)。
func TimeEntryVisibleCondition(ctx context.Context, a *authz.Authorizer, opts authz.ConditionOptions) (string, error) {
	return a.AllowedToCondition(ctx, "view_time_entries", opts, func(role *domain.Role, user *domain.User) string {
		switch {
		case role.TimeEntriesVisibility == domain.TimeEntriesVisibilityAll:
			return ""
		case role.TimeEntriesVisibility == domain.TimeEntriesVisibilityOwn && user.ID != 0 && user.Logged():
			return "time_entries.user_id = " + itoa(user.ID)
		}
		return "1=0"
	})
}
