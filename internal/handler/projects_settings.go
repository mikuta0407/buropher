// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/permission"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/urlroot"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは projects#new / create / settings / update と、設定画面の各タブ
// （projects/settings/_*.html.erb）の描画データ。

// ---------------------------------------------------------------- new / create

// projectFormData は projects/_form の描画データ（@project・@trackers・@issue_custom_fields など）。
func (a *App) projectFormData(c *Req, f *projectForm) (map[string]any, error) {
	ctx := c.Ctx()
	vis, err := a.visibleCustomFieldValues(c, f.Project, f.CFValues)
	if err != nil {
		return nil, err
	}
	f.VisibleCFValues = vis
	if err := a.computeSafe(c, f); err != nil {
		return nil, err
	}
	allowed, withNil, err := a.allowedParents(c, f)
	if err != nil {
		return nil, err
	}
	data := map[string]any{"Project": f}
	if len(allowed) > 0 {
		tag, err := a.parentProjectSelectTag(c, f)
		if err != nil {
			return nil, err
		}
		data["ParentSelect"] = tag
	}
	_ = withNil
	trackers, err := repository.ListTrackers(ctx, a.DB)
	if err != nil {
		return nil, err
	}
	data["Trackers"] = trackers
	icfs, err := repository.CustomFieldInfosByKind(ctx, a.DB, "issue")
	if err != nil {
		return nil, err
	}
	data["IssueCustomFields"] = icfs
	data["Modules"] = projectModuleItems(c)
	// 非管理者が inherit_members の親のメンバーなら確認ダイアログ
	if !c.User.IsAdmin() && f.InheritMembers && f.ParentID != nil {
		if m, err := c.Authz().Membership(ctx, *f.ParentID); err != nil {
			return nil, err
		} else if m != nil {
			data["ConfirmInheritMembers"] = true
		}
	}
	return data, nil
}

// projectModuleItem は Redmine::AccessControl.available_project_modules の 1 項目。
type projectModuleItem struct {
	Name  string
	Label string
}

func projectModuleItems(c *Req) []projectModuleItem {
	var out []projectModuleItem
	for _, m := range availableProjectModules() {
		out = append(out, projectModuleItem{Name: m, Label: c.Loc.LOrHumanize(m, "project_module_")})
	}
	return out
}

// ProjectsNew は projects#new（GET /projects/new）。
func (a *App) ProjectsNew(c *Req) {
	f, err := a.newProjectForm(c)
	if err != nil {
		a.internalError(c, "new project", err)
		return
	}
	if err := a.assignProject(c, f, c.Params().Map("project")); err != nil {
		a.internalError(c, "assign project", err)
		return
	}
	// safe_attributes= は new_record では常に親を検査する（エラーは表示しない）
	f.errs = &domain.ValidationErrors{}
	a.renderProjectNew(c, f)
}

func (a *App) renderProjectNew(c *Req, f *projectForm) {
	c.NewRecordProject = true
	c.NewProjectName, c.NewProjectIdentifier = f.Project.Name, f.Project.Identifier
	data, err := a.projectFormData(c, f)
	if errors.Is(err, errParentNotFound) {
		c.renderPublic404()
		return
	} else if err != nil {
		a.internalError(c, "project form", err)
		return
	}
	c.Render("projects/new", data)
}

