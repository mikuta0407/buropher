package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mikuta0407/buropher/internal/apibuilder"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/pagination"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/validation"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// GroupsController（app/controllers/groups_controller.rb）。layout 'admin'（show は base）。
var GroupsController = &Controller{Name: "groups", MainMenu: false}

// routesGroups は groups コントローラのルートを登録する。
func (a *App) routesGroups(r Router) {
	adm := RequireAdmin()
	api := AcceptAPIAuth()
	fg := a.findGroup()
	a.Handle(r, http.MethodGet, "/groups", GroupsController, "index", a.GroupsIndex, api, adm)
	a.Handle(r, http.MethodPost, "/groups", GroupsController, "create", a.GroupsCreate, api, adm)
	a.Handle(r, http.MethodGet, "/groups/new", GroupsController, "new", a.GroupsNew, adm)
	a.Handle(r, http.MethodGet, "/groups/{id}/edit", GroupsController, "edit", a.GroupsEdit, adm, fg)
	a.Handle(r, http.MethodGet, "/groups/{id}/autocomplete_for_user", GroupsController, "autocomplete_for_user", a.GroupsAutocompleteForUser, adm, fg)
	a.Handle(r, http.MethodGet, "/groups/{id}", GroupsController, "show", a.GroupsShow, api, fg)
	for _, m := range []string{http.MethodPatch, http.MethodPut} {
		a.Handle(r, m, "/groups/{id}", GroupsController, "update", a.GroupsUpdate, api, adm, fg)
	}
	a.Handle(r, http.MethodDelete, "/groups/{id}", GroupsController, "destroy", a.GroupsDestroy, api, adm, fg)
	// get 'groups/:id/users/new' / post 'groups/:id/users' / delete 'groups/:id/users/:user_id'（:id => /\d+/）
	a.Handle(r, http.MethodGet, "/groups/{id:[0-9]+}/users/new", GroupsController, "new_users", a.GroupsNewUsers, adm, fg)
	a.Handle(r, http.MethodPost, "/groups/{id:[0-9]+}/users", GroupsController, "add_users", a.GroupsAddUsers, api, adm, fg)
	a.Handle(r, http.MethodDelete, "/groups/{id:[0-9]+}/users/{user_id}", GroupsController, "remove_user", a.GroupsRemoveUser, api, adm, fg)
}

type groupCtxKey struct{}

// findGroup は GroupsController#find_group（Group.visible.find(params[:id])。組込グループを含む）。
func (a *App) findGroup() ActionOption {
	return Before(func(c *Req) {
		id, err := strconv.ParseInt(c.Params().String("id"), 10, 64)
		if err != nil {
			c.Render404("")
			return
		}
		g, err := repository.GetGroup(c.Ctx(), a.DB, id)
		if err != nil {
			if !errors.Is(err, repository.ErrNotFound) {
				a.logger().Error("find group", "err", err)
			}
			c.Render404("")
			return
		}
		// Group.visible: 管理者以外は有効な（status = 1）グループのみ（Principal.visible）
		if !c.User.IsAdmin() {
			cond, err := c.Authz().PrincipalVisibleCondition(c.Ctx())
			if err == nil {
				if ok, err := repository.PrincipalVisible(c.Ctx(), a.DB, cond, id); err != nil || !ok {
					c.Render404("")
					return
				}
			}
		}
		c.setValue(groupCtxKey{}, g)
	})
}

// groupModel は編集中の Group。
type groupModel struct {
	*domain.Group
	newRecord    bool
	orig         domain.Group
	userIDs      []int64
	userIDsSet   bool
	customValues []principalCustomValue
	errors       *validation.Errors
	c            *Req
}

