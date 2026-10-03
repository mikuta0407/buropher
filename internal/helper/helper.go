// Package helper は Redmine のビューヘルパー（ApplicationHelper / IconsHelper / AvatarsHelper /
// MenuManager::MenuHelper / Redmine::Themes::Helper など）の移植。
//
// リクエストに依存する状態（User.current、@project、コントローラ名、Setting など）は
// Page にまとめ、view.Context.Values[PageKey] に格納する。テンプレート関数は
// Deps.RequestFuncs で view.Engine に登録する。
//
// ユーザー・プロジェクトは internal/domain の型（*domain.User / *domain.Project）で受け取る。
package helper

import (
	"context"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/assets"
	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/brand"
	"github.com/mikuta0407/buropher/internal/clock"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/view"
)

// PageKey は view.Context.Values で Page を格納するキー。
const PageKey = "helper.page"

// AppName は Redmine::Info.app_name 相当（製品名 "Buropher"）。
const AppName = brand.Name

// AppURL は Redmine::Info.url 相当（buropher のソースコードの入手先）。
var AppURL = brand.SourceURL

// JumpProject はプロジェクトジャンプボックスの 1 項目。
// Lft / Rgt はネストセット相当の値（project_tree の is_descendant_of 判定と並び順に使う）。
type JumpProject struct {
	ID         int64
	Name       string
	Identifier string
	Lft, Rgt   int
}

// JumpBox は render_project_jump_box が使うプロジェクト一覧。
type JumpBox struct {
	// Projects は projects_for_jump_box（User#projects.active）。
	Projects []JumpProject
	// Bookmarked は Redmine::ProjectJumpBox#bookmarked_projects。
	Bookmarked []JumpProject
	// Recents は recently_used_projects（使用順）。
	Recents []JumpProject
}

// Page は 1 リクエスト分の描画状態（コントローラのインスタンス変数・User.current 等）。
//
// ユーザー・プロジェクトは internal/domain の型をそのまま使う。権限判定は Authz が返す
// internal/authz の Authorizer（リクエスト単位）で行い、追加のデータは DB から
// internal/repository の関数で読む（ヘルパー内で SQL を書かない）。
type Page struct {
	Request  *http.Request
	Settings *settings.Settings
	Loc      *i18n.Localizer
	// User は User.current（nil なら匿名として扱う）。
	User *domain.User
	// Pref は User.current.pref（nil なら既定値）。
	Pref *domain.UserPreference
	// Project は @project（nil 可）。
	Project *domain.Project
	// Controller / Action は controller_name / action_name。
	Controller, Action string
	// MainMenu は controller.class.main_menu。
	MainMenu bool
	// NoCurrentMenu は current_menu(project) が（@project が無いとき）nil を返す
	// （ImportsController#current_menu の admin レイアウト）。
	NoCurrentMenu bool
	// MenuItem は current_menu_item の上書き（空なら menu.CurrentMenuItem）。
	MenuItem string
	// DefaultSearchScope は controller.default_search_scope（空なら nil）。
	DefaultSearchScope string
	// NewRecordProject は @project が未保存のプロジェクト（projects#new / create / copy）。
	NewRecordProject bool
	// ProjectNameWas は @project.name_was（空なら Project.Name。ジャンプボックスの表示に使う）。
	ProjectNameWas string
	// Question は @question（検索語）。
	Question string
	// QuestionSet は @question が nil でない（空文字列でも value="" を出す）。
	QuestionSet bool
	// DB はヘルパーがデータを読むための接続（nil ならデータを要する部分は空になる）。
	DB db.Queryer
	// Authz は User.current の Authorizer を返す（nil なら管理者のみ許可する保守的な判定）。
	Authz func() *authz.Authorizer
	// Now は現在時刻（nil なら clock.Now。distance_of_time_in_words の基準）。
	Now func() time.Time
	// Logger はヘルパー内のエラーの記録先（nil なら slog.Default()）。
	Logger *slog.Logger

	// PreviewAttachments は @attachments（プレビューで thumbnail マクロが参照する未保存の添付）。
	PreviewAttachments []*redmine.Attachment
	// BaseURL は完全 URL（only_path: false）の基点の上書き（メールの描画で Mailer.default_url_options を使う）。
	// 空ならリクエストの base_url。
	BaseURL string

	accessKeys   []string
	wikiRenderer *redmine.Renderer
	theme        *assets.Theme
	themeSet     bool
	leaf         map[int64]bool
	// contextMenuIncluded は @context_menu_included（context_menu ヘルパー）。
	contextMenuIncluded bool
	// calendarHeadersIncluded は @calendar_headers_tags_included。
	calendarHeadersIncluded bool
}

// Params は Rails の params。
func (p *Page) Params() *httpx.Params {
	if p == nil || p.Request == nil {
		return httpx.NewParams()
	}
	return httpx.ParamsOf(p.Request)
}

// PageOf は描画中の Page を返す（未設定なら空の Page）。
func PageOf(r *view.Render) *Page {
	if r != nil && r.Ctx != nil && r.Ctx.Values != nil {
		if p, ok := r.Ctx.Values[PageKey].(*Page); ok {
			return p
		}
	}
	return &Page{}
}

