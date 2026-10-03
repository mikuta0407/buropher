package handler

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"regexp"
	"strings"

	"github.com/mikuta0407/buropher/internal/auth/doorkeeper"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/repository"
)

// このファイルは Doorkeeper の共通部分（OAuth::ErrorResponse / InvalidRequestResponse /
// InvalidTokenResponse、OAuth::Token.authenticate、JSON 応答）の移植。
// 個々のエンドポイントは oauth2_applications.go / oauth_authorizations.go / oauth_tokens.go /
// oauth_authorized_applications.go。

// routesOAuth は config/routes.rb の use_doorkeeper（controllers applications: 'oauth2_applications'）。
func (a *App) routesOAuth(r Router) {
	a.routesOAuthAuthorizations(r)
	a.routesOAuthTokens(r)
	a.routesOAuth2Applications(r)
	a.routesOAuthAuthorizedApplications(r)
}

// doorkeeperT はビューの t(key)。訳が無ければ Rails の translation_missing の span（"Callback Url" など）を返す。
func doorkeeperT(c *Req, key string) template.HTML {
	if v, ok := c.Loc.Bundle.Translate(c.Loc.Lang, key, nil); ok {
		return template.HTML(template.HTMLEscapeString(i18n.RubyToS(v)))
	}
	last := key[strings.LastIndex(key, ".")+1:]
	words := strings.Split(last, "_")
	for i, w := range words {
		if w != "" {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return template.HTML(`<span class="translation_missing" title="translation missing: ` +
		template.HTMLEscapeString(c.Loc.Lang+"."+key) + `">` + template.HTMLEscapeString(strings.Join(words, " ")) + `</span>`)
}

// doorkeeperTFunc はテンプレートから {{call .T "key"}} で使う doorkeeperT。
func doorkeeperTFunc(c *Req) func(string) template.HTML {
	return func(key string) template.HTML { return doorkeeperT(c, key) }
}

// oauthError は Doorkeeper::OAuth::ErrorResponse（とそのサブクラス）。
type oauthError struct {
	// Name は error（invalid_request / invalid_client / ...）。
	Name string
	// State は state（空なら本文に含めない）。
	State       string
	Description string
	// Status は HTTP ステータス。
	Status int
	// RedirectURI はリダイレクト先（pre_auth の redirect_uri）。
	RedirectURI string
	// ResponseOnFragment はエラーをフラグメントで返すか。
	ResponseOnFragment bool
	// missingClientID は InvalidRequestResponse の missing_param == :client_id。
	missingClientID bool
}

// oauthErrorDescription は OAuth::Error#description（doorkeeper.errors.messages.<name>。無ければ server_error）。
func (a *App) oauthErrorDescription(lang, name string) string {
	vars := i18n.Vars{}
	if name == "invalid_code_challenge_method" {
		// Errors::InvalidCodeChallengeMethod.translate_options
		vars["challenge_methods"] = strings.Join(doorkeeper.PKCEMethods, ", ")
		vars["count"] = len(doorkeeper.PKCEMethods)
	}
	key := "doorkeeper.errors.messages." + name
	if v, ok := a.Bundle.Translate(lang, key, vars); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return a.Bundle.T(lang, "doorkeeper.errors.messages.server_error", nil)
}

// newOAuthError は ErrorResponse.new(name:, state:, redirect_uri:)。
func (a *App) newOAuthError(lang, name, state, redirectURI string) *oauthError {
	e := &oauthError{Name: name, State: state, RedirectURI: redirectURI, Description: a.oauthErrorDescription(lang, name), Status: http.StatusBadRequest}
	if name == "invalid_client" || name == "unauthorized_client" {
		e.Status = http.StatusUnauthorized
	}
	return e
}

// newOAuthInvalidRequest は InvalidRequestResponse（missing_param または reason）。
func (a *App) newOAuthInvalidRequest(lang, missingParam, reason, state, redirectURI string) *oauthError {
	r := reason
	vars := i18n.Vars{}
	if missingParam != "" {
		r = "missing_param"
		vars["value"] = missingParam
	}
	if r == "" {
		r = "unknown"
	}
	desc, ok := a.Bundle.Translate(lang, "doorkeeper.errors.messages.invalid_request."+r, vars)
	if !ok {
		desc = a.Bundle.T(lang, "doorkeeper.errors.messages.invalid_request.unknown", vars)
	}
	return &oauthError{Name: "invalid_request", State: state, RedirectURI: redirectURI, Description: i18n.RubyToS(desc),
		Status: http.StatusBadRequest, missingClientID: missingParam == "client_id"}
}

// newOAuthInvalidToken は InvalidTokenResponse（reason: revoked / expired / unknown。state は "unauthorized"）。
func (a *App) newOAuthInvalidToken(lang, reason string) *oauthError {
	return &oauthError{Name: "invalid_token", State: "unauthorized", Status: http.StatusUnauthorized,
		Description: a.Bundle.T(lang, "doorkeeper.errors.messages.invalid_token."+reason, nil)}
}

// Body は ErrorResponse#body（空の値は除く）。
func (e *oauthError) Body() []jsonKV {
	out := []jsonKV{{"error", e.Name}, {"error_description", e.Description}}
	if strings.TrimSpace(e.State) != "" {
		out = append(out, jsonKV{"state", e.State})
	}
	return out
}

// Redirectable は ErrorResponse#redirectable?。
func (e *oauthError) Redirectable() bool {
	switch e.Name {
	case "invalid_redirect_uri", "invalid_client", "unauthorized_client":
		return false
	}
	if doorkeeper.IsOOB(e.RedirectURI) {
		return false
	}
	return !e.missingClientID
}

// Location は ErrorResponse#redirect_uri。
func (e *oauthError) Location() string {
	params := []doorkeeper.Param{{Key: "error", Value: e.Name}, {Key: "error_description", Value: e.Description}, {Key: "state", Value: e.State}}
	if e.ResponseOnFragment {
		return doorkeeper.URIWithFragment(e.RedirectURI, params)
	}
	return doorkeeper.URIWithQuery(e.RedirectURI, params)
}

// AuthenticateInfo は WWW-Authenticate ヘッダの値。
func (e *oauthError) AuthenticateInfo(realm string) string {
	return `Bearer realm="` + realm + `", error="` + e.Name + `", error_description="` + e.Description + `"`
}

// setHeaders は ErrorResponse#headers（Cache-Control は Rails が no-store に正規化する）。
// realm が空でなければ WWW-Authenticate も付ける。
func (e *oauthError) setHeaders(w http.ResponseWriter, realm string) {
	w.Header().Set("Cache-Control", "no-store")
	if realm != "" {
		w.Header().Set("WWW-Authenticate", e.AuthenticateInfo(realm))
	}
}

// ---------------------------------------------------------------- JSON（render json:）

// jsonKV は順序付きの JSON オブジェクトの 1 項目。
type jsonKV struct {
	Key   string
	Value any
}

// jsonObject は順序を保つ JSON オブジェクト。
type jsonObject []jsonKV

// MarshalJSON は ActiveSupport の to_json と同じ形（キー順を保つ）で出力する。
func (o jsonObject) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, kv := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(kv.Key)
		b.Write(k)
		b.WriteByte(':')
		v, err := json.Marshal(kv.Value)
		if err != nil {
			return nil, err
		}
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// activeSupportEscape は ActiveSupport::JSON の追加エスケープ（U+2028 / U+2029。<>& は encoding/json と同じ）。
var activeSupportEscape = strings.NewReplacer(" ", ` `, " ", ` `)

// renderOAuthJSON は render json: body, status:（200 の応答には Rack::ETag と同じ弱い ETag を付ける）。
func renderOAuthJSON(w http.ResponseWriter, status int, body any) {
	data, err := json.Marshal(body)
	if err != nil {
		data = []byte("{}")
	}
	data = []byte(activeSupportEscape.Replace(string(data)))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if status == http.StatusOK && !strings.Contains(w.Header().Get("Cache-Control"), "no-cache") {
		sum := md5.Sum(data)
		w.Header().Set("ETag", `W/"`+hex.EncodeToString(sum[:])+`"`)
		if w.Header().Get("Cache-Control") == "" {
			w.Header().Set("Cache-Control", "max-age=0, private, must-revalidate")
		}
	} else if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "no-cache")
	}
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

// ---------------------------------------------------------------- アクセストークンの認証（OAuth::Token.authenticate）

var bearerRe = regexp.MustCompile(`(?i)^Bearer `)

// bearerTokenFromRequest は OAuth::Token.from_request（from_bearer_authorization → from_access_token_param →
// from_bearer_param の順）。
func bearerTokenFromRequest(r *http.Request, params interface {
	String(keys ...string) string
}) string {
	if h := r.Header.Get("Authorization"); bearerRe.MatchString(h) {
		if t := bearerRe.ReplaceAllString(h, ""); strings.TrimSpace(t) != "" {
			return t
		}
	}
	if t := params.String("access_token"); strings.TrimSpace(t) != "" {
		return t
	}
	if t := params.String("bearer_token"); strings.TrimSpace(t) != "" {
		return t
	}
	return ""
}

// authenticateOAuthToken は Doorkeeper.authenticate(request)（トークンが無ければ nil）。
// 見つかったトークンでは revoke_previous_refresh_token! を行う（リフレッシュで置き換えられた旧トークンを失効させる）。
func (a *App) authenticateOAuthToken(r *http.Request, params interface {
	String(keys ...string) string
}) *domain.OAuthAccessToken {
	plain := bearerTokenFromRequest(r, params)
	if plain == "" {
		return nil
	}
	ctx := r.Context()
	tok, err := repository.FindOAuthAccessTokenByToken(ctx, a.DB, doorkeeper.HashToken(plain))
	if err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			a.logger().Error("find oauth access token", "err", err)
		}
		return nil
	}
	a.revokePreviousRefreshToken(r, tok)
	return tok
}