// ProjectsCreate は projects#create（POST /projects）。
func (a *App) ProjectsCreate(c *Req) {
	f, err := a.newProjectForm(c)
	if err != nil {
		a.internalError(c, "new project", err)
		return
	}
	if err := a.assignProject(c, f, c.Params().Map("project")); err != nil {
		a.internalError(c, "assign project", err)
		return
	}
	if err := a.validateProject(c, f); err != nil {
		a.internalError(c, "validate project", err)
		return
	}
	api := httpx.IsAPIRequest(c.R)
	if f.errs.Any() {
		if api {
			c.RenderAPIErrorsMin(f.errs.FullMessages(c.L)...)
			return
		}
		a.renderProjectNew(c, f)
		return
	}
	err = a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
		if err := a.saveProject(c, f, tx); err != nil {
			return err
		}
		if !c.User.IsAdmin() {
			// add_default_member
			r, err := a.defaultMemberRole(c)
			if err != nil {
				return err
			}
			if r != nil {
				if _, err := repository.CreateMember(c.Ctx(), tx, f.Project.ID, c.User.ID, []int64{r.ID}); err != nil &&
					!errors.Is(err, repository.ErrMemberTaken) {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, repository.ErrInvalidParent) {
			f.errs.Add("parent_id", "invalid", nil)
			a.renderProjectNew(c, f)
			return
		}
		a.internalError(c, "create project", err)
		return
	}
	c.ResetAuthz()
	p, err := repository.GetProject(c.Ctx(), a.DB, f.Project.ID)
	if err != nil {
		a.internalError(c, "reload project", err)
		return
	}
	// after_action :record_project_usage は作成したプロジェクトに対して行われる
	c.Project = p
	if api {
		c.W.Header().Set("Location", httpx.RequestBaseURL(c.R)+urlroot.Path("/projects/"+strconv.FormatInt(p.ID, 10)))
		a.renderProjectShowAPI(c, p, http.StatusCreated)
		return
	}
	c.Flash().SetNotice(c.L("notice_successful_create"))
	if c.Params().Present("continue") {
		if p.ParentID != nil {
			c.Redirect("/projects/new?parent_id=" + strconv.FormatInt(*p.ParentID, 10))
		} else {
			c.Redirect("/projects/new")
		}
		return
	}
	c.Redirect("/projects/" + p.Identifier + "/settings")
}

// ---------------------------------------------------------------- settings / update

// ProjectsSettings は projects#settings（GET /projects/:id/settings(/:tab)）。
func (a *App) ProjectsSettings(c *Req) {
	f, err := a.loadProjectForm(c, c.Project)
	if err != nil {
		a.internalError(c, "project form", err)
		return
	}
	a.renderProjectSettings(c, f)
}

// ProjectsUpdate は projects#update（PATCH/PUT /projects/:id）。
func (a *App) ProjectsUpdate(c *Req) {
	f, err := a.loadProjectForm(c, c.Project)
	if err != nil {
		a.internalError(c, "project form", err)
		return
	}
	if err := a.assignProject(c, f, c.Params().Map("project")); err != nil {
		a.internalError(c, "assign project", err)
		return
	}
	if err := a.validateProject(c, f); err != nil {
		a.internalError(c, "validate project", err)
		return
	}
	api := httpx.IsAPIRequest(c.R)
	if !f.errs.Any() {
		err = a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error { return a.saveProject(c, f, tx) })
		if errors.Is(err, repository.ErrInvalidParent) {
			f.errs.Add("parent_id", "invalid", nil)
		} else if err != nil {
			a.internalError(c, "update project", err)
			return
		}
	}
	if !f.errs.Any() {
		if api {
			c.RenderAPIOKMin()
			return
		}
		c.Flash().SetNotice(c.L("notice_successful_update"))
		u := "/projects/" + f.Project.Identifier + "/settings"
		if tab := c.Params().String("tab"); tab != "" {
			u += "/" + tab
		}
		c.Redirect(u)
		return
	}
	if api {
		c.RenderAPIErrorsMin(f.errs.FullMessages(c.L)...)
		return
	}
	// render :action => 'settings'（レイアウトの @project は代入後の値。ジャンプボックスは name_was）
	c.ProjectNameWas = c.Project.Name
	c.Project = f.Project
	a.renderProjectSettings(c, f)
}

// settingsMember は projects/settings/_members の 1 行。
type settingsMember struct {
	*repository.MemberPrincipal
	RoleNames string
	Deletable bool
	// ConfirmOwn は非管理者が自分を含むメンバーを削除する場合の確認。
	ConfirmOwn bool
}

// settingsVersion は projects/settings/_versions の 1 行。
type settingsVersion struct {
	*repository.VersionInfo
	Shared    bool
	IsDefault bool
	// Label は link_to_version の表示名（共有なら "プロジェクト - 名前"）。
	Label        string
	SharingLabel string
	StatusLabel  string
	WikiLink     template.HTML
	// Visible は version.visible?（バージョンのプロジェクトで view_issues）。
	Visible bool
}

