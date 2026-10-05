// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"errors"
	"io/fs"
	"net/http"
	"strconv"

	"github.com/mikuta0407/buropher/internal/apibuilder"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/timelog"
	"github.com/mikuta0407/buropher/internal/webhook"
	"github.com/mikuta0407/buropher/web"
)

// TimelogController（app/controllers/timelog_controller.rb）。menu_item :time_entries。
var TimelogController = &Controller{Name: "timelog", MainMenu: true, MenuItem: func(string) string { return "time_entries" }}

// routesTimelog は timelog コントローラのルートを登録する。
func (a *App) routesTimelog(r Router) {
	// before_action :find_time_entry, :only => [:show, :edit, :update]
	// before_action :check_editability, :only => [:edit, :update]
	// before_action :find_time_entries, :only => [:bulk_edit, :bulk_update, :destroy]
	// before_action :authorize, :only => [:show, :edit, :update, :bulk_edit, :bulk_update, :destroy]
	// before_action :find_optional_issue, :only => [:new, :create]
	// before_action :find_optional_project, :only => [:index, :report]
	// accept_atom_auth :index
	// accept_api_auth :index, :show, :create, :update, :destroy
	findOne := Before(a.teFindTimeEntry)
	editable := Before(a.teCheckEditability)
	findMany := Before(a.teFindTimeEntries)
	optIssue := Before(a.teFindOptionalIssue)
	optProject := FindOptionalProject()
	api := AcceptAPIAuth()
	ctrl := TimelogController
	a.routesTimelogContextMenu(r)

	// プロジェクト配下（resources :time_entries, :except => [:show, :edit, :update, :destroy]）
	a.Handle(r, http.MethodGet, "/projects/{project_id}/time_entries/report", ctrl, "report", a.TimelogReport, optProject)
	a.Handle(r, http.MethodGet, "/projects/{project_id}/time_entries/new", ctrl, "new", a.TimelogNew, optIssue)
	a.Handle(r, http.MethodGet, "/projects/{project_id}/time_entries", ctrl, "index", a.TimelogIndex, optProject, AcceptAtomAuth(), api)
	a.Handle(r, http.MethodPost, "/projects/{project_id}/time_entries", ctrl, "create", a.TimelogCreate, optIssue, api)
	// チケット配下（resources :time_entries, :only => [:new, :create]）
	a.Handle(r, http.MethodGet, "/issues/{issue_id}/time_entries/new", ctrl, "new", a.TimelogNew, optIssue)
	a.Handle(r, http.MethodPost, "/issues/{issue_id}/time_entries", ctrl, "create", a.TimelogCreate, optIssue, api)
	// グローバル
	a.Handle(r, http.MethodGet, "/time_entries/report", ctrl, "report", a.TimelogReport, optProject)
	a.Handle(r, http.MethodGet, "/time_entries/bulk_edit", ctrl, "bulk_edit", a.TimelogBulkEdit, findMany, Authorize())
	a.Handle(r, http.MethodPost, "/time_entries/bulk_edit", ctrl, "bulk_edit", a.TimelogBulkEdit, findMany, Authorize())
	a.Handle(r, http.MethodPost, "/time_entries/bulk_update", ctrl, "bulk_update", a.TimelogBulkUpdate, findMany, Authorize())
	a.Handle(r, http.MethodDelete, "/time_entries/destroy", ctrl, "destroy", a.TimelogDestroy, findMany, Authorize(), api)
	a.Handle(r, http.MethodGet, "/time_entries/new", ctrl, "new", a.TimelogNew, optIssue)
	a.Handle(r, http.MethodPost, "/time_entries/new", ctrl, "new", a.TimelogNew, optIssue)
	a.Handle(r, http.MethodGet, "/time_entries", ctrl, "index", a.TimelogIndex, optProject, AcceptAtomAuth(), api)
	a.Handle(r, http.MethodPost, "/time_entries", ctrl, "create", a.TimelogCreate, optIssue, api)
	a.Handle(r, http.MethodGet, "/time_entries/{id}/edit", ctrl, "edit", a.TimelogEdit, findOne, editable, Authorize())
	a.Handle(r, http.MethodPatch, "/time_entries/{id}/edit", ctrl, "edit", a.TimelogEdit, findOne, editable, Authorize())
	a.Handle(r, http.MethodGet, "/time_entries/{id}", ctrl, "show", a.TimelogShow, findOne, Authorize(), api)
	a.Handle(r, http.MethodPatch, "/time_entries/{id}", ctrl, "update", a.TimelogUpdate, findOne, editable, Authorize(), api)
	a.Handle(r, http.MethodPut, "/time_entries/{id}", ctrl, "update", a.TimelogUpdate, findOne, editable, Authorize(), api)
	a.Handle(r, http.MethodDelete, "/time_entries/{id}", ctrl, "destroy", a.TimelogDestroy, findMany, Authorize(), api)
}