// revokePreviousRefreshToken は AccessToken#revoke_previous_refresh_token!。
func (a *App) revokePreviousRefreshToken(r *http.Request, tok *domain.OAuthAccessToken) {
	if strings.TrimSpace(tok.PreviousRefreshToken) == "" {
		return
	}
	ctx := r.Context()
	if old, err := repository.FindOAuthAccessTokenByRefreshToken(ctx, a.DB, tok.PreviousRefreshToken); err == nil {
		if _, err := repository.RevokeOAuthAccessToken(ctx, a.DB, old.ID, a.now()); err != nil {
			a.logger().Error("revoke previous refresh token", "err", err)
		}
	}
	if err := repository.ClearOAuthPreviousRefreshToken(ctx, a.DB, tok.ID); err != nil {
		a.logger().Error("clear previous refresh token", "err", err)
	}
	tok.PreviousRefreshToken = ""
}

// oauthTokenError は doorkeeper_render_error で返すエラー（InvalidTokenResponse.from_access_token）。
func (a *App) oauthTokenError(lang string, tok *domain.OAuthAccessToken) *oauthError {
	reason := "unknown"
	switch {
	case tok != nil && tok.Revoked(a.now()):
		reason = "revoked"
	case tok != nil && tok.Expired(a.now()):
		reason = "expired"
	}
	return a.newOAuthInvalidToken(lang, reason)
}

