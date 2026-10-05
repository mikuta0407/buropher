// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/notify"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view"
)

// このファイルは AdminController#projects（ProjectAdminQuery の一覧）と ContextMenus::ProjectsController#index、
// projects#destroy / bulk_destroy（DestroyProjectJob / DestroyProjectsJob）。
//
// TODO(dedupe): admin users の移植で AdminController の他のアクションが
// 追加されたら、コントローラ変数を共通化する。

// AdminProjectsController は AdminController（projects アクションのみ）。layout 'admin'、main_menu false。
var AdminProjectsController = &Controller{Name: "admin", MainMenu: false}

// routesAdminProjects は admin#projects と context_menus/projects#index のルートを登録する。
//
//	get 'admin/projects', :to => 'admin#projects'
//	match '/admin/projects_context_menu', :to => 'context_menus/projects#index', :as => 'projects_context_menu', :via => [:get, :post]
func (a *App) routesAdminProjects(r Router) {
	a.Handle(r, http.MethodGet, "/admin/projects", AdminProjectsController, "projects", a.AdminProjects, RequireAdmin())
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		// before_action :require_admin（Redmine 6.1.3 #44109）
		a.Handle(r, m, "/admin/projects_context_menu", ContextMenusProjectsController, "index", a.ContextMenusProjects, RequireAdmin())
	}
}

// AdminProjects は admin#projects（GET /admin/projects）。
func (a *App) AdminProjects(c *Req) {
	q, ok := a.retrieveProjectQuery(c, query.KindProjectAdmin)
	if !ok {
		return
	}
	ctx := c.Ctx()
	qv, err := a.newQueryView(c, q, "ProjectAdminQuery", "/admin/projects")
	if err != nil {
		a.internalError(c, "query view", err)
		return
	}
	data := map[string]any{"QueryView": qv}
	if qv.Valid {
		count, err := q.Count(ctx)
		if err != nil {
			a.queryStatementError(c, err)
			return
		}
		pg := newMinPaginator(int(count), c.perPageOptionMin(), c.Params().String("page"))
		projects, err := q.Projects(ctx, query.ListOptions{Offset: pg.Offset(), Limit: pg.PerPage})
		if err != nil {
			a.queryStatementError(c, err)
			return
		}
		data["Projects"] = projects
		if len(projects) > 0 {
			list, err := a.projectListView(c, qv, projects, pg, int(count), c.User.IsAdmin())
			if err != nil {
				a.internalError(c, "project list", err)
				return
			}
			data["List"] = list
		}
	}
	sidebar, err := a.sidebarQueriesHTML(c, query.KindProjectAdmin, q, "/admin/projects")
	if err != nil {
		a.internalError(c, "sidebar queries", err)
		return
	}
	data["SidebarQueries"] = sidebar
	c.renderAdmin("admin/projects", data, httpx.IsXHR(c.R))
}

// ContextMenusProjects は ContextMenus::ProjectsController#index（レイアウトなし）。
func (a *App) ContextMenusProjects(c *Req) {
	ids := paramIDs(c.Params().Strings("ids"))
	var projects []*domain.Project
	if len(ids) > 0 {
		ps, err := repository.ProjectsByIDs(c.Ctx(), a.DB, ids)
		if err != nil {
			a.internalError(c, "context menu projects", err)
			return
		}
		for _, id := range ids {
			if p := ps[id]; p != nil {
				projects = append(projects, p)
			}
		}
	}
	if len(projects) == 0 {
		c.Render404("")
		return
	}
	data := map[string]any{"Projects": projects}
	if len(projects) == 1 {
		data["Project"] = projects[0]
	}
	q := url.Values{}
	for _, p := range projects {
		q.Add("ids[]", strconv.FormatInt(p.ID, 10))
	}
	data["BulkDestroyPath"] = "/projects/bulk_destroy?" + q.Encode()
	c.Render("context_menus/projects", data, RenderOptions{Layout: view.NoLayout})
}

// ProjectsDestroy は projects#destroy（DELETE /projects/:id）。確認の識別子が一致すれば
// DestroyProjectJob.schedule（自身と子孫を削除予約にしてから削除する）。
func (a *App) ProjectsDestroy(c *Req) {
	p := c.Project
	ok, err := a.projectDeletable(c, p)
	if err != nil {
		a.internalError(c, "project deletable", err)
		return
	}
	if !ok {
		c.DenyAccess()
		return
	}
	api := httpx.IsAPIRequest(c.R)
	if api || c.Params().String("confirm") == p.Identifier {
		if err := a.destroyProjects(c, []*domain.Project{p}); err != nil {
			a.internalError(c, "destroy project", err)
			return
		}
		// hide project in layout（@project = nil。最近使ったプロジェクトにも記録しない）
		c.Project = nil
		c.Flash().SetNotice(c.L("notice_successful_delete"))
		if api {
			c.RenderAPIOKMin()
			return
		}
		if c.User.IsAdmin() {
			c.Redirect("/admin/projects")
		} else {
			c.Redirect("/projects")
		}
		return
	}
	descendants, err := repository.ProjectDescendants(c.Ctx(), a.DB, p.ID)
	if err != nil {
		a.internalError(c, "descendants", err)
		return
	}
	names := make([]string, len(descendants))
	for i, d := range descendants {
		names[i] = d.Name
	}
	// hide project in layout
	c.Project = nil
	c.Render("projects/destroy", map[string]any{"ProjectToDestroy": p, "DescendantNames": names,
		"DescendantsText": strings.Join(names, ", ")})
}

