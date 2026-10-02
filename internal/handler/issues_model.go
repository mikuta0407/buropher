package handler

// チケット詳細・編集フォーム・コンテキストメニューで使う Issue モデルの参照系メソッド
// （app/models/issue.rb の editable? / safe_attribute? / new_statuses_allowed_to / assignable_users ...）。
//
// TODO(dedupe): internal/issues（チケットのドメインサービス）が入ったら、ワークフロー判定などはそちらに寄せる。

import (
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
)

// issueModel は 1 件のチケットと関連（@issue）。
type issueModel struct {
	l   *issueLookup
	Row *query.IssueRow

	Project  *domain.Project
	Tracker  *domain.Tracker
	Status   *domain.IssueStatus
	Priority *domain.Enumeration

	wfRules       map[string]string
	wfLoaded      bool
	roles         []*domain.Role
	rolesLoaded   bool
	cfValues      []*issueCFValue
	cfLoaded      bool
	descendants   []*query.IssueRow
	descLoaded    bool
	transitionMsg string
	closableDone  bool
	closable      bool
	reopenDone    bool
	reopenable    bool
	statuses      []*domain.IssueStatus
	statusesDone  bool
}

// issueCFValue は CustomFieldValue（カスタムフィールドと値）。
type issueCFValue struct {
	CF     *customfield.CustomField
	Values []string
}

// Value は単一値（無ければ nil）。複数値なら []string。
func (v *issueCFValue) Value() any {
	if v.CF.Multiple {
		if len(v.Values) == 0 {
			return []string{""}
		}
		return v.Values
	}
	if len(v.Values) == 0 {
		return nil
	}
	return v.Values[0]
}

func (l *issueLookup) model(r *query.IssueRow) *issueModel {
	return &issueModel{l: l, Row: r, Project: l.project(r.ProjectID), Tracker: l.tracker(r.TrackerID),
		Status: l.status(r.StatusID), Priority: l.priority(r.PriorityID)}
}

func (m *issueModel) user() *domain.User { return m.l.c.User }

func (m *issueModel) allowedTo(perm string) bool {
	return m.l.c.AllowedTo(domain.Perm(perm), m.Project)
}

// userTrackerPermission は Issue#user_tracker_permission?。
func (m *issueModel) userTrackerPermission(perm string) bool {
	ok, err := m.l.c.Authz().UserTrackerPermission(m.l.ctx, m.Project, m.Row.TrackerID, perm)
	m.l.fail(err)
	return ok
}

// AttributesEditable は Issue#attributes_editable?。
func (m *issueModel) AttributesEditable() bool {
	return m.userTrackerPermission("edit_issues") ||
		(m.userTrackerPermission("edit_own_issues") && m.Row.AuthorID == m.user().ID && m.user().Logged())
}

// NotesAddable は Issue#notes_addable?。
func (m *issueModel) NotesAddable() bool { return m.userTrackerPermission("add_issue_notes") }

// Editable は Issue#editable?。
func (m *issueModel) Editable() bool { return m.AttributesEditable() || m.NotesAddable() }

// AttachmentsAddable は Issue#attachments_addable?。
func (m *issueModel) AttachmentsAddable() bool { return m.AttributesEditable() || m.NotesAddable() }

// AttachmentsDeletable は Issue#attachments_deletable?（visible? は find_issue で確認済み）。
func (m *issueModel) AttachmentsDeletable() bool { return m.AttributesEditable() }

// Deletable は Issue#deletable?。
func (m *issueModel) Deletable() bool { return m.userTrackerPermission("delete_issues") }

// Closed は Issue#closed?。
func (m *issueModel) Closed() bool { return m.Status.IsClosed }

// TimeLoggable は Issue#time_loggable?。
func (m *issueModel) TimeLoggable() bool {
	return m.allowedTo("log_time") && (m.l.a.Settings.Bool("timelog_accept_closed_issues") || !m.Closed())
}

// Leaf は Issue#leaf?。
func (m *issueModel) Leaf() bool { return !m.l.hasChildren(m.Row.ID) }

// DisabledCoreFields は Issue#disabled_core_fields。
func (m *issueModel) DisabledCoreFields() []string { return m.Tracker.DisabledCoreFields }

