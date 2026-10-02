package handler

import (
	"net/http"

	"github.com/mikuta0407/buropher/internal/activity"
	"github.com/mikuta0407/buropher/internal/apibuilder"
	"github.com/mikuta0407/buropher/internal/attachments"
	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/pagination"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view"
)

// NewsController（app/controllers/news_controller.rb）。
//
//	default_search_scope :news
//	model_object News
//	before_action :find_model_object, :except => [:new, :create, :index]
//	before_action :find_project_from_association, :except => [:new, :create, :index]
//	before_action :find_project_by_project_id, :only => :create
//	before_action :authorize, :except => [:index, :new]
//	before_action :find_optional_project, :only => [:index, :new]
//	accept_atom_auth :index
//	accept_api_auth :index, :show, :create, :update, :destroy
var NewsController = &Controller{Name: "news", MainMenu: true, DefaultSearchScope: "news"}

// routesNews は news コントローラのルートを登録する。
//
//	resources :projects do resources :news, :except => [:show, :edit, :update, :destroy] end
//	resources :news, :only => [:index, :show, :edit, :update, :destroy, :create, :new]
func (a *App) routesNews(r Router) {
	n := NewsController
	find := Before(a.findNews)
	a.Handle(r, http.MethodGet, "/projects/{project_id}/news", n, "index", a.NewsIndex, FindOptionalProject(), AcceptAtomAuth(), AcceptAPIAuth())
	a.Handle(r, http.MethodPost, "/projects/{project_id}/news", n, "create", a.NewsCreate, FindProjectByProjectID(), Authorize(), AcceptAPIAuth())
	a.Handle(r, http.MethodGet, "/projects/{project_id}/news/new", n, "new", a.NewsNew, FindOptionalProject())
	a.Handle(r, http.MethodGet, "/news", n, "index", a.NewsIndex, FindOptionalProject(), AcceptAtomAuth(), AcceptAPIAuth())
	a.Handle(r, http.MethodPost, "/news", n, "create", a.NewsCreate, FindProjectByProjectID(), Authorize(), AcceptAPIAuth())
	a.Handle(r, http.MethodGet, "/news/new", n, "new", a.NewsNew, FindOptionalProject())
	a.Handle(r, http.MethodGet, "/news/{id}/edit", n, "edit", a.NewsEdit, find, Authorize())
	a.Handle(r, http.MethodGet, "/news/{id}", n, "show", a.NewsShow, find, Authorize(), AcceptAPIAuth())
	a.Handle(r, http.MethodPatch, "/news/{id}", n, "update", a.NewsUpdate, find, Authorize(), AcceptAPIAuth())
	a.Handle(r, http.MethodPut, "/news/{id}", n, "update", a.NewsUpdate, find, Authorize(), AcceptAPIAuth())
	a.Handle(r, http.MethodDelete, "/news/{id}", n, "destroy", a.NewsDestroy, find, Authorize(), AcceptAPIAuth())
}

type newsCtxKey struct{}

// newsForm は @news（labelled_form_for のモデル）。
type newsForm struct {
	contentForm
	*domain.News
}

func (c *Req) news() *newsForm {
	f, _ := c.value(newsCtxKey{}).(*newsForm)
	return f
}

// findNews は find_model_object（News.find(params[:id])）と find_project_from_association。
func (a *App) findNews(c *Req) {
	id, ok := paramInt64(c, "id")
	if !ok {
		c.Render404("")
		return
	}
	n, err := repository.GetNews(c.Ctx(), a.DB, id)
	if err != nil {
		a.notFoundOr500(c, "find news", err)
		return
	}
	c.Project = n.Project
	c.setValue(newsCtxKey{}, &newsForm{contentForm: newContentForm(c, "news", n.ID), News: n})
}

// newsVisibleCondition は News.visible の条件。
func (a *App) newsVisibleCondition(c *Req) (string, error) {
	return c.Authz().AllowedToCondition(c.Ctx(), "view_news", authz.ConditionOptions{}, nil)
}

