package handler

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは projects#index（ボード / リスト表示・CSV・Atom・API）と、
// ProjectsQueriesHelper / ProjectsHelper#render_project_hierarchy の移植。

// retrieveProjectQuery は ProjectsController#retrieve_default_query + retrieve_project_query
// （admin は AdminController#projects の retrieve_query(ProjectAdminQuery, false)）。
// 見つからない・見えない保存クエリはそれぞれ 404 / deny_access を描画して nil を返す。
func (a *App) retrieveProjectQuery(c *Req, kind query.Kind) (*query.Query, bool) {
	ctx := c.Ctx()
	env, err := a.queryEnv(c)
	if err != nil {
		a.internalError(c, "query env", err)
		return nil, false
	}
	p := queryParams(c)
	api := httpx.IsAPIRequest(c.R)
	if kind == query.KindProject {
		id, setFilter, err := query.DefaultQueryID(ctx, env, kind, nil, p, nil, false, api)
		if err != nil {
			a.internalError(c, "default query", err)
			return nil, false
		}
		if setFilter {
			p.SetFilter = true
		} else if id != 0 {
			p.QueryID = strconv.FormatInt(id, 10)
		}
	}
	q, _, err := query.Retrieve(ctx, env, kind, nil, p, nil, query.RetrieveOptions{UseSession: false, API: api})
	switch {
	case errors.Is(err, query.ErrNotFound):
		c.Render404("")
		return nil, false
	case errors.Is(err, query.ErrUnauthorized):
		c.DenyAccess()
		return nil, false
	case err != nil:
		a.internalError(c, "retrieve query", err)
		return nil, false
	}
	return q, true
}

// ProjectsIndex は projects#index（GET /projects）。
func (a *App) ProjectsIndex(c *Req) {
	// try to redirect to the requested menu item
	if jump := c.Params().String("jump"); jump != "" {
		if u, ok := a.Helpers.ProjectMenuItemURL(c.Page(), "application_menu", jump, nil); ok {
			c.Redirect(u)
			return
		}
	}
	q, ok := a.retrieveProjectQuery(c, query.KindProject)
	if !ok {
		return
	}
	ctx := c.Ctx()
	switch httpx.Negotiate(c.R, "html", "xml", "json", "atom", "csv") {
	case "html":
		data := map[string]any{}
		qv, err := a.newQueryView(c, q, "ProjectQuery", "/projects")
		if err != nil {
			a.internalError(c, "query view", err)
			return
		}
		data["QueryView"] = qv
		if qv.Valid {
			if q.DisplayType() == "board" {
				entries, err := q.Projects(ctx, query.ListOptions{})
				if err != nil {
					a.queryStatementError(c, err)
					return
				}
				data["Entries"] = entries
				if len(entries) > 0 {
					board, err := a.renderProjectHierarchy(c, entries)
					if err != nil {
						a.internalError(c, "project hierarchy", err)
						return
					}
					data["Board"] = board
				}
			} else {
				count, err := q.Count(ctx)
				if err != nil {
					a.queryStatementError(c, err)
					return
				}
				pg := newMinPaginator(int(count), c.perPageOptionMin(), c.Params().String("page"))
				entries, err := q.Projects(ctx, query.ListOptions{Offset: pg.Offset(), Limit: pg.PerPage})
				if err != nil {
					a.queryStatementError(c, err)
					return
				}
				data["Entries"] = entries
				list, err := a.projectListView(c, qv, entries, pg, int(count), false)
				if err != nil {
					a.internalError(c, "project list", err)
					return
				}
				data["List"] = list
			}
		}
		sidebar, err := a.sidebarQueriesHTML(c, query.KindProject, q, "/projects")
		if err != nil {
			a.internalError(c, "sidebar queries", err)
			return
		}
		data["SidebarQueries"] = sidebar
		data["CanAddProject"] = c.AllowedToGlobally(domain.Perm("add_project"))
		data["AtomKey"] = c.AtomKey()
		data["CSVQuery"] = csvLinkQuery(c)
		c.Render("projects/index", data)
	case "xml", "json":
		a.renderProjectsIndexAPI(c, q)
	case "atom":
		a.renderProjectsAtom(c, q)
	case "csv":
		entries, err := q.Projects(ctx, query.ListOptions{})
		if err != nil {
			a.queryStatementError(c, err)
			return
		}
		a.sendProjectsCSV(c, q, entries)
	default:
		c.unknownFormat()
	}
}

// queryStatementError は Query::StatementInvalid（rescue_from → render_error）。
func (a *App) queryStatementError(c *Req, err error) {
	var qe *query.QueryError
	if errors.As(err, &qe) {
		c.RenderError(http.StatusInternalServerError, qe.Error())
		return
	}
	a.internalError(c, "query", err)
}

