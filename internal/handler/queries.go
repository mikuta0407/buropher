// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/apibuilder"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// QueriesController（app/controllers/queries_controller.rb）。menu_item :issues だが
// current_menu_item を上書きしており、@query が無ければ nil（queries#filter や find_query の 404）。
// @query があれば queried_class の複数形（admin レイアウトのクエリでは nil）で、setQueryMenu で上書きする。
var QueriesController = &Controller{Name: "queries", MainMenu: true,
	MenuItem: func(string) string { return helper.NoMenuItem }}

// routesQueries は queries コントローラのルートを登録する。
//
//	resources :projects do
//	  resources :queries, :only => [:new, :create]
//	end
//	resources :queries, :except => [:show]
//	get '/queries/filter', :to => 'queries#filter', :as => 'queries_filter'
func (a *App) routesQueries(r Router) {
	ctrl := QueriesController
	kind := Before(a.queriesKindFilter)
	// before_action :find_optional_project, :only => [:new, :create]
	optProject := FindOptionalProject()
	// before_action :find_query, :only => [:edit, :update, :destroy]
	find := Before(a.findQueryFilter)
	// accept_api_auth :index
	a.Handle(r, http.MethodGet, "/queries", ctrl, "index", a.QueriesIndex, AcceptAPIAuth())
	a.Handle(r, http.MethodPost, "/queries", ctrl, "create", a.QueriesCreate, optProject, kind)
	a.Handle(r, http.MethodGet, "/queries/new", ctrl, "new", a.QueriesNew, optProject, kind)
	a.Handle(r, http.MethodGet, "/queries/filter", ctrl, "filter", a.QueriesFilter)
	a.Handle(r, http.MethodPost, "/projects/{project_id}/queries", ctrl, "create", a.QueriesCreate, optProject, kind)
	a.Handle(r, http.MethodGet, "/projects/{project_id}/queries/new", ctrl, "new", a.QueriesNew, optProject, kind)
	a.Handle(r, http.MethodGet, "/queries/{id}/edit", ctrl, "edit", a.QueriesEdit, find)
	a.Handle(r, http.MethodPatch, "/queries/{id}", ctrl, "update", a.QueriesUpdate, find)
	a.Handle(r, http.MethodPut, "/queries/{id}", ctrl, "update", a.QueriesUpdate, find)
	a.Handle(r, http.MethodDelete, "/queries/{id}", ctrl, "destroy", a.QueriesDestroy, find)
}

// queryClasses は Query.get_subclass の対象（クラス名 → 種別）。
var queryClasses = map[string]query.Kind{
	"IssueQuery":        query.KindIssue,
	"TimeEntryQuery":    query.KindTimeEntry,
	"ProjectQuery":      query.KindProject,
	"ProjectAdminQuery": query.KindProjectAdmin,
	"UserQuery":         query.KindUser,
}

// queryClassName は種別のクラス名（@query.type）。
func queryClassName(k query.Kind) string {
	for n, kk := range queryClasses {
		if kk == k {
			return n
		}
	}
	return ""
}

type queryKindKey struct{}
type queryModelKey struct{}
type queryIntStatusKey struct{}

// queryKindParam は query_class（params[:type] || 'IssueQuery'）。不明なクラスなら false
// （Redmine は nil.new で 500 になるが、buropher は 404 にする）。
func queryKindParam(c *Req) (query.Kind, bool) {
	t := c.Params().String("type")
	if t == "" {
		t = "IssueQuery"
	}
	k, ok := queryClasses[t]
	return k, ok
}

// queriesKindFilter は query_class を決める（不明なら 404）。
func (a *App) queriesKindFilter(c *Req) {
	k, ok := queryKindParam(c)
	if !ok {
		c.Render404("")
		return
	}
	// 管理画面のクエリ（ユーザー・プロジェクト管理）は管理者に限る
	if (k == query.KindUser || k == query.KindProjectAdmin) && !c.User.IsAdmin() {
		c.DenyAccess()
		return
	}
	c.setValue(queryKindKey{}, k)
}

