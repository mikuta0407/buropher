package handler

import (
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view"
)

// ProjectsController（app/controllers/projects_controller.rb）。
//
//	menu_item :overview
//	menu_item :settings, :only => :settings
//	menu_item :projects, :only => [:index, :new, :copy, :create]
var ProjectsController = &Controller{Name: "projects", MainMenu: true}

// routesProjects は projects コントローラのルートを登録する。
//
//	resources :projects do
//	  collection do
//	    get 'autocomplete'
//	    delete 'bulk_destroy'
//	  end
//	  member do
//	    get 'settings(/:tab)', :action => 'settings', :as => 'settings'
//	    match 'archive', :via => [:post, :put]
//	    match 'unarchive', :via => [:post, :put]
//	    match 'close', :via => [:post, :put]
//	    match 'reopen', :via => [:post, :put]
//	    match 'copy', :via => [:get, :post]
//	    match 'bookmark', :via => [:delete, :post]
//	  end
//	end
func (a *App) routesProjects(r Router) {
	ctrl := ProjectsController
	// before_action :find_project, :except => [:index, :autocomplete, :list, :new, :create, :copy, :bulk_destroy]
	// before_action :authorize, :except => [:index, :autocomplete, :list, :new, :create, :copy,
	//                                        :archive, :unarchive, :destroy, :bulk_destroy]
	// before_action :authorize_global, :only => [:new, :create]
	// before_action :require_admin, :only => [:copy, :archive, :unarchive, :bulk_destroy]
	// accept_atom_auth :index
	// accept_api_auth :index, :show, :create, :update, :destroy, :archive, :unarchive, :close, :reopen
	a.Handle(r, http.MethodGet, "/projects/autocomplete", ctrl, "autocomplete", a.ProjectsAutocomplete)
	a.Handle(r, http.MethodDelete, "/projects/bulk_destroy", ctrl, "bulk_destroy", a.ProjectsBulkDestroy, RequireAdmin())
	a.Handle(r, http.MethodGet, "/projects", ctrl, "index", a.ProjectsIndex, AcceptAtomAuth(), AcceptAPIAuth())
	a.Handle(r, http.MethodPost, "/projects", ctrl, "create", a.ProjectsCreate, AuthorizeGlobal(), AcceptAPIAuth())
	a.Handle(r, http.MethodGet, "/projects/new", ctrl, "new", a.ProjectsNew, AuthorizeGlobal())
	a.Handle(r, http.MethodGet, "/projects/{id}/settings", ctrl, "settings", a.ProjectsSettings, FindProject("id"), Authorize())
	a.Handle(r, http.MethodGet, "/projects/{id}/settings/{tab}", ctrl, "settings", a.ProjectsSettings, FindProject("id"), Authorize())
	for _, m := range []string{http.MethodPost, http.MethodPut} {
		a.Handle(r, m, "/projects/{id}/archive", ctrl, "archive", a.ProjectsArchive, FindProject("id"), RequireAdmin(), AcceptAPIAuth())
		a.Handle(r, m, "/projects/{id}/unarchive", ctrl, "unarchive", a.ProjectsUnarchive, FindProject("id"), RequireAdmin(), AcceptAPIAuth())
		a.Handle(r, m, "/projects/{id}/close", ctrl, "close", a.ProjectsClose, FindProject("id"), Authorize(), AcceptAPIAuth())
		a.Handle(r, m, "/projects/{id}/reopen", ctrl, "reopen", a.ProjectsReopen, FindProject("id"), Authorize(), AcceptAPIAuth())
	}
	a.Handle(r, http.MethodGet, "/projects/{id}/copy", ctrl, "copy", a.ProjectsCopy, RequireAdmin())
	a.Handle(r, http.MethodPost, "/projects/{id}/copy", ctrl, "copy", a.ProjectsCopy, RequireAdmin())
	a.Handle(r, http.MethodPost, "/projects/{id}/bookmark", ctrl, "bookmark", a.ProjectsBookmark, FindProject("id"), Authorize())
	a.Handle(r, http.MethodDelete, "/projects/{id}/bookmark", ctrl, "bookmark", a.ProjectsBookmark, FindProject("id"), Authorize())
	a.Handle(r, http.MethodGet, "/projects/{id}/edit", ctrl, "edit", a.ProjectsEdit, FindProject("id"), Authorize())
	a.Handle(r, http.MethodGet, "/projects/{id}", ctrl, "show", a.ProjectsShow, FindProject("id"), Authorize(), AcceptAPIAuth())
	a.Handle(r, http.MethodPatch, "/projects/{id}", ctrl, "update", a.ProjectsUpdate, FindProject("id"), Authorize(), AcceptAPIAuth())
	a.Handle(r, http.MethodPut, "/projects/{id}", ctrl, "update", a.ProjectsUpdate, FindProject("id"), Authorize(), AcceptAPIAuth())
	a.Handle(r, http.MethodDelete, "/projects/{id}", ctrl, "destroy", a.ProjectsDestroy, FindProject("id"), AcceptAPIAuth())
}

