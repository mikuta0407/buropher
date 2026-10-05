// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

// IssueRelationsController（app/controllers/issue_relations_controller.rb）。
// 関連の作成・削除は internal/issues（NewRelation / CreateRelation / DestroyRelation）で行い、
// 両チケットのジャーナル（journal_details の property 'relation'）もそこで記録する。

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/apibuilder"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/urlroot"
	"github.com/mikuta0407/buropher/internal/validation"
)

// IssueRelationsController（helper :issues）。
var IssueRelationsController = &Controller{Name: "issue_relations", MainMenu: true}

// routesIssueRelations は
//
//	resources :issues do
//	  shallow do
//	    resources :relations, :controller => 'issue_relations', :only => [:index, :show, :create, :destroy]
//	  end
//	end
func (a *App) routesIssueRelations(r Router) {
	// before_action :find_issue, :authorize, :only => [:index, :create]
	// before_action :find_relation, :only => [:show, :destroy]
	// accept_api_auth :index, :show, :create, :destroy
	a.Handle(r, http.MethodGet, "/issues/{issue_id}/relations", IssueRelationsController, "index", a.IssueRelationsIndex,
		Before(a.findRelationsIssue), Authorize(), AcceptAPIAuth())
	a.Handle(r, http.MethodPost, "/issues/{issue_id}/relations", IssueRelationsController, "create", a.IssueRelationsCreate,
		Before(a.findRelationsIssue), Authorize(), AcceptAPIAuth())
	a.Handle(r, http.MethodGet, "/relations/{id}", IssueRelationsController, "show", a.IssueRelationsShow,
		Before(a.findRelation), AcceptAPIAuth())
	a.Handle(r, http.MethodDelete, "/relations/{id}", IssueRelationsController, "destroy", a.IssueRelationsDestroy,
		Before(a.findRelation), AcceptAPIAuth())
}

const ctxRelation = "relation"

// findRelationsIssue は IssueRelationsController#find_issue（Issue.find(params[:issue_id])）。
//
// 本家は可視性を見ないため、プロジェクトで view_issues / manage_issue_relations を持つだけで
// 見えないチケット（非公開など）の関連一覧（関連先の番号）を API で取得でき、関連の追加で
// そのチケットにジャーナルを書き込めた。buropher はチケットが見えなければ拒否する。
func (a *App) findRelationsIssue(c *Req) {
	id, ok := c.Params().IntStrict("issue_id")
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
	c.Project = p
	ok, err = c.Authz().IssueVisible(c.Ctx(), &domain.Issue{ID: r.ID, ProjectID: r.ProjectID, TrackerID: r.TrackerID,
		StatusID: r.StatusID, AuthorID: r.AuthorID, AssignedToID: r.AssignedToID, IsPrivate: r.IsPrivate}, p)
	if err != nil {
		a.internalError(c, "issue visible", err)
		return
	}
	if !ok {
		c.DenyAccess()
		return
	}
	c.setLocal(ctxIssue, issueRowFromRead(r))
}

// findRelation は find_relation（IssueRelation.find(params[:id])）。
func (a *App) findRelation(c *Req) {
	id, ok := c.Params().IntStrict("id")
	if !ok {
		c.Render404("")
		return
	}
	rs, err := repository.RelationsByIDs(c.Ctx(), a.DB, []int64{id})
	if err != nil {
		a.internalError(c, "find relation", err)
		return
	}
	r := rs[id]
	if r == nil {
		c.Render404("")
		return
	}
	c.setLocal(ctxRelation, &domain.IssueRelation{ID: r.ID, IssueFromID: r.IssueFromID, IssueToID: r.IssueToID,
		RelationType: r.RelationType, Delay: r.Delay})
}

func (c *Req) currentRelation() *domain.IssueRelation {
	r, _ := c.local(ctxRelation).(*domain.IssueRelation)
	return r
}

// renderRelationAPI は show.api.rsb の api.relation。
func renderRelationAPI(b apibuilder.Builder, r *domain.IssueRelation) {
	b.Object("relation", func() { relationAPIFields(b, r) })
}

func relationAPIFields(b apibuilder.Builder, r *domain.IssueRelation) {
	b.Value("id", r.ID)
	b.Value("issue_id", r.IssueFromID)
	b.Value("issue_to_id", r.IssueToID)
	b.Value("relation_type", r.RelationType)
	if r.Delay != nil {
		b.Value("delay", *r.Delay)
	} else {
		b.Value("delay", nil)
	}
}

