// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

// IssuesController#issue_tab（チケット詳細の作業時間・関連リビジョンのタブを XHR で返す）。

import (
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// IssuesTab は IssuesController#issue_tab。
func (a *App) IssuesTab(c *Req) {
	if c.R.Header.Get("X-Requested-With") != "XMLHttpRequest" {
		c.RenderError(http.StatusUnprocessableEntity, "")
		return
	}
	l := a.newIssueLookup(c)
	m := l.model(c.currentIssue())
	switch c.Params().String("name") {
	case "time_entries":
		tes, err := repository.IssueTimeEntries(c.Ctx(), a.DB, m.Row.ID, l.timeEntryVisibleCondition())
		if err != nil {
			a.internalError(c, "time entries", err)
			return
		}
		var uids []int64
		for _, t := range tes {
			uids = append(uids, t.UserID)
		}
		l.preloadPrincipals(uids)
		var items []*timeEntryItem
		for _, t := range tes {
			items = append(items, &timeEntryItem{ReadTimeEntry: t, l: l, m: m, User: l.principal(t.UserID)})
		}
		c.Render("issues/tabs/_time_entries", map[string]any{"TimeEntries": items}, RenderOptions{Layout: view.NoLayout})
	case "changesets":
		vis, err := a.changesetVisibleCondition(c)
		if err != nil {
			a.internalError(c, "changesets", err)
			return
		}
		cs, err := repository.IssueChangesets(c.Ctx(), a.DB, m.Row.ID, vis)
		if err != nil {
			a.internalError(c, "changesets", err)
			return
		}
		if c.Pref().CommentsSorting == "desc" {
			slices.Reverse(cs)
		}
		var items []*changesetItem
		for _, x := range cs {
			items = append(items, &changesetItem{ReadChangeset: x, l: l, m: m})
		}
		c.Render("issues/tabs/_changesets", map[string]any{"Changesets": items}, RenderOptions{Layout: view.NoLayout})
	default:
		// Redmine はどのテンプレートも描画せず、既定のテンプレート（issue_tab）が無いため 204 相当の空応答になる
		c.W.WriteHeader(http.StatusNoContent)
		c.Halt()
	}
}

// timeEntryItem は tabs/_time_entries の 1 件。
type timeEntryItem struct {
	*repository.ReadTimeEntry
	l    *issueLookup
	m    *issueModel
	User *domain.User
}

// EditableBy は TimeEntry#editable_by?(User.current)。
func (t *timeEntryItem) EditableBy() bool {
	u := t.l.c.User
	p := t.l.project(t.ProjectID)
	return (u.Logged() && t.UserID == u.ID && t.l.c.AllowedTo(domain.Perm("edit_own_time_entries"), p)) ||
		t.l.c.AllowedTo(domain.Perm("edit_time_entries"), p)
}

// HoursShort は l_hours_short(time_entry.hours)。
func (t *timeEntryItem) HoursShort() string { return t.l.c.Loc.LHoursShort(t.Hours) }

// CommentsString は time_entry.comments。
func (t *timeEntryItem) CommentsString() string { return strPtrValue(t.Comments) }

// changesetItem は tabs/_changesets の 1 件。
type changesetItem struct {
	*repository.ReadChangeset
	l *issueLookup
	m *issueModel
}

// User は changeset.user。
func (c *changesetItem) User() *domain.User {
	if c.UserID == nil {
		return nil
	}
	return c.l.principal(*c.UserID)
}

// Author は changeset.author（user が無ければ committer の名前部分）。
func (c *changesetItem) Author() any {
	if u := c.User(); u != nil {
		return u
	}
	s := strPtrValue(c.Committer)
	if i := strings.Index(s, "<"); i >= 0 {
		s = s[:i]
	}
	return s
}

// Identifier は changeset.identifier（Git / Mercurial は scmid）。
func (c *changesetItem) Identifier() string {
	switch c.RepoSCM {
	case "git", "mercurial":
		if c.Scmid != nil {
			return *c.Scmid
		}
	}
	return c.Revision
}

// FormatIdentifier は changeset.format_identifier（Git は先頭 8 文字、Mercurial は "rev:scmid の先頭 12 文字"）。
func (c *changesetItem) FormatIdentifier() string {
	switch c.RepoSCM {
	case "git":
		id := c.Identifier()
		if len(id) > 8 {
			return id[:8]
		}
		return id
	case "mercurial":
		s := strPtrValue(c.Scmid)
		if len(s) > 12 {
			s = s[:12]
		}
		return c.Revision + ":" + s
	}
	return c.Identifier()
}

// ProjectPrefix は "#{changeset.project.name} - "（別プロジェクトのリビジョン）。
func (c *changesetItem) ProjectPrefix() string {
	if c.ProjectID == c.m.Project.ID {
		return ""
	}
	if p := c.l.project(c.ProjectID); p != nil {
		return p.Name + " - "
	}
	return ""
}

func (c *changesetItem) repoPath() string {
	p := c.l.project(c.ProjectID)
	path := "/projects/" + p.Identifier + "/repository"
	if !c.RepoIsDefault && c.RepoIdentifier.Valid && c.RepoIdentifier.String != "" {
		path += "/" + url.PathEscape(c.RepoIdentifier.String)
	}
	return path
}

// RevisionLink は link_to_revision(changeset, repository, :text => "Revision ...")。
func (c *changesetItem) RevisionLink() template.HTML {
	text := c.l.L("label_revision") + " " + c.FormatIdentifier()
	return rails.LinkTo(rails.H(text), c.repoPath()+"/revisions/"+url.PathEscape(c.Identifier()),
		rails.NewHash("title", c.l.L("label_revision_id", c.FormatIdentifier())))
}

// DiffLink は filechanges があり browse_repository できるときの (diff) リンク。
func (c *changesetItem) DiffLink() template.HTML {
	n, err := repository.ChangesetFileCount(c.l.ctx, c.l.a.DB, c.ID)
	c.l.fail(err)
	if n == 0 || !c.l.c.AllowedTo(domain.Perm("browse_repository"), c.l.project(c.ProjectID)) {
		return ""
	}
	return "(" + rails.LinkTo(c.l.L("label_diff"), c.repoPath()+"/revisions/"+url.PathEscape(c.Identifier())+"/diff", nil) + ")"
}

// Comments は format_changeset_comments changeset。
func (c *changesetItem) Comments() template.HTML {
	text := strPtrValue(c.ReadChangeset.Comments)
	o := redmine.Options{Object: &redmine.Object{Kind: "changeset", ID: c.ID, Project: c.l.project(c.ProjectID)},
		Project: c.l.project(c.ProjectID), NoFormatting: !c.l.a.Settings.Bool("commit_logs_formatting")}
	return c.l.renderer().Textilizable(text, o)
}

// CommittedOn は changeset.committed_on。
func (c *changesetItem) CommittedOn() any { return c.CommittedAt.Time }

func itoaID(id int64) string { return strconv.FormatInt(id, 10) }
