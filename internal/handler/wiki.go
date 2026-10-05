// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
)

// WikiController（app/controllers/wiki_controller.rb）。
//
//	default_search_scope :wiki_pages
//	before_action :find_wiki, :authorize
//	before_action :find_existing_or_new_page, :only => [:show, :edit]
//	before_action :find_existing_page, :only => [:rename, :protect, :history, :diff, :annotate, :add_attachment, :destroy, :destroy_version]
//	before_action :find_attachments, :only => [:preview]
//	accept_api_auth :index, :show, :update, :destroy
var WikiController = &Controller{Name: "wiki", MainMenu: true, DefaultSearchScope: "wiki_pages"}

// routesWiki は wiki コントローラのルートを登録する（config/routes.rb の記述順）。
//
//	match 'wiki/index', :controller => 'wiki', :action => 'index', :via => :get
//	resources :wiki, :except => [:index, :create], :as => 'wiki_page' do
//	  member do get/post 'rename'; get 'history'; get 'diff'; match 'preview' (post/put/patch); post 'protect'; post 'add_attachment' end
//	  collection do get 'export'; get 'date_index'; post 'new' end
//	end
//	match 'wiki', :controller => 'wiki', :action => 'show', :via => :get
//	get 'wiki/:id/:version', :to => 'wiki#show', :constraints => {:version => /\d+/}
//	delete 'wiki/:id/:version', :to => 'wiki#destroy_version'
//	get 'wiki/:id/:version/annotate', :to => 'wiki#annotate'
//	get 'wiki/:id/:version/diff', :to => 'wiki#diff'
func (a *App) routesWiki(r Router) {
	w := WikiController
	base := "/projects/{project_id}/wiki"
	common := []ActionOption{Before(findWiki), Authorize()}
	opts := func(extra ...ActionOption) []ActionOption {
		return append(append([]ActionOption{}, common...), extra...)
	}
	existing := Before(findExistingPage)
	existingOrNew := Before(findExistingOrNewPage)

	a.Handle(r, http.MethodGet, base+"/index", w, "index", a.WikiIndex, opts(AcceptAPIAuth())...)
	// collection
	a.Handle(r, http.MethodGet, base+"/export", w, "export", a.WikiExport, opts()...)
	a.Handle(r, http.MethodGet, base+"/date_index", w, "date_index", a.WikiDateIndex, opts()...)
	a.Handle(r, http.MethodPost, base+"/new", w, "new", a.WikiNew, opts()...)
	a.Handle(r, http.MethodGet, base+"/new", w, "new", a.WikiNew, opts()...)
	// member
	a.Handle(r, http.MethodGet, base+"/{id}/rename", w, "rename", a.WikiRename, opts(existing)...)
	a.Handle(r, http.MethodPost, base+"/{id}/rename", w, "rename", a.WikiRename, opts(existing)...)
	a.Handle(r, http.MethodGet, base+"/{id}/history", w, "history", a.WikiHistory, opts(existing)...)
	a.Handle(r, http.MethodGet, base+"/{id}/diff", w, "diff", a.WikiDiff, opts(existing)...)
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch} {
		a.Handle(r, m, base+"/{id}/preview", w, "preview", a.WikiPreview, opts(Before(findAttachments))...)
	}
	a.Handle(r, http.MethodPost, base+"/{id}/protect", w, "protect", a.WikiProtect, opts(existing)...)
	a.Handle(r, http.MethodPost, base+"/{id}/add_attachment", w, "add_attachment", a.WikiAddAttachment, opts(existing)...)
	a.Handle(r, http.MethodGet, base+"/{id}/edit", w, "edit", a.WikiEdit, opts(existingOrNew)...)
	a.Handle(r, http.MethodGet, base+"/{id}", w, "show", a.WikiShow, opts(existingOrNew, AcceptAPIAuth())...)
	a.Handle(r, http.MethodPatch, base+"/{id}", w, "update", a.WikiUpdate, opts(AcceptAPIAuth())...)
	a.Handle(r, http.MethodPut, base+"/{id}", w, "update", a.WikiUpdate, opts(AcceptAPIAuth())...)
	a.Handle(r, http.MethodDelete, base+"/{id}", w, "destroy", a.WikiDestroy, opts(existing, AcceptAPIAuth())...)
	a.Handle(r, http.MethodGet, base, w, "show", a.WikiShow, opts(existingOrNew, AcceptAPIAuth())...)
	a.Handle(r, http.MethodGet, base+"/{id}/{version:[0-9]+}", w, "show", a.WikiShow, opts(existingOrNew, AcceptAPIAuth())...)
	a.Handle(r, http.MethodDelete, base+"/{id}/{version}", w, "destroy_version", a.WikiDestroyVersion, opts(existing)...)
	a.Handle(r, http.MethodGet, base+"/{id}/{version}/annotate", w, "annotate", a.WikiAnnotate, opts(existing)...)
	a.Handle(r, http.MethodGet, base+"/{id}/{version}/diff", w, "diff", a.WikiDiff, opts(existing)...)
	// GET wiki/:id/annotate はルートが無いが Redmine の url_for は ?version= で生成しない（annotate の
	// リンクは常に版付き）。
}

