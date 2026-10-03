// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"net/http"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

// このファイルはリクエスト単位の「現在のユーザー」（User.current）と、その権限判定器
// （authz.Authorizer）へのアクセサを定義する。
//
//	u := handler.CurrentUser(r)      // *domain.User（匿名なら AnonymousUser。Handle の外では nil）
//	az := handler.Authz(r)           // *authz.Authorizer（初回呼び出し時に作る）
//	ok := c.AllowedTo(domain.Perm("view_issues"), c.Project)

// CurrentUser は User.current を返す（App.Handle の外では nil）。
func CurrentUser(r *http.Request) *domain.User {
	if c := ReqOf(r); c != nil {
		return c.User
	}
	return nil
}

// Authz は User.current の Authorizer を返す（App.Handle の外では nil）。
func Authz(r *http.Request) *authz.Authorizer {
	if c := ReqOf(r); c != nil && c.User != nil {
		return c.Authz()
	}
	return nil
}

// SetUser は User.current を置き換える（Authorizer と個人設定のキャッシュも捨てる）。
func (c *Req) SetUser(u *domain.User) {
	c.User = u
	c.authz = nil
	c.pref = nil
}

// Authz は User.current の Authorizer（リクエスト内で 1 つ。初回呼び出し時に作る）。
// Authorizer はメンバーシップ等をキャッシュするため、権限に関わるデータを変更した後は
// ResetAuthz で作り直すこと。
func (c *Req) Authz() *authz.Authorizer {
	if c.authz == nil {
		c.authz = authz.New(c.App.DB, c.User)
	}
	return c.authz
}

// ResetAuthz は Authorizer のキャッシュを捨てる（メンバーシップ・ロール等の変更後に呼ぶ）。
func (c *Req) ResetAuthz() { c.authz = nil }

// Pref は User.current.pref（行が無ければ既定値）。
func (c *Req) Pref() *domain.UserPreference {
	if c.pref == nil {
		var id int64
		if c.User != nil && c.User.Logged() {
			id = c.User.ID
		}
		p, err := repository.GetUserPreference(c.Ctx(), c.App.DB, id)
		if err != nil {
			c.App.logger().Error("user preference", "err", err)
			p = domain.DefaultUserPreference(id)
		}
		c.pref = p
	}
	return c.pref
}

// AllowedTo は User.current.allowed_to?(action, project)。DB エラーはログに残して false。
func (c *Req) AllowedTo(action domain.Action, p *domain.Project) bool {
	ok, err := c.Authz().AllowedTo(c.Ctx(), action, p)
	if err != nil {
		c.App.logger().Error("allowed_to", "action", action.String(), "err", err)
		return false
	}
	return ok
}

// AllowedToGlobally は User.current.allowed_to?(action, nil, global: true)。DB エラーは false。
func (c *Req) AllowedToGlobally(action domain.Action) bool {
	ok, err := c.Authz().AllowedToGlobally(c.Ctx(), action, nil)
	if err != nil {
		c.App.logger().Error("allowed_to_globally", "action", action.String(), "err", err)
		return false
	}
	return ok
}

// AtomKey は User.current.atom_key（匿名なら ""。ログインユーザーは無ければ作成する）。
func (c *Req) AtomKey() string {
	if c.User == nil || !c.User.Logged() {
		return ""
	}
	k, err := repository.AtomKey(c.Ctx(), c.App.DB, c.User.ID)
	if err != nil {
		c.App.logger().Error("atom key", "err", err)
		return ""
	}
	return k
}

// recordProjectUsage は ApplicationController#record_project_usage（after_action）:
// @project を表示したログインユーザーの「最近使ったプロジェクト」を更新する。
func (a *App) recordProjectUsage(c *Req) {
	p := c.Project
	if p == nil || p.ID == 0 || c.User == nil || !c.User.Logged() {
		return
	}
	if !c.AllowedTo(domain.Perm("view_project"), p) {
		return
	}
	if err := repository.ProjectUsed(c.Ctx(), a.DB, c.User.ID, p.ID, c.Pref().RecentlyUsedProjects); err != nil {
		a.logger().Error("record project usage", "err", err)
	}
}
