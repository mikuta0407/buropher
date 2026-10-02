package handler

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/auth/doorkeeper"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/permission"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/validation"
)

// Oauth2ApplicationsController（app/controllers/oauth2_applications_controller.rb。
// Doorkeeper::ApplicationsController を継承し、layout "admin"・main_menu = false）。
//
// before_action :authenticate_admin!（Redmine の admin_authenticator: REST API が無効か管理者でなければ deny_access）、
// require_sudo_mode :create, :show, :update, :destroy。
// メニュー項目（:applications）は current_menu_item（controller_name = oauth2_applications）と一致しないため選択表示されない。
var Oauth2ApplicationsController = &Controller{Name: "oauth2_applications", MainMenu: false}

// routesOAuth2Applications は use_doorkeeper の applications（controllers applications: 'oauth2_applications'）。
//
//	resources :doorkeeper_applications, controller: ..., as: :applications, path: "applications"
func (a *App) routesOAuth2Applications(r Router) {
	admin := Before(a.authenticateOAuthAdmin)
	find := Before(a.setOAuthApplication)
	ctrl := Oauth2ApplicationsController
	a.Handle(r, http.MethodGet, "/oauth/applications", ctrl, "index", a.OAuth2ApplicationsIndex, admin)
	a.Handle(r, http.MethodPost, "/oauth/applications", ctrl, "create", a.OAuth2ApplicationsCreate, admin, RequireSudoMode())
	a.Handle(r, http.MethodGet, "/oauth/applications/new", ctrl, "new", a.OAuth2ApplicationsNew, admin)
	a.Handle(r, http.MethodGet, "/oauth/applications/{id}/edit", ctrl, "edit", a.OAuth2ApplicationsEdit, admin, find)
	a.Handle(r, http.MethodGet, "/oauth/applications/{id}", ctrl, "show", a.OAuth2ApplicationsShow, admin, find, RequireSudoMode())
	a.Handle(r, http.MethodPatch, "/oauth/applications/{id}", ctrl, "update", a.OAuth2ApplicationsUpdate, admin, find, RequireSudoMode())
	a.Handle(r, http.MethodPut, "/oauth/applications/{id}", ctrl, "update", a.OAuth2ApplicationsUpdate, admin, find, RequireSudoMode())
	a.Handle(r, http.MethodDelete, "/oauth/applications/{id}", ctrl, "destroy", a.OAuth2ApplicationsDestroy, admin, find, RequireSudoMode())
}

// authenticateOAuthAdmin は admin_authenticator:
//
//	if !Setting.rest_api_enabled? || !User.current.admin? then deny_access end
func (a *App) authenticateOAuthAdmin(c *Req) {
	if !a.Settings.Bool("rest_api_enabled") || !c.User.IsAdmin() {
		c.DenyAccess()
	}
}

const ctxOAuthApplication = "oauth_application"

// setOAuthApplication は before_action :set_application（Application.find(params[:id])）。
// ActiveRecord::RecordNotFound は rescue されないため public/404.html になる。
func (a *App) setOAuthApplication(c *Req) {
	id, ok := c.Params().IntStrict("id")
	if !ok {
		renderPublic404(c)
		return
	}
	app, err := repository.GetOAuthApplication(c.Ctx(), a.DB, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			renderPublic404(c)
		} else {
			a.internalError(c, "find oauth application", err)
		}
		return
	}
	c.setLocal(ctxOAuthApplication, app)
}

func (c *Req) oauthApplication() *domain.OAuthApplication {
	app, _ := c.local(ctxOAuthApplication).(*domain.OAuthApplication)
	return app
}

// ---------------------------------------------------------------- フォームモデル

// oauthAppForm は編集中の Doorkeeper::Application（@application）。
type oauthAppForm struct {
	App    *domain.OAuthApplication
	errors *validation.Errors
	loc    *i18n.Localizer
	seen   map[string]bool
	// nameAssigned は name が代入されたか。
	nameAssigned bool
}

func newOAuthAppForm(c *Req, app *domain.OAuthApplication) *oauthAppForm {
	return &oauthAppForm{App: app, errors: validation.New("doorkeeper/application"), loc: c.Loc}
}