// settingsActivity は projects/settings/_activities の 1 行。
type settingsActivity struct {
	*domain.Enumeration
	System   bool
	CFValues []*domain.CustomFieldValue
}

// project_settings_tabs の定義（ProjectsHelper#project_settings_tabs）。
var projectSettingsTabDefs = []struct {
	name, action, module, partial, label string
}{
	{"info", "edit_project", "", "projects/edit", "label_project"},
	{"members", "manage_members", "", "projects/settings/members", "label_member_plural"},
	{"issues", "edit_project", "issue_tracking", "projects/settings/issues", "label_issue_tracking"},
	{"versions", "manage_versions", "", "projects/settings/versions", "label_version_plural"},
	{"categories", "manage_categories", "", "projects/settings/issue_categories", "label_issue_category_plural"},
	{"repositories", "manage_repository", "", "projects/settings/repositories", "label_repository_plural"},
	{"boards", "manage_boards", "", "projects/settings/boards", "label_board_plural"},
	{"activities", "manage_project_activities", "", "projects/settings/activities", "label_time_tracking"},
}

// projectSettingsTabs は project_settings_tabs。
func projectSettingsTabs(c *Req, p *domain.Project) []helper.Tab {
	var tabs []helper.Tab
	for _, d := range projectSettingsTabDefs {
		if !c.AllowedTo(domain.Perm(d.action), p) {
			continue
		}
		if d.module != "" && !p.ModuleEnabled(d.module) {
			continue
		}
		u := "/projects/" + p.Identifier + "/settings/" + d.name
		if c.Action != "settings" {
			// url_for(:tab => name) は現在のアクション（update）のルートで生成される
			u = "/projects/" + p.Identifier + "?tab=" + d.name
		}
		if d.name == "versions" {
			// :url => {:tab => 'versions', :version_status => params[:version_status], :version_name => params[:version_name]}
			q := []string{}
			if s, ok := c.Params().StringOK("version_name"); ok {
				q = append(q, "version_name="+urlEncode(s))
			}
			if s, ok := c.Params().StringOK("version_status"); ok {
				q = append(q, "version_status="+urlEncode(s))
			}
			if len(q) > 0 {
				sep := "?"
				if strings.Contains(u, "?") {
					sep = "&"
				}
				u += sep + strings.Join(q, "&")
			}
		}
		tabs = append(tabs, helper.Tab{Name: d.name, Label: d.label, Partial: d.partial, URL: u})
	}
	return tabs
}

// renderProjectSettings は projects/settings の描画（全タブのデータを読み込む）。
func (a *App) renderProjectSettings(c *Req, f *projectForm) {
	data, err := a.projectSettingsData(c, f)
	if errors.Is(err, errParentNotFound) {
		c.renderPublic404()
		return
	} else if err != nil {
		a.internalError(c, "project settings", err)
		return
	}
	c.Render("projects/settings", data)
}