// csvLinkQuery は link_to_with_query_parameters 'CSV' のクエリ文字列（page / format を除く）。
func csvLinkQuery(c *Req) string {
	v := url.Values{}
	for k, vals := range c.R.URL.Query() {
		if k == "page" || k == "format" {
			continue
		}
		v[k] = vals
	}
	return railsToQuery(v)
}

// ---------------------------------------------------------------- board（render_project_hierarchy）

// projectMarks は User.current.member_of? / bookmarked_project_ids。
type projectMarks struct {
	member     map[int64]bool
	bookmarked map[int64]bool
}

func (a *App) loadProjectMarks(c *Req) (*projectMarks, error) {
	m := &projectMarks{member: map[int64]bool{}, bookmarked: map[int64]bool{}}
	if !c.User.Logged() {
		return m, nil
	}
	ids, err := repository.MemberProjectIDs(c.Ctx(), a.DB, c.User.ID)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		m.member[id] = true
	}
	bids, err := repository.BookmarkedProjectIDs(c.Ctx(), a.DB, c.User.ID)
	if err != nil {
		return nil, err
	}
	for _, id := range bids {
		m.bookmarked[id] = true
	}
	return m, nil
}

// projectTreeInfo はツリー判定（is_descendant_of? / leaf?）に使う入れ子集合の値。
type projectTreeInfo struct {
	ns     map[int64]repository.NestedSetValue
	leaves map[int64]bool
}

func (a *App) loadProjectTree(c *Req) (*projectTreeInfo, error) {
	ns, err := repository.ProjectNestedSet(c.Ctx(), a.DB)
	if err != nil {
		return nil, err
	}
	t := &projectTreeInfo{ns: ns, leaves: map[int64]bool{}}
	for id, v := range ns {
		t.leaves[id] = v.Rgt == v.Lft+1
	}
	return t, nil
}

func (t *projectTreeInfo) isDescendantOf(p, ancestor *domain.Project) bool {
	a, b := t.ns[ancestor.ID], t.ns[p.ID]
	return a.Lft < b.Lft && b.Rgt < a.Rgt
}

// renderProjectHierarchy は ProjectsHelper#render_project_hierarchy（render_project_nested_lists）。
func (a *App) renderProjectHierarchy(c *Req, projects []*domain.Project) (template.HTML, error) {
	marks, err := a.loadProjectMarks(c)
	if err != nil {
		return "", err
	}
	tree, err := a.loadProjectTree(c)
	if err != nil {
		return "", err
	}
	page := c.Page()
	page.SetProjectLeaves(tree.leaves)
	sorted := slices.Clone(projects)
	slices.SortStableFunc(sorted, func(x, y *domain.Project) int { return tree.ns[x.ID].Lft - tree.ns[y.ID].Lft })
	var s strings.Builder
	var ancestors []*domain.Project
	for _, p := range sorted {
		if len(ancestors) == 0 || tree.isDescendantOf(p, ancestors[len(ancestors)-1]) {
			cls := ""
			if len(ancestors) == 0 {
				cls = "root"
			}
			s.WriteString("<ul class='projects " + cls + "'>\n")
		} else {
			ancestors = ancestors[:len(ancestors)-1]
			s.WriteString("</li>")
			for len(ancestors) > 0 && !tree.isDescendantOf(p, ancestors[len(ancestors)-1]) {
				ancestors = ancestors[:len(ancestors)-1]
				s.WriteString("</ul></li>\n")
			}
		}
		classes := "child"
		if len(ancestors) == 0 {
			classes = "root"
		}
		s.WriteString("<li class='" + classes + "'><div class='" + classes + "'>")
		s.WriteString(string(a.projectHierarchyItem(c, page, p, marks)))
		s.WriteString("</div>\n")
		ancestors = append(ancestors, p)
	}
	s.WriteString(strings.Repeat("</li></ul>\n", len(ancestors)))
	return template.HTML(s.String()), nil
}

// projectHierarchyItem は render_project_hierarchy のブロック（1 プロジェクト分）。
func (a *App) projectHierarchyItem(c *Req, page *helper.Page, p *domain.Project, marks *projectMarks) template.HTML {
	classes := strings.Fields(helper.ProjectCSSClasses(page, p))
	if marks.member[p.ID] {
		classes = append(classes, "icon", "icon-user", "my-project")
	}
	if marks.bookmarked[p.ID] {
		classes = append(classes, "icon", "icon-bookmarked-project")
	}
	var uniq []string
	for _, cl := range classes {
		if !slices.Contains(uniq, cl) {
			uniq = append(uniq, cl)
		}
	}
	s := string(linkToProjectHTML(p, rails.NewHash("class", strings.Join(uniq, " "))))
	s += string(a.projectMarkIcons(c, page, p, marks))
	if strings.TrimSpace(p.Description) != "" {
		s += string(rails.ContentTag("div", helper.Textilizable(shortDescription(p.Description)), rails.NewHash("class", "wiki description")))
	}
	return template.HTML(s)
}