type teEntryKey struct{}
type teEntriesKey struct{}
type teEnvKey struct{}

// teEnv は User.current の timelog.Env（リクエスト内で 1 つ）。
func (a *App) teEnv(c *Req) *timelog.Env {
	if v, ok := c.value(teEnvKey{}).(*timelog.Env); ok {
		return v
	}
	env := &timelog.Env{Q: a.DB, Settings: a.Settings, User: c.User, Az: c.Authz(), Loc: c.Loc, Now: a.now}
	c.setValue(teEnvKey{}, env)
	return env
}

// teFindTimeEntry は find_time_entry。
func (a *App) teFindTimeEntry(c *Req) {
	id, ok := c.Params().IntStrict("id")
	if !ok {
		c.Render404("")
		return
	}
	env := a.teEnv(c)
	t, err := env.Find(c.Ctx(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.Render404("")
		} else {
			a.internalError(c, "find time entry", err)
		}
		return
	}
	vis, err := env.Visible(c.Ctx(), t, c.User)
	if err != nil {
		a.internalError(c, "time entry visible", err)
		return
	}
	if !vis {
		c.DenyAccess()
		return
	}
	p, err := env.Project(c.Ctx(), t.ProjectID)
	if err != nil {
		a.internalError(c, "time entry project", err)
		return
	}
	c.Project = p
	c.setValue(teEntryKey{}, t)
}

// teCheckEditability は check_editability。
func (a *App) teCheckEditability(c *Req) {
	t := c.value(teEntryKey{}).(*timelog.Entry)
	ok, err := a.teEnv(c).EditableBy(c.Ctx(), t, c.User)
	if err != nil {
		a.internalError(c, "time entry editable", err)
		return
	}
	if !ok {
		c.Render403("")
	}
}

// teFindTimeEntries は find_time_entries。
func (a *App) teFindTimeEntries(c *Req) {
	p := c.Params()
	v, ok := p.Get("id")
	if !ok || httpx.IsBlank(v) {
		v, _ = p.Get("ids")
	}
	ids := idsFromParam(v)
	env := a.teEnv(c)
	rs, err := repository.TimeEntriesByIDs(c.Ctx(), a.DB, ids)
	if err != nil {
		a.internalError(c, "find time entries", err)
		return
	}
	if len(rs) == 0 {
		c.Render404("")
		return
	}
	var entries []*timelog.Entry
	var projects []*domain.Project
	for _, r := range rs {
		t := timelog.FromRecord(r)
		ok, err := env.EditableBy(c.Ctx(), t, c.User)
		if err != nil {
			a.internalError(c, "time entry editable", err)
			return
		}
		if !ok {
			c.DenyAccess()
			return
		}
		entries = append(entries, t)
		pr, err := env.Project(c.Ctx(), t.ProjectID)
		if err != nil {
			a.internalError(c, "time entry project", err)
			return
		}
		if pr != nil && !containsProject(projects, pr) {
			projects = append(projects, pr)
		}
	}
	c.Projects = projects
	if len(projects) == 1 {
		c.Project = projects[0]
	}
	c.setValue(teEntriesKey{}, entries)
}

func containsProject(ps []*domain.Project, p *domain.Project) bool {
	for _, x := range ps {
		if x.ID == p.ID {
			return true
		}
	}
	return false
}

// teFindOptionalIssue は find_optional_issue。
func (a *App) teFindOptionalIssue(c *Req) {
	p := c.Params()
	if !p.Present("issue_id") {
		// find_optional_project
		if p.Present("project_id") {
			pr, err := c.lookupProject(p.String("project_id"))
			if err != nil {
				if errors.Is(err, repository.ErrNotFound) {
					c.Render404("")
				} else {
					a.internalError(c, "find project", err)
				}
				return
			}
			c.Project = pr
		}
		c.Authorize(c.Controller.Name, c.Action, true)
		return
	}
	// Issue.find の RecordNotFound は rescue されない（public/404.html）
	id, ok := p.IntStrict("issue_id")
	if !ok {
		teRecordNotFound(c)
		return
	}
	iss, err := a.teEnv(c).Issues().Find(c.Ctx(), id)
	if err != nil {
		a.internalError(c, "find issue", err)
		return
	}
	if iss == nil {
		teRecordNotFound(c)
		return
	}
	pr, err := a.teEnv(c).Project(c.Ctx(), &iss.ProjectID)
	if err != nil {
		a.internalError(c, "issue project", err)
		return
	}
	c.Project = pr
	c.setValue(teIssueKey{}, iss.ID)
	c.Authorize(c.Controller.Name, c.Action, false)
}