// findQueryFilter は find_query（Query.find → @project = @query.project、編集できなければ 403）。
func (a *App) findQueryFilter(c *Req) {
	id, ok := c.Params().IntStrict("id")
	if !ok {
		c.Render404("")
		return
	}
	env, err := a.queryEnv(c)
	if err != nil {
		a.internalError(c, "query env", err)
		return
	}
	q, err := query.Load(c.Ctx(), env, id, "")
	if err != nil {
		a.internalError(c, "find query", err)
		return
	}
	if q == nil {
		c.Render404("")
		return
	}
	c.Project = q.Project
	// render_403 でも current_menu_item / current_menu は @query で決まる
	setQueryMenu(c, q.Kind)
	ok, err = q.EditableBy(c.Ctx(), c.Authz())
	if err != nil {
		a.internalError(c, "query editable", err)
		return
	}
	if !ok {
		c.Render403("")
		return
	}
	c.setValue(queryModelKey{}, q)
}

// queryLayout は Query#layout（ProjectAdminQuery / UserQuery は admin）。
func queryLayout(k query.Kind) string {
	if k == query.KindProjectAdmin || k == query.KindUser {
		return "admin"
	}
	return "base"
}

// queryMenuItem は current_menu_item（queried_class.to_s.underscore.pluralize）。
func queryMenuItem(k query.Kind) string {
	switch k {
	case query.KindIssue:
		return "issues"
	case query.KindTimeEntry:
		return "time_entries"
	case query.KindProject:
		return "projects"
	}
	return helper.NoMenuItem
}

// setQueryMenu は @query に応じた current_menu_item と current_menu（admin レイアウトならメインメニューなし）。
func setQueryMenu(c *Req, k query.Kind) {
	ctrl := *c.Controller
	item := queryMenuItem(k)
	if queryLayout(k) == "admin" {
		item = helper.NoMenuItem
		ctrl.NoCurrentMenu = true
	}
	ctrl.MenuItem = func(string) string { return item }
	c.Controller = &ctrl
}

// QueriesIndex は queries#index（API のみ。HTML は 406）。
func (a *App) QueriesIndex(c *Req) {
	k, ok := queryKindParam(c)
	if !ok {
		c.Render404("")
		return
	}
	if !httpx.IsAPIRequest(c.R) {
		// respond_to { format.html { render_error :status => 406 } }
		c.PerPageOption()
		c.RenderError(http.StatusNotAcceptable, "")
		return
	}
	offset, limit := c.APIOffsetAndLimit()
	list, total, err := query.ListVisiblePage(c.Ctx(), c.Authz(), k, offset, limit)
	if err != nil {
		a.internalError(c, "queries index", err)
		return
	}
	c.RenderAPI(0, func(b apibuilder.Builder) {
		b.Array("queries", c.APIMeta(apibuilder.A("total_count", total, "offset", offset, "limit", limit)), func() {
			for _, q := range list {
				b.Object("query", func() {
					b.Value("id", q.ID)
					b.Value("name", q.Name)
					b.Value("is_public", q.Visibility != query.VisibilityPrivate)
					if q.ProjectID != nil {
						b.Value("project_id", *q.ProjectID)
					} else {
						b.Value("project_id", nil)
					}
				})
			}
		})
	})
}

// newFormQuery は query_class.new（user = User.current、project = @project）。名前の既定値は ""（queries.name の既定値）。
func (a *App) newFormQuery(c *Req) (*query.Query, error) {
	k, _ := c.value(queryKindKey{}).(query.Kind)
	if k == "" {
		k = query.KindIssue
	}
	env, err := a.queryEnv(c)
	if err != nil {
		return nil, err
	}
	q, err := query.New(c.Ctx(), env, k, c.Project)
	if err != nil {
		return nil, err
	}
	q.Name = ""
	if c.User.Logged() {
		uid := c.User.ID
		q.UserID = &uid
	}
	return q, nil
}

// QueriesNew は queries#new。
func (a *App) QueriesNew(c *Req) {
	q, err := a.newFormQuery(c)
	if err != nil {
		a.internalError(c, "new query", err)
		return
	}
	qp := queryParams(c)
	if err := q.BuildFromParams(c.Ctx(), qp, nil); err != nil {
		a.internalError(c, "query params", err)
		return
	}
	if q.Kind == query.KindUser && !qp.HasFields && qp.Short["status"] == "" {
		c.setValue(queryIntStatusKey{}, true)
	}
	a.renderQueryForm(c, "new", q, nil, http.StatusOK, true)
}