// projectMarkIcons は「自分のプロジェクト」「ブックマーク」のアイコン。
func (a *App) projectMarkIcons(c *Req, page *helper.Page, p *domain.Project, marks *projectMarks) template.HTML {
	var s string
	if marks.member[p.ID] {
		s += string(rails.ContentTag("span", a.Helpers.SpriteIconOnly(page, "user", c.L("label_my_projects")), rails.NewHash("class", "icon-only icon-user my-project")))
	}
	if marks.bookmarked[p.ID] {
		s += string(rails.ContentTag("span", a.Helpers.SpriteIconOnly(page, "bookmarked", c.L("label_my_bookmarks")), rails.NewHash("class", "icon-only icon-bookmarked-project")))
	}
	return template.HTML(s)
}

// linkToProjectHTML は link_to_project(project, {}, html_options)（アーカイブ済みは名前のみ）。
func linkToProjectHTML(p *domain.Project, htmlOpts *rails.Hash) template.HTML {
	if p.Archived() {
		return rails.H(p.Name)
	}
	return rails.LinkTo(p.Name, "/projects/"+p.Identifier, htmlOpts)
}

var shortDescRe = func(s string, length int) string {
	// description.gsub(/^(.{#{length}}[^\n\r]*).*$/m, '\1...').strip
	lines := strings.SplitAfter(s, "\n")
	for i, line := range lines {
		body := strings.TrimRight(line, "\r\n")
		if len([]rune(body)) >= length {
			// この行で切り、行末までを残して以降を捨てる
			idx := strings.IndexAny(line, "\r\n")
			if idx < 0 {
				idx = len(line)
			}
			head := strings.Join(lines[:i], "") + line[:idx]
			return strings.TrimSpace(head + "...")
		}
	}
	return strings.TrimSpace(s)
}

// shortDescription は Project#short_description（255 文字）。
func shortDescription(s string) string { return shortDescRe(s, 255) }

// ---------------------------------------------------------------- list（projects/_list）

// projectListRow は一覧の 1 行。
type projectListRow struct {
	Project *domain.Project
	// RowClass は tr の class（cycle 以外の部分）。
	RowClass string
	Cells    []projectListCell
	// Group* は grouped_project_list のグループ見出し（GroupName が nil でなければ見出しを出す）。
	GroupName   any
	GroupCount  any
	GroupTotals template.HTML
}

// projectListCell は 1 セル。
type projectListCell struct {
	Content template.HTML
	CSS     string
}

// projectListView は projects/_list の描画データ。
type projectListView struct {
	QV         *queryView
	Columns    []*query.Column
	Rows       []projectListRow
	Totals     template.HTML
	BackURL    string
	Pagination template.HTML
	AdminList  bool
	CSVHidden  template.HTML
	CMURL      string
	// FormAction は form_tag({}) の action（現在のパス）。
	FormAction string
}