// ---------------------------------------------------------------- show

// issueReportRow は概要のチケット集計の 1 行（トラッカーごとの未完了・完了・合計）。
type issueReportRow struct {
	Tracker *domain.Tracker
	Open    int64
	Closed  int64
	Total   int64
}

// roleMembers は principals_by_role の 1 項目。
type roleMembers struct {
	Role       *domain.Role
	Principals []*repository.MemberPrincipal
}

// ProjectsShow は projects#show（GET /projects/:id）。
func (a *App) ProjectsShow(c *Req) {
	p := c.Project
	// try to redirect to the requested menu item
	if jump := c.Params().String("jump"); jump != "" {
		if u, ok := a.Helpers.ProjectMenuItemURL(c.Page(), "project_menu", jump, p); ok {
			c.Redirect(u)
			return
		}
	}
	switch httpx.Negotiate(c.R, "html", "xml", "json") {
	case "html":
	case "xml", "json":
		a.renderProjectShowAPI(c, p, http.StatusOK)
		return
	default:
		c.unknownFormat()
		return
	}
	ctx := c.Ctx()
	az := c.Authz()
	data := map[string]any{"Project": p}

	byRole, err := a.principalsByRole(c, p)
	if err != nil {
		a.internalError(c, "principals by role", err)
		return
	}
	data["PrincipalsByRole"] = byRole

	leaf, err := repository.IsProjectLeaf(ctx, a.DB, p.ID)
	if err != nil {
		a.internalError(c, "project leaf", err)
		return
	}
	var subprojects []*domain.Project
	if !leaf {
		vis, err := az.VisibleCondition(ctx, authz.ConditionOptions{})
		if err != nil {
			a.internalError(c, "visible condition", err)
			return
		}
		subprojects, err = repository.LoadProjects(ctx, a.DB, "projects.parent_id = ? AND "+vis, p.ID)
		if err != nil {
			a.internalError(c, "subprojects", err)
			return
		}
	}
	data["Subprojects"] = subprojects

	news, err := repository.ProjectNews(ctx, a.DB, p.ID, 5)
	if err != nil {
		a.internalError(c, "project news", err)
		return
	}
	data["News"] = news
	data["CanViewNews"] = len(news) > 0 && c.AllowedTo(domain.ControllerAction("news", "index"), p)

	withSub := a.Settings.Bool("display_subprojects_issues")
	trackerVis, err := trackerVisibleCondition(c)
	if err != nil {
		a.internalError(c, "tracker visible", err)
		return
	}
	trackers, err := repository.RolledUpTrackers(ctx, a.DB, p, withSub, trackerVis)
	if err != nil {
		a.internalError(c, "rolled up trackers", err)
		return
	}
	cond := authz.ProjectCondition(p, withSub)
	issueVis, err := az.IssueVisibleCondition(ctx, authz.ConditionOptions{})
	if err != nil {
		a.internalError(c, "issue visible", err)
		return
	}
	open, err := repository.IssueCountsByTracker(ctx, a.DB, issueVis, cond, true)
	if err != nil {
		a.internalError(c, "open issues", err)
		return
	}
	total, err := repository.IssueCountsByTracker(ctx, a.DB, issueVis, cond, false)
	if err != nil {
		a.internalError(c, "total issues", err)
		return
	}
	rows := make([]issueReportRow, len(trackers))
	for i, t := range trackers {
		rows[i] = issueReportRow{Tracker: t, Open: open[t.ID], Closed: total[t.ID] - open[t.ID], Total: total[t.ID]}
	}
	data["IssueReport"] = rows
	data["CanViewIssues"] = c.AllowedTo(domain.Perm("view_issues"), p)
	data["CanViewCalendar"] = c.allowedToGloballyOrProject("view_calendar", p)
	data["CanViewGantt"] = c.allowedToGloballyOrProject("view_gantt", p)
	data["CanViewTimeEntries"] = c.AllowedTo(domain.Perm("view_time_entries"), p)
	data["CanLogTime"] = c.AllowedTo(domain.Perm("log_time"), p)

	if ok, err := az.AllowedToViewAllTimeEntries(ctx, p); err != nil {
		a.internalError(c, "view all time entries", err)
		return
	} else if ok {
		teVis, err := timeEntryVisibleCondition(c)
		if err != nil {
			a.internalError(c, "time entry visible", err)
			return
		}
		hours, err := repository.SumTimeEntryHours(ctx, a.DB, teVis, cond)
		if err != nil {
			a.internalError(c, "total hours", err)
			return
		}
		est, err := repository.SumIssueEstimatedHours(ctx, a.DB, issueVis, cond)
		if err != nil {
			a.internalError(c, "estimated hours", err)
			return
		}
		data["TotalHours"] = hours
		data["TotalEstimatedHours"] = est
		data["ShowHours"] = true
	}

	cfs, err := a.projectVisibleCustomFieldValues(c, p)
	if err != nil {
		a.internalError(c, "custom field values", err)
		return
	}
	// render_custom_field_values: 書式化した値が空でないものだけ
	var display []cfDisplay
	anyCF := false
	for _, v := range cfs {
		if strings.TrimSpace(strings.Join(v.Values, "")) != "" {
			anyCF = true
		}
		if f := showCFValue(c, v); strings.TrimSpace(string(f)) != "" {
			display = append(display, cfDisplay{Field: v.Field, Formatted: f})
		}
	}
	data["CustomFieldDisplay"] = display
	data["AnyCustomFieldValue"] = anyCF

	data["CanAddSubproject"] = c.AllowedTo(domain.Perm("add_subprojects"), p)
	data["CanCloseProject"] = c.AllowedTo(domain.Perm("close_project"), p)
	deletable, err := a.projectDeletable(c, p)
	if err != nil {
		a.internalError(c, "project deletable", err)
		return
	}
	data["Deletable"] = deletable
	data["CanSettings"] = c.AllowedTo(domain.Perm("edit_project"), p) &&
		c.AllowedTo(domain.ControllerAction("projects", "settings"), p)
	data["AtomKey"] = c.AtomKey()
	c.Render("projects/show", data)
}

