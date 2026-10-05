// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

// チケットの履歴（Journal / JournalDetail）と IssuesHelper#show_detail / details_to_strings、
// JournalsHelper（render_journal_actions / render_notes ...）、ReactionsHelper#reaction_button の移植。

import (
	"html/template"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/urlroot"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// journalView は 1 件のジャーナル（@journals の要素）。
type journalView struct {
	*issues.Journal
	l     *issueLookup
	issue *issueModel
	User  *domain.User

	visible     []*domain.JournalDetail
	visibleDone bool
}

// visibleJournals は Issue#visible_journals_with_index(User.current)。
func (m *issueModel) visibleJournals() []*journalView {
	if m.I == nil {
		return nil
	}
	js, err := m.env().VisibleJournalsWithIndex(m.l.ctx, m.I, m.user())
	m.l.fail(err)
	var uids []int64
	for _, j := range js {
		uids = append(uids, j.UserID)
		if j.UpdatedByID != nil {
			uids = append(uids, *j.UpdatedByID)
		}
	}
	m.l.preloadPrincipals(uids)
	ids := make([]int64, len(js))
	for i, j := range js {
		ids[i] = j.ID
	}
	m.l.preloadReactions("journal", ids)
	out := make([]*journalView, 0, len(js))
	for _, j := range js {
		out = append(out, &journalView{Journal: j, l: m.l, issue: m, User: m.l.principal(j.UserID)})
	}
	return out
}

// NotesString は notes。
func (j *journalView) NotesString() string { return j.Journal.Notes }

// HasNotes は notes.present?。
func (j *journalView) HasNotes() bool { return rails.IsPresent(j.NotesString()) }

// CSSClasses は Journal#css_classes。
func (j *journalView) CSSClasses() string {
	s := "journal"
	if j.HasNotes() {
		s += " has-notes"
	}
	if len(j.Details) > 0 {
		s += " has-details"
	}
	if j.PrivateNotes {
		s += " private-notes"
	}
	return s
}

// VisibleDetails は Journal#visible_details(User.current)。
func (j *journalView) VisibleDetails() []*domain.JournalDetail {
	if !j.visibleDone {
		j.visibleDone = true
		ds, err := j.l.issuesEnv().VisibleDetails(j.l.ctx, j.Journal, j.issue.I, j.l.c.User)
		j.l.fail(err)
		j.visible = ds
	}
	return j.visible
}

// EditableBy は Journal#editable_by?(User.current)。
func (j *journalView) EditableBy() bool {
	ok, err := j.l.issuesEnv().JournalEditableBy(j.l.ctx, j.Journal, j.l.c.User)
	j.l.fail(err)
	return ok
}

// Attachments は Journal#attachments（追加された添付）。
func (j *journalView) Attachments() []*repository.ReadAttachment {
	var ids []int64
	for _, d := range j.Details {
		if d.Property == "attachment" && d.Value != nil && *d.Value != "" {
			ids = append(ids, customfield.RubyToI(d.PropKey))
		}
	}
	if len(ids) == 0 {
		return nil
	}
	m, err := repository.ReadAttachmentsByIDs(j.l.ctx, j.l.a.DB, ids)
	j.l.fail(err)
	var out []*repository.ReadAttachment
	for _, id := range ids {
		if a := m[id]; a != nil {
			out = append(out, a)
		}
	}
	return out
}

// UpdatedOn は journal.updated_on（nil なら created_on）。
func (j *journalView) UpdatedOn() time.Time {
	if j.UpdatedAt != nil {
		return *j.UpdatedAt
	}
	return j.CreatedAt
}

// PrivateNotesIndicator は render_private_notes_indicator(journal)。
func (j *journalView) PrivateNotesIndicator() template.HTML {
	content, css := "", ""
	if j.PrivateNotes {
		content, css = j.l.L("field_is_private"), "badge badge-private private"
	}
	return rails.ContentTag("span", template.HTML(content), rails.NewHash("id", "journal-"+strconv.FormatInt(j.ID, 10)+"-private_notes", "class", css))
}

// UpdateInfo は render_journal_update_info(journal)。
func (j *journalView) UpdateInfo() template.HTML {
	if j.UpdatedAt == nil || j.UpdatedAt.Equal(j.CreatedAt) {
		return ""
	}
	var by string
	if j.UpdatedByID != nil {
		by = j.l.principalName(j.l.principal(*j.UpdatedByID))
	}
	title := j.l.L("label_time_by_author", i18n.Vars{"time": j.l.formatTime(*j.UpdatedAt), "author": by})
	return rails.ContentTag("span", "· "+j.l.L("label_edited"), rails.NewHash("title", title, "class", "update-info"))
}

// NotesHTML は render_notes(issue, journal)。
func (j *journalView) NotesHTML() template.HTML {
	text := j.l.renderer().Textilizable(j.NotesString(), redmine.Options{Object: &redmine.Object{Kind: "journal", ID: j.ID,
		Project: j.issue.Project, JournalizedID: j.IssueID}})
	return rails.ContentTag("div", text, rails.NewHash("id", "journal-"+strconv.FormatInt(j.ID, 10)+"-notes",
		"class", "wiki journal-note", "data", rails.NewHash("quote_reply_target", "content")))
}

// Actions は render_journal_actions(issue, journal, :reply_links => reply)。
func (j *journalView) Actions(reply bool) template.HTML {
	l := j.l
	var links, drop []string
	u := issueURL(l.c, j.IssueID) + "#note-" + strconv.Itoa(j.Indice)
	drop = append(drop, string(l.copyObjectURLLink(u)))
	if atts := j.Attachments(); len(atts) > 1 {
		drop = append(drop, string(rails.LinkTo(l.icon("download", l.L("label_download_all_attachments")),
			"/attachments/journals/"+strconv.FormatInt(j.ID, 10)+"/download",
			rails.NewHash("title", l.L("label_download_all_attachments"), "class", "icon icon-download"))))
	}
	links = append(links, string(l.reactionButton("journal", j.ID, j.issue)))
	if j.HasNotes() {
		if reply {
			qu := "/issues/" + strconv.FormatInt(j.IssueID, 10) + "/quoted?journal_id=" + strconv.FormatInt(j.ID, 10) +
				"&journal_indice=" + strconv.Itoa(j.Indice)
			links = append(links, string(l.quoteReplyButton(qu, true)))
		}
		if j.EditableBy() {
			links = append(links, string(rails.LinkTo(l.icon("edit", l.L("button_edit")), "/journals/"+strconv.FormatInt(j.ID, 10)+"/edit",
				rails.NewHash("remote", true, "method", "get", "title", l.L("button_edit"), "class", "icon-only icon-edit"))))
			drop = append(drop, string(rails.LinkTo(l.icon("del", l.L("button_delete")),
				"/journals/"+strconv.FormatInt(j.ID, 10)+"?"+url.QueryEscape("journal[notes]")+"=",
				rails.NewHash("remote", true, "method", "put", "data", rails.NewHash("confirm", l.L("text_are_you_sure")), "class", "icon icon-del"))))
		}
	}
	return template.HTML(strings.Join(links, " ")) + l.actionsDropdown(template.HTML(strings.Join(drop, " ")))
}

// DetailStrings は details_to_strings(journal.visible_details)。
func (j *journalView) DetailStrings() []template.HTML {
	return j.l.detailsToStrings(j.VisibleDetails(), j.issue, false, true)
}

// ---------------------------------------------------------------- show_detail

// multiDetail は MultipleValuesDetail（複数値 CF の追加・削除をまとめたもの）。
type detailItem struct {
	d        *domain.JournalDetail
	values   []string
	olds     []string
	multiple bool
}

// detailsToStrings は details_to_strings(details, no_html, only_path)。
func (l *issueLookup) detailsToStrings(details []*domain.JournalDetail, issue *issueModel, noHTML, onlyPath bool) []template.HTML {
	var out []template.HTML
	type changes struct {
		added, deleted []string
	}
	var order []int64
	byField := map[int64]*changes{}
	for _, d := range details {
		if d.Property == "cf" {
			if cf := l.customField(customfield.RubyToI(d.PropKey)); cf != nil && cf.Multiple {
				ch := byField[cf.ID]
				if ch == nil {
					ch = &changes{}
					byField[cf.ID] = ch
					order = append(order, cf.ID)
				}
				if d.OldValue != nil {
					ch.deleted = append(ch.deleted, *d.OldValue)
				}
				if d.Value != nil {
					ch.added = append(ch.added, *d.Value)
				}
				continue
			}
		}
		out = append(out, l.showDetail(detailItem{d: d}, issue, noHTML, onlyPath))
	}
	for _, id := range order {
		ch := byField[id]
		key := strconv.FormatInt(id, 10)
		if len(ch.added) > 0 {
			out = append(out, l.showDetail(detailItem{d: &domain.JournalDetail{Property: "cf", PropKey: key}, values: ch.added, multiple: true}, issue, noHTML, onlyPath))
		}
		if len(ch.deleted) > 0 {
			out = append(out, l.showDetail(detailItem{d: &domain.JournalDetail{Property: "cf", PropKey: key}, olds: ch.deleted, multiple: true}, issue, noHTML, onlyPath))
		}
	}
	return out
}

func strPtrValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// showDetail は show_detail(detail, no_html, options)。
func (l *issueLookup) showDetail(it detailItem, issue *issueModel, noHTML, onlyPath bool) template.HTML {
	d := it.d
	multiple, showDiff, noDetails := false, false, false
	var label string
	var value, oldValue any // string（未エスケープ）または template.HTML
	hasValue := d.Value != nil || it.values != nil
	hasOld := d.OldValue != nil || it.olds != nil
	rawValue := strPtrValue(d.Value)
	rawOld := strPtrValue(d.OldValue)
	if it.values != nil {
		rawValue = strings.Join(it.values, ", ")
	}
	if it.olds != nil {
		rawOld = strings.Join(it.olds, ", ")
	}
	valuePresent := rails.IsPresent(rawValue)
	switch d.Property {
	case "attr":
		field := strings.TrimSuffix(d.PropKey, "_id")
		label = l.L("field_" + field)
		switch d.PropKey {
		case "due_date", "start_date":
			if d.Value != nil {
				value = l.formatDateString(*d.Value)
			}
			if d.OldValue != nil {
				oldValue = l.formatDateString(*d.OldValue)
			}
		case "project_id", "status_id", "tracker_id", "assigned_to_id", "priority_id", "category_id", "fixed_version_id":
			if v := l.findNameByReflection(field, rawValue); v != "" {
				value = v
			}
			if v := l.findNameByReflection(field, rawOld); v != "" {
				oldValue = v
			}
		case "estimated_hours":
			if rails.IsPresent(rawValue) {
				value = l.c.Loc.LHoursShort(customfield.RubyToF(rawValue))
			}
			if rails.IsPresent(rawOld) {
				oldValue = l.c.Loc.LHoursShort(customfield.RubyToF(rawOld))
			}
		case "parent_id":
			label = l.L("field_parent_issue")
			if rails.IsPresent(rawValue) {
				value = "#" + rawValue
			}
			if rails.IsPresent(rawOld) {
				oldValue = "#" + rawOld
			}
		case "child_id":
			label = l.L("label_subtask")
			if rails.IsPresent(rawValue) {
				value = "#" + rawValue
			}
			if rails.IsPresent(rawOld) {
				oldValue = "#" + rawOld
			}
			multiple = true
		case "is_private":
			yn := func(s string) string {
				if s == "0" {
					return l.L("general_text_No")
				}
				return l.L("general_text_Yes")
			}
			if rails.IsPresent(rawValue) {
				value = yn(rawValue)
			}
			if rails.IsPresent(rawOld) {
				oldValue = yn(rawOld)
			}
		case "description":
			showDiff = true
		}
	case "cf":
		if cf := l.customField(customfield.RubyToI(d.PropKey)); cf != nil {
			label = cf.Name
			f := cf.Format()
			switch {
			case f.ChangeNoDetails:
				noDetails = true
			case f.ChangeAsDiff:
				showDiff = true
			default:
				multiple = cf.Multiple
				if hasValue {
					value = l.formatValue(cf, it.values, d.Value)
				}
				if hasOld {
					oldValue = l.formatValue(cf, it.olds, d.OldValue)
				}
			}
		}
	case "attachment":
		label = l.L("label_attachment")
	case "relation":
		if d.Value != nil && d.OldValue == nil {
			value = l.relationDetailValue(*d.Value, noHTML, onlyPath)
		} else if d.OldValue != nil && d.Value == nil {
			oldValue = l.relationDetailValue(*d.OldValue, noHTML, onlyPath)
		}
		if t, ok := relationTypes[d.PropKey]; ok {
			label = l.L(t.name)
		}
	}
	if label == "" {
		label = d.PropKey
	}
	if value == nil && hasValue {
		value = rawValue
	}
	if oldValue == nil && hasOld {
		oldValue = rawOld
	}
	var labelS, valueS, oldS string
	if noHTML {
		labelS = label
		valueS = rails.ToS(value)
		oldS = rails.ToS(oldValue)
	} else {
		labelS = string(rails.ContentTag("strong", label, nil))
		if hasOld {
			oldS = string(rails.ContentTag("i", rails.H(oldValue), nil))
			if !valuePresent && d.Property != "relation" {
				oldS = string(rails.ContentTag("del", template.HTML(oldS), nil))
			}
		}
		var att *repository.ReadAttachment
		if d.Property == "attachment" && rails.IsPresent(value) && issue != nil {
			for _, a := range l.issueAttachments(issue.Row.ID) {
				if a.ID == customfield.RubyToI(d.PropKey) {
					att = a
				}
			}
		}
		if att != nil {
			id := strconv.FormatInt(att.ID, 10)
			v := string(rails.LinkTo(att.Filename, l.urlFor(onlyPath, "/attachments/"+id), nil))
			if onlyPath {
				v += " "
				v += string(rails.LinkTo(l.icon("download", att.Filename), "/attachments/download/"+id+"/"+url.PathEscape(att.Filename),
					rails.NewHash("class", "icon-only icon-download", "title", l.L("button_download"))))
			}
			valueS = v
		} else if value != nil {
			valueS = string(rails.ContentTag("i", rails.H(value), nil))
		}
	}
	switch {
	case noDetails:
		return template.HTML(l.L("text_journal_changed_no_detail", i18n.Vars{"label": labelS}))
	case showDiff:
		s := l.L("text_journal_changed_no_detail", i18n.Vars{"label": labelS})
		if !noHTML {
			u := l.urlFor(onlyPath, "/journals/"+strconv.FormatInt(d.JournalID, 10)+"/diff?detail_id="+strconv.FormatInt(d.ID, 10))
			s += " (" + string(rails.LinkTo(l.L("label_diff"), u, rails.NewHash("title", l.L("label_view_diff")))) + ")"
		}
		return template.HTML(s)
	case valuePresent:
		switch d.Property {
		case "attr", "cf":
			switch {
			case rails.IsPresent(rawOld):
				return template.HTML(l.L("text_journal_changed", i18n.Vars{"label": labelS, "old": oldS, "new": valueS}))
			case multiple:
				return template.HTML(l.L("text_journal_added", i18n.Vars{"label": labelS, "value": valueS}))
			default:
				return template.HTML(l.L("text_journal_set_to", i18n.Vars{"label": labelS, "value": valueS}))
			}
		case "attachment", "relation":
			return template.HTML(l.L("text_journal_added", i18n.Vars{"label": labelS, "value": valueS}))
		}
		return ""
	}
	return template.HTML(l.L("text_journal_deleted", i18n.Vars{"label": labelS, "old": oldS}))
}

// urlFor は only_path に応じて相対パスか完全 URL を返す。
func (l *issueLookup) urlFor(onlyPath bool, path string) string {
	if onlyPath {
		return path
	}
	return l.renderer().BaseURL + path
}

// formatDateString は format_date(value.to_date)。
func (l *issueLookup) formatDateString(s string) string {
	if len(s) >= 10 {
		if t, err := time.Parse("2006-01-02", s[:10]); err == nil {
			return l.formatDate(t)
		}
	}
	return ""
}

// formatValue は format_value(value, custom_field)（formatted_value(html: false) を format_object(html: false)）。
func (l *issueLookup) formatValue(cf *customfield.CustomField, multi []string, single *string) string {
	var v any
	if multi != nil {
		v = multi
	} else if single != nil {
		v = *single
	}
	return rails.ToS(l.formatCustomValue(cf, v, l.dummyCustomized(), false))
}

func (l *issueLookup) dummyCustomized() *customfield.Customized {
	return &customfield.Customized{Kind: "issue"}
}

// findNameByReflection は find_name_by_reflection(field, id)。
func (l *issueLookup) findNameByReflection(field, id string) string {
	if strings.TrimSpace(id) == "" {
		return ""
	}
	n := customfield.RubyToI(id)
	switch field {
	case "project":
		p := l.project(n)
		if p != nil && l.projectVisible(p) {
			return p.Name
		}
	case "status":
		l.loadMasters()
		if s := l.statuses[n]; s != nil {
			return s.Name
		}
	case "tracker":
		l.loadMasters()
		if t := l.trackers[n]; t != nil {
			return t.Name
		}
	case "priority":
		l.loadMasters()
		if p := l.priorities[n]; p != nil {
			return p.Name
		}
	case "assigned_to":
		if u := l.principal(n); u != nil {
			// Principal#name（グループは name、ユーザーは User#name）
			return l.principalName(u)
		}
	case "category":
		if c := l.category(&n); c != nil {
			return c.Name
		}
	case "fixed_version":
		if v := l.version(&n); v != nil {
			return v.Name
		}
	}
	return ""
}

// projectVisible は project.visible?。
func (l *issueLookup) projectVisible(p *domain.Project) bool {
	ok, err := l.c.Authz().ProjectVisible(l.ctx, p)
	l.fail(err)
	return ok
}

// relationDetailValue は関連の詳細の値（見えなければ "Issue #n"）。
func (l *issueLookup) relationDetailValue(id string, noHTML, onlyPath bool) any {
	r := l.issue(customfield.RubyToI(id))
	if r == nil || !l.issueVisible(r) {
		return l.L("label_issue") + " #" + id
	}
	if noHTML {
		// rel_issue.to_s（"Tracker #id: subject"）
		return l.tracker(r.TrackerID).Name + " #" + strconv.FormatInt(r.ID, 10) + ": " + r.Subject
	}
	return l.linkToIssue(r, redmine.LinkToIssueOptions{FullURL: !onlyPath})
}

// ---------------------------------------------------------------- reactions / quote / copy link

// copyObjectURLLink は copy_object_url_link(url)（Redmine 7.0 で clipboard Stimulus コントローラーに移行）。
func (l *issueLookup) copyObjectURLLink(u string) template.HTML {
	return rails.LinkTo(l.icon("copy-link", l.L("button_copy_link")), "#",
		rails.NewHash("class", "icon icon-copy-link",
			"data", rails.NewHash("clipboard_text", u, "controller", "clipboard", "action", "clipboard#copyText")))
}

// quoteReplyButton は quote_reply_button(url:, icon_only:)。
func (l *issueLookup) quoteReplyButton(u string, iconOnly bool) template.HTML {
	css := "icon icon-quote"
	if iconOnly {
		css = "icon-only icon-quote"
	}
	h := rails.NewHash("data", rails.NewHash("action", "quote-reply#quote", "quote_reply_url_param", u,
		"quote_reply_text_formatting_param", l.a.Settings.String("text_formatting")), "class", css)
	if iconOnly {
		h.Set("title", l.L("button_quote"))
	}
	label := l.a.Helpers.Icon(l.page, "quote-filled", l.L("button_quote"), rails.NewHash("icon_only", iconOnly, "style", "filled"))
	return rails.LinkTo(label, "#", h)
}

// actionsDropdown は actions_dropdown { content }。
func (l *issueLookup) actionsDropdown(content template.HTML) template.HTML {
	if !rails.IsPresent(string(content)) {
		return ""
	}
	trigger := rails.ContentTag("span", l.icon("3-bullets", l.L("button_actions")),
		rails.NewHash("class", "icon-only icon-actions", "title", l.L("button_actions")))
	trigger = rails.ContentTag("span", trigger, rails.NewHash("class", "drdn-trigger"))
	body := rails.ContentTag("div", rails.ContentTag("div", content, rails.NewHash("class", "drdn-items")), rails.NewHash("class", "drdn-content"))
	return rails.ContentTag("span", trigger+body, rails.NewHash("class", "drdn"))
}

// reactionButton は reaction_button(object)（kind は issue / journal）。
func (l *issueLookup) reactionButton(kind string, id int64, issue *issueModel) template.HTML {
	if !l.a.Settings.Bool("reactions_enabled") {
		return ""
	}
	users, mine, err := l.reactionDetail(kind, id)
	l.fail(err)
	count := len(users)
	var names []string
	for i, u := range users {
		if i >= 10 {
			break
		}
		names = append(names, l.principalName(u))
	}
	var tooltip any
	if count > 0 {
		dn := slices.Clone(names)
		if others := count - len(names); others > 0 {
			dn = append(dn, l.L("reaction_text_x_other_users", i18n.Vars{"count": others}))
		}
		tooltip = l.toSentence(dn)
	}
	var countLabel any
	if count > 0 {
		countLabel = count
	}
	objType := "Issue"
	if kind == "journal" {
		objType = "Journal"
	}
	sid := strconv.FormatInt(id, 10)
	wrap := func(inner template.HTML) template.HTML {
		return rails.ContentTag("span", inner, rails.NewHash("class", "reaction-button-wrapper", "data", rails.NewHash("reaction-button-id", "reaction_"+kind+"_"+sid)))
	}
	cls := func(base string) string {
		if count > 0 {
			return base + " has-reactions"
		}
		return base
	}
	editable := l.c.User.Logged() && issue.Project != nil && issue.Project.Active()
	if !editable {
		return wrap(rails.ContentTag("span", l.icon("thumb-up", countLabel), rails.NewHash("class", cls("icon reaction-button readonly"), "title", tooltip)))
	}
	if mine != 0 {
		return wrap(rails.LinkTo(l.a.Helpers.Icon(l.page, "thumb-up-filled", countLabel, rails.NewHash("style", "filled")),
			"/reactions/"+strconv.FormatInt(mine, 10)+"?object_id="+sid+"&object_type="+objType,
			rails.NewHash("class", cls("icon reaction-button reacted"), "title", tooltip, "remote", true, "method", "delete")))
	}
	return wrap(rails.LinkTo(l.icon("thumb-up", countLabel), "/reactions?object_id="+sid+"&object_type="+objType,
		rails.NewHash("remote", true, "method", "post", "class", cls("icon reaction-button"), "title", tooltip)))
}

// preloadReactions は ids のリアクションをまとめて読み込む（reaction_button を一覧で描くときの N+1 を避ける）。
func (l *issueLookup) preloadReactions(kind string, ids []int64) {
	if len(ids) == 0 || !l.a.Settings.Bool("reactions_enabled") {
		return
	}
	vis, err := l.c.Authz().PrincipalVisibleCondition(l.ctx)
	if err != nil {
		l.fail(err)
		return
	}
	m, err := repository.ReactionsForMany(l.ctx, l.a.DB, kind, ids, vis)
	if err != nil {
		l.fail(err)
		return
	}
	if l.reactions == nil {
		l.reactions = map[string]map[int64][]*repository.ReactionRow{}
		l.reactionsLoaded = map[string]map[int64]bool{}
	}
	if l.reactions[kind] == nil {
		l.reactions[kind] = map[int64][]*repository.ReactionRow{}
		l.reactionsLoaded[kind] = map[int64]bool{}
	}
	for _, id := range ids {
		l.reactions[kind][id] = m[id]
		l.reactionsLoaded[kind][id] = true
	}
	var uids []int64
	for _, rows := range m {
		for _, r := range rows {
			uids = append(uids, r.UserID)
		}
	}
	l.preloadPrincipals(uids)
}

// reactionDetail は Reaction.build_detail_map_for（可視なユーザー、id の降順）と自分のリアクションの id。
func (l *issueLookup) reactionDetail(kind string, id int64) ([]*domain.User, int64, error) {
	var rows []*repository.ReactionRow
	if l.reactionsLoaded[kind][id] {
		rows = l.reactions[kind][id]
	} else {
		vis, err := l.c.Authz().PrincipalVisibleCondition(l.ctx)
		if err != nil {
			return nil, 0, err
		}
		if rows, err = repository.ReactionsFor(l.ctx, l.a.DB, kind, id, vis); err != nil {
			return nil, 0, err
		}
	}
	var uids []int64
	for _, r := range rows {
		uids = append(uids, r.UserID)
	}
	l.preloadPrincipals(uids)
	var users []*domain.User
	var mine int64
	for _, r := range rows {
		if u := l.principal(r.UserID); u != nil {
			users = append(users, u)
		}
		if r.UserID == l.c.User.ID && l.c.User.Logged() {
			mine = r.ID
		}
	}
	return users, mine, nil
}

// toSentence は Array#to_sentence（support.array の区切り）。
func (l *issueLookup) toSentence(items []string) string {
	two := l.L("support.array.two_words_connector")
	words := l.L("support.array.words_connector")
	last := l.L("support.array.last_word_connector")
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + two + items[1]
	}
	return strings.Join(items[:len(items)-1], words) + last + items[len(items)-1]
}

// issueURL は issue_url(issue)（完全 URL）。
func issueURL(c *Req, id int64) string {
	return httpx.RequestBaseURL(c.R) + urlroot.Path("/issues/"+strconv.FormatInt(id, 10))
}
