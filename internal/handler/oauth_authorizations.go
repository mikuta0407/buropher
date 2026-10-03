// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"errors"
	"html/template"
	"net/http"
	"slices"
	"strings"

	"github.com/mikuta0407/buropher/internal/auth/doorkeeper"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/repository"
)

// AuthorizationsController（Doorkeeper::AuthorizationsController。Redmine では layout "base"）。
//
// before_action :authenticate_resource_owner!（Redmine の resource_owner_authenticator: require_login し、
// REST API が無効なら deny_access）。require_sudo_mode :create, :destroy。
// handle_auth_errors は既定の :render なので、事前認可のエラーはリダイレクトせずエラー画面を表示する。
var AuthorizationsController = &Controller{Name: "authorizations", MainMenu: true}

// routesOAuthAuthorizations は use_doorkeeper の authorization のルートを登録する。
//
//	resource :authorization, path: "authorize", only: [:create, :destroy] do
//	  get "/native", action: :show, on: :member
//	  get "/", action: :new, on: :member
//	end
func (a *App) routesOAuthAuthorizations(r Router) {
	owner := Before(a.authenticateResourceOwner)
	a.Handle(r, http.MethodGet, "/oauth/authorize/native", AuthorizationsController, "show", a.OAuthAuthorizationsShow, owner)
	a.Handle(r, http.MethodGet, "/oauth/authorize", AuthorizationsController, "new", a.OAuthAuthorizationsNew, owner)
	a.Handle(r, http.MethodPost, "/oauth/authorize", AuthorizationsController, "create", a.OAuthAuthorizationsCreate, owner, RequireSudoMode())
	a.Handle(r, http.MethodDelete, "/oauth/authorize", AuthorizationsController, "destroy", a.OAuthAuthorizationsDestroy, owner, RequireSudoMode())
}

// authenticateResourceOwner は resource_owner_authenticator:
//
//	if require_login
//	  if Setting.rest_api_enabled? then User.current else deny_access end
//	end
func (a *App) authenticateResourceOwner(c *Req) {
	if !c.RequireLogin() {
		return
	}
	if !a.Settings.Bool("rest_api_enabled") {
		c.DenyAccess()
	}
}

// oauthPreAuth は Doorkeeper::OAuth::PreAuthorization。
type oauthPreAuth struct {
	a                   *App
	lang                string
	ClientID            string
	ResponseType        string
	responseMode        *string
	RedirectURI         *string
	scope               string
	State               *string
	CodeChallenge       *string
	CodeChallengeMethod *string
	client              *domain.OAuthApplication
	err                 *oauthError
}

