package handler

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/validation"
)

// PrincipalMembershipsController（app/controllers/principal_memberships_controller.rb）。
// ユーザー・グループのプロジェクトのメンバーシップ（/users/:user_id/memberships, /groups/:group_id/memberships）。
var PrincipalMembershipsController = &Controller{Name: "principal_memberships", MainMenu: false}

// routesPrincipalMemberships は principal_memberships コントローラのルートを登録する。
func (a *App) routesPrincipalMemberships(r Router) {
	adm := RequireAdmin()
	for _, kind := range []string{"users", "groups"} {
		param := "user_id"
		if kind == "groups" {
			param = "group_id"
		}
		base := "/" + kind + "/{" + param + "}/memberships"
		// index / show はコントローラにアクションが無い（Rails では ActionNotFound の 404）
		a.Handle(r, http.MethodGet, base+"/new", PrincipalMembershipsController, "new", a.PrincipalMembershipsNew, adm, a.findPrincipal(param))
		a.Handle(r, http.MethodPost, base, PrincipalMembershipsController, "create", a.PrincipalMembershipsCreate, adm, a.findPrincipal(param))
		a.Handle(r, http.MethodGet, base+"/{id}/edit", PrincipalMembershipsController, "edit", a.PrincipalMembershipsEdit, adm, a.findMembership())
		for _, m := range []string{http.MethodPatch, http.MethodPut} {
			a.Handle(r, m, base+"/{id}", PrincipalMembershipsController, "update", a.PrincipalMembershipsUpdate, adm, a.findMembership())
		}
		a.Handle(r, http.MethodDelete, base+"/{id}", PrincipalMembershipsController, "destroy", a.PrincipalMembershipsDestroy, adm, a.findMembership())
	}
}

// principalRef は @principal（User か Group）。
type principalRef struct {
	User  *domain.User
	Group *domain.Group
}

// ID は principal の id。
func (p *principalRef) ID() int64 {
	if p.User != nil {
		return p.User.ID
	}
	return p.Group.ID
}

// IsGroup は principal.is_a?(Group)。
func (p *principalRef) IsGroup() bool { return p.Group != nil }

// Base は edit_polymorphic_path の基底（/users/1 または /groups/10）。
func (p *principalRef) Base() string {
	if p.Group != nil {
		return "/groups/" + itoa(p.Group.ID)
	}
	return "/users/" + itoa(p.User.ID)
}

// principalName は principal.to_s。
func (a *App) principalName(c *Req, p *principalRef) string {
	if p.Group != nil {
		return helper.GroupName(c.Page(), p.Group)
	}
	return p.User.Name(a.Settings.String("user_format"))
}

type principalCtxKey struct{}
type membershipCtxKey struct{}

// loadPrincipal は Principal.find(id)。
func (a *App) loadPrincipal(c *Req, id int64) (*principalRef, error) {
	pr, err := repository.GetPrincipal(c.Ctx(), a.DB, id)
	if err != nil {
		return nil, err
	}
	if pr.Kind.IsGroup() {
		g, err := repository.GetGroup(c.Ctx(), a.DB, id)
		if err != nil {
			return nil, err
		}
		return &principalRef{Group: g}, nil
	}
	u, err := repository.GetUser(c.Ctx(), a.DB, id)
	if err != nil {
		return nil, err
	}
	return &principalRef{User: u}, nil
}

// findPrincipal は PrincipalMembershipsController#find_principal（params[:user_id] || params[:group_id]）。
func (a *App) findPrincipal(param string) ActionOption {
	return Before(func(c *Req) {
		id, err := strconv.ParseInt(c.Params().String(param), 10, 64)
		if err != nil {
			c.Render404("")
			return
		}
		p, err := a.loadPrincipal(c, id)
		if err != nil {
			if !errors.Is(err, repository.ErrNotFound) {
				a.logger().Error("find principal", "err", err)
			}
			c.Render404("")
			return
		}
		c.setValue(principalCtxKey{}, p)
	})
}

// findMembership は PrincipalMembershipsController#find_membership（Member.find(params[:id])）。
func (a *App) findMembership() ActionOption {
	return Before(func(c *Req) {
		id, err := strconv.ParseInt(c.Params().String("id"), 10, 64)
		if err != nil {
			c.Render404("")
			return
		}
		m, err := repository.GetMember(c.Ctx(), a.DB, id)
		if err != nil {
			c.Render404("")
			return
		}
		p, err := a.loadPrincipal(c, m.PrincipalID)
		if err != nil {
			c.Render404("")
			return
		}
		c.setValue(membershipCtxKey{}, m)
		c.setValue(principalCtxKey{}, p)
	})
}

