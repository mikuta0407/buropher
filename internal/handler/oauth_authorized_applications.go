// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"net/http"

	"github.com/mikuta0407/buropher/internal/repository"
)

// AuthorizedApplicationsController（Doorkeeper::AuthorizedApplicationsController。Redmine では layout "base"・
// main_menu = false）。before_action :authenticate_resource_owner!。
var AuthorizedApplicationsController = &Controller{Name: "authorized_applications", MainMenu: false}

// routesOAuthAuthorizedApplications は use_doorkeeper の authorized_applications。
//
//	resources :authorized_applications, only: [:index, :destroy]
func (a *App) routesOAuthAuthorizedApplications(r Router) {
	owner := Before(a.authenticateResourceOwner)
	ctrl := AuthorizedApplicationsController
	a.Handle(r, http.MethodGet, "/oauth/authorized_applications", ctrl, "index", a.OAuthAuthorizedApplicationsIndex, owner)
	a.Handle(r, http.MethodDelete, "/oauth/authorized_applications/{id}", ctrl, "destroy", a.OAuthAuthorizedApplicationsDestroy, owner)
}

// OAuthAuthorizedApplicationsIndex は index（Application.authorized_for(current_resource_owner)）。
func (a *App) OAuthAuthorizedApplicationsIndex(c *Req) {
	apps, err := repository.AuthorizedOAuthApplications(c.Ctx(), a.DB, c.User.ID)
	if err != nil {
		a.internalError(c, "authorized oauth applications", err)
		return
	}
	data := map[string]any{"Applications": apps}
	// content_for :sidebar で my/_sidebar を描画する（@user = User.current）
	if err := a.mySidebarData(c, c.User, data); err != nil {
		a.internalError(c, "my sidebar", err)
		return
	}
	c.Render("doorkeeper/authorized_applications/index", data)
}

// OAuthAuthorizedApplicationsDestroy は destroy（Application.revoke_tokens_and_grants_for(params[:id], current_resource_owner)）。
func (a *App) OAuthAuthorizedApplicationsDestroy(c *Req) {
	// params[:id] は where(application_id: ...) にそのまま渡る（整数でなければ一致しない）
	if id, ok := c.Params().IntStrict("id"); ok {
		if err := repository.RevokeOAuthTokensAndGrantsFor(c.Ctx(), a.DB, id, c.User.ID, a.now()); err != nil {
			a.internalError(c, "revoke oauth tokens", err)
			return
		}
	}
	c.Flash().SetNotice(c.L("doorkeeper.flash.authorized_applications.destroy.notice"))
	c.Redirect("/oauth/authorized_applications")
}