// QueriesCreate は queries#create。
func (a *App) QueriesCreate(c *Req) {
	q, err := a.newFormQuery(c)
	if err != nil {
		a.internalError(c, "new query", err)
		return
	}
	if err := a.updateQueryFromParams(c, q); err != nil {
		a.internalError(c, "query params", err)
		return
	}
	if msgs, ok := a.saveQuery(c, q); !ok {
		// render :action => 'new', :layout => !request.xhr?
		a.renderQueryForm(c, "new", q, msgs, http.StatusOK, !httpx.IsXHR(c.R))
		return
	} else if msgs != nil {
		return
	}
	c.Flash().SetNotice(c.L("notice_successful_create"))
	a.redirectToQueryItems(c, q, url.Values{"query_id": {strconv.FormatInt(q.ID, 10)}})
}

// QueriesEdit は queries#edit。
func (a *App) QueriesEdit(c *Req) {
	q := c.value(queryModelKey{}).(*query.Query)
	a.renderQueryForm(c, "edit", q, nil, http.StatusOK, true)
}

// QueriesUpdate は queries#update。
func (a *App) QueriesUpdate(c *Req) {
	q := c.value(queryModelKey{}).(*query.Query)
	if err := a.updateQueryFromParams(c, q); err != nil {
		a.internalError(c, "query params", err)
		return
	}
	if msgs, ok := a.saveQuery(c, q); !ok {
		a.renderQueryForm(c, "edit", q, msgs, http.StatusOK, true)
		return
	} else if msgs != nil {
		return
	}
	c.Flash().SetNotice(c.L("notice_successful_update"))
	a.redirectToQueryItems(c, q, url.Values{"query_id": {strconv.FormatInt(q.ID, 10)}})
}

// QueriesDestroy は queries#destroy。
func (a *App) QueriesDestroy(c *Req) {
	q := c.value(queryModelKey{}).(*query.Query)
	if err := q.Delete(c.Ctx()); err != nil {
		a.internalError(c, "destroy query", err)
		return
	}
	a.redirectToQueryItems(c, q, url.Values{"set_filter": {"1"}})
}

// saveQuery は @query.save。検証エラーなら (メッセージ, false)、内部エラーで応答済みなら (非 nil, true)。
func (a *App) saveQuery(c *Req, q *query.Query) ([]string, bool) {
	err := q.Save(c.Ctx())
	var inv *query.ErrInvalid
	switch {
	case err == nil:
		return nil, true
	case errors.As(err, &inv):
		return inv.Messages, false
	default:
		a.internalError(c, "save query", err)
		return []string{}, true
	}
}

// updateQueryFromParams は update_query_from_params。
func (a *App) updateQueryFromParams(c *Req, q *query.Query) error {
	p := c.Params()
	if p.Present("query_is_for_all") {
		q.SetProject(nil)
	} else {
		q.SetProject(c.Project)
	}
	if err := q.BuildFromParams(c.Ctx(), queryParams(c), nil); err != nil {
		return err
	}
	if p.Present("default_columns") {
		if err := q.SetColumnNames(c.Ctx(), nil); err != nil {
			return err
		}
	}
	// @query.sort_criteria = params[:query][:sort_criteria] || @query.sort_criteria
	if sc, ok := querySortCriteriaParam(p); ok {
		q.SetSortCriteria(sc)
	}
	q.Name = p.String("query", "name")
	q.Description = p.String("query", "description")
	if c.User.IsAdmin() || (q.Project != nil && c.AllowedTo(domain.Perm("manage_public_queries"), q.Project)) {
		q.Visibility = query.VisibilityPrivate
		if v, ok := p.StringOK("query", "visibility"); ok {
			q.Visibility = int(rubyStringToI(v))
		}
		q.RoleIDs = nil
		for _, s := range p.Strings("query", "role_ids") {
			if strings.TrimSpace(s) == "" {
				continue
			}
			if id := rubyStringToI(s); !slices.Contains(q.RoleIDs, id) {
				q.RoleIDs = append(q.RoleIDs, id)
			}
		}
	} else {
		q.Visibility = query.VisibilityPrivate
	}
	return nil
}

// querySortCriteriaParam は params[:query][:sort_criteria]（{"0" => [key, order], ...} の値を送信順に、
// または "priority:desc,id" 形式の文字列）。
func querySortCriteriaParam(p *httpx.Params) (query.SortCriteria, bool) {
	v, ok := p.Lookup("query", "sort_criteria")
	if !ok || v == nil {
		return nil, false
	}
	if s, ok := v.(string); ok {
		return query.ParseSortCriteria(s), true
	}
	m := p.Map("query", "sort_criteria")
	if m == nil {
		return nil, false
	}
	// SortCriteria.new(hash) は hash.values（送信された順。キーでは並べ替えない）
	var sc query.SortCriteria
	for _, k := range m.Keys() {
		vals := m.Strings(k)
		var pair [2]string
		if len(vals) > 0 {
			pair[0] = vals[0]
		}
		if len(vals) > 1 {
			pair[1] = vals[len(vals)-1]
		}
		sc = append(sc, pair)
	}
	return sc, true
}

