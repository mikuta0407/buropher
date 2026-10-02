package handler

// QueriesController（app/controllers/queries_controller.rb）: 保存クエリの作成・編集・削除と、
// フィルタ UI が remote なフィルタの選択肢を取得する queries#filter。

import (
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// QueriesController（current_menu_item は @query の種類で決まる。@query が無ければ nil）。
var QueriesController = &Controller{Name: "queries", MainMenu: true,
	MenuItem: func(string) string { return helper.NoMenuItem }}

// routesQueries は queries コントローラのルートを登録する。
//
//	resources :projects { resources :queries, :only => [:new, :create] }
//	resources :queries, :except => [:show]
//	get '/queries/filter', :to => 'queries#filter', :as => 'queries_filter'
func (a *App) routesQueries(r Router) {
	ctrl := QueriesController
	// before_action :find_query, :only => [:edit, :update, :destroy]
	// before_action :find_optional_project, :only => [:new, :create]
	find := Before(a.findQueryFilter)
	a.Handle(r, http.MethodGet, "/queries/filter", ctrl, "filter", a.QueriesFilter)
	a.Handle(r, http.MethodGet, "/projects/{project_id}/queries/new", ctrl, "new", a.QueriesNew, FindOptionalProject())
	a.Handle(r, http.MethodPost, "/projects/{project_id}/queries", ctrl, "create", a.QueriesCreate, FindOptionalProject())
	a.Handle(r, http.MethodGet, "/queries/new", ctrl, "new", a.QueriesNew, FindOptionalProject())
	a.Handle(r, http.MethodPost, "/queries", ctrl, "create", a.QueriesCreate, FindOptionalProject())
	a.Handle(r, http.MethodGet, "/queries/{id}/edit", ctrl, "edit", a.QueriesEdit, find)
	a.Handle(r, http.MethodPatch, "/queries/{id}", ctrl, "update", a.QueriesUpdate, find)
	a.Handle(r, http.MethodPut, "/queries/{id}", ctrl, "update", a.QueriesUpdate, find)
	a.Handle(r, http.MethodDelete, "/queries/{id}", ctrl, "destroy", a.QueriesDestroy, find)
}

// queryClasses は Query.get_subclass が受け付けるクラス名と種別。
var queryClasses = map[string]query.Kind{
	"IssueQuery":        query.KindIssue,
	"TimeEntryQuery":    query.KindTimeEntry,
	"ProjectQuery":      query.KindProject,
	"ProjectAdminQuery": query.KindProjectAdmin,
	"UserQuery":         query.KindUser,
}

// queryClassName は種別のクラス名。
func queryClassName(k query.Kind) string {
	for name, kind := range queryClasses {
		if kind == k {
			return name
		}
	}
	return "IssueQuery"
}

// queryClassFromParams は query_class（params[:type] || 'IssueQuery'）。未知の型は ok = false。
func queryClassFromParams(c *Req) (query.Kind, bool) {
	t := c.Params().String("type")
	if t == "" {
		t = "IssueQuery"
	}
	k, ok := queryClasses[t]
	return k, ok
}

// queryLayoutAdmin は Query#layout が 'admin'（ProjectAdminQuery / UserQuery）。
func queryLayoutAdmin(k query.Kind) bool {
	return k == query.KindProjectAdmin || k == query.KindUser
}

// queryMenuItem は current_menu_item（queried_class の複数形）。
func queryMenuItem(k query.Kind) string {
	switch k {
	case query.KindTimeEntry:
		return "time_entries"
	case query.KindProject, query.KindProjectAdmin:
		return "projects"
	case query.KindUser:
		return "users"
	}
	return "issues"
}

// setQueryMenu は current_menu_item / current_menu を設定する（admin レイアウトでは
// current_menu_item は nil、メインメニューも出さない）。
func (c *Req) setQueryMenu(k query.Kind) {
	ctrl := *c.Controller
	item := queryMenuItem(k)
	if queryLayoutAdmin(k) {
		item = helper.NoMenuItem
		ctrl.MainMenu = false
	}
	ctrl.MenuItem = func(string) string { return item }
	c.Controller = &ctrl
}

const ctxSavedQuery = "saved_query"

// findQueryFilter は find_query（Query.find、@project = @query.project、編集できなければ 403）。
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
		a.internalError(c, "load query", err)
		return
	}
	if q == nil {
		c.Render404("")
		return
	}
	c.Project = q.Project
	c.setQueryMenu(q.Kind)
	ok, err = q.EditableBy(c.Ctx(), c.Authz())
	if err != nil {
		a.internalError(c, "query editable", err)
		return
	}
	if !ok {
		c.Render403("")
		return
	}
	c.setLocal(ctxSavedQuery, q)
}