func (m *groupModel) ParamKey() string {
	switch m.Kind {
	case domain.KindGroupAnonymous:
		return "group_anonymous"
	case domain.KindGroupNonMember:
		return "group_non_member"
	}
	return "group"
}
func (m *groupModel) Persisted() bool                      { return !m.newRecord }
func (m *groupModel) ToParam() string                      { return itoa(m.ID) }
func (m *groupModel) ValidationErrors() *validation.Errors { return m.errors }
func (m *groupModel) ErrorsOn(attr string) []string        { return m.errors.Messages(m.c.Loc, attr) }
func (m *groupModel) HumanAttributeName(attr string) string {
	return m.errors.HumanAttributeName(m.c.Loc, attr)
}
func (m *groupModel) NewRecord() bool     { return m.newRecord }
func (m *groupModel) IsBuiltin() bool     { return m.Group.Builtin() }
func (m *groupModel) DisplayName() string { return helper.GroupName(m.c.Page(), m.Group) }

// Send は属性参照（name は Group#name。組込グループは翻訳名）。
func (m *groupModel) Send(method string) (any, bool) {
	switch method {
	case "name", "lastname":
		if m.Builtin() {
			return helper.GroupName(m.c.Page(), m.Group), true
		}
		return m.Name, true
	case "twofa_required":
		return m.TwofaRequired, true
	}
	return nil, false
}

// CustomFieldTags は groups/_form の custom_field_tag_with_label :group, value。
func (m *groupModel) CustomFieldTags() []rails.HTML {
	var out []rails.HTML
	for _, cv := range m.customValues {
		out = append(out, customFieldTagWithLabel("group", cv, m.errors.Include(cv.Field.Name)))
	}
	return out
}

func newGroupErrors() *validation.Errors {
	e := validation.New("group")
	// Group.human_attribute_name: lastname → name
	e.AttrNames = map[string]string{"lastname": "field_name"}
	return e
}

// assign は Group#safe_attributes=（管理者かつ組込でないときのみ）。
func (m *groupModel) assign(p *httpx.Params) {
	if p == nil || !m.c.User.IsAdmin() || m.Builtin() {
		return
	}
	if v, ok := p.Get("name"); ok {
		m.Name = httpx.ValueString(v)
	}
	if v, ok := p.Get("twofa_required"); ok {
		m.TwofaRequired = castBoolAny(v)
	}
	if v, ok := p.Get("user_ids"); ok {
		m.userIDs = idsFromParam(v)
		m.userIDsSet = true
	}
	m.customValues = assignCustomFieldValues(m.customValues, p)
}