// NewsIndex は news#index。
func (a *App) NewsIndex(c *Req) {
	format := httpx.Negotiate(c.R, "html", "xml", "json", "atom")
	if format == "" {
		respondNotAcceptable(c)
		return
	}
	api := format == "xml" || format == "json"
	var offset, limit int
	if api {
		offset, limit = c.APIOffsetAndLimit()
	} else {
		limit = 10
	}
	cond, err := a.newsVisibleCondition(c)
	if err != nil {
		a.internalError(c, "news visible", err)
		return
	}
	var pid int64
	if c.Project != nil {
		pid = c.Project.ID
	}
	count, err := repository.CountNews(c.Ctx(), a.DB, cond, pid)
	if err != nil {
		a.internalError(c, "count news", err)
		return
	}
	pages := pagination.New(count, limit, c.Params().String("page"))
	if !api {
		offset = pages.Offset()
	}
	newss, err := repository.ListNews(c.Ctx(), a.DB, cond, pid, limit, offset)
	if err != nil {
		a.internalError(c, "list news", err)
		return
	}
	switch format {
	case "xml", "json":
		c.RenderAPI(0, func(b apibuilder.Builder) {
			b.Array("news", c.APIMeta(apibuilder.A("total_count", count, "offset", offset, "limit", limit)), func() {
				for _, n := range newss {
					b.Object("news", func() {
						b.Value("id", n.ID)
						if n.Project != nil {
							b.Attrs("project", apibuilder.A("id", n.ProjectID, "name", n.Project.Name))
						}
						if n.Author != nil {
							b.Attrs("author", apibuilder.A("id", n.AuthorID, "name", c.Page().UserName(n.Author)))
						}
						b.Value("title", n.Title)
						b.Value("summary", nilIfEmptyString(n.Summary))
						b.Value("description", nilIfEmptyString(n.Description))
						b.Value("created_on", n.CreatedAt)
					})
				}
			})
		})
	case "atom":
		title := a.Settings.String("app_title")
		if c.Project != nil {
			title = c.Project.Name
		}
		items := make([]*activity.Event, len(newss))
		for i, n := range newss {
			items[i] = newsEvent(n)
		}
		a.renderFeed(c, items, title+": "+c.L("label_news_plural"))
	default:
		data := map[string]any{
			"Newss":       newss,
			"Pages":       pages,
			"AtomKey":     c.AtomKey(),
			"News":        a.newNewsForm(c),
			"CanManage":   a.canManageNews(c),
			"ModuleID":    a.newsModuleID(c),
			"FormProject": a.newsFormProjects(c),
		}
		opts := RenderOptions{}
		if httpx.IsXHR(c.R) {
			opts.Layout = view.NoLayout
		}
		c.Render("news/index", data, opts)
	}
}

// canManageNews は User.current.allowed_to?(:manage_news, @project, global: true)。
func (a *App) canManageNews(c *Req) bool {
	if c.Project != nil {
		return c.AllowedTo(domain.Perm("manage_news"), c.Project)
	}
	return c.AllowedToGlobally(domain.Perm("manage_news"))
}

// newsModuleID は @project.enabled_module('news') の id（ウォッチのリンク用。無ければ 0）。
func (a *App) newsModuleID(c *Req) int64 {
	if c.Project == nil || !c.User.Logged() {
		return 0
	}
	id, err := repository.ProjectModuleID(c.Ctx(), a.DB, c.Project.ID, "news")
	if err != nil {
		return 0
	}
	return id
}

// newsFormProjects は _form のプロジェクト選択（@project が nil のときの Project.allowed_to(:manage_news)）。
func (a *App) newsFormProjects(c *Req) []*domain.Project {
	if c.Project != nil {
		return nil
	}
	cond, err := c.Authz().AllowedToCondition(c.Ctx(), "manage_news", authz.ConditionOptions{}, nil)
	if err != nil {
		a.logger().Error("manage_news projects", "err", err)
		return nil
	}
	ps, err := repository.LoadProjects(c.Ctx(), a.DB, cond)
	if err != nil {
		a.logger().Error("manage_news projects", "err", err)
		return nil
	}
	if err := repository.SortProjectsByTree(c.Ctx(), a.DB, ps); err != nil {
		a.logger().Error("sort projects", "err", err)
	}
	return ps
}

// newNewsForm は News.new(:project => @project, :author => User.current)。
func (a *App) newNewsForm(c *Req) *newsForm {
	n := &domain.News{Project: c.Project, Author: c.User, AuthorID: c.User.ID}
	if c.Project != nil {
		n.ProjectID = c.Project.ID
	}
	return &newsForm{contentForm: newContentForm(c, "news", 0), News: n}
}

// assignNews は news.safe_attributes = params[:news]（title / summary / description）。
func assignNews(c *Req, n *domain.News) {
	p := c.Params().Map("news")
	if p == nil {
		return
	}
	if v, ok := p.StringOK("title"); ok {
		n.Title = v
	}
	if v, ok := p.StringOK("summary"); ok {
		n.Summary = v
	}
	if v, ok := p.StringOK("description"); ok {
		n.Description = v
	}
}