// newQueryOf は query_class.new（名前は空・既定フィルタ付き）に user / project を設定したもの。
func (a *App) newQueryOf(c *Req, kind query.Kind) (*query.Query, error) {
	env, err := a.queryEnv(c)
	if err != nil {
		return nil, err
	}
	q, err := query.New(c.Ctx(), env, kind, c.Project)
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
	kind, ok := queryClassFromParams(c)
	if !ok {
		c.Render404("")
		return
	}
	q, err := a.newQueryOf(c, kind)
	if err != nil {
		a.internalError(c, "new query", err)
		return
	}
	p := queryParams(c)
	if err := q.BuildFromParams(c.Ctx(), p, nil); err != nil {
		a.queryFailed(c, err)
		return
	}
	a.renderQueryForm(c, q, "queries/new", http.StatusOK, kind == query.KindUser && !p.HasFields && p.Short["status"] == "")
}

// QueriesCreate は queries#create。
func (a *App) QueriesCreate(c *Req) {
	kind, ok := queryClassFromParams(c)
	if !ok {
		c.Render404("")
		return
	}
	q, err := a.newQueryOf(c, kind)
	if err != nil {
		a.internalError(c, "new query", err)
		return
	}
	if err := a.updateQueryFromParams(c, q); err != nil {
		a.queryFailed(c, err)
		return
	}
	if a.saveQuery(c, q, "notice_successful_create") {
		a.redirectToQueryItems(c, q, url.Values{"query_id": {strconv.FormatInt(q.ID, 10)}})
		return
	}
	a.renderQueryForm(c, q, "queries/new", http.StatusOK, false)
}

// QueriesEdit は queries#edit。
func (a *App) QueriesEdit(c *Req) {
	q, _ := c.local(ctxSavedQuery).(*query.Query)
	a.renderQueryForm(c, q, "queries/edit", http.StatusOK, false)
}

// QueriesUpdate は queries#update。
func (a *App) QueriesUpdate(c *Req) {
	q, _ := c.local(ctxSavedQuery).(*query.Query)
	if err := a.updateQueryFromParams(c, q); err != nil {
		a.queryFailed(c, err)
		return
	}
	if a.saveQuery(c, q, "notice_successful_update") {
		a.redirectToQueryItems(c, q, url.Values{"query_id": {strconv.FormatInt(q.ID, 10)}})
		return
	}
	a.renderQueryForm(c, q, "queries/edit", http.StatusOK, false)
}

// QueriesDestroy は queries#destroy。
func (a *App) QueriesDestroy(c *Req) {
	q, _ := c.local(ctxSavedQuery).(*query.Query)
	if err := q.Delete(c.Ctx()); err != nil {
		a.internalError(c, "delete query", err)
		return
	}
	a.redirectToQueryItems(c, q, url.Values{"set_filter": {"1"}})
}