// saveGroup は Group#save（検証 → 保存）。
func (a *App) saveGroup(c *Req, m *groupModel) (bool, error) {
	e := m.errors
	e.Clear()
	validateCustomFieldValues(e, m.customValues)
	if strings.TrimSpace(m.Name) == "" {
		e.Add("lastname", "blank")
	}
	if m.Name != "" && (m.newRecord || m.Name != m.orig.Name) {
		taken, err := repository.GroupNameTaken(c.Ctx(), a.DB, m.Name, m.ID)
		if err != nil {
			return false, err
		}
		if taken {
			e.Add("lastname", "taken")
		}
	}
	if utf8.RuneCountInString(m.Name) > 255 {
		e.Add("lastname", "too_long", "count", 255)
	}
	if e.Any() {
		return false, nil
	}
	now := a.now()
	err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
		if m.newRecord {
			id, err := repository.InsertGroup(c.Ctx(), tx, m.Name, m.TwofaRequired, now)
			if err != nil {
				return err
			}
			m.ID = id
		} else {
			changed := m.Name != m.orig.Name || m.TwofaRequired != m.orig.TwofaRequired
			if err := repository.UpdateGroup(c.Ctx(), tx, m.ID, m.Name, m.TwofaRequired, changed, now); err != nil {
				return err
			}
		}
		for _, cv := range m.customValues {
			if cv.Field.Multiple || cv.Value == nil {
				continue
			}
			if _, err := repository.SetPrincipalCustomValue(c.Ctx(), tx, m.ID, cv.Field.ID, *cv.Value); err != nil {
				return err
			}
		}
		if m.userIDsSet && !m.Builtin() {
			var ids []int64
			for _, id := range m.userIDs {
				if u, err := repository.GetUser(c.Ctx(), tx, id); err == nil && u.Logged() {
					ids = append(ids, id)
				}
			}
			if err := repository.SetGroupUsers(c.Ctx(), tx, m.ID, ids); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	m.newRecord = false
	m.orig = *m.Group
	return true, nil
}

func (a *App) loadGroupModel(c *Req, g *domain.Group) (*groupModel, error) {
	m := &groupModel{Group: g, orig: *g, errors: newGroupErrors(), c: c}
	cvs, err := a.principalCustomValuesByID(c, "group", []int64{g.ID}, false)
	if err != nil {
		return nil, err
	}
	m.customValues = cvs[g.ID]
	return m, nil
}

// ---------------------------------------------------------------- actions

// GroupsIndex は groups#index（html はページネーション、API は Group.sorted（builtin=1 で組込を含む））。
func (a *App) GroupsIndex(c *Req) {
	ctx := c.Ctx()
	if httpx.IsAPIRequest(c.R) {
		groups, err := repository.ListGroups(ctx, a.DB, c.Params().String("builtin") == "1")
		if err != nil {
			a.serverError(c, err)
			return
		}
		ids := make([]int64, len(groups))
		for i, g := range groups {
			ids[i] = g.ID
		}
		cvs, err := a.principalCustomValuesByID(c, "group", ids, true)
		if err != nil {
			a.serverError(c, err)
			return
		}
		c.RenderAPI(0, func(b apibuilder.Builder) {
			b.Array("groups", nil, func() {
				for _, g := range groups {
					b.Object("group", func() { a.groupAPIFields(c, b, g, cvs[g.ID]) })
				}
			})
		})
		return
	}
	name := c.Params().String("name")
	count, err := repository.CountGroups(ctx, a.DB, name)
	if err != nil {
		a.serverError(c, err)
		return
	}
	pages := pagination.New(count, c.PerPageOption(), c.Params().String("page"))
	groups, err := repository.SearchGroups(ctx, a.DB, name, pages.PerPage, pages.Offset())
	if err != nil {
		a.serverError(c, err)
		return
	}
	counts, err := repository.GroupUserCounts(ctx, a.DB)
	if err != nil {
		a.serverError(c, err)
		return
	}
	c.Render("groups/index", map[string]any{"Groups": groups, "Pages": pages, "Count": count, "UserCounts": counts, "Name": name}, adminLayoutXHR(c))
}

func (a *App) groupAPIFields(c *Req, b apibuilder.Builder, g *domain.Group, cvs []principalCustomValue) {
	b.Value("id", g.ID)
	b.Value("name", g.Name)
	if t := g.BuiltinType(); t != "" {
		b.Value("builtin", t)
	}
	renderAPICustomValues(b, cvs)
}

// GroupsShow は groups#show（layout base / API）。
func (a *App) GroupsShow(c *Req) {
	g := c.value(groupCtxKey{}).(*domain.Group)
	ctx := c.Ctx()
	cvs, err := a.principalCustomValuesByID(c, "group", []int64{g.ID}, true)
	if err != nil {
		a.serverError(c, err)
		return
	}
	users, err := repository.GroupUsers(ctx, a.DB, g.ID)
	if err != nil {
		a.serverError(c, err)
		return
	}
	if httpx.IsAPIRequest(c.R) {
		var rows []*membershipRow
		if c.IncludeInAPIResponse("memberships") {
			if rows, err = a.membershipRows(c, g.ID, "", false); err != nil {
				a.serverError(c, err)
				return
			}
		}
		a.renderGroupShowAPI(c, g, users, rows, cvs[g.ID], 0)
		return
	}
	// @group.users.visible
	cond, err := c.Authz().PrincipalVisibleCondition(ctx)
	if err != nil {
		a.serverError(c, err)
		return
	}
	var visible []*domain.User
	for _, u := range users {
		if ok, err := repository.PrincipalVisible(ctx, a.DB, cond, u.ID); err == nil && ok {
			visible = append(visible, u)
		}
	}
	var shown []principalCustomValue
	for _, cv := range cvs[g.ID] {
		if cv.ValueString() != "" {
			shown = append(shown, cv)
		}
	}
	c.Render("groups/show", map[string]any{"Group": g, "Users": visible, "CustomValues": shown, "HasCustomValues": len(cvs[g.ID]) > 0})
}

func (a *App) renderGroupShowAPI(c *Req, g *domain.Group, users []*domain.User, rows []*membershipRow, cvs []principalCustomValue, status int) {
	format := a.Settings.String("user_format")
	c.RenderAPI(status, func(b apibuilder.Builder) {
		b.Object("group", func() {
			a.groupAPIFields(c, b, g, cvs)
			if c.IncludeInAPIResponse("users") && !g.Builtin() {
				b.Array("users", nil, func() {
					for _, u := range users {
						b.Attrs("user", apibuilder.A("id", u.ID, "name", u.Name(format)))
					}
				})
			}
			if c.IncludeInAPIResponse("memberships") {
				renderAPIMemberships(c, b, rows)
			}
		})
	})
}

// groupFormData は groups/new・groups/edit のデータ。
func (a *App) groupFormData(c *Req, m *groupModel) (map[string]any, error) {
	data := map[string]any{
		"Group":         m,
		"TwofaOptional": a.Settings.TwofaOptional(),
		"TwofaRequired": a.Settings.TwofaRequired(),
		"GroupName":     helper.GroupName(c.Page(), m.Group),
		"PrincipalBase": "/groups/" + itoa(m.ID),
	}
	if !m.newRecord {
		tabs := []helper.Tab{{Name: "general", Partial: "groups/general", Label: "label_general"}}
		if !m.Builtin() {
			tabs = append(tabs, helper.Tab{Name: "users", Partial: "groups/users", Label: "label_user_plural"})
			users, err := repository.GroupUsers(c.Ctx(), a.DB, m.ID)
			if err != nil {
				return nil, err
			}
			sortPrincipalsByName(users, a.Settings.String("user_format"))
			data["Users"] = users
		}
		tabs = append(tabs, helper.Tab{Name: "memberships", Partial: "groups/memberships", Label: "label_project_plural"})
		data["Tabs"] = tabs
		rows, err := a.membershipRows(c, m.ID, "", true)
		if err != nil {
			return nil, err
		}
		data["Memberships"] = rows
		data["Principal"] = &principalRef{Group: m.Group}
	}
	return data, nil
}

// sortPrincipalsByName は Principal#<=>（名前の大文字小文字を無視した比較）で並べる。
func sortPrincipalsByName(users []*domain.User, format string) {
	for i := 1; i < len(users); i++ {
		for j := i; j > 0; j-- {
			a, b := users[j-1].Name(format), users[j].Name(format)
			la, lb := strings.ToLower(a), strings.ToLower(b)
			if la > lb || (la == lb && a > b) {
				users[j-1], users[j] = users[j], users[j-1]
			} else {
				break
			}
		}
	}
}

// GroupsNew は groups#new。
func (a *App) GroupsNew(c *Req) {
	m := &groupModel{Group: &domain.Group{Principal: domain.Principal{Kind: domain.KindGroup, Status: domain.StatusActive}}, newRecord: true, errors: newGroupErrors(), c: c}
	cvs, err := a.newPrincipalCustomValues(c, "group")
	if err != nil {
		a.serverError(c, err)
		return
	}
	m.customValues = cvs
	data, _ := a.groupFormData(c, m)
	c.Render("groups/new", data, adminLayoutXHR(c))
}

// GroupsCreate は groups#create。
func (a *App) GroupsCreate(c *Req) {
	m := &groupModel{Group: &domain.Group{Principal: domain.Principal{Kind: domain.KindGroup, Status: domain.StatusActive}}, newRecord: true, errors: newGroupErrors(), c: c}
	cvs, err := a.newPrincipalCustomValues(c, "group")
	if err != nil {
		a.serverError(c, err)
		return
	}
	m.customValues = cvs
	m.assign(c.Params().Map("group"))
	ok, err := a.saveGroup(c, m)
	if err != nil {
		a.serverError(c, err)
		return
	}
	if ok {
		if httpx.IsAPIRequest(c.R) {
			c.W.Header().Set("Location", httpx.RequestBaseURL(c.R)+"/groups/"+itoa(m.ID))
			users, _ := repository.GroupUsers(c.Ctx(), a.DB, m.ID)
			var rows []*membershipRow
			if c.IncludeInAPIResponse("memberships") {
				rows, _ = a.membershipRows(c, m.ID, "", false)
			}
			cv, _ := a.principalCustomValuesByID(c, "group", []int64{m.ID}, true)
			a.renderGroupShowAPI(c, m.Group, users, rows, cv[m.ID], http.StatusCreated)
			return
		}
		c.Flash().SetNotice(c.L("notice_successful_create"))
		if c.Params().Present("continue") {
			c.Redirect("/groups/new")
		} else {
			c.Redirect("/groups")
		}
		return
	}
	if httpx.IsAPIRequest(c.R) {
		c.RenderValidationErrors(m.errors)
		return
	}
	data, _ := a.groupFormData(c, m)
	c.Render("groups/new", data, adminLayoutXHR(c))
}

// GroupsEdit は groups#edit。
func (a *App) GroupsEdit(c *Req) {
	g := c.value(groupCtxKey{}).(*domain.Group)
	m, err := a.loadGroupModel(c, g)
	if err != nil {
		a.serverError(c, err)
		return
	}
	data, err := a.groupFormData(c, m)
	if err != nil {
		a.serverError(c, err)
		return
	}
	c.Render("groups/edit", data, adminLayoutXHR(c))
}

// GroupsUpdate は groups#update。
func (a *App) GroupsUpdate(c *Req) {
	g := c.value(groupCtxKey{}).(*domain.Group)
	m, err := a.loadGroupModel(c, g)
	if err != nil {
		a.serverError(c, err)
		return
	}
	m.assign(c.Params().Map("group"))
	ok, err := a.saveGroup(c, m)
	if err != nil {
		a.serverError(c, err)
		return
	}
	if ok {
		c.Flash().SetNotice(c.L("notice_successful_update"))
		if httpx.IsAPIRequest(c.R) {
			c.RenderAPIOK()
			return
		}
		httpx.RedirectToRefererOr(c.W, c.R, "/groups", 0)
		c.Halt()
		return
	}
	if httpx.IsAPIRequest(c.R) {
		c.RenderValidationErrors(m.errors)
		return
	}
	data, err := a.groupFormData(c, m)
	if err != nil {
		a.serverError(c, err)
		return
	}
	c.Render("groups/edit", data, adminLayoutXHR(c))
}

// GroupsDestroy は groups#destroy（組込グループは削除しない）。
func (a *App) GroupsDestroy(c *Req) {
	g := c.value(groupCtxKey{}).(*domain.Group)
	if !g.Builtin() {
		if err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error { return repository.DestroyGroup(c.Ctx(), tx, g.ID) }); err != nil {
			a.serverError(c, err)
			return
		}
		c.ResetAuthz()
	}
	if httpx.IsAPIRequest(c.R) {
		c.RenderAPIOK()
		return
	}
	httpx.RedirectToRefererOr(c.W, c.R, "/groups", 0)
	c.Halt()
}

