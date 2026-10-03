// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"encoding/base64"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/mikuta0407/buropher/internal/auth/doorkeeper"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
)

// Doorkeeper::TokensController（create / revoke）と Doorkeeper::TokenInfoController（show）。
//
// どちらも ActionController::API（base_metal_controller）を継承するため、Redmine の ApplicationController の
// before_action（user_setup・ログイン要求・CSRF・ロケール設定）は通らない。エラーの説明文は既定ロケール（en）。
// grant_flows は authorization_code のみ（+ use_refresh_token による refresh_token）なので、
// client_credentials / password / implicit は unsupported_grant_type になる。
// allow_token_introspection false のため /oauth/introspect のルートは無い（404）。

// oauthTokenLang はトークンエンドポイントの I18n.locale。
const oauthTokenLang = "en"

// routesOAuthTokens は use_doorkeeper の token / revoke / token_info のルートを登録する。
//
//	resource :token, path: "token", only: [:create]
//	post "revoke"
//	resource :token_info, path: "token/info", only: [:show]
func (a *App) routesOAuthTokens(r Router) {
	httpx.Route(r, http.MethodPost, "/oauth/token", a.OAuthTokenCreate)
	httpx.Route(r, http.MethodPost, "/oauth/revoke", a.OAuthTokenRevoke)
	httpx.Route(r, http.MethodGet, "/oauth/token/info", a.OAuthTokenInfo)
}

// oauthParams は request.parameters の String。
type oauthParams interface {
	String(keys ...string) string
}

func blankStr(s string) bool { return strings.TrimSpace(s) == "" }

var basicAuthRe = regexp.MustCompile(`(?s)^Basic (.*)`)

// lenientBase64 は Base64.decode64（不正な文字は無視する）。
func lenientBase64(s string) string {
	var b strings.Builder
	for _, c := range s {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' {
			b.WriteRune(c)
		}
	}
	clean := b.String()
	for len(clean)%4 == 1 {
		clean = clean[:len(clean)-1]
	}
	out, err := base64.RawStdEncoding.DecodeString(clean)
	if err != nil {
		return ""
	}
	return string(out)
}

// oauthCredentials は OAuth::Client::Credentials.from_request（from_basic → from_params）。
// 見つからなければ ok = false（credentials.blank?）。
func oauthCredentials(r *http.Request, p oauthParams) (uid, secret string, ok bool) {
	if m := basicAuthRe.FindStringSubmatch(r.Header.Get("Authorization")); m != nil {
		dec := lenientBase64(m[1])
		u, s, _ := strings.Cut(dec, ":")
		if !blankStr(u) {
			return u, s, true
		}
	}
	if u := p.String("client_id"); !blankStr(u) {
		return u, p.String("client_secret"), true
	}
	return "", "", false
}

// oauthClient は Server#client（OAuth::Client.authenticate(credentials) = Application.by_uid_and_secret）。
func (a *App) oauthClient(r *http.Request, p oauthParams) *domain.OAuthApplication {
	uid, secret, ok := oauthCredentials(r, p)
	if !ok {
		return nil
	}
	return a.oauthAppByUIDAndSecret(r, uid, secret)
}

// oauthAppByUIDAndSecret は Application.by_uid_and_secret。
func (a *App) oauthAppByUIDAndSecret(r *http.Request, uid, secret string) *domain.OAuthApplication {
	app, err := repository.FindOAuthApplicationByUID(r.Context(), a.DB, uid)
	if err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			a.logger().Error("find oauth application", "err", err)
		}
		return nil
	}
	if blankStr(secret) && !app.Confidential {
		return app
	}
	if !doorkeeper.SecretMatches(secret, app.Secret) {
		return nil
	}
	return app
}

// oauthTokenResult は TokenResponse（成功）か ErrorResponse（失敗）。
type oauthTokenResult struct {
	token        *domain.OAuthAccessToken
	plain        string
	plainRefresh string
	err          *oauthError
}

