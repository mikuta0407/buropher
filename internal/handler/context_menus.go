// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"net/http"

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
	users, err := repository.UsersWhereIDs(c.Ctx(), a.DB, idsFromParam(c.Params().Slice("ids")))
	if err != nil {
		a.serverError(c, err)
		return
	}
	if len(users) == 0 {
		c.Render404("")
		return
	}
	// @back は users アクションでは設定されない（nil）
	data := map[string]any{"Users": users}
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
	c.Render("context_menus/users", data, RenderOptions{Layout: view.NoLayout})
}