// destroyProjects は DestroyProjectJob.schedule / DestroyProjectsJob.schedule + perform:
// 自身と子孫を削除予約にし、続けて削除する（Redmine はジョブで非同期に削除する。buropher は同じリクエストで行う）。
// 削除の完了・失敗は削除した本人にセキュリティ通知メールで知らせる（DestroyProjectJob#success / failure）。
func (a *App) destroyProjects(c *Req, projects []*domain.Project) error {
	ctx := c.Ctx()
	for _, p := range projects {
		if err := a.DB.WithTx(ctx, func(tx *db.Tx) error { return repository.ScheduleProjectDeletion(ctx, tx, p.ID) }); err != nil {
			return err
		}
	}
	for _, p := range projects {
		msg := "mail_destroy_project_successful"
		if ds, err := repository.ProjectDescendants(ctx, a.DB, p.ID); err == nil && len(ds) > 0 {
			msg = "mail_destroy_project_with_subprojects_successful"
		}
		var removed []*domain.Attachment
		err := a.DB.WithTx(ctx, func(tx *db.Tx) error {
			var err error
			removed, err = repository.DestroyProject(ctx, tx, p.ID)
			return err
		})
		if errors.Is(err, repository.ErrNotFound) {
			continue
		}
		if err != nil {
			a.logger().Error("destroy project job", "project", p.ID, "err", err)
			msg = "mail_destroy_project_failed"
		} else {
			// 添付の実ファイルはコミット後に消す（他の添付と共有していれば残る）
			a.deleteAttachmentsAfterCommit(c, removed)
		}
		a.Notify.SecurityNotification(ctx, []int64{c.User.ID}, c.User, c.remoteIP(), notify.SecurityOptions{
			Message: msg, Value: p.Name, URL: "/admin/projects", Title: "label_project_plural"})
	}
	c.ResetAuthz()
	return nil
}

// ProjectsBulkDestroy は projects#bulk_destroy（DELETE /projects/bulk_destroy）。
func (a *App) ProjectsBulkDestroy(c *Req) {
	ctx := c.Ctx()
	ids := paramIDs(c.Params().Strings("ids"))
	var projects []*domain.Project
	if len(ids) > 0 {
		ps, err := repository.ProjectsByIDs(ctx, a.DB, ids)
		if err != nil {
			a.internalError(c, "bulk destroy projects", err)
			return
		}
		all, err := repository.ListProjects(ctx, a.DB)
		if err != nil {
			a.internalError(c, "projects", err)
			return
		}
		// Project.where(id: params[:ids]).where.not(status: SCHEDULED_FOR_DELETION)（id 順）
		for _, p := range all {
			if ps[p.ID] != nil && !p.ScheduledForDeletion() {
				projects = append(projects, p)
			}
		}
		sortProjectsByID(projects)
	}
	if len(projects) == 0 {
		c.Render404("")
		return
	}
	if c.Params().String("confirm") == c.L("general_text_Yes") {
		if err := a.destroyProjects(c, projects); err != nil {
			a.internalError(c, "bulk destroy", err)
			return
		}
		c.Flash().SetNotice(c.L("notice_successful_delete"))
		c.Redirect("/admin/projects")
		return
	}
	type item struct {
		Project     *domain.Project
		Descendants string
	}
	var items []item
	q := url.Values{}
	for _, p := range projects {
		ds, err := repository.ProjectDescendants(ctx, a.DB, p.ID)
		if err != nil {
			a.internalError(c, "descendants", err)
			return
		}
		names := ""
		for i, d := range ds {
			if i > 0 {
				names += ", "
			}
			names += d.Name
		}
		items = append(items, item{Project: p, Descendants: names})
		q.Add("ids[]", strconv.FormatInt(p.ID, 10))
	}
	c.Render("projects/bulk_destroy", map[string]any{"Items": items, "FormPath": "/projects/bulk_destroy?" + q.Encode()})
}

func sortProjectsByID(ps []*domain.Project) {
	for i := 1; i < len(ps); i++ {
		for j := i; j > 0 && ps[j].ID < ps[j-1].ID; j-- {
			ps[j], ps[j-1] = ps[j-1], ps[j]
		}
	}
}