// redirectToQueryItems は redirect_to_items（クエリの種類ごとの一覧へ。IssueQuery は gantt / calendar も見る）。
// project は @project（create は params[:project_id]、edit 系は保存前の @query.project）。
func (a *App) redirectToQueryItems(c *Req, q *query.Query, opts url.Values) {
	qs := ""
	if len(opts) > 0 {
		qs = "?" + railsToQuery(opts)
	}
	prefix := ""
	if c.Project != nil {
		prefix = "/projects/" + c.Project.Identifier
	}
	p := c.Params()
	var path string
	switch q.Kind {
	case query.KindIssue:
		switch {
		case p.Present("gantt"):
			path = prefix + "/issues/gantt"
		case p.Present("calendar"):
			path = prefix + "/issues/calendar"
		default:
			path = prefix + "/issues"
		}
	case query.KindTimeEntry:
		path = prefix + "/time_entries"
	case query.KindProject:
		path = "/projects"
	case query.KindProjectAdmin:
		path = "/admin/projects"
	case query.KindUser:
		path = "/users"
	}
	c.Redirect(path + qs)
}

// QueriesFilter は queries#filter（フィルタの選択肢を JSON で返す）。
func (a *App) QueriesFilter(c *Req) {
	k, ok := queryKindParam(c)
	if !ok {
		c.Render404("")
		return
	}
	env, err := a.queryEnv(c)
	if err != nil {
		a.internalError(c, "query env", err)
		return
	}
	var project *domain.Project
	if pid := c.Params().String("project_id"); strings.TrimSpace(pid) != "" {
		pr, err := c.lookupProject(pid)
		if errors.Is(err, repository.ErrNotFound) {
			c.Render404("")
			return
		} else if err != nil {
			a.internalError(c, "query filter project", err)
			return
		}
		project = pr
	}
	q, err := query.New(c.Ctx(), env, k, project)
	if err != nil {
		a.internalError(c, "query filter", err)
		return
	}
	// User.current.allowed_to?(q.class.view_permission, q.project, :global => true) でなければ Unauthorized
	perm := domain.Perm(q.ViewPermission())
	allowed := false
	if project != nil {
		allowed = c.AllowedTo(perm, project)
	} else {
		allowed = c.AllowedToGlobally(perm)
	}
	if !allowed {
		c.DenyAccess()
		return
	}
	af, err := q.AvailableFilters(c.Ctx())
	if err != nil {
		a.internalError(c, "query filter", err)
		return
	}
	var out any = []any{}
	if def := af.Get(c.Params().String("name")); def != nil {
		vals, err := def.LoadValues(c.Ctx())
		if err != nil {
			a.internalError(c, "query filter values", err)
			return
		}
		out = filterValuesJSON(def, vals)
	}
	renderJSON(c, out)
}

// filterValuesJSON は filter.values の JSON 表現（list 形式のカスタムフィールドは文字列の配列、
// それ以外は [label, value] / [label, value, group] の配列。値が無ければ nil）。
func filterValuesJSON(def *query.FilterDef, vals []query.Option) any {
	if vals == nil {
		return nil
	}
	if def.CustomField != nil && def.CustomField.FieldFormat == "list" {
		// ListFormat#possible_values_options は文字列の配列（[label, value] ではない）
		arr := make([]string, len(vals))
		for i, v := range vals {
			arr[i] = v.Value
		}
		return arr
	}
	arr := make([][]string, len(vals))
	for i, v := range vals {
		if v.Group != "" {
			arr[i] = []string{v.Label, v.Value, v.Group}
		} else {
			arr[i] = []string{v.Label, v.Value}
		}
	}
	return arr
}

// ---------------------------------------------------------------- フォーム（queries/_form）

