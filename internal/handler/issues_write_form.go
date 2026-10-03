// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

// 作成・編集フォーム（issues/new・_edit・_watchers_form・_conflict）の表示用データのうち、
// 書き込み系でだけ使うもの。

import (
	"html/template"
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/attachments"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// noLayout は render :layout => false。
const noLayout = view.NoLayout

// issueRowByID は id のチケットを読み直した表示用の行。
func issueRowByID(c *Req, a *App, id int64) (*query.IssueRow, error) {
	r, err := repository.ReadIssue(c.Ctx(), a.DB, id)
	if err != nil {
		return nil, err
	}
	return issueRowFromRead(r), nil
}

// savedAttachment は attachments/_form の saved_attachments の 1 件。
type savedAttachment struct {
	ID          int64
	Filename    string
	Description string
	ContainerID *int64
	Token       string
}

// SavedAttachments は container.saved_attachments（保存に失敗したときに再表示するアップロード済みの添付）。
func (f *issueEditForm) SavedAttachments() []savedAttachment {
	if f.saved == nil {
		return nil
	}
	var out []savedAttachment
	add := func(a *domain.Attachment) {
		out = append(out, savedAttachment{ID: a.ID, Filename: a.Filename, Description: a.Description,
			ContainerID: a.ContainerID, Token: a.Token()})
	}
	for _, a := range f.saved.Files {
		add(a)
	}
	return out
}

// ExistingAttachmentsStyle は existing-attachments の style（deleted_attachment_ids が空なら非表示）。
func (f *issueEditForm) ExistingAttachmentsStyle() string {
	if f.m.I != nil && len(f.m.I.DeletedAttachmentIDs()) > 0 {
		return ""
	}
	return "display:none;"
}

// AttachmentDeleted は @issue.deleted_attachment_ids.include?(id)。
func (f *issueEditForm) AttachmentDeleted(id int64) bool {
	return f.m.I != nil && slices.Contains(f.m.I.DeletedAttachmentIDs(), id)
}

// CancelOnclick は取消リンクの onclick（show の埋め込みフォームでだけフォームを閉じる）。
func (f *issueEditForm) CancelOnclick() string {
	if f.l.c.Action == "show" {
		return "$('#update').hide(); return false;"
	}
	return ""
}

// HasNotes は @issue.notes.present?。
func (f *issueEditForm) HasNotes() bool {
	if f.m.I == nil {
		return false
	}
	n := f.m.I.Notes()
	return n != nil && rails.IsPresent(*n)
}

// ConflictDetailStrings は details_to_strings(journal.details)。
func (f *issueEditForm) ConflictDetailStrings(j *journalView) []template.HTML {
	return f.l.detailsToStrings(j.Details, f.m, false, true)
}

// ConflictNotes は textilizable(journal, :notes) unless journal.notes.blank?。
func (f *issueEditForm) ConflictNotes(j *journalView) template.HTML {
	if !rails.IsPresent(j.Notes) {
		return ""
	}
	return f.l.renderer().Textilizable(j.Notes, redmine.Options{Object: &redmine.Object{Kind: "journal", ID: j.ID,
		Project: f.m.Project, JournalizedID: j.IssueID}})
}

// ConflictCancelLabel は l(:text_issue_conflict_resolution_cancel, :link => link_to_issue(@issue, :subject => false))。
func (f *issueEditForm) ConflictCancelLabel() template.HTML {
	link := f.l.linkToIssue(f.m.Row, redmine.LinkToIssueOptions{NoSubject: true})
	return template.HTML(f.l.L("text_issue_conflict_resolution_cancel", map[string]any{"link": string(link)}))
}

// ---------------------------------------------------------------- _watchers_form

// ShowWatchersForm は @issue.safe_attribute? 'watcher_user_ids'。
func (f *issueEditForm) ShowWatchersForm() bool { return f.m.SafeAttribute("watcher_user_ids") }

// SearchWatchersURL は {:controller => 'watchers', :action => 'new', :project_id => @issue.project}。
func (f *issueEditForm) SearchWatchersURL() string {
	return "/watchers/new?project_id=" + f.m.Project.Identifier
}

// WatchersCheckboxes は watchers_checkboxes(@issue, users_for_new_issue_watchers(@issue))。
func (f *issueEditForm) WatchersCheckboxes() template.HTML {
	l := f.l
	var checked []int64
	if f.m.I != nil {
		ids, err := f.m.env().WatcherIDs(l.ctx, f.m.I)
		l.fail(err)
		checked = ids
	}
	return watchersCheckboxes(l, f.usersForNewIssueWatchers(checked), func(id int64) bool { return slices.Contains(checked, id) })
}

// usersForNewIssueWatchers は users_for_new_issue_watchers(issue)。
func (f *issueEditForm) usersForNewIssueWatchers(watcherIDs []int64) []*domain.User {
	l := f.l
	l.preloadPrincipals(watcherIDs)
	var users []*domain.User
	for _, id := range watcherIDs {
		// issue.watcher_users.select {|u| u.status == User::STATUS_ACTIVE}
		if u := l.principal(id); u != nil && !u.Kind.IsGroup() && u.Status == domain.StatusActive {
			users = append(users, u)
		}
	}
	cond, err := l.c.Authz().PrincipalVisibleCondition(l.ctx)
	l.fail(err)
	ids, err := repository.ProjectAssignableWatcherIDs(l.ctx, l.a.DB, f.m.Project.ID, cond, 21)
	l.fail(err)
	if len(ids) <= 20 {
		l.preloadPrincipals(ids)
		var aw []*domain.User
		for _, id := range ids {
			if u := l.principal(id); u != nil {
				aw = append(aw, u)
			}
		}
		l.sortPrincipals(aw)
		users = append(users, aw...)
	}
	// users.uniq
	var out []*domain.User
	seen := map[int64]bool{}
	for _, u := range users {
		if !seen[u.ID] {
			seen[u.ID] = true
			out = append(out, u)
		}
	}
	return out
}

// watchersCheckboxes は WatchersHelper#watchers_checkboxes(object, users, checked)。
func watchersCheckboxes(l *issueLookup, users []*domain.User, checked func(id int64) bool) template.HTML {
	var b strings.Builder
	for _, u := range users {
		tag := rails.CheckBoxTag("issue[watcher_user_ids][]", u.ID, checked(u.ID), rails.NewHash("id", nil))
		b.WriteString(string(rails.ContentTag("label", template.HTML(string(tag)+" "+string(rails.H(l.principalName(u)))),
			rails.NewHash("id", "issue_watcher_user_ids_"+strconv.FormatInt(u.ID, 10), "class", "floating"))))
	}
	return template.HTML(b.String())
}

var _ = attachments.WarningNotSaved
