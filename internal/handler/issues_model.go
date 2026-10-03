// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

// チケット詳細・編集フォーム・コンテキストメニューで使う Issue モデル。権限・ワークフロー・割り当て候補などの
// 判定は internal/issues（チケットのドメインサービス）に委ね、ここでは表示用の値への変換だけを行う。

import (
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
)

// issueModel は 1 件のチケットと関連（@issue）。
type issueModel struct {
	l   *issueLookup
	Row *query.IssueRow
	I   *issues.Issue

	Project  *domain.Project
	Tracker  *domain.Tracker
	Status   *domain.IssueStatus
	Priority *domain.Enumeration

	statuses     []*domain.IssueStatus
	statusesDone bool
	cfValues     []*issueCFValue
	cfLoaded     bool
	descendants  []*query.IssueRow
	descLoaded   bool
}

// issueCFValue は CustomFieldValue（カスタムフィールドと値）。
type issueCFValue struct {
	CF     *customfield.CustomField
	Values []string
	// Multi は値が配列（複数選択）。
	Multi bool
}

// Value は表示・入力欄に渡す値（単一値は string か nil、複数値は []string）。
func (v *issueCFValue) Value() any {
	if v.Multi {
		return v.Values
	}
	if len(v.Values) == 0 {
		return nil
	}
	return v.Values[0]
}

func cfValueFrom(c *issues.CustomFieldValue) *issueCFValue {
	v := &issueCFValue{CF: c.Field, Multi: c.Value.IsArray()}
	if c.Value.IsArray() {
		v.Values = c.Value.Strings()
	} else if !c.Value.IsNil() {
		v.Values = []string{c.Value.String()}
	}
	return v
}

// issuesEnv は User.current の issues.Env（リクエスト内で 1 つ）。
func (l *issueLookup) issuesEnv() *issues.Env {
	if l.ie == nil {
		e := issues.NewEnv(l.a.DB, l.a.Settings, l.c.User)
		e.Now = l.a.now
		e.Notifier = l.a.issueNotifier()
		e.Translate = func(key string, args ...any) string { return l.c.L(key, args...) }
		l.ie = e
	}
	return l.ie
}

func (l *issueLookup) model(r *query.IssueRow) *issueModel {
	m := &issueModel{l: l, Row: r, Project: l.project(r.ProjectID), Tracker: l.tracker(r.TrackerID),
		Status: l.status(r.StatusID), Priority: l.priority(r.PriorityID)}
	iss, err := l.issuesEnv().Find(l.ctx, r.ID)
	l.fail(err)
	m.I = iss
	return m
}

// models は rows の各行について model と同じものを作る（チケットとカスタム値をまとめて読み込む）。
func (l *issueLookup) models(rows []*query.IssueRow) []*issueModel {
	if len(rows) == 0 {
		return nil
	}
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = strconv.FormatInt(r.ID, 10)
	}
	byID := map[int64]*issues.Issue{}
	for len(ids) > 0 {
		n := min(len(ids), 500)
		loaded, err := l.issuesEnv().LoadMany(l.ctx, "issues.id IN ("+strings.Join(ids[:n], ",")+")")
		l.fail(err)
		for _, iss := range loaded {
			byID[iss.ID] = iss
		}
		ids = ids[n:]
	}
	out := make([]*issueModel, len(rows))
	for i, r := range rows {
		out[i] = &issueModel{l: l, Row: r, Project: l.project(r.ProjectID), Tracker: l.tracker(r.TrackerID),
			Status: l.status(r.StatusID), Priority: l.priority(r.PriorityID), I: byID[r.ID]}
	}
	return out
}

func (m *issueModel) env() *issues.Env { return m.l.issuesEnv() }

func (m *issueModel) user() *domain.User { return m.l.c.User }

func (m *issueModel) allowedTo(perm string) bool {
	return m.l.c.AllowedTo(domain.Perm(perm), m.Project)
}

func (m *issueModel) check(ok bool, err error) bool {
	m.l.fail(err)
	return ok && err == nil
}

// AttributesEditable は Issue#attributes_editable?。
func (m *issueModel) AttributesEditable() bool {
	return m.I != nil && m.check(m.env().AttributesEditable(m.l.ctx, m.I, m.user()))
}

// NotesAddable は Issue#notes_addable?。
func (m *issueModel) NotesAddable() bool {
	return m.I != nil && m.check(m.env().NotesAddable(m.l.ctx, m.I, m.user()))
}