// validateNews は News の検証（title・description 必須、title 60 文字・summary 255 文字以内）。
func validateNews(f *newsForm, res *attachments.SaveResult) {
	e := f.errs
	if httpx.IsBlank(f.Title) {
		e.Add("title", "blank")
	}
	if httpx.IsBlank(f.Description) {
		e.Add("description", "blank")
	}
	if len([]rune(f.Title)) > 60 {
		e.Add("title", "too_long", "count", 60)
	}
	if len([]rune(f.Summary)) > 255 {
		e.Add("summary", "too_long", "count", 255)
	}
	if msg := res.FailedMessage(f.loc); msg != "" {
		e.AddMessage("base", msg)
	}
}

// newsAttachmentParams は params[:attachments] || (params[:news] && params[:news][:uploads])。
func newsAttachmentParams(c *Req) any {
	if v := paramAttachments(c); v != nil {
		return v
	}
	if p := c.Params().Map("news"); p != nil {
		if v, ok := p.Get("uploads"); ok {
			return v
		}
	}
	return nil
}

// NewsNew は news#new。
func (a *App) NewsNew(c *Req) {
	if !a.canManageNews(c) {
		c.DenyAccess()
		return
	}
	a.renderNewsForm(c, "news/new", a.newNewsForm(c), nil)
}

func (a *App) renderNewsForm(c *Req, tmpl string, f *newsForm, res *attachments.SaveResult) {
	data := map[string]any{"News": f, "FormProject": a.newsFormProjects(c)}
	if res != nil {
		data["SavedAttachments"] = res.Files
	}
	c.Render(tmpl, data)
}

// NewsCreate は news#create。
func (a *App) NewsCreate(c *Req) {
	f := a.newNewsForm(c)
	assignNews(c, f.News)
	api := httpx.IsAPIRequest(c.R)
	res, err := a.saveContainerAttachments(c, newsAttachmentParams(c))
	if err != nil {
		a.internalError(c, "save attachments", err)
		return
	}
	validateNews(f, res)
	if f.errs.Any() {
		if api {
			c.RenderValidationErrors(f.errs)
			return
		}
		a.renderNewsForm(c, "news/new", f, res)
		return
	}
	n := f.News
	n.CreatedAt = a.now()
	err = a.withTx(c, func(tx *db.Tx) error {
		if err := repository.InsertNews(c.Ctx(), tx, n); err != nil {
			return err
		}
		if err := a.AttachmentStore.AttachSaved(c.Ctx(), tx, res, domain.AttachmentContainerNews, n.ID); err != nil {
			return err
		}
		// after_create :add_author_as_watcher
		return repository.AddWatcher(c.Ctx(), tx, "news", n.ID, n.AuthorID)
	})
	if err != nil {
		a.internalError(c, "create news", err)
		return
	}
	a.notify(c, "news_added", "news_added", n)
	if api {
		c.RenderAPIOK()
		return
	}
	c.AttachFilesWarning(res)
	c.Flash().SetNotice(c.L("notice_successful_create"))
	if c.Params().Present("cross_project") {
		c.Redirect("/news")
		return
	}
	c.Redirect("/projects/" + c.Project.Identifier + "/news")
}

