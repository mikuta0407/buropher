// Package handler は Redmine のコントローラ（app/controllers）の移植。
//
// ApplicationController の before_action（session_expiration, user_setup,
// check_if_login_required, set_localization, check_password_change, check_twofa_activation）は
// App.Handle でルートごとに適用する。各アクションは *Req（コントローラのインスタンス相当）を受け取る。
package handler

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/assets"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/view"
)

// App はアプリケーション全体の依存（コントローラ共通の状態）。
type App struct {
	DB       *db.DB
	Settings *settings.Settings
	Bundle   *i18n.Bundle
	Assets   *assets.Pipeline
	Views    *view.Engine
	Helpers  *helper.Deps
	Users    UserStore
	Errors   *httpx.ErrorRenderer
	Logger   *slog.Logger
	// Now は現在時刻（テスト用。nil なら time.Now）。
	Now func() time.Time
	// FormNameSuffix は form の name 属性の乱数部（テスト用。nil なら乱数）。
	FormNameSuffix func() string
	// AutologinCookieName は Redmine::Configuration['autologin_cookie_name']（空なら "autologin"）。
	AutologinCookieName string
	// AutologinCookiePath は autologin_cookie_path（空なら "/"）。
	AutologinCookiePath string
	// AutologinCookieSecure は autologin_cookie_secure（nil ならリクエストが HTTPS のとき）。
	AutologinCookieSecure *bool
}

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *App) logger() *slog.Logger {
	if a.Logger != nil {
		return a.Logger
	}
	return slog.Default()
}

func (a *App) autologinCookieName() string {
	if a.AutologinCookieName != "" {
		return a.AutologinCookieName
	}
	return "autologin"
}

func (a *App) autologinCookiePath() string {
	if a.AutologinCookiePath != "" {
		return a.AutologinCookiePath
	}
	return "/"
}

// Controller はコントローラのクラス属性（controller_name, main_menu, default_search_scope など）。
type Controller struct {
	Name string
	// MainMenu は self.main_menu（既定 true）。
	MainMenu bool
	// DefaultSearchScope は default_search_scope（空なら nil）。
	DefaultSearchScope string
	// MenuItem は menu_item の宣言をアクション名から決める関数（nil なら menu.CurrentMenuItem）。
	MenuItem func(action string) string
}

// Filter は before_action の種類（skip_before_action の指定に使う）。
type Filter int

const (
	FilterLoginRequired Filter = iota
	FilterPasswordChange
	FilterTwofaActivation
)

// ActionOption はアクション単位の設定。
type ActionOption func(*actionConfig)

type actionConfig struct {
	skip map[Filter]bool
}

// Skip は skip_before_action。
func Skip(filters ...Filter) ActionOption {
	return func(c *actionConfig) {
		for _, f := range filters {
			c.skip[f] = true
		}
	}
}

// Req は 1 リクエスト分のコントローラ状態（コントローラのインスタンス変数・User.current など）。
type Req struct {
	App        *App
	W          http.ResponseWriter
	R          *http.Request
	Controller *Controller
	Action     string

	// User は User.current。
	User *User
	// Loc は current_language と翻訳。
	Loc *i18n.Localizer
	// Project は @project（nil 可）。
	Project helper.Project
	// Question は @question。
	Question string

	halted bool
}

type ctxKey int

const (
	ctxReq ctxKey = iota
	ctxRoute
)

type routeInfo struct {
	ctrl   *Controller
	action string
}

// ReqOf は描画中のリクエスト状態を返す（App.Handle の外では nil）。
func ReqOf(r *http.Request) *Req {
	c, _ := r.Context().Value(ctxReq).(*Req)
	return c
}

// Ctx は context.Context。
func (c *Req) Ctx() context.Context { return c.R.Context() }

// Session はセッション（SessionManager 未設定なら nil）。
func (c *Req) Session() *httpx.Session { return httpx.SessionOf(c.R) }

// Flash は flash。
func (c *Req) Flash() *httpx.Flash { return httpx.FlashOf(c.R) }

// Params は params。
func (c *Req) Params() *httpx.Params { return httpx.ParamsOf(c.R) }

// L は l(key, args...)。
func (c *Req) L(key string, args ...any) string { return c.Loc.L(key, args...) }

// Halt は以降の処理（before_action・アクション）を止める（render / redirect 済みの印）。
func (c *Req) Halt() { c.halted = true }

// Halted は render / redirect 済みか。
func (c *Req) Halted() bool { return c.halted }