// saveQuery は @query.save（成功したら flash を設定して true）。検証エラーは false（フォームに表示される）。
func (a *App) saveQuery(c *Req, q *query.Query, notice string) bool {
	err := q.Save(c.Ctx())
	var inv *query.ErrInvalid
	switch {
	case errors.As(err, &inv):
		return false
	case err != nil:
		a.internalError(c, "save query", err)
		return false
	}
	c.Flash().SetNotice(c.L(notice))
	return true
}

var querySortCriteriaKeyRe = regexp.MustCompile(`^query\[sort_criteria\]\[([^\]]*)\]\[\]$`)

// updateQueryFromParams は QueriesController#update_query_from_params。
func (a *App) updateQueryFromParams(c *Req, q *query.Query) error {
	ctx := c.Ctx()
	p := c.Params()
	if p.Present("query_is_for_all") {
		q.SetProject(nil)
	} else {
		q.SetProject(c.Project)
	}
	if err := q.BuildFromParams(ctx, queryParams(c), nil); err != nil {
		return err
	}
	if p.Present("default_columns") {
		if err := q.SetColumnNames(ctx, nil); err != nil {
			return err
		}
	}
	// @query.sort_criteria = params[:query][:sort_criteria]（{"0" => [attr, dir], ...}）|| @query.sort_criteria
	form := railsParamValues(c)
	type crit struct {
		key  string
		pair [2]string
	}
	var crits []crit
	for k, vs := range form {
		m := querySortCriteriaKeyRe.FindStringSubmatch(k)
		if m == nil {
			continue
		}
		var pair [2]string
		if len(vs) > 0 {
			pair[0] = vs[0]
		}
		if len(vs) > 1 {
			pair[1] = vs[1]
		}
		crits = append(crits, crit{m[1], pair})
	}
	if len(crits) > 0 {
		// Hash#keys.sort（文字列としての順序）
		sort.Slice(crits, func(i, j int) bool { return crits[i].key < crits[j].key })
		sc := make(query.SortCriteria, len(crits))
		for i, cr := range crits {
			sc[i] = cr.pair
		}
		q.SetSortCriteria(sc)
	}
	qp := p.Map("query")
	q.Name, q.Description = "", ""
	if qp != nil {
		q.Name = qp.String("name")
		q.Description = qp.String("description")
	}
	if c.User.IsAdmin() || c.allowedToProjectOrNil("manage_public_queries", q.Project) {
		q.Visibility = query.VisibilityPrivate
		if qp != nil && qp.Present("visibility") {
			q.Visibility = int(httpx.RubyToI(qp.String("visibility")))
		}
		q.RoleIDs = nil
		if qp != nil {
			for _, s := range qp.Strings("role_ids") {
				if id, ok := parseRoleID(s); ok {
					q.RoleIDs = append(q.RoleIDs, id)
				}
			}
		}
	} else {
		q.Visibility = query.VisibilityPrivate
	}
	return nil
}

func parseRoleID(s string) (int64, bool) {
	id, err := strconv.ParseInt(s, 10, 64)
	return id, err == nil && id > 0
}

// allowedToProjectOrNil は User.current.allowed_to?(perm, project)（project が nil なら false）。
func (c *Req) allowedToProjectOrNil(perm string, p *domain.Project) bool {
	if p == nil {
		return false
	}
	return c.AllowedTo(domain.Perm(perm), p)
}

// redirectToQueryItems は redirect_to_items（クエリの種類ごとの一覧へ）。
func (a *App) redirectToQueryItems(c *Req, q *query.Query, opts url.Values) {
	p := c.Project
	var path string
	switch q.Kind {
	case query.KindIssue:
		switch {
		case c.Params().Present("gantt"):
			path = ganttPath(p)
		case c.Params().Present("calendar"):
			if p != nil {
				path = "/projects/" + p.Identifier + "/issues/calendar"
			} else {
				path = "/issues/calendar"
			}
		default:
			path = issuesPath(p)
		}
	case query.KindTimeEntry:
		path = teTimeEntriesPath(p)
	case query.KindProject:
		path = "/projects"
	case query.KindProjectAdmin:
		path = "/admin/projects"
	case query.KindUser:
		path = "/users"
	}
	c.Redirect(path + "?" + opts.Encode())
}