func (a *App) projectSettingsData(c *Req, f *projectForm) (map[string]any, error) {
	ctx := c.Ctx()
	p := c.Project
	data, err := a.projectFormData(c, f)
	if err != nil {
		return nil, err
	}
	tabs := projectSettingsTabs(c, p)
	data["Tabs"] = tabs
	data["SelectedTab"] = c.Params().String("tab")
	has := func(name string) bool {
		return slices.ContainsFunc(tabs, func(t helper.Tab) bool { return t.Name == name })
	}
	page := c.Page()
	if has("members") {
		members, err := a.settingsMembers(c, p)
		if err != nil {
			return nil, err
		}
		data["Members"] = members
	}
	if has("issues") {
		allIDs, err := a.allIssueCustomFieldIDs(c, f)
		if err != nil {
			return nil, err
		}
		data["AllIssueCustomFieldIDs"] = allIDs
		vopts, err := a.projectDefaultVersionOptions(c, p)
		if err != nil {
			return nil, err
		}
		data["DefaultVersionOptions"] = vopts
		aopts, err := a.projectDefaultAssignedToOptions(c, page, p)
		if err != nil {
			return nil, err
		}
		data["DefaultAssignedToOptions"] = aopts
		qopts, err := a.projectDefaultIssueQueryOptions(c, p)
		if err != nil {
			return nil, err
		}
		data["DefaultIssueQueryOptions"] = qopts
	}
	if has("versions") {
		status := "open"
		if v, ok := c.Params().StringOK("version_status"); ok {
			status = v
		}
		name := c.Params().String("version_name")
		vs, err := repository.ProjectSharedVersions(ctx, a.DB, p, status, name)
		if err != nil {
			return nil, err
		}
		var rows []settingsVersion
		for _, v := range vs {
			rows = append(rows, a.settingsVersionRow(c, p, v))
		}
		data["Versions"] = rows
		data["VersionStatus"] = status
		data["VersionName"] = name
		data["VersionStatusOptions"] = []any{
			[]any{c.L("label_all"), ""},
			[]any{c.L("version_status_open"), "open"},
			[]any{c.L("version_status_locked"), "locked"},
			[]any{c.L("version_status_closed"), "closed"},
		}
		data["CanManageVersions"] = c.AllowedTo(domain.Perm("manage_versions"), p)
	}
	if has("categories") {
		cats, err := repository.ProjectIssueCategories(ctx, a.DB, p.ID)
		if err != nil {
			return nil, err
		}
		var ids []int64
		for _, cat := range cats {
			if cat.AssignedToID != nil {
				ids = append(ids, *cat.AssignedToID)
			}
		}
		users, groups, err := repository.PrincipalsByIDs(ctx, a.DB, ids)
		if err != nil {
			return nil, err
		}
		names := map[int64]string{}
		for id, u := range users {
			names[id] = helper.PrincipalName(page, u)
		}
		for id, g := range groups {
			names[id] = helper.PrincipalName(page, g)
		}
		data["Categories"] = cats
		data["CategoryAssignees"] = names
		data["CanManageCategories"] = c.AllowedTo(domain.Perm("manage_categories"), p)
	}
	if has("repositories") {
		repos, err := repository.ProjectRepositoriesForSettings(ctx, a.DB, p.ID)
		if err != nil {
			return nil, err
		}
		data["Repositories"] = repos
		data["CanManageRepository"] = c.AllowedTo(domain.Perm("manage_repository"), p)
	}
	if has("boards") {
		boards, err := repository.ProjectBoards(ctx, a.DB, p.ID)
		if err != nil {
			return nil, err
		}
		data["Boards"] = boards
		data["BoardsTree"] = renderBoardsTree(boards)
		data["CanManageBoards"] = c.AllowedTo(domain.Perm("manage_boards"), p)
	}
	if has("activities") {
		acts, err := a.settingsActivities(c, p)
		if err != nil {
			return nil, err
		}
		data["Activities"] = acts
		cfs, err := repository.CustomFieldInfosByKind(ctx, a.DB, "time_entry_activity")
		if err != nil {
			return nil, err
		}
		data["ActivityCustomFields"] = cfs
	}
	return data, nil
}