// optParam は params[key]（無ければ nil）。
func optParam(c *Req, key string) *string {
	if s, ok := c.Params().StringOK(key); ok {
		return &s
	}
	return nil
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// newPreAuth は PreAuthorization.new(config, pre_auth_params, current_resource_owner)。
func (a *App) newPreAuth(c *Req) *oauthPreAuth {
	p := c.Params()
	return &oauthPreAuth{a: a, lang: c.Loc.Lang, ClientID: p.String("client_id"), ResponseType: p.String("response_type"),
		responseMode: optParam(c, "response_mode"), RedirectURI: optParam(c, "redirect_uri"), scope: p.String("scope"),
		State: optParam(c, "state"), CodeChallenge: optParam(c, "code_challenge"), CodeChallengeMethod: optParam(c, "code_challenge_method")}
}

// Client はクライアント（validate_client 後に設定）。
func (pa *oauthPreAuth) Client() *domain.OAuthApplication { return pa.client }

// Scope は PreAuthorization#scope（@scope.presence || build_scopes）。
func (pa *oauthPreAuth) Scope() string {
	if !blankStr(pa.scope) {
		return pa.scope
	}
	def := doorkeeper.DefaultScopes()
	if pa.client == nil || len(pa.client.ScopeList()) == 0 {
		return strings.Join(def, " ")
	}
	return strings.Join(doorkeeper.AllowedScopes(def, pa.client.ScopeList()), " ")
}

// Scopes は PreAuthorization#scopes。
func (pa *oauthPreAuth) Scopes() []string { return doorkeeper.ParseScopes(pa.Scope()) }

// IncludesAdmin は @pre_auth.scopes.include?('admin')。
func (pa *oauthPreAuth) IncludesAdmin() bool {
	return slices.Contains(pa.Scopes(), doorkeeper.AdminScope)
}

func optAny(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// 同意画面の hidden_field_tag の値（パラメータが無ければ nil で value 属性を出さない）。
func (pa *oauthPreAuth) RedirectURIValue() any         { return optAny(pa.RedirectURI) }
func (pa *oauthPreAuth) StateValue() any               { return optAny(pa.State) }
func (pa *oauthPreAuth) CodeChallengeValue() any       { return optAny(pa.CodeChallenge) }
func (pa *oauthPreAuth) CodeChallengeMethodValue() any { return optAny(pa.CodeChallengeMethod) }

// ResponseMode は response_mode（validate_response_mode で既定値 query が入る）。
func (pa *oauthPreAuth) ResponseMode() string { return derefStr(pa.responseMode) }

// responseOnFragment は PreAuthorization#response_on_fragment?。
func (pa *oauthPreAuth) responseOnFragment() bool {
	if pa.responseMode == nil {
		return pa.ResponseType == "token"
	}
	return *pa.responseMode == "fragment"
}

// formPost は form_post_response?。
func (pa *oauthPreAuth) formPost() bool { return derefStr(pa.responseMode) == "form_post" }

// fail は validate の失敗（error_response を作る）。
func (pa *oauthPreAuth) fail(name, missingParam string) bool {
	if name == "invalid_request" {
		pa.err = pa.a.newOAuthInvalidRequest(pa.lang, missingParam, "", derefStr(pa.State), derefStr(pa.RedirectURI))
	} else {
		pa.err = pa.a.newOAuthError(pa.lang, name, derefStr(pa.State), derefStr(pa.RedirectURI))
	}
	pa.err.ResponseOnFragment = pa.responseOnFragment()
	return false
}

// Authorizable は PreAuthorization#authorizable?（validate を順に行い、最初の失敗で止まる）。
func (pa *oauthPreAuth) Authorizable(c *Req) bool {
	pa.err = nil
	if blankStr(pa.ClientID) {
		return pa.fail("invalid_request", "client_id")
	}
	app, err := repository.FindOAuthApplicationByUID(c.Ctx(), pa.a.DB, pa.ClientID)
	if err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			pa.a.logger().Error("find oauth application", "err", err)
		}
		return pa.fail("invalid_client", "")
	}
	pa.client = app
	if blankStr(derefStr(pa.RedirectURI)) || !doorkeeper.ValidForAuthorization(*pa.RedirectURI, app.RedirectURI) {
		return pa.fail("invalid_redirect_uri", "")
	}
	if blankStr(pa.ResponseType) {
		return pa.fail("invalid_request", "response_type")
	}
	// authorization_response_flows は authorization_code（response_type "code"）のみ
	if pa.ResponseType != "code" {
		return pa.fail("unsupported_response_type", "")
	}
	if pa.responseMode == nil || blankStr(*pa.responseMode) {
		q := "query"
		pa.responseMode = &q
	} else if !slices.Contains([]string{"query", "fragment", "form_post"}, *pa.responseMode) {
		return pa.fail("unsupported_response_mode", "")
	}
	if !doorkeeper.ScopeValid(pa.Scope(), doorkeeper.ServerScopes(), app.ScopeList()) {
		return pa.fail("invalid_scope", "")
	}
	if !blankStr(derefStr(pa.CodeChallenge)) {
		m := derefStr(pa.CodeChallengeMethod)
		if blankStr(m) || !slices.Contains(doorkeeper.PKCEMethods, m) {
			return pa.fail("invalid_code_challenge_method", "")
		}
	}
	return true
}