// Handle はルートを登録する。CSRF 検証（verify_authenticity_token）→ ApplicationController の
// before_action → アクションの順に実行する。
func (a *App) Handle(r chi.Router, method, pattern string, ctrl *Controller, action string, fn func(c *Req), opts ...ActionOption) {
	cfg := &actionConfig{skip: map[Filter]bool{}}
	for _, o := range opts {
		o(cfg)
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := a.newReq(w, r, ctrl, action)
		if a.runBeforeActions(c, cfg) {
			return
		}
		fn(c)
	})
	csrf := httpx.CSRFMiddleware(httpx.CSRFOptions{
		OnFailure: httpx.RedmineCSRFFailure(httpx.RedmineCSRFFailureOptions{
			Errors:              a.Errors,
			AutologinCookie:     a.autologinCookieName(),
			AutologinCookiePath: a.autologinCookiePath(),
			Message: func(r *http.Request) string {
				return a.anonymousReq(nil, r).L("error_invalid_authenticity_token")
			},
		}),
		Logger: a.Logger,
	})(inner)
	h := func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), ctxRoute, routeInfo{ctrl, action}))
		csrf.ServeHTTP(w, r)
	}
	httpx.Route(r, method, pattern, h)
}

func (a *App) newReq(w http.ResponseWriter, r *http.Request, ctrl *Controller, action string) *Req {
	c := &Req{App: a, W: w, Controller: ctrl, Action: action}
	r = r.WithContext(context.WithValue(r.Context(), ctxReq, c))
	c.R = r
	return c
}

// anonymousReq はアクション外（CSRF 失敗・ルーティング外のエラー）用に匿名ユーザーの Req を作る。
func (a *App) anonymousReq(w http.ResponseWriter, r *http.Request) *Req {
	if c := ReqOf(r); c != nil && c.Loc != nil {
		return c
	}
	ctrl, action := &Controller{Name: "application", MainMenu: true}, "index"
	if ri, ok := r.Context().Value(ctxRoute).(routeInfo); ok {
		ctrl, action = ri.ctrl, ri.action
	}
	c := a.newReq(w, r, ctrl, action)
	c.User = a.anonymous(r.Context())
	a.setLocalization(c, nil)
	return c
}

func (a *App) anonymous(ctx context.Context) *User {
	u, err := a.Users.Anonymous(ctx)
	if err != nil || u == nil {
		a.logger().Error("anonymous user", "err", err)
		return &User{Status: StatusAnonymous, Lastname: "Anonymous", anonymous: true, PrefWarnOnLeaving: true, settings: a.Settings}
	}
	return u
}

// runBeforeActions は ApplicationController の before_action を順に実行する。止まった場合 true。
func (a *App) runBeforeActions(c *Req, cfg *actionConfig) bool {
	if a.sessionExpiration(c); c.halted {
		return true
	}
	a.userSetup(c)
	if !cfg.skip[FilterLoginRequired] {
		if a.checkIfLoginRequired(c); c.halted {
			return true
		}
	}
	a.setLocalization(c, c.User)
	if !cfg.skip[FilterPasswordChange] {
		if a.checkPasswordChange(c); c.halted {
			return true
		}
	}
	if !cfg.skip[FilterTwofaActivation] {
		if a.checkTwofaActivation(c); c.halted {
			return true
		}
	}
	return false
}

// policy は Setting.session_lifetime / session_timeout。
func (a *App) policy() httpx.ExpiryPolicy {
	return httpx.PolicyFromMinutes(a.Settings.Int("session_lifetime"), a.Settings.Int("session_timeout"))
}

// SessionPolicy は SessionManager.Policy に渡す関数。
func (a *App) SessionPolicy() httpx.ExpiryPolicy { return a.policy() }

// sessionExpiration は ApplicationController#session_expiration。
func (a *App) sessionExpiration(c *Req) {
	s := c.Session()
	if s == nil {
		return
	}
	expired := (s.UserID() != 0 && s.Expired(a.policy(), a.now())) || s.Revoked()
	if !expired {
		return
	}
	if u := a.tryToAutologin(c); u != nil {
		return
	}
	var user *User
	if s.UserID() != 0 {
		user, _ = a.Users.FindActive(c.Ctx(), s.UserID())
	}
	if user == nil {
		user = a.anonymous(c.Ctx())
	}
	a.setLocalization(c, user)
	a.setLoggedUser(c, nil)
	c.Flash().SetError(c.L("error_session_expired"))
	a.requireLogin(c)
}