// Deps はヘルパーが使うアプリケーション全体の依存。
type Deps struct {
	Assets *assets.Pipeline
	// Hooks はビューのフック（Redmine::Hook の call_hook。buropher 独自機能の差し込み口）。
	Hooks map[string][]HookFunc
}

// HookFunc は call_hook(name, args...) で呼ばれるビューのフック。
type HookFunc func(r *view.Render, p *Page, args ...any) template.HTML

// AddHook はビューのフックを登録する（起動時に呼ぶ。並行利用中の登録は想定しない）。
func (d *Deps) AddHook(name string, fn HookFunc) {
	if d.Hooks == nil {
		d.Hooks = map[string][]HookFunc{}
	}
	d.Hooks[name] = append(d.Hooks[name], fn)
}

// callHook は登録順にフックを呼んで出力をつなげる（Redmine の call_hook と同じく改行で区切る）。
func (d *Deps) callHook(r *view.Render, p *Page, name string, args ...any) template.HTML {
	var out []string
	for _, fn := range d.Hooks[name] {
		if h := fn(r, p, args...); h != "" {
			out = append(out, string(h))
		}
	}
	return template.HTML(strings.Join(out, "\n"))
}

// setting は Setting[name] の文字列値（Settings 未設定なら定義の既定値）。
func (p *Page) setting(name string) string {
	if p.Settings == nil {
		if d := settings.Lookup(name); d != nil {
			if s, ok := d.Default.(string); ok {
				return s
			}
		}
		return ""
	}
	return p.Settings.String(name)
}

// settingBool は Setting.xxx?（to_i > 0）。
func (p *Page) settingBool(name string) bool { return settings.RubyToI(p.setting(name)) > 0 }

// l は l(key, args...)。
func (p *Page) l(key string, args ...any) string {
	if p.Loc == nil {
		return key
	}
	return p.Loc.L(key, args...)
}

// logged は User.current.logged?。
func (p *Page) logged() bool { return p.User != nil && p.User.Logged() }

// admin は User.current.admin?。
func (p *Page) admin() bool { return p.User != nil && p.User.IsAdmin() }

// ctx はデータ読み込み用の context。
func (p *Page) ctx() context.Context {
	if p.Request != nil {
		return p.Request.Context()
	}
	return context.Background()
}

// now は現在時刻（Now 未設定なら clock.Now）。
func (p *Page) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return clock.Now()
}

// logError はヘルパー内のエラーを記録する（描画は続ける）。
func (p *Page) logError(what string, err error) {
	l := p.Logger
	if l == nil {
		l = slog.Default()
	}
	l.Error("helper: "+what, "err", err)
}

// authorizer は User.current の Authorizer（未設定なら nil）。
func (p *Page) authorizer() *authz.Authorizer {
	if p.Authz == nil || p.User == nil {
		return nil
	}
	return p.Authz()
}

// pref は User.current.pref。
func (p *Page) pref() *domain.UserPreference {
	if p.Pref != nil {
		return p.Pref
	}
	var id int64
	if p.User != nil {
		id = p.User.ID
	}
	return domain.DefaultUserPreference(id)
}

// userFormat は Setting.user_format（name(nil) が使う書式）。
func (p *Page) userFormat() string { return p.setting("user_format") }

// userName は User#name(format)（format が空なら Setting.user_format。匿名は label_user_anonymous）。
func (p *Page) userName(u *domain.User, format string) string {
	if u.Anonymous() {
		return p.l("label_user_anonymous")
	}
	if format == "" || !domain.ValidUserFormat(format) {
		format = p.userFormat()
	}
	return u.Name(format)
}

// projectLeaf は Project#leaf?（子プロジェクトがない）。
func (p *Page) projectLeaf(pr *domain.Project) bool {
	if v, ok := p.leaf[pr.ID]; ok {
		return v
	}
	if p.DB == nil {
		return true
	}
	v, err := repository.IsProjectLeaf(p.ctx(), p.DB, pr.ID)
	if err != nil {
		p.logError("project leaf", err)
		v = true
	}
	if p.leaf == nil {
		p.leaf = map[int64]bool{}
	}
	p.leaf[pr.ID] = v
	return v
}

// currentTheme は Redmine::Themes::Helper#current_theme。
func (d *Deps) currentTheme(p *Page) *assets.Theme {
	if !p.themeSet {
		p.themeSet = true
		if d.Assets != nil {
			if id := p.setting("ui_theme"); id != "" {
				p.theme = d.Assets.Theme(id)
			}
		}
	}
	return p.theme
}

// CurrentTheme は current_theme（ビュー Context の構築用）。
func (d *Deps) CurrentTheme(p *Page) *assets.Theme { return d.currentTheme(p) }

// NoMenuItem は Page.MenuItem に設定すると current_menu_item が nil になる値
// （AttachmentsController#current_menu_item でコンテナが無い場合など）。
const NoMenuItem = "-"

// currentMenuItem は current_menu_item。
func (p *Page) currentMenuItem() string {
	if p.MenuItem == NoMenuItem {
		return ""
	}
	if p.MenuItem != "" {
		return p.MenuItem
	}
	return menuCurrentItem(p.Controller, p.Action)
}

// html は template.HTML の短縮。
type html = template.HTML