// queryFormView は queries/_form の描画用データ。
type queryFormView struct {
	qv *queryView
	c  *Req
	// Q は @query。
	Q *query.Query
	// NameSet / DescriptionSet は name / description が nil でない（text_field が value 属性を出す）。
	NameSet, DescriptionSet bool
	// ClassName は @query.class.name。
	ClassName string
	// CanManagePublic は User.current.admin? || allowed_to?(:manage_public_queries, @query.project)。
	CanManagePublic bool
	IsAdmin         bool
	Roles           []*domain.Role
	Gantt, Calendar bool
}

// IsProjectQuery は @query.is_a?(ProjectQuery)（ProjectAdminQuery を含む）。
func (f *queryFormView) IsProjectQuery() bool {
	return f.Q.Kind == query.KindProject || f.Q.Kind == query.KindProjectAdmin
}

// HasRole は @query.roles.include?(role)。
func (f *queryFormView) HasRole(id int64) bool {
	for _, r := range f.Q.RoleIDs {
		if r == id {
			return true
		}
	}
	return false
}

// IsForAll は @query.project.nil?。
func (f *queryFormView) IsForAll() bool { return f.Q.Project == nil }

// IsForAllDisabled は !@query.new_record? && @query.project.nil?。
func (f *queryFormView) IsForAllDisabled() bool { return f.Q.ID != 0 && f.Q.Project == nil }

// SingleDisplayType は available_display_types.size == 1 の場合のその値（それ以外は ""）。
func (f *queryFormView) SingleDisplayType() string {
	if t := f.Q.AvailableDisplayTypes(); len(t) == 1 {
		return t[0]
	}
	return ""
}

// GroupByOptions は select 'query', 'group_by', groupable_columns, :include_blank => true の option 群。
func (f *queryFormView) GroupByOptions() (template.HTML, error) {
	cols, err := f.Q.GroupableColumns(f.qv.ctx)
	if err != nil {
		return "", err
	}
	var items []any
	for _, col := range cols {
		items = append(items, []any{f.qv.Caption(col), col.Name})
	}
	// :include_blank => true は label=" " の空 option（選択状態にはしない）
	return `<option value="" label=" "></option>` + "\n" + rails.OptionsForSelect(items, f.Q.GroupBy), nil
}

// sortCriterion は @query.sort_criteria_key(i) / sort_criteria_order(i)（無ければ nil）。
func (f *queryFormView) sortCriterion(i int) (key, order any) {
	sc := f.Q.SortCriteria()
	if i < len(sc) {
		return sc[i][0], sc[i][1]
	}
	return nil, nil
}

// SortAttributeOptions は [[]] + 並べ替え可能な列の option 群。
func (f *queryFormView) SortAttributeOptions(i int) (template.HTML, error) {
	cols, err := f.Q.AvailableColumns(f.qv.ctx)
	if err != nil {
		return "", err
	}
	items := []any{[]any{}}
	for _, col := range cols {
		if col.IsSortable() {
			items = append(items, []any{f.qv.Caption(col), col.Name})
		}
	}
	key, _ := f.sortCriterion(i)
	return rails.OptionsForSelect(items, key), nil
}

// SortDirectionOptions は [[], [昇順, 'asc'], [降順, 'desc']] の option 群。
func (f *queryFormView) SortDirectionOptions(i int) template.HTML {
	items := []any{[]any{}, []any{f.c.L("label_ascending"), "asc"}, []any{f.c.L("label_descending"), "desc"}}
	_, order := f.sortCriterion(i)
	return rails.OptionsForSelect(items, order)
}

// SortIndexes は 3.times。
func (f *queryFormView) SortIndexes() []int { return []int{0, 1, 2} }

