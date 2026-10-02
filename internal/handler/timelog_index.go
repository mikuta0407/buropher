package handler

import (
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/activity"
	"github.com/mikuta0407/buropher/internal/apibuilder"
	"github.com/mikuta0407/buropher/internal/csvexport"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/pagination"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/timelog"
	"github.com/mikuta0407/buropher/internal/urlroot"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// TimelogIndex は timelog#index（html / api / atom / csv）。
func (a *App) TimelogIndex(c *Req) {
	format := teFormat(c, "html", "json", "xml", "atom", "csv")
	if format == "" {
		c.head(http.StatusNotAcceptable)
		return
	}
	q, ok := a.teRetrieveQuery(c)
	if !ok {
		return
	}
	switch format {
	case "html":
		a.teIndexHTML(c, q)
	case "json", "xml":
		a.teIndexAPI(c, q)
	case "atom":
		a.teIndexAtom(c, q)
	case "csv":
		a.teIndexCSV(c, q)
	}
}

// teEntriesFromRows は query の行を Entry にする。
func teEntriesFromRows(rows []*query.TimeEntryRow) []*timelog.Entry {
	out := make([]*timelog.Entry, len(rows))
	for i, r := range rows {
		out[i] = timelog.FromRecord(&domain.TimeEntry{ID: r.ID, ProjectID: r.ProjectID, UserID: r.UserID, AuthorID: r.AuthorID,
			IssueID: r.IssueID, Hours: r.Hours, Comments: teCommentsPtr(r), ActivityID: r.ActivityID, SpentOn: r.SpentOn,
			TYear: r.TYear, TMonth: r.TMonth, TWeek: r.TWeek, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt})
	}
	return out
}

// teCommentsPtr は comments（NULL なら nil。D-17）。
func teCommentsPtr(r *query.TimeEntryRow) *string {
	if r.CommentsNull {
		return nil
	}
	s := r.Comments
	return &s
}

// teIndexAPI は index.api.rsb。
func (a *App) teIndexAPI(c *Req, q *query.Query) {
	ctx := c.Ctx()
	count, err := q.Count(ctx)
	if err != nil {
		a.teQueryFailed(c, err)
		return
	}
	offset, limit := c.APIOffsetAndLimit()
	rows, err := q.TimeEntries(ctx, query.ListOptions{Offset: offset, Limit: limit})
	if err != nil {
		a.teQueryFailed(c, err)
		return
	}
	entries := teEntriesFromRows(rows)
	l, err := a.newTELookup(c, entries)
	if err != nil {
		a.internalError(c, "time entry lookup", err)
		return
	}
	env := a.teEnv(c)
	cvs := make([][]*domain.CustomFieldValue, len(entries))
	for i, t := range entries {
		if cvs[i], err = a.teLoadVisibleCVs(c, env, t); err != nil {
			a.internalError(c, "time entry custom values", err)
			return
		}
	}
	c.RenderAPI(0, func(b apibuilder.Builder) {
		b.Array("time_entries", c.APIMeta(apibuilder.A("total_count", count, "offset", offset, "limit", limit)), func() {
			for i, t := range entries {
				b.Object("time_entry", func() { l.apiEntry(b, t, cvs[i]) })
			}
		})
	})
}

// teLoadVisibleCVs は保存済みの工数の visible_custom_field_values。
func (a *App) teLoadVisibleCVs(c *Req, env *timelog.Env, t *timelog.Entry) ([]*domain.CustomFieldValue, error) {
	full, err := env.Find(c.Ctx(), t.ID)
	if err != nil {
		return nil, err
	}
	return env.VisibleCustomFieldValues(c.Ctx(), full, c.User)
}

// teIndexAtom は format.atom（作成日時の降順で feeds_limit 件）。
func (a *App) teIndexAtom(c *Req, q *query.Query) {
	ctx := c.Ctx()
	ids, err := q.IDs(ctx, query.ListOptions{Order: []string{"time_entries.created_at DESC"}, Limit: a.Settings.Int("feeds_limit")})
	if err != nil {
		a.teQueryFailed(c, err)
		return
	}
	var events []*activity.Event
	if len(ids) > 0 {
		parts := make([]string, len(ids))
		for i, id := range ids {
			parts[i] = strconv.FormatInt(id, 10)
		}
		loader := &activity.Loader{Q: a.DB, Auth: c.Authz(), Loc: c.Loc}
		events, err = loader.TimeEntries(ctx, "time_entries.id IN ("+strings.Join(parts, ",")+")", nil, "ORDER BY time_entries.created_at DESC")
		if err != nil {
			a.internalError(c, "time entry events", err)
			return
		}
	}
	a.renderFeed(c, events, c.L("label_spent_time"))
}

// teIndexCSV は format.csv（query_to_csv）。
func (a *App) teIndexCSV(c *Req, q *query.Query) {
	ctx := c.Ctx()
	rows, err := q.TimeEntries(ctx, query.ListOptions{})
	if err != nil {
		a.teQueryFailed(c, err)
		return
	}
	entries := teEntriesFromRows(rows)
	l, err := a.newTELookup(c, entries)
	if err != nil {
		a.internalError(c, "time entry lookup", err)
		return
	}
	cols, err := q.Columns(ctx)
	if err != nil {
		a.teQueryFailed(c, err)
		return
	}
	if err := l.prepareColumns(cols, entries); err != nil {
		a.internalError(c, "time entry columns", err)
		return
	}
	p := c.Params()
	w := csvexport.New(csvexport.Options{
		Separator:       firstNonEmpty(p.String("field_separator"), c.L("general_csv_separator")),
		Encoding:        p.String("encoding"),
		DefaultEncoding: c.L("general_csv_encoding"),
	})
	var head []string
	for _, col := range cols {
		head = append(head, col.CaptionText(q.Env()))
	}
	w.Strings(head...)
	for _, t := range entries {
		var row []csvexport.Field
		for _, col := range cols {
			row = append(row, csvexport.S(l.csvContent(col, t)))
		}
		w.Row(row...)
	}
	name := teFilenameForExport(p.String("query_name"), q, "timelog")
	c.halted = true
	c.W.Header().Set("Content-Type", "text/csv; header=present")
	c.W.Header().Set("Content-Disposition", `attachment; filename="`+name+`.csv"; filename*=UTF-8''`+name+".csv")
	c.W.WriteHeader(http.StatusOK)
	_, _ = c.W.Write(w.Bytes())
}

// teFilenameForExport は filename_for_export(query, default_name)。
func teFilenameForExport(param string, q *query.Query, def string) string {
	name := strings.TrimSpace(param)
	if name == "" {
		name = q.Name
	}
	if name == "_" || strings.TrimSpace(name) == "" {
		name = def
	}
	return strings.ToLower(redmine.Titleize(name))
}

// ---------------------------------------------------------------- HTML

// teListRow は一覧の 1 行。
type teListRow struct {
	Entry    *timelog.Entry
	CSS      string
	Editable bool
	Cells    []teCell
	// Group* はグループの見出し（GroupName が nil でなければ出す）。
	GroupName   any
	GroupCount  any
	GroupTotals template.HTML
	HasGroup    bool
}

// teCell は 1 セル。
type teCell struct {
	CSS     string
	Content any
}

// teListView は timelog/_list の表示データ。
type teListView struct {
	qv      *queryView
	Rows    []*teListRow
	Inline  []*query.Column
	BackURL string
}

// ColSpan は @query.inline_columns.size + 2。
func (v *teListView) ColSpan() int { return len(v.Inline) + 2 }

// Headers は column_header の並び。
func (v *teListView) Headers() []template.HTML {
	out := make([]template.HTML, len(v.Inline))
	for i, col := range v.Inline {
		out[i] = v.qv.ColumnHeader(col)
	}
	return out
}

// newTEListView は entries の一覧を組み立てる（grouped_query_results）。
func (a *App) newTEListView(c *Req, qv *queryView, entries []*timelog.Entry) (*teListView, error) {
	ctx := c.Ctx()
	q := qv.Q
	inline, err := q.InlineColumns(ctx)
	if err != nil {
		return nil, err
	}
	l, err := a.newTELookup(c, entries)
	if err != nil {
		return nil, err
	}
	if err := l.prepareColumns(inline, entries); err != nil {
		return nil, err
	}
	v := &teListView{qv: qv, Inline: inline, BackURL: helper.URLWithQuery(c.R.URL.Path, pageQueryParameters(c))}
	groupCol, err := q.GroupByColumn(ctx)
	if err != nil {
		return nil, err
	}
	var counts map[query.GroupKey]int64
	var totals []query.GroupTotal
	if groupCol != nil {
		if counts, err = q.ResultCountByGroup(ctx); err != nil {
			return nil, err
		}
		if totals, err = q.TotalsByGroup(ctx); err != nil {
			return nil, err
		}
		if err := l.prepareColumns([]*query.Column{groupCol}, entries); err != nil {
			return nil, err
		}
	}
	env := a.teEnv(c)
	var prev query.GroupKey
	first := true
	odd := true
	for _, t := range entries {
		row := &teListRow{Entry: t}
		if groupCol != nil {
			key, name := l.groupValue(groupCol, t)
			if first || key != prev {
				row.HasGroup = true
				if key.Null {
					row.GroupName = "(" + c.L("label_blank_value") + ")"
				} else if name == nil {
					row.GroupName = ""
				} else {
					row.GroupName = name
				}
				if counts != nil {
					if n, ok := counts[key]; ok {
						row.GroupCount = n
					}
				}
				var parts []string
				for _, gt := range totals {
					parts = append(parts, string(qv.totalTag(gt.Column, gt.ByGroup[key])))
				}
				row.GroupTotals = template.HTML(strings.Join(parts, " "))
				odd = true
			}
			prev, first = key, false
		}
		if odd {
			row.CSS = "odd"
		} else {
			row.CSS = "even"
		}
		odd = !odd
		if row.Editable, err = env.EditableBy(ctx, t, c.User); err != nil {
			return nil, err
		}
		for _, col := range inline {
			row.Cells = append(row.Cells, teCell{CSS: col.CSSClasses(), Content: l.columnContent(col, t)})
		}
		v.Rows = append(v.Rows, row)
	}
	return v, nil
}

// teIndexHTML は index.html（render :layout => !request.xhr?）。
func (a *App) teIndexHTML(c *Req, q *query.Query) {
	ctx := c.Ctx()
	qv, err := a.newQueryView(c, q, "TimeEntryQuery", teTimeEntriesPath(c.Project))
	if err != nil {
		a.teQueryFailed(c, err)
		return
	}
	data := a.teCommonData(c, q, qv, teTimeEntriesPath(c.Project))
	if qv.Valid {
		count, err := q.Count(ctx)
		if err != nil {
			a.teQueryFailed(c, err)
			return
		}
		pages := pagination.New(int(count), c.PerPageOption(), c.Params().String("page"))
		rows, err := q.TimeEntries(ctx, query.ListOptions{Offset: pages.Offset(), Limit: pages.PerPage})
		if err != nil {
			a.teQueryFailed(c, err)
			return
		}
		entries := teEntriesFromRows(rows)
		lv, err := a.newTEListView(c, qv, entries)
		if err != nil {
			a.teQueryFailed(c, err)
			return
		}
		totals, err := qv.TotalsHTML()
		if err != nil {
			a.teQueryFailed(c, err)
			return
		}
		hidden, err := qv.AsHiddenFieldTags()
		if err != nil {
			a.teQueryFailed(c, err)
			return
		}
		blocks, err := q.AvailableBlockColumns(ctx)
		if err != nil {
			a.teQueryFailed(c, err)
			return
		}
		var bcs []map[string]any
		for _, b := range blocks {
			bcs = append(bcs, map[string]any{"Name": b.Name, "Caption": b.CaptionText(q.Env())})
		}
		data["List"] = lv
		data["Empty"] = len(entries) == 0
		data["Totals"] = totals
		data["Pages"] = pages
		data["Count"] = int(count)
		data["CSVHidden"] = hidden
		data["CSVBlockColumns"] = bcs
		qp := pageQueryParameters(c).Except("page", "format")
		data["CSVURL"] = helper.URLWithQuery(teTimeEntriesPath(c.Project)+".csv", qp)
		atomQP := qp.Except("key")
		if k := c.AtomKey(); k != "" {
			atomQP.Set("key", k)
		}
		data["AtomURL"] = helper.URLWithQuery(teTimeEntriesPath(c.Project)+".atom", atomQP)
	}
	data["CanImport"] = c.allowedToGloballyOrProject("import_time_entries", c.Project) && c.allowedToGloballyOrProject("log_time", c.Project)
	data["ImportPath"] = "/time_entries/imports/new"
	if c.Project != nil {
		data["ImportPath"] = "/time_entries/imports/new?project_id=" + c.Project.Identifier
	}
	data["CanManageActivities"] = c.Project != nil && c.AllowedTo(domain.Perm("manage_project_activities"), c.Project) &&
		c.AllowedTo(domain.ControllerAction("projects", "settings"), c.Project)
	// auto_discovery_link_tag(:atom, {:issue_id => @issue, :format => 'atom', :key => atom_key})
	disc := teTimeEntriesPath(c.Project) + ".atom"
	if k := c.AtomKey(); k != "" {
		disc += "?key=" + k
	}
	data["AtomDiscoveryURL"] = httpx.RequestBaseURL(c.R) + urlroot.Path(disc)
	opts := RenderOptions{}
	if httpx.IsXHR(c.R) {
		opts.Layout = view.NoLayout
	}
	c.Render("timelog/index", data, opts)
}

// teCommonData は index / report 共通の値（右上のリンク・クエリフォーム・サイドバー）。
func (a *App) teCommonData(c *Req, q *query.Query, qv *queryView, listPath string) map[string]any {
	data := map[string]any{"qv": qv, "Valid": qv.Valid}
	data["CanLogTime"] = c.allowedToGloballyOrProject("log_time", c.Project)
	var issueID *int64
	if id := q.ValueFor("issue_id", 0); id != "" && teDigitsRe(id) {
		n, _ := strconv.ParseInt(id, 10, 64)
		issueID = &n
	}
	data["NewTimeEntryPath"] = teNewTimeEntryPath(c.Project, issueID)
	data["Title"] = c.L("label_spent_time")
	if q.ID != 0 {
		data["Title"] = q.Name
		if strings.TrimSpace(q.Description) != "" {
			data["Description"] = q.Description
		}
	}
	data["TimeEntriesPath"] = teTimeEntriesPath(c.Project)
	data["ListFormAction"] = teTimeEntriesPath(c.Project)
	data["ReportPath"] = teReportPath(c.Project)
	qp := pageQueryParameters(c)
	data["DetailsTabURL"] = helper.URLWithQuery(teTimeEntriesPath(c.Project), qp)
	data["ReportTabURL"] = helper.URLWithQuery(teReportPath(c.Project), qp)
	// content_for :sidebar（render :partial => 'timelog/sidebar'）。空白のみなら Rails の capture は
	// ブロックの戻り値（最後の改行）になる
	data["Sidebar"] = template.HTML("\n")
	if sidebar, err := a.sidebarQueriesHTML(c, query.KindTimeEntry, q, listPath); err != nil {
		a.logger().Error("sidebar queries", "err", err)
	} else if sidebar != "" {
		data["Sidebar"] = "  " + sidebar + "\n\n"
	}
	data["SettingsActivitiesPath"] = ""
	if c.Project != nil {
		data["SettingsActivitiesPath"] = "/projects/" + c.Project.Identifier + "/settings/activities"
	}
	return data
}

func teDigitsRe(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// teNewTimeEntryPath は _new_time_entry_path(project, issue)。
func teNewTimeEntryPath(p *domain.Project, issueID *int64) string {
	switch {
	case issueID != nil:
		return "/issues/" + strconv.FormatInt(*issueID, 10) + "/time_entries/new"
	case p != nil:
		return "/projects/" + p.Identifier + "/time_entries/new"
	}
	return "/time_entries/new"
}

var _ = rails.H
