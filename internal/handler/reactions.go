package handler

import (
	"errors"
	"net/http"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/view"
)

// ReactionsController（app/controllers/reactions_controller.rb）。
//
//	before_action :require_login
//	before_action :check_enabled
//	before_action :set_object, :authorize_reactable
var ReactionsController = &Controller{Name: "reactions", MainMenu: true}

// routesReactions は reactions コントローラのルートを登録する（resources :reactions, only: [:create, :destroy]）。
func (a *App) routesReactions(r Router) {
	rc := ReactionsController
	opts := []ActionOption{RequireLogin(), Before(a.reactionsCheckEnabled), Before(a.reactionsSetObject)}
	a.Handle(r, http.MethodPost, "/reactions", rc, "create", a.ReactionsCreate, opts...)
	a.Handle(r, http.MethodDelete, "/reactions/{id}", rc, "destroy", a.ReactionsDestroy, opts...)
}

// reactableKinds は Redmine::Reaction::REACTABLE_TYPES（クラス名 → reactions.reactable_kind）。
var reactableKinds = map[string]string{
	"Journal": "journal", "Issue": "issue", "Message": "message", "News": "news", "Comment": "comment",
}

type reactionCtxKey struct{}

// reactionTarget は @object（種類・id・プロジェクト）。
type reactionTarget struct {
	Kind    string
	ID      int64
	Project *domain.Project
}

func (a *App) reactionsCheckEnabled(c *Req) {
	if !a.Settings.Bool("reactions_enabled") {
		c.Render403("")
	}
}

// reactionsSetObject は set_object と authorize_reactable（Redmine::Reaction.editable?）。
func (a *App) reactionsSetObject(c *Req) {
	kind, ok := reactableKinds[c.Params().String("object_type")]
	if !ok {
		c.Render403("")
		return
	}
	id, _ := paramInt64(c, "object_id")
	t, err := repository.FindReactable(c.Ctx(), a.DB, kind, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// object_type.constantize.find は処理されない RecordNotFound（public/404.html）
			renderPublic404(c)
			return
		}
		a.internalError(c, "find reactable", err)
		return
	}
	p, err := repository.GetProject(c.Ctx(), a.DB, t.ProjectID)
	if err != nil {
		a.internalError(c, "reactable project", err)
		return
	}
	if !a.reactableVisible(c, kind, t, p) || !p.Active() {
		c.Render403("")
		return
	}
	c.setValue(reactionCtxKey{}, &reactionTarget{Kind: kind, ID: id, Project: p})
}

// reactableVisible は object.visible?(User.current)。
func (a *App) reactableVisible(c *Req, kind string, t *repository.ReactableTarget, p *domain.Project) bool {
	switch kind {
	case "issue", "journal":
		is, err := redmine.NewDBStore(c.Ctx(), a.DB, c.Authz()).VisibleIssue(t.IssueID)
		if err != nil || is == nil {
			return false
		}
		if kind == "journal" && t.PrivateNotes {
			return c.AllowedTo(domain.Perm("view_private_notes"), p)
		}
		return true
	case "news", "comment":
		return c.AllowedTo(domain.Perm("view_news"), p)
	case "message":
		return c.AllowedTo(domain.Perm("view_messages"), p)
	}
	return false
}

func (c *Req) reactionTarget() *reactionTarget {
	t, _ := c.value(reactionCtxKey{}).(*reactionTarget)
	return t
}

// ReactionsCreate は reactions#create（JS のみ。他の形式は 404）。
func (a *App) ReactionsCreate(c *Req) {
	if httpx.Negotiate(c.R, "js") != "js" {
		httpx.Head(c.W, c.R, http.StatusNotFound)
		c.Halt()
		return
	}
	t := c.reactionTarget()
	if err := repository.FindOrCreateReaction(c.Ctx(), a.DB, t.Kind, t.ID, c.User.ID, a.now()); err != nil {
		a.internalError(c, "create reaction", err)
		return
	}
	a.renderReactionButton(c, t)
}

// ReactionsDestroy は reactions#destroy（JS のみ。他の形式は 404）。
func (a *App) ReactionsDestroy(c *Req) {
	if httpx.Negotiate(c.R, "js") != "js" {
		httpx.Head(c.W, c.R, http.StatusNotFound)
		c.Halt()
		return
	}
	t := c.reactionTarget()
	rid, _ := paramInt64(c, "id")
	if err := repository.DeleteUserReaction(c.Ctx(), a.DB, t.Kind, t.ID, c.User.ID, rid); err != nil {
		a.internalError(c, "destroy reaction", err)
		return
	}
	a.renderReactionButton(c, t)
}

// renderReactionButton は reactions/_replace_button.js。
func (a *App) renderReactionButton(c *Req, t *reactionTarget) {
	c.Render("reactions/replace_button", map[string]any{"Kind": t.Kind, "ID": t.ID, "Project": t.Project},
		RenderOptions{Format: "js", Layout: view.NoLayout})
}
