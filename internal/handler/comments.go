package handler

import (
	"errors"
	"net/http"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
)

// CommentsController（app/controllers/comments_controller.rb）。
//
//	default_search_scope :news
//	model_object News
//	before_action :find_model_object, :find_project_from_association, :authorize
var CommentsController = &Controller{Name: "comments", MainMenu: true, DefaultSearchScope: "news"}

// routesComments は comments コントローラのルートを登録する。
//
//	match '/news/:id/comments', :to => 'comments#create', :via => :post
//	match '/news/:id/comments/:comment_id', :to => 'comments#destroy', :via => :delete
func (a *App) routesComments(r Router) {
	find := Before(a.findNews)
	a.Handle(r, http.MethodPost, "/news/{id}/comments", CommentsController, "create", a.CommentsCreate, find, Authorize())
	a.Handle(r, http.MethodDelete, "/news/{id}/comments/{comment_id}", CommentsController, "destroy", a.CommentsDestroy, find, Authorize())
}

// CommentsCreate は comments#create。
func (a *App) CommentsCreate(c *Req) {
	n := c.news()
	// raise Unauthorized unless @news.commentable?
	if !c.AllowedTo(domain.Perm("comment_news"), c.Project) {
		c.DenyAccess()
		return
	}
	content := ""
	if p := c.Params().Map("comment"); p != nil {
		content = p.String("comments")
	}
	// validates_presence_of :content
	if !httpx.IsBlank(content) {
		now := a.now()
		cm := &domain.Comment{NewsID: n.ID, AuthorID: c.User.ID, Content: content, CreatedAt: now, UpdatedAt: now}
		if err := repository.InsertComment(c.Ctx(), a.DB, cm); err != nil {
			a.internalError(c, "create comment", err)
			return
		}
		cm.Author = c.User
		c.Flash().SetNotice(c.L("label_comment_added"))
		a.notify(c, "news_comment_added", "news_comment_added", cm)
	}
	c.Redirect("/news/" + itoa(n.ID))
}

// CommentsDestroy は comments#destroy。
func (a *App) CommentsDestroy(c *Req) {
	n := c.news()
	id, ok := paramInt64(c, "comment_id")
	if !ok {
		c.Render404("")
		return
	}
	if err := repository.DeleteComment(c.Ctx(), a.DB, n.ID, id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.Render404("")
			return
		}
		a.internalError(c, "destroy comment", err)
		return
	}
	c.Redirect("/news/" + itoa(n.ID))
}
