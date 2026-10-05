// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

// IssuesController の一括編集・一括更新・削除（bulk_edit / bulk_update / destroy）。
//
//	resources :issues { collection { match 'bulk_edit', :via => [:get, :post]; match 'bulk_update', :via => [:post, :patch] } }
//	match '/issues', :controller => 'issues', :action => 'destroy', :via => :delete
//	resources :issues（DELETE /issues/:id）

import (
	"errors"
	"html/template"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/urlroot"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// routesIssuesBulk は issues#bulk_edit / bulk_update / destroy のルートを登録する。
func (a *App) routesIssuesBulk(r Router) {
	// before_action :find_issues, :only => [:bulk_edit, :bulk_update, :destroy]
	// before_action :authorize, :except => [:index, :new, :create]
	// accept_api_auth ... :destroy
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		a.Handle(r, m, "/issues/bulk_edit", IssuesController, "bulk_edit", a.IssuesBulkEdit,
			Before(a.findIssues), Authorize())
	}
	for _, m := range []string{http.MethodPost, http.MethodPatch} {
		a.Handle(r, m, "/issues/bulk_update", IssuesController, "bulk_update", a.IssuesBulkUpdate,
			Before(a.findIssues), Authorize())
	}
	a.Handle(r, http.MethodDelete, "/issues", IssuesController, "destroy", a.IssuesDestroy,
		Before(a.findIssues), Authorize(), AcceptAPIAuth())
	a.Handle(r, http.MethodDelete, "/issues/{id}", IssuesController, "destroy", a.IssuesDestroy,
		Before(a.findIssues), Authorize(), AcceptAPIAuth())
}

// errBulkUnauthorized は bulk_edit 内の raise ::Unauthorized。
var errBulkUnauthorized = errors.New("bulk edit: unauthorized")

// sortIssueRows は @issues.sort!（Issue#<=>: root_id、同じツリーなら lft 順）。
func sortIssueRows(rows []*query.IssueRow) {
	slices.SortStableFunc(rows, func(x, y *query.IssueRow) int {
		if x.RootID != y.RootID {
			if x.RootID < y.RootID {
				return -1
			}
			return 1
		}
		return strings.Compare(x.HierPath, y.HierPath)
	})
}

// ---------------------------------------------------------------- bulk_edit

// bulkCFWarning は @values_by_custom_field の 1 項目。
type bulkCFWarning struct {
	CF  *customfield.CustomField
	IDs []int64
}

// bulkEditView は issues/bulk_edit のインスタンス変数。
type bulkEditView struct {
	l *issueLookup

	Copy   bool
	Notes  any
	Issues []*query.IssueRow
	models []*issueModel

	AllowedProjects   []*domain.Project
	TargetProject     *domain.Project
	Trackers          []*domain.Tracker
	AvailableStatuses []*domain.IssueStatus
	CustomFields      []*customfield.CustomField
	ValuesByCF        []*bulkCFWarning
	Assignables       []*issues.PrincipalRef
	Versions          []*repository.Version
	Categories        []*repository.IssueCategory
	SafeAttributes    []string

	AttachmentsPresent, SubtasksPresent, WatchersPresent bool

	// IssueParams は @issue_params（params[:issue]）。
	IssueParams *httpx.Params

	// SavedCount / Unsaved は bulk_update の失敗時（@saved_issues / @unsaved_issues）。
	SavedCount    int
	Unsaved       []*issues.Issue
	ErrorMessages []string
}