// allowedToGloballyOrProject は User.current.allowed_to?(perm, project, global: true)。
func (c *Req) allowedToGloballyOrProject(perm string, p *domain.Project) bool {
	if p != nil {
		return c.AllowedTo(domain.Perm(perm), p)
	}
	return c.AllowedToGlobally(domain.Perm(perm))
}

// trackerVisibleCondition は Tracker.visible の条件（trackers / projects を参照）。
func trackerVisibleCondition(c *Req) (string, error) {
	return c.Authz().AllowedToCondition(c.Ctx(), "view_issues", authz.ConditionOptions{}, func(role *domain.Role, _ *domain.User) string {
		if role.PermissionsAllTrackers("view_issues") {
			return ""
		}
		if ids := role.PermissionsTrackerIDs("view_issues"); len(ids) > 0 {
			parts := make([]string, len(ids))
			for i, id := range ids {
				parts[i] = itoa(id)
			}
			return "trackers.id IN (" + strings.Join(parts, ",") + ")"
		}
		return "1=0"
	})
}

// timeEntryVisibleCondition は TimeEntry.visible_condition(User.current)。
func timeEntryVisibleCondition(c *Req) (string, error) {
	return c.Authz().AllowedToCondition(c.Ctx(), "view_time_entries", authz.ConditionOptions{}, func(role *domain.Role, user *domain.User) string {
		switch {
		case role.TimeEntriesVisibility == domain.TimeEntriesVisibilityAll:
			return ""
		case role.TimeEntriesVisibility == domain.TimeEntriesVisibilityOwn && user.ID != 0 && user.Logged():
			return "time_entries.user_id = " + itoa(user.ID)
		}
		return "1=0"
	})
}

