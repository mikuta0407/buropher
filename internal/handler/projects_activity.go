package handler

import (
	"time"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/repository"
)

// lastActivityByProject は Project.load_last_activity_date（Redmine::Activity::Fetcher の
// last_by_project）: 全イベント種別（issues, changesets, news, documents, files, wiki_edits,
// messages, time_entries）の可視なイベントのプロジェクトごとの最新日時。
func (a *App) lastActivityByProject(c *Req) (map[int64]time.Time, error) {
	ctx := c.Ctx()
	az := c.Authz()
	perm := func(p string) (string, error) {
		return az.AllowedToCondition(ctx, p, authz.ConditionOptions{}, nil)
	}
	var conds repository.LastActivityConds
	var err error
	if conds.Issues, err = az.IssueVisibleCondition(ctx, authz.ConditionOptions{}); err != nil {
		return nil, err
	}
	// Journal.visible_notes_condition(user, :skip_pre_condition => true)
	pn, err := az.AllowedToCondition(ctx, "view_private_notes", authz.ConditionOptions{SkipPreCondition: true}, nil)
	if err != nil {
		return nil, err
	}
	conds.JournalNotes = "issue_journals.private_notes = " + a.DB.Dialect().BoolLiteral(false) +
		" OR issue_journals.user_id = " + itoa(c.User.ID) + " OR (" + pn + ")"
	for _, x := range []struct {
		dst  *string
		perm string
	}{
		{&conds.Changesets, "view_changesets"},
		{&conds.News, "view_news"},
		{&conds.Documents, "view_documents"},
		{&conds.Files, "view_files"},
		{&conds.WikiEdits, "view_wiki_edits"},
		{&conds.Messages, "view_messages"},
	} {
		if *x.dst, err = perm(x.perm); err != nil {
			return nil, err
		}
	}
	if conds.TimeEntries, err = timeEntryVisibleCondition(c); err != nil {
		return nil, err
	}
	return repository.LastActivityByProject(ctx, a.DB, conds)
}