// projectListView は projects/_list の描画データを作る（admin なら @admin_list）。
func (a *App) projectListView(c *Req, qv *queryView, entries []*domain.Project, pg *minPaginator, count int, admin bool) (*projectListView, error) {
	ctx := c.Ctx()
	q := qv.Q
	cols, err := q.InlineColumns(ctx)
	if err != nil {
		return nil, err
	}
	lv := &projectListView{QV: qv, Columns: cols, AdminList: admin, CMURL: "/admin/projects_context_menu", FormAction: c.R.URL.Path}
	if lv.Totals, err = qv.TotalsHTML(); err != nil {
		return nil, err
	}
	lv.BackURL = c.R.URL.Path
	if qs := railsToQuery(c.R.URL.Query()); qs != "" {
		lv.BackURL += "?" + qs
	}
	lv.Pagination = c.paginationLinksFull(pg, count, true, nil)
	if lv.CSVHidden, err = qv.AsHiddenFieldTags(); err != nil {
		return nil, err
	}
	marks, err := a.loadProjectMarks(c)
	if err != nil {
		return nil, err
	}
	tree, err := a.loadProjectTree(c)
	if err != nil {
		return nil, err
	}
	page := c.Page()
	page.SetProjectLeaves(tree.leaves)
	ctxv, err := a.newProjectCellContext(c, entries, cols)
	if err != nil {
		return nil, err
	}
	// grouped_query_results
	groupCol, err := q.GroupByColumn(ctx)
	if err != nil {
		return nil, err
	}
	var countByGroup map[query.GroupKey]int64
	if groupCol != nil {
		if countByGroup, err = q.ResultCountByGroup(ctx); err != nil {
			return nil, err
		}
	}
	var ancestors []*domain.Project
	var prev *query.GroupKey
	first := true
	for _, p := range entries {
		row := projectListRow{Project: p}
		if groupCol != nil {
			key, label := a.projectGroupValue(c, ctxv, groupCol, p)
			if first || prev == nil || *prev != key {
				row.GroupName = label
				if countByGroup != nil {
					row.GroupCount = countByGroup[key]
				}
			}
			k := key
			prev = &k
		}
		first = false
		// grouped_project_list
		for len(ancestors) > 0 && !tree.isDescendantOf(p, ancestors[len(ancestors)-1]) {
			ancestors = ancestors[:len(ancestors)-1]
		}
		level := len(ancestors)
		css := helper.ProjectCSSClasses(page, p) + " "
		if level > 0 {
			css += "idnt idnt-" + strconv.Itoa(level)
		}
		row.RowClass = css
		for _, col := range cols {
			row.Cells = append(row.Cells, projectListCell{Content: a.projectColumnContent(c, page, ctxv, col, p, marks), CSS: col.CSSClasses()})
		}
		lv.Rows = append(lv.Rows, row)
		if !tree.leaves[p.ID] {
			ancestors = append(ancestors, p)
		}
	}
	return lv, nil
}

// projectCellContext は列の値に必要な関連データ（親・CF 値・最終活動日）。
type projectCellContext struct {
	parents      map[int64]*domain.Project
	customValues map[int64]map[int64][]string
	cfFormats    map[int64]*domain.CustomFieldInfo
	lastActivity map[int64]time.Time
}

func (a *App) newProjectCellContext(c *Req, entries []*domain.Project, cols []*query.Column) (*projectCellContext, error) {
	ctx := c.Ctx()
	pc := &projectCellContext{customValues: map[int64]map[int64][]string{}, cfFormats: map[int64]*domain.CustomFieldInfo{}}
	var parentIDs []int64
	for _, p := range entries {
		if p.ParentID != nil {
			parentIDs = append(parentIDs, *p.ParentID)
		}
	}
	var err error
	if pc.parents, err = repository.ProjectsByIDs(ctx, a.DB, parentIDs); err != nil {
		return nil, err
	}
	needCF := false
	needActivity := false
	for _, col := range cols {
		if col.CustomField != nil {
			needCF = true
		}
		if col.Name == "last_activity_date" {
			needActivity = true
		}
	}
	if needCF {
		infos, err := repository.ProjectCustomFieldInfos(ctx, a.DB)
		if err != nil {
			return nil, err
		}
		for _, cf := range infos {
			pc.cfFormats[cf.ID] = cf
		}
		for _, p := range entries {
			cv, err := repository.CustomValues(ctx, a.DB, "project", p.ID)
			if err != nil {
				return nil, err
			}
			pc.customValues[p.ID] = cv
		}
	}
	if needActivity {
		if pc.lastActivity, err = a.lastActivityByProject(c); err != nil {
			return nil, err
		}
	}
	return pc, nil
}

// projectStatusLabel は get_project_status_label。
func projectStatusLabel(c *Req, status int) string {
	switch status {
	case domain.ProjectStatusActive:
		return c.L("project_status_active")
	case domain.ProjectStatusClosed:
		return c.L("project_status_closed")
	}
	return ""
}