// ---------------------------------------------------------------- before_action

type wikiCtxKey struct{}

// wikiState は @wiki / @page（find_wiki / find_existing_page の結果）。
type wikiState struct {
	Wiki *domain.Wiki
	Page *domain.WikiPage
}

func (c *Req) wikiState() *wikiState {
	if s, ok := c.value(wikiCtxKey{}).(*wikiState); ok {
		return s
	}
	return &wikiState{}
}

// findWiki は WikiController#find_wiki（@project = Project.find(params[:project_id]); @wiki = @project.wiki）。
func findWiki(c *Req) {
	if !c.FindProject(c.Params().String("project_id")) {
		return
	}
	w, err := repository.FindWiki(c.Ctx(), c.App.DB, c.Project.ID)
	if err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			c.App.logger().Error("find wiki", "err", err)
		}
		c.Render404("")
		return
	}
	c.setValue(wikiCtxKey{}, &wikiState{Wiki: w})
}

// findPage は Wiki#find_page(title, with_redirect:)。redirected は page_found_with_redirect?。
func (a *App) findPage(c *Req, w *domain.Wiki, title string, withRedirect bool) (page *domain.WikiPage, redirected bool, err error) {
	if httpx.IsBlank(title) {
		title = w.StartPage
	}
	title = domain.WikiTitleize(title)
	page, err = repository.FindWikiPage(c.Ctx(), a.DB, w.ID, title)
	if errors.Is(err, repository.ErrNotFound) {
		page, err = nil, nil
		if withRedirect {
			p, rerr := repository.FindWikiRedirectTarget(c.Ctx(), a.DB, w.ID, title)
			switch {
			case rerr == nil:
				page, redirected = p, true
			case !errors.Is(rerr, repository.ErrNotFound):
				err = rerr
			}
		}
	}
	if err != nil {
		return nil, false, err
	}
	if page != nil {
		if err := a.loadPageProject(c, page); err != nil {
			return nil, false, err
		}
	}
	return page, redirected, nil
}

// loadPageProject は page.project（wiki.project）を設定する。
func (a *App) loadPageProject(c *Req, page *domain.WikiPage) error {
	if ws := c.wikiState(); ws.Wiki != nil && ws.Wiki.ID == page.WikiID && c.Project != nil {
		page.Project = c.Project
		return nil
	}
	w, err := repository.GetWiki(c.Ctx(), a.DB, page.WikiID)
	if err != nil {
		return err
	}
	p, err := repository.GetProject(c.Ctx(), a.DB, w.ProjectID)
	if err != nil {
		return err
	}
	page.Project = p
	return nil
}

// newWikiPage は WikiPage.new(:wiki => wiki, :title => title)（DEFAULT_PROTECTED_PAGES は保護する）。
func newWikiPage(c *Req, w *domain.Wiki, title string) *domain.WikiPage {
	p := &domain.WikiPage{WikiID: w.ID, Title: domain.WikiTitleize(title), Project: c.Project}
	for _, t := range domain.WikiDefaultProtectedPages {
		if strings.ToLower(p.Title) == t {
			p.Protected = true
		}
	}
	return p
}

// findOrNewPage は Wiki#find_or_new_page(title)。
func (a *App) findOrNewPage(c *Req, w *domain.Wiki, title string) (*domain.WikiPage, bool, error) {
	if httpx.IsBlank(title) {
		title = w.StartPage
	}
	p, redirected, err := a.findPage(c, w, title, true)
	if err != nil {
		return nil, false, err
	}
	if p == nil {
		return newWikiPage(c, w, title), redirected, nil
	}
	return p, redirected, nil
}