// FormAction は new の form_tag の action（project_queries_path / queries_path）。
func (f *queryFormView) FormAction() string {
	if f.c.Project != nil {
		return "/projects/" + f.c.Project.Identifier + "/queries"
	}
	return "/queries"
}

// renderQueryForm は queries/new / queries/edit を描画する。
// intDefaultStatus は UserQuery の既定フィルタを整数で出す（queries#new で status を指定していない場合）。
func (a *App) renderQueryForm(c *Req, q *query.Query, tmpl string, status int, intDefaultStatus bool) {
	c.setQueryMenu(q.Kind)
	qv, err := a.newQueryView(c, q, queryClassName(q.Kind), "")
	if err != nil {
		a.internalError(c, "query view", err)
		return
	}
	qv.intDefaultStatus = intDefaultStatus
	// error_messages_for 'query' は保存を試みた場合のみ（新規表示では出さない）
	if c.R.Method == http.MethodGet {
		qv.ErrorMessages = nil
	}
	// description は保存値か params[:query][:description] が nil でなければ value を出す
	// （name は新規でも "" になる）
	descSet := q.Description != ""
	if c.R.Method != http.MethodGet {
		_, descSet = c.Params().StringOK("query", "description")
	}
	f := &queryFormView{qv: qv, c: c, Q: q, NameSet: true, DescriptionSet: descSet, ClassName: queryClassName(q.Kind), IsAdmin: c.User.IsAdmin(),
		Gantt: c.Params().Present("gantt"), Calendar: c.Params().Present("calendar")}
	f.CanManagePublic = c.User.IsAdmin() || c.allowedToProjectOrNil("manage_public_queries", q.Project)
	if f.CanManagePublic {
		if f.Roles, err = repository.GivableRoles(c.Ctx(), a.DB); err != nil {
			a.internalError(c, "givable roles", err)
			return
		}
	}
	opts := RenderOptions{Status: status}
	if queryLayoutAdmin(q.Kind) {
		opts.Layout = "admin"
	}
	if httpx.IsXHR(c.R) && tmpl == "queries/new" && c.R.Method == http.MethodPost {
		opts.Layout = view.NoLayout
	}
	c.Render(tmpl, map[string]any{"F": f, "qv": qv, "Project": c.Project}, opts)
}

// QueriesFilter は queries#filter（remote なフィルタの選択肢を JSON で返す）。
func (a *App) QueriesFilter(c *Req) {
	kind, ok := queryClassFromParams(c)
	if !ok {
		c.Render404("")
		return
	}
	var project *domain.Project
	if pid := c.Params().String("project_id"); pid != "" {
		p, err := c.lookupProjectAny(pid)
		if err != nil {
			a.internalError(c, "find project", err)
			return
		}
		if p == nil {
			c.Render404("")
			return
		}
		project = p
	}
	env, err := a.queryEnv(c)
	if err != nil {
		a.internalError(c, "query env", err)
		return
	}
	q, err := query.New(c.Ctx(), env, kind, project)
	if err != nil {
		a.internalError(c, "new query", err)
		return
	}
	if !c.allowedToGloballyOrProject(q.ViewPermission(), project) {
		c.DenyAccess()
		return
	}
	af, err := q.AvailableFilters(c.Ctx())
	if err != nil {
		a.internalError(c, "available filters", err)
		return
	}
	var values any = []any{}
	if def := af.Get(c.Params().String("name")); def != nil {
		vals, err := def.LoadValues(c.Ctx())
		if err != nil {
			a.internalError(c, "filter values", err)
			return
		}
		values = filterValuesJSON(def, vals)
	}
	b, err := json.Marshal(values)
	if err != nil {
		a.internalError(c, "filter json", err)
		return
	}
	c.W.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.W.WriteHeader(http.StatusOK)
	_, _ = c.W.Write(b)
	c.Halt()
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