// OAuthTokenCreate は TokensController#create（POST /oauth/token）。
func (a *App) OAuthTokenCreate(w http.ResponseWriter, r *http.Request) {
	p := httpx.ParamsOf(r)
	var res oauthTokenResult
	grantType := p.String("grant_type")
	switch {
	case blankStr(grantType):
		// Errors::MissingRequiredParameter（handle_token_exception。state は params[:state]）
		res.err = a.newOAuthInvalidRequest(oauthTokenLang, "grant_type", "", p.String("state"), "")
	case grantType == "authorization_code":
		res = a.oauthAuthorizationCodeGrant(r, p)
	case grantType == "refresh_token":
		res = a.oauthRefreshTokenGrant(r, p)
	default:
		// Errors::InvalidTokenStrategy
		res.err = a.newOAuthError(oauthTokenLang, "unsupported_grant_type", p.String("state"), "")
	}
	if res.err != nil {
		res.err.setHeaders(w, a.Realm())
		renderOAuthJSON(w, res.err.Status, jsonObject(res.err.Body()))
		return
	}
	t := res.token
	body := jsonObject{{"access_token", res.plain}, {"token_type", "Bearer"}}
	if exp := t.ExpiresInSeconds(a.now()); exp != nil {
		body = append(body, jsonKV{"expires_in", *exp})
	}
	if res.plainRefresh != "" {
		body = append(body, jsonKV{"refresh_token", res.plainRefresh})
	}
	if !blankStr(t.Scopes) {
		body = append(body, jsonKV{"scope", t.Scopes})
	}
	body = append(body, jsonKV{"created_at", t.CreatedAt.Unix()})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	renderOAuthJSON(w, http.StatusOK, body)
}

// createOAuthAccessToken は AccessToken.create_for（use_refresh_token: true）。平文のトークンも返す。
func (a *App) createOAuthAccessToken(r *http.Request, q db.Queryer, appID, ownerID *int64, scopes string, expiresIn *int, previousRefresh string) (*domain.OAuthAccessToken, string, string, error) {
	plain, refresh := doorkeeper.GenerateToken(), doorkeeper.GenerateToken()
	t := &domain.OAuthAccessToken{ResourceOwnerID: ownerID, ApplicationID: appID, Token: doorkeeper.HashToken(plain),
		RefreshToken: doorkeeper.HashToken(refresh), ExpiresIn: expiresIn, Scopes: scopes, PreviousRefreshToken: previousRefresh}
	if err := repository.CreateOAuthAccessToken(r.Context(), q, t, a.now()); err != nil {
		return nil, "", "", err
	}
	return t, plain, refresh, nil
}

var errOAuthGrantReuse = errors.New("invalid grant reuse")

// oauthAuthorizationCodeGrant は Request::AuthorizationCode + OAuth::AuthorizationCodeRequest。
func (a *App) oauthAuthorizationCodeGrant(r *http.Request, p oauthParams) oauthTokenResult {
	lang := oauthTokenLang
	code := p.String("code")
	if blankStr(code) {
		return oauthTokenResult{err: a.newOAuthInvalidRequest(lang, "code", "", p.String("state"), "")}
	}
	ctx := r.Context()
	grant, err := repository.FindOAuthAccessGrantByToken(ctx, a.DB, doorkeeper.HashToken(code))
	if err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			a.logger().Error("find oauth grant", "err", err)
			return oauthTokenResult{err: a.newOAuthError(lang, "server_error", "", "")}
		}
		grant = nil
	}
	client := a.oauthClient(r, p)
	redirectURI, verifier := p.String("redirect_uri"), p.String("code_verifier")
	now := a.now()
	// validate :params
	switch {
	case grant != nil && grant.UsesPKCE() && blankStr(verifier):
		return oauthTokenResult{err: a.newOAuthInvalidRequest(lang, "code_verifier", "", "", "")}
	case blankStr(redirectURI):
		return oauthTokenResult{err: a.newOAuthInvalidRequest(lang, "redirect_uri", "", "", "")}
	}
	// validate :client
	if client == nil {
		return oauthTokenResult{err: a.newOAuthError(lang, "invalid_client", "", "")}
	}
	invalidGrant := oauthTokenResult{err: a.newOAuthError(lang, "invalid_grant", "", "")}
	// validate :grant
	if grant == nil || grant.ApplicationID != client.ID || !grant.Accessible(now) {
		return invalidGrant
	}
	// validate :redirect_uri
	if !doorkeeper.ValidForAuthorization(redirectURI, grant.RedirectURI) {
		return invalidGrant
	}
	// validate :code_verifier
	if blankStr(verifier) {
		if !blankStr(grant.CodeChallenge) {
			return invalidGrant
		}
	} else {
		switch grant.CodeChallengeMethod {
		case "S256":
			if grant.CodeChallenge != doorkeeper.CodeChallengeS256(verifier) {
				return invalidGrant
			}
		case "plain":
			if grant.CodeChallenge != verifier {
				return invalidGrant
			}
		default:
			return invalidGrant
		}
	}
	var res oauthTokenResult
	err = a.DB.WithTx(ctx, func(tx *db.Tx) error {
		ok, err := repository.RevokeOAuthAccessGrant(ctx, tx, grant.ID, now)
		if err != nil {
			return err
		}
		if !ok {
			return errOAuthGrantReuse
		}
		exp := doorkeeper.AccessTokenExpiresIn
		owner := grant.ResourceOwnerID
		scopes := strings.Join(doorkeeper.ParseScopes(grant.Scopes), " ")
		res.token, res.plain, res.plainRefresh, err = a.createOAuthAccessToken(r, tx, &client.ID, &owner, scopes, &exp, "")
		return err
	})
	if errors.Is(err, errOAuthGrantReuse) {
		// Errors::InvalidGrantReuse（handle_token_exception）
		return oauthTokenResult{err: a.newOAuthError(lang, "invalid_grant", p.String("state"), "")}
	}
	if err != nil {
		a.logger().Error("create oauth access token", "err", err)
		return oauthTokenResult{err: a.newOAuthError(lang, "server_error", "", "")}
	}
	return res
}