// principalsByRole は Project#principals_by_role（memberships.active をロールごとに）を
// keys.sort（Role#<=>）と principals.sort（Principal#<=>）の順で返す。
func (a *App) principalsByRole(c *Req, p *domain.Project) ([]roleMembers, error) {
	ctx := c.Ctx()
	members, err := repository.ProjectMembersWithPrincipals(ctx, a.DB, p.ID, true)
	if err != nil {
		return nil, err
	}
	var roleIDs []int64
	for _, m := range members {
		roleIDs = append(roleIDs, m.Member.RoleIDs()...)
	}
	roles, err := repository.RolesByIDs(ctx, a.DB, roleIDs)
	if err != nil {
		return nil, err
	}
	byID := map[int64]*roleMembers{}
	var out []*roleMembers
	for _, r := range roles {
		rm := &roleMembers{Role: r}
		byID[r.ID] = rm
		out = append(out, rm)
	}
	for _, m := range members {
		if m.Principal() == nil {
			continue
		}
		for _, rid := range m.Member.RoleIDs() {
			if rm := byID[rid]; rm != nil {
				rm.Principals = append(rm.Principals, m)
			}
		}
	}
	domain.SortRoles(rolesOf(out))
	sort.SliceStable(out, func(i, j int) bool { return out[i].Role.Compare(out[j].Role) < 0 })
	page := c.Page()
	res := make([]roleMembers, 0, len(out))
	for _, rm := range out {
		if len(rm.Principals) == 0 {
			continue
		}
		sortPrincipals(page, rm.Principals)
		res = append(res, *rm)
	}
	return res, nil
}

func rolesOf(rms []*roleMembers) []*domain.Role {
	out := make([]*domain.Role, len(rms))
	for i, rm := range rms {
		out[i] = rm.Role
	}
	return out
}

// principalClassName は Principal のクラス名（並べ替えに使う）。
func principalClassName(m *repository.MemberPrincipal) string {
	if m.User != nil {
		return m.User.Kind.RedmineType()
	}
	if m.Group != nil {
		return m.Group.Kind.RedmineType()
	}
	return ""
}

// sortPrincipals は Principal#<=>（同じクラスは to_s の casecmp、異なればクラス名の逆順 = グループは後）。
func sortPrincipals(page *helper.Page, ps []*repository.MemberPrincipal) {
	sort.SliceStable(ps, func(i, j int) bool {
		ci, cj := principalClassName(ps[i]), principalClassName(ps[j])
		if ci == cj {
			return casecmp(helper.PrincipalName(page, ps[i]), helper.PrincipalName(page, ps[j])) < 0
		}
		return strings.Compare(cj, ci) < 0
	})
}