// Editable は Issue#editable?。
func (m *issueModel) Editable() bool {
	return m.I != nil && m.check(m.env().Editable(m.l.ctx, m.I, m.user()))
}

// AttachmentsAddable は Issue#attachments_addable?。
func (m *issueModel) AttachmentsAddable() bool {
	return m.I != nil && m.check(m.env().AttachmentsAddable(m.l.ctx, m.I, m.user()))
}

// AttachmentsEditable は Issue#attachments_editable?。
func (m *issueModel) AttachmentsEditable() bool {
	return m.I != nil && m.check(m.env().AttachmentsEditable(m.l.ctx, m.I, m.user()))
}

// AttachmentsDeletable は Issue#attachments_deletable?。
func (m *issueModel) AttachmentsDeletable() bool {
	return m.I != nil && m.check(m.env().AttachmentsDeletable(m.l.ctx, m.I, m.user()))
}

// Deletable は Issue#deletable?。
func (m *issueModel) Deletable() bool {
	return m.I != nil && m.check(m.env().Deletable(m.l.ctx, m.I, m.user()))
}

// TimeLoggable は Issue#time_loggable?。
func (m *issueModel) TimeLoggable() bool {
	return m.I != nil && m.check(m.env().TimeLoggable(m.l.ctx, m.I, m.user()))
}

// Closed は Issue#closed?。
func (m *issueModel) Closed() bool { return m.Status.IsClosed }

// Leaf は Issue#leaf?。
func (m *issueModel) Leaf() bool { return !m.l.hasChildren(m.Row.ID) }

// FieldDisabled は disabled_core_fields.include?(name)。
func (m *issueModel) FieldDisabled(name string) bool {
	return slices.Contains(m.Tracker.DisabledCoreFields, name)
}

// ReadOnlyAttribute は read_only_attribute_names.include?(name)。
func (m *issueModel) ReadOnlyAttribute(name string) bool {
	if m.I == nil {
		return false
	}
	ro, err := m.env().ReadOnlyAttributeNames(m.l.ctx, m.I, m.user())
	m.l.fail(err)
	return slices.Contains(ro, name)
}

// RequiredAttribute は required_attribute?(name)。
func (m *issueModel) RequiredAttribute(name string) bool {
	return m.I != nil && m.check(m.env().RequiredAttribute(m.l.ctx, m.I, name, m.user()))
}

// SafeAttribute は safe_attribute?(name)。
func (m *issueModel) SafeAttribute(name string) bool {
	return m.I != nil && m.check(m.env().SafeAttribute(m.l.ctx, m.I, name, m.user()))
}

// DefaultStatus は Issue#default_status。
func (m *issueModel) DefaultStatus() *domain.IssueStatus {
	if m.I == nil {
		return nil
	}
	s, err := m.env().DefaultStatus(m.l.ctx, m.I)
	m.l.fail(err)
	return s
}

// NewStatusesAllowed は Issue#new_statuses_allowed_to(User.current)。
func (m *issueModel) NewStatusesAllowed() []*domain.IssueStatus {
	if m.statusesDone || m.I == nil {
		return m.statuses
	}
	m.statusesDone = true
	ss, err := m.env().NewStatusesAllowedTo(m.l.ctx, m.I, m.user(), false)
	m.l.fail(err)
	m.statuses = ss
	return ss
}

// TransitionWarning は Issue#transition_warning（翻訳済み）。
func (m *issueModel) TransitionWarning() string {
	m.NewStatusesAllowed()
	if m.I == nil || m.I.TransitionWarning == "" {
		return ""
	}
	return m.l.L(m.I.TransitionWarning)
}

// Descendants は issue.descendants（ツリー順）。
func (m *issueModel) Descendants() []*query.IssueRow {
	if m.descLoaded {
		return m.descendants
	}
	m.descLoaded = true
	rows, err := repository.IssueDescendants(m.l.ctx, m.l.a.DB, m.Row.RootID, m.Row.HierPath)
	m.l.fail(err)
	for _, r := range rows {
		m.descendants = append(m.descendants, issueRowFromRead(r))
	}
	m.l.addIssues(m.descendants)
	return m.descendants
}

// VisibleDescendants は issue.descendants.visible。
func (m *issueModel) VisibleDescendants() []*query.IssueRow {
	var out []*query.IssueRow
	for _, d := range m.Descendants() {
		if m.l.issueVisible(d) {
			out = append(out, d)
		}
	}
	return out
}

