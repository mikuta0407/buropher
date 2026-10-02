package handler

// チケット詳細画面に埋め込まれる編集フォーム（issues/_edit・_form・_attributes・_form_custom_fields）の
// 表示用データ。送信（update）は後続の書き込み系の移植で扱う。

import (
	"html/template"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/attachments"
	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/timelog"
	"github.com/mikuta0407/buropher/internal/urlroot"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// issueEditForm は issues/_edit の @issue / @time_entry / @priorities ...。
type issueEditForm struct {
	l *issueLookup
	v *issueShowView
	m *issueModel

	// Model は labelled_form_for @issue のモデル。
	Model *issueFormModel
	// TimeEntry は @time_entry（time_tracking モジュールが無効なら nil）。
	TimeEntry *timeEntryFormModel

	// IsNew は @issue.new_record?。
	IsNew bool
	// ErrorMessages は error_messages_for 'issue'（edit では 'issue', 'time_entry'）。
	ErrorMessages []string
	// Conflict は @conflict（楽観ロックの衝突）。
	Conflict bool
	// ConflictJournals は @conflict_journals。
	ConflictJournals []*journalView
	// saved は save_attachments の結果（saved_attachments の再表示）。
	saved *attachments.SaveResult

	cfEnvCache map[int64]*customfield.Env
}

// newIssueEditForm は編集フォームのデータを作る。
func (a *App) newIssueEditForm(c *Req, l *issueLookup, v *issueShowView) *issueEditForm {
	m := v.M
	f := &issueEditForm{l: l, v: v, m: m, Model: &issueFormModel{l: l, m: m}, IsNew: m.I != nil && m.I.NewRecord()}
	if !f.IsNew && m.Project.ModuleEnabled("time_tracking") {
		f.TimeEntry = a.newTimeEntryFormModel(l, m)
	}
	return f
}

// ---------------------------------------------------------------- フォームのモデル

// issueFormModel は labelled_form_for / labelled_fields_for :issue のモデル（rails.Sender）。
type issueFormModel struct {
	l *issueLookup
	m *issueModel
}

// ParamKey は model_name.param_key。
func (f *issueFormModel) ParamKey() string { return "issue" }

// Persisted は persisted?。
func (f *issueFormModel) Persisted() bool { return f.m.I == nil || f.m.I.Persisted() }

// ErrorsOn は errors[attr]（ラベルの class="error" に使う）。
func (f *issueFormModel) ErrorsOn(attr string) []string {
	if f.m.I == nil {
		return nil
	}
	return f.m.I.Errors.On(attr)
}

// ToParam は to_param。
func (f *issueFormModel) ToParam() string { return strconv.FormatInt(f.m.Row.ID, 10) }

// HumanAttributeName は Issue.human_attribute_name(attr)。
func (f *issueFormModel) HumanAttributeName(attr string) string {
	return f.l.L("field_" + strings.TrimSuffix(attr, "_id"))
}

func idOrNil(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func dateOrNilValue(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

// Send は属性値（rails.Sender）。
func (f *issueFormModel) Send(method string) (any, bool) {
	r := f.m.Row
	switch method {
	case "id":
		return r.ID, true
	case "is_private":
		return r.IsPrivate, true
	case "project_id":
		return r.ProjectID, true
	case "tracker_id":
		return r.TrackerID, true
	case "status_id":
		return r.StatusID, true
	case "priority_id":
		return r.PriorityID, true
	case "assigned_to_id":
		return idOrNil(r.AssignedToID), true
	case "category_id":
		return idOrNil(r.CategoryID), true
	case "fixed_version_id":
		return idOrNil(r.FixedVersionID), true
	case "parent_issue_id":
		if f.m.I != nil {
			if s := f.m.I.ParentIssueID(); s != "" {
				return s, true
			}
			return nil, true
		}
		return idOrNil(r.ParentID), true
	case "subject":
		return r.Subject, true
	case "description":
		if f.m.I != nil && f.m.I.Description == nil {
			return nil, true
		}
		return r.Description, true
	case "start_date":
		return dateOrNilValue(r.StartDate), true
	case "due_date":
		return dateOrNilValue(r.DueDate), true
	case "estimated_hours":
		if r.EstimatedHours == nil {
			return nil, true
		}
		return *r.EstimatedHours, true
	case "estimated_hours_before_type_cast":
		if f.m.I != nil && f.m.I.EstimatedHoursBeforeTypeCast() != "" {
			return f.m.I.EstimatedHoursBeforeTypeCast(), true
		}
		if r.EstimatedHours == nil {
			return nil, true
		}
		return *r.EstimatedHours, true
	case "done_ratio":
		return r.DoneRatio, true
	case "lock_version":
		return r.LockVersion, true
	case "notes":
		if f.m.I != nil {
			if n := f.m.I.Notes(); n != nil {
				return *n, true
			}
		}
		return nil, true
	case "private_notes":
		return f.m.I != nil && f.m.I.PrivateNotes(), true
	}
	return nil, false
}

// timeEntryFormModel は labelled_fields_for :time_entry, @time_entry のモデル（TimeEntry.new(:issue, :project)）。
type timeEntryFormModel struct {
	l          *issueLookup
	m          *issueModel
	ActivityID any
	Activities []*domain.Enumeration
	CFValues   []*issueCFValue
	// TE は入力値を代入済みの @time_entry（edit / update。nil なら新規の既定値）。
	TE *timelog.Entry
}

// setInput は @time_entry（safe_attributes = params[:time_entry] の結果）をフォームに反映する。
func (t *timeEntryFormModel) setInput(te *timelog.Entry) {
	t.TE = te
	if te == nil {
		return
	}
	t.ActivityID = nil
	if te.ActivityID != nil {
		t.ActivityID = *te.ActivityID
	}
	for _, v := range t.CFValues {
		if vals, ok := te.CFValues[v.CF.ID]; ok {
			v.Values = vals
		}
	}
}

// newTimeEntryFormModel は TimeEntry.new(:issue => @issue, :project => @issue.project)。
func (a *App) newTimeEntryFormModel(l *issueLookup, m *issueModel) *timeEntryFormModel {
	t := &timeEntryFormModel{l: l, m: m}
	acts, err := repository.ProjectActivities(l.ctx, a.DB, m.Project.ID, false)
	l.fail(err)
	t.Activities = acts
	for _, e := range acts {
		if e.IsDefault {
			t.ActivityID = e.ID
		}
	}
	// editable_custom_field_values（新規レコードなので既定値）
	cfs, err := repository.VisibleTimeEntryCustomFieldIDs(l.ctx, a.DB)
	l.fail(err)
	for _, id := range cfs {
		cf := l.customField(id)
		if cf == nil || !l.cfVisibleBy(cf, m.Project) {
			continue
		}
		v := &issueCFValue{CF: cf, Multi: cf.Multiple}
		if d := cf.DefaultValueString(); d != "" {
			v.Values = []string{d}
		} else if cf.Multiple {
			v.Values = []string{""}
		}
		t.CFValues = append(t.CFValues, v)
	}
	return t
}

// ParamKey / Persisted / HumanAttributeName は FormBuilder 用。
func (t *timeEntryFormModel) ParamKey() string { return "time_entry" }
func (t *timeEntryFormModel) Persisted() bool  { return false }
func (t *timeEntryFormModel) HumanAttributeName(attr string) string {
	return t.l.L("field_" + strings.TrimSuffix(attr, "_id"))
}

// Send は属性値。
func (t *timeEntryFormModel) Send(method string) (any, bool) {
	switch method {
	case "hours":
		if t.TE != nil && t.TE.Hours != nil {
			return *t.TE.Hours, true
		}
		return nil, true
	case "hours_before_type_cast":
		if t.TE != nil && t.TE.HoursBeforeTypeCast != nil {
			return t.TE.HoursBeforeTypeCast, true
		}
		if t.TE != nil && t.TE.Hours != nil {
			return *t.TE.Hours, true
		}
		return nil, true
	case "comments":
		if t.TE != nil && t.TE.Comments != nil {
			return *t.TE.Comments, true
		}
		return nil, true
	case "activity_id":
		return t.ActivityID, true
	}
	return nil, false
}

// ErrorsOn は errors[attr]。
func (t *timeEntryFormModel) ErrorsOn(attr string) []string {
	if t.TE == nil || t.TE.Errors == nil {
		return nil
	}
	return t.TE.Errors.Messages(t.l.c.Loc, attr)
}

// ActivityOptions は activity_collection_for_select_options(@time_entry)。
func (t *timeEntryFormModel) ActivityOptions() []any {
	var out []any
	hasDefault := false
	for _, a := range t.Activities {
		if a.IsDefault {
			hasDefault = true
		}
	}
	if !hasDefault {
		out = append(out, []any{"--- " + t.l.L("actionview_instancetag_blank_option") + " ---", ""})
	}
	for _, a := range t.Activities {
		out = append(out, []any{a.Name, a.ID})
	}
	return out
}

// CustomFieldTags は @time_entry.editable_custom_field_values の custom_field_tag_with_label。
func (t *timeEntryFormModel) CustomFieldTags(f *issueEditForm) []template.HTML {
	var out []template.HTML
	for _, v := range t.CFValues {
		cz := &customfield.Customized{Kind: "time_entry", ProjectID: t.m.Project.ID, ProjectIdentifier: t.m.Project.Identifier}
		out = append(out, f.customFieldTagWithLabel("time_entry", v, cz, false))
	}
	return out
}

// ---------------------------------------------------------------- _form

// UpdateFormPath は update_issue_form_path(@project, @issue)（escape_javascript 済みの onchange 用）。
func (f *issueEditForm) UpdateFormOnchange() string {
	return "updateIssueFrom('" + rails.EscapeJavascriptString(urlroot.Path(f.updateFormPath())) + "', this)"
}

// updateFormPath は update_issue_form_path(@project, @issue)。
func (f *issueEditForm) updateFormPath() string {
	if f.IsNew {
		if p := f.l.c.Project; p != nil {
			return "/projects/" + p.Identifier + "/issues/new.js"
		}
		return "/issues/new.js"
	}
	return "/issues/" + strconv.FormatInt(f.m.Row.ID, 10) + "/edit.js"
}

// CategoryOnchange は category_id の onchange（新規のときだけ update_form を呼ぶ）。
func (f *issueEditForm) CategoryOnchange() any {
	if f.IsNew {
		return f.UpdateFormOnchange()
	}
	return nil
}

// PreviewURL は preview_issue_path(:project_id => @issue.project, :issue_id => @issue.id)。
func (f *issueEditForm) PreviewURL() string {
	if f.IsNew {
		return "/issues/preview?project_id=" + url.QueryEscape(f.m.Project.Identifier)
	}
	return "/issues/preview?issue_id=" + strconv.FormatInt(f.m.Row.ID, 10) + "&project_id=" + url.QueryEscape(f.m.Project.Identifier)
}

// NotesPreviewURL は preview_issue_path(:project_id => @project, :issue_id => @issue)。
func (f *issueEditForm) NotesPreviewURL() string {
	p := f.l.c.Project
	if p == nil {
		return "/issues/preview?issue_id=" + strconv.FormatInt(f.m.Row.ID, 10)
	}
	return "/issues/preview?issue_id=" + strconv.FormatInt(f.m.Row.ID, 10) + "&project_id=" + url.QueryEscape(p.Identifier)
}

// ShowProjectSelect は project_id の select の表示条件（既存チケット）。
func (f *issueEditForm) ShowProjectSelect() bool {
	if !f.m.SafeAttribute("project_id") && !(f.m.I != nil && f.m.I.AttrChanged("project_id")) {
		return false
	}
	return f.l.c.Project == nil || len(f.projects()) > 1 || (f.m.I != nil && f.m.I.IsCopy())
}

// projects は projects_for_select(@issue)。
func (f *issueEditForm) projects() []*domain.Project {
	var ps []*domain.Project
	switch iss := f.m.I; {
	case iss != nil && iss.ParentIssueID() != "":
		var err error
		ps, err = f.m.env().AllowedTargetProjectsForSubtask(f.l.ctx, iss, f.l.c.User)
		f.l.fail(err)
	case iss != nil && f.l.c.Project != nil && iss.NewRecord() && !iss.IsCopy():
		var err error
		ps, err = f.m.env().AllowedTargetProjects(f.l.ctx, iss, f.l.c.User, "tree")
		f.l.fail(err)
	default:
		ps = f.m.AllowedTargetProjects()
	}
	if f.m.ReadOnlyAttribute("project_id") {
		if id := f.l.c.Params().String("project_id"); rails.IsPresent(id) {
			if p, err := repository.FindProjectByIdentifier(f.l.ctx, f.l.a.DB, id); err == nil {
				return []*domain.Project{p}
			}
			return nil
		}
	}
	return ps
}

// ProjectOptions は project_tree_options_for_select(projects, :selected => @issue.project)。
func (f *issueEditForm) ProjectOptions() template.HTML {
	return projectTreeOptions(f.l, f.projects(), f.m.Project.ID)
}

// projectTreeOptions は project_tree_options_for_select（ツリー順に並んだプロジェクト）。
func projectTreeOptions(l *issueLookup, ps []*domain.Project, selected int64) template.HTML {
	var b strings.Builder
	levels := projectLevels(ps)
	for _, p := range ps {
		prefix := ""
		if lv := levels[p.ID]; lv > 0 {
			prefix = strings.Repeat("&nbsp;", 2*lv) + "&#187; "
		}
		var sel any
		if p.ID == selected {
			sel = "selected"
		}
		b.WriteString(string(rails.ContentTag("option", template.HTML(prefix)+rails.H(p.Name), rails.NewHash("value", p.ID, "selected", sel))))
	}
	return template.HTML(b.String())
}

// projectLevels は Project.project_tree の深さ（一覧に含まれる祖先の数）。
func projectLevels(ps []*domain.Project) map[int64]int {
	in := map[int64]*domain.Project{}
	for _, p := range ps {
		in[p.ID] = p
	}
	out := map[int64]int{}
	var stack []*domain.Project
	for _, p := range ps {
		for len(stack) > 0 && !isProjectAncestorIn(stack[len(stack)-1], p, in) {
			stack = stack[:len(stack)-1]
		}
		out[p.ID] = len(stack)
		stack = append(stack, p)
	}
	return out
}

func isProjectAncestorIn(anc, p *domain.Project, in map[int64]*domain.Project) bool {
	for cur := p; cur.ParentID != nil; {
		if *cur.ParentID == anc.ID {
			return true
		}
		next := in[*cur.ParentID]
		if next == nil {
			// 一覧に無い祖先は辿れないため、親 id だけで判定する
			return false
		}
		cur = next
	}
	return false
}

// ShowTrackerSelect は safe_attribute?('tracker_id') || (persisted? && tracker_id_changed?)。
func (f *issueEditForm) ShowTrackerSelect() bool {
	return f.m.SafeAttribute("tracker_id") || (f.m.I != nil && f.m.I.Persisted() && f.m.I.AttrChanged("tracker_id"))
}

// trackersForSelect は trackers_for_select(@issue)。
func (f *issueEditForm) trackersForSelect() []*domain.Tracker {
	ts := f.m.AllowedTargetTrackers()
	if iss := f.m.I; iss != nil && iss.NewRecord() && iss.ParentIssueID() != "" {
		var out []*domain.Tracker
		for _, t := range ts {
			if iss.TrackerID != t.ID && slices.Contains(t.DisabledCoreFields, "parent_issue_id") {
				continue
			}
			out = append(out, t)
		}
		ts = out
	}
	return ts
}

// TrackerOptions は trackers_options_for_select(@issue)。
func (f *issueEditForm) TrackerOptions() []any {
	var out []any
	for _, t := range f.trackersForSelect() {
		out = append(out, []any{t.Name, t.ID})
	}
	return out
}

// TrackerTitle は @issue.tracker.description。
func (f *issueEditForm) TrackerTitle() any {
	if f.m.Tracker.Description == nil {
		return nil
	}
	return *f.m.Tracker.Description
}

// TrackersWithDescription は trackers_for_select(@issue) のうち説明のあるもの。
func (f *issueEditForm) TrackersWithDescription() []*domain.Tracker {
	var out []*domain.Tracker
	for _, t := range f.trackersForSelect() {
		if strings.TrimSpace(t.DescriptionString()) != "" {
			out = append(out, t)
		}
	}
	return out
}

// DescriptionRows は [[10, @issue.description.to_s.length / 50].max, 20].min。
func (f *issueEditForm) DescriptionRows() int {
	n := len([]rune(f.m.Row.Description)) / 50
	if n < 10 {
		n = 10
	}
	if n > 20 {
		n = 20
	}
	return n
}

// ListAutofillData は {:auto_complete => true}.merge(list_autofill_data_attributes)。
func (f *issueEditForm) ListAutofillData() *rails.Hash {
	h := rails.NewHash("auto_complete", true)
	if tf := f.l.a.Settings.String("text_formatting"); tf != "" {
		h.Set("controller", "list-autofill")
		h.Set("action", "beforeinput->list-autofill#handleBeforeInput")
		h.Set("list_autofill_text_formatting_param", tf)
	}
	return h
}

// ---------------------------------------------------------------- _attributes

// ShowStatusSelect は @issue.safe_attribute?('status_id') && @allowed_statuses.present?。
func (f *issueEditForm) ShowStatusSelect() bool {
	return f.m.SafeAttribute("status_id") && len(f.v.AllowedStatuses) > 0
}

// StatusOptions は @allowed_statuses.collect {|p| [p.name, p.id]}。
func (f *issueEditForm) StatusOptions() []any {
	var out []any
	for _, s := range f.v.AllowedStatuses {
		out = append(out, []any{s.Name, s.ID})
	}
	return out
}

// StatusTitle は @issue.status.description。
func (f *issueEditForm) StatusTitle() any {
	if f.m.Status.Description == nil {
		return nil
	}
	return *f.m.Status.Description
}

// StatusesWithDescription は説明のある許可ステータス（issue_status_description）。
func (f *issueEditForm) StatusesWithDescription() []*domain.IssueStatus {
	var out []*domain.IssueStatus
	for _, s := range f.v.AllowedStatuses {
		if strings.TrimSpace(s.DescriptionString()) != "" {
			out = append(out, s)
		}
	}
	return out
}

// TransitionWarning は @issue.transition_warning。
func (f *issueEditForm) TransitionWarning() string { return f.m.TransitionWarning() }

// WasDefaultStatus は @issue.status == @issue.default_status。
func (f *issueEditForm) WasDefaultStatus() bool {
	d := f.m.DefaultStatus()
	return d != nil && d.ID == f.m.Row.StatusID
}

// PriorityOptions は @priorities（IssuePriority.active）。
func (f *issueEditForm) PriorityOptions() []any {
	f.l.loadMasters()
	var out []any
	for _, p := range f.l.prioOrd {
		if p.Active {
			out = append(out, []any{p.Name, p.ID})
		}
	}
	return out
}

// AssigneeOptions は principals_options_for_select(@issue.assignable_users, @issue.assigned_to)。
func (f *issueEditForm) AssigneeOptions() template.HTML {
	l := f.l
	users := f.m.AssignableUsers()
	contains := func(id int64) bool {
		for _, u := range users {
			if u.ID == id {
				return true
			}
		}
		return false
	}
	var s strings.Builder
	cur := l.c.User
	if cur.Logged() && contains(cur.ID) {
		s.WriteString(string(rails.ContentTag("option", "<< "+l.L("label_me")+" >>", rails.NewHash("value", cur.ID))))
	}
	// involved principals（author, prior_assigned_to）
	// この optgroup は既存チケットの編集時だけ表示する
	var involved []*domain.User
	if !f.IsNew {
		if a := l.principal(f.m.Row.AuthorID); a != nil {
			involved = append(involved, a)
		}
		if p := f.m.PriorAssignedTo(); p != nil && (len(involved) == 0 || involved[0].ID != p.ID) {
			involved = append(involved, p)
		}
	}
	var invHTML strings.Builder
	for _, p := range involved {
		invHTML.WriteString(string(rails.ContentTag("option", l.principalName(p), rails.NewHash("value", p.ID, "disabled", !contains(p.ID)))))
	}
	sorted := append([]*domain.User(nil), users...)
	l.sortPrincipals(sorted)
	var usersHTML, groupsHTML strings.Builder
	for _, u := range sorted {
		sel := ""
		if f.m.Row.AssignedToID != nil && *f.m.Row.AssignedToID == u.ID {
			sel = ` selected="selected"`
		}
		opt := `<option value="` + strconv.FormatInt(u.ID, 10) + `"` + sel + `>` + string(rails.H(l.principalName(u))) + `</option>`
		if u.Kind.IsGroup() {
			groupsHTML.WriteString(opt)
		} else {
			usersHTML.WriteString(opt)
		}
	}
	if invHTML.Len() == 0 && groupsHTML.Len() == 0 {
		s.WriteString(usersHTML.String())
	} else {
		for _, g := range [][2]string{{l.L("label_involved_principals"), invHTML.String()}, {l.L("label_user_plural"), usersHTML.String()},
			{l.L("label_group_plural"), groupsHTML.String()}} {
			if g[1] != "" {
				s.WriteString(`<optgroup label="` + string(rails.H(g[0])) + `">` + g[1] + `</optgroup>`)
			}
		}
	}
	return template.HTML(s.String())
}

// ShowAssignToMe は @issue.assignable_users.include?(User.current)。
func (f *issueEditForm) ShowAssignToMe() bool {
	cur := f.l.c.User
	if !cur.Logged() {
		return false
	}
	for _, u := range f.m.AssignableUsers() {
		if u.ID == cur.ID {
			return true
		}
	}
	return false
}

// AssignToMeClass は assign-to-me-link のクラス。
func (f *issueEditForm) AssignToMeClass() string {
	if f.m.Row.AssignedToID != nil && *f.m.Row.AssignedToID == f.l.c.User.ID {
		return "assign-to-me-link hidden"
	}
	return "assign-to-me-link"
}

// CategoryOptions は @issue.project.issue_categories.pluck(:name, :id)。
func (f *issueEditForm) CategoryOptions() []any {
	var out []any
	for _, c := range f.l.projectCategories(f.m.Project.ID) {
		out = append(out, []any{c.Name, c.ID})
	}
	return out
}

// VersionOptions は version_options_for_select(@issue.assignable_versions, @issue.fixed_version)。
func (f *issueEditForm) VersionOptions() template.HTML {
	vs := f.m.AssignableVersions()
	var keys []string
	grouped := map[string][]any{}
	for _, v := range vs {
		if _, ok := grouped[v.ProjectName]; !ok {
			keys = append(keys, v.ProjectName)
		}
		grouped[v.ProjectName] = append(grouped[v.ProjectName], []any{v.Name, v.ID})
	}
	sel := idOrNil(f.m.Row.FixedVersionID)
	if len(keys) > 1 {
		var g []any
		for _, k := range keys {
			g = append(g, []any{k, grouped[k]})
		}
		return rails.GroupedOptionsForSelect(g, sel, nil)
	}
	var items []any
	if len(keys) == 1 {
		items = grouped[keys[0]]
	}
	return rails.OptionsForSelect(items, sel)
}

// HasAssignableVersions は @issue.assignable_versions.any?。
func (f *issueEditForm) HasAssignableVersions() bool { return len(f.m.AssignableVersions()) > 0 }

// ParentAutocompleteJS は observeAutocompleteField('issue_parent_issue_id', ...)。
func (f *issueEditForm) ParentAutocompleteJS() string {
	status := "o"
	if f.m.Closed() {
		status = "c"
	}
	var issueID any = f.m.Row.ID
	if f.IsNew {
		issueID = nil
	}
	u := "/issues/auto_complete?" + helper.ToQuery(rails.NewHash("issue_id", issueID, "project_id", f.m.Project.Identifier,
		"scope", f.l.a.Settings.String("cross_project_subtasks"), "status", status))
	return "observeAutocompleteField('issue_parent_issue_id', '" + rails.EscapeJavascriptString(urlroot.Path(u)) + "')"
}

// UseFieldForDoneRatio は Issue.use_field_for_done_ratio?。
func (f *issueEditForm) UseFieldForDoneRatio() bool {
	return f.l.a.Settings.String("issue_done_ratio") == "issue_field"
}

// DoneRatioOptions は (0..100).step(Setting.issue_done_ratio_interval.to_i)。
func (f *issueEditForm) DoneRatioOptions() []any {
	step, _ := strconv.Atoi(f.l.a.Settings.String("issue_done_ratio_interval"))
	if step <= 0 {
		step = 10
	}
	var out []any
	for r := 0; r <= 100; r += step {
		out = append(out, []any{strconv.Itoa(r) + " %", r})
	}
	return out
}

// EstimatedHoursOptions は hours_field :estimated_hours の options。
func (f *issueEditForm) HoursFieldOptions(size int, required bool, value *float64) *rails.Hash {
	if f.m.I != nil && len(f.m.I.Errors.On("estimated_hours")) > 0 {
		// hours_field: エラーがあれば value_before_type_cast をそのまま表示する（placeholder も付けない）
		h := rails.NewHash("size", size)
		if required {
			h.Set("required", true)
		}
		return h
	}
	h := rails.NewHash("placeholder", "h:mm", "size", size)
	if required {
		h.Set("required", true)
	}
	if value != nil {
		h.Set("value", f.l.c.Loc.FormatHours(*value))
	} else {
		h.Set("value", "")
	}
	return h
}

// TimeEntryHoursOptions は time_entry.hours_field :hours, :size => 6, :label => :label_spent_time の options
// （検証エラーが無ければ format_hours(hours)、あれば入力値のまま）。
func (f *issueEditForm) TimeEntryHoursOptions() *rails.Hash {
	t := f.TimeEntry
	if t != nil && t.TE != nil && t.TE.Errors.Include("hours") {
		return rails.NewHash("size", 6, "label", rails.Symbol("label_spent_time"))
	}
	h := rails.NewHash("placeholder", "h:mm", "size", 6, "label", rails.Symbol("label_spent_time"))
	if t != nil && t.TE != nil && t.TE.Hours != nil {
		h.Set("value", f.l.c.Loc.FormatHours(*t.TE.Hours))
	} else {
		h.Set("value", "")
	}
	return h
}

// EstimatedHours は @issue.estimated_hours。
func (f *issueEditForm) EstimatedHours() *float64 { return f.m.Row.EstimatedHours }

// ---------------------------------------------------------------- _form_custom_fields

var tagIDRe = regexp.MustCompile(` id="(.+?)"`)

// jsonForJS は Hash#to_json（JSON.parse('...') に埋め込む）。
func jsonForJS(v any) string { return rails.ToJSON(v) }

// customFieldTagWithLabel は custom_field_tag_with_label(prefix, value, :required => ...)。
func (f *issueEditForm) customFieldTagWithLabel(prefix string, v *issueCFValue, cz *customfield.Customized, required bool) template.HTML {
	tag := f.customFieldTag(prefix, v, cz)
	var forID any = prefix + "_custom_field_values_" + strconv.FormatInt(v.CF.ID, 10)
	if ids := tagIDRe.FindAllStringSubmatch(string(tag), -1); len(ids) == 1 {
		forID = ids[0][1]
	} else {
		forID = nil
	}
	content := customFieldNameTag(v.CF)
	if required || v.CF.IsRequired {
		content += template.HTML(` <span class="required">*</span>`)
	}
	return rails.ContentTag("label", content, rails.NewHash("for", forID, "class", nil)) + tag
}

// customFieldTag は custom_field_tag(prefix, custom_value)。
func (f *issueEditForm) customFieldTag(prefix string, v *issueCFValue, cz *customfield.Customized) template.HTML {
	cf := v.CF
	css := cf.CSSClasses()
	var placeholder any
	if d := cf.Description; d != nil {
		s := *d
		if cf.FieldFormat != "text" {
			s = strings.ReplaceAll(s, "\n", " ")
		}
		placeholder = s
	}
	var data any
	if cf.FullTextFormatting() {
		css += " wiki-edit"
		data = f.ListAutofillData()
	}
	id := prefix + "_custom_field_values_" + strconv.FormatInt(cf.ID, 10)
	name := prefix + "[custom_field_values][" + strconv.FormatInt(cf.ID, 10) + "]"
	cv := &customfield.CustomValue{CustomField: cf, Customized: cz, Value: v.Value(), ValueWas: v.Value()}
	return cf.Format().EditTag(f.cfEnv(cf), id, name, cv, rails.NewHash("class", css, "placeholder", placeholder, "data", data))
}

// cfEnv は edit_tag の環境（calendar_for はテンプレートの include_calendar_headers_tags と同じ規則）。
func (f *issueEditForm) cfEnv(cf *customfield.CustomField) *customfield.Env {
	env := f.l.cfEnv(cf)
	env.CalendarFor = func(fieldID string) template.HTML {
		return rails.JavascriptTag("$(function() { $('#"+fieldID+"').addClass('date').datepickerFallback(datepickerOptions); });", nil)
	}
	env.ProjectUsers = func(projectID int64, roleIDs []int64) []customfield.Option {
		return f.l.projectUserOptions(projectID, roleIDs)
	}
	env.SharedVersions = func(projectID int64, statuses []string) []customfield.Option {
		return f.l.sharedVersionOptions(projectID, statuses)
	}
	return env
}

// projectUserOptions は project.users（roleIDs で絞る）を [name, id] で返す。
func (l *issueLookup) projectUserOptions(projectID int64, roleIDs []int64) []customfield.Option {
	ids, err := repository.ProjectMemberUserIDs(l.ctx, l.a.DB, projectID, roleIDs)
	l.fail(err)
	l.preloadPrincipals(ids)
	var us []*domain.User
	for _, id := range ids {
		if u := l.principal(id); u != nil {
			us = append(us, u)
		}
	}
	l.sortUsersByFormat(us)
	var out []customfield.Option
	for _, u := range us {
		out = append(out, customfield.Option{Label: l.principalName(u), Value: strconv.FormatInt(u.ID, 10)})
	}
	return out
}

// sharedVersionOptions は project.shared_versions（statuses で絞る）。
func (l *issueLookup) sharedVersionOptions(projectID int64, statuses []string) []customfield.Option {
	p := l.project(projectID)
	if p == nil {
		return nil
	}
	where := repository.SharedVersionsCondition(p)
	if len(statuses) > 0 {
		var qs []string
		for _, s := range statuses {
			qs = append(qs, "'"+strings.ReplaceAll(s, "'", "''")+"'")
		}
		where += " AND versions.status IN (" + strings.Join(qs, ",") + ")"
	}
	vs, err := repository.VersionsWhere(l.ctx, l.a.DB, where)
	l.fail(err)
	repository.SortVersions(vs)
	var out []customfield.Option
	for _, v := range vs {
		out = append(out, customfield.Option{Label: v.Name, Value: strconv.FormatInt(v.ID, 10)})
	}
	return out
}

// cfFormRow は _form_custom_fields の 1 項目。
type cfFormRow struct {
	Tag       template.HTML
	Toolbar   bool
	FieldID   string
	SplitHere bool
}

// HalfCustomFields は _form_custom_fields の左右 2 列の項目（split_on の位置で SplitHere）。
func (f *issueEditForm) HalfCustomFields() []cfFormRow {
	var vals []*issueCFValue
	for _, v := range f.m.EditableCustomFieldValues() {
		if !v.CF.FullWidthLayout() {
			vals = append(vals, v)
		}
	}
	splitOn := (len(vals)+1)/2 - 1
	var out []cfFormRow
	for i, v := range vals {
		out = append(out, cfFormRow{Tag: f.customFieldTagWithLabel("issue", v, f.m.customized(),
			f.m.RequiredAttribute(strconv.FormatInt(v.CF.ID, 10))), SplitHere: i == splitOn})
	}
	return out
}

// FullCustomFields は全幅のカスタムフィールド。
func (f *issueEditForm) FullCustomFields() []cfFormRow {
	var out []cfFormRow
	for _, v := range f.m.EditableCustomFieldValues() {
		if !v.CF.FullWidthLayout() {
			continue
		}
		out = append(out, cfFormRow{Tag: f.customFieldTagWithLabel("issue", v, f.m.customized(),
			f.m.RequiredAttribute(strconv.FormatInt(v.CF.ID, 10))), Toolbar: v.CF.FullTextFormatting(),
			FieldID: "issue_custom_field_values_" + strconv.FormatInt(v.CF.ID, 10)})
	}
	return out
}

// ---------------------------------------------------------------- _edit

// ShowWatcherDataSources は !@issue.attributes_editable? && User.current.allowed_to?(:add_issue_watchers, @issue.project)。
func (f *issueEditForm) ShowWatcherDataSources() bool {
	return !f.m.AttributesEditable() && f.m.allowedTo("add_issue_watchers")
}

// WatchersDataSourcesJS は update_data_sources_for_auto_complete({users: watchers_autocomplete_for_mention_path(...)})。
func (f *issueEditForm) WatchersDataSourcesJS() template.HTML {
	var objectID any = f.m.Row.ID
	if f.IsNew {
		objectID = nil
	}
	u := "/watchers/autocomplete_for_mention?" + helper.ToQuery(rails.NewHash("object_id", objectID, "object_type", "issue",
		"project_id", f.m.Project.Identifier, "q", ""))
	return rails.JavascriptTag("rm.AutoComplete.dataSources = Object.assign(rm.AutoComplete.dataSources, JSON.parse('"+
		jsonForJS(map[string]string{"users": urlroot.Path(u)})+"'));", nil)
}

// LastJournalID は params[:last_journal_id] || @issue.last_journal_id。
func (f *issueEditForm) LastJournalID() any {
	if s := f.l.c.Params().String("last_journal_id"); s != "" {
		return s
	}
	id, err := repository.LastJournalID(f.l.ctx, f.l.a.DB, f.m.Row.ID)
	f.l.fail(err)
	if id == nil {
		return nil
	}
	return *id
}

// FormURL は issue_path(@issue)（labelled_form_for @issue の url）。
func (f *issueEditForm) FormURL() string {
	if f.IsNew {
		// _project_issues_path(@project)
		if p := f.l.c.Project; p != nil {
			return "/projects/" + p.Identifier + "/issues"
		}
		return "/issues"
	}
	return "/issues/" + strconv.FormatInt(f.m.Row.ID, 10)
}

// AttachmentsPartialData は attachments/_form の data 属性。
func (f *issueEditForm) AttachmentFileField() template.HTML {
	l := f.l
	maxKB, _ := strconv.Atoi(l.a.Settings.String("attachment_max_size"))
	size := l.c.Loc.NumberToHumanSize(int64(maxKB) * 1024)
	data := rails.NewHash(
		"max_number_of_files_message", l.L("error_attachments_too_many", map[string]any{"max_number_of_files": 10}),
		"max_file_size", int64(maxKB)*1024,
		"max_file_size_message", l.L("error_attachment_too_big", map[string]any{"max_size": size}),
		"max_concurrent_uploads", 2,
		"upload_path", "/uploads.js",
		"param", "attachments",
		"description", true,
		"description_placeholder", l.L("label_optional_description"),
	)
	return rails.FileFieldTag("attachments[dummy][file]", rails.NewHash("id", nil, "class", "file_selector filedrop",
		"multiple", true, "onchange", "addInputFiles(this);", "data", data))
}

// MaxSize は number_to_human_size(Setting.attachment_max_size.to_i.kilobytes)。
func (f *issueEditForm) MaxSize() string {
	maxKB, _ := strconv.Atoi(f.l.a.Settings.String("attachment_max_size"))
	return f.l.c.Loc.NumberToHumanSize(int64(maxKB) * 1024)
}

// ExistingAttachments は @issue.attachments（削除用の一覧を出す条件込み）。
func (f *issueEditForm) ExistingAttachments() []*repository.ReadAttachment {
	if !f.m.SafeAttribute("deleted_attachment_ids") {
		return nil
	}
	return f.l.issueAttachments(f.m.Row.ID)
}
