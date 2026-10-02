package handler

// ContextMenusController#issues（チケット一覧・詳細の右クリックメニュー。context_menus/issues）。

import (
	"html/template"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// routesContextMenusIssues は match '/issues/context_menu', :to => 'context_menus#issues', :via => [:get, :post]。
func (a *App) routesContextMenusIssues(r Router) {
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		// before_action :find_issues, :only => :issues（ContextMenusController は authorize しない）
		a.Handle(r, m, "/issues/context_menu", ContextMenusController, "issues", a.ContextMenusIssues, Before(a.findIssues))
	}
}

const ctxIssues = "issues"

// findIssues は ApplicationController#find_issues（params[:id] || params[:ids]）。
func (a *App) findIssues(c *Req) {
	p := c.Params()
	v, ok := p.Get("id")
	if !ok || v == nil {
		v, _ = p.Get("ids")
	}
	ids := idsFromParam(v)
	if len(ids) == 0 {
		c.Render404("")
		return
	}
	m, err := repository.ReadIssuesByIDs(c.Ctx(), a.DB, ids)
	if err != nil {
		a.internalError(c, "find issues", err)
		return
	}
	var rows []*query.IssueRow
	for _, id := range ids {
		if r := m[id]; r != nil {
			rows = append(rows, issueRowFromRead(r))
		}
	}
	if len(rows) == 0 {
		c.Render404("")
		return
	}
	// 並びは Issue.where(:id => ...) の既定（id 順）
	slices.SortFunc(rows, func(x, y *query.IssueRow) int { return int(x.ID - y.ID) })
	var projects []*domain.Project
	for _, r := range rows {
		pr, err := repository.GetProject(c.Ctx(), a.DB, r.ProjectID)
		if err != nil {
			a.internalError(c, "find issues project", err)
			return
		}
		ok, err := c.Authz().IssueVisible(c.Ctx(), &domain.Issue{ID: r.ID, ProjectID: r.ProjectID, TrackerID: r.TrackerID,
			StatusID: r.StatusID, AuthorID: r.AuthorID, AssignedToID: r.AssignedToID, IsPrivate: r.IsPrivate}, pr)
		if err != nil {
			a.internalError(c, "issue visible", err)
			return
		}
		if !ok {
			c.DenyAccess()
			return
		}
		if !slices.ContainsFunc(projects, func(x *domain.Project) bool { return x.ID == pr.ID }) {
			projects = append(projects, pr)
		}
	}
	c.Projects = projects
	if len(projects) == 1 {
		c.Project = projects[0]
	}
	c.setLocal(ctxIssues, rows)
}

// issueContextMenu は context_menus/issues のデータ。
type issueContextMenu struct {
	l      *issueLookup
	Models []*issueModel
	Issue  *issueModel
	IDs    []int64
	Back   any

	AllowedStatuses []*domain.IssueStatus
	Trackers        []*domain.Tracker
	Versions        []*repository.Version
	Assignables     []*domain.User
	Priorities      []*domain.Enumeration
	Categories      []*repository.IssueCategory
	CustomFields    []cmCustomField
	SafeAttributes  []string

	CanEdit, CanLogTime, CanCopy, CanAddWatchers, CanDelete, CanAddSubtask bool
	IncludeDelete                                                          bool
	Columns                                                                any
}

type cmCustomField struct {
	CF      *customfield.CustomField
	Options []customfield.Option
}

