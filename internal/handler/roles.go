package handler

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/permission"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// RolesController（app/controllers/roles_controller.rb）。
var RolesController = &Controller{Name: "roles", MainMenu: false}

// routesRoles は roles コントローラのルートを登録する。
//
//	resources :roles do
//	  collection do
//	    get 'permissions'
//	    post 'permissions', :to => 'roles#update_permissions'
//	  end
//	end
func (a *App) routesRoles(r Router) {
	// before_action :require_admin, :except => [:index, :show]
	// before_action :require_admin_or_api_request, :only => [:index, :show]
	// before_action :find_role, :only => [:show, :edit, :update, :destroy]
	// accept_api_auth :index, :show
	findRole := Before(a.findRole)
	a.Handle(r, http.MethodGet, "/roles/permissions", RolesController, "permissions", a.RolesPermissions, RequireAdmin())
	a.Handle(r, http.MethodPost, "/roles/permissions", RolesController, "update_permissions", a.RolesUpdatePermissions, RequireAdmin())
	a.Handle(r, http.MethodGet, "/roles", RolesController, "index", a.RolesIndex, RequireAdminOrAPIRequest(), AcceptAPIAuth())
	a.Handle(r, http.MethodPost, "/roles", RolesController, "create", a.RolesCreate, RequireAdmin())
	a.Handle(r, http.MethodGet, "/roles/new", RolesController, "new", a.RolesNew, RequireAdmin())
	a.Handle(r, http.MethodGet, "/roles/{id}/edit", RolesController, "edit", a.RolesEdit, RequireAdmin(), findRole)
	a.Handle(r, http.MethodGet, "/roles/{id}", RolesController, "show", a.RolesShow, RequireAdminOrAPIRequest(), findRole, AcceptAPIAuth())
	a.Handle(r, http.MethodPatch, "/roles/{id}", RolesController, "update", a.RolesUpdate, RequireAdmin(), findRole)
	a.Handle(r, http.MethodPut, "/roles/{id}", RolesController, "update", a.RolesUpdate, RequireAdmin(), findRole)
	a.Handle(r, http.MethodDelete, "/roles/{id}", RolesController, "destroy", a.RolesDestroy, RequireAdmin(), findRole)
}

const ctxRole = "role"

// findRole は RolesController#find_role。
func (a *App) findRole(c *Req) {
	id, ok := c.Params().IntStrict("id")
	if !ok {
		c.Render404("")
		return
	}
	role, err := repository.GetRole(c.Ctx(), a.DB, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.Render404("")
		} else {
			a.internalError(c, "find role", err)
		}
		return
	}
	c.setLocal(ctxRole, role)
}

// internalError は想定外のエラーを記録して 500 を返す。
func (a *App) internalError(c *Req, what string, err error) {
	a.logger().Error(what, "err", err)
	c.renderInternalError()
	c.Halt()
}

// ---------------------------------------------------------------- フォームモデル

// roleForm は roles/_form のモデル（@role）。
type roleForm struct {
	formModel
	*domain.Role
	displayName string
	// allTrackers は permissions_all_trackers（権限 → "0" / "1"）。
	allTrackers map[string]string
	// trackerIDs は permissions_tracker_ids（権限 → トラッカー id）。
	trackerIDs map[string][]int64
}

func newRoleForm(c *Req, r *domain.Role) *roleForm {
	f := &roleForm{formModel: newFormModel(c, "role", r.ID), Role: r,
		allTrackers: map[string]string{}, trackerIDs: map[string][]int64{}}
	for perm, ids := range r.RestrictedTrackers {
		f.allTrackers[perm] = "0"
		f.trackerIDs[perm] = slices.Clone(ids)
	}
	f.displayName = helper.RoleName(c.Page(), r)
	return f
}

// DisplayName は Role#name（組込ロールは翻訳名、それ以外は現在の名前）。
func (f *roleForm) DisplayName() string {
	if f.IsBuiltin() {
		return f.displayName
	}
	return f.Name
}

// ManagesRole は @role.managed_roles.include?(role)。
func (f *roleForm) ManagesRole(id int64) bool { return slices.Contains(f.ManagedRoleIDs, id) }

// DefaultActivityID は default_time_entry_activity_id（nil 可）。
func (f *roleForm) DefaultActivityID() any {
	if f.DefaultTimeEntryActivityID == nil {
		return nil
	}
	return *f.DefaultTimeEntryActivityID
}