// FieldDisabled は disabled_core_fields.include?(name)。
func (m *issueModel) FieldDisabled(name string) bool { return slices.Contains(m.Tracker.DisabledCoreFields, name) }

// rolesForWorkflow は Issue#roles_for_workflow(User.current)。
func (m *issueModel) rolesForWorkflow() []*domain.Role {
	if m.rolesLoaded {
		return m.roles
	}
	m.rolesLoaded = true
	var roles []*domain.Role
	var err error
	if m.user().IsAdmin() {
		roles, err = repository.ListRoles(m.l.ctx, m.l.a.DB)
	} else {
		roles, err = m.l.c.Authz().RolesForProject(m.l.ctx, m.Project)
	}
	m.l.fail(err)
	for _, r := range roles {
		if r.ConsiderWorkflow() {
			m.roles = append(m.roles, r)
		}
	}
	return m.roles
}

// workflowRuleByAttribute は Issue#workflow_rule_by_attribute(User.current)。
func (m *issueModel) workflowRuleByAttribute() map[string]string {
	if m.wfLoaded {
		return m.wfRules
	}
	m.wfLoaded = true
	m.wfRules = map[string]string{}
	roles := m.rolesForWorkflow()
	if len(roles) == 0 {
		return m.wfRules
	}
	perms, err := repository.WorkflowFieldRules(m.l.ctx, m.l.a.DB, []int64{m.Row.TrackerID}, roleIDs(roles))
	m.l.fail(err)
	var mine []*domain.WorkflowFieldRule
	for _, p := range perms {
		if p.StatusID == m.Row.StatusID {
			mine = append(mine, p)
		}
	}
	if len(mine) == 0 {
		return m.wfRules
	}
	rules := map[string]map[int64]string{}
	var order []string
	add := func(field string, role int64, rule string) {
		if rules[field] == nil {
			rules[field] = map[int64]string{}
			order = append(order, field)
		}
		rules[field][role] = rule
	}
	for _, p := range mine {
		add(p.FieldName, p.RoleID, p.Rule)
	}
	hidden, err := repository.HiddenIssueCustomFieldRoles(m.l.ctx, m.l.a.DB)
	m.l.fail(err)
	for _, r := range roles {
		for cfID, rids := range hidden {
			if !slices.Contains(rids, r.ID) {
				add(strconv.FormatInt(cfID, 10), r.ID, domain.WorkflowRuleReadonly)
			}
		}
	}
	for _, field := range order {
		rs := rules[field]
		if len(rs) < len(roles) {
			continue
		}
		uniq := map[string]bool{}
		var first string
		for _, v := range rs {
			uniq[v] = true
			first = v
		}
		if len(uniq) == 1 {
			m.wfRules[field] = first
		} else {
			m.wfRules[field] = domain.WorkflowRuleRequired
		}
	}
	return m.wfRules
}

// ReadOnlyAttribute は read_only_attribute_names.include?(name)。
func (m *issueModel) ReadOnlyAttribute(name string) bool {
	return m.workflowRuleByAttribute()[name] == domain.WorkflowRuleReadonly
}

// RequiredAttribute は required_attribute?(name)。
func (m *issueModel) RequiredAttribute(name string) bool {
	return m.workflowRuleByAttribute()[name] == domain.WorkflowRuleRequired
}

// SafeAttribute は safe_attribute?(name)（既存チケット）。
func (m *issueModel) SafeAttribute(name string) bool {
	editable := m.AttributesEditable()
	ok := false
	switch name {
	case "project_id", "tracker_id", "status_id", "category_id", "assigned_to_id", "priority_id", "fixed_version_id",
		"subject", "description", "start_date", "due_date", "done_ratio", "estimated_hours", "custom_field_values",
		"custom_fields", "lock_version":
		ok = editable
	case "notes":
		ok = m.NotesAddable()
	case "private_notes":
		ok = m.allowedTo("set_notes_private")
	case "watcher_user_ids":
		ok = false
	case "is_private":
		ok = m.allowedTo("set_issues_private") || (m.Row.AuthorID == m.user().ID && m.allowedTo("set_own_issues_private"))
	case "parent_issue_id":
		ok = editable && m.allowedTo("manage_subtasks")
	case "deleted_attachment_ids":
		ok = m.AttachmentsDeletable()
	}
	if !ok {
		return false
	}
	if slices.Contains(m.Tracker.DisabledCoreFields, name) || m.ReadOnlyAttribute(name) {
		return false
	}
	// dates_derived? / priority_derived? / done_ratio_derived?
	if !m.Leaf() {
		switch name {
		case "start_date", "due_date":
			if m.l.a.Settings.String("parent_issue_dates") == "derived" {
				return false
			}
		case "priority_id":
			if m.l.a.Settings.String("parent_issue_priority") == "derived" {
				return false
			}
		case "done_ratio":
			if m.l.a.Settings.String("parent_issue_done_ratio") == "derived" {
				return false
			}
		}
	}
	return true
}