// ParamKey は model_name.param_key。
func (f *oauthAppForm) ParamKey() string { return "doorkeeper_application" }
func (f *oauthAppForm) Persisted() bool  { return f.App.ID != 0 }
func (f *oauthAppForm) ToParam() string  { return strconv.FormatInt(f.App.ID, 10) }

// Send は属性値（name / redirect_uri）。
func (f *oauthAppForm) Send(method string) (any, bool) {
	switch method {
	case "name":
		if f.App.ID == 0 && !f.nameAssigned {
			// 新規作成時の name は nil（value 属性を出さない）
			return nil, true
		}
		return f.App.Name, true
	case "redirect_uri":
		return f.App.RedirectURI, true
	}
	return nil, false
}

// ErrorsOn は errors[attr]。
func (f *oauthAppForm) ErrorsOn(attr string) []string { return f.errors.Messages(f.loc, attr) }

// FullErrorMessages は errors.full_messages。
func (f *oauthAppForm) FullErrorMessages() []string { return f.errors.FullMessages(f.loc) }

// HumanAttributeName は Doorkeeper::Application.human_attribute_name。
func (f *oauthAppForm) HumanAttributeName(attr string) string {
	return validation.HumanAttributeName(f.loc, "doorkeeper/application", attr)
}

// HasScope は @application.scopes.include?(scope)。
func (f *oauthAppForm) HasScope(scope string) bool { return slices.Contains(f.App.ScopeList(), scope) }

// addError は errors.add(attr, key)（activerecord.errors.models.doorkeeper/application.attributes.<attr>.<key> を優先）。
// 同じ属性・キーのエラーは 1 つにまとめる（Redmine の表示と同じ）。
func (f *oauthAppForm) addError(attr, key string) {
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	if f.seen[attr+"."+key] {
		return
	}
	f.seen[attr+"."+key] = true
	for _, k := range []string{
		"activerecord.errors.models.doorkeeper/application.attributes." + attr + "." + key,
		"activerecord.errors.models.doorkeeper/application." + key,
	} {
		if f.loc.Bundle.Exists(f.loc.Lang, k) {
			f.errors.AddMessage(attr, f.loc.L(k))
			return
		}
	}
	f.errors.Add(attr, key)
}

// oauthApplicationParams は Oauth2ApplicationsController#application_params
// （公開権限のスコープを常に含め、スペース区切りの文字列にする）。
// 戻り値は name / redirect_uri / scopes / confidential の各値と、パラメータにあったか。
type oauthAppParams struct {
	name, redirectURI *string
	scopes            string
	confidential      *bool
}

func oauthApplicationParams(c *Req) oauthAppParams {
	p := c.Params().Map("doorkeeper_application")
	var out oauthAppParams
	if p == nil {
		p = httpx.NewParams()
	}
	if s, ok := p.StringOK("name"); ok {
		out.name = &s
	}
	if s, ok := p.StringOK("redirect_uri"); ok {
		out.redirectURI = &s
	}
	scopes := doorkeeper.DefaultScopes()
	if v, ok := p.Get("scopes"); ok {
		switch x := v.(type) {
		case []any:
			for _, e := range x {
				if s, ok := e.(string); ok && !slices.Contains(scopes, s) {
					scopes = append(scopes, s)
				}
			}
		case string:
			for _, s := range strings.Fields(x) {
				if !slices.Contains(scopes, s) {
					scopes = append(scopes, s)
				}
			}
		}
	}
	// Scopes#scopes=（from_string で正規化）
	out.scopes = strings.Join(doorkeeper.ParseScopes(strings.Join(scopes, " ")), " ")
	if s, ok := p.StringOK("confidential"); ok {
		b := castBool(s)
		out.confidential = &b
	}
	return out
}

// assign は application_params の代入（変更があれば true）。
func (f *oauthAppForm) assign(ps oauthAppParams) bool {
	app := f.App
	changed := false
	if ps.name != nil {
		f.nameAssigned = true
	}
	if ps.name != nil && *ps.name != app.Name {
		app.Name, changed = *ps.name, true
	}
	if ps.redirectURI != nil && *ps.redirectURI != app.RedirectURI {
		app.RedirectURI, changed = *ps.redirectURI, true
	}
	if ps.scopes != app.Scopes {
		app.Scopes, changed = ps.scopes, true
	}
	if ps.confidential != nil && *ps.confidential != app.Confidential {
		app.Confidential, changed = *ps.confidential, true
	}
	return changed
}