// userSetup は ApplicationController#user_setup / find_current_user。
// TODO(api): API キー（X-Redmine-API-Key / key パラメータ）・HTTP Basic・OAuth2・atom キー認証。
func (a *App) userSetup(c *Req) {
	var user *User
	if !httpx.IsAPIRequest(c.R) {
		if s := c.Session(); s != nil && s.UserID() != 0 {
			u, err := a.Users.FindActive(c.Ctx(), s.UserID())
			if err != nil {
				a.logger().Error("find current user", "err", err)
			}
			user = u
		} else if u := a.tryToAutologin(c); u != nil {
			user = u
		}
	}
	if user == nil {
		user = a.anonymous(c.Ctx())
	}
	c.User = user
}

// tryToAutologin は ApplicationController#try_to_autologin。
func (a *App) tryToAutologin(c *Req) *User {
	ck, err := c.R.Cookie(a.autologinCookieName())
	if err != nil || ck.Value == "" || !a.Settings.Bool("autologin") {
		return nil
	}
	u, err := a.Users.FindTokenUser(c.Ctx(), "autologin", ck.Value, a.Settings.Int("autologin"))
	if err != nil {
		a.logger().Error("autologin", "err", err)
		return nil
	}
	if u == nil {
		return nil
	}
	_ = a.Users.UpdateLastLogin(c.Ctx(), u.ID(), a.now())
	if s := c.Session(); s != nil {
		s.Reset()
		a.startUserSession(c, u)
	}
	c.User = u
	return u
}

// startUserSession は ApplicationController#start_user_session。
func (a *App) startUserSession(c *Req, u *User) {
	s := c.Session()
	s.SetUserID(u.ID())
	if u.MustChangePassword() {
		s.Set("pwd", "1")
	}
	if u.MustActivateTwofa() {
		s.Set("must_activate_twofa", "1")
	}
}

// setLoggedUser は ApplicationController#logged_user=。
func (a *App) setLoggedUser(c *Req, u *User) {
	if s := c.Session(); s != nil {
		s.Reset()
	}
	if u != nil {
		c.User = u
		if c.Session() != nil {
			a.startUserSession(c, u)
		}
	} else {
		c.User = a.anonymous(c.Ctx())
	}
}

// logoutUser は ApplicationController#logout_user。
func (a *App) logoutUser(c *Req) {
	if !c.User.Logged() {
		return
	}
	if ck, err := c.R.Cookie(a.autologinCookieName()); err == nil {
		a.deleteAutologinCookie(c)
		if ck.Value != "" {
			_ = a.Users.DeleteToken(c.Ctx(), c.User.ID(), "autologin", ck.Value)
		}
	}
	a.setLoggedUser(c, nil)
}

func (a *App) deleteAutologinCookie(c *Req) {
	http.SetCookie(c.W, &http.Cookie{Name: a.autologinCookieName(), Value: "", Path: a.autologinCookiePath(), MaxAge: -1, Expires: time.Unix(0, 0)})
}

// checkIfLoginRequired は ApplicationController#check_if_login_required。
func (a *App) checkIfLoginRequired(c *Req) {
	if c.User.Logged() {
		return
	}
	if a.Settings.Bool("login_required") {
		a.requireLogin(c)
	}
}

// checkPasswordChange は ApplicationController#check_password_change。
func (a *App) checkPasswordChange(c *Req) {
	s := c.Session()
	if s == nil || !s.Has("pwd") {
		return
	}
	if c.User.MustChangePassword() {
		c.Flash().SetError(c.L("error_password_expired"))
		c.Redirect("/my/password")
		return
	}
	s.Delete("pwd")
}

// checkTwofaActivation は ApplicationController#check_twofa_activation。
// TODO(twofa): 2FA 実装後に init_twofa_pairing_and_send_code_for を移植する（現在は画面へリダイレクトのみ）。
func (a *App) checkTwofaActivation(c *Req) {
	s := c.Session()
	if s == nil || !s.Has("must_activate_twofa") {
		return
	}
	if c.User.MustActivateTwofa() {
		c.Flash().SetWarning(c.L("twofa_warning_require"))
		// 利用可能な方式は totp のみ
		c.Redirect("/my/twofa/totp/activate/confirm")
		return
	}
	s.Delete("must_activate_twofa")
}