// membershipRow は principal_memberships/_index の 1 行（Member + project + roles）。
type membershipRow struct {
	Member  *domain.Member
	Project *domain.Project
	// Roles は member.roles（重複なし、Role#<=> 順）。
	Roles    []*domain.Role
	roleByID map[int64]*domain.Role
	// Deletable は membership.deletable?（継承ロールが無い。管理者は全ロールを管理できる）。
	Deletable bool
	// RolesText は roles.sort.collect(&:to_s).join(', ')。
	RolesText string
	// Level は project_tree(..., init_level: true) の深さ（users/show 用）。
	Level int
	// Inheritance は role_id → render_role_inheritance の表示内容。
	Inheritance map[int64]string
	// Valid は保存に成功したか（create.js / update.js 用）。
	Errors *validation.Errors
}

// HasRole は membership.roles.include?(role)。
func (r *membershipRow) HasRole(roleID int64) bool { return r.roleByID[roleID] != nil }

// RoleEditable は membership.role_editable?(role)（管理者は継承ロール以外を編集できる）。
func (r *membershipRow) RoleEditable(roleID int64) bool { return !r.Member.HasInheritedRole(roleID) }

// RoleInheritance は render_role_inheritance(membership, role)（無ければ ""）。
func (r *membershipRow) RoleInheritance(roleID int64) string { return r.Inheritance[roleID] }

// sortRowsByProject は sorted_by_project（projects.lft 順）。
func sortRowsByProject(rows []*membershipRow, ns map[int64]repository.NestedSetValue) {
	sort.SliceStable(rows, func(i, j int) bool {
		return ns[rows[i].Member.ProjectID].Lft < ns[rows[j].Member.ProjectID].Lft
	})
}

// membershipRows は principal.memberships（アーカイブされていないプロジェクト）をプロジェクトのツリー順に返す。
// projCond が空でなければ Project.visible_condition で絞る。
func (a *App) membershipRows(c *Req, principalID int64, projCond string, sortByProject bool) ([]*membershipRow, error) {
	ctx := c.Ctx()
	members, err := repository.Memberships(ctx, a.DB, principalID)
	if err != nil {
		return nil, err
	}
	ns, err := repository.ProjectNestedSet(ctx, a.DB)
	if err != nil {
		return nil, err
	}
	var visible map[int64]bool
	if projCond != "" {
		ps, err := repository.LoadProjects(ctx, a.DB, projCond)
		if err != nil {
			return nil, err
		}
		visible = map[int64]bool{}
		for _, p := range ps {
			visible[p.ID] = true
		}
	}
	var rows []*membershipRow
	for _, m := range members {
		if visible != nil && !visible[m.ProjectID] {
			continue
		}
		row, err := a.membershipRow(c, m)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	if sortByProject {
		sortRowsByProject(rows, ns)
	}
	return rows, nil
}

// setTreeLevels は Project.project_tree(projects, init_level: true) の深さを rows（lft 順）に設定する。
func (a *App) setTreeLevels(c *Req, rows []*membershipRow) error {
	if len(rows) == 0 || rows[0].Project == nil {
		return nil
	}
	ns, err := repository.ProjectNestedSet(c.Ctx(), a.DB)
	if err != nil {
		return err
	}
	sortRowsByProject(rows, ns)
	anc, err := repository.ProjectAncestors(c.Ctx(), a.DB, rows[0].Project.ID)
	if err != nil {
		return err
	}
	var stack []int64
	for _, p := range anc {
		stack = append(stack, p.ID)
	}
	sort.SliceStable(stack, func(i, j int) bool { return ns[stack[i]].Lft < ns[stack[j]].Lft })
	isDesc := func(id, of int64) bool { return ns[of].Lft < ns[id].Lft && ns[id].Rgt < ns[of].Rgt }
	for _, r := range rows {
		if r.Project == nil {
			continue
		}
		for len(stack) > 0 && !isDesc(r.Project.ID, stack[len(stack)-1]) {
			stack = stack[:len(stack)-1]
		}
		r.Level = len(stack)
		stack = append(stack, r.Project.ID)
	}
	return nil
}

// membershipRow は 1 件の membershipRow を作る。
func (a *App) membershipRow(c *Req, m *domain.Member) (*membershipRow, error) {
	ctx := c.Ctx()
	p, err := repository.GetProject(ctx, a.DB, m.ProjectID)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	roles, err := repository.RolesByIDs(ctx, a.DB, m.RoleIDs())
	if err != nil {
		return nil, err
	}
	helper.SortRoles(roles)
	row := &membershipRow{Member: m, Project: p, Roles: roles, roleByID: map[int64]*domain.Role{}, Inheritance: map[int64]string{}}
	var names []string
	for _, r := range roles {
		row.roleByID[r.ID] = r
		names = append(names, roleDisplayName(c, r))
	}
	row.RolesText = strings.Join(names, ", ")
	row.Deletable = !m.AnyInheritedRole()
	// role_inheritance: 継承元のメンバーが同じプロジェクトならその principal（グループ）、違えば親プロジェクト
	for _, mr := range m.MemberRoles {
		if mr.InheritedFrom == nil {
			continue
		}
		src, err := repository.MemberOfMemberRole(ctx, a.DB, *mr.InheritedFrom)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				continue
			}
			return nil, err
		}
		var label string
		if src.ProjectID == m.ProjectID {
			if g, err := repository.GetGroup(ctx, a.DB, src.PrincipalID); err == nil {
				label = c.L("label_inherited_from_group", map[string]any{"group": helper.GroupName(c.Page(), g)})
			} else if u, err := repository.GetUser(ctx, a.DB, src.PrincipalID); err == nil {
				label = c.L("label_inherited_from_group", map[string]any{"group": u.Name(a.Settings.String("user_format"))})
			}
		} else {
			label = c.L("label_inherited_from_parent_project")
		}
		if label == "" {
			continue
		}
		cur := row.Inheritance[mr.RoleID]
		if cur == "" {
			row.Inheritance[mr.RoleID] = label
		} else if !contains(strings.Split(cur, ", "), label) {
			row.Inheritance[mr.RoleID] = cur + ", " + label
		}
	}
	return row, nil
}