// findExistingOrNewPage は find_existing_or_new_page。
func findExistingOrNewPage(c *Req) {
	ws := c.wikiState()
	p, redirected, err := c.App.findOrNewPage(c, ws.Wiki, c.Params().String("id"))
	if err != nil {
		c.App.logger().Error("find wiki page", "err", err)
		c.renderInternalError()
		c.Halt()
		return
	}
	ws.Page = p
	if redirected && !p.NewRecord() {
		redirectToPage(c, p)
	}
}

// findExistingPage は find_existing_page（無ければ 404）。
func findExistingPage(c *Req) {
	ws := c.wikiState()
	p, redirected, err := c.App.findPage(c, ws.Wiki, c.Params().String("id"), true)
	if err != nil {
		c.App.logger().Error("find wiki page", "err", err)
		c.renderInternalError()
		c.Halt()
		return
	}
	if p == nil {
		c.Render404("")
		return
	}
	ws.Page = p
	if redirected {
		redirectToPage(c, p)
	}
}

// redirectToPage は redirect_to_page（リダイレクト先のプロジェクトが見えなければ 404）。
func redirectToPage(c *Req, page *domain.WikiPage) {
	if page.Project != nil {
		if ok, err := c.Authz().ProjectVisible(c.Ctx(), page.Project); err == nil && ok {
			path := wikiPagePath(page.Project, page.Title)
			if c.Action != "show" {
				path += "/" + c.Action
			}
			c.Redirect(path)
			return
		}
	}
	c.Render404("")
}

// editable は editable?(page)（WikiPage#editable_by?: 保護されていなければ、または protect_wiki_pages 権限）。
func (c *Req) wikiEditable(page *domain.WikiPage) bool {
	if !page.Protected {
		return true
	}
	return c.AllowedTo(domain.Perm("protect_wiki_pages"), c.wikiPageProject(page))
}

// wikiRedirectTargetEditable は他プロジェクトの Wiki への転送（ページを別プロジェクトへ移動した跡）で
// 見つかったページを、URL のプロジェクトのページとして編集・プレビューしてよいか。
// Redmine の wiki#update / #preview は find_page が他の Wiki へのリダイレクトをたどった結果を
// そのまま使い、転送先プロジェクトの権限を確かめない（非メンバーが非公開プロジェクトのページを書き換えられる）。
// buropher は転送先プロジェクトで view_wiki_pages と edit_wiki_pages の両方を持つときだけ許す。
func (c *Req) wikiRedirectTargetEditable(w *domain.Wiki, page *domain.WikiPage, redirected bool) bool {
	if !redirected || page == nil || page.NewRecord() || w == nil || page.WikiID == w.ID {
		return true
	}
	p := c.wikiPageProject(page)
	return c.AllowedTo(domain.Perm("view_wiki_pages"), p) && c.AllowedTo(domain.Perm("edit_wiki_pages"), p)
}

func (c *Req) wikiPageProject(page *domain.WikiPage) *domain.Project {
	if page.Project != nil {
		return page.Project
	}
	return c.Project
}

// ---------------------------------------------------------------- パス

// projectParam は Project#to_param（識別子）。
func projectParam(p *domain.Project) string {
	if p == nil {
		return ""
	}
	if p.Identifier != "" {
		return p.Identifier
	}
	return itoa(p.ID)
}

// wikiPagePath は project_wiki_page_path(project, title)。
func wikiPagePath(p *domain.Project, title string) string {
	return "/projects/" + url.PathEscape(projectParam(p)) + "/wiki/" + escapePathSegment(title)
}

// wikiIndexPath は project_wiki_index_path(project)。
func wikiIndexPath(p *domain.Project) string {
	return "/projects/" + url.PathEscape(projectParam(p)) + "/wiki/index"
}

// escapePathSegment は Rails のパスパラメータのエスケープ（Journey::Router::Utils.escape_segment）。
func escapePathSegment(s string) string {
	const keep = "-._~!$&'()*+,;=:@"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.IndexByte(keep, ch) >= 0 {
			b.WriteByte(ch)
			continue
		}
		b.WriteString("%" + strings.ToUpper(strconv.FormatInt(int64(ch)|0x100, 16)[1:]))
	}
	return b.String()
}