// authorizationsRender は render :new / :error / :show / :form_post。
func (a *App) renderAuthorizations(c *Req, name string, data map[string]any, status int) {
	c.Render("doorkeeper/authorizations/"+name, data, RenderOptions{Status: status})
}

// renderPreAuthError は AuthorizationsController#render_error（render :error, status: error_response.status）。
func (a *App) renderPreAuthError(c *Req, pa *oauthPreAuth, status int) {
	a.renderAuthorizations(c, "error", map[string]any{"ErrorDescription": pa.err.Description}, status)
}

// OAuthAuthorizationsNew は AuthorizationsController#new（GET /oauth/authorize）。
func (a *App) OAuthAuthorizationsNew(c *Req) {
	pa := a.newPreAuth(c)
	if !pa.Authorizable(c) {
		a.renderPreAuthError(c, pa, pa.err.Status)
		return
	}
	// render_success: 機密クライアントで同じスコープの有効なトークンが既にあれば同意画面を省略する
	if pa.client.Confidential && a.matchingOAuthToken(c, pa) {
		a.oauthRedirectOrRender(c, pa, a.oauthAuthorizeResponse(c, pa))
		return
	}
	name := template.HTMLEscapeString(pa.client.Name)
	prompt := c.Loc.Bundle.T(c.Loc.Lang, "doorkeeper.authorizations.new.prompt", i18n.Vars{"client_name": `<strong class="text-info">` + name + `</strong>`})
	a.renderAuthorizations(c, "new", map[string]any{"PreAuth": pa, "Prompt": template.HTML(prompt)}, http.StatusOK)
}

// matchingOAuthToken は AccessToken.matching_token_for(client, resource_owner, scopes)（期限切れを含む）。
func (a *App) matchingOAuthToken(c *Req, pa *oauthPreAuth) bool {
	tokens, err := repository.AuthorizedOAuthTokens(c.Ctx(), a.DB, pa.client.ID, c.User.ID)
	if err != nil {
		a.logger().Error("authorized oauth tokens", "err", err)
		return false
	}
	scopes := pa.Scopes()
	for _, t := range tokens {
		if doorkeeper.ScopesMatch(t.ScopeList(), scopes, pa.client.ScopeList()) {
			return true
		}
	}
	return false
}

// oauthAuthResponse は CodeResponse（成功）または ErrorResponse。
type oauthAuthResponse struct {
	code string
	err  *oauthError
}

// oauthAuthorizeResponse は AuthorizationsController#authorize_response（認可コードを発行する）。
func (a *App) oauthAuthorizeResponse(c *Req, pa *oauthPreAuth) oauthAuthResponse {
	if !pa.Authorizable(c) {
		return oauthAuthResponse{err: pa.err}
	}
	plain := doorkeeper.GenerateToken()
	g := &domain.OAuthAccessGrant{ResourceOwnerID: c.User.ID, ApplicationID: pa.client.ID, Token: doorkeeper.HashToken(plain),
		ExpiresIn: doorkeeper.AuthorizationCodeExpiresIn, RedirectURI: derefStr(pa.RedirectURI), Scopes: strings.Join(pa.Scopes(), " "),
		CodeChallenge: derefStr(pa.CodeChallenge), CodeChallengeMethod: derefStr(pa.CodeChallengeMethod)}
	if err := repository.CreateOAuthAccessGrant(c.Ctx(), a.DB, g, a.now()); err != nil {
		a.logger().Error("create oauth grant", "err", err)
		return oauthAuthResponse{err: a.newOAuthError(pa.lang, "server_error", derefStr(pa.State), derefStr(pa.RedirectURI))}
	}
	return oauthAuthResponse{code: plain}
}