// ContextMenusIssues は ContextMenusController#issues。
func (a *App) ContextMenusIssues(c *Req) {
	rows, _ := c.local(ctxIssues).([]*query.IssueRow)
	l := a.newIssueLookup(c)
	l.addIssues(rows)
	l.markVisible(rows)
	cm := &issueContextMenu{l: l}
	for _, r := range rows {
		cm.Models = append(cm.Models, l.model(r))
		cm.IDs = append(cm.IDs, r.ID)
	}
	slices.Sort(cm.IDs)
	if len(cm.Models) == 1 {
		cm.Issue = cm.Models[0]
	}
	// @allowed_statuses = @issues.map(&:new_statuses_allowed_to).reduce(:&)
	for i, m := range cm.Models {
		ss := m.NewStatusesAllowed()
		if i == 0 {
			cm.AllowedStatuses = slices.Clone(ss)
		} else {
			cm.AllowedStatuses = slices.DeleteFunc(cm.AllowedStatuses, func(s *domain.IssueStatus) bool {
				return !slices.ContainsFunc(ss, func(x *domain.IssueStatus) bool { return x.ID == s.ID })
			})
		}
	}
	cm.CanEdit = true
	cm.CanDelete = true
	for _, m := range cm.Models {
		if !m.AttributesEditable() {
			cm.CanEdit = false
		}
		if !m.Deletable() {
			cm.CanDelete = false
		}
	}
	if cm.Issue != nil {
		cm.CanLogTime = cm.Issue.TimeLoggable()
		cm.CanAddSubtask = !cm.Issue.Closed() && c.Project != nil && c.AllowedTo(domain.Perm("manage_subtasks"), c.Project)
	}
	cm.CanCopy = a.allowedToProjects(c, "copy_issues") && l.allowedTargetProjectsAny()
	cm.CanAddWatchers = a.allowedToProjects(c, "add_issue_watchers")
	// @assignables = @issues.map(&:assignable_users).reduce(:&)
	for i, m := range cm.Models {
		us := m.AssignableUsers()
		if i == 0 {
			cm.Assignables = slices.Clone(us)
		} else {
			cm.Assignables = slices.DeleteFunc(cm.Assignables, func(u *domain.User) bool {
				return !slices.ContainsFunc(us, func(x *domain.User) bool { return x.ID == u.ID })
			})
		}
	}
	// @trackers = @projects.map {|p| Issue.allowed_target_trackers(p)}.reduce(:&)
	for i, p := range c.Projects {
		ids, err := c.Authz().AllowedTargetTrackerIDs(c.Ctx(), p, 0)
		l.fail(err)
		var ts []*domain.Tracker
		for _, id := range ids {
			ts = append(ts, l.tracker(id))
		}
		slices.SortStableFunc(ts, func(x, y *domain.Tracker) int { return x.Position - y.Position })
		if i == 0 {
			cm.Trackers = ts
		} else {
			cm.Trackers = slices.DeleteFunc(cm.Trackers, func(t *domain.Tracker) bool {
				return !slices.ContainsFunc(ts, func(x *domain.Tracker) bool { return x.ID == t.ID })
			})
		}
	}
	// @versions = @projects.map {|p| p.shared_versions.open}.reduce(:&)
	for i, p := range c.Projects {
		vs, err := repository.VersionsWhere(c.Ctx(), a.DB, repository.SharedVersionsCondition(p)+" AND versions.status = 'open'")
		l.fail(err)
		if i == 0 {
			cm.Versions = vs
		} else {
			cm.Versions = slices.DeleteFunc(cm.Versions, func(v *repository.Version) bool {
				return !slices.ContainsFunc(vs, func(x *repository.Version) bool { return x.ID == v.ID })
			})
		}
	}
	repository.SortVersions(cm.Versions)
	// @priorities = IssuePriority.active.reverse
	l.loadMasters()
	for i := len(l.prioOrd) - 1; i >= 0; i-- {
		if l.prioOrd[i].Active {
			cm.Priorities = append(cm.Priorities, l.prioOrd[i])
		}
	}
	if c.Project != nil {
		cm.Categories = l.projectCategories(c.Project.ID)
	}
	// @back = back_url
	if b := backURLParam(c); b != "" {
		cm.Back = b
		cm.IncludeDelete = includeDeleteFor(b)
	}
	if v, ok := c.Params().Get("c"); ok {
		cm.Columns = v
	}
	if cm.CanEdit {
		cm.CustomFields = a.contextMenuCustomFields(c, l, cm)
	}
	// @safe_attributes = @issues.map(&:safe_attribute_names).reduce(:&)
	for i, m := range cm.Models {
		names, err := m.env().SafeAttributeNames(c.Ctx(), m.I, c.User)
		l.fail(err)
		if i == 0 {
			cm.SafeAttributes = names
		} else {
			cm.SafeAttributes = slices.DeleteFunc(cm.SafeAttributes, func(n string) bool { return !slices.Contains(names, n) })
		}
	}
	if l.err != nil {
		a.internalError(c, "context menu", l.err)
		return
	}
	c.Render("context_menus/issues", map[string]any{"CM": cm}, RenderOptions{Layout: view.NoLayout})
}