// validate は Doorkeeper::Application の検証。
func (a *App) validateOAuthApp(c *Req, f *oauthAppForm) error {
	app := f.App
	if blankStr(app.Name) {
		f.addError("name", "blank")
	}
	if blankStr(app.UID) {
		f.addError("uid", "blank")
	} else if taken, err := repository.OAuthApplicationUIDTaken(c.Ctx(), a.DB, app.UID, app.ID); err != nil {
		return err
	} else if taken {
		f.addError("uid", "taken")
	}
	for _, k := range doorkeeper.ValidateRedirectURI(app.RedirectURI) {
		f.addError("redirect_uri", k)
	}
	// enforce_configured_scopes
	if !blankStr(app.Scopes) && !doorkeeper.ScopeValid(app.Scopes, doorkeeper.ServerScopes(), nil) {
		f.addError("scopes", "not_match_configured")
	}
	return nil
}

// ---------------------------------------------------------------- 画面のデータ

// oauthScopeGroup はスコープ選択の fieldset（perms_by_module）。
type oauthScopeGroup struct {
	Label       string
	Permissions []*permission.Permission
}

// oauthScopeGroups は Redmine::AccessControl.permissions.group_by(&:project_module) をキー順に並べたもの。
func oauthScopeGroups(c *Req) []oauthScopeGroup {
	var out []oauthScopeGroup
	for _, g := range groupPermissions(c, permission.All()) {
		out = append(out, oauthScopeGroup{Label: g.Label, Permissions: g.Permissions})
	}
	return out
}

// oauthScopeLabels は scopes.map { |scope| l_or_humanize(scope, prefix: 'permission_') }.join(", ")。
func oauthScopeLabels(c *Req, scopes []string) string {
	labels := make([]string, len(scopes))
	for i, s := range scopes {
		labels[i] = c.Loc.LOrHumanize(s, "permission_")
	}
	return strings.Join(labels, ", ")
}

// oauthAppRow は index の行。
type oauthAppRow struct {
	*domain.OAuthApplication
	ScopeLabels string
}

// CallbackURLs は redirect_uri.split.join(', ')。
func (r oauthAppRow) CallbackURLs() string { return strings.Join(r.RedirectURIs(), ", ") }

// OAuth2ApplicationsIndex は index（GET /oauth/applications）。
func (a *App) OAuth2ApplicationsIndex(c *Req) {
	apps, err := repository.OAuthApplications(c.Ctx(), a.DB)
	if err != nil {
		a.internalError(c, "list oauth applications", err)
		return
	}
	rows := make([]oauthAppRow, len(apps))
	for i, app := range apps {
		rows[i] = oauthAppRow{OAuthApplication: app, ScopeLabels: oauthScopeLabels(c, app.ScopeList())}
	}
	c.renderAdmin("doorkeeper/applications/index", map[string]any{"Applications": rows, "T": doorkeeperTFunc(c)}, false)
}

func (a *App) renderOAuthAppForm(c *Req, name string, f *oauthAppForm) {
	c.renderAdmin("doorkeeper/applications/"+name, map[string]any{"Application": f, "ScopeGroups": oauthScopeGroups(c)}, false)
}

// OAuth2ApplicationsNew は new（GET /oauth/applications/new）。
func (a *App) OAuth2ApplicationsNew(c *Req) {
	a.renderOAuthAppForm(c, "new", newOAuthAppForm(c, &domain.OAuthApplication{Confidential: true}))
}