// settingsMembers は @project.memberships.sorted（ロールの position、principal の種別降順・名前順）。
func (a *App) settingsMembers(c *Req, p *domain.Project) ([]settingsMember, error) {
	ctx := c.Ctx()
	members, err := repository.ProjectMembersWithPrincipals(ctx, a.DB, p.ID, false)
	if err != nil {
		return nil, err
	}
	allRoles, err := repository.ListRoles(ctx, a.DB)
	if err != nil {
		return nil, err
	}
	roleByID := map[int64]*domain.Role{}
	for _, r := range allRoles {
		roleByID[r.ID] = r
	}
	managed, err := c.Authz().ManagedRoles(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	page := c.Page()
	uf := a.Settings.String("user_format")
	minPos := func(m *repository.MemberPrincipal) int {
		pos := int(^uint(0) >> 1)
		for _, id := range m.Member.RoleIDs() {
			if r := roleByID[id]; r != nil && r.Position < pos {
				pos = r.Position
			}
		}
		return pos
	}
	sort.SliceStable(members, func(i, j int) bool {
		pi, pj := minPos(members[i]), minPos(members[j])
		if pi != pj {
			return pi < pj
		}
		ki, kj := principalTypeName(members[i]), principalTypeName(members[j])
		if ki != kj {
			return ki > kj
		}
		fi, fj := principalOrderFields(members[i], uf), principalOrderFields(members[j], uf)
		for k := range fi {
			if fi[k] != fj[k] {
				return fi[k] < fj[k]
			}
		}
		return members[i].Member.PrincipalID < members[j].Member.PrincipalID
	})
	var out []settingsMember
	for _, m := range members {
		if m.Principal() == nil {
			continue
		}
		var roles []*domain.Role
		for _, id := range m.Member.RoleIDs() {
			if r := roleByID[id]; r != nil {
				roles = append(roles, r)
			}
		}
		domain.SortRoles(roles)
		names := make([]string, len(roles))
		for i, r := range roles {
			names[i] = helper.RoleName(page, r)
		}
		sm := settingsMember{MemberPrincipal: m, RoleNames: strings.Join(names, ", ")}
		// Member#deletable?: 継承ロールが無く、ロールがすべて管理可能
		if !m.Member.AnyInheritedRole() {
			ok := true
			for _, r := range roles {
				if !slices.ContainsFunc(managed, func(x *domain.Role) bool { return x.ID == r.ID }) {
					ok = false
				}
			}
			sm.Deletable = ok
		}
		if !c.User.IsAdmin() {
			inc, err := a.memberIncludesUser(c, m, c.User.ID)
			if err != nil {
				return nil, err
			}
			sm.ConfirmOwn = inc
		}
		out = append(out, sm)
	}
	return out, nil
}

// principalTypeName は users.type（並べ替えの type DESC 用）。
func principalTypeName(m *repository.MemberPrincipal) string {
	if p := m.Principal(); p != nil {
		return p.Kind.RedmineType()
	}
	return ""
}

// principalOrderFields は User.name_formatter[:order] - ['id'] + ['lastname'] の値（グループは名前が lastname）。
func principalOrderFields(m *repository.MemberPrincipal, userFormat string) []string {
	p := m.Principal()
	first, last := p.Firstname, p.Lastname
	if m.Group != nil {
		first, last = "", p.Name
	}
	switch userFormat {
	case "lastname_firstname", "lastname_comma_firstname", "lastnamefirstname":
		return []string{last, first, last}
	case "lastname":
		return []string{last, last}
	case "firstname":
		return []string{first, last}
	case "username":
		login := ""
		if m.User != nil {
			login = m.User.Login
		}
		return []string{login, last}
	}
	return []string{first, last, last}
}

// memberIncludesUser は Member#include?(user)。
func (a *App) memberIncludesUser(c *Req, m *repository.MemberPrincipal, userID int64) (bool, error) {
	if m.Member.PrincipalID == userID {
		return true, nil
	}
	if m.Group != nil {
		ids, err := repository.GroupUserIDs(c.Ctx(), a.DB, m.Group.ID)
		if err != nil {
			return false, err
		}
		return slices.Contains(ids, userID), nil
	}
	return false, nil
}

// allIssueCustomFieldIDs は @project.all_issue_custom_fields.ids（is_for_all または関連付け済み）。
func (a *App) allIssueCustomFieldIDs(c *Req, f *projectForm) ([]int64, error) {
	cfs, err := repository.CustomFieldInfosByKind(c.Ctx(), a.DB, "issue")
	if err != nil {
		return nil, err
	}
	var out []int64
	for _, cf := range cfs {
		if cf.IsForAll || slices.Contains(f.IssueCustomFieldIDs, cf.ID) {
			out = append(out, cf.ID)
		}
	}
	return out, nil
}

// projectDefaultVersionOptions は project_default_version_options(project)。
func (a *App) projectDefaultVersionOptions(c *Req, p *domain.Project) (template.HTML, error) {
	ctx := c.Ctx()
	vs, err := repository.ProjectOpenSharedVersions(ctx, a.DB, p)
	if err != nil {
		return "", err
	}
	if p.DefaultVersionID != nil && !slices.ContainsFunc(vs, func(v *repository.VersionInfo) bool { return v.ID == *p.DefaultVersionID }) {
		if v, err := repository.GetVersionInfo(ctx, a.DB, *p.DefaultVersionID); err == nil {
			vs = append(vs, v)
		} else if !errors.Is(err, repository.ErrNotFound) {
			return "", err
		}
	}
	return versionOptionsForSelect(vs, derefID(p.DefaultVersionID)), nil
}

// versionOptionsForSelect は ProjectsHelper#version_options_for_select（プロジェクトが複数ならグループ化）。
func versionOptionsForSelect(vs []*repository.VersionInfo, selected any) template.HTML {
	var keys []string
	groups := map[string][]any{}
	for _, v := range vs {
		if _, ok := groups[v.ProjectName]; !ok {
			keys = append(keys, v.ProjectName)
		}
		groups[v.ProjectName] = append(groups[v.ProjectName], []any{v.Name, v.ID})
	}
	if len(keys) > 1 {
		var grouped []any
		for _, k := range keys {
			grouped = append(grouped, []any{k, groups[k]})
		}
		return rails.GroupedOptionsForSelect(grouped, selected, nil)
	}
	var items []any
	if len(keys) == 1 {
		items = groups[keys[0]]
	}
	return rails.OptionsForSelect(items, selected)
}

// projectDefaultAssignedToOptions は project_default_assigned_to_options(project)。
func (a *App) projectDefaultAssignedToOptions(c *Req, page *helper.Page, p *domain.Project) (template.HTML, error) {
	ctx := c.Ctx()
	ids, err := repository.ProjectAssignableUserIDs(ctx, a.DB, p.ID, a.Settings.Bool("issue_group_assignment"))
	if err != nil {
		return "", err
	}
	if p.DefaultAssignedToID != nil && !slices.Contains(ids, *p.DefaultAssignedToID) {
		ids = append(ids, *p.DefaultAssignedToID)
	}
	users, groups, err := repository.PrincipalsByIDs(ctx, a.DB, ids)
	if err != nil {
		return "", err
	}
	return principalsOptionsForSelect(c, page, users, groups, derefID(p.DefaultAssignedToID)), nil
}

// principalsOptionsForSelect は ApplicationHelper#principals_options_for_select。
func principalsOptionsForSelect(c *Req, page *helper.Page, users map[int64]*domain.User, groups map[int64]*domain.Group, selected any) template.HTML {
	type item struct {
		id    int64
		name  string
		group bool
	}
	var items []item
	hasMe := false
	for id, u := range users {
		if id == c.User.ID {
			hasMe = true
		}
		items = append(items, item{id, helper.PrincipalName(page, u), false})
	}
	for id, g := range groups {
		items = append(items, item{id, helper.PrincipalName(page, g), true})
	}
	// collection.sort（Principal#<=>: 同種は名前の casecmp、ユーザーが先）
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].group != items[j].group {
			return !items[i].group
		}
		if c := casecmp(items[i].name, items[j].name); c != 0 {
			return c < 0
		}
		return items[i].id < items[j].id
	})
	sel := rails.ToS(selected)
	var s, g strings.Builder
	if hasMe {
		s.WriteString(string(rails.ContentTag("option", "<< "+c.L("label_me")+" >>", rails.NewHash("value", c.User.ID))))
	}
	for _, it := range items {
		attr := ""
		if strconv.FormatInt(it.id, 10) == sel {
			attr = ` selected="selected"`
		}
		opt := `<option value="` + strconv.FormatInt(it.id, 10) + `"` + attr + `>` + string(rails.H(it.name)) + `</option>`
		if it.group {
			g.WriteString(opt)
		} else {
			s.WriteString(opt)
		}
	}
	if g.Len() > 0 {
		s.WriteString(`<optgroup label="` + string(rails.H(c.L("label_group_plural"))) + `">` + g.String() + `</optgroup>`)
	}
	return template.HTML(s.String())
}