type teIssueKey struct{}

// teIssueID は @issue の id（無ければ nil）。
func teIssueID(c *Req) *int64 {
	if v, ok := c.value(teIssueKey{}).(int64); ok {
		return &v
	}
	return nil
}

// teFormat は respond_to の形式（html / js / json / xml / atom / csv。不明なら ""）。
func teFormat(c *Req, allowed ...string) string {
	return httpx.Negotiate(c.R, allowed...)
}

// teTimeEntriesPath は _time_entries_path(project, issue)。
func teTimeEntriesPath(p *domain.Project) string {
	if p != nil {
		return "/projects/" + p.Identifier + "/time_entries"
	}
	return "/time_entries"
}

// teReportPath は _report_time_entries_path(project, issue)。
func teReportPath(p *domain.Project) string { return teTimeEntriesPath(p) + "/report" }

// ---------------------------------------------------------------- retrieve_time_entry_query

// teRetrieveQuery は retrieve_time_entry_query（retrieve_query(TimeEntryQuery, false)）。
func (a *App) teRetrieveQuery(c *Req) (*query.Query, bool) {
	env, err := a.queryEnv(c)
	if err != nil {
		a.internalError(c, "query env", err)
		return nil, false
	}
	q, _, err := query.Retrieve(c.Ctx(), env, query.KindTimeEntry, c.Project, queryParams(c), nil,
		query.RetrieveOptions{UseSession: false, API: httpx.IsAPIRequest(c.R)})
	if err != nil {
		switch {
		case errors.Is(err, query.ErrNotFound):
			// retrieve_query の find の RecordNotFound は rescue されない（public/404.html）
			teRecordNotFound(c)
		case errors.Is(err, query.ErrUnauthorized):
			c.DenyAccess()
		default:
			a.teQueryFailed(c, err)
		}
		return nil, false
	}
	return q, true
}

// teQueryFailed は query_statement_invalid / query_error。
func (a *App) teQueryFailed(c *Req, err error) {
	var qe *query.QueryError
	if errors.As(err, &qe) {
		a.logger().Error("query statement invalid", "err", err)
		c.RenderError(http.StatusInternalServerError, c.L("error_query_statement_invalid"))
		return
	}
	a.internalError(c, "time entry query", err)
}

// ---------------------------------------------------------------- show（API のみ）

// TimelogShow は timelog#show（HTML は 406）。
func (a *App) TimelogShow(c *Req) {
	switch teFormat(c, "html", "json", "xml") {
	case "json", "xml":
	default:
		c.head(http.StatusNotAcceptable)
		return
	}
	t := c.value(teEntryKey{}).(*timelog.Entry)
	a.teRenderShowAPI(c, t, 0)
}

// teRenderShowAPI は show.api.rsb。本家は custom_field_values（非表示・ロール限定のカスタムフィールドも含む）を
// 出すが、閲覧できない値が漏れるため visible_custom_field_values にする。
func (a *App) teRenderShowAPI(c *Req, t *timelog.Entry, status int) {
	ctx := c.Ctx()
	env := a.teEnv(c)
	l, err := a.newTELookup(c, []*timelog.Entry{t})
	if err != nil {
		a.internalError(c, "time entry lookup", err)
		return
	}
	cvs, err := env.VisibleCustomFieldValues(ctx, t, c.User)
	if err != nil {
		a.internalError(c, "time entry custom values", err)
		return
	}
	c.RenderAPI(status, func(b apibuilder.Builder) {
		b.Object("time_entry", func() { l.apiEntry(b, t, cvs) })
	})
}