// membershipsData は principal_memberships/_index に必要な assigns。
func (a *App) membershipsData(c *Req, p *principalRef) (map[string]any, error) {
	rows, err := a.membershipRows(c, p.ID(), "", true)
	if err != nil {
		return nil, err
	}
	data := map[string]any{"Memberships": rows, "PrincipalBase": p.Base(), "Principal": p}
	if p.User != nil {
		data["User"] = p.User
	} else {
		data["Group"] = p.Group
	}
	return data, nil
}

// PrincipalMembershipsNew は principal_memberships#new（html / js）。
func (a *App) PrincipalMembershipsNew(c *Req) {
	p := c.value(principalCtxKey{}).(*principalRef)
	ctx := c.Ctx()
	projects, err := repository.LoadProjects(ctx, a.DB, "projects.status = ?", domain.ProjectStatusActive)
	if err != nil {
		a.serverError(c, err)
		return
	}
	roles, err := repository.GivableRoles(ctx, a.DB)
	if err != nil {
		a.serverError(c, err)
		return
	}
	memberOf, err := repository.MemberProjectIDs(ctx, a.DB, p.ID())
	if err != nil {
		a.serverError(c, err)
		return
	}
	data := map[string]any{"Principal": p, "PrincipalBase": p.Base(), "Projects": projects, "Roles": roles, "MemberOf": memberOf}
	if httpx.Format(c.R) == "js" {
		c.Render("principal_memberships/new", data, RenderOptions{Format: "js"})
		return
	}
	c.Render("principal_memberships/new", data, adminLayoutXHR(c))
}

// PrincipalMembershipsCreate は principal_memberships#create（Member.create_principal_memberships）。
func (a *App) PrincipalMembershipsCreate(c *Req) {
	p := c.value(principalCtxKey{}).(*principalRef)
	mp := c.Params().Map("membership")
	var created []*membershipRow
	if mp != nil {
		projectIDs := idsFromParam(mp.Slice("project_ids"))
		if len(projectIDs) == 0 {
			projectIDs = idsFromParam(mp.Slice("project_id"))
		}
		roleIDs := idsFromParam(mp.Slice("role_ids"))
		err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
			for _, pid := range projectIDs {
				row := &membershipRow{Errors: validation.New("member")}
				id, err := repository.CreatePrincipalMembership(c.Ctx(), tx, p.ID(), pid, roleIDs)
				switch {
				case errors.Is(err, repository.ErrMemberRoleEmpty):
					row.Errors.Add("role", "empty")
				case errors.Is(err, repository.ErrInvalidMemberRole):
					row.Errors.Add("role", "invalid")
				case err != nil:
					return err
				default:
					m, err := repository.GetMember(c.Ctx(), tx, id)
					if err != nil {
						return err
					}
					row.Member = m
				}
				created = append(created, row)
			}
			return nil
		})
		if err != nil {
			a.serverError(c, err)
			return
		}
	}
	c.ResetAuthz()
	if httpx.Format(c.R) == "js" {
		data, err := a.membershipsData(c, p)
		if err != nil {
			a.serverError(c, err)
			return
		}
		data["Members"] = created
		var errs []string
		allPersisted := len(created) > 0
		for _, m := range created {
			if m.Member == nil {
				allPersisted = false
				for _, e := range m.Errors.FullMessages(c.Loc) {
					if !contains(errs, e) {
						errs = append(errs, e)
					}
				}
			}
		}
		data["AllPersisted"] = allPersisted
		data["ErrorsText"] = strings.Join(errs, ", ")
		c.Render("principal_memberships/create", data, RenderOptions{Format: "js"})
		return
	}
	c.Redirect(p.Base() + "/edit?tab=memberships")
}