// setLocalization は ApplicationController#set_localization。
func (a *App) setLocalization(c *Req, user *User) {
	lang := ""
	if user != nil && user.Logged() {
		lang = a.Bundle.FindLanguage(user.EffectiveLanguage())
	}
	if lang == "" && !a.Settings.Bool("force_default_language_for_anonymous") {
		if al := c.R.Header.Get("Accept-Language"); al != "" {
			if q := parseQvalues(al); len(q) > 0 && q[0] != "" {
				accept := strings.ToLower(q[0])
				lang = a.Bundle.FindLanguage(accept)
				if lang == "" {
					lang = a.Bundle.FindLanguage(strings.SplitN(accept, "-", 2)[0])
				}
			}
		}
	}
	if lang == "" {
		lang = a.Settings.String("default_language")
	}
	is := i18n.Settings{
		DateFormat:      a.Settings.String("date_format"),
		TimeFormat:      a.Settings.String("time_format"),
		TimespanFormat:  a.Settings.String("timespan_format"),
		StartOfWeek:     a.Settings.String("start_of_week"),
		DefaultLanguage: a.Settings.String("default_language"),
	}
	var loc *time.Location
	if user != nil && user.PrefTimeZone != "" {
		loc = i18n.UserLocation(user.PrefTimeZone)
	}
	// set_language_if_valid: 無効なら現在の言語（既定 en）のまま
	cur := "en"
	if c.Loc != nil {
		cur = c.Loc.Lang
	}
	l := a.Bundle.NewLocalizer(cur, is, loc)
	l.SetLanguageIfValid(lang)
	c.Loc = l
}

var qvalueRe = regexp.MustCompile(`^([^\s,]+?)(?:;\s*q=(\d+(?:\.\d+)?))?$`)
var qsplitRe = regexp.MustCompile(`,\s*`)

// parseQvalues は ApplicationController#parse_qvalues（q 値の降順）。
func parseQvalues(value string) []string {
	type item struct {
		val string
		q   float64
	}
	var tmp []item
	for _, part := range qsplitRe.Split(value, -1) {
		m := qvalueRe.FindStringSubmatch(part)
		if m == nil {
			continue
		}
		q := 1.0
		if m[2] != "" {
			q, _ = strconv.ParseFloat(m[2], 64)
		}
		tmp = append(tmp, item{m[1], q})
	}
	sort.SliceStable(tmp, func(i, j int) bool { return tmp[i].q > tmp[j].q })
	out := make([]string, len(tmp))
	for i, t := range tmp {
		out[i] = t.val
	}
	return out
}

// OriginalURL は request.original_url。
func OriginalURL(r *http.Request) string { return httpx.RequestBaseURL(r) + r.URL.RequestURI() }

// requireLogin は ApplicationController#require_login。
func (a *App) requireLogin(c *Req) bool {
	if c.User != nil && c.User.Logged() {
		return true
	}
	var back string
	if c.R.Method == http.MethodGet || c.R.Method == http.MethodHead {
		back = OriginalURL(c.R)
	} else {
		// TODO: url_for(controller:, action:, id:, project_id:) の正確な再現
		back = httpx.RequestBaseURL(c.R) + c.R.URL.Path
	}
	signin := "/login?back_url=" + url.QueryEscape(back)
	switch format := httpx.Format(c.R); {
	case format == "html" || format == "":
		if httpx.IsXHR(c.R) {
			httpx.Head(c.W, c.R, http.StatusUnauthorized)
		} else {
			c.Redirect(signin)
		}
	case format == "atom" || format == "pdf" || format == "csv":
		c.Redirect(signin)
	case format == "xml" || format == "json":
		if a.Settings.Bool("rest_api_enabled") {
			c.W.Header().Set("WWW-Authenticate", `Basic realm="Redmine API"`)
			httpx.Head(c.W, c.R, http.StatusUnauthorized)
		} else {
			httpx.Head(c.W, c.R, http.StatusForbidden)
		}
	case format == "js":
		c.W.Header().Set("WWW-Authenticate", `Basic realm="Redmine API"`)
		httpx.Head(c.W, c.R, http.StatusUnauthorized)
	default:
		httpx.Head(c.W, c.R, http.StatusUnauthorized)
	}
	c.halted = true
	return false
}

// Redirect は redirect_to。
func (c *Req) Redirect(location string, status ...int) {
	httpx.Redirect(c.W, c.R, location, status...)
	c.halted = true
}

// RedirectBackOrDefault は redirect_back_or_default(default, referer:)。
func (c *Req) RedirectBackOrDefault(def string, referer bool) {
	httpx.RedirectBackOrDefault(c.W, c.R, def, httpx.BackURLOptions{Referer: referer})
	c.halted = true
}

// NoStore は no_store（Cache-Control: no-store）。
func (c *Req) NoStore() { c.W.Header().Set("Cache-Control", "no-store") }

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