// OAuth2ApplicationsCreate は create（POST /oauth/applications）。
func (a *App) OAuth2ApplicationsCreate(c *Req) {
	f := newOAuthAppForm(c, &domain.OAuthApplication{Confidential: true})
	f.assign(oauthApplicationParams(c))
	// before_validation :generate_uid, :generate_secret, on: :create
	f.App.UID = doorkeeper.GenerateToken()
	plainSecret := doorkeeper.GenerateToken()
	if err := a.validateOAuthApp(c, f); err != nil {
		a.internalError(c, "validate oauth application", err)
		return
	}
	if f.errors.Any() {
		a.renderOAuthAppForm(c, "new", f)
		return
	}
	hashed, err := doorkeeper.HashSecret(plainSecret)
	if err != nil {
		a.internalError(c, "hash oauth secret", err)
		return
	}
	f.App.Secret = hashed
	if err := repository.CreateOAuthApplication(c.Ctx(), a.DB, f.App, a.now()); err != nil {
		a.internalError(c, "create oauth application", err)
		return
	}
	c.Flash().SetNotice(c.L("doorkeeper.flash.applications.create.notice"))
	c.Flash().Set("application_secret", plainSecret)
	c.Redirect("/oauth/applications/" + strconv.FormatInt(f.App.ID, 10))
}

// OAuth2ApplicationsShow は show（GET /oauth/applications/:id）。
// 作成直後だけ flash[:application_secret] の平文のシークレットを表示する（保存されているのは BCrypt のハッシュ）。
func (a *App) OAuth2ApplicationsShow(c *Req) {
	app := c.oauthApplication()
	secret := c.Flash().Get("application_secret")
	c.Flash().Delete("application_secret")
	type uriRow struct {
		URI, AuthorizeURL string
	}
	var uris []uriRow
	for _, u := range app.RedirectURIs() {
		q := "client_id=" + urlQueryEscape(app.UID) + "&redirect_uri=" + urlQueryEscape(u) + "&response_type=code&scope=" +
			urlQueryEscape(strings.Join(app.ScopeList(), " "))
		uris = append(uris, uriRow{URI: u, AuthorizeURL: "/oauth/authorize?" + q})
	}
	c.renderAdmin("doorkeeper/applications/show", map[string]any{"App": app, "Secret": secret, "T": doorkeeperTFunc(c),
		"ScopeLabels": oauthScopeLabels(c, app.ScopeList()), "RedirectURIs": uris}, false)
}

// OAuth2ApplicationsEdit は edit（GET /oauth/applications/:id/edit）。
func (a *App) OAuth2ApplicationsEdit(c *Req) {
	a.renderOAuthAppForm(c, "edit", newOAuthAppForm(c, c.oauthApplication()))
}

// OAuth2ApplicationsUpdate は update（PATCH/PUT /oauth/applications/:id）。
func (a *App) OAuth2ApplicationsUpdate(c *Req) {
	f := newOAuthAppForm(c, c.oauthApplication())
	changed := f.assign(oauthApplicationParams(c))
	if err := a.validateOAuthApp(c, f); err != nil {
		a.internalError(c, "validate oauth application", err)
		return
	}
	if f.errors.Any() {
		a.renderOAuthAppForm(c, "edit", f)
		return
	}
	if err := repository.UpdateOAuthApplication(c.Ctx(), a.DB, f.App, changed, a.now()); err != nil {
		a.internalError(c, "update oauth application", err)
		return
	}
	c.Flash().SetNotice(c.L("doorkeeper.flash.applications.update.notice"))
	c.Redirect("/oauth/applications/" + strconv.FormatInt(f.App.ID, 10))
}

// OAuth2ApplicationsDestroy は destroy（DELETE /oauth/applications/:id）。
func (a *App) OAuth2ApplicationsDestroy(c *Req) {
	app := c.oauthApplication()
	err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error { return repository.DeleteOAuthApplication(c.Ctx(), tx, app.ID) })
	if err != nil {
		a.internalError(c, "destroy oauth application", err)
		return
	}
	c.Flash().SetNotice(c.L("doorkeeper.flash.applications.destroy.notice"))
	c.Redirect("/oauth/applications")
}

// urlQueryEscape は Hash#to_query の値のエスケープ（CGI.escape）。
func urlQueryEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9', ch == '_', ch == '.', ch == '-', ch == '~':
			b.WriteByte(ch)
		case ch == ' ':
			b.WriteByte('+')
		default:
			b.WriteByte('%')
			b.WriteByte("0123456789ABCDEF"[ch>>4])
			b.WriteByte("0123456789ABCDEF"[ch&15])
		}
	}
	return b.String()
}
