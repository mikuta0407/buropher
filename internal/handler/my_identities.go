// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

// このファイルはマイアカウントの外部 ID 連携（buropher 拡張）。
//
//	GET    /my/sso       連携中の外部 ID の一覧と、未連携の OIDC 認証方式の「連携する」ボタン
//	DELETE /my/sso/{id}  連携の解除

// routesMyIdentities は外部 ID 連携のルート。
func (a *App) routesMyIdentities(r Router) {
	login := RequireLogin()
	a.Handle(r, http.MethodGet, "/my/sso", MyController, "sso", a.MySSO, login)
	a.Handle(r, http.MethodDelete, "/my/sso/{id}", MyController, "sso_unlink", a.MySSOUnlink, login)
}

// identityRow は一覧の 1 行。
type identityRow struct {
	Identity *domain.UserIdentity
	Source   string
}

// MySSO は GET /my/sso。
func (a *App) MySSO(c *Req) {
	ids, err := repository.UserIdentities(c.Ctx(), a.DB, c.User.ID)
	if err != nil {
		a.serverError(c, err)
		return
	}
	sources := a.oidcSources(c)
	names := map[string]string{}
	for _, rec := range sources {
		names[oidcProviderKey(rec)] = rec.Name
	}
	linked := map[string]bool{}
	var rows []identityRow
	for _, id := range ids {
		name := names[id.Provider]
		if name == "" {
			if id.AuthSourceID != nil {
				if rec, err := repository.GetAuthSource(c.Ctx(), a.DB, *id.AuthSourceID); err == nil {
					name = rec.Name
				}
			}
			if name == "" {
				name = id.Provider
			}
		}
		linked[id.Provider] = true
		rows = append(rows, identityRow{Identity: id, Source: name})
	}
	var linkable []ssoButton
	for _, rec := range sources {
		if !linked[oidcProviderKey(rec)] {
			linkable = append(linkable, ssoButton{Label: rec.Name, URL: "/auth/oidc/" + strconv.FormatInt(rec.ID, 10) + "/start?mode=link"})
		}
	}
	data := map[string]any{"Identities": rows, "Linkable": linkable}
	if err := a.mySidebarData(c, c.User, data); err != nil {
		a.serverError(c, err)
		return
	}
	c.Render("my/sso", data)
}

// MySSOUnlink は DELETE /my/sso/{id}。パスワードでログインできないユーザーの最後の連携は解除させない。
func (a *App) MySSOUnlink(c *Req) {
	id, ok := c.Params().IntStrict("id")
	if !ok {
		c.Render404("")
		return
	}
	ids, err := repository.UserIdentities(c.Ctx(), a.DB, c.User.ID)
	if err != nil {
		a.serverError(c, err)
		return
	}
	// ログイン手段になる OIDC の連携だけを数える（Discord の連携ではログインできない）
	found, target, logins := false, "", 0
	for _, ident := range ids {
		if ident.ID == id {
			found, target = true, ident.Provider
		}
		if strings.HasPrefix(ident.Provider, "oidc:") {
			logins++
		}
	}
	if !found {
		c.Render404("")
		return
	}
	u, err := repository.GetUser(c.Ctx(), a.DB, c.User.ID)
	if err != nil {
		a.serverError(c, err)
		return
	}
	passwordLogin := u.AuthSourceID == nil && u.PasswordHash != ""
	if u.AuthSourceID != nil {
		if rec, err := repository.GetAuthSource(c.Ctx(), a.DB, *u.AuthSourceID); err == nil && rec.Kind == domain.AuthSourceKindLDAP {
			passwordLogin = true
		}
	}
	if strings.HasPrefix(target, "oidc:") && logins == 1 && !passwordLogin {
		c.Flash().SetError(c.L("buropher.sso.error_last_identity"))
		c.Redirect("/my/sso")
		return
	}
	if _, err := repository.DeleteUserIdentity(c.Ctx(), a.DB, c.User.ID, id); err != nil {
		a.serverError(c, err)
		return
	}
	c.Flash().SetNotice(c.L("buropher.sso.notice_identity_unlinked"))
	c.Redirect("/my/sso")
}