// PrincipalMembershipsEdit は principal_memberships#edit（html / js）。
func (a *App) PrincipalMembershipsEdit(c *Req) {
	p := c.value(principalCtxKey{}).(*principalRef)
	m := c.value(membershipCtxKey{}).(*domain.Member)
	row, err := a.membershipRow(c, m)
	if err != nil {
		a.serverError(c, err)
		return
	}
	roles, err := repository.GivableRoles(c.Ctx(), a.DB)
	if err != nil {
		a.serverError(c, err)
		return
	}
	data := map[string]any{"Principal": p, "PrincipalBase": p.Base(), "Membership": row, "Roles": roles, "XHR": httpx.IsXHR(c.R),
		"PrincipalName": a.principalName(c, p)}
	if httpx.Format(c.R) == "js" {
		c.Render("principal_memberships/edit", data, RenderOptions{Format: "js"})
		return
	}
	c.Render("principal_memberships/edit", data, adminLayoutXHR(c))
}

// PrincipalMembershipsUpdate は principal_memberships#update（role_ids の置き換え）。
func (a *App) PrincipalMembershipsUpdate(c *Req) {
	p := c.value(principalCtxKey{}).(*principalRef)
	m := c.value(membershipCtxKey{}).(*domain.Member)
	mp := c.Params().Map("membership")
	if mp == nil {
		// params.require(:membership) → ActionController::ParameterMissing（400）
		httpx.BadRequest(c.W)
		c.Halt()
		return
	}
	errs := validation.New("member")
	destroyed := false
	roleIDs := idsFromParam(mp.Slice("role_ids"))
	err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
		err := repository.SetMemberRoles(c.Ctx(), tx, m.ID, roleIDs)
		switch {
		case errors.Is(err, repository.ErrMemberRoleEmpty):
			errs.Add("role", "empty")
			return nil
		case errors.Is(err, repository.ErrInvalidMemberRole):
			errs.Add("role", "invalid")
			return nil
		}
		return err
	})
	if err != nil {
		a.serverError(c, err)
		return
	}
	c.ResetAuthz()
	if httpx.Format(c.R) == "js" {
		row := &membershipRow{Member: m, Errors: errs}
		if nm, err := repository.GetMember(c.Ctx(), a.DB, m.ID); err == nil {
			if row, err = a.membershipRow(c, nm); err != nil {
				a.serverError(c, err)
				return
			}
			row.Errors = errs
		} else if errors.Is(err, repository.ErrNotFound) {
			destroyed = true
		}
		c.Render("principal_memberships/update", map[string]any{"Membership": row, "Destroyed": destroyed,
			"ErrorsText": strings.Join(errs.FullMessages(c.Loc), ", ")}, RenderOptions{Format: "js"})
		return
	}
	c.Redirect(p.Base() + "/edit?tab=memberships")
}

// PrincipalMembershipsDestroy は principal_memberships#destroy（deletable? なら削除）。
func (a *App) PrincipalMembershipsDestroy(c *Req) {
	p := c.value(principalCtxKey{}).(*principalRef)
	m := c.value(membershipCtxKey{}).(*domain.Member)
	if !m.AnyInheritedRole() {
		if err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error { return repository.DestroyMember(c.Ctx(), tx, m.ID) }); err != nil {
			a.serverError(c, err)
			return
		}
	}
	c.ResetAuthz()
	if httpx.Format(c.R) == "js" {
		data, err := a.membershipsData(c, p)
		if err != nil {
			a.serverError(c, err)
			return
		}
		c.Render("principal_memberships/destroy", data, RenderOptions{Format: "js"})
		return
	}
	c.Redirect(p.Base() + "/edit?tab=memberships")
}