// apiEntry は index / show の time_entry 要素の中身。
func (l *teLookup) apiEntry(b apibuilder.Builder, t *timelog.Entry, cvs []*domain.CustomFieldValue) {
	b.Value("id", t.ID)
	if p := l.project(t.ProjectID); p != nil {
		b.Attrs("project", apibuilder.A("id", p.ID, "name", p.Name))
	}
	if t.IssueID != nil && l.issueExists(*t.IssueID) {
		b.Attrs("issue", apibuilder.A("id", *t.IssueID))
	}
	if u := l.user(t.UserID); u != nil {
		b.Attrs("user", apibuilder.A("id", u.ID, "name", l.c.Page().UserName(u)))
	}
	if t.ActivityID != nil {
		if act := l.activities[*t.ActivityID]; act != nil {
			b.Attrs("activity", apibuilder.A("id", act.ID, "name", act.Name))
		}
	}
	h := 0.0
	if rh := t.RoundedHours(); rh != nil {
		h = round2(*rh)
	}
	b.Value("hours", h)
	// api.comments time_entry.comments（NULL は null、"" は ""）
	if t.Comments == nil {
		b.Value("comments", nil)
	} else {
		b.Value("comments", *t.Comments)
	}
	if t.SpentOn != nil {
		b.Value("spent_on", t.SpentOn.Format("2006-01-02"))
	} else {
		b.Value("spent_on", nil)
	}
	b.Value("created_on", t.CreatedAt)
	b.Value("updated_on", t.UpdatedAt)
	teRenderAPICustomValues(b, cvs)
}

// round2 は Float#round(2)。
func round2(f float64) float64 {
	s := strconv.FormatFloat(f, 'f', 2, 64)
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// teRenderAPICustomValues は render_api_custom_values(custom_values, api)。
func teRenderAPICustomValues(b apibuilder.Builder, cvs []*domain.CustomFieldValue) {
	if len(cvs) == 0 {
		return
	}
	b.Array("custom_fields", nil, func() {
		for _, cv := range cvs {
			attrs := apibuilder.A("id", cv.Field.ID, "name", cv.Field.Name)
			if cv.Field.Multiple {
				attrs = apibuilder.A("id", cv.Field.ID, "name", cv.Field.Name, "multiple", true)
			}
			b.ObjectAttrs("custom_field", attrs, func() {
				if cv.Field.Multiple {
					b.Array("value", nil, func() {
						for _, v := range cv.Values {
							b.Value("value", v)
						}
					})
				} else if len(cv.Values) == 0 {
					b.Value("value", nil)
				} else {
					b.Value("value", cv.Values[0])
				}
			})
		}
	})
}

// ---------------------------------------------------------------- destroy

// TimelogDestroy は timelog#destroy（単体・一括）。
func (a *App) TimelogDestroy(c *Req) {
	entries := c.value(teEntriesKey{}).([]*timelog.Entry)
	ctx := c.Ctx()
	destroyed := true
	for _, t := range entries {
		a.prepareDeleteWebhooks(c, webhook.TypeTimeEntry, t.ID)
	}
	err := a.DB.WithTx(ctx, func(tx *db.Tx) error {
		env := a.teEnv(c).WithQ(tx)
		for _, t := range entries {
			if err := env.Destroy(ctx, t); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		a.logger().Error("destroy time entries", "err", err)
		destroyed = false
	}
	if destroyed {
		a.enqueuePreparedDeleteWebhooks(c, webhook.TypeTimeEntry)
	}
	if httpx.IsAPIRequest(c.R) {
		if destroyed {
			c.RenderAPIOK()
		} else {
			c.RenderAPIErrors(c.L("notice_unable_delete_time_entry"))
		}
		return
	}
	if destroyed {
		c.Flash().SetNotice(c.L("notice_successful_delete"))
	} else {
		c.Flash().SetError(c.L("notice_unable_delete_time_entry"))
	}
	var first *domain.Project
	if len(c.Projects) > 0 {
		first = c.Projects[0]
	}
	c.RedirectBackOrDefault(teTimeEntriesPath(first), true)
}

// teRecordNotFound は rescue されない ActiveRecord::RecordNotFound の応答（ActionDispatch::ShowExceptions。
// json / xml は {status, error}、それ以外は public/404.html）。
func teRecordNotFound(c *Req) {
	c.Halt()
	switch httpx.Format(c.R) {
	case "json":
		c.W.Header().Set("Content-Type", "application/json; charset=utf-8")
		c.W.WriteHeader(http.StatusNotFound)
		_, _ = c.W.Write([]byte(`{"status":404,"error":"Not Found"}`))
	case "xml":
		c.W.Header().Set("Content-Type", "application/xml; charset=utf-8")
		c.W.WriteHeader(http.StatusNotFound)
		_, _ = c.W.Write([]byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<hash>\n  <status type=\"integer\">404</status>\n  <error>Not Found</error>\n</hash>\n"))
	default:
		b, err := fs.ReadFile(web.Public(), "404.html")
		if err != nil {
			http.NotFound(c.W, c.R)
			return
		}
		c.W.Header().Set("Content-Type", "text/html; charset=utf-8")
		c.W.WriteHeader(http.StatusNotFound)
		_, _ = c.W.Write(b)
	}
}