// queryFormView は queries/new・edit・_form の値。
type queryFormView struct {
	QV *queryView
	c  *Req
	// Errors は error_messages_for 'query'（保存に失敗した場合のみ）。
	Errors []string
	// Action はフォームの送信先。
	Action string
	// Gantt / Calendar は params[:gantt] / params[:calendar]。
	Gantt, Calendar bool
	// CanSetVisibility は User.current.admin? || allowed_to?(:manage_public_queries, @query.project)。
	CanSetVisibility bool
	// Roles は Role.givable.sorted。
	Roles []*domain.Role
	// NameSet / DescriptionSet は name / description が nil でない（text_field が value 属性を出す）。
	NameSet, DescriptionSet bool
}

// renderQueryForm は queries/new または edit を描画する。
func (a *App) renderQueryForm(c *Req, action string, q *query.Query, errs []string, status int, layout bool) {
	setQueryMenu(c, q.Kind)
	path := ""
	qv, err := a.newQueryView(c, q, queryClassName(q.Kind), path)
	if err != nil {
		a.internalError(c, "query view", err)
		return
	}
	qv.IntDefaultStatus, _ = c.value(queryIntStatusKey{}).(bool)
	f := &queryFormView{QV: qv, c: c, Errors: errs}
	if action == "new" {
		f.Action = "/queries"
		if c.Project != nil {
			f.Action = "/projects/" + c.Project.Identifier + "/queries"
		}
	} else {
		f.Action = "/queries/" + strconv.FormatInt(q.ID, 10)
	}
	// 新規・保存済みの name は "" 以上（queries.name の既定値）。description は保存値が空なら nil。
	// create / update では params[:query][:name] / [:description] がそのまま代入される（無ければ nil）。
	f.NameSet, f.DescriptionSet = true, q.Description != ""
	if c.R.Method != http.MethodGet {
		_, f.NameSet = c.Params().StringOK("query", "name")
		_, f.DescriptionSet = c.Params().StringOK("query", "description")
	}
	f.Gantt = c.Params().Present("gantt")
	f.Calendar = c.Params().Present("calendar")
	f.CanSetVisibility = c.User.IsAdmin() || (q.Project != nil && c.AllowedTo(domain.Perm("manage_public_queries"), q.Project))
	if f.CanSetVisibility && !f.IsProjectQuery() {
		roles, err := repository.GivableRoles(c.Ctx(), a.DB)
		if err != nil {
			a.internalError(c, "givable roles", err)
			return
		}
		f.Roles = roles
	}
	opts := RenderOptions{Status: status}
	if !layout {
		opts.Layout = viewNoLayout
	} else if queryLayout(q.Kind) == "admin" {
		opts.Layout = "admin"
	}
	c.Render("queries/"+action, map[string]any{"F": f, "QV": qv}, opts)
}

// IsProjectQuery は @query.is_a?(ProjectQuery)（ProjectAdminQuery を含む）。
func (f *queryFormView) IsProjectQuery() bool {
	return f.QV.Q.Kind == query.KindProject || f.QV.Q.Kind == query.KindProjectAdmin
}

// NameField は text_field 'query', 'name', :size => 80（nil なら value なし）。
func (f *queryFormView) NameField() template.HTML {
	var v any
	if f.NameSet {
		v = f.QV.Q.Name
	}
	return rails.Tag("input", rails.NewHash("size", 80, "type", "text", "value", v, "name", "query[name]", "id", "query_name"))
}

// DescriptionField は text_field 'query', 'description', :size => 80（nil なら value なし）。
func (f *queryFormView) DescriptionField() template.HTML {
	var v any
	if f.DescriptionSet {
		v = f.QV.Q.Description
	}
	return rails.Tag("input", rails.NewHash("size", 80, "type", "text", "value", v, "name", "query[description]", "id", "query_description"))
}

// VisibilityRadio は radio_button 'query', 'visibility', v。
func (f *queryFormView) VisibilityRadio(v int) template.HTML {
	return rails.Tag("input", rails.NewHash("type", "radio", "value", v, "checked", f.QV.Q.Visibility == v,
		"name", "query[visibility]", "id", "query_visibility_"+strconv.Itoa(v)))
}

// RoleCheckBox は check_box_tag 'query[role_ids][]', role.id, @query.roles.include?(role), :id => nil。
func (f *queryFormView) RoleCheckBox(r *domain.Role) template.HTML {
	return rails.CheckBoxTag("query[role_ids][]", r.ID, slices.Contains(f.QV.Q.RoleIDs, r.ID), rails.NewHash("id", nil))
}