// PermissionsAllTrackers は Role#permissions_all_trackers?(permission)。
func (f *roleForm) PermissionsAllTrackers(perm string) bool {
	return f.HasPermission(perm) && f.allTrackers[perm] != "0"
}

// PermissionsTrackerIDsInclude は Role#permissions_tracker_ids?(permission, tracker_id)。
func (f *roleForm) PermissionsTrackerIDsInclude(perm string, trackerID int64) bool {
	return f.HasPermission(perm) && slices.Contains(f.trackerIDs[perm], trackerID)
}

// copyFrom は Role#copy_from（id / name / position / builtin / permissions 以外の属性と権限・管理対象ロールを写す）。
func (f *roleForm) copyFrom(src *domain.Role) {
	f.Assignable = src.Assignable
	f.IssuesVisibility = src.IssuesVisibility
	f.UsersVisibility = src.UsersVisibility
	f.TimeEntriesVisibility = src.TimeEntriesVisibility
	f.AllRolesManaged = src.AllRolesManaged
	f.DefaultTimeEntryActivityID = src.DefaultTimeEntryActivityID
	f.allTrackers = map[string]string{}
	f.trackerIDs = map[string][]int64{}
	for perm, ids := range src.RestrictedTrackers {
		f.allTrackers[perm] = "0"
		f.trackerIDs[perm] = slices.Clone(ids)
	}
	f.Permissions = slices.Clone(src.Permissions)
	f.ManagedRoleIDs = slices.Clone(src.ManagedRoleIDs)
}