// projectDefaultIssueQueryOptions は project_default_issue_query_options(project)。
func (a *App) projectDefaultIssueQueryOptions(c *Req, p *domain.Project) (template.HTML, error) {
	global, current, err := repository.PublicIssueQueries(c.Ctx(), a.DB, p.ID)
	if err != nil {
		return "", err
	}
	toItems := func(qs []repository.NamedID) []any {
		out := []any{}
		for _, q := range qs {
			out = append(out, []any{q.Name, q.ID})
		}
		return out
	}
	grouped := []any{
		[]any{c.L("label_default_queries.for_all_projects"), toItems(global)},
		[]any{c.L("label_default_queries.for_current_project"), toItems(current)},
	}
	return rails.GroupedOptionsForSelect(grouped, derefID(p.DefaultIssueQueryID), nil), nil
}

// settingsVersionRow はバージョン一覧の 1 行。
func (a *App) settingsVersionRow(c *Req, p *domain.Project, v *repository.VersionInfo) settingsVersion {
	sv := settingsVersion{VersionInfo: v, Shared: v.ProjectID != p.ID,
		IsDefault: p.DefaultVersionID != nil && *p.DefaultVersionID == v.ID}
	sv.Label = v.Name
	if sv.Shared {
		sv.Label = v.ProjectName + " - " + v.Name
	}
	sharing := v.Sharing
	switch sharing {
	case "none", "descendants", "hierarchy", "tree", "system":
	default:
		sharing = "none"
	}
	sv.SharingLabel = c.L("label_version_sharing_" + sharing)
	sv.StatusLabel = c.L("version_status_" + v.Status)
	vp := &domain.Project{ID: v.ProjectID, Identifier: v.ProjectIdentifier}
	if vproj, err := repository.GetProject(c.Ctx(), a.DB, v.ProjectID); err == nil {
		vp = vproj
	}
	sv.Visible = c.AllowedTo(domain.Perm("view_issues"), vp)
	if strings.TrimSpace(v.WikiPageTitle) != "" && v.ProjectHasWiki {
		title := wikiTitleize(v.WikiPageTitle)
		if c.AllowedTo(domain.ControllerAction("wiki", "show"), vp) {
			sv.WikiLink = rails.LinkTo(v.WikiPageTitle, "/projects/"+v.ProjectIdentifier+"/wiki/"+urlPathEscape(title), nil)
		} else {
			sv.WikiLink = rails.H(v.WikiPageTitle)
		}
	}
	return sv
}