// casecmp は String#casecmp（ASCII の大文字小文字を区別しない比較）。
func casecmp(a, b string) int {
	la, lb := asciiLower(a), asciiLower(b)
	return strings.Compare(la, lb)
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// projectDeletable は Project#deletable?(User.current)。
func (a *App) projectDeletable(c *Req, p *domain.Project) (bool, error) {
	if c.User.IsAdmin() {
		return true, nil
	}
	if !c.AllowedTo(domain.Perm("delete_project"), p) {
		return false, nil
	}
	return repository.IsProjectLeaf(c.Ctx(), a.DB, p.ID)
}

// ---------------------------------------------------------------- edit / bookmark / close / reopen / archive

// ProjectsEdit は projects#edit（GET /projects/:id/edit）。
func (a *App) ProjectsEdit(c *Req) {
	// Redmine 6.1 には projects/edit ビューが無い。default_render はブラウザの通常の GET（html・非 XHR）なら
	// ActionController::MissingExactTemplate（406、本文なし）、それ以外（Accept: */* や XHR）は head :no_content
	f := httpx.Format(c.R)
	if (c.R.Method == http.MethodGet || c.R.Method == http.MethodHead) && (f == "html" || f == "") && !httpx.IsXHR(c.R) {
		c.unknownFormat()
		return
	}
	httpx.Head(c.W, c.R, http.StatusNoContent)
	c.Halt()
}

// ProjectsBookmark は projects#bookmark（POST / DELETE /projects/:id/bookmark）。
func (a *App) ProjectsBookmark(c *Req) {
	ctx := c.Ctx()
	p := c.Project
	if c.User.Logged() {
		var err error
		switch c.R.Method {
		case http.MethodDelete:
			err = repository.DeleteProjectBookmark(ctx, a.DB, c.User.ID, p.ID)
		case http.MethodPost:
			err = repository.BookmarkProject(ctx, a.DB, c.User.ID, p.ID)
		}
		if err != nil {
			a.internalError(c, "bookmark", err)
			return
		}
	}
	switch httpx.Negotiate(c.R, "js", "html") {
	case "js":
		page := c.Page()
		var jb *helper.JumpBox
		if c.User.Logged() {
			var err error
			jb, err = helper.LoadJumpBox(page, c.Authz())
			if err != nil {
				a.internalError(c, "jump box", err)
				return
			}
		} else {
			jb = &helper.JumpBox{}
		}
		c.Render("projects/bookmark", map[string]any{
			"JumpBox":      a.Helpers.RenderProjectsForJumpBox(page, &helper.JumpBox{Projects: jb.Projects, Bookmarked: jb.Bookmarked, Recents: jb.Recents}, p),
			"BookmarkLink": a.Helpers.BookmarkLink(page, p),
		}, RenderOptions{Format: "js", Layout: view.NoLayout})
	case "html":
		c.Redirect("/projects/" + p.Identifier)
	default:
		c.unknownFormat()
	}
}

// ProjectsClose は projects#close。
func (a *App) ProjectsClose(c *Req) {
	if err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error { return repository.CloseProject(c.Ctx(), tx, c.Project.ID) }); err != nil {
		a.internalError(c, "close project", err)
		return
	}
	a.respondProjectStatus(c)
}

// ProjectsReopen は projects#reopen。
func (a *App) ProjectsReopen(c *Req) {
	if err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error { return repository.ReopenProject(c.Ctx(), tx, c.Project.ID) }); err != nil {
		a.internalError(c, "reopen project", err)
		return
	}
	a.respondProjectStatus(c)
}

func (a *App) respondProjectStatus(c *Req) {
	if httpx.IsAPIRequest(c.R) {
		c.RenderAPIOKMin()
		return
	}
	c.Redirect("/projects/" + c.Project.Identifier)
}

// RenderAPIOKMin は render_api_ok（204）。
// TODO(dedupe): 共通の API ヘルパー（RenderAPIOK）がマージされたら置き換える。
func (c *Req) RenderAPIOKMin() {
	httpx.RenderAPIOK(c.W, c.R)
	c.Halt()
}

// RenderAPIErrorsMin は render_api_errors（422）。
func (c *Req) RenderAPIErrorsMin(messages ...string) {
	c.App.Errors.RenderAPIErrors(c.W, c.R, messages...)
	c.Halt()
}