// setPermissions は Role#permissions=（空と重複を除く）。
func (f *roleForm) setPermissions(perms []string) {
	out := []string{}
	for _, p := range perms {
		if p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	f.Permissions = out
}

// assign は @role.safe_attributes = params[:role]（渡されたキーのみ）。
func (f *roleForm) assign(p *httpx.Params) {
	if p == nil {
		return
	}
	if v, ok := p.StringOK("name"); ok {
		f.Name = v
	}
	if v, ok := p.StringOK("assignable"); ok {
		f.Assignable = castBool(v)
	}
	if v, ok := p.StringOK("position"); ok {
		f.Position = int(httpx.RubyToI(v))
	}
	if v, ok := p.StringOK("issues_visibility"); ok {
		f.IssuesVisibility = v
	}
	if v, ok := p.StringOK("users_visibility"); ok {
		f.UsersVisibility = v
	}
	if v, ok := p.StringOK("time_entries_visibility"); ok {
		f.TimeEntriesVisibility = v
	}
	if v, ok := p.StringOK("all_roles_managed"); ok {
		f.AllRolesManaged = castBool(v)
	}
	if p.Has("managed_role_ids") {
		f.ManagedRoleIDs = paramIDs(p.Strings("managed_role_ids"))
	}
	if p.Has("permissions") {
		f.setPermissions(p.Strings("permissions"))
	}
	if p.Has("permissions_all_trackers") {
		f.allTrackers = map[string]string{}
		if m := p.Map("permissions_all_trackers"); m != nil {
			for _, k := range m.Keys() {
				f.allTrackers[k] = m.String(k)
			}
		}
	}
	if p.Has("permissions_tracker_ids") {
		f.trackerIDs = map[string][]int64{}
		if m := p.Map("permissions_tracker_ids"); m != nil {
			for _, k := range m.Keys() {
				f.trackerIDs[k] = paramIDs(m.Strings(k))
			}
		}
	}
	if v, ok := p.StringOK("default_time_entry_activity_id"); ok {
		f.DefaultTimeEntryActivityID = optionalID(v)
	}
}

// restrictedTrackers は保存用の RestrictedTrackers（permissions_all_trackers が '0' の付与済み権限）。
func (f *roleForm) restrictedTrackers() map[string][]int64 {
	out := map[string][]int64{}
	for _, perm := range f.Permissions {
		if f.allTrackers[perm] == "0" {
			ids := f.trackerIDs[perm]
			if ids == nil {
				ids = []int64{}
			}
			out[perm] = ids
		}
	}
	return out
}

var (
	issuesVisibilityValues      = []string{"all", "default", "own"}
	timeEntriesVisibilityValues = []string{"all", "own"}
	usersVisibilityValues       = []string{"all", "members_of_visible_projects"}
)

// validate は Role のバリデーション。
func (a *App) validateRole(c *Req, f *roleForm, orig *domain.Role) error {
	f.errs = &domain.ValidationErrors{}
	if strings.TrimSpace(f.Name) == "" {
		f.errs.Add("name", "blank", nil)
	}
	var n int
	if err := a.DB.Get(c.Ctx(), &n, `SELECT COUNT(*) FROM roles WHERE name = ? AND id <> ?`, f.Name, f.ID); err != nil {
		return err
	}
	if n > 0 {
		f.errs.Add("name", "taken", nil)
	}
	if len([]rune(f.Name)) > 255 {
		f.errs.Add("name", "too_long", map[string]any{"count": 255})
	}
	check := func(attr, val, was string, allowed []string) {
		if (orig == nil || val != was) && !slices.Contains(allowed, val) {
			f.errs.Add(attr, "inclusion", nil)
		}
	}
	var o domain.Role
	if orig != nil {
		o = *orig
	}
	check("issues_visibility", f.IssuesVisibility, o.IssuesVisibility, issuesVisibilityValues)
	check("users_visibility", f.UsersVisibility, o.UsersVisibility, usersVisibilityValues)
	check("time_entries_visibility", f.TimeEntriesVisibility, o.TimeEntriesVisibility, timeEntriesVisibilityValues)
	return nil
}

// saveRole はロールを保存する（acts_as_positioned scope: :builtin）。
func (a *App) saveRole(c *Req, f *roleForm, oldPos *int, copyWorkflowFrom *int64) error {
	return a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
		r := f.Role
		r.RestrictedTrackers = f.restrictedTrackers()
		// managed_role_ids= は存在するロールのみ
		if len(r.ManagedRoleIDs) > 0 {
			rs, err := repository.RolesByIDs(c.Ctx(), tx, r.ManagedRoleIDs)
			if err != nil {
				return err
			}
			ids := make([]int64, 0, len(rs))
			for _, x := range rs {
				ids = append(ids, x.ID)
			}
			r.ManagedRoleIDs = ids
		}
		isNew := r.ID == 0
		if err := repository.SaveRole(c.Ctx(), tx, r); err != nil {
			return err
		}
		f.id = r.ID
		scope := repository.PositionScope{Table: "roles", Where: "builtin = ?", Args: []any{r.Builtin}}
		if isNew {
			if err := repository.InsertPosition(c.Ctx(), tx, scope, r.ID, r.Position); err != nil {
				return err
			}
		} else if oldPos != nil && *oldPos != r.Position {
			if err := repository.ShiftPositions(c.Ctx(), tx, scope, r.ID, *oldPos, r.Position); err != nil {
				return err
			}
		}
		if copyWorkflowFrom != nil {
			if err := repository.CopyWorkflowRules(c.Ctx(), tx, nil, copyWorkflowFrom, nil, []int64{r.ID}); err != nil {
				return err
			}
		}
		return nil
	})
}

// ---------------------------------------------------------------- 表示用

// roleItem はロールの一覧・選択肢の表示用（name は Role#name）。
type roleItem struct {
	ID      int64
	Name    string
	Builtin bool
	role    *domain.Role
	setable map[string]bool
}

// Setable は role.setable_permissions.include?(permission)。
func (r *roleItem) Setable(perm string) bool { return r.setable[perm] }

// Has は role.permissions.include?(permission)。
func (r *roleItem) Has(perm string) bool { return r.role.HasPermission(perm) }

func (a *App) roleItems(c *Req, roles []*domain.Role) []*roleItem {
	page := c.Page()
	out := make([]*roleItem, len(roles))
	for i, r := range roles {
		set := map[string]bool{}
		for _, p := range r.SetablePermissions() {
			set[p.Name] = true
		}
		out[i] = &roleItem{ID: r.ID, Name: helper.RoleName(page, r), Builtin: r.IsBuiltin(), role: r, setable: set}
	}
	return out
}