// allowedToProjects は User.current.allowed_to?(perm, @projects)。
func (a *App) allowedToProjects(c *Req, perm string) bool {
	if len(c.Projects) == 0 {
		return false
	}
	ok, err := c.Authz().AllowedToProjects(c.Ctx(), domain.Perm(perm), c.Projects, nil)
	if err != nil {
		a.logger().Error("allowed to projects", "err", err)
		return false
	}
	return ok
}

// backURLParam は back_url（params[:back_url] か Referer）。
func backURLParam(c *Req) string {
	if s := c.Params().String("back_url"); s != "" {
		return s
	}
	return c.R.Referer()
}

var includeDeleteRe = regexp.MustCompile(`\A(?:/projects/[^/?#]+)?/issues(?:/gantt|/calendar)?(?:\.[a-z]+)?(?:[?#].*)?\z`)

// includeDeleteFor は back_url が issues#index / gantts#show / calendars#show か。
func includeDeleteFor(back string) bool {
	if u := strings.TrimPrefix(back, "http://"); u != back {
		if i := strings.Index(u, "/"); i >= 0 {
			back = u[i:]
		}
	}
	return includeDeleteRe.MatchString(back)
}

// contextMenuCustomFields は @options_by_custom_field。
func (a *App) contextMenuCustomFields(c *Req, l *issueLookup, cm *issueContextMenu) []cmCustomField {
	var fields []*customfield.CustomField
	for i, m := range cm.Models {
		cfs, err := m.env().EditableCustomFields(c.Ctx(), m.I, c.User)
		l.fail(err)
		if i == 0 {
			fields = cfs
		} else {
			fields = slices.DeleteFunc(fields, func(f *customfield.CustomField) bool {
				return !slices.ContainsFunc(cfs, func(x *customfield.CustomField) bool { return x.ID == f.ID })
			})
		}
	}
	var objs []*customfield.Customized
	for _, p := range c.Projects {
		objs = append(objs, &customfield.Customized{Kind: "project", ID: p.ID, ProjectID: p.ID, ProjectIdentifier: p.Identifier})
	}
	var out []cmCustomField
	for _, f := range fields {
		if f.Multiple || !f.Format().BulkEditSupported {
			continue
		}
		env := l.cfEnv(f)
		env.ProjectUsers = func(projectID int64, roleIDs []int64) []customfield.Option {
			return l.projectUserOptions(projectID, roleIDs)
		}
		env.SharedVersions = func(projectID int64, statuses []string) []customfield.Option {
			return l.sharedVersionOptions(projectID, statuses)
		}
		opts := customfield.PossibleValuesOptionsFor(env, f, objs)
		if len(opts) > 0 {
			out = append(out, cmCustomField{CF: f, Options: opts})
		}
	}
	return out
}

// ---------------------------------------------------------------- テンプレート用

// Safe は @safe_attributes.include?(name)。
func (cm *issueContextMenu) Safe(name string) bool { return slices.Contains(cm.SafeAttributes, name) }

// bulkUpdatePath は _bulk_update_issues_path(@issue, :ids => @issue_ids, :issue => attrs, :back_url => @back)。
func (cm *issueContextMenu) bulkUpdatePath(attrs *rails.Hash) string {
	path := "/issues/bulk_update"
	if cm.Issue != nil {
		path = "/issues/" + strconv.FormatInt(cm.Issue.Row.ID, 10)
	}
	h := rails.NewHash("ids", cm.IDs, "issue", attrs, "back_url", cm.Back)
	if qs := helper.ToQuery(h); qs != "" {
		path += "?" + qs
	}
	return path
}

// link は context_menu_link(name, url, options)。
func (cm *issueContextMenu) link(name any, url string, class any, method any, selected, disabled bool, data *rails.Hash) template.HTML {
	label := name
	var css []string
	if class != nil {
		css = append(css, rails.ToS(class))
	}
	opts := rails.NewHash()
	if method != nil {
		opts.Set("method", method)
	}
	if data != nil {
		opts.Set("data", data)
	}
	if selected {
		css = append(css, "icon disabled")
		disabled = true
		label = cm.l.icon("checked", name)
	}
	if disabled {
		opts.Delete("method")
		opts.Delete("data")
		opts.Set("onclick", "return false;")
		css = append(css, "disabled")
		url = "#"
	}
	h := rails.NewHash()
	for _, e := range opts.Entries() {
		h.Set(e.Key, e.Value)
	}
	h.Set("class", classNames(css))
	return rails.LinkTo(label, url, h)
}