// renderBoardsTree は ProjectsHelper#render_boards_tree の入れ子構造（親 → 子の順と深さ）。
func renderBoardsTree(boards []*repository.BoardInfo) []boardTreeNode {
	var walk func(parent *int64, level int) []boardTreeNode
	walk = func(parent *int64, level int) []boardTreeNode {
		var out []boardTreeNode
		for _, b := range boards {
			if !sameIDPtr(b.ParentID, parent) {
				continue
			}
			id := b.ID
			out = append(out, boardTreeNode{Board: b, Level: level, PaddingLeft: 2 + level*16, Children: walk(&id, level+1)})
		}
		return out
	}
	return walk(nil, 0)
}

// boardTreeNode は render_boards_tree の 1 ノード。
type boardTreeNode struct {
	Board       *repository.BoardInfo
	Level       int
	PaddingLeft int
	Children    []boardTreeNode
}

// settingsActivities は @project.activities(true) と各行のカスタムフィールド値。
func (a *App) settingsActivities(c *Req, p *domain.Project) ([]settingsActivity, error) {
	ctx := c.Ctx()
	acts, err := repository.ProjectActivities(ctx, a.DB, p.ID, true)
	if err != nil {
		return nil, err
	}
	cfs, err := repository.CustomFieldInfosByKind(ctx, a.DB, "time_entry_activity")
	if err != nil {
		return nil, err
	}
	var out []settingsActivity
	for _, e := range acts {
		vals, err := repository.CustomValues(ctx, a.DB, "enumeration", e.ID)
		if err != nil {
			return nil, err
		}
		sa := settingsActivity{Enumeration: e, System: e.ProjectID == nil}
		for _, cf := range cfs {
			sa.CFValues = append(sa.CFValues, &domain.CustomFieldValue{Field: cf, Values: vals[cf.ID]})
		}
		out = append(out, sa)
	}
	return out, nil
}

// availableProjectModules は Redmine::AccessControl.available_project_modules。
func availableProjectModules() []string { return permission.AvailableProjectModules() }

// urlEncode は CGI.escape。
func urlEncode(s string) string { return url.QueryEscape(s) }

// urlPathEscape は URL のパス部分のエスケープ。
func urlPathEscape(s string) string { return url.PathEscape(s) }