// formData は roles/new・edit の描画データ。
func (a *App) roleFormData(c *Req, f *roleForm, copyFrom *domain.Role) (map[string]any, error) {
	ctx := c.Ctx()
	givable, err := repository.GivableRoles(ctx, a.DB)
	if err != nil {
		return nil, err
	}
	acts, err := repository.ListEnumerations(ctx, a.DB, domain.EnumTimeEntryActivity, true)
	if err != nil {
		return nil, err
	}
	acts = slices.DeleteFunc(acts, func(e *domain.Enumeration) bool { return !e.Active })
	trackers, err := repository.ListTrackers(ctx, a.DB)
	if err != nil {
		return nil, err
	}
	data := map[string]any{
		"Role":         f,
		"GivableRoles": givable,
		"Activities":   acts,
		"Trackers":     trackers,
		"IssuesVisibilityOptions": [][]any{{c.L("label_issues_visibility_all"), "all"},
			{c.L("label_issues_visibility_public"), "default"}, {c.L("label_issues_visibility_own"), "own"}},
		"TimeEntriesVisibilityOptions": [][]any{{c.L("label_time_entries_visibility_all"), "all"},
			{c.L("label_time_entries_visibility_own"), "own"}},
		"UsersVisibilityOptions": [][]any{{c.L("label_users_visibility_all"), "all"},
			{c.L("label_users_visibility_members_of_visible_projects"), "members_of_visible_projects"}},
	}
	setable := f.SetablePermissions()
	data["PermissionGroups"] = groupPermissions(c, setable)
	var tps []string
	for _, p := range domain.TrackerPermissions {
		if slices.ContainsFunc(setable, func(x *permission.Permission) bool { return x.Name == p }) {
			tps = append(tps, p)
		}
	}
	data["TrackerPermissions"] = tps
	if f.ID == 0 {
		all, err := repository.ListRoles(ctx, a.DB)
		if err != nil {
			return nil, err
		}
		data["CopyRoles"] = a.roleItems(c, all)
		var sel any
		if v := c.Params().String("copy_workflow_from"); v != "" {
			sel = v
		} else if copyFrom != nil {
			sel = copyFrom.ID
		}
		data["CopyWorkflowFrom"] = sel
	}
	return data, nil
}

func (a *App) renderRoleForm(c *Req, tmpl string, f *roleForm, copyFrom *domain.Role) {
	data, err := a.roleFormData(c, f, copyFrom)
	if err != nil {
		a.internalError(c, "role form", err)
		return
	}
	c.renderAdmin(tmpl, data, false)
}

// ---------------------------------------------------------------- アクション

// RolesIndex は roles#index（GET /roles）。
func (a *App) RolesIndex(c *Req) {
	switch httpx.Negotiate(c.R, "html", "xml", "json") {
	case "html":
		roles, err := repository.ListRoles(c.Ctx(), a.DB)
		if err != nil {
			a.internalError(c, "list roles", err)
			return
		}
		a.renderRolesIndex(c, roles, httpx.IsXHR(c.R))
	case "xml", "json":
		roles, err := repository.GivableRoles(c.Ctx(), a.DB)
		if err != nil {
			a.internalError(c, "list roles", err)
			return
		}
		a.renderRolesAPI(c, roles)
	default:
		c.unknownFormat()
	}
}

func (a *App) renderRolesIndex(c *Req, roles []*domain.Role, noLayout bool) {
	wf, err := repository.RoleIDsWithWorkflow(c.Ctx(), a.DB)
	if err != nil {
		a.internalError(c, "role workflows", err)
		return
	}
	c.renderAdmin("roles/index", map[string]any{"Roles": roles, "WorkflowRoles": wf}, noLayout)
}

// RolesShow は roles#show（GET /roles/:id。API のみ）。
func (a *App) RolesShow(c *Req) {
	switch httpx.Negotiate(c.R, "xml", "json") {
	case "xml", "json":
		a.renderRoleAPI(c, c.local(ctxRole).(*domain.Role))
	default:
		c.unknownFormat()
	}
}

// RolesNew は roles#new（GET /roles/new）。
func (a *App) RolesNew(c *Req) {
	ctx := c.Ctx()
	role := &domain.Role{Assignable: true, IssuesVisibility: domain.IssuesVisibilityDefault,
		UsersVisibility: domain.UsersVisibilityMembersOfVisibleProjects, TimeEntriesVisibility: domain.TimeEntriesVisibilityAll,
		AllRolesManaged: true, Permissions: []string{}}
	f := newRoleForm(c, role)
	if p := c.Params().Map("role"); p != nil {
		f.assign(p)
	} else {
		// Prefills the form with 'Non member' role permissions by default
		nm, err := repository.BuiltinRole(ctx, a.DB, domain.RoleBuiltinNonMember)
		if err != nil {
			a.internalError(c, "non member role", err)
			return
		}
		f.Permissions = slices.Clone(nm.Permissions)
	}
	var copyFrom *domain.Role
	if v := c.Params().String("copy"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			if src, err := repository.GetRole(ctx, a.DB, id); err == nil {
				copyFrom = src
				f.copyFrom(src)
			}
		}
	}
	a.renderRoleForm(c, "roles/new", f, copyFrom)
}