// oauthCurrentUser は find_current_user の OAuth の分岐:
//
//	if access_token.accessible?
//	  user = User.active.find_by_id(access_token.resource_owner_id)
//	  user.oauth_scope = access_token.scopes.all.map(&:to_sym)
//	else
//	  doorkeeper_render_error
//	end
//
// doorkeeper_render_error（head 401 と WWW-Authenticate）を返したら false。
// Redmine では有効でないユーザーのトークンは NoMethodError（500）になるが、ここでは匿名として扱う。
func (a *App) oauthCurrentUser(c *Req, tok *domain.OAuthAccessToken, user **domain.User) bool {
	if !tok.Accessible(a.now()) {
		// user_setup は set_localization より前なので I18n.locale は既定（en）
		e := a.oauthTokenError("en", tok)
		e.setHeaders(c.W, a.Realm())
		httpx.HeadAs(c.W, c.R, http.StatusUnauthorized, "html")
		c.Halt()
		return false
	}
	if tok.ResourceOwnerID == nil {
		return true
	}
	u := a.findActiveUser(c, *tok.ResourceOwnerID)
	if u == nil {
		return true
	}
	scopes := tok.ScopeList()
	if scopes == nil {
		scopes = []string{}
	}
	u.OAuthScope = scopes
	*user = u
	return true
}
