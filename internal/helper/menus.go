package helper

import (
	"html/template"

	"github.com/mikuta0407/buropher/internal/menu"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// メニュー定義（lib/redmine/preparation.rb）。起動時に一度だけ構築する。
var menus = map[string]*menu.Menu{
	"top_menu":         menu.TopMenu(),
	"account_menu":     menu.AccountMenu(),
	"application_menu": menu.ApplicationMenu(),
	"admin_menu":       menu.AdminMenu(),
	"project_menu":     menu.ProjectMenu(),
}

func menuCurrentItem(controller, action string) string {
	return menu.CurrentMenuItem(controller, action)
}

// menuProject は helper.Project を menu.Project として渡すアダプタ。
type menuProject struct{ Project }

func toMenuProject(p Project) menu.Project {
	if p == nil {
		return nil
	}
	return menuProject{p}
}

func fromMenuProject(p menu.Project) Project {
	if mp, ok := p.(menuProject); ok {
		return mp.Project
	}
	return nil
}

// menuEnv は menu.Env の実装（User.current・Setting・i18n を Page から引く）。
type menuEnv struct {
	d *Deps
	p *Page
}

var _ menu.Env = menuEnv{}

func (e menuEnv) L(key string) string { return e.p.l(key) }
func (e menuEnv) LOrHumanize(name, prefix string) string {
	if e.p.Loc == nil {
		return rails.Humanize(name)
	}
	return e.p.Loc.LOrHumanize(name, prefix)
}
func (e menuEnv) LoggedIn() bool { return e.p.logged() }
func (e menuEnv) Admin() bool    { return e.p.admin() }
func (e menuEnv) authz() Authz {
	if e.p.Authz != nil {
		return e.p.Authz
	}
	return adminOnlyAuthz{admin: e.p.admin()}
}
func (e menuEnv) AllowedTo(permission string, p menu.Project) bool {
	return e.authz().AllowedTo(permission, fromMenuProject(p))
}
func (e menuEnv) AllowedToAction(controller, action string, p menu.Project) bool {
	return e.authz().AllowedToAction(controller, action, fromMenuProject(p))
}
func (e menuEnv) AllowedToGlobally(permission string) bool {
	return e.authz().AllowedToGlobally(permission)
}
func (e menuEnv) ModuleEnabledInVisibleProject(module string) bool {
	return e.authz().ModuleEnabledInVisibleProject(module)
}
func (e menuEnv) Setting(name string) string { return e.p.setting(name) }
func (e menuEnv) SpriteIcon(icon string, label template.HTML) template.HTML {
	return e.d.spriteIcon(e.p, icon, label, nil)
}
func (e menuEnv) CurrentMenuItem() string { return e.p.currentMenuItem() }

// TODO(authz): プロジェクトメニューの表示条件はプロジェクト関連リポジトリの完成後に実装する。
func (e menuEnv) SharedVersionsAny(menu.Project) bool        { return false }
func (e menuEnv) RolledUpVersionsAny(menu.Project) bool      { return false }
func (e menuEnv) AllowedTargetTrackersAny(menu.Project) bool { return false }
func (e menuEnv) HasWiki(menu.Project) bool                  { return false }
func (e menuEnv) BoardsAny(menu.Project) bool                { return false }
func (e menuEnv) RepositoriesExist(menu.Project) bool        { return false }

// adminOnlyAuthz は Authz 未設定時の判定（管理者は常に許可、それ以外は拒否）。
// TODO(authz): internal/authz の User#allowed_to? に置き換える。
type adminOnlyAuthz struct{ admin bool }

func (a adminOnlyAuthz) AllowedTo(string, Project) bool               { return a.admin }
func (a adminOnlyAuthz) AllowedToAction(string, string, Project) bool { return a.admin }
func (a adminOnlyAuthz) AllowedToGlobally(string) bool                { return a.admin }
func (a adminOnlyAuthz) ModuleEnabledInVisibleProject(string) bool    { return false }

// currentMenu は MenuController#current_menu(project)。
func (p *Page) currentMenu(project Project) string {
	if project != nil {
		return "project_menu"
	}
	if p.MainMenu {
		return "application_menu"
	}
	return ""
}

// displayMainMenu は display_main_menu?(project)。
func (p *Page) displayMainMenu(project Project) bool {
	name := p.currentMenu(project)
	return name != "" && menus[name].HasItems()
}

// renderMenu は render_menu(menu, project)。
func (d *Deps) renderMenu(p *Page, name string, project Project) template.HTML {
	m := menus[name]
	if m == nil {
		return ""
	}
	return m.Render(menuEnv{d, p}, toMenuProject(project))
}

// renderMainMenu は render_main_menu(project)。
func (d *Deps) renderMainMenu(p *Page, project Project) template.HTML {
	if name := p.currentMenu(project); name != "" {
		return d.renderMenu(p, name, project)
	}
	return ""
}

// DisplayMainMenu は display_main_menu?(project)（body_css_classes 用）。
func (p *Page) DisplayMainMenu(project Project) bool { return p.displayMainMenu(project) }
