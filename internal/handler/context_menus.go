// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"net/http"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view"
)

// Redmine 7.0 #44169 でコンテキストメニューは ContextMenus::BaseController を継承する名前空間付きの
// コントローラ（app/controllers/context_menus/{issues,projects,time_entries,users}_controller.rb）に分割され、
// アクションはいずれも index になった（URL は従来どおり）。
var (
	// ContextMenusIssuesController は ContextMenus::IssuesController。
	ContextMenusIssuesController = &Controller{Name: "context_menus/issues", MainMenu: true}
	// ContextMenusProjectsController は ContextMenus::ProjectsController。
	ContextMenusProjectsController = &Controller{Name: "context_menus/projects", MainMenu: true}
	// ContextMenusTimeEntriesController は ContextMenus::TimeEntriesController。
	ContextMenusTimeEntriesController = &Controller{Name: "context_menus/time_entries", MainMenu: true}
	// ContextMenusUsersController は ContextMenus::UsersController。
	ContextMenusUsersController = &Controller{Name: "context_menus/users", MainMenu: true}
)

// routesContextMenus は context_menus コントローラのルートを登録する。
func (a *App) routesContextMenus(r Router) {
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		// match '/users/context_menu', to: 'context_menus/users#index', via: [:get, :post]
		// before_action :require_admin（Redmine 6.1.3 #44109）
		a.Handle(r, m, "/users/context_menu", ContextMenusUsersController, "index", a.ContextMenusUsers, RequireAdmin())
	}
}

// ContextMenusUsers は ContextMenus::UsersController#index（layout なし）。
func (a *App) ContextMenusUsers(c *Req) {
	// User.where(id: params[:id] || params[:ids])
	idParam, _ := c.Params().Lookup("id")
	if idParam == nil {
		idParam, _ = c.Params().Lookup("ids")
	}
	users, err := repository.UsersWhereIDs(c.Ctx(), a.DB, idsFromParam(idParam))
	if err != nil {
		a.serverError(c, err)
		return
	}
	if len(users) == 0 {
		c.Render404("")
		return
	}
	data := map[string]any{"Users": users}
	// @back = back_url（ContextMenus::BaseController#render_context_menu）
	if b := backURLParam(c); b != "" {
		data["Back"] = b
	}
	if len(users) == 1 {
		data["User"] = users[0]
	}
	allLocked := true
	for _, u := range users {
		if !u.Locked() {
			allLocked = false
		}
	}
	data["AllLocked"] = allLocked
	ids := make([]any, len(users))
	for i, u := range users {
		ids[i] = u.ID
	}
	data["IDs"] = ids
	// @groups = Group.givable.sorted.to_a
	// @common_group_ids = Group.givable.joins(:groups_users).where(groups_users: { user_id: @users.map(&:id) }).distinct.pluck(:id).to_set
	// （名前に反して、選択したユーザーのいずれかが所属するグループ）
	groups, err := repository.ListGroups(c.Ctx(), a.DB, false)
	if err != nil {
		a.serverError(c, err)
		return
	}
	common := map[int64]bool{}
	for _, u := range users {
		gids, err := repository.UserGroupIDs(c.Ctx(), a.DB, u.ID)
		if err != nil {
			a.serverError(c, err)
			return
		}
		for _, id := range gids {
			common[id] = true
		}
	}
	var commonGroups []*domain.Group
	for _, g := range groups {
		if common[g.ID] {
			commonGroups = append(commonGroups, g)
		}
	}
	data["Groups"] = groups
	data["CommonGroups"] = commonGroups
	c.Render("context_menus/users", data, RenderOptions{Layout: view.NoLayout})
}