// IssueRelationsIndex は issue_relations#index（html は head :ok、API は index.api.rsb）。
func (a *App) IssueRelationsIndex(c *Req) {
	switch formatOf(c) {
	case "html":
		httpx.HeadAs(c.W, c.R, http.StatusOK, "html")
		c.Halt()
		return
	case "json", "xml":
	default:
		renderUnknownFormat(c)
		return
	}
	e := a.writeIssuesEnv(c, a.DB)
	iss, err := e.Find(c.Ctx(), c.currentIssue().ID)
	if err != nil || iss == nil {
		a.internalError(c, "relations", err)
		return
	}
	rels, err := e.Relations(c.Ctx(), iss)
	if err != nil {
		a.internalError(c, "relations", err)
		return
	}
	c.RenderAPI(0, func(b apibuilder.Builder) {
		b.Array("relations", nil, func() {
			for _, r := range rels {
				b.Object("relation", func() { relationAPIFields(b, r) })
			}
		})
	})
}

// IssueRelationsShow は issue_relations#show（raise Unauthorized unless @relation.visible?）。
func (a *App) IssueRelationsShow(c *Req) {
	rel := c.currentRelation()
	ok, err := a.relationVisible(c, rel)
	if err != nil {
		a.internalError(c, "relation visible", err)
		return
	}
	if !ok {
		c.DenyAccess()
		return
	}
	switch formatOf(c) {
	case "html":
		httpx.HeadAs(c.W, c.R, http.StatusOK, "html")
		c.Halt()
	case "json", "xml":
		c.RenderAPI(0, func(b apibuilder.Builder) { renderRelationAPI(b, rel) })
	default:
		renderUnknownFormat(c)
	}
}

