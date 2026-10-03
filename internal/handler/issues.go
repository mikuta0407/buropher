package handler

// IssuesController（app/controllers/issues_controller.rb）の参照系アクション（index / show）。
// 作成・更新・削除は internal/issues（ドメインサービス）の完成後に追加する。

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
)

// IssuesController（default_search_scope :issues）。
var IssuesController = &Controller{Name: "issues", MainMenu: true, DefaultSearchScope: "issues"}

// routesIssues は issues コントローラ（参照系）のルートを登録する。
//
//	resources :issues（index / show）
//	resources :projects { resources :issues, :only => [:index] }
func (a *App) routesIssues(r Router) {
	// before_action :find_issue, :only => [:show, :edit, :update, :issue_tab]
	// before_action :authorize, :except => [:index, :new, :create]
	// before_action :find_optional_project, :only => [:index, :new, :create]
	// accept_atom_auth :index, :show
	// accept_api_auth :index, :show, :create, :update, :destroy
	a.Handle(r, http.MethodGet, "/issues", IssuesController, "index", a.IssuesIndex,
		FindOptionalProject(), AcceptAtomAuth(), AcceptAPIAuth())
	a.Handle(r, http.MethodGet, "/projects/{project_id}/issues", IssuesController, "index", a.IssuesIndex,
		FindOptionalProject(), AcceptAtomAuth(), AcceptAPIAuth())
	a.routesContextMenusIssues(r)
	a.Handle(r, http.MethodGet, "/issues/{id}", IssuesController, "show", a.IssuesShow,
		Before(a.findIssue), Authorize(), AcceptAtomAuth(), AcceptAPIAuth())
	a.Handle(r, http.MethodGet, "/issues/{id}/tab/{name}", IssuesController, "issue_tab", a.IssuesTab,
		Before(a.findIssue), Authorize())
}

// ---------------------------------------------------------------- retrieve_query

const issueQuerySessionKey = "issue_query"

// issueQuerySession は session[:issue_query]。
func (c *Req) issueQuerySession() *query.SessionState {
	s := c.Session()
	if s == nil || !s.Has(issueQuerySessionKey) {
		return nil
	}
	raw, err := json.Marshal(s.Get(issueQuerySessionKey))
	if err != nil {
		return nil
	}
	var st query.SessionState
	if json.Unmarshal(raw, &st) != nil {
		return nil
	}
	return &st
}

// setIssueQuerySession は session[:issue_query] = st（nil なら削除）。
func (c *Req) setIssueQuerySession(st *query.SessionState) {
	s := c.Session()
	if s == nil {
		return
	}
	if st == nil {
		s.Delete(issueQuerySessionKey)
		return
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return
	}
	var v map[string]any
	if json.Unmarshal(raw, &v) == nil {
		s.Set(issueQuerySessionKey, v)
	}
}

// retrieveIssueQuery は IssuesController#retrieve_default_query + QueriesHelper#retrieve_query(IssueQuery, use_session)。
// エラー時は描画済み（ok = false）。
func (a *App) retrieveIssueQuery(c *Req, useSession bool) (*query.Query, bool) {
	env, err := a.queryEnv(c)
	if err != nil {
		a.internalError(c, "query env", err)
		return nil, false
	}
	p := queryParams(c)
	api := httpx.IsAPIRequest(c.R)
	sess := c.issueQuerySession()
	// retrieve_default_query
	id, setFilter, err := query.DefaultQueryID(c.Ctx(), env, query.KindIssue, c.Project, p, sess, useSession, api)
	if err != nil {
		a.internalError(c, "default query", err)
		return nil, false
	}
	if setFilter {
		p.SetFilter = true
	} else if id != 0 {
		p.QueryID = itoa(id)
	}
	q, newSess, err := query.Retrieve(c.Ctx(), env, query.KindIssue, c.Project, p, sess, query.RetrieveOptions{UseSession: useSession, API: api})
	switch {
	case errors.Is(err, query.ErrNotFound) || errors.Is(err, repository.ErrNotFound):
		c.Render404("")
		return nil, false
	case errors.Is(err, query.ErrUnauthorized):
		c.DenyAccess()
		return nil, false
	case err != nil:
		a.queryFailed(c, err)
		return nil, false
	}
	if useSession {
		c.setIssueQuerySession(newSess)
	}
	return q, true
}

// queryFailed は rescue_from Query::StatementInvalid（query_statement_invalid）とそれ以外のエラー。
func (a *App) queryFailed(c *Req, err error) {
	var qe *query.QueryError
	if errors.As(err, &qe) {
		a.logger().Error("Query::StatementInvalid", "err", err)
		if s := c.Session(); s != nil {
			s.Delete(issueQuerySessionKey)
		}
		c.RenderError(http.StatusInternalServerError, c.L("error_query_statement_invalid"))
		return
	}
	a.internalError(c, "issue query", err)
}

// ---------------------------------------------------------------- find_issue

const ctxIssue = "issue"

// findIssue は IssuesController#find_issue（Issue.find(params[:id])。@project も設定する）。
func (a *App) findIssue(c *Req) {
	id, ok := c.Params().IntStrict("id")
	if !ok {
		c.Render404("")
		return
	}
	r, err := repository.ReadIssue(c.Ctx(), a.DB, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.Render404("")
		} else {
			a.internalError(c, "find issue", err)
		}
		return
	}
	p, err := repository.GetProject(c.Ctx(), a.DB, r.ProjectID)
	if err != nil {
		a.internalError(c, "find issue project", err)
		return
	}
	// raise Unauthorized unless @issue.visible?
	ok, err = c.Authz().IssueVisible(c.Ctx(), &domain.Issue{ID: r.ID, ProjectID: r.ProjectID, TrackerID: r.TrackerID,
		StatusID: r.StatusID, AuthorID: r.AuthorID, AssignedToID: r.AssignedToID, IsPrivate: r.IsPrivate}, p)
	if err != nil {
		a.internalError(c, "issue visible", err)
		return
	}
	c.Project = p
	if !ok {
		c.DenyAccess()
		return
	}
	c.setLocal(ctxIssue, issueRowFromRead(r))
}

// currentIssue は @issue。
func (c *Req) currentIssue() *query.IssueRow {
	r, _ := c.local(ctxIssue).(*query.IssueRow)
	return r
}

// formatOf は params[:format]（空なら html）。
func formatOf(c *Req) string {
	f := strings.ToLower(httpx.Format(c.R))
	// Accept: */*（Mime::ALL）は respond_to の最初の形式。formatOf を使うアクションはどれも format.html が先頭
	if f == "" || f == httpx.FormatAll {
		return "html"
	}
	return f
}