// Ancestors は issue.ancestors（ルートから順）。
func (m *issueModel) Ancestors() []*query.IssueRow {
	var out []*query.IssueRow
	ids := repository.IssueAncestorIDs(m.Row.HierPath)
	m.l.preloadIssues(ids)
	for _, id := range ids {
		if r := m.l.issue(id); r != nil {
			out = append(out, r)
		}
	}
	return out
}

// ---------------------------------------------------------------- カスタムフィールド

func (m *issueModel) convertCF(vs []*issues.CustomFieldValue, err error) []*issueCFValue {
	m.l.fail(err)
	out := make([]*issueCFValue, 0, len(vs))
	for _, v := range vs {
		out = append(out, cfValueFrom(v))
	}
	return out
}

// CustomFieldValues は Issue#custom_field_values。
func (m *issueModel) CustomFieldValues() []*issueCFValue {
	if !m.cfLoaded && m.I != nil {
		m.cfLoaded = true
		m.cfValues = m.convertCF(m.env().CustomFieldValues(m.l.ctx, m.I))
	}
	return m.cfValues
}

// VisibleCustomFieldValues は Issue#visible_custom_field_values。
func (m *issueModel) VisibleCustomFieldValues() []*issueCFValue {
	if m.I == nil {
		return nil
	}
	return m.convertCF(m.env().VisibleCustomFieldValues(m.l.ctx, m.I, m.user()))
}

// EditableCustomFieldValues は Issue#editable_custom_field_values。
func (m *issueModel) EditableCustomFieldValues() []*issueCFValue {
	if m.I == nil {
		return nil
	}
	vs := m.convertCF(m.env().EditableCustomFieldValues(m.l.ctx, m.I, m.user()))
	if m.I.NewRecord() {
		// 新規チケットの CustomValue は value ||= custom_field.default_value なので、既定値が空文字列なら ""
		// （issues パッケージは空の既定値を nil として扱う）
		for _, v := range vs {
			if !v.Multi && len(v.Values) == 0 && v.CF.DefaultValue != nil {
				v.Values = []string{""}
			}
		}
	}
	return vs
}

// customized は CustomValue#customized。
func (m *issueModel) customized() *customfield.Customized { return m.l.customized(m.Row) }

// ---------------------------------------------------------------- 割り当て候補

// principalsOf は PrincipalRef をキャッシュ済みの domain.User にする（順序は保つ）。
func (m *issueModel) principalsOf(ps []*issues.PrincipalRef, err error) []*domain.User {
	m.l.fail(err)
	ids := make([]int64, len(ps))
	for i, p := range ps {
		ids[i] = p.ID
	}
	m.l.preloadPrincipals(ids)
	var out []*domain.User
	for _, id := range ids {
		if u := m.l.principal(id); u != nil {
			out = append(out, u)
		}
	}
	return out
}

// AssignableUsers は Issue#assignable_users。
func (m *issueModel) AssignableUsers() []*domain.User {
	if m.I == nil {
		return nil
	}
	return m.principalsOf(m.env().AssignableUsers(m.l.ctx, m.I))
}

// PriorAssignedTo は Issue#prior_assigned_to。
func (m *issueModel) PriorAssignedTo() *domain.User {
	if m.I == nil {
		return nil
	}
	p, err := m.env().PriorAssignedTo(m.l.ctx, m.I)
	m.l.fail(err)
	if p == nil {
		return nil
	}
	return m.l.principal(p.ID)
}

// versionsOf は domain.Version を表示用の repository.Version にする（順序は保つ）。
func (m *issueModel) versionsOf(vs []*domain.Version, err error) []*repository.Version {
	m.l.fail(err)
	ids := make([]int64, len(vs))
	for i, v := range vs {
		ids[i] = v.ID
	}
	m.l.preloadVersions(ids)
	var out []*repository.Version
	for _, id := range ids {
		if v := m.l.versions[id]; v != nil {
			out = append(out, v)
		}
	}
	return out
}

// AssignableVersions は Issue#assignable_versions。
func (m *issueModel) AssignableVersions() []*repository.Version {
	if m.I == nil {
		return nil
	}
	return m.versionsOf(m.env().AssignableVersions(m.l.ctx, m.I))
}