// newUsersData は groups/_new_users_form（render_principals_for_new_group_users）のデータ。
func (a *App) newUsersData(c *Req, g *domain.Group) (map[string]any, error) {
	q := c.Params().String("q")
	count, err := repository.CountUsersNotInGroup(c.Ctx(), a.DB, g.ID, q)
	if err != nil {
		return nil, err
	}
	pages := pagination.New(count, 100, c.Params().String("page"))
	users, err := repository.UsersNotInGroup(c.Ctx(), a.DB, g.ID, q, a.Settings.String("user_format"), pages.PerPage, pages.Offset())
	if err != nil {
		return nil, err
	}
	return map[string]any{"Group": g, "Candidates": users, "CandidatePages": pages, "CandidateCount": count, "Q": q}, nil
}

// GroupsNewUsers は groups#new_users（html / js）。
func (a *App) GroupsNewUsers(c *Req) {
	g := c.value(groupCtxKey{}).(*domain.Group)
	data, err := a.newUsersData(c, g)
	if err != nil {
		a.serverError(c, err)
		return
	}
	if httpx.Format(c.R) == "js" {
		c.Render("groups/new_users", data, RenderOptions{Format: "js"})
		return
	}
	c.Render("groups/new_users", data, adminLayoutXHR(c))
}

