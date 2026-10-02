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
	"github.com/mikuta0407/buropher/internal/attachments"
	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/clock"
	"github.com/mikuta0407/buropher/internal/crypto/secretbox"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/repository"
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
	Errors   *httpx.ErrorRenderer
	Logger   *slog.Logger
	// Now は現在時刻（テスト用。nil なら clock.Now）。
	Now func() time.Time
	// FormNameSuffix は form の name 属性の乱数部（テスト用。nil なら乱数）。
	FormNameSuffix func() string
	// AutologinCookieName は Redmine::Configuration['autologin_cookie_name']（空なら "autologin"）。
	AutologinCookieName string
	// AutologinCookiePath は autologin_cookie_path（空なら "/"）。
	AutologinCookiePath string
	// AutologinCookieSecure は autologin_cookie_secure（nil ならリクエストが HTTPS のとき）。
	AutologinCookieSecure *bool
	// AttachmentStore は添付ファイルの保存先（Attachment.storage_path。doc.go の「規約: 添付ファイル」）。
	AttachmentStore *attachments.Store
	// Notifier はチケットの通知（issue_add / issue_edit）の配送先（nil ならログ出力のみ。issues_env.go）。
	Notifier issues.Notifier
	// Secrets は DB に保存する秘密値（LDAP の account_password 等）の暗号化器（server.secret_key 由来）。
	Secrets *secretbox.Box
	// Version は buropher のバージョン（admin/info に表示する。空なら "dev"）。
	Version string
}

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return clock.Now()
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

// Filter は ApplicationController の before_action の種類（skip_before_action の指定に使う）。
type Filter int

const (
	FilterLoginRequired Filter = iota
	FilterPasswordChange
	FilterTwofaActivation
)

// ActionOption はアクション単位の設定（skip_before_action・コントローラの before_action・
// accept_api_auth 等）。filters.go の Skip / FindProject / Authorize / RequireAdmin ... を参照。
type ActionOption func(*actionConfig)

type actionConfig struct {
	skip map[Filter]bool
	// before はコントローラの before_action（宣言順に ApplicationController の before_action の後で実行）。
	before []func(c *Req)
	// acceptAPIAuth は accept_api_auth（API キー・HTTP Basic による認証を受け付ける）。
	acceptAPIAuth bool
	// acceptAtomAuth は accept_atom_auth（GET の .atom で key パラメータによる認証を受け付ける）。
	acceptAtomAuth bool
}

// Req は 1 リクエスト分のコントローラ状態（コントローラのインスタンス変数・User.current など）。
type Req struct {
	App        *App
	W          http.ResponseWriter
	R          *http.Request
	Controller *Controller
	Action     string

	// User は User.current（常に非 nil。匿名なら AnonymousUser）。変更は SetUser で行う。
	User *domain.User
	// Loc は current_language と翻訳。
	Loc *i18n.Localizer
	// Project は @project（nil 可）。
	Project *domain.Project
	// Projects は @projects（一括操作などで複数プロジェクトを対象にする場合。authorize が参照する）。
	Projects []*domain.Project
	// ArchivedProject は @archived_project（アーカイブ済みプロジェクトの 403 画面で使う）。
	ArchivedProject *domain.Project
	// Question は @question。
	Question string
	// NewRecordProject は @project が未保存のプロジェクト（メニューの判定に使う）。
	NewRecordProject bool
	// NewProjectName / NewProjectIdentifier は未保存の @project の name / identifier
	// （html_title と body_css_classes に使う）。
	NewProjectName, NewProjectIdentifier string
	// ProjectNameWas は @project.name_was（保存に失敗した場合のジャンプボックスの表示名）。
	ProjectNameWas string
	// QuestionSet は @question が nil でない（空文字列でも検索欄に value="" を出す）。
	QuestionSet bool
	// Attachments は @attachments（find_attachments が読み込む未紐付けの添付。プレビューで使う）。
	Attachments []*repository.RefAttachment

	cfg    *actionConfig
	authz  *authz.Authorizer
	pref   *domain.UserPreference
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
// before_action（session_expiration, user_setup, check_if_login_required, set_localization,
// check_password_change, check_twofa_activation）→ opts で宣言したコントローラの before_action
// （FindProject, Authorize ...）→ アクション → after_action（record_project_usage）の順に実行する。
// before_action が render / redirect した（Halted）場合はアクションと after_action を実行しない。
func (a *App) Handle(r chi.Router, method, pattern string, ctrl *Controller, action string, fn func(c *Req), opts ...ActionOption) {
	cfg := &actionConfig{skip: map[Filter]bool{}}
	for _, o := range opts {
		o(cfg)
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := a.newReq(w, r, ctrl, action)
		c.cfg = cfg
		if a.runBeforeActions(c, cfg) {
			return
		}
		fn(c)
		a.recordProjectUsage(c)
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
	c := &Req{App: a, W: w, Controller: ctrl, Action: action, cfg: &actionConfig{skip: map[Filter]bool{}}}
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
	c.SetUser(a.anonymous(r.Context()))
	a.setLocalization(c, nil)
	return c
}

// anonymous は User.anonymous（組込の匿名ユーザー。DB に無ければ作成する）。
func (a *App) anonymous(ctx context.Context) *domain.User {
	u, err := repository.AnonymousUser(ctx, a.DB)
	if err != nil || u == nil {
		a.logger().Error("anonymous user", "err", err)
		return &domain.User{Principal: domain.Principal{Kind: domain.KindAnonymousUser, Status: domain.StatusAnonymous, Lastname: "Anonymous"}}
	}
	return u
}

// runBeforeActions は ApplicationController の before_action とコントローラの before_action を
// 順に実行する。止まった場合 true。
func (a *App) runBeforeActions(c *Req, cfg *actionConfig) bool {
	if a.sessionExpiration(c); c.halted {
		return true
	}
	if a.userSetup(c); c.halted {
		return true
	}
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
	for _, f := range cfg.before {
		if f(c); c.halted {
			return true
		}
	}
	return false
}

// setLocalization は ApplicationController#set_localization。
func (a *App) setLocalization(c *Req, user *domain.User) {
	lang := ""
	if user != nil && user.Logged() {
		lang = a.Bundle.FindLanguage(a.userLanguage(user))
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
	if user != nil && user.Logged() {
		if tz := a.preference(c, user).TimeZone; tz != "" {
			loc = i18n.UserLocation(tz)
		}
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
		if a.Settings.Bool("rest_api_enabled") && c.cfg.acceptAPIAuth {
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