func (cm *issueContextMenu) attrLink(name any, attr string, value any, selected, disabled bool) template.HTML {
	return cm.link(name, cm.bulkUpdatePath(rails.NewHash(attr, value)), nil, "patch", selected, disabled, nil)
}

// EditLink は編集（1 件）か一括編集のリンク。
func (cm *issueContextMenu) EditLink() template.HTML {
	if cm.Issue != nil {
		return cm.link(cm.l.icon("edit", cm.l.L("button_edit")), "/issues/"+strconv.FormatInt(cm.Issue.Row.ID, 10)+"/edit", "icon icon-edit", nil, false, !cm.CanEdit, nil)
	}
	return cm.link(cm.l.icon("edit", cm.l.L("label_bulk_edit")), "/issues/bulk_edit?"+helper.ToQuery(rails.NewHash("ids", cm.IDs)), "icon icon-edit", nil, false, !cm.CanEdit, nil)
}

// StatusLink / TrackerLink / ... は各サブメニューの項目。
func (cm *issueContextMenu) StatusLink(s *domain.IssueStatus) template.HTML {
	return cm.attrLink(s.Name, "status_id", s.ID, cm.Issue != nil && cm.Issue.Row.StatusID == s.ID, !cm.CanEdit)
}

func (cm *issueContextMenu) TrackerLink(t *domain.Tracker) template.HTML {
	return cm.attrLink(t.Name, "tracker_id", t.ID, cm.Issue != nil && cm.Issue.Row.TrackerID == t.ID, !cm.CanEdit)
}

func (cm *issueContextMenu) PriorityLink(p *domain.Enumeration) template.HTML {
	return cm.attrLink(p.Name, "priority_id", p.ID, cm.Issue != nil && cm.Issue.Row.PriorityID == p.ID, !cm.CanEdit || cm.anyDerived("priority"))
}

func (cm *issueContextMenu) anyDerived(attr string) bool {
	for _, m := range cm.Models {
		if m.I == nil {
			continue
		}
		var ok bool
		var err error
		switch attr {
		case "priority":
			ok, err = m.env().PriorityDerived(cm.l.ctx, m.I)
		case "done_ratio":
			ok, err = m.env().DoneRatioDerived(cm.l.ctx, m.I)
		}
		cm.l.fail(err)
		if ok {
			return true
		}
	}
	return false
}

func (cm *issueContextMenu) VersionLink(v *repository.Version) template.HTML {
	return cm.attrLink(cm.l.formatVersionName(v), "fixed_version_id", v.ID,
		cm.Issue != nil && cm.Issue.Row.FixedVersionID != nil && *cm.Issue.Row.FixedVersionID == v.ID, !cm.CanEdit)
}

func (cm *issueContextMenu) VersionNoneLink() template.HTML {
	return cm.attrLink(cm.l.L("label_none"), "fixed_version_id", "none", cm.Issue != nil && cm.Issue.Row.FixedVersionID == nil, !cm.CanEdit)
}

// AssignToMe は @assignables.include?(User.current)。
func (cm *issueContextMenu) AssignToMe() bool {
	u := cm.l.c.User
	return u.Logged() && slices.ContainsFunc(cm.Assignables, func(x *domain.User) bool { return x.ID == u.ID })
}

func (cm *issueContextMenu) MeLink() template.HTML {
	return cm.attrLink("<< "+cm.l.L("label_me")+" >>", "assigned_to_id", cm.l.c.User.ID, false, !cm.CanEdit)
}

func (cm *issueContextMenu) AssigneeLink(u *domain.User) template.HTML {
	return cm.attrLink(cm.l.principalName(u), "assigned_to_id", u.ID,
		cm.Issue != nil && cm.Issue.Row.AssignedToID != nil && *cm.Issue.Row.AssignedToID == u.ID, !cm.CanEdit)
}

func (cm *issueContextMenu) NobodyLink() template.HTML {
	return cm.attrLink(cm.l.L("label_nobody"), "assigned_to_id", "none", cm.Issue != nil && cm.Issue.Row.AssignedToID == nil, !cm.CanEdit)
}