// GroupsAutocompleteForUser は groups#autocomplete_for_user（js のみ）。
func (a *App) GroupsAutocompleteForUser(c *Req) {
	g := c.value(groupCtxKey{}).(*domain.Group)
	if f := httpx.Format(c.R); f != "js" {
		// respond_to format.js のみ（それ以外は 406）
		httpx.Head(c.W, c.R, http.StatusNotAcceptable)
		c.Halt()
		return
	}
	data, err := a.newUsersData(c, g)
	if err != nil {
		a.serverError(c, err)
		return
	}
	c.Render("groups/autocomplete_for_user", data, RenderOptions{Format: "js"})
}

// GroupsAddUsers は groups#add_users（params[:user_id] || params[:user_ids]）。
func (a *App) GroupsAddUsers(c *Req) {
	g := c.value(groupCtxKey{}).(*domain.Group)
	p := c.Params()
	v, ok := p.Get("user_id")
	if !ok || v == nil {
		v, _ = p.Get("user_ids")
	}
	users, err := repository.UsersNotInGroupByIDs(c.Ctx(), a.DB, g.ID, idsFromParam(v))
	if err != nil {
		a.serverError(c, err)
		return
	}
	if g.Builtin() && len(users) > 0 {
		c.RenderError(http.StatusInternalServerError, "Cannot add users to a builtin group")
		return
	}
	err = a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
		for _, u := range users {
			if err := repository.AddUserToGroup(c.Ctx(), tx, g.ID, u.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		a.serverError(c, err)
		return
	}
	c.ResetAuthz()
	switch {
	case httpx.IsAPIRequest(c.R):
		if len(users) > 0 {
			c.RenderAPIOK()
		} else {
			c.RenderAPIErrors(c.L("label_user") + " " + c.L("activerecord.errors.messages.invalid"))
		}
	case httpx.Format(c.R) == "js":
		m, _ := a.loadGroupModel(c, g)
		data, err := a.groupFormData(c, m)
		if err != nil {
			a.serverError(c, err)
			return
		}
		data["Added"] = users
		c.Render("groups/add_users", data, RenderOptions{Format: "js"})
	default:
		c.Redirect("/groups/" + itoa(g.ID) + "/edit?tab=users")
	}
}

// GroupsRemoveUser は groups#remove_user（DELETE のみ実際に外す）。
func (a *App) GroupsRemoveUser(c *Req) {
	g := c.value(groupCtxKey{}).(*domain.Group)
	uid, err := strconv.ParseInt(c.Params().String("user_id"), 10, 64)
	if err != nil {
		c.Render404("")
		return
	}
	if _, err := repository.GetUser(c.Ctx(), a.DB, uid); err != nil {
		c.Render404("")
		return
	}
	if err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error { return repository.RemoveUserFromGroup(c.Ctx(), tx, g.ID, uid) }); err != nil {
		a.serverError(c, err)
		return
	}
	c.ResetAuthz()
	switch {
	case httpx.IsAPIRequest(c.R):
		c.RenderAPIOK()
	case httpx.Format(c.R) == "js":
		m, _ := a.loadGroupModel(c, g)
		data, err := a.groupFormData(c, m)
		if err != nil {
			a.serverError(c, err)
			return
		}
		c.Render("groups/remove_user", data, RenderOptions{Format: "js"})
	default:
		c.Redirect("/groups/" + itoa(g.ID) + "/edit?tab=users")
	}
}