// NewsShow は news#show。
func (a *App) NewsShow(c *Req) {
	f := c.news()
	comments, err := repository.NewsComments(c.Ctx(), a.DB, f.ID)
	if err != nil {
		a.internalError(c, "news comments", err)
		return
	}
	// User.current.wants_comments_in_reverse_order?
	if c.Pref().CommentsSorting == "desc" {
		for i, j := 0, len(comments)-1; i < j; i, j = i+1, j-1 {
			comments[i], comments[j] = comments[j], comments[i]
		}
	}
	format := httpx.Negotiate(c.R, "html", "xml", "json")
	switch format {
	case "xml", "json":
		var atts []*domain.Attachment
		if c.IncludeInAPIResponse("attachments") {
			if atts, err = repository.ContainerAttachmentList(c.Ctx(), a.DB, domain.AttachmentContainerNews, f.ID); err != nil {
				a.internalError(c, "news attachments", err)
				return
			}
		}
		n := f.News
		c.RenderAPI(0, func(b apibuilder.Builder) {
			b.Object("news", func() {
				b.Value("id", n.ID)
				if n.Project != nil {
					b.Attrs("project", apibuilder.A("id", n.ProjectID, "name", n.Project.Name))
				}
				if n.Author != nil {
					b.Attrs("author", apibuilder.A("id", n.AuthorID, "name", c.Page().UserName(n.Author)))
				}
				b.Value("title", n.Title)
				if !httpx.IsBlank(n.Summary) {
					b.Value("summary", n.Summary)
				}
				b.Value("description", nilIfEmptyString(n.Description))
				b.Value("created_on", n.CreatedAt)
				if c.IncludeInAPIResponse("attachments") {
					b.Array("attachments", nil, func() {
						for _, att := range atts {
							renderAPIAttachment(c, b, att)
						}
					})
				}
				if c.IncludeInAPIResponse("comments") {
					b.Array("comments", nil, func() {
						for _, cm := range comments {
							b.ObjectAttrs("comment", apibuilder.A("id", cm.ID), func() {
								if cm.Author != nil {
									b.Attrs("author", apibuilder.A("id", cm.AuthorID, "name", c.Page().UserName(cm.Author)))
								}
								b.Value("content", nilIfEmptyString(cm.Content))
							})
						}
					})
				}
			})
		})
	case "html":
		atts, err := repository.ContainerAttachmentList(c.Ctx(), a.DB, domain.AttachmentContainerNews, f.ID)
		if err != nil {
			a.internalError(c, "news attachments", err)
			return
		}
		data := map[string]any{
			"News":        f,
			"Comments":    comments,
			"Attachments": atts,
			"Commentable": c.AllowedTo(domain.Perm("comment_news"), c.Project),
			"CanManage":   c.AllowedTo(domain.Perm("manage_news"), c.Project),
		}
		c.Render("news/show", data)
	default:
		respondNotAcceptable(c)
	}
}

// NewsEdit は news#edit。
func (a *App) NewsEdit(c *Req) {
	a.renderNewsForm(c, "news/edit", c.news(), nil)
}

// NewsUpdate は news#update。
func (a *App) NewsUpdate(c *Req) {
	f := c.news()
	assignNews(c, f.News)
	api := httpx.IsAPIRequest(c.R)
	res, err := a.saveContainerAttachments(c, newsAttachmentParams(c))
	if err != nil {
		a.internalError(c, "save attachments", err)
		return
	}
	validateNews(f, res)
	if f.errs.Any() {
		if api {
			c.RenderValidationErrors(f.errs)
			return
		}
		a.renderNewsForm(c, "news/edit", f, res)
		return
	}
	err = a.withTx(c, func(tx *db.Tx) error {
		if err := repository.UpdateNews(c.Ctx(), tx, f.News); err != nil {
			return err
		}
		return a.AttachmentStore.AttachSaved(c.Ctx(), tx, res, domain.AttachmentContainerNews, f.ID)
	})
	if err != nil {
		a.internalError(c, "update news", err)
		return
	}
	if api {
		c.RenderAPIOK()
		return
	}
	c.AttachFilesWarning(res)
	c.Flash().SetNotice(c.L("notice_successful_update"))
	c.Redirect("/news/" + itoa(f.ID))
}

// NewsDestroy は news#destroy。
func (a *App) NewsDestroy(c *Req) {
	f := c.news()
	var deleted []*domain.Attachment
	err := a.withTx(c, func(tx *db.Tx) error {
		var err error
		if deleted, err = repository.DeleteContainerAttachments(c.Ctx(), tx, domain.AttachmentContainerNews, []int64{f.ID}); err != nil {
			return err
		}
		return repository.DeleteNews(c.Ctx(), tx, f.ID)
	})
	if err != nil {
		a.internalError(c, "destroy news", err)
		return
	}
	a.deleteAttachmentsAfterCommit(c, deleted)
	if httpx.IsAPIRequest(c.R) {
		c.RenderAPIOK()
		return
	}
	c.Flash().SetNotice(c.L("notice_successful_delete"))
	c.Redirect("/projects/" + c.Project.Identifier + "/news")
}

// newsEvent は News の acts_as_event（render_feed に渡すイベント）。
func newsEvent(n *domain.News) *activity.Event {
	var author any
	if n.Author != nil {
		author = n.Author
	}
	return &activity.Event{
		Provider: activity.ProviderNews, ID: n.ID, Project: n.Project, Datetime: n.CreatedAt, Title: n.Title,
		Description: n.Description, URL: "/news/" + itoa(n.ID), Type: "news", Author: author,
		Group: "News:" + itoa(n.ID),
	}
}
