// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"net/http"

	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view"
)

// ContextMenusController（app/controllers/context_menus_controller.rb）。
// 現在は users アクションのみ（issues / time_entries / projects は各機能で追加する）。
var ContextMenusController = &Controller{Name: "context_menus", MainMenu: true}

// routesContextMenus は context_menus コントローラのルートを登録する。
func (a *App) routesContextMenus(r Router) {
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		// match '/users/context_menu', to: 'context_menus#users', via: [:get, :post]
		// 本家は管理者に限定しておらず、任意の id のユーザーの存在とロック状態が分かる（一覧は管理者専用）
		a.Handle(r, m, "/users/context_menu", ContextMenusController, "users", a.ContextMenusUsers, RequireAdmin())
	}
}

// ContextMenusUsers は context_menus#users（layout なし）。
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