// AllowedTargetProjects は Issue#allowed_target_projects(User.current)。
func (m *issueModel) AllowedTargetProjects() []*domain.Project {
	if m.I == nil {
		return nil
	}
	ps, err := m.env().AllowedTargetProjects(m.l.ctx, m.I, m.user(), "*")
	m.l.fail(err)
	return ps
}

// AllowedTargetTrackers は Issue#allowed_target_trackers(User.current)。
func (m *issueModel) AllowedTargetTrackers() []*domain.Tracker {
	if m.I == nil {
		return nil
	}
	ts, err := m.env().AllowedTargetTrackers(m.l.ctx, m.I, m.user())
	m.l.fail(err)
	return ts
}

// allowedTargetProjectsAny は Issue.allowed_target_projects(User.current).any?（コピーリンクの表示条件）。
func (l *issueLookup) allowedTargetProjectsAny() bool {
	e := l.issuesEnv()
	blank, err := e.NewBlank(l.ctx)
	if err != nil {
		l.fail(err)
		return false
	}
	ps, err := e.AllowedTargetProjects(l.ctx, blank, l.c.User, "*")
	l.fail(err)
	return len(ps) > 0
}

// ---------------------------------------------------------------- 時間

// SpentHours は Issue#spent_hours（Issue.load_visible_spent_hours 済み（Row.SpentHours）ならその値）。
func (m *issueModel) SpentHours() float64 {
	if m.Row != nil && m.Row.SpentHours != nil {
		return *m.Row.SpentHours
	}
	if m.I == nil {
		return 0
	}
	v, err := m.env().SpentHours(m.l.ctx, m.I)
	m.l.fail(err)
	return v
}

// TotalSpentHours は Issue#total_spent_hours（load_visible_total_spent_hours 済みならその値）。
func (m *issueModel) TotalSpentHours() float64 {
	if m.Row != nil && m.Row.TotalSpentHours != nil {
		return *m.Row.TotalSpentHours
	}
	if m.I == nil {
		return 0
	}
	v, err := m.env().TotalSpentHours(m.l.ctx, m.I)
	m.l.fail(err)
	return v
}

// TotalEstimatedHours は Issue#total_estimated_hours。
func (m *issueModel) TotalEstimatedHours() *float64 {
	if m.I == nil {
		return nil
	}
	v, err := m.env().TotalEstimatedHours(m.l.ctx, m.I)
	m.l.fail(err)
	return v
}

// Heading は issue_heading(issue)。
func (m *issueModel) Heading() string {
	return m.Tracker.Name + " #" + strconv.FormatInt(m.Row.ID, 10)
}

// CanManageCategories は User.current.allowed_to?(:manage_categories, @issue.project)。
func (m *issueModel) CanManageCategories() bool { return m.allowedTo("manage_categories") }

// CanManageVersions は User.current.allowed_to?(:manage_versions, @issue.project)。
func (m *issueModel) CanManageVersions() bool { return m.allowedTo("manage_versions") }

// issueRowFromIssue は編集中（未保存の変更を含む）チケットの表示用の行。
func issueRowFromIssue(iss *issues.Issue) *query.IssueRow {
	r := &query.IssueRow{ID: iss.ID, ProjectID: iss.ProjectID, TrackerID: iss.TrackerID, StatusID: iss.StatusID,
		PriorityID: iss.PriorityID, AuthorID: iss.AuthorID, AssignedToID: iss.AssignedToID, CategoryID: iss.CategoryID,
		FixedVersionID: iss.FixedVersionID, ParentID: iss.ParentID, RootID: iss.RootID, HierPath: iss.HierPath,
		Subject: iss.Subject, StartDate: iss.StartDate, DueDate: iss.DueDate, DoneRatio: iss.DoneRatio,
		EstimatedHours: iss.EstimatedHours, IsPrivate: iss.IsPrivate, LockVersion: iss.LockVersion,
		CreatedAt: iss.CreatedAt, UpdatedAt: iss.UpdatedAt, ClosedAt: iss.ClosedAt}
	if iss.Description != nil {
		r.Description = *iss.Description
	}
	return r
}

// modelFor は編集中のチケット（issues.Issue のインスタンス）の issueModel（new / edit / update の @issue）。
func (l *issueLookup) modelFor(iss *issues.Issue) *issueModel {
	r := issueRowFromIssue(iss)
	return &issueModel{l: l, Row: r, I: iss, Project: l.project(r.ProjectID), Tracker: l.tracker(r.TrackerID),
		Status: l.status(r.StatusID), Priority: l.priority(r.PriorityID)}
}