// adminProjectsPath は admin_projects_path(:status => params[:status])。
func adminProjectsPath(c *Req) string {
	if s := c.Params().String("status"); s != "" {
		return "/admin/projects?status=" + url.QueryEscape(s)
	}
	return "/admin/projects"
}

// ProjectsArchive は projects#archive。
func (a *App) ProjectsArchive(c *Req) {
	var ok bool
	err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
		var err error
		ok, err = repository.ArchiveProject(c.Ctx(), tx, c.Project.ID)
		return err
	})
	if err != nil {
		a.internalError(c, "archive project", err)
		return
	}
	var msg string
	if !ok {
		msg = c.L("error_can_not_archive_project")
	}
	if httpx.IsAPIRequest(c.R) {
		if msg != "" {
			c.RenderAPIErrorsMin(msg)
		} else {
			c.RenderAPIOKMin()
		}
		return
	}
	if msg != "" {
		c.Flash().SetError(msg)
	}
	c.redirectToRefererOr(adminProjectsPath(c))
}

// ProjectsUnarchive は projects#unarchive。
func (a *App) ProjectsUnarchive(c *Req) {
	if !c.Project.Active() {
		if err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error { return repository.UnarchiveProject(c.Ctx(), tx, c.Project.ID) }); err != nil {
			a.internalError(c, "unarchive project", err)
			return
		}
	}
	if httpx.IsAPIRequest(c.R) {
		c.RenderAPIOKMin()
		return
	}
	c.redirectToRefererOr(adminProjectsPath(c))
}

// ---------------------------------------------------------------- autocomplete

// ProjectsAutocomplete は projects#autocomplete（GET /projects/autocomplete.js）。
func (a *App) ProjectsAutocomplete(c *Req) {
	if httpx.Negotiate(c.R, "js") != "js" {
		c.unknownFormat()
		return
	}
	ctx := c.Ctx()
	q := c.Params().String("q")
	var projects []*domain.Project
	var err error
	if strings.TrimSpace(q) != "" {
		var vis string
		vis, err = c.Authz().VisibleCondition(ctx, authz.ConditionOptions{})
		if err == nil {
			pattern := "%" + likeEscape(strings.TrimSpace(q)) + "%"
			projects, err = repository.LoadProjects(ctx, a.DB, vis+` AND (LOWER(projects.identifier) LIKE LOWER(?) ESCAPE '\' OR LOWER(projects.name) LIKE LOWER(?) ESCAPE '\')`, pattern, pattern)
		}
	} else if c.User.Logged() {
		projects, err = repository.UserProjectsAll(ctx, a.DB, c.User.ID)
	}
	if err != nil {
		a.internalError(c, "autocomplete projects", err)
		return
	}
	page := c.Page()
	var s any = ""
	if len(projects) > 0 {
		ns, err := repository.ProjectNestedSet(ctx, a.DB)
		if err != nil {
			a.internalError(c, "nested set", err)
			return
		}
		jps := make([]helper.JumpProject, len(projects))
		for i, p := range projects {
			v := ns[p.ID]
			jps[i] = helper.JumpProject{ID: p.ID, Name: p.Name, Identifier: p.Identifier, Lft: v.Lft, Rgt: v.Rgt}
		}
		s = a.Helpers.RenderProjectsForJumpBoxQuery(page, jps, q)
	} else if strings.TrimSpace(q) != "" {
		s = "<span>" + escapeHTML(c.L("label_no_data")) + "</span>"
	}
	c.Render("projects/autocomplete", map[string]any{"Items": s}, RenderOptions{Format: "js", Layout: view.NoLayout})
}

// likeEscape は sanitize_sql_like（% _ \ をエスケープ）。
func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func escapeHTML(s string) string {
	return strings.NewReplacer("&", "&amp;", `"`, "&quot;", "'", "&#39;", "<", "&lt;", ">", "&gt;").Replace(s)
}