// oauthRefreshTokenGrant は Request::RefreshToken + OAuth::RefreshTokenRequest。
func (a *App) oauthRefreshTokenGrant(r *http.Request, p oauthParams) oauthTokenResult {
	lang := oauthTokenLang
	ctx := r.Context()
	param := p.String("refresh_token")
	var old *domain.OAuthAccessToken
	if param != "" {
		t, err := repository.FindOAuthAccessTokenByRefreshToken(ctx, a.DB, doorkeeper.HashToken(param))
		if err == nil {
			old = t
		} else if !errors.Is(err, repository.ErrNotFound) {
			a.logger().Error("find oauth refresh token", "err", err)
			return oauthTokenResult{err: a.newOAuthError(lang, "server_error", "", "")}
		}
	}
	uid, secret, hasCreds := oauthCredentials(r, p)
	var client *domain.OAuthApplication
	if hasCreds {
		client = a.oauthAppByUIDAndSecret(r, uid, secret)
	}
	originalScopes := p.String("scope")
	if originalScopes == "" {
		originalScopes = p.String("scopes")
	}
	now := a.now()
	// validate :token_presence
	if old == nil && blankStr(param) {
		return oauthTokenResult{err: a.newOAuthInvalidRequest(lang, "refresh_token", "", "", "")}
	}
	invalidGrant := oauthTokenResult{err: a.newOAuthError(lang, "invalid_grant", "", "")}
	// validate :token
	if old == nil || old.Revoked(now) {
		return invalidGrant
	}
	// validate :client
	if hasCreds && client == nil {
		return oauthTokenResult{err: a.newOAuthError(lang, "invalid_client", "", "")}
	}
	// validate :client_match
	if old.ApplicationID != nil && (client == nil || *old.ApplicationID != client.ID) {
		return invalidGrant
	}
	// validate :scope
	scopes := strings.Join(old.ScopeList(), " ")
	if !blankStr(originalScopes) {
		if !doorkeeper.ScopeValid(originalScopes, old.ScopeList(), nil) {
			return oauthTokenResult{err: a.newOAuthError(lang, "invalid_scope", "", "")}
		}
		scopes = strings.Join(doorkeeper.ParseScopes(originalScopes), " ")
	}
	var res oauthTokenResult
	err := a.DB.WithTx(ctx, func(tx *db.Tx) error {
		// refresh_token.lock! / raise InvalidGrantReuse if refresh_token.revoked?
		cur, err := repository.FindOAuthAccessTokenByRefreshToken(ctx, tx, old.RefreshToken)
		if err != nil {
			return err
		}
		if cur.Revoked(now) {
			return errOAuthGrantReuse
		}
		// previous_refresh_token 列があるため旧トークンはここでは失効させない（refresh_token_revoked_on_use?）
		res.token, res.plain, res.plainRefresh, err = a.createOAuthAccessToken(r, tx, old.ApplicationID, old.ResourceOwnerID, scopes, old.ExpiresIn, old.RefreshToken)
		return err
	})
	if errors.Is(err, errOAuthGrantReuse) {
		return oauthTokenResult{err: a.newOAuthError(lang, "invalid_grant", p.String("state"), "")}
	}
	if err != nil {
		a.logger().Error("refresh oauth access token", "err", err)
		return oauthTokenResult{err: a.newOAuthError(lang, "server_error", "", "")}
	}
	return res
}