func (cm *issueContextMenu) CategoryLink(cat *repository.IssueCategory) template.HTML {
	return cm.attrLink(cat.Name, "category_id", cat.ID,
		cm.Issue != nil && cm.Issue.Row.CategoryID != nil && *cm.Issue.Row.CategoryID == cat.ID, !cm.CanEdit)
}

func (cm *issueContextMenu) CategoryNoneLink() template.HTML {
	return cm.attrLink(cm.l.L("label_none"), "category_id", "none", cm.Issue != nil && cm.Issue.Row.CategoryID == nil, !cm.CanEdit)
}

// ShowDoneRatio は @safe_attributes.include?('done_ratio') && Issue.use_field_for_done_ratio?。
func (cm *issueContextMenu) ShowDoneRatio() bool {
	return cm.Safe("done_ratio") && cm.l.a.Settings.String("issue_done_ratio") == "issue_field"
}

// DoneRatioLinks は (0..100).step(interval) の項目。
func (cm *issueContextMenu) DoneRatioLinks() []template.HTML {
	step, _ := strconv.Atoi(cm.l.a.Settings.String("issue_done_ratio_interval"))
	if step <= 0 {
		step = 10
	}
	derived := cm.anyDerived("done_ratio")
	var out []template.HTML
	for p := 0; p <= 100; p += step {
		out = append(out, cm.attrLink(strconv.Itoa(p)+"%", "done_ratio", p, cm.Issue != nil && cm.Issue.Row.DoneRatio == p, !cm.CanEdit || derived))
	}
	return out
}

// CustomFieldLink は bulk_update_custom_field_context_menu_link(field, text, value)。
func (cm *issueContextMenu) CustomFieldLink(f *customfield.CustomField, text, value string) template.HTML {
	selected := false
	if cm.Issue != nil && cm.Issue.I != nil {
		v, _, err := cm.Issue.env().CustomFieldValue(cm.l.ctx, cm.Issue.I, f.ID)
		cm.l.fail(err)
		selected = !v.IsArray() && !v.IsNil() && v.String() == value
	}
	attrs := rails.NewHash("custom_field_values", rails.NewHash(strconv.FormatInt(f.ID, 10), value))
	return cm.link(rails.H(text), cm.bulkUpdatePath(attrs), nil, "patch", selected, false, nil)
}

// OptionValue は value || text。
func (cm *issueContextMenu) OptionValue(o customfield.Option) string {
	if o.Value != "" {
		return o.Value
	}
	return o.Label
}

// AddWatchersLink は ウォッチャーの追加リンク。
func (cm *issueContextMenu) AddWatchersLink() template.HTML {
	u := "/watchers/new?" + helper.ToQuery(rails.NewHash("object_id", cm.IDs, "object_type", "issue"))
	h := rails.NewHash("remote", true, "class", "icon icon-add")
	return rails.LinkTo(cm.l.icon("add", cm.l.L("button_add")), u, h)
}

// WatcherLink は watcher_link(@issues, User.current)。
func (cm *issueContextMenu) WatcherLink() template.HTML {
	l := cm.l
	u := l.c.User
	watched := false
	for _, m := range cm.Models {
		if m.I == nil {
			continue
		}
		ok, err := m.env().WatchedBy(l.ctx, m.I, u)
		l.fail(err)
		if ok {
			watched = true
		}
	}
	wid := "bulk"
	if len(cm.IDs) == 1 {
		wid = strconv.FormatInt(cm.IDs[0], 10)
	}
	css := "issue-" + wid + "-watcher " + map[bool]string{true: "icon icon-fav", false: "icon icon-fav-off"}[watched]
	text, icon, method := l.L("button_watch"), "watch", "post"
	if watched {
		text, icon, method = l.L("button_unwatch"), "unwatch", "delete"
	}
	var oid any = cm.IDs
	if len(cm.IDs) == 1 {
		oid = cm.IDs[0]
	}
	return rails.LinkTo(l.icon(icon, text), "/watchers/watch?"+helper.ToQuery(rails.NewHash("object_id", oid, "object_type", "issue")),
		rails.NewHash("remote", true, "method", method, "class", css))
}