// buildBulkEdit は IssuesController#bulk_edit のインスタンス変数を組み立てる。
func (a *App) buildBulkEdit(c *Req, rows []*query.IssueRow) (*bulkEditView, error) {
	ctx := c.Ctx()
	l := a.newIssueLookup(c)
	l.addIssues(rows)
	l.markVisible(rows)
	sortIssueRows(rows)
	p := c.Params()
	v := &bulkEditView{l: l, Issues: rows, Copy: p.Present("copy")}
	if n, ok := p.Get("notes"); ok {
		v.Notes = n
	}
	v.models = l.models(rows)
	if l.err != nil {
		return nil, l.err
	}
	u := c.User
	if v.Copy {
		if !a.allowedToProjects(c, "copy_issues") {
			return nil, errBulkUnauthorized
		}
	} else {
		for _, m := range v.models {
			if !m.AttributesEditable() {
				return nil, errBulkUnauthorized
			}
		}
	}
	e := l.issuesEnv()
	// edited_issues = Issue.where(:id => @issues.map(&:id)).to_a（id 順）
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	slices.Sort(ids)
	var edited []*issues.Issue
	for _, id := range ids {
		iss, err := e.Find(ctx, id)
		if err != nil {
			return nil, err
		}
		if iss != nil {
			edited = append(edited, iss)
		}
	}
	// @values_by_custom_field（値のある CF → チケット id）
	cfIndex := map[int64]*bulkCFWarning{}
	for _, iss := range edited {
		vals, err := e.CustomFieldValues(ctx, iss)
		if err != nil {
			return nil, err
		}
		for _, cv := range vals {
			if !cv.Value.Present() {
				continue
			}
			w := cfIndex[cv.Field.ID]
			if w == nil {
				w = &bulkCFWarning{CF: cv.Field}
				cfIndex[cv.Field.ID] = w
				v.ValuesByCF = append(v.ValuesByCF, w)
			}
			w.IDs = append(w.IDs, iss.ID)
		}
	}
	// @allowed_projects = Issue.allowed_target_projects
	blank, err := e.NewBlank(ctx)
	if err != nil {
		return nil, err
	}
	if v.AllowedProjects, err = e.AllowedTargetProjects(ctx, blank, u, "*"); err != nil {
		return nil, err
	}
	hasIssue := p.Has("issue")
	ip := p.Map("issue")
	v.IssueParams = ip
	if hasIssue {
		want := paramToS(ip, "project_id")
		for _, pr := range v.AllowedProjects {
			if strconv.FormatInt(pr.ID, 10) == want {
				v.TargetProject = pr
				break
			}
		}
		if v.TargetProject != nil {
			for _, iss := range edited {
				if err := e.SetProject(ctx, iss, v.TargetProject, false); err != nil {
					return nil, err
				}
			}
		}
	}
	targets := c.Projects
	if v.TargetProject != nil {
		targets = []*domain.Project{v.TargetProject}
	}
	// @trackers = target_projects.map {|p| Issue.allowed_target_trackers(p)}.reduce(:&)
	for i, pr := range targets {
		tids, err := c.Authz().AllowedTargetTrackerIDs(ctx, pr, 0)
		if err != nil {
			return nil, err
		}
		var ts []*domain.Tracker
		for _, id := range tids {
			if t := l.tracker(id); t != nil {
				ts = append(ts, t)
			}
		}
		slices.SortStableFunc(ts, func(x, y *domain.Tracker) int { return x.Position - y.Position })
		if i == 0 {
			v.Trackers = ts
		} else {
			v.Trackers = slices.DeleteFunc(v.Trackers, func(t *domain.Tracker) bool {
				return !slices.ContainsFunc(ts, func(x *domain.Tracker) bool { return x.ID == t.ID })
			})
		}
	}
	if hasIssue {
		want := paramToS(ip, "tracker_id")
		for _, t := range v.Trackers {
			if strconv.FormatInt(t.ID, 10) == want {
				for _, iss := range edited {
					if err := e.SetTracker(ctx, iss, t); err != nil {
						return nil, err
					}
				}
				break
			}
		}
	}
	// @available_statuses（コピーでは既定のステータスになるので空）
	if !v.Copy {
		for i, iss := range edited {
			ss, err := e.NewStatusesAllowedTo(ctx, iss, u, false)
			if err != nil {
				return nil, err
			}
			if i == 0 {
				v.AvailableStatuses = slices.Clone(ss)
			} else {
				v.AvailableStatuses = slices.DeleteFunc(v.AvailableStatuses, func(s *domain.IssueStatus) bool {
					return !slices.ContainsFunc(ss, func(x *domain.IssueStatus) bool { return x.ID == s.ID })
				})
			}
		}
	}
	if hasIssue {
		want := paramToS(ip, "status_id")
		for _, s := range v.AvailableStatuses {
			if strconv.FormatInt(s.ID, 10) == want {
				for _, iss := range edited {
					if err := e.SetStatusID(ctx, iss, s.ID); err != nil {
						return nil, err
					}
				}
				break
			}
		}
	}
	// 変更後も値が残る CF は警告から除く
	for _, iss := range edited {
		vals, err := e.CustomFieldValues(ctx, iss)
		if err != nil {
			return nil, err
		}
		for _, cv := range vals {
			if w := cfIndex[cv.Field.ID]; w != nil && cv.Value.Present() {
				w.IDs = slices.DeleteFunc(w.IDs, func(id int64) bool { return id == iss.ID })
			}
		}
	}
	v.ValuesByCF = slices.DeleteFunc(v.ValuesByCF, func(w *bulkCFWarning) bool { return len(w.IDs) == 0 })
	// @custom_fields = edited_issues.map(&:editable_custom_fields).reduce(:&).select(bulk_edit_supported)
	for i, iss := range edited {
		cfs, err := e.EditableCustomFields(ctx, iss, u)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			v.CustomFields = slices.Clone(cfs)
		} else {
			v.CustomFields = slices.DeleteFunc(v.CustomFields, func(f *customfield.CustomField) bool {
				return !slices.ContainsFunc(cfs, func(x *customfield.CustomField) bool { return x.ID == f.ID })
			})
		}
	}
	v.CustomFields = slices.DeleteFunc(v.CustomFields, func(f *customfield.CustomField) bool { return !f.Format().BulkEditSupported })
	// @assignables = target_projects.map(&:assignable_users).reduce(:&)
	for i, pr := range targets {
		us, err := e.ProjectAssignableUsers(ctx, pr, nil)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			v.Assignables = us
		} else {
			v.Assignables = slices.DeleteFunc(v.Assignables, func(x *issues.PrincipalRef) bool {
				return !slices.ContainsFunc(us, func(y *issues.PrincipalRef) bool { return x.ID == y.ID })
			})
		}
	}
	// @versions = target_projects.map {|p| p.shared_versions.open}.reduce(:&)
	for i, pr := range targets {
		vs, err := repository.VersionsWhere(ctx, a.DB, repository.SharedVersionsCondition(pr)+" AND versions.status = 'open'")
		if err != nil {
			return nil, err
		}
		if i == 0 {
			v.Versions = vs
		} else {
			v.Versions = slices.DeleteFunc(v.Versions, func(x *repository.Version) bool {
				return !slices.ContainsFunc(vs, func(y *repository.Version) bool { return x.ID == y.ID })
			})
		}
	}
	repository.SortVersions(v.Versions)
	// @categories = target_projects.map {|p| p.issue_categories}.reduce(:&)
	for i, pr := range targets {
		cs := l.projectCategories(pr.ID)
		if i == 0 {
			v.Categories = cs
		} else {
			v.Categories = slices.DeleteFunc(v.Categories, func(x *repository.IssueCategory) bool {
				return !slices.ContainsFunc(cs, func(y *repository.IssueCategory) bool { return x.ID == y.ID })
			})
		}
	}
	if v.Copy {
		if a.Settings.String("copy_attachments_on_issue_copy") == "ask" {
			for _, r := range rows {
				if len(l.issueAttachments(r.ID)) > 0 {
					v.AttachmentsPresent = true
					break
				}
			}
		}
		for _, r := range rows {
			if l.hasChildren(r.ID) {
				v.SubtasksPresent = true
				break
			}
		}
		if a.allowedToProjects(c, "add_issue_watchers") {
			for _, m := range v.models {
				if m.I == nil {
					continue
				}
				ws, err := e.WatcherIDs(ctx, m.I)
				if err != nil {
					return nil, err
				}
				if len(ws) > 0 {
					v.WatchersPresent = true
					break
				}
			}
		}
	}
	// @safe_attributes = edited_issues.map(&:safe_attribute_names).reduce(:&)
	for i, iss := range edited {
		names, err := e.SafeAttributeNames(ctx, iss, u)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			v.SafeAttributes = names
		} else {
			v.SafeAttributes = slices.DeleteFunc(v.SafeAttributes, func(n string) bool { return !slices.Contains(names, n) })
		}
	}
	if l.err != nil {
		return nil, l.err
	}
	return v, nil
}

