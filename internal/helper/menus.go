package helper

import (
	"html/template"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/menu"
	"github.com/mikuta0407/buropher/internal/repository"
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

// menuProject は *domain.Project を menu.Project として渡すアダプタ。
type menuProject struct{ p *domain.Project }

func (m menuProject) Identifier() string { return m.p.Identifier }

func toMenuProject(p *domain.Project) menu.Project {
	if p == nil {
		return nil
	}
	return menuProject{p}
}

func fromMenuProject(p menu.Project) *domain.Project {
	if mp, ok := p.(menuProject); ok {
		return mp.p
	}
	return nil
}

// menuEnv は menu.Env の実装（User.current・Setting・i18n・権限を Page から引く）。
// 権限は internal/authz の Authorizer、表示条件のデータは internal/repository で判定する。
// DB エラーはログに残して「表示しない」側に倒す。
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

// check は (bool, error) の結果をログ付きで bool にする。
func (e menuEnv) check(what string, ok bool, err error) bool {
	if err != nil {
		e.p.logError(what, err)
		return false
	}
	return ok
}

func (e menuEnv) AllowedTo(permission string, p menu.Project) bool {
	return e.p.AllowedTo(domain.Perm(permission), fromMenuProject(p))
}
func (e menuEnv) AllowedToAction(controller, action string, p menu.Project) bool {
	return e.p.AllowedTo(domain.ControllerAction(controller, action), fromMenuProject(p))
}
func (e menuEnv) AllowedToGlobally(permission string) bool {
	a := e.p.authorizer()
	if a == nil {
		return e.p.admin()
	}
	ok, err := a.AllowedToGlobally(e.p.ctx(), domain.Perm(permission), nil)
	return e.check("allowed_to_globally", ok, err)
}
func (e menuEnv) ModuleEnabledInVisibleProject(module string) bool {
	a := e.p.authorizer()
	if a == nil {
		return false
	}
	ok, err := a.ModuleEnabledInVisibleProject(e.p.ctx(), module)
	return e.check("module_enabled_in_visible_project", ok, err)
}
func (e menuEnv) Setting(name string) string { return e.p.setting(name) }
func (e menuEnv) SpriteIcon(icon string, label template.HTML) template.HTML {
	return e.d.spriteIcon(e.p, icon, label, nil)
}
func (e menuEnv) CurrentMenuItem() string { return e.p.currentMenuItem() }

// ---- プロジェクトメニューの表示条件（lib/redmine/preparation.rb の :if） ----

func (e menuEnv) SharedVersionsAny(mp menu.Project) bool {
	p := fromMenuProject(mp)
	if p == nil || e.p.DB == nil {
		return false
	}
	ok, err := repository.ProjectHasSharedVersions(e.p.ctx(), e.p.DB, p)
	return e.check("shared_versions", ok, err)
}

func (e menuEnv) RolledUpVersionsAny(mp menu.Project) bool {
	p := fromMenuProject(mp)
	if p == nil || e.p.DB == nil {
		return false
	}
	ok, err := repository.ProjectHasRolledUpVersions(e.p.ctx(), e.p.DB, p)
	return e.check("rolled_up_versions", ok, err)
}

func (e menuEnv) AllowedTargetTrackersAny(mp menu.Project) bool {
	p := fromMenuProject(mp)
	a := e.p.authorizer()
	if p == nil || a == nil {
		return false
	}
	ids, err := a.AllowedTargetTrackerIDs(e.p.ctx(), p, 0)
	return e.check("allowed_target_trackers", len(ids) > 0, err)
}

func (e menuEnv) HasWiki(mp menu.Project) bool {
	p := fromMenuProject(mp)
	if p == nil || e.p.DB == nil {
		return false
	}
	ok, err := repository.ProjectHasWiki(e.p.ctx(), e.p.DB, p.ID)
	return e.check("wiki", ok, err)
}

func (e menuEnv) BoardsAny(mp menu.Project) bool {
	p := fromMenuProject(mp)
	if p == nil || e.p.DB == nil {
		return false
	}
	ok, err := repository.ProjectHasBoards(e.p.ctx(), e.p.DB, p.ID)
	return e.check("boards", ok, err)
}

func (e menuEnv) RepositoriesExist(mp menu.Project) bool {
	p := fromMenuProject(mp)
	if p == nil || e.p.DB == nil {
		return false
	}
	ok, err := repository.ProjectHasRepositories(e.p.ctx(), e.p.DB, p.ID)
	return e.check("repositories", ok, err)
}

// AllowedTo は User.current.allowed_to?(action, project)（ヘルパー・テンプレート用）。
// Authorizer が無い場合は管理者のみ許可する。
func (p *Page) AllowedTo(action domain.Action, project *domain.Project) bool {
	if project != nil && project.ID == 0 {
		// 未保存のプロジェクト（NewRecordProject のメニュー描画）
		return false
	}
	a := p.authorizer()
	if a == nil {
		return p.admin() && project != nil && project.AllowsTo(action)
	}
	ok, err := a.AllowedTo(p.ctx(), action, project)
	if err != nil {
		p.logError("allowed_to", err)
		return false
	}
	return ok
}

// currentMenu は MenuController#current_menu(project)。
func (p *Page) currentMenu(project *domain.Project) string {
	if project != nil {
		return "project_menu"
	}
	if p.MainMenu && !p.NoCurrentMenu {
		return "application_menu"
	}
	return ""
}

// displayMainMenu は display_main_menu?(project)。
func (p *Page) displayMainMenu(project *domain.Project) bool {
	name := p.currentMenu(project)
	return name != "" && menus[name].HasItems()
}

// renderMenu は render_menu(menu, project)。
func (d *Deps) renderMenu(p *Page, name string, project *domain.Project) template.HTML {
	m := menus[name]
	if m == nil {
		return ""
	}
	return m.Render(menuEnv{d, p}, toMenuProject(project))
}

// renderMainMenu は render_main_menu(project)。
func (d *Deps) renderMainMenu(p *Page, project *domain.Project) template.HTML {
	if name := p.currentMenu(project); name != "" {
		if project == nil && p.NewRecordProject {
			// @project が未保存（projects#new / create / copy）の場合、アプリケーションメニューの項目は
			// 未保存のプロジェクトに対して権限判定される（allowed_to? は常に false になる）
			return d.renderMenu(p, name, &domain.Project{})
		}
		return d.renderMenu(p, name, project)
	}
	return ""
}

// DisplayMainMenu は display_main_menu?(project)（body_css_classes 用）。
func (p *Page) DisplayMainMenu(project *domain.Project) bool { return p.displayMainMenu(project) }