// oauthRedirectOrRender は AuthorizationsController#redirect_or_render。
func (a *App) oauthRedirectOrRender(c *Req, pa *oauthPreAuth, res oauthAuthResponse) {
	if res.err != nil {
		e := res.err
		if !e.Redirectable() {
			renderOAuthJSON(c.W, e.Status, jsonObject(e.Body()))
			c.Halt()
			return
		}
		if pa.formPost() {
			a.renderFormPost(c, pa, e.Body())
			return
		}
		c.Redirect(e.Location())
		return
	}
	redirectURI := derefStr(pa.RedirectURI)
	body := []jsonKV{{"code", res.code}}
	if s := derefStr(pa.State); !blankStr(s) {
		body = append(body, jsonKV{"state", s})
	}
	switch {
	case pa.formPost():
		a.renderFormPost(c, pa, body)
	case doorkeeper.IsOOB(redirectURI):
		// Authorization::Code#oob_redirect（{action: :show, code: ...}）
		c.Redirect("/oauth/authorize/native?code=" + escapeQueryValue(res.code))
	default:
		params := []doorkeeper.Param{{Key: "code", Value: res.code}, {Key: "state", Value: derefStr(pa.State)}}
		if pa.ResponseMode() == "fragment" {
			c.Redirect(doorkeeper.URIWithFragment(redirectURI, params))
		} else {
			c.Redirect(doorkeeper.URIWithQuery(redirectURI, params))
		}
	}
}

// escapeQueryValue は url_for のクエリ値のエスケープ（認可コードは URL-safe なのでそのまま）。
func escapeQueryValue(s string) string {
	return strings.NewReplacer("%", "%25", "&", "%26", "+", "%2B", "#", "%23", " ", "+").Replace(s)
}

// renderFormPost は render :form_post（response_mode=form_post）。
func (a *App) renderFormPost(c *Req, pa *oauthPreAuth, body []jsonKV) {
	fields := make([][2]string, 0, len(body))
	for _, kv := range body {
		if s, ok := kv.Value.(string); ok {
			fields = append(fields, [2]string{kv.Key, s})
		}
	}
	a.renderAuthorizations(c, "form_post", map[string]any{"RedirectURI": derefStr(pa.RedirectURI), "Fields": fields}, http.StatusOK)
}

// OAuthAuthorizationsCreate は AuthorizationsController#create（POST /oauth/authorize。承認）。
func (a *App) OAuthAuthorizationsCreate(c *Req) {
	pa := a.newPreAuth(c)
	a.oauthRedirectOrRender(c, pa, a.oauthAuthorizeResponse(c, pa))
}

// OAuthAuthorizationsDestroy は AuthorizationsController#destroy（DELETE /oauth/authorize。拒否）。
// authorization.deny は事前認可の検証をせずに access_denied を返す（Doorkeeper と同じく redirect_uri も検証しない）。
func (a *App) OAuthAuthorizationsDestroy(c *Req) {
	pa := a.newPreAuth(c)
	switch pa.ResponseType {
	case "code", "token":
		// CodeRequest#deny / TokenRequest#deny
		e := a.newOAuthError(pa.lang, "access_denied", derefStr(pa.State), derefStr(pa.RedirectURI))
		e.ResponseOnFragment = pa.responseOnFragment()
		a.oauthRedirectOrRender(c, pa, oauthAuthResponse{err: e})
	default:
		// Errors::InvalidTokenStrategy → render :error（@pre_auth は未検証なので説明は server_error）
		a.renderAuthorizations(c, "error", map[string]any{"ErrorDescription": a.oauthErrorDescription(pa.lang, "server_error")}, http.StatusOK)
	}
}

// OAuthAuthorizationsShow は AuthorizationsController#show（GET /oauth/authorize/native。OOB の認可コード表示）。
func (a *App) OAuthAuthorizationsShow(c *Req) {
	a.renderAuthorizations(c, "show", map[string]any{"Code": c.Params().String("code")}, http.StatusOK)
}
