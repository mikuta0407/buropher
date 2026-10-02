package handler

import (
	"net/http"
	"strconv"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// ReportsController（app/controllers/reports_controller.rb）。menu_item :issues。
var ReportsController = &Controller{Name: "reports", MainMenu: true,
	MenuItem: func(string) string { return "issues" }}

// routesReports は reports コントローラのルートを登録する。
func (a *App) routesReports(r Router) {
	// before_action :find_project, :authorize, :find_issue_statuses
	// get 'projects/:id/issues/report', :to => 'reports#issue_report', :as => 'project_issues_report'
	a.Handle(r, http.MethodGet, "/projects/{id}/issues/report", ReportsController, "issue_report", a.ReportsIssueReport,
		FindProject("id"), Authorize())
	// get 'projects/:id/issues/report/:detail', :to => 'reports#issue_report_details', :as => 'project_issues_report_details'
	a.Handle(r, http.MethodGet, "/projects/{id}/issues/report/{detail}", ReportsController, "issue_report_details", a.ReportsIssueReportDetails,
		FindProject("id"), Authorize())
}

// reportCell は aggregate_link / aggregate の 1 セル。
type reportCell struct {
	Count int
	Link  rails.HTML
}

// reportRowView はレポート表の 1 行。
type reportRowView struct {
	Name       string
	NameLink   rails.HTML
	ByStatus   []reportCell
	Open       reportCell
	Closed     reportCell
	Total      reportCell
	StatusData []int
}

// reportTable は reports/_simple・_details の表（rows・data・field_name）。
type reportTable struct {
	Field    string
	Statuses []*domain.IssueStatus
	Rows     []reportRowView
	// TotalByStatus / TotalOpen / TotalClosed / Total はフッターの aggregate。
	TotalByStatus []int
	TotalOpen     int
	TotalClosed   int
	Total         int
	// ChartRowsJSON / ChartDatasets1JSON / ChartDatasets2JSON は _details のグラフのデータ。
	ChartLabelsJSON    rails.HTML
	ChartDatasets1JSON rails.HTML
	ChartStatusesJSON  rails.HTML
	ChartDatasets2JSON rails.HTML
}

// Empty は @statuses.empty? or rows.empty?。
func (t *reportTable) Empty() bool { return len(t.Statuses) == 0 || len(t.Rows) == 0 }

// aggregateReport は ReportsHelper#aggregate（criteria に合う行の total の合計）。
// value が nil でなければ集計列の値（"" は NULL）、statusID が 0 でなければ status_id、closed が nil でなければ is_closed で絞る。
func aggregateReport(data []repository.ReportCount, value *string, statusID int64, closed *bool) int {
	n := 0
	for _, r := range data {
		if value != nil && r.Value != *value {
			continue
		}
		if statusID != 0 && r.StatusID != statusID {
			continue
		}
		if closed != nil && r.Closed != *closed {
			continue
		}
		n += r.Total
	}
	return n
}

// buildReportTable は表のデータを組み立てる（aggregate_path のリンクを含む）。
func (a *App) buildReportTable(c *Req, field string, statuses []*domain.IssueStatus, rows []repository.ReportRow, data []repository.ReportCount) *reportTable {
	t := &reportTable{Field: field, Statuses: statuses}
	f, tr := false, true
	// aggregate_path(@project, field, row, options)
	path := func(row repository.ReportRow, status any) string {
		p := c.Project
		if row.Project != nil {
			p = row.Project
		}
		var v any = "!*"
		if row.ID != 0 {
			v = row.ID
		}
		params := rails.NewHash("set_filter", 1, field, v)
		if status != nil {
			params.Set("status_id", status)
		}
		return helper.URLWithQuery("/projects/"+p.Identifier+"/issues", params)
	}
	cell := func(n int, href string) reportCell {
		if n > 0 {
			return reportCell{Count: n, Link: rails.LinkTo(strconv.Itoa(n), href, nil)}
		}
		return reportCell{Count: n, Link: "-"}
	}
	type dataset1 struct {
		Label  string `json:"label"`
		Hidden bool   `json:"hidden"`
		Data   []int  `json:"data"`
	}
	type dataset2 struct {
		Label string `json:"label"`
		Data  []int  `json:"data"`
	}
	var labels []string
	for _, row := range rows {
		value := ""
		if row.ID != 0 {
			value = strconv.FormatInt(row.ID, 10)
		}
		rv := reportRowView{Name: row.Name, NameLink: rails.LinkTo(row.Name, path(row, nil), nil)}
		for _, s := range statuses {
			n := aggregateReport(data, &value, s.ID, nil)
			rv.ByStatus = append(rv.ByStatus, cell(n, path(row, s.ID)))
			rv.StatusData = append(rv.StatusData, n)
		}
		rv.Open = cell(aggregateReport(data, &value, 0, &f), path(row, "o"))
		rv.Closed = cell(aggregateReport(data, &value, 0, &tr), path(row, "c"))
		rv.Total = cell(aggregateReport(data, &value, 0, nil), path(row, "*"))
		t.Rows = append(t.Rows, rv)
		labels = append(labels, row.Name)
	}
	var ds1 []dataset1
	var statusNames []string
	for i, s := range statuses {
		t.TotalByStatus = append(t.TotalByStatus, aggregateReport(data, nil, s.ID, nil))
		d := dataset1{Label: s.Name, Hidden: s.IsClosed, Data: []int{}}
		for _, rv := range t.Rows {
			d.Data = append(d.Data, rv.StatusData[i])
		}
		ds1 = append(ds1, d)
		statusNames = append(statusNames, s.Name)
	}
	var ds2 []dataset2
	for _, rv := range t.Rows {
		ds2 = append(ds2, dataset2{Label: rv.Name, Data: append([]int{}, rv.StatusData...)})
	}
	t.TotalOpen = aggregateReport(data, nil, 0, &f)
	t.TotalClosed = aggregateReport(data, nil, 0, &tr)
	t.Total = aggregateReport(data, nil, 0, nil)
	t.ChartLabelsJSON = rails.HTML(rails.ToJSON(nonNilStrings(labels)))
	t.ChartDatasets1JSON = rails.HTML(rails.ToJSON(nonNilSlice(ds1)))
	t.ChartStatusesJSON = rails.HTML(rails.ToJSON(nonNilStrings(statusNames)))
	t.ChartDatasets2JSON = rails.HTML(rails.ToJSON(nonNilSlice(ds2)))
	return t
}

func nonNilSlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// reportData は 1 つの集計（rows と data）。
type reportData struct {
	field string
	rows  []repository.ReportRow
	data  []repository.ReportCount
	title string
}

// loadReport は detail（tracker / version / priority / category / assigned_to / author / subproject）の集計を読む。
// 未知の detail なら nil。
func (a *App) loadReport(c *Req, detail string) (*reportData, error) {
	ctx := c.Ctx()
	p := c.Project
	withSub := a.Settings.Bool("display_subprojects_issues")
	issueCond, err := c.Authz().IssueVisibleCondition(ctx, authz.ConditionOptions{Project: p, WithSubprojects: withSub})
	if err != nil {
		return nil, err
	}
	counts := func(field string) ([]repository.ReportCount, error) {
		return repository.IssueReportCounts(ctx, a.DB, issueCond, field)
	}
	none := "[" + c.L("label_none") + "]"
	r := &reportData{}
	switch detail {
	case "tracker":
		cond, err := c.Authz().AllowedToCondition(ctx, "view_issues", authz.ConditionOptions{}, func(role *domain.Role, _ *domain.User) string {
			if role.PermissionsAllTrackers("view_issues") {
				return ""
			}
			if ids := role.PermissionsTrackerIDs("view_issues"); len(ids) > 0 {
				return "trackers.id IN (" + joinIDs(ids) + ")"
			}
			return "1=0"
		})
		if err != nil {
			return nil, err
		}
		r.field, r.title = "tracker_id", c.L("field_tracker")
		if r.rows, err = repository.ReportTrackers(ctx, a.DB, p.ID, withSub, cond); err != nil {
			return nil, err
		}
	case "version":
		r.field, r.title = "fixed_version_id", c.L("field_version")
		if r.rows, err = repository.ReportVersions(ctx, a.DB, p); err != nil {
			return nil, err
		}
		r.rows = append(r.rows, repository.ReportRow{Name: none})
	case "priority":
		r.field, r.title = "priority_id", c.L("field_priority")
		if r.rows, err = repository.ReportPriorities(ctx, a.DB); err != nil {
			return nil, err
		}
	case "category":
		r.field, r.title = "category_id", c.L("field_category")
		if r.rows, err = repository.ReportCategories(ctx, a.DB, p.ID); err != nil {
			return nil, err
		}
		r.rows = append(r.rows, repository.ReportRow{Name: none})
	case "assigned_to", "author":
		format := a.Settings.String("user_format")
		users, err := repository.ReportUsers(ctx, a.DB, p.ID, format)
		if err != nil {
			return nil, err
		}
		page := c.Page()
		for _, u := range users {
			r.rows = append(r.rows, repository.ReportRow{ID: u.ID, Name: page.UserName(u)})
		}
		if detail == "assigned_to" {
			r.field, r.title = "assigned_to_id", c.L("field_assigned_to")
			if a.Settings.Bool("issue_group_assignment") {
				groups, err := repository.ReportGroups(ctx, a.DB, p.ID)
				if err != nil {
					return nil, err
				}
				for _, g := range groups {
					r.rows = append(r.rows, repository.ReportRow{ID: g.ID, Name: helper.GroupName(page, &domain.Group{Principal: *g})})
				}
			}
			r.rows = append(r.rows, repository.ReportRow{Name: (&domain.User{Principal: domain.Principal{Firstname: none}}).Name(format)})
		} else {
			r.field, r.title = "author_id", c.L("field_author")
		}
	case "subproject":
		r.field, r.title = "project_id", c.L("field_subproject")
		cond, err := c.Authz().VisibleCondition(ctx, authz.ConditionOptions{})
		if err != nil {
			return nil, err
		}
		ps, err := repository.LoadProjects(ctx, a.DB, "projects.id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = ? AND depth > 0) AND ("+cond+")", p.ID)
		if err != nil {
			return nil, err
		}
		for _, sp := range ps {
			r.rows = append(r.rows, repository.ReportRow{ID: sp.ID, Name: sp.Name, Project: sp})
		}
		// Issue.by_subproject（サブプロジェクトを含めて数え、自身のチケットを除く）
		subCond, err := c.Authz().IssueVisibleCondition(ctx, authz.ConditionOptions{Project: p, WithSubprojects: true})
		if err != nil {
			return nil, err
		}
		data, err := repository.IssueReportCounts(ctx, a.DB, subCond, "project_id")
		if err != nil {
			return nil, err
		}
		own := strconv.FormatInt(p.ID, 10)
		for _, d := range data {
			if d.Value != own {
				r.data = append(r.data, d)
			}
		}
		return r, nil
	default:
		return nil, nil
	}
	if r.data, err = counts(r.field); err != nil {
		return nil, err
	}
	return r, nil
}

// ReportsIssueReport は reports#issue_report（GET /projects/:id/issues/report）。
func (a *App) ReportsIssueReport(c *Req) {
	ctx := c.Ctx()
	statuses, err := repository.ReportStatuses(ctx, a.DB, c.Project.ID)
	if err != nil {
		a.serverError(c, err)
		return
	}
	tables := map[string]*reportTable{}
	for _, d := range []string{"tracker", "version", "priority", "category", "assigned_to", "author", "subproject"} {
		r, err := a.loadReport(c, d)
		if err != nil {
			a.serverError(c, err)
			return
		}
		tables[d] = a.buildReportTable(c, r.field, statuses, r.rows, r.data)
	}
	leaf, err := repository.IsProjectLeaf(ctx, a.DB, c.Project.ID)
	if err != nil {
		a.serverError(c, err)
		return
	}
	c.Render("reports/issue_report", map[string]any{
		"Tables":      tables,
		"HasChildren": !leaf,
		"ReportPath":  "/projects/" + c.Project.Identifier + "/issues/report",
	})
}

// ReportsIssueReportDetails は reports#issue_report_details（GET /projects/:id/issues/report/:detail(.csv)）。
func (a *App) ReportsIssueReportDetails(c *Req) {
	ctx := c.Ctx()
	detail := c.Params().String("detail")
	statuses, err := repository.ReportStatuses(ctx, a.DB, c.Project.ID)
	if err != nil {
		a.serverError(c, err)
		return
	}
	r, err := a.loadReport(c, detail)
	if err != nil {
		a.serverError(c, err)
		return
	}
	if r == nil {
		c.Render404("")
		return
	}
	t := a.buildReportTable(c, r.field, statuses, r.rows, r.data)
	reportPath := "/projects/" + c.Project.Identifier + "/issues/report"
	switch httpx.Format(c.R) {
	case "csv":
		// issue_report_details_to_csv
		head := []string{""}
		for _, s := range statuses {
			head = append(head, s.Name)
		}
		head = append(head, c.L("label_open_issues_plural"), c.L("label_closed_issues_plural"), c.L("label_total"))
		rows := [][]string{head}
		for _, rv := range t.Rows {
			line := []string{rv.Name}
			for _, n := range rv.StatusData {
				line = append(line, strconv.Itoa(n))
			}
			line = append(line, strconv.Itoa(rv.Open.Count), strconv.Itoa(rv.Closed.Count), strconv.Itoa(rv.Total.Count))
			rows = append(rows, line)
		}
		c.sendCSV("report-"+detail+".csv", rows, false)
	case "", "html":
		c.Render("reports/issue_report_details", map[string]any{
			"Table":       t,
			"ReportTitle": r.title,
			"Detail":      detail,
			"ReportPath":  reportPath,
			"CSVPath":     reportPath + "/" + detail + ".csv",
		})
	default:
		c.RenderError(http.StatusNotAcceptable, "")
	}
}