// DefaultStatus は Issue#default_status。
func (m *issueModel) DefaultStatus() *domain.IssueStatus {
	if m.Tracker.DefaultStatusID == 0 {
		return nil
	}
	return m.l.status(m.Tracker.DefaultStatusID)
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

// closable は Issue#closable?。
func (m *issueModel) isClosable() bool {
	if m.closableDone {
		return m.closable
	}
	m.closableDone = true
	for _, d := range m.Descendants() {
		if !m.l.status(d.StatusID).IsClosed {
			m.transitionMsg = m.l.L("notice_issue_not_closable_by_open_tasks")
			return false
		}
	}
	rels, err := repository.IssueRelations(m.l.ctx, m.l.a.DB, m.Row.ID)
	m.l.fail(err)
	for _, r := range rels {
		if r.IssueToID == m.Row.ID && r.RelationType == "blocks" {
			if from := m.l.issue(r.IssueFromID); from != nil && !m.l.status(from.StatusID).IsClosed {
				m.transitionMsg = m.l.L("notice_issue_not_closable_by_blocking_issue")
				return false
			}
		}
	}
	m.closable = true
	return true
}

// isReopenable は Issue#reopenable?。
func (m *issueModel) isReopenable() bool {
	if m.reopenDone {
		return m.reopenable
	}
	m.reopenDone = true
	for _, a := range m.Ancestors() {
		if m.l.status(a.StatusID).IsClosed {
			m.transitionMsg = m.l.L("notice_issue_not_reopenable_by_closed_parent_issue")
			return false
		}
	}
	m.reopenable = true
	return true
}

// TransitionWarning は Issue#transition_warning（new_statuses_allowed_to の後で設定される）。
func (m *issueModel) TransitionWarning() string {
	m.NewStatusesAllowed()
	return m.transitionMsg
}

// NewStatusesAllowed は Issue#new_statuses_allowed_to(User.current)（既存チケット）。
func (m *issueModel) NewStatusesAllowed() []*domain.IssueStatus {
	if m.statusesDone {
		return m.statuses
	}
	m.statusesDone = true
	initial := m.Status
	u := m.user()
	assigneeOK := false
	if m.Row.AssignedToID != nil {
		if *m.Row.AssignedToID == u.ID {
			assigneeOK = true
		} else {
			gids, err := m.l.c.Authz().GroupIDs(m.l.ctx)
			m.l.fail(err)
			assigneeOK = slices.Contains(gids, *m.Row.AssignedToID)
		}
	}
	author := m.Row.AuthorID == u.ID && u.Logged()
	var statuses []*domain.IssueStatus
	roles := m.rolesForWorkflow()
	if len(roles) > 0 {
		ts, err := repository.WorkflowTransitions(m.l.ctx, m.l.a.DB, []int64{m.Row.TrackerID}, roleIDs(roles))
		m.l.fail(err)
		seen := map[int64]bool{}
		for _, t := range ts {
			if t.OldStatusID != initial.ID {
				continue
			}
			if !(author && assigneeOK) {
				if author || assigneeOK {
					if !((t.Author && author) || (t.Assignee && assigneeOK)) {
						continue
					}
				} else if t.Author || t.Assignee {
					continue
				}
			}
			if !seen[t.NewStatusID] {
				seen[t.NewStatusID] = true
				statuses = append(statuses, m.l.status(t.NewStatusID))
			}
		}
	}
	if len(statuses) > 0 {
		statuses = append(statuses, initial)
	}
	// compact.uniq.sort
	uniq := map[int64]bool{}
	var out []*domain.IssueStatus
	for _, s := range statuses {
		if s != nil && !uniq[s.ID] {
			uniq[s.ID] = true
			out = append(out, s)
		}
	}
	slices.SortStableFunc(out, func(a, b *domain.IssueStatus) int { return a.Position - b.Position })
	if !m.isClosable() {
		out = slices.DeleteFunc(out, func(s *domain.IssueStatus) bool { return s.IsClosed })
	}
	if !m.isReopenable() {
		out = slices.DeleteFunc(out, func(s *domain.IssueStatus) bool { return !s.IsClosed })
	}
	m.statuses = out
	return out
}

// ---------------------------------------------------------------- カスタムフィールド

// availableCustomFields は Issue#available_custom_fields（project.all_issue_custom_fields & tracker.custom_fields）。
func (m *issueModel) availableCustomFields() []*customfield.CustomField {
	t := m.l.a.DB.Dialect().BoolLiteral(true)
	cfs, err := customfield.Load(m.l.ctx, m.l.a.DB, `custom_fields.owner_kind = 'issue'
AND (custom_fields.is_for_all = `+t+` OR custom_fields.id IN (SELECT custom_field_id FROM custom_fields_projects WHERE project_id = ?))
AND custom_fields.id IN (SELECT custom_field_id FROM custom_fields_trackers WHERE tracker_id = ?)`, m.Row.ProjectID, m.Row.TrackerID)
	m.l.fail(err)
	slices.SortStableFunc(cfs, func(a, b *customfield.CustomField) int {
		if a.Position != b.Position {
			return a.Position - b.Position
		}
		return int(a.ID - b.ID)
	})
	return cfs
}

// CustomFieldValues は Issue#custom_field_values。
func (m *issueModel) CustomFieldValues() []*issueCFValue {
	if m.cfLoaded {
		return m.cfValues
	}
	m.cfLoaded = true
	vals, err := repository.CustomValues(m.l.ctx, m.l.a.DB, "issue", m.Row.ID)
	m.l.fail(err)
	for _, cf := range m.availableCustomFields() {
		m.cfValues = append(m.cfValues, &issueCFValue{CF: cf, Values: vals[cf.ID]})
	}
	return m.cfValues
}

// VisibleCustomFieldValues は Issue#visible_custom_field_values。
func (m *issueModel) VisibleCustomFieldValues() []*issueCFValue {
	var out []*issueCFValue
	for _, v := range m.CustomFieldValues() {
		if m.l.cfVisibleBy(v.CF, m.Project) {
			out = append(out, v)
		}
	}
	return out
}

// EditableCustomFieldValues は Issue#editable_custom_field_values。
func (m *issueModel) EditableCustomFieldValues() []*issueCFValue {
	var out []*issueCFValue
	for _, v := range m.VisibleCustomFieldValues() {
		if !m.ReadOnlyAttribute(strconv.FormatInt(v.CF.ID, 10)) {
			out = append(out, v)
		}
	}
	return out
}

// customized は CustomValue#customized。
func (m *issueModel) customized() *customfield.Customized { return m.l.customized(m.Row) }

// ---------------------------------------------------------------- 割り当て候補

// AssignableUsers は Issue#assignable_users。
func (m *issueModel) AssignableUsers() []*domain.User {
	ids := m.projectAssignableIDs(m.Project, m.Tracker)
	if a := m.l.principal(m.Row.AuthorID); a != nil && a.Active() {
		ids = append(ids, a.ID)
	}
	if m.Row.AssignedToID != nil {
		ids = append(ids, *m.Row.AssignedToID)
	}
	m.l.preloadPrincipals(ids)
	seen := map[int64]bool{}
	var out []*domain.User
	for _, id := range ids {
		if u := m.l.principal(id); u != nil && !seen[id] {
			seen[id] = true
			out = append(out, u)
		}
	}
	m.l.sortPrincipals(out)
	return out
}

// projectAssignableIDs は project.assignable_users(tracker) の id。
func (m *issueModel) projectAssignableIDs(p *domain.Project, t *domain.Tracker) []int64 {
	var rids []int64
	if t != nil {
		roles, err := repository.ListRoles(m.l.ctx, m.l.a.DB)
		m.l.fail(err)
		rids = []int64{}
		for _, r := range roles {
			if r.Assignable && r.PermissionsTracker("view_issues", t.ID) {
				rids = append(rids, r.ID)
			}
		}
	}
	ids, err := repository.ProjectAssignablePrincipalIDs(m.l.ctx, m.l.a.DB, p.ID, rids, m.l.a.Settings.Bool("issue_group_assignment"))
	m.l.fail(err)
	return ids
}

// AssignableVersions は Issue#assignable_versions（project.shared_versions.open + fixed_version）。
func (m *issueModel) AssignableVersions() []*repository.Version {
	vs, err := repository.VersionsWhere(m.l.ctx, m.l.a.DB, repository.SharedVersionsCondition(m.Project)+" AND versions.status = 'open'")
	m.l.fail(err)
	if m.Row.FixedVersionID != nil {
		if v := m.l.version(m.Row.FixedVersionID); v != nil && !slices.ContainsFunc(vs, func(x *repository.Version) bool { return x.ID == v.ID }) {
			vs = append(vs, v)
		}
	}
	repository.SortVersions(vs)
	return vs
}

// AllowedTargetProjects は Issue#allowed_target_projects(User.current)（既存チケットは自身のプロジェクトを含む）。
func (m *issueModel) AllowedTargetProjects() []*domain.Project {
	return m.l.allowedTargetProjects(m.Project)
}

// allowedTargetProjects は Issue.allowed_target_projects(user, current_project)（having_trackers、lft 順）。
func (l *issueLookup) allowedTargetProjects(current *domain.Project) []*domain.Project {
	cond, err := l.c.Authz().AllowedToCondition(l.ctx, "add_issues", authz.ConditionOptions{}, nil)
	l.fail(err)
	where := "(" + cond + ")"
	if current != nil {
		where = "((" + cond + ") OR projects.id = " + strconv.FormatInt(current.ID, 10) + ")"
	}
	where += " AND EXISTS (SELECT 1 FROM project_trackers pt WHERE pt.project_id = projects.id)"
	ps, err := repository.LoadProjects(l.ctx, l.a.DB, where)
	l.fail(err)
	l.fail(repository.SortProjectsByTree(l.ctx, l.a.DB, ps))
	return ps
}

// AllowedTargetTrackers は Issue#allowed_target_trackers(User.current)。
func (m *issueModel) AllowedTargetTrackers() []*domain.Tracker {
	ids, err := m.l.c.Authz().AllowedTargetTrackerIDs(m.l.ctx, m.Project, m.Row.TrackerID)
	m.l.fail(err)
	var out []*domain.Tracker
	for _, id := range ids {
		out = append(out, m.l.tracker(id))
	}
	slices.SortStableFunc(out, func(a, b *domain.Tracker) int { return a.Position - b.Position })
	return out
}

// ---------------------------------------------------------------- 時間

// SpentHours は Issue#spent_hours（可視な作業時間）。
func (m *issueModel) SpentHours() float64 {
	v, err := repository.IssueSpentHours(m.l.ctx, m.l.a.DB, m.Row.ID, m.l.timeEntryVisibleCondition())
	m.l.fail(err)
	return v
}

// TotalSpentHours は Issue#total_spent_hours。
func (m *issueModel) TotalSpentHours() float64 {
	v, err := repository.IssueTotalSpentHours(m.l.ctx, m.l.a.DB, m.Row.RootID, m.Row.HierPath, m.l.timeEntryVisibleCondition())
	m.l.fail(err)
	return v
}

// TotalEstimatedHours は Issue#total_estimated_hours。
func (m *issueModel) TotalEstimatedHours() *float64 { return m.l.totalEstimatedHours(m.Row) }

// issueHeading は issue_heading(issue)。
func (m *issueModel) Heading() string {
	return m.Tracker.Name + " #" + strconv.FormatInt(m.Row.ID, 10)
}

func joinNonEmpty(parts []string, sep string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}