// relationVisible は IssueRelation#visible?（両方のチケットが User.current に見える）。
func (a *App) relationVisible(c *Req, rel *domain.IssueRelation) (bool, error) {
	e := a.writeIssuesEnv(c, a.DB)
	for _, id := range []int64{rel.IssueFromID, rel.IssueToID} {
		iss, err := e.Find(c.Ctx(), id)
		if err != nil {
			return false, err
		}
		if iss == nil {
			continue
		}
		ok, err := e.Visible(c.Ctx(), iss, c.User)
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

// relationIssuesToID は relation_issues_to_id（params[:relation].require(:issue_to_id) のカンマ区切り。
// 無ければ検証エラーを 1 回返すための [”]）。
func relationIssuesToID(p *httpx.Params) []string {
	v, ok := p.Lookup("relation", "issue_to_id")
	if !ok || v == nil {
		return []string{""}
	}
	switch x := v.(type) {
	case string:
		if strings.TrimSpace(x) == "" {
			return []string{""}
		}
		var out []string
		for _, s := range strings.Split(x, ",") {
			if strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	case []any:
		if len(x) == 0 {
			return []string{""}
		}
		out := make([]string, len(x))
		for i, e := range x {
			out[i] = httpx.ValueString(e)
		}
		return out
	default:
		return []string{httpx.ValueString(x)}
	}
}

// relationErrors は IssueRelation#errors（human_attribute_name は issue_relation のモデル名で引く）。
func relationErrors(r *issues.Relation) *validation.Errors {
	errs := validation.New("issue_relation")
	for _, x := range r.Errors.List {
		if x.Message != "" {
			errs.AddMessage(x.Attr, x.Message)
			continue
		}
		var vars []any
		for k, v := range x.Vars {
			vars = append(vars, k, v)
		}
		errs.Add(x.Attr, x.Key, vars...)
	}
	return errs
}

// IssueRelationsCreate は issue_relations#create。
func (a *App) IssueRelationsCreate(c *Req) {
	ctx := c.Ctx()
	row := c.currentIssue()
	p := c.Params()
	relType, _ := p.Lookup("relation", "relation_type")
	delay, _ := p.Lookup("relation", "delay")
	// 各関連は Redmine と同じく個別のトランザクションで保存する（失敗した関連だけが保存されない）
	e := a.writeIssuesEnv(c, a.DB)
	from, err := e.Find(ctx, row.ID)
	if err != nil || from == nil {
		a.internalError(c, "relation issue", err)
		return
	}
	var (
		saved    bool
		last     *issues.Relation
		unsaved  []*issues.Relation
		toNotify []*issues.SaveResult
	)
	for _, id := range relationIssuesToID(p) {
		r, err := e.NewRelation(ctx, from, issues.RelationParams{IssueToID: id,
			RelationType: httpx.ValueString(relType), Delay: httpx.ValueString(delay)}, c.User)
		if err != nil {
			a.internalError(c, "new relation", err)
			return
		}
		if err := e.InitRelationJournals(ctx, r, c.User); err != nil {
			a.internalError(c, "relation journals", err)
			return
		}
		ok, res, err := e.CreateRelation(ctx, r)
		if repository.IsUniqueViolation(err) {
			// rescue ActiveRecord::RecordNotUnique（反転後の関連が既にある）→ errors.add :base, :taken
			r.Errors.Add("base", "taken", nil)
			ok, err = false, nil
		}
		if err != nil {
			a.internalError(c, "create relation", err)
			return
		}
		saved, last = ok, r
		if ok {
			toNotify = append(toNotify, res)
		} else {
			unsaved = append(unsaved, r)
		}
	}
	a.dispatchIssueNotifications(c, toNotify...)

	switch f := formatOf(c); {
	case f == "json" || f == "xml":
		if last == nil {
			a.internalError(c, "create relation", errors.New("no relation"))
			return
		}
		if saved {
			c.W.Header().Set("Location", httpx.RequestBaseURL(c.R)+urlroot.Path("/relations/"+strconv.FormatInt(last.ID, 10)))
			c.RenderAPI(http.StatusCreated, func(b apibuilder.Builder) { renderRelationAPI(b, &last.IssueRelation) })
		} else {
			c.RenderValidationErrors(relationErrors(last))
		}
	case f == "js":
		if last == nil {
			a.internalError(c, "create relation", errors.New("no relation"))
			return
		}
		v, err := a.relationsSectionView(c, row.ID)
		if err != nil {
			a.internalError(c, "relations", err)
			return
		}
		form := &relationFormModel{persisted: saved, relationType: last.RelationType}
		if last.Delay != nil {
			form.delay = *last.Delay
		} else if !saved {
			form.delay = httpx.ValueString(delay)
		}
		v.relationForm = form
		v.RelationErrorMessages = relationErrorMessages(c, unsaved)
		var ids []string
		for _, r := range unsaved {
			if r.To != nil {
				ids = append(ids, strconv.FormatInt(r.To.ID, 10))
			}
		}
		v.UnsavedRelationsIDs = strings.Join(ids, ", ")
		c.Render("issue_relations/create", map[string]any{"V": v, "Saved": saved}, RenderOptions{Format: "js"})
	case f == "html":
		c.Redirect("/issues/" + strconv.FormatInt(row.ID, 10))
	default:
		renderUnknownFormat(c)
	}
}

// relationErrorMessages は IssueRelationsHelper#relation_error_messages。
func relationErrorMessages(c *Req, relations []*issues.Relation) []string {
	var order []string
	ids := map[string][]string{}
	for _, r := range relations {
		for _, m := range relationErrors(r).FullMessages(c.Loc) {
			if _, ok := ids[m]; !ok {
				order = append(order, m)
				ids[m] = []string{}
			}
			if r.To != nil {
				ids[m] = append(ids[m], strconv.FormatInt(r.To.ID, 10))
			}
		}
	}
	out := make([]string, 0, len(order))
	for _, m := range order {
		if len(ids[m]) == 0 {
			out = append(out, m)
		} else {
			out = append(out, m+": #"+strings.Join(ids[m], ", "))
		}
	}
	return out
}

// relationsSectionView は issues/_relations を描画するためのビュー（@issue.reload と select_relations(@issue)）。
func (a *App) relationsSectionView(c *Req, issueID int64) (*issueShowView, error) {
	r, err := repository.ReadIssue(c.Ctx(), a.DB, issueID)
	if err != nil {
		return nil, err
	}
	row := issueRowFromRead(r)
	l := a.newIssueLookup(c)
	l.addIssues([]*query.IssueRow{row})
	m := l.model(row)
	v := &issueShowView{l: l, M: m}
	v.Relations = l.visibleRelations(m)
	if l.err != nil {
		return nil, l.err
	}
	return v, nil
}

// IssueRelationsDestroy は issue_relations#destroy（raise Unauthorized unless @relation.deletable?）。
func (a *App) IssueRelationsDestroy(c *Req) {
	rel := c.currentRelation()
	e := a.writeIssuesEnv(c, a.DB)
	ok, err := e.RelationDeletable(c.Ctx(), rel, c.User)
	if err != nil {
		a.internalError(c, "relation deletable", err)
		return
	}
	if !ok {
		c.DenyAccess()
		return
	}
	res, err := e.DestroyRelation(c.Ctx(), rel, c.User)
	if err != nil {
		a.internalError(c, "destroy relation", err)
		return
	}
	a.dispatchIssueNotifications(c, res)
	switch f := formatOf(c); f {
	case "html":
		c.Redirect("/issues/" + strconv.FormatInt(rel.IssueFromID, 10))
	case "js":
		// find_issue（params[:issue_id]）
		a.findRelationsIssue(c)
		if c.Halted() {
			return
		}
		v, err := a.relationsSectionView(c, c.currentIssue().ID)
		if err != nil {
			a.internalError(c, "relations", err)
			return
		}
		c.Render("issue_relations/destroy", map[string]any{"V": v, "Relation": rel}, RenderOptions{Format: "js"})
	case "json", "xml":
		c.RenderAPIOK()
	default:
		renderUnknownFormat(c)
	}
}