// projectColumnContent は column_content（ProjectsQueriesHelper#column_value）。
func (a *App) projectColumnContent(c *Req, page *helper.Page, pc *projectCellContext, col *query.Column, p *domain.Project, marks *projectMarks) template.HTML {
	if col.CustomField != nil {
		return a.projectCFCell(c, pc, col, p, true)
	}
	switch col.Name {
	case "name":
		return linkToProjectHTML(p, nil) + a.projectMarkIcons(c, page, p, marks)
	case "short_description":
		if strings.TrimSpace(p.Description) == "" {
			return ""
		}
		return rails.ContentTag("div", helper.Textilizable(shortDescription(p.Description)), rails.NewHash("class", "wiki"))
	case "homepage":
		if strings.TrimSpace(p.Homepage) == "" {
			return ""
		}
		return rails.ContentTag("div", helper.Textilizable(p.Homepage), rails.NewHash("class", "wiki"))
	case "status":
		return rails.H(projectStatusLabel(c, p.Status))
	case "parent_id":
		if p.ParentID == nil {
			return ""
		}
		if parent := pc.parents[*p.ParentID]; parent != nil {
			return linkToProjectHTML(parent, nil)
		}
		return ""
	case "identifier":
		return rails.H(p.Identifier)
	case "is_public":
		if p.IsPublic {
			return rails.H(c.L("general_text_Yes"))
		}
		return rails.H(c.L("general_text_No"))
	case "created_on":
		return rails.H(c.Loc.FormatTime(p.CreatedAt, true))
	case "updated_on":
		return rails.H(c.Loc.FormatTime(p.UpdatedAt, true))
	case "last_activity_date":
		t, ok := pc.lastActivity[p.ID]
		if !ok {
			return ""
		}
		from := t
		if c.Loc.Location != nil {
			from = t.In(c.Loc.Location)
		}
		return rails.LinkTo(c.Loc.FormatTime(t, true), "/projects/"+p.Identifier+"/activity?from="+from.Format("2006-01-02"), nil)
	}
	return ""
}

func (a *App) projectCFCell(c *Req, pc *projectCellContext, col *query.Column, p *domain.Project, html bool) template.HTML {
	info := pc.cfFormats[col.CustomField.ID]
	if info == nil {
		return ""
	}
	v := &projectCFValue{Field: info, Values: pc.customValues[p.ID][info.ID]}
	if html {
		return showCFValue(c, v)
	}
	return template.HTML(csvCFValue(c, v))
}

// projectGroupValue はグループ列の値（キーと表示名。空なら "(blank)"）。
func (a *App) projectGroupValue(c *Req, pc *projectCellContext, col *query.Column, p *domain.Project) (query.GroupKey, string) {
	if col.CustomField != nil {
		vals := pc.customValues[p.ID][col.CustomField.ID]
		if len(vals) == 0 || strings.TrimSpace(vals[0]) == "" {
			return query.GroupKey{Null: true}, "(" + c.L("label_blank_value") + ")"
		}
		return query.GroupKey{Value: vals[0]}, vals[0]
	}
	switch col.Name {
	case "is_public":
		if p.IsPublic {
			return query.GroupKey{Value: "true"}, c.L("general_text_Yes")
		}
		return query.GroupKey{Value: "false"}, c.L("general_text_No")
	}
	return query.GroupKey{Null: true}, "(" + c.L("label_blank_value") + ")"
}

// ---------------------------------------------------------------- CSV

// sendProjectsCSV は query_to_csv(entries, @query, params)（ProjectsQueriesHelper#csv_value）。
func (a *App) sendProjectsCSV(c *Req, q *query.Query, entries []*domain.Project) {
	ctx := c.Ctx()
	cols, err := q.Columns(ctx)
	if err != nil {
		a.internalError(c, "columns", err)
		return
	}
	pc, err := a.newProjectCellContext(c, entries, cols)
	if err != nil {
		a.internalError(c, "cells", err)
		return
	}
	env := q.Env()
	header := make([]string, len(cols))
	for i, col := range cols {
		header[i] = col.CaptionText(env)
	}
	rows := [][]string{header}
	for _, p := range entries {
		row := make([]string, len(cols))
		for i, col := range cols {
			row[i] = a.projectCSVValue(c, pc, col, p)
		}
		rows = append(rows, row)
	}
	c.sendCSV("projects.csv", rows, true)
}

func (a *App) projectCSVValue(c *Req, pc *projectCellContext, col *query.Column, p *domain.Project) string {
	if col.CustomField != nil {
		return string(a.projectCFCell(c, pc, col, p, false))
	}
	switch col.Name {
	case "name":
		return p.Name
	case "status":
		return projectStatusLabel(c, p.Status)
	case "parent_id":
		if p.ParentID != nil {
			if parent := pc.parents[*p.ParentID]; parent != nil {
				return parent.Name
			}
		}
		return csvNil
	case "short_description":
		return shortDescription(p.Description)
	case "homepage":
		return p.Homepage
	case "identifier":
		return p.Identifier
	case "is_public":
		if p.IsPublic {
			return c.L("general_text_Yes")
		}
		return c.L("general_text_No")
	case "created_on":
		return c.Loc.FormatTime(p.CreatedAt, true)
	case "updated_on":
		return c.Loc.FormatTime(p.UpdatedAt, true)
	case "last_activity_date":
		if t, ok := pc.lastActivity[p.ID]; ok {
			return c.Loc.FormatTime(t, true)
		}
	}
	return ""
}