// IsForAllCheckBox は check_box_tag 'query_is_for_all', 1, @query.project.nil?, :disabled => ..., :class => ...。
func (f *queryFormView) IsForAllCheckBox() template.HTML {
	class := "disable-unless-private"
	if f.c.User.IsAdmin() {
		class = ""
	}
	q := f.QV.Q
	return rails.CheckBoxTag("query_is_for_all", 1, q.Project == nil,
		rails.NewHash("disabled", q.ID != 0 && q.Project == nil, "class", class))
}

// DisplayTypesSize は @query.available_display_types.size。
func (f *queryFormView) DisplayTypesSize() int { return len(f.QV.Q.AvailableDisplayTypes()) }

// SingleDisplayTypeField は hidden_field_tag 'query[display_type]', @query.available_display_types.first。
func (f *queryFormView) SingleDisplayTypeField() template.HTML {
	return rails.HiddenFieldTag("query[display_type]", f.QV.Q.AvailableDisplayTypes()[0], nil)
}

// DefaultColumnsCheckBox は check_box_tag 'default_columns', 1, @query.has_default_columns?, ...。
func (f *queryFormView) DefaultColumnsCheckBox() template.HTML {
	return rails.CheckBoxTag("default_columns", 1, f.QV.Q.HasDefaultColumns(),
		rails.NewHash("id", "query_default_columns", "data", rails.NewHash("disables", "#columns, .block_columns input")))
}

// GroupBySelect は select 'query', 'group_by', groupable_columns, :include_blank => true。
func (f *queryFormView) GroupBySelect() (template.HTML, error) {
	cols, err := f.QV.Q.GroupableColumns(f.QV.ctx)
	if err != nil {
		return "", err
	}
	var items []any
	for _, col := range cols {
		items = append(items, []any{f.QV.Caption(col), col.Name})
	}
	var sel any
	if f.QV.Q.GroupBy != "" {
		sel = f.QV.Q.GroupBy
	}
	opts := template.HTML(`<option value="" label=" "></option>` + "\n")
	opts += rails.OptionsForSelect(items, sel)
	return rails.ContentTag("select", opts, rails.NewHash("name", "query[group_by]", "id", "query_group_by")), nil
}

// DrawCheckBox は gantt の check_box_tag "query[draw_xxx]", "1", @query.draw_xxx（data 属性付き可）。
func (f *queryFormView) DrawCheckBox(name string) template.HTML {
	q := f.QV.Q
	var checked bool
	opts := rails.NewHash()
	switch name {
	case "draw_relations":
		checked = q.DrawRelations()
	case "draw_progress_line":
		checked = q.DrawProgressLine()
	case "draw_selected_columns":
		checked = q.DrawSelectedColumns()
		opts.Set("data", rails.NewHash("enables", "span.query-columns select, span.query-columns input"))
	}
	return rails.CheckBoxTag("query["+name+"]", "1", checked, opts)
}

// SortIndexes は 3.times。
func (f *queryFormView) SortIndexes() []int { return []int{0, 1, 2} }

// SortAttributeSelect は sort_criteria の i 番目の列の select。
func (f *queryFormView) SortAttributeSelect(i int) (template.HTML, error) {
	cols, err := f.QV.Q.AvailableColumns(f.QV.ctx)
	if err != nil {
		return "", err
	}
	items := []any{[]any{}}
	for _, col := range cols {
		if col.IsSortable() {
			items = append(items, []any{f.QV.Caption(col), col.Name})
		}
	}
	var sel any
	if sc := f.QV.Q.SortCriteria(); i < len(sc) {
		sel = sc[i][0]
	}
	return rails.SelectTag("query[sort_criteria]["+strconv.Itoa(i)+"][]", rails.OptionsForSelect(items, sel),
		rails.NewHash("id", "query_sort_criteria_attribute_"+strconv.Itoa(i))), nil
}

// SortDirectionSelect は sort_criteria の i 番目の向きの select。
func (f *queryFormView) SortDirectionSelect(i int) template.HTML {
	items := []any{[]any{}, []any{f.c.L("label_ascending"), "asc"}, []any{f.c.L("label_descending"), "desc"}}
	var sel any
	if sc := f.QV.Q.SortCriteria(); i < len(sc) {
		sel = sc[i][1]
	}
	return rails.SelectTag("query[sort_criteria]["+strconv.Itoa(i)+"][]", rails.OptionsForSelect(items, sel),
		rails.NewHash("id", "query_sort_criteria_direction_"+strconv.Itoa(i)))
}