// RolesCreate は roles#create（POST /roles）。
func (a *App) RolesCreate(c *Req) {
	role := &domain.Role{Assignable: true, IssuesVisibility: domain.IssuesVisibilityDefault,
		UsersVisibility: domain.UsersVisibilityMembersOfVisibleProjects, TimeEntriesVisibility: domain.TimeEntriesVisibilityAll,
		AllRolesManaged: true, Permissions: []string{}}
	f := newRoleForm(c, role)
	f.assign(c.Params().Map("role"))
	if err := a.validateRole(c, f, nil); err != nil {
		a.internalError(c, "validate role", err)
		return
	}
	if !f.errs.Any() {
		var copyFrom *int64
		if v := c.Params().String("copy_workflow_from"); v != "" {
			if id, err := strconv.ParseInt(v, 10, 64); err == nil {
				if _, err := repository.GetRole(c.Ctx(), a.DB, id); err == nil {
					copyFrom = &id
				}
			}
		}
		if err := a.saveRole(c, f, nil, copyFrom); err != nil {
			a.internalError(c, "save role", err)
			return
		}
		c.Flash().SetNotice(c.L("notice_successful_create"))
		c.Redirect("/roles")
		return
	}
	a.renderRoleForm(c, "roles/new", f, nil)
}

// RolesEdit は roles#edit（GET /roles/:id/edit）。
func (a *App) RolesEdit(c *Req) {
	f := newRoleForm(c, c.local(ctxRole).(*domain.Role))
	a.renderRoleForm(c, "roles/edit", f, nil)
}

// RolesUpdate は roles#update（PATCH/PUT /roles/:id）。
func (a *App) RolesUpdate(c *Req) {
	orig := c.local(ctxRole).(*domain.Role)
	cp := *orig
	f := newRoleForm(c, &cp)
	oldPos := orig.Position
	f.assign(c.Params().Map("role"))
	format := httpx.Negotiate(c.R, "html", "js")
	if err := a.validateRole(c, f, orig); err != nil {
		a.internalError(c, "validate role", err)
		return
	}
	if !f.errs.Any() {
		if err := a.saveRole(c, f, &oldPos, nil); err != nil {
			a.internalError(c, "save role", err)
			return
		}
		if format == "js" {
			c.head(http.StatusOK)
			return
		}
		c.Flash().SetNotice(c.L("notice_successful_update"))
		c.Redirect(pagePath(c, "/roles"))
		return
	}
	if format == "js" {
		c.head(http.StatusUnprocessableEntity)
		return
	}
	a.renderRoleForm(c, "roles/edit", f, nil)
}

// RolesDestroy は roles#destroy（DELETE /roles/:id）。
func (a *App) RolesDestroy(c *Req) {
	role := c.local(ctxRole).(*domain.Role)
	err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
		if err := repository.DestroyRole(c.Ctx(), tx, role.ID); err != nil {
			return err
		}
		return repository.RemovePosition(c.Ctx(), tx, repository.PositionScope{Table: "roles", Where: "builtin = ?", Args: []any{role.Builtin}}, role.ID, role.Position)
	})
	if err == nil {
		c.Redirect("/roles")
		return
	}
	if !errors.Is(err, repository.ErrRoleNotDeletable) {
		a.logger().Error("destroy role", "err", err)
	}
	msg := c.L("error_can_not_remove_role")
	projects, perr := repository.LoadProjects(c.Ctx(), a.DB,
		`projects.id IN (SELECT m.project_id FROM members m JOIN member_roles mr ON mr.member_id = m.id WHERE mr.role_id = ?)`, role.ID)
	if perr != nil {
		a.internalError(c, "role projects", perr)
		return
	}
	if len(projects) > 0 {
		links := make([]string, len(projects))
		for i, p := range projects {
			links[i] = string(rails.LinkTo(p.Name, "/projects/"+p.Identifier+"/settings/members", nil))
		}
		msg += c.L("error_can_not_remove_role_reason_members_html", map[string]any{"projects": strings.Join(links, ", ")})
	}
	c.Flash().Now("error", msg)
	roles, lerr := repository.ListRoles(c.Ctx(), a.DB)
	if lerr != nil {
		a.internalError(c, "list roles", lerr)
		return
	}
	a.renderRolesIndex(c, roles, false)
}

