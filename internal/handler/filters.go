package handler

import (
	"errors"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

// このファイルは App.Handle に渡すアクション単位の設定（ActionOption）と、
// ApplicationController の再利用可能な before_action（find_project, authorize, require_admin ...）の移植。
//
//	// before_action :find_project, :authorize
//	a.Handle(r, http.MethodGet, "/projects/{id}/settings", ProjectsController, "settings", a.ProjectsSettings,
//		FindProject("id"), Authorize())
//
// ActionOption は宣言順に実行される（Redmine の before_action の宣言順と同じにすること）。

// Skip は skip_before_action（ApplicationController の before_action を省く）。
func Skip(filters ...Filter) ActionOption {
	return func(c *actionConfig) {
		for _, f := range filters {
			c.skip[f] = true
		}
	}
}

// Before は任意の before_action を追加する。fn が render / redirect（c.Halted()）したら以降を止める。
func Before(fn func(c *Req)) ActionOption {
	return func(cfg *actionConfig) { cfg.before = append(cfg.before, fn) }
}

// AcceptAPIAuth は accept_api_auth（このアクションで API キー・HTTP Basic 認証を受け付ける）。
func AcceptAPIAuth() ActionOption {
	return func(cfg *actionConfig) { cfg.acceptAPIAuth = true }
}

// AcceptAtomAuth は accept_atom_auth（GET の .atom で key パラメータによる認証を受け付ける）。
func AcceptAtomAuth() ActionOption {
	return func(cfg *actionConfig) { cfg.acceptAtomAuth = true }
}

// ---------------------------------------------------------------- before_action

// FindProject は find_project(params[param])（id か識別子。無ければ 404）。
// Redmine の find_project は FindProject("id")、find_project_by_project_id は FindProject("project_id")。
func FindProject(param string) ActionOption {
	return Before(func(c *Req) { c.FindProject(c.Params().String(param)) })
}

// FindProjectByProjectID は find_project_by_project_id。
func FindProjectByProjectID() ActionOption { return FindProject("project_id") }

// FindOptionalProjectByID は find_optional_project_by_id（params[:id] があれば find_project）。
func FindOptionalProjectByID() ActionOption {
	return Before(func(c *Req) {
		if c.Params().Present("id") {
			c.FindProject(c.Params().String("id"))
		}
	})
}

// FindOptionalProject は find_optional_project: params[:project_id] があれば @project を設定し、
// authorize_global する。プロジェクトが無ければログイン済みなら 404、匿名ならログインを要求する。
func FindOptionalProject() ActionOption {
	return Before(func(c *Req) {
		if c.Params().Present("project_id") {
			p, err := c.lookupProject(c.Params().String("project_id"))
			if err != nil {
				if !errors.Is(err, repository.ErrNotFound) {
					c.renderInternalError()
					c.halted = true
					return
				}
				if c.User.Logged() {
					c.Render404("")
				} else {
					c.App.requireLogin(c)
				}
				return
			}
			c.Project = p
		}
		c.Authorize(c.Controller.Name, c.Action, true)
	})
}

// Authorize は authorize（params[:controller] / params[:action] を @project または @projects で判定）。
func Authorize() ActionOption {
	return Before(func(c *Req) { c.Authorize(c.Controller.Name, c.Action, false) })
}

// AuthorizeGlobal は authorize_global（プロジェクト外のアクション。global: true で判定）。
func AuthorizeGlobal() ActionOption {
	return Before(func(c *Req) { c.Authorize(c.Controller.Name, c.Action, true) })
}

// RequireLogin は require_login。
func RequireLogin() ActionOption {
	return Before(func(c *Req) { c.App.requireLogin(c) })
}

// RequireAdmin は require_admin（未ログインならログインを要求、管理者でなければ 403）。
func RequireAdmin() ActionOption {
	return Before(func(c *Req) { c.RequireAdmin() })
}

// CheckProjectPrivacy は check_project_privacy（@project が無いかアーカイブ済みなら 404、
// 見えなければ deny_access）。
func CheckProjectPrivacy() ActionOption {
	return Before(func(c *Req) { c.CheckProjectPrivacy() })
}

// ---------------------------------------------------------------- Req のメソッド（アクション内からも使える）

// lookupProject は Project.find(param)（数字のみなら id、それ以外は識別子）。
func (c *Req) lookupProject(param string) (*domain.Project, error) {
	if param == "" {
		return nil, repository.ErrNotFound
	}
	p, err := repository.FindProject(c.Ctx(), c.App.DB, param)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		c.App.logger().Error("find project", "param", param, "err", err)
	}
	return p, err
}

// FindProject は ApplicationController#find_project: @project を設定する。無ければ 404 を描画して false。
func (c *Req) FindProject(param string) bool {
	p, err := c.lookupProject(param)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.Render404("")
		} else {
			c.renderInternalError()
			c.halted = true
		}
		return false
	}
	c.Project = p
	return true
}

// Authorize は ApplicationController#authorize(ctrl, action, global)。
// 許可されれば true。拒否時は、アーカイブ済みプロジェクトなら notice_not_authorized_archived_project の 403、
// プロジェクトのモジュールが無効なら 403、それ以外は deny_access（未ログインならログイン画面へ）。
func (c *Req) Authorize(ctrl, action string, global bool) bool {
	act := domain.ControllerAction(ctrl, action)
	var allowed bool
	var err error
	az := c.Authz()
	switch {
	case c.Project != nil:
		allowed, err = az.AllowedTo(c.Ctx(), act, c.Project)
	case c.Projects != nil:
		allowed, err = az.AllowedToProjects(c.Ctx(), act, c.Projects, nil)
	case global:
		allowed, err = az.AllowedToGlobally(c.Ctx(), act, nil)
	}
	if err != nil {
		c.App.logger().Error("authorize", "action", act.String(), "err", err)
		c.renderInternalError()
		c.halted = true
		return false
	}
	if allowed {
		return true
	}
	switch {
	case c.Project != nil && c.Project.Archived():
		c.ArchivedProject = c.Project
		c.Render403(c.L("notice_not_authorized_archived_project"))
	case c.Project != nil && !c.Project.AllowsTo(act):
		// プロジェクトのモジュールが無効
		c.Render403("")
	default:
		c.DenyAccess()
	}
	return false
}

// DenyAccess は ApplicationController#deny_access（ログイン済みなら 403、未ログインならログインを要求）。
func (c *Req) DenyAccess() {
	if c.User.Logged() {
		c.Render403("")
	} else {
		c.App.requireLogin(c)
	}
}

// RequireLogin は ApplicationController#require_login（ログイン済みなら true）。
func (c *Req) RequireLogin() bool { return c.App.requireLogin(c) }

// RequireAdmin は ApplicationController#require_admin。
func (c *Req) RequireAdmin() bool {
	if !c.App.requireLogin(c) {
		return false
	}
	if !c.User.IsAdmin() {
		c.Render403("")
		return false
	}
	return true
}

// CheckProjectPrivacy は ApplicationController#check_project_privacy。
func (c *Req) CheckProjectPrivacy() bool {
	if c.Project != nil && !c.Project.Archived() {
		if c.AllowedTo(domain.Perm("view_project"), c.Project) {
			return true
		}
		c.DenyAccess()
		return false
	}
	c.Project = nil
	c.Render404("")
	return false
}