// oauthRevocationError は TokensController#revocation_error_response（403）。
func (a *App) renderOAuthRevocationError(w http.ResponseWriter) {
	renderOAuthJSON(w, http.StatusForbidden, jsonObject{{"error", "unauthorized_client"},
		{"error_description", a.Bundle.T(oauthTokenLang, "doorkeeper.errors.messages.revoke.unauthorized", nil)}})
}

// OAuthTokenRevoke は TokensController#revoke（POST /oauth/revoke。RFC 7009）。
func (a *App) OAuthTokenRevoke(w http.ResponseWriter, r *http.Request) {
	p := httpx.ParamsOf(r)
	ctx := r.Context()
	// before_action :validate_presence_of_client
	client := a.oauthClient(r, p)
	if client == nil {
		a.renderOAuthRevocationError(w)
		return
	}
	hashed := doorkeeper.HashToken(p.String("token"))
	var tok *domain.OAuthAccessToken
	isRefresh := false
	if p.String("token_type_hint") == "refresh_token" {
		tok, _ = repository.FindOAuthAccessTokenByRefreshToken(ctx, a.DB, hashed)
		isRefresh = true
	} else {
		var err error
		if tok, err = repository.FindOAuthAccessTokenByToken(ctx, a.DB, hashed); err != nil {
			tok, _ = repository.FindOAuthAccessTokenByRefreshToken(ctx, a.DB, hashed)
			isRefresh = true
		}
	}
	if tok == nil {
		renderOAuthJSON(w, http.StatusOK, jsonObject{})
		return
	}
	// authorized?: 機密クライアントのトークンは発行先のクライアントのみ失効できる
	if tok.ApplicationID != nil {
		app, err := repository.GetOAuthApplication(ctx, a.DB, *tok.ApplicationID)
		if err == nil && app.Confidential && client.ID != app.ID {
			a.renderOAuthRevocationError(w)
			return
		}
	}
	now := a.now()
	revocable := !tok.Revoked(now)
	if !isRefresh {
		revocable = tok.Accessible(now)
	}
	if revocable {
		if _, err := repository.RevokeOAuthAccessToken(ctx, a.DB, tok.ID, now); err != nil {
			a.logger().Error("revoke oauth token", "err", err)
		}
	}
	renderOAuthJSON(w, http.StatusOK, jsonObject{})
}

// OAuthTokenInfo は TokenInfoController#show（GET /oauth/token/info）。
func (a *App) OAuthTokenInfo(w http.ResponseWriter, r *http.Request) {
	p := httpx.ParamsOf(r)
	tok := a.authenticateOAuthToken(r, p)
	now := a.now()
	if tok == nil || !tok.Accessible(now) {
		e := a.newOAuthInvalidToken(oauthTokenLang, "unknown")
		e.setHeaders(w, a.Realm())
		renderOAuthJSON(w, e.Status, jsonObject(e.Body()))
		return
	}
	var owner any
	if tok.ResourceOwnerID != nil {
		owner = *tok.ResourceOwnerID
	}
	var exp any
	if e := tok.ExpiresInSeconds(now); e != nil {
		exp = *e
	}
	var uid any
	if tok.ApplicationID != nil {
		if app, err := repository.GetOAuthApplication(r.Context(), a.DB, *tok.ApplicationID); err == nil {
			uid = app.UID
		}
	}
	scopes := tok.ScopeList()
	if scopes == nil {
		scopes = []string{}
	}
	w.Header().Set("Cache-Control", "no-store")
	renderOAuthJSON(w, http.StatusOK, jsonObject{{"resource_owner_id", owner}, {"scope", scopes}, {"expires_in", exp},
		{"application", jsonObject{{"uid", uid}}}, {"created_at", tok.CreatedAt.Unix()}})
}