// RolesPermissions は roles#permissions（GET /roles/permissions[.csv]）。
func (a *App) RolesPermissions(c *Req) {
	ctx := c.Ctx()
	all, err := repository.ListRoles(ctx, a.DB)
	if err != nil {
		a.internalError(c, "list roles", err)
		return
	}
	roles := all
	if ids := c.Params().Strings("ids"); len(ids) > 0 && c.Params().Present("ids") {
		want := map[int64]bool{}
		for _, s := range ids {
			want[httpx.RubyToI(s)] = true
		}
		roles = nil
		for _, r := range all {
			if want[r.ID] {
				roles = append(roles, r)
			}
		}
	}
	var perms []*permission.Permission
	for _, p := range permission.All() {
		if !p.Public {
			perms = append(perms, p)
		}
	}
	items := a.roleItems(c, roles)
	switch httpx.Negotiate(c.R, "html", "csv") {
	case "csv":
		a.sendPermissionsCSV(c, items, perms)
		return
	case "html":
	default:
		c.unknownFormat()
		return
	}
	selected := map[int64]bool{}
	for _, r := range roles {
		selected[r.ID] = true
	}
	c.renderAdmin("roles/permissions", map[string]any{
		"AllRoles":         a.roleItems(c, all),
		"Roles":            items,
		"SelectedRoleIDs":  selected,
		"PermissionGroups": groupPermissions(c, perms),
		"CSVPath":          csvPathWithQuery(c, "/roles/permissions.csv"),
	}, false)
}

// RolesUpdatePermissions は roles#update_permissions（POST /roles/permissions）。
func (a *App) RolesUpdatePermissions(c *Req) {
	ctx := c.Ctx()
	m := c.Params().Map("permissions")
	if m == nil {
		a.internalError(c, "update permissions", errors.New("params[:permissions] is missing"))
		return
	}
	err := a.DB.WithTx(ctx, func(tx *db.Tx) error {
		for _, k := range m.Keys() {
			id, err := strconv.ParseInt(k, 10, 64)
			if err != nil {
				continue
			}
			role, err := repository.GetRole(ctx, tx, id)
			if errors.Is(err, repository.ErrNotFound) {
				continue
			} else if err != nil {
				return err
			}
			f := newRoleForm(c, role)
			f.setPermissions(m.Strings(k))
			if err := repository.SetRolePermissions(ctx, tx, role.ID, f.Permissions, role.RestrictedTrackers); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		a.internalError(c, "update permissions", err)
		return
	}
	c.Flash().SetNotice(c.L("notice_successful_update"))
	c.Redirect("/roles")
}

// csvPathWithQuery は link_to_with_query_parameters 'CSV'（page / format 以外のクエリを引き継ぐ）。
func csvPathWithQuery(c *Req, path string) string {
	q := c.R.URL.Query()
	q.Del("page")
	q.Del("format")
	if s := encodeQueryRails(q); s != "" {
		return path + "?" + s
	}
	return path
}

// sendPermissionsCSV は RolesHelper#permissions_to_csv。
func (a *App) sendPermissionsCSV(c *Req, roles []*roleItem, perms []*permission.Permission) {
	header := []string{c.L("field_cvs_module"), c.L("label_permissions")}
	for _, r := range roles {
		header = append(header, r.Name)
	}
	rows := [][]string{header}
	for _, g := range groupPermissions(c, perms) {
		for _, p := range g.Permissions {
			row := []string{c.Loc.LOrHumanize(p.Module, "project_module_"), c.Loc.LOrHumanize(p.Name, "permission_")}
			for _, r := range roles {
				switch {
				case !r.Setable(p.Name):
					row = append(row, "")
				case r.Has(p.Name):
					row = append(row, c.L("general_text_Yes"))
				default:
					row = append(row, c.L("general_text_No"))
				}
			}
			rows = append(rows, row)
		}
	}
	// permissions_to_csv は Redmine::Export::CSV.generate(:encoding => params[:encoding]) のみ（区切り文字は既定）
	c.sendCSV("permissions.csv", rows, false)
}
