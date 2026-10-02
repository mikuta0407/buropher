package handler

import (
	"errors"
	"html/template"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// MembersController（app/controllers/members_controller.rb）。
var MembersController = &Controller{Name: "members", MainMenu: true}

// routesMembers は members コントローラのルートを登録する。
//
//	resources :projects do
//	  shallow do
//	    resources :memberships, :controller => 'members' do
//	      collection do
//	        get 'autocomplete'
//	      end
//	    end
//	  end
//	end
func (a *App) routesMembers(r Router) {
	ctrl := MembersController
	// before_action :find_model_object, :except => [:index, :new, :create, :autocomplete]
	// before_action :find_project_from_association, :except => [:index, :new, :create, :autocomplete]
	// before_action :find_project_by_project_id, :only => [:index, :new, :create, :autocomplete]
	// before_action :authorize
	// accept_api_auth :index, :show, :create, :update, :destroy
	findMember := Before(a.findMemberFilter)
	a.Handle(r, http.MethodGet, "/projects/{project_id}/memberships/autocomplete", ctrl, "autocomplete", a.MembersAutocomplete, FindProjectByProjectID(), Authorize())
	a.Handle(r, http.MethodGet, "/projects/{project_id}/memberships/new", ctrl, "new", a.MembersNew, FindProjectByProjectID(), Authorize())
	a.Handle(r, http.MethodGet, "/projects/{project_id}/memberships", ctrl, "index", a.MembersIndex, FindProjectByProjectID(), Authorize(), AcceptAPIAuth())
	a.Handle(r, http.MethodPost, "/projects/{project_id}/memberships", ctrl, "create", a.MembersCreate, FindProjectByProjectID(), Authorize(), AcceptAPIAuth())
	a.Handle(r, http.MethodGet, "/memberships/{id}/edit", ctrl, "edit", a.MembersEdit, findMember, Authorize())
	a.Handle(r, http.MethodGet, "/memberships/{id}", ctrl, "show", a.MembersShow, findMember, Authorize(), AcceptAPIAuth())
	a.Handle(r, http.MethodPatch, "/memberships/{id}", ctrl, "update", a.MembersUpdate, findMember, Authorize(), AcceptAPIAuth())
	a.Handle(r, http.MethodPut, "/memberships/{id}", ctrl, "update", a.MembersUpdate, findMember, Authorize(), AcceptAPIAuth())
	a.Handle(r, http.MethodDelete, "/memberships/{id}", ctrl, "destroy", a.MembersDestroy, findMember, Authorize(), AcceptAPIAuth())
}

type memberKey struct{}

// findMemberFilter は find_model_object + find_project_from_association。
func (a *App) findMemberFilter(c *Req) {
	id, ok := c.Params().IntStrict("id")
	if !ok {
		c.Render404("")
		return
	}
	m, err := repository.GetMemberWithPrincipal(c.Ctx(), a.DB, id)
	if errors.Is(err, repository.ErrNotFound) {
		c.Render404("")
		return
	} else if err != nil {
		a.internalError(c, "find member", err)
		return
	}
	p, err := repository.GetProject(c.Ctx(), a.DB, m.Member.ProjectID)
	if err != nil {
		a.internalError(c, "member project", err)
		return
	}
	c.Project = p
	c.setLocal("member", m)
}

func (c *Req) member() *repository.MemberPrincipal {
	m, _ := c.local("member").(*repository.MemberPrincipal)
	return m
}

// MembersIndex は members#index（API のみ。HTML は 406）。
func (a *App) MembersIndex(c *Req) {
	if httpx.Negotiate(c.R, "html", "xml", "json") == "html" || !httpx.IsAPIRequest(c.R) {
		c.head(http.StatusNotAcceptable)
		return
	}
	offset, limit := apiOffsetAndLimitMin(c)
	ctx := c.Ctx()
	members, err := repository.ProjectMembersWithPrincipals(ctx, a.DB, c.Project.ID, false)
	if err != nil {
		a.internalError(c, "members", err)
		return
	}
	count := len(members)
	if offset < len(members) {
		members = members[offset:]
	} else {
		members = nil
	}
	if len(members) > limit {
		members = members[:limit]
	}
	arr := apiArr("memberships")
	for _, m := range members {
		el, err := a.membershipAPIEl(c, m)
		if err != nil {
			a.internalError(c, "membership api", err)
			return
		}
		arr.children = append(arr.children, el)
	}
	c.renderAPIRoot(arr, apiMetaMin(c, count, offset, limit), http.StatusOK)
}

// MembersShow は members#show（API のみ）。
func (a *App) MembersShow(c *Req) {
	if !httpx.IsAPIRequest(c.R) {
		c.head(http.StatusNotAcceptable)
		return
	}
	el, err := a.membershipAPIEl(c, c.member())
	if err != nil {
		a.internalError(c, "membership api", err)
		return
	}
	c.renderAPIRoot(el, nil, http.StatusOK)
}

// membershipAPIEl は members/show.api.rsb の membership 要素。
func (a *App) membershipAPIEl(c *Req, m *repository.MemberPrincipal) (*apiEl, error) {
	ctx := c.Ctx()
	p, err := repository.GetProject(ctx, a.DB, m.Member.ProjectID)
	if err != nil {
		return nil, err
	}
	page := c.Page()
	el := apiObj("membership", apiField("id", m.Member.ID), apiAttr("project", [2]any{"id", p.ID}, [2]any{"name", p.Name}))
	if pr := m.Principal(); pr != nil {
		name := "user"
		if m.Group != nil {
			name = "group"
			if m.Group.Builtin() {
				name = map[domain.PrincipalKind]string{domain.KindGroupAnonymous: "group_anonymous", domain.KindGroupNonMember: "group_non_member"}[m.Group.Kind]
			}
		}
		el.children = append(el.children, apiAttr(name, [2]any{"id", pr.ID}, [2]any{"name", helper.PrincipalName(page, m)}))
	}
	roles := apiArr("roles")
	ids := []int64{}
	for _, mr := range m.Member.MemberRoles {
		ids = append(ids, mr.RoleID)
	}
	rs, err := repository.RolesByIDs(ctx, a.DB, ids)
	if err != nil {
		return nil, err
	}
	byID := map[int64]*domain.Role{}
	for _, r := range rs {
		byID[r.ID] = r
	}
	for _, mr := range m.Member.MemberRoles {
		r := byID[mr.RoleID]
		if r == nil {
			continue
		}
		attrs := [][2]any{{"id", r.ID}, {"name", helper.RoleName(page, r)}}
		if mr.Inherited() {
			attrs = append(attrs, [2]any{"inherited", true})
		}
		roles.children = append(roles.children, apiAttr("role", attrs...))
	}
	el.children = append(el.children, roles)
	return el, nil
}

// MembersNew は members#new（HTML / js）。
func (a *App) MembersNew(c *Req) {
	data, err := a.newMemberFormData(c)
	if err != nil {
		a.internalError(c, "new member", err)
		return
	}
	switch httpx.Negotiate(c.R, "html", "js") {
	case "js":
		c.Render("members/new", data, RenderOptions{Format: "js", Layout: view.NoLayout})
	case "html":
		c.Render("members/new", data)
	default:
		c.unknownFormat()
	}
}

func (a *App) newMemberFormData(c *Req) (map[string]any, error) {
	principals, err := a.renderPrincipalsForNewMembers(c)
	if err != nil {
		return nil, err
	}
	roles, err := c.Authz().ManagedRoles(c.Ctx(), c.Project.ID)
	if err != nil {
		return nil, err
	}
	var out []*domain.Role
	for _, r := range roles {
		if r.Givable() {
			out = append(out, r)
		}
	}
	domain.SortRoles(out)
	return map[string]any{"PrincipalsForNewMembers": principals, "ManagedRoles": out, "Project": c.Project}, nil
}

// renderPrincipalsForNewMembers は MembersHelper#render_principals_for_new_members(project, 100)。
func (a *App) renderPrincipalsForNewMembers(c *Req) (template.HTML, error) {
	ctx := c.Ctx()
	vis, err := c.Authz().PrincipalVisibleCondition(ctx)
	if err != nil {
		return "", err
	}
	q := c.Params().String("q")
	pg := newMinPaginator(0, 100, c.Params().String("page"))
	count, ids, err := repository.NewMemberCandidates(ctx, a.DB, c.Project.ID, vis, q, a.Settings.String("user_format"), pg.Offset(), pg.PerPage)
	if err != nil {
		return "", err
	}
	pg.ItemCount = count
	users, groups, err := repository.PrincipalsByIDs(ctx, a.DB, ids)
	if err != nil {
		return "", err
	}
	var list []*repository.MemberPrincipal
	for _, id := range ids {
		list = append(list, &repository.MemberPrincipal{User: users[id], Group: groups[id]})
	}
	boxes, err := c.renderFragment("members/principal_boxes", map[string]any{"Principals": list})
	if err != nil {
		return "", err
	}
	s := rails.ContentTag("div", rails.ContentTag("div", boxes, rails.NewHash("id", "principals")), rails.NewHash("class", "objects-selection"))
	base := "/projects/" + c.Project.Identifier + "/memberships/autocomplete.js"
	links := c.paginationLinksFull(pg, count, false, func(text string, params map[string]string, opts *rails.Hash) template.HTML {
		qs := map[string]string{}
		for k, v := range params {
			qs[k] = v
		}
		if q != "" {
			qs["q"] = q
		}
		vals := make([]string, 0, len(qs))
		for _, k := range sortedKeys(qs) {
			if qs[k] == "" {
				continue
			}
			vals = append(vals, urlEncode(k)+"="+urlEncode(qs[k]))
		}
		u := base
		if len(vals) > 0 {
			u += "?" + strings.Join(vals, "&")
		}
		o := rails.NewHash("remote", true)
		if opts != nil {
			o = opts.Clone().Update(o)
		}
		return rails.LinkTo(text, u, o)
	})
	return s + rails.ContentTag("span", links, rails.NewHash("class", "pagination")), nil
}

// renderFragment はテンプレートをレイアウトなしで描画した HTML を返す（ヘルパーの出力を組み立てる用）。
func (c *Req) renderFragment(name string, data any) (template.HTML, error) {
	out, err := c.App.Views.Render(c.ViewContext(), name, data, view.RenderOptions{Layout: view.NoLayout})
	return template.HTML(out), err
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// MembersAutocomplete は members#autocomplete（js）。
func (a *App) MembersAutocomplete(c *Req) {
	if httpx.Negotiate(c.R, "js") != "js" {
		c.unknownFormat()
		return
	}
	s, err := a.renderPrincipalsForNewMembers(c)
	if err != nil {
		a.internalError(c, "principals for new members", err)
		return
	}
	c.Render("members/autocomplete", map[string]any{"Principals": s}, RenderOptions{Format: "js", Layout: view.NoLayout})
}

// setEditableRoleIDs は Member#set_editable_role_ids(ids)（管理できないロールは変更しない）。
func (a *App) setEditableRoleIDs(c *Req, current []int64, ids []int64, projectID int64) ([]int64, error) {
	managed, err := c.Authz().ManagedRoles(c.Ctx(), projectID)
	if err != nil {
		return nil, err
	}
	editable := map[int64]bool{}
	for _, r := range managed {
		editable[r.ID] = true
	}
	var out []int64
	for _, id := range current {
		if !editable[id] && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	for _, id := range ids {
		if id != 0 && editable[id] && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

// memberResult は create の 1 件（保存結果とエラー）。
type memberResult struct {
	ID     int64
	Errors []string
}

// MembersCreate は members#create（POST /projects/:project_id/memberships）。
func (a *App) MembersCreate(c *Req) {
	ctx := c.Ctx()
	var results []memberResult
	if mp := c.Params().Map("membership"); mp != nil {
		var userIDs []string
		if mp.Has("user_id") {
			userIDs = mp.Strings("user_id")
		} else {
			userIDs = mp.Strings("user_ids")
		}
		if len(userIDs) == 0 {
			userIDs = []string{""}
		}
		roleIDs := paramIDs(mp.Strings("role_ids"))
		for _, uid := range userIDs {
			res := memberResult{}
			pid := httpx.RubyToI(uid)
			roles, err := a.setEditableRoleIDs(c, nil, roleIDs, c.Project.ID)
			if err != nil {
				a.internalError(c, "editable roles", err)
				return
			}
			res.Errors, err = a.validateNewMember(c, pid, strings.TrimSpace(uid) != "", roles)
			if err != nil {
				a.internalError(c, "validate member", err)
				return
			}
			if len(res.Errors) == 0 {
				err := a.DB.WithTx(ctx, func(tx *db.Tx) error {
					id, err := repository.CreateMember(ctx, tx, c.Project.ID, pid, roles)
					res.ID = id
					return err
				})
				switch {
				case errors.Is(err, repository.ErrMemberRoleEmpty):
					res.Errors = append(res.Errors, c.L("field_role")+" "+c.L("activerecord.errors.messages.empty"))
				case errors.Is(err, repository.ErrMemberTaken):
					res.Errors = append(res.Errors, c.L("field_user")+" "+c.L("activerecord.errors.messages.taken"))
				case errors.Is(err, repository.ErrInvalidMemberRole):
					res.Errors = append(res.Errors, c.L("field_role")+" "+c.L("activerecord.errors.messages.invalid"))
				case err != nil:
					a.internalError(c, "create member", err)
					return
				}
			}
			results = append(results, res)
		}
	}
	c.ResetAuthz()
	switch httpx.Negotiate(c.R, "html", "js", "xml", "json") {
	case "html":
		c.Redirect("/projects/" + c.Project.Identifier + "/settings/members")
	case "js":
		a.renderMembersJS(c, "members/create", results, 0)
	case "xml", "json":
		if len(results) == 0 {
			c.RenderAPIErrorsMin()
			return
		}
		if errs := results[0].Errors; len(errs) > 0 {
			c.RenderAPIErrorsMin(errs...)
			return
		}
		m, err := repository.GetMemberWithPrincipal(ctx, a.DB, results[0].ID)
		if err != nil {
			a.internalError(c, "reload member", err)
			return
		}
		el, err := a.membershipAPIEl(c, m)
		if err != nil {
			a.internalError(c, "membership api", err)
			return
		}
		c.W.Header().Set("Location", httpx.RequestBaseURL(c.R)+"/memberships/"+strconv.FormatInt(m.Member.ID, 10))
		c.renderAPIRoot(el, nil, http.StatusCreated)
	default:
		c.unknownFormat()
	}
}

// validateNewMember は Member の検証（principal の存在・一意性・ロール）。
func (a *App) validateNewMember(c *Req, principalID int64, given bool, roles []int64) ([]string, error) {
	var errs []string
	ctx := c.Ctx()
	exists := false
	if given && principalID != 0 {
		if _, err := repository.GetPrincipal(ctx, a.DB, principalID); err == nil {
			exists = true
		} else if !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}
	}
	if !exists {
		errs = append(errs, c.L("field_principal")+" "+c.L("activerecord.errors.messages.blank"))
	} else {
		if _, err := repository.FindMember(ctx, a.DB, c.Project.ID, principalID); err == nil {
			errs = append(errs, c.L("field_user")+" "+c.L("activerecord.errors.messages.taken"))
		} else if !errors.Is(err, repository.ErrNotFound) {
			return nil, err
		}
	}
	if len(roles) == 0 {
		errs = append(errs, c.L("field_role")+" "+c.L("activerecord.errors.messages.empty"))
	}
	return errs, nil
}

// renderMembersJS は members/create.js・update.js・destroy.js（設定画面のメンバータブを再描画）。
func (a *App) renderMembersJS(c *Req, tmpl string, results []memberResult, highlight int64) {
	members, err := a.settingsMembers(c, c.Project)
	if err != nil {
		a.internalError(c, "settings members", err)
		return
	}
	data := map[string]any{"Members": members, "Highlight": highlight}
	if results != nil {
		allValid := true
		var ids []int64
		var errs []string
		for _, r := range results {
			if len(r.Errors) > 0 {
				allValid = false
				for _, e := range r.Errors {
					if !slices.Contains(errs, e) {
						errs = append(errs, e)
					}
				}
			} else {
				ids = append(ids, r.ID)
			}
		}
		data["HasMembers"] = len(results) > 0
		data["AllValid"] = allValid
		data["CreatedIDs"] = ids
		data["ErrorsMessage"] = c.L("notice_failed_to_save_members", map[string]any{"errors": strings.Join(errs, ", ")})
	}
	c.Render(tmpl, data, RenderOptions{Format: "js", Layout: view.NoLayout})
}

// memberEditData は members/_edit の描画データ（@member, @roles）。
func (a *App) memberEditData(c *Req) (map[string]any, error) {
	ctx := c.Ctx()
	m := c.member()
	roles, err := repository.GivableRoles(ctx, a.DB)
	if err != nil {
		return nil, err
	}
	managed, err := c.Authz().ManagedRoles(ctx, c.Project.ID)
	if err != nil {
		return nil, err
	}
	page := c.Page()
	var items []memberRoleItem
	for _, r := range roles {
		it := memberRoleItem{Role: r, Name: helper.RoleName(page, r)}
		it.Checked = slices.Contains(m.Member.RoleIDs(), r.ID)
		// Member#role_editable?(role): 継承ロールでなく、管理可能
		it.Editable = !m.Member.HasInheritedRole(r.ID) && slices.ContainsFunc(managed, func(x *domain.Role) bool { return x.ID == r.ID })
		inh, err := a.roleInheritance(c, m, r)
		if err != nil {
			return nil, err
		}
		it.Inheritance = inh
		items = append(items, it)
	}
	title := helper.PrincipalName(page, m) + " - " + c.Project.Name
	return map[string]any{"Member": m, "Roles": items, "Title": title, "XHR": httpx.IsXHR(c.R)}, nil
}

// memberRoleItem は members/_edit のロール 1 件。
type memberRoleItem struct {
	Role        *domain.Role
	Name        string
	Checked     bool
	Editable    bool
	Inheritance template.HTML
}

// roleInheritance は MembersHelper#render_role_inheritance(member, role)。
func (a *App) roleInheritance(c *Req, m *repository.MemberPrincipal, r *domain.Role) (template.HTML, error) {
	ctx := c.Ctx()
	var labels []string
	for _, mr := range m.Member.MemberRoles {
		if mr.RoleID != r.ID || mr.InheritedFrom == nil {
			continue
		}
		// 継承元の member_role のメンバー（グループ or 親プロジェクト）
		src, err := repository.MemberOfMemberRole(ctx, a.DB, *mr.InheritedFrom)
		if errors.Is(err, repository.ErrNotFound) {
			continue
		} else if err != nil {
			return "", err
		}
		var label string
		if src.ProjectID != m.Member.ProjectID {
			label = c.L("label_inherited_from_parent_project")
		} else if g, err := repository.GetGroup(ctx, a.DB, src.PrincipalID); err == nil {
			label = c.L("label_inherited_from_group", map[string]any{"name": helper.GroupDisplayName(c.Page(), g)})
		}
		if label != "" && !slices.Contains(labels, label) {
			labels = append(labels, label)
		}
	}
	if len(labels) == 0 {
		return "", nil
	}
	return rails.ContentTag("em", strings.Join(labels, ", "), rails.NewHash("class", "info")), nil
}

// MembersEdit は members#edit（HTML / js）。
func (a *App) MembersEdit(c *Req) {
	data, err := a.memberEditData(c)
	if err != nil {
		a.internalError(c, "edit member", err)
		return
	}
	switch httpx.Negotiate(c.R, "html", "js") {
	case "js":
		c.Render("members/edit", data, RenderOptions{Format: "js", Layout: view.NoLayout})
	case "html":
		c.Render("members/edit", data)
	default:
		c.unknownFormat()
	}
}

// MembersUpdate は members#update。
func (a *App) MembersUpdate(c *Req) {
	ctx := c.Ctx()
	m := c.member()
	var saveErrs []string
	if mp := c.Params().Map("membership"); mp != nil {
		roles, err := a.setEditableRoleIDs(c, directRoleIDs(m.Member), paramIDs(mp.Strings("role_ids")), c.Project.ID)
		if err != nil {
			a.internalError(c, "editable roles", err)
			return
		}
		if len(roles) == 0 && !m.Member.AnyInheritedRole() {
			saveErrs = append(saveErrs, c.L("field_role")+" "+c.L("activerecord.errors.messages.empty"))
		} else {
			err := a.DB.WithTx(ctx, func(tx *db.Tx) error { return repository.SetMemberRoles(ctx, tx, m.Member.ID, roles) })
			if errors.Is(err, repository.ErrMemberRoleEmpty) {
				saveErrs = append(saveErrs, c.L("field_role")+" "+c.L("activerecord.errors.messages.empty"))
			} else if err != nil {
				a.internalError(c, "update member", err)
				return
			}
		}
	}
	c.ResetAuthz()
	switch httpx.Negotiate(c.R, "html", "js", "xml", "json") {
	case "html":
		c.Redirect("/projects/" + c.Project.Identifier + "/settings/members")
	case "js":
		a.renderMembersJS(c, "members/update", nil, m.Member.ID)
	case "xml", "json":
		if len(saveErrs) > 0 {
			c.RenderAPIErrorsMin(saveErrs...)
			return
		}
		c.RenderAPIOKMin()
	default:
		c.unknownFormat()
	}
}

// directRoleIDs は継承でないロール（role_ids のうち直接付与されたもの）。
// set_editable_role_ids の untouched_role_ids は self.role_ids（継承含む）から計算されるが、
// 継承ロールは SetMemberRoles が維持するため直接付与分だけを渡す。
func directRoleIDs(m *domain.Member) []int64 {
	var out []int64
	for _, mr := range m.MemberRoles {
		if mr.InheritedFrom == nil && !slices.Contains(out, mr.RoleID) {
			out = append(out, mr.RoleID)
		}
	}
	return out
}

// MembersDestroy は members#destroy。
func (a *App) MembersDestroy(c *Req) {
	ctx := c.Ctx()
	m := c.member()
	destroyed := false
	deletable, err := a.memberDeletable(c, m)
	if err != nil {
		a.internalError(c, "member deletable", err)
		return
	}
	if deletable {
		if err := a.DB.WithTx(ctx, func(tx *db.Tx) error { return repository.DestroyMember(ctx, tx, m.Member.ID) }); err != nil {
			a.internalError(c, "destroy member", err)
			return
		}
		destroyed = true
	}
	c.ResetAuthz()
	switch httpx.Negotiate(c.R, "html", "js", "xml", "json") {
	case "html":
		c.Redirect("/projects/" + c.Project.Identifier + "/settings/members")
	case "js":
		a.renderMembersJS(c, "members/destroy", nil, 0)
	case "xml", "json":
		if destroyed {
			c.RenderAPIOKMin()
		} else {
			c.head(http.StatusUnprocessableEntity)
		}
	default:
		c.unknownFormat()
	}
}

// memberDeletable は Member#deletable?(User.current)。
func (a *App) memberDeletable(c *Req, m *repository.MemberPrincipal) (bool, error) {
	if m.Member.AnyInheritedRole() {
		return false, nil
	}
	managed, err := c.Authz().ManagedRoles(c.Ctx(), m.Member.ProjectID)
	if err != nil {
		return false, err
	}
	for _, id := range m.Member.RoleIDs() {
		if !slices.ContainsFunc(managed, func(x *domain.Role) bool { return x.ID == id }) {
			return false, nil
		}
	}
	return true, nil
}
