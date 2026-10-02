// Package helper は Redmine のビューヘルパー（ApplicationHelper / IconsHelper / AvatarsHelper /
// MenuManager::MenuHelper / Redmine::Themes::Helper など）の移植。
//
// リクエストに依存する状態（User.current、@project、コントローラ名、Setting など）は
// Page にまとめ、view.Context.Values[PageKey] に格納する。テンプレート関数は
// Deps.RequestFuncs で view.Engine に登録する。
//
// ドメインモデル（User / Project）は internal/domain の完成までインタフェースで受け取る。
package helper

import (
	"html/template"
	"net/http"

	"github.com/mikuta0407/buropher/internal/assets"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/view"
)

// PageKey は view.Context.Values で Page を格納するキー。
const PageKey = "helper.page"

// AppName は Redmine::Info.app_name。
const AppName = "Redmine"

// AppURL は Redmine::Info.url。
const AppURL = "https://www.redmine.org/"

// User はヘルパーが参照する User.current / 任意のユーザーの情報。
type User interface {
	ID() int64
	// Logged は User#logged?（匿名ユーザーなら false）。
	Logged() bool
	Admin() bool
	// Active は status == STATUS_ACTIVE。
	Active() bool
	Login() string
	// Mail は既定のメールアドレス（なければ空）。
	Mail() string
	// Name は User#name(formatter)。format が空なら Setting.user_format。
	Name(format string) string
	// Initials は User#initials。
	Initials() string
	// CSSClasses は User#css_classes（"user active" など）。
	CSSClasses() string
	// WarnOnLeavingUnsaved は pref.warn_on_leaving_unsaved != '0'。
	WarnOnLeavingUnsaved() bool
	// TextareaFont は pref.textarea_font（"monospace" / "proportional" / ""）。
	TextareaFont() string
	// AtomKey は User#atom_key（匿名なら空。ログインユーザーは必要なら作成する）。
	AtomKey() string
}

// Project はヘルパーが参照する @project の情報。
type Project interface {
	ID() int64
	Identifier() string
	Name() string
	// Archived は status == STATUS_ARCHIVED。
	Archived() bool
	// Leaf は子プロジェクトがない。
	Leaf() bool
}

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

// Authz は権限判定（internal/authz の完成までの差し替え点）。
type Authz interface {
	AllowedTo(permission string, p Project) bool
	AllowedToAction(controller, action string, p Project) bool
	AllowedToGlobally(permission string) bool
	ModuleEnabledInVisibleProject(module string) bool
}

// Page は 1 リクエスト分の描画状態（コントローラのインスタンス変数・User.current 等）。
type Page struct {
	Request  *http.Request
	Settings *settings.Settings
	Loc      *i18n.Localizer
	User     User
	Project  Project
	// Controller / Action は controller_name / action_name。
	Controller, Action string
	// MainMenu は controller.class.main_menu。
	MainMenu bool
	// MenuItem は current_menu_item の上書き（空なら menu.CurrentMenuItem）。
	MenuItem string
	// DefaultSearchScope は controller.default_search_scope（空なら nil）。
	DefaultSearchScope string
	// Question は @question（検索語）。
	Question string
	// JumpBox はジャンプボックスの一覧を返す（nil なら空）。
	JumpBox func() *JumpBox
	// Authz は権限判定（nil なら管理者のみ許可する保守的な判定）。
	Authz Authz

	accessKeys []string
	theme      *assets.Theme
	themeSet   bool
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
func (p *Page) admin() bool { return p.User != nil && p.User.Admin() }

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

// currentMenuItem は current_menu_item。
func (p *Page) currentMenuItem() string {
	if p.MenuItem != "" {
		return p.MenuItem
	}
	return menuCurrentItem(p.Controller, p.Action)
}

// html は template.HTML の短縮。
type html = template.HTML