// FilterLink は複数選択時の「絞り込み」リンク。
func (cm *issueContextMenu) FilterLink() template.HTML {
	var idsS []string
	for _, id := range cm.IDs {
		idsS = append(idsS, strconv.FormatInt(id, 10))
	}
	h := rails.NewHash("set_filter", 1, "status_id", "*", "issue_id", strings.Join(idsS, ","), "c", cm.Columns)
	return cm.link(cm.l.icon("list", cm.l.L("button_filter")), issuesPath(cm.l.c.Project)+"?"+helper.ToQuery(h), "icon icon-list", nil, false, false, nil)
}

// LogTimeLink / AddSubtaskLink / CopyLink / CopyURLLink / DeleteLink。
func (cm *issueContextMenu) LogTimeLink() template.HTML {
	return cm.link(cm.l.icon("time-add", cm.l.L("button_log_time")), "/issues/"+strconv.FormatInt(cm.Issue.Row.ID, 10)+"/time_entries/new",
		"icon icon-time-add", nil, false, false, nil)
}

func (cm *issueContextMenu) AddSubtaskLink() template.HTML {
	v := &issueShowView{l: cm.l, M: cm.Issue}
	return cm.link(cm.l.icon("add", cm.l.L("button_add_subtask")), v.NewSubtaskURL(), "icon icon-add", nil, false, false, nil)
}

func (cm *issueContextMenu) CopyURLLink() template.HTML {
	if cm.Issue != nil {
		return cm.l.copyObjectURLLink(issueURL(cm.l.c, cm.Issue.Row.ID))
	}
	var idsS []string
	for _, id := range cm.IDs {
		idsS = append(idsS, strconv.FormatInt(id, 10))
	}
	u := httpx.RequestBaseURL(cm.l.c.R) + issuesPath(cm.l.c.Project) + "?" + helper.ToQuery(rails.NewHash("set_filter", 1, "status_id", "*", "issue_id", strings.Join(idsS, ",")))
	return cm.l.copyObjectURLLink(u)
}

func (cm *issueContextMenu) CopyLink() template.HTML {
	if cm.Issue != nil {
		return cm.link(cm.l.icon("copy", cm.l.L("button_copy")), "/projects/"+cm.Issue.Project.Identifier+"/issues/"+strconv.FormatInt(cm.Issue.Row.ID, 10)+"/copy",
			"icon icon-copy", nil, false, !cm.CanCopy, nil)
	}
	return cm.link(cm.l.icon("copy", cm.l.L("button_copy")), "/issues/bulk_edit?"+helper.ToQuery(rails.NewHash("copy", "1", "ids", cm.IDs)),
		"icon icon-copy", nil, false, !cm.CanCopy, nil)
}

func (cm *issueContextMenu) DeleteLink() template.HTML {
	obj := cm.l.L("label_issue")
	if len(cm.IDs) > 1 {
		obj = cm.l.L("label_issue_plural")
	}
	label := RubyCapitalize(cm.l.L("button_delete_object", i18n.Vars{"object_name": obj}))
	u := "/issues?" + helper.ToQuery(rails.NewHash("back_url", cm.Back, "ids", cm.IDs))
	return cm.link(cm.l.icon("del", label), u, "icon icon-del", "delete", false, !cm.CanDelete,
		rails.NewHash("confirm", cm.destroyConfirmation()))
}

// destroyConfirmation は issues_destroy_confirmation_message(@issues)。
func (cm *issueContextMenu) destroyConfirmation() string {
	msg := cm.l.L("text_issues_destroy_confirmation")
	seen := map[int64]bool{}
	for _, m := range cm.Models {
		seen[m.Row.ID] = true
	}
	n := 0
	counted := map[int64]bool{}
	for _, m := range cm.Models {
		if m.Leaf() {
			continue
		}
		for _, d := range m.Descendants() {
			if !seen[d.ID] && !counted[d.ID] {
				counted[d.ID] = true
				n++
			}
		}
	}
	if n > 0 {
		msg += "\n" + cm.l.L("text_issues_destroy_descendants_confirmation", i18n.Vars{"count": n})
	}
	return msg
}

// Logged は User.current.logged?。
func (cm *issueContextMenu) Logged() bool { return cm.l.c.User.Logged() }

// classNames は class_names / token_list（空白区切りのトークンを重複なく連結する）。
func classNames(parts []string) string {
	var out []string
	for _, p := range parts {
		for _, t := range strings.Fields(p) {
			if !slices.Contains(out, t) {
				out = append(out, t)
			}
		}
	}
	return strings.Join(out, " ")
}