// paramToS は params[key].to_s（配列・ハッシュは空文字列扱い）。
func paramToS(p *httpx.Params, key string) string {
	v, ok := p.Get(key)
	if !ok {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return rails.ToS(v)
}

// IssuesBulkEdit は IssuesController#bulk_edit（GET / POST /issues/bulk_edit(.js)）。
func (a *App) IssuesBulkEdit(c *Req) {
	rows, _ := c.local(ctxIssues).([]*query.IssueRow)
	v, err := a.buildBulkEdit(c, rows)
	if errors.Is(err, errBulkUnauthorized) {
		c.DenyAccess()
		return
	}
	if err != nil {
		a.internalError(c, "bulk edit", err)
		return
	}
	a.renderBulkEdit(c, v)
}

// renderBulkEdit は bulk_edit の描画（.js は bulk_edit.js.erb: #content を差し替える）。
func (a *App) renderBulkEdit(c *Req, v *bulkEditView) {
	data := map[string]any{"B": v}
	if httpx.Format(c.R) == "js" {
		if (c.R.Method == http.MethodGet || c.R.Method == http.MethodHead) && !httpx.IsXHR(c.R) {
			httpx.HeadAs(c.W, c.R, http.StatusUnprocessableEntity, "html")
			c.Halt()
			return
		}
		out, err := a.Views.Render(c.ViewContext(), "issues/bulk_edit", data, view.RenderOptions{Format: "html", Layout: view.NoLayout})
		if err != nil {
			a.internalError(c, "bulk_edit.js", err)
			return
		}
		c.WriteJS("$('#content').html('" + rails.EscapeJavascriptString(string(out)) + "');\n")
		return
	}
	c.Render("issues/bulk_edit", data)
}

// ---------------------------------------------------------------- bulk_edit テンプレート用

// Heading は見出し。
func (v *bulkEditView) Heading() string {
	if v.Copy {
		return v.l.L("button_copy")
	}
	return v.l.L("label_bulk_edit_selected_issues")
}

// ShowErrors は @saved_issues && @unsaved_issues.present?。
func (v *bulkEditView) ShowErrors() bool { return len(v.Unsaved) > 0 }

// FailedMessage は l(:notice_failed_to_save_issues, ...)。
func (v *bulkEditView) FailedMessage() string {
	return v.l.L("notice_failed_to_save_issues", map[string]any{"count": len(v.Unsaved), "total": v.SavedCount,
		"ids": unsavedIDs(v.Unsaved)})
}

func unsavedIDs(us []*issues.Issue) string {
	var s []string
	for _, i := range us {
		s = append(s, "#"+strconv.FormatInt(i.ID, 10))
	}
	return strings.Join(s, ", ")
}

// IssueLinks は @issues の link_to_issue。
func (v *bulkEditView) IssueLinks() []template.HTML {
	var out []template.HTML
	for _, r := range v.Issues {
		out = append(out, v.l.linkToIssue(r, redmine.LinkToIssueOptions{}))
	}
	return out
}

// IDs は hidden の ids[]。
func (v *bulkEditView) IDs() []int64 {
	var out []int64
	for _, r := range v.Issues {
		out = append(out, r.ID)
	}
	return out
}

// Safe は @safe_attributes.include?(name)。
func (v *bulkEditView) Safe(name string) bool { return slices.Contains(v.SafeAttributes, name) }

// Param は @issue_params[name]。
func (v *bulkEditView) Param(name string) any {
	x, _ := v.IssueParams.Get(name)
	return x
}

// ParamIs は @issue_params[name] == value。
func (v *bulkEditView) ParamIs(name, value string) bool {
	x, ok := v.IssueParams.Get(name)
	if !ok {
		return false
	}
	s, isStr := x.(string)
	return isStr && s == value
}

// OnchangeJS は updateBulkEditFrom('/issues/bulk_edit.js')。
func (v *bulkEditView) OnchangeJS() string {
	return "updateBulkEditFrom('" + rails.EscapeJavascriptString(urlroot.Path("/issues/bulk_edit.js")) + "')"
}

func noChangeOption(l *issueLookup) template.HTML {
	return rails.ContentTag("option", l.L("label_no_change_option"), rails.NewHash("value", ""))
}

// ProjectOptions は project_tree_options_for_select(@allowed_projects, :include_blank => ..., :selected => @target_project)。
func (v *bulkEditView) ProjectOptions() template.HTML {
	var s template.HTML
	// (!@copy || (@projects & @allowed_projects == @projects))
	includeBlank := !v.Copy
	if v.Copy {
		includeBlank = true
		for _, p := range v.l.c.Projects {
			if !slices.ContainsFunc(v.AllowedProjects, func(x *domain.Project) bool { return x.ID == p.ID }) {
				includeBlank = false
			}
		}
	}
	if includeBlank {
		s = noChangeOption(v.l)
	}
	var sel int64
	if v.TargetProject != nil {
		sel = v.TargetProject.ID
	}
	return s + projectTreeOptions(v.l, v.AllowedProjects, sel)
}

// TrackerOptions は (No change) + options_from_collection_for_select(@trackers, :id, :name, @issue_params[:tracker_id])。
func (v *bulkEditView) TrackerOptions() template.HTML {
	var items []any
	for _, t := range v.Trackers {
		items = append(items, []any{t.Name, t.ID})
	}
	return noChangeOption(v.l) + rails.OptionsForSelect(items, v.Param("tracker_id"))
}

// StatusOptions は (No change) + @available_statuses。
func (v *bulkEditView) StatusOptions() template.HTML {
	var items []any
	for _, s := range v.AvailableStatuses {
		items = append(items, []any{s.Name, s.ID})
	}
	return noChangeOption(v.l) + rails.OptionsForSelect(items, v.Param("status_id"))
}

// PriorityOptions は (No change) + IssuePriority.active。
func (v *bulkEditView) PriorityOptions() template.HTML {
	v.l.loadMasters()
	var items []any
	for _, p := range v.l.prioOrd {
		if p.Active {
			items = append(items, []any{p.Name, p.ID})
		}
	}
	return noChangeOption(v.l) + rails.OptionsForSelect(items, v.Param("priority_id"))
}

// AssigneeOptions は (No change) + nobody + principals_options_for_select(@assignables, @issue_params[:assigned_to_id])。
func (v *bulkEditView) AssigneeOptions() template.HTML {
	l := v.l
	s := noChangeOption(l) + rails.ContentTag("option", l.L("label_nobody"), rails.NewHash("value", "none", "selected", v.ParamIs("assigned_to_id", "none")))
	sel := paramToS(v.IssueParams, "assigned_to_id")
	var b strings.Builder
	cur := l.c.User
	if cur.Logged() && slices.ContainsFunc(v.Assignables, func(p *issues.PrincipalRef) bool { return p.ID == cur.ID }) {
		b.WriteString(string(rails.ContentTag("option", "<< "+l.L("label_me")+" >>", rails.NewHash("value", cur.ID))))
	}
	var users, groups strings.Builder
	for _, p := range v.Assignables {
		attr := ""
		if strconv.FormatInt(p.ID, 10) == sel {
			attr = ` selected="selected"`
		}
		name := l.principalName(l.principal(p.ID))
		opt := `<option value="` + strconv.FormatInt(p.ID, 10) + `"` + attr + `>` + string(rails.H(name)) + `</option>`
		if p.Kind.IsGroup() {
			groups.WriteString(opt)
		} else {
			users.WriteString(opt)
		}
	}
	if groups.Len() == 0 {
		b.WriteString(users.String())
	} else {
		for _, g := range [][2]string{{l.L("label_user_plural"), users.String()}, {l.L("label_group_plural"), groups.String()}} {
			if g[1] != "" {
				b.WriteString(`<optgroup label="` + string(rails.H(g[0])) + `">` + g[1] + `</optgroup>`)
			}
		}
	}
	return s + template.HTML(b.String())
}

// CategoryOptions は (No change) + none + @categories。
func (v *bulkEditView) CategoryOptions() template.HTML {
	l := v.l
	var items []any
	for _, cat := range v.Categories {
		items = append(items, []any{cat.Name, cat.ID})
	}
	return noChangeOption(l) + rails.ContentTag("option", l.L("label_none"), rails.NewHash("value", "none", "selected", v.ParamIs("category_id", "none"))) +
		rails.OptionsForSelect(items, v.Param("category_id"))
}

// VersionOptions は (No change) + none + version_options_for_select(@versions.sort, @issue_params[:fixed_version_id])。
func (v *bulkEditView) VersionOptions() template.HTML {
	l := v.l
	var keys []string
	grouped := map[string][]any{}
	for _, ver := range v.Versions {
		if _, ok := grouped[ver.ProjectName]; !ok {
			keys = append(keys, ver.ProjectName)
		}
		grouped[ver.ProjectName] = append(grouped[ver.ProjectName], []any{ver.Name, ver.ID})
	}
	sel := v.Param("fixed_version_id")
	var opts template.HTML
	if len(keys) > 1 {
		var g []any
		for _, k := range keys {
			g = append(g, []any{k, grouped[k]})
		}
		opts = rails.GroupedOptionsForSelect(g, sel, nil)
	} else {
		var items []any
		if len(keys) == 1 {
			items = grouped[keys[0]]
		}
		opts = rails.OptionsForSelect(items, sel)
	}
	return noChangeOption(l) + rails.ContentTag("option", l.L("label_none"), rails.NewHash("value", "none", "selected", v.ParamIs("fixed_version_id", "none"))) + opts
}

// ShowLinkCopy は @copy && Setting.link_copied_issue == 'ask'。
func (v *bulkEditView) ShowLinkCopy() bool {
	return v.Copy && v.l.a.Settings.String("link_copied_issue") == "ask"
}

// LinkCopyChecked は params[:link_copy] != 0（文字列は数値の 0 と等しくないので常に true）。
func (v *bulkEditView) LinkCopyChecked() bool { return true }

// CopyChecked は params[name] != '0'。
func (v *bulkEditView) CopyChecked(name string) bool {
	x, ok := v.l.c.Params().Get(name)
	if !ok {
		return true
	}
	s, isStr := x.(string)
	return !isStr || s != "0"
}

// ShowCopyOptions は @copy && (@attachments_present || @subtasks_present || @watchers_present)。
func (v *bulkEditView) ShowCopyOptions() bool {
	return v.Copy && (v.AttachmentsPresent || v.SubtasksPresent || v.WatchersPresent)
}

// IsPrivateOptions は is_private の選択肢。
func (v *bulkEditView) IsPrivateOptions() template.HTML {
	l := v.l
	return noChangeOption(l) +
		rails.ContentTag("option", l.L("general_text_Yes"), rails.NewHash("value", "1", "selected", v.ParamIs("is_private", "1"))) +
		rails.ContentTag("option", l.L("general_text_No"), rails.NewHash("value", "0", "selected", v.ParamIs("is_private", "0")))
}

// Project は @project（親チケット欄の表示条件）。
func (v *bulkEditView) Project() *domain.Project { return v.l.c.Project }

// ParentAutocompleteJS は observeAutocompleteField('issue_parent_issue_id', auto_complete_issues_path(...))。
func (v *bulkEditView) ParentAutocompleteJS() string {
	u := "/issues/auto_complete?" + helper.ToQuery(rails.NewHash("project_id", v.l.c.Project.Identifier,
		"scope", v.l.a.Settings.String("cross_project_subtasks")))
	return "observeAutocompleteField('issue_parent_issue_id', '" + rails.EscapeJavascriptString(urlroot.Path(u)) + "')"
}

// ShowDoneRatio は @safe_attributes.include?('done_ratio') && Issue.use_field_for_done_ratio?。
func (v *bulkEditView) ShowDoneRatio() bool {
	return v.Safe("done_ratio") && v.l.a.Settings.String("issue_done_ratio") == "issue_field"
}

// DoneRatioOptions は options_for_select([[(No change), ”]] + (0..100).step(interval) ..., @issue_params[:done_ratio])。
func (v *bulkEditView) DoneRatioOptions() template.HTML {
	step, _ := strconv.Atoi(v.l.a.Settings.String("issue_done_ratio_interval"))
	if step <= 0 {
		step = 10
	}
	items := []any{[]any{v.l.L("label_no_change_option"), ""}}
	for r := 0; r <= 100; r += step {
		items = append(items, []any{strconv.Itoa(r) + " %", r})
	}
	return rails.OptionsForSelect(items, v.Param("done_ratio"))
}

// bulkCFRow は custom_field_tag_for_bulk_edit の 1 項目。
type bulkCFRow struct {
	CF        *customfield.CustomField
	Tag       template.HTML
	SplitHere bool
	Toolbar   bool
	FieldID   string
}

// HalfCustomFields は全幅でない CF（split_on の位置で SplitHere）。
func (v *bulkEditView) HalfCustomFields() []bulkCFRow {
	var cfs []*customfield.CustomField
	for _, cf := range v.CustomFields {
		if !cf.FullWidthLayout() {
			cfs = append(cfs, cf)
		}
	}
	splitOn := (len(cfs)+1)/2 - 1
	var out []bulkCFRow
	for i, cf := range cfs {
		out = append(out, bulkCFRow{CF: cf, Tag: v.customFieldTag(cf), SplitHere: i == splitOn})
	}
	return out
}

// FullCustomFields は全幅の CF。
func (v *bulkEditView) FullCustomFields() []bulkCFRow {
	var out []bulkCFRow
	for _, cf := range v.CustomFields {
		if cf.FullWidthLayout() {
			out = append(out, bulkCFRow{CF: cf, Tag: v.customFieldTag(cf), Toolbar: cf.FullTextFormatting(),
				FieldID: "issue_custom_field_values_" + strconv.FormatInt(cf.ID, 10)})
		}
	}
	return out
}

// CFPreviewURL は wikitoolbar_for のプレビュー URL。
func (v *bulkEditView) CFPreviewURL() string {
	if p := v.l.c.Project; p != nil {
		return "/issues/preview?" + helper.ToQuery(rails.NewHash("project_id", p.Identifier))
	}
	return "/preview/text"
}

// customFieldTag は custom_field_tag_for_bulk_edit('issue', custom_field, @issues, @issue_params[:custom_field_values][id])。
func (v *bulkEditView) customFieldTag(cf *customfield.CustomField) template.HTML {
	l := v.l
	css := cf.CSSClasses()
	var data any
	if cf.FullTextFormatting() {
		css += " wiki-edit"
		d := rails.NewHash("auto_complete", true)
		d.Update(listAutofillHash(l))
		data = d
	}
	var value any
	if m := v.IssueParams.Map("custom_field_values"); m != nil {
		if x, ok := m.Get(strconv.FormatInt(cf.ID, 10)); ok {
			if xs, isArr := x.([]any); isArr {
				var ss []string
				for _, e := range xs {
					ss = append(ss, rails.ToS(e))
				}
				value = ss
			} else {
				value = x
			}
		} else {
			value = nil
		}
	} else {
		value = nil
	}
	var objs []*customfield.Customized
	for _, r := range v.Issues {
		objs = append(objs, l.customized(r))
	}
	env := l.cfEnv(cf)
	env.CalendarFor = func(fieldID string) template.HTML {
		return rails.JavascriptTag("$(function() { $('#"+fieldID+"').addClass('date').datepickerFallback(datepickerOptions); });", nil)
	}
	env.ProjectUsers = func(projectID int64, roleIDs []int64) []customfield.Option {
		return l.projectUserOptions(projectID, roleIDs)
	}
	env.SharedVersions = func(projectID int64, statuses []string) []customfield.Option {
		return l.sharedVersionOptions(projectID, statuses)
	}
	id := "issue_custom_field_values_" + strconv.FormatInt(cf.ID, 10)
	name := "issue[custom_field_values][" + strconv.FormatInt(cf.ID, 10) + "]"
	return cf.Format().BulkEditTag(env, id, name, cf, objs, value, rails.NewHash("class", css, "data", data))
}

// listAutofillHash は list_autofill_data_attributes。
func listAutofillHash(l *issueLookup) *rails.Hash {
	f := l.a.Settings.String("text_formatting")
	if strings.TrimSpace(f) == "" {
		return rails.NewHash()
	}
	return rails.NewHash("controller", "list-autofill", "action", "beforeinput->list-autofill#handleBeforeInput",
		"list_autofill_text_formatting_param", f)
}

// NotesData は {:auto_complete => true}.merge(list_autofill_data_attributes)。
func (v *bulkEditView) NotesData() *rails.Hash {
	d := rails.NewHash("auto_complete", true)
	d.Update(listAutofillHash(v.l))
	return d
}

// ShowWatchersDataSources は User.current.allowed_to?(:add_issue_watchers, nil, global: true)。
func (v *bulkEditView) ShowWatchersDataSources() bool {
	return v.l.c.AllowedToGlobally(domain.Perm("add_issue_watchers"))
}

// WatchersDataSourcesJS は update_data_sources_for_auto_complete({users: watchers_autocomplete_for_mention_path(...)})。
func (v *bulkEditView) WatchersDataSourcesJS() template.HTML {
	u := "/watchers/autocomplete_for_mention?" + helper.ToQuery(rails.NewHash("object_id", v.IDs(), "object_type", "issue", "q", ""))
	return rails.JavascriptTag("rm.AutoComplete.dataSources = Object.assign(rm.AutoComplete.dataSources, JSON.parse('"+
		jsonForJS(map[string]string{"users": urlroot.Path(u)})+"'));", nil)
}

// FieldsCleared は @values_by_custom_field の表示（"name (n)" を ", " で連結）。
func (v *bulkEditView) FieldsCleared() template.HTML {
	var parts []string
	for _, w := range v.ValuesByCF {
		parts = append(parts, string(rails.ContentTag("span", w.CF.Name+" ("+strconv.Itoa(len(w.IDs))+")", nil)))
	}
	return template.HTML(strings.Join(parts, ", "))
}

// ---------------------------------------------------------------- bulk_update

// copyParamEnabled は link_copy? / copy_attachments?（Setting が ask なら param == '1'）。
func (a *App) copyParamEnabled(setting string, c *Req, param string) bool {
	switch a.Settings.String(setting) {
	case "yes":
		return true
	case "ask":
		return c.Params().String(param) == "1"
	}
	return false
}

// IssuesBulkUpdate は IssuesController#bulk_update（POST / PATCH /issues/bulk_update）。
func (a *App) IssuesBulkUpdate(c *Req) {
	rows, _ := c.local(ctxIssues).([]*query.IssueRow)
	p := c.Params()
	copyMode := p.Present("copy")
	attrs := issues.ParseBulkParams(issueParamsOf(p.Map("issue")))
	opts := issues.BulkOptions{
		Notes:          p.String("notes"),
		Copy:           copyMode,
		CopySubtasks:   p.String("copy_subtasks") == "1",
		CopyWatchers:   p.String("copy_watchers") == "1",
		CopyAttachment: a.copyParamEnabled("copy_attachments_on_issue_copy", c, "copy_attachments"),
		Link:           a.copyParamEnabled("link_copied_issue", c, "link_copy"),
	}
	e := a.writeIssuesEnv(c, a.DB)
	var list []*issues.Issue
	sortIssueRows(rows)
	for _, r := range rows {
		iss, err := e.Find(c.Ctx(), r.ID)
		if err != nil {
			a.internalError(c, "bulk update", err)
			return
		}
		if iss != nil {
			list = append(list, iss)
		}
	}
	res, err := e.BulkUpdate(c.Ctx(), list, attrs, opts, c.User)
	if errors.Is(err, issues.ErrUnauthorized) {
		c.DenyAccess()
		return
	}
	if err != nil {
		a.internalError(c, "bulk update", err)
		return
	}
	a.dispatchIssueNotifications(c, &res.SaveResult)
	processed := len(res.Saved) + len(res.Unsaved)
	if len(res.Unsaved) == 0 {
		if len(res.Saved) > 0 {
			c.Flash().SetNotice(c.L("notice_successful_update"))
		}
		if p.Has("follow") {
			if processed == 1 && len(res.Saved) == 1 {
				c.Redirect("/issues/" + strconv.FormatInt(res.Saved[0].ID, 10))
				return
			}
			var pid int64
			same := true
			for i, s := range res.Saved {
				if i == 0 {
					pid = s.ProjectID
				} else if s.ProjectID != pid {
					same = false
				}
			}
			if same && len(res.Saved) > 0 {
				if pr, err := repository.GetProject(c.Ctx(), a.DB, pid); err == nil {
					c.Redirect(issuesPath(pr))
					return
				}
			}
		}
		c.RedirectBackOrDefault(issuesPath(c.Project), false)
		return
	}
	// 失敗したチケットだけで bulk_edit を再描画する（@issues = Issue.visible.where(:id => unsaved)）
	var ids []int64
	for _, u := range res.Unsaved {
		ids = append(ids, u.ID)
	}
	m, err := repository.ReadIssuesByIDs(c.Ctx(), a.DB, ids)
	if err != nil {
		a.internalError(c, "bulk update", err)
		return
	}
	l := a.newIssueLookup(c)
	var visible []*query.IssueRow
	for _, id := range ids {
		if r := m[id]; r != nil {
			row := issueRowFromRead(r)
			if l.issueVisible(row) {
				visible = append(visible, row)
			}
		}
	}
	slices.SortFunc(visible, func(x, y *query.IssueRow) int { return int(x.ID - y.ID) })
	v, err := a.buildBulkEdit(c, visible)
	if errors.Is(err, errBulkUnauthorized) {
		c.DenyAccess()
		return
	}
	if err != nil {
		a.internalError(c, "bulk update", err)
		return
	}
	v.SavedCount = processed
	v.Unsaved = res.Unsaved
	v.ErrorMessages = bulkEditErrorMessages(c, res.Unsaved)
	c.Render("issues/bulk_edit", map[string]any{"B": v})
}

// bulkEditErrorMessages は bulk_edit_error_messages(items)（同じメッセージのチケットをまとめる）。
func bulkEditErrorMessages(c *Req, items []*issues.Issue) []string {
	var order []string
	byMsg := map[string][]string{}
	for _, it := range items {
		for _, msg := range it.Errors.FullMessages(issueErrorTranslator(c)) {
			if _, ok := byMsg[msg]; !ok {
				order = append(order, msg)
			}
			byMsg[msg] = append(byMsg[msg], "#"+strconv.FormatInt(it.ID, 10))
		}
	}
	var out []string
	for _, msg := range order {
		out = append(out, msg+": "+strings.Join(byMsg[msg], ", "))
	}
	return out
}

// issueErrorTranslator は Issue.human_attribute_name 用の翻訳関数（field_<attr> が無ければ属性名そのもの。
// カスタムフィールドのエラーは属性名が CF 名のため）。
func issueErrorTranslator(c *Req) domain.Translator {
	return func(key string, args ...any) string {
		if name, ok := strings.CutPrefix(key, "field_"); ok && c.Loc != nil && c.Loc.Bundle != nil && !c.Loc.Bundle.Exists(c.Loc.Lang, key) {
			return name
		}
		return c.L(key, args...)
	}
}

// ---------------------------------------------------------------- destroy

// destroyView は issues/destroy のインスタンス変数。
type destroyView struct {
	IDs          []int64
	Hours        float64
	HoursText    string
	ShowNullify  bool
	Project      *domain.Project
	ReassignTo   any
	AutoComplete string
}

// IssuesDestroy は IssuesController#destroy（DELETE /issues/:id、DELETE /issues?ids[]=...）。
func (a *App) IssuesDestroy(c *Req) {
	rows, _ := c.local(ctxIssues).([]*query.IssueRow)
	ctx := c.Ctx()
	l := a.newIssueLookup(c)
	l.addIssues(rows)
	// raise Unauthorized unless @issues.all?(&:deletable?)
	for _, r := range rows {
		if !l.model(r).Deletable() {
			if l.err != nil {
				a.internalError(c, "issue destroy", l.err)
				return
			}
			c.DenyAccess()
			return
		}
	}
	var ids []int64
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	e := a.writeIssuesEnv(c, a.DB)
	hours, all, err := e.TimeEntriesHours(ctx, ids)
	if err != nil {
		a.internalError(c, "issue destroy", err)
		return
	}
	api := httpx.IsAPIRequest(c.R)
	p := c.Params()
	todo := issues.TimeEntriesTodo(p.String("todo"))
	opts := issues.DestroyOptions{Todo: todo}
	if c.Project != nil {
		opts.ProjectID = c.Project.ID
	}
	if hours > 0 {
		switch todo {
		case issues.TimeEntriesDestroy, issues.TimeEntriesNullify:
		case issues.TimeEntriesReassign:
			opts.ReassignToID = customfield.RubyToI(p.String("reassign_to_id"))
			// 見えないチケットは「見つからない」扱いにする
			if t, err := e.Find(ctx, opts.ReassignToID); err != nil {
				a.internalError(c, "issue destroy", err)
				return
			} else if t != nil {
				ok, err := e.Visible(ctx, t, c.User)
				if err != nil {
					a.internalError(c, "issue destroy", err)
					return
				}
				if !ok {
					opts.ReassignToID = 0
				}
			}
		default:
			if !api {
				a.renderIssuesDestroy(c, rows, hours)
				return
			}
			// API は工数ごと削除する（has_many :time_entries, :dependent => :destroy）
			opts.Todo = issues.TimeEntriesDestroy
		}
	}
	// 削除される添付（コミット後にディスクから消す）
	var atts []*domain.Attachment
	for _, id := range all {
		list, err := repository.ContainerAttachmentList(ctx, a.DB, domain.AttachmentContainerIssue, id)
		if err != nil {
			a.internalError(c, "issue destroy", err)
			return
		}
		atts = append(atts, list...)
	}
	res, err := e.DestroyIssues(ctx, ids, opts)
	switch {
	case errors.Is(err, issues.ErrTimeEntryIssueRequired):
		c.Flash().Now("error", c.L("field_issue")+" "+c.L("activerecord.errors.messages.blank"))
		a.renderIssuesDestroy(c, rows, hours)
		return
	case errors.Is(err, issues.ErrReassignTargetNotFound):
		c.Flash().Now("error", c.L("error_issue_not_found_in_project"))
		a.renderIssuesDestroy(c, rows, hours)
		return
	case errors.Is(err, issues.ErrReassignToDeleted):
		c.Flash().Now("error", c.L("error_cannot_reassign_time_entries_to_an_issue_about_to_be_deleted"))
		a.renderIssuesDestroy(c, rows, hours)
		return
	case err != nil:
		a.internalError(c, "issue destroy", err)
		return
	}
	if len(atts) > 0 && a.AttachmentStore != nil {
		if err := a.AttachmentStore.DeleteFromDisk(ctx, a.DB, atts...); err != nil {
			a.logger().Error("delete attachments from disk", "err", err)
		}
	}
	a.dispatchIssueNotifications(c, res)
	if api {
		c.RenderAPIOK()
		return
	}
	c.Flash().SetNotice(c.L("notice_successful_delete"))
	c.RedirectBackOrDefault(issuesPath(c.Project), false)
}

// renderIssuesDestroy は destroy.html.erb（工数の扱いの確認画面）。
func (a *App) renderIssuesDestroy(c *Req, rows []*query.IssueRow, hours float64) {
	if httpx.IsAPIRequest(c.R) {
		// flash.now のエラーで return した場合の API（destroy.api のテンプレートが無い: MissingExactTemplate → 406）
		c.head(http.StatusNotAcceptable)
		return
	}
	v := &destroyView{Hours: hours, HoursText: c.Loc.FormatHours(hours), Project: c.Project,
		ShowNullify: !slices.Contains(a.Settings.Strings("timelog_required_fields"), "issue_id")}
	for _, r := range rows {
		v.IDs = append(v.IDs, r.ID)
	}
	if x, ok := c.Params().Get("reassign_to_id"); ok {
		v.ReassignTo = x
	}
	if c.Project != nil {
		v.AutoComplete = "observeAutocompleteField('reassign_to_id', '" +
			rails.EscapeJavascriptString(urlroot.Path("/issues/auto_complete?"+helper.ToQuery(rails.NewHash("project_id", c.Project.Identifier)))) + "')"
	}
	// form_tag({}, :method => :delete) の action は現在のルート（/issues/:id または /issues）
	action := "/issues"
	if id := c.Params().String("id"); id != "" && strings.HasPrefix(routePath(c.R), "/issues/") {
		action = "/issues/" + id
	}
	c.Render("issues/destroy", map[string]any{"D": v, "Action": action})
}
