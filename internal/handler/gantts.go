// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/urlroot"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// GanttsController（app/controllers/gantts_controller.rb）。menu_item :gantt。
var GanttsController = &Controller{Name: "gantts", MainMenu: true,
	MenuItem: func(string) string { return "gantt" }}

// routesGantts は gantts コントローラのルートを登録する。
func (a *App) routesGantts(r Router) {
	// get '/projects/:project_id/issues/gantt', :to => 'gantts#show', :as => 'project_gantt'
	a.Handle(r, http.MethodGet, "/projects/{project_id}/issues/gantt", GanttsController, "show", a.GanttsShow, FindOptionalProject())
	// get '/issues/gantt', :to => 'gantts#show'
	a.Handle(r, http.MethodGet, "/issues/gantt", GanttsController, "show", a.GanttsShow, FindOptionalProject())
}

// ganttPath は gantts#show のパス（project_gantt_path / issues_gantt_path）。
func ganttPath(p *domain.Project) string {
	if p != nil {
		return "/projects/" + p.Identifier + "/issues/gantt"
	}
	return "/issues/gantt"
}

// GanttsShow は gantts#show（GET /issues/gantt, /projects/:project_id/issues/gantt）。
func (a *App) GanttsShow(c *Req) {
	ctx := c.Ctx()
	g, err := a.newGanttChart(c)
	if err != nil {
		a.serverError(c, err)
		return
	}
	g.Project = c.Project
	g.l = a.newIssueLookup(c)
	q, _, err := a.calendarRetrieveIssueQuery(c)
	if err != nil {
		switch {
		case errors.Is(err, query.ErrNotFound):
			// IssueQuery の find が ActiveRecord::RecordNotFound を投げ、gantts では rescue されない（public/404.html）
			renderPublic404(c)
		case errors.Is(err, query.ErrUnauthorized):
			c.DenyAccess()
		default:
			a.queryFailed(c, err)
		}
		return
	}
	q.GroupBy = ""
	qv, err := a.newQueryView(c, q, "IssueQuery", ganttPath(c.Project))
	if err != nil {
		a.queryFailed(c, err)
		return
	}
	if qv.Valid {
		g.query = q
	}
	format := formatOf(c)
	switch format {
	case "html", "pdf":
	default:
		// PNG は MiniMagick が無い Redmine と同じく受け付けない。
		renderUnknownFormat(c)
		return
	}
	if err := g.load(ctx); err != nil {
		a.queryFailed(c, err)
		return
	}
	if g.l.err != nil {
		a.serverError(c, g.l.err)
		return
	}
	if format == "pdf" {
		a.ganttShowPDF(c, g)
		return
	}
	data, err := a.ganttViewData(c, g, q, qv)
	if err != nil {
		a.queryFailed(c, err)
		return
	}
	var ropts []RenderOptions
	if httpx.IsXHR(c.R) {
		ropts = append(ropts, RenderOptions{Layout: view.NoLayout})
	}
	c.Render("gantts/show", data, ropts...)
}

// ganttColumnView は選択列（gantt_<name>_column）の td。
type ganttColumnView struct {
	Name    string
	Caption string
	Last    bool
	Content rails.HTML
}

// ganttViewData は gantts/show.html.erb の値を組み立てる（ビュー内の計算と @gantt.render を含む）。
func (a *App) ganttViewData(c *Req, g *ganttChart, q *query.Query, qv *queryView) (map[string]any, error) {
	ctx := c.Ctx()
	path := ganttPath(c.Project)
	reqParams := pageQueryParameters(c)
	withReq := func(h *rails.Hash) string {
		qp := pageQueryParameters(c)
		for _, e := range h.Entries() {
			qp.Set(e.Key, e.Value)
		}
		return helper.URLWithQuery(path, qp)
	}
	data := map[string]any{
		"Query": q,
		"qv":    qv,
		"G":     g,
		"Valid": qv.Valid,
	}

	// form_tag({:controller => 'gantts', :action => 'show', :project_id => @project, :month, :year, :months})
	formParams := rails.NewHash()
	for _, k := range []string{"month", "year", "months"} {
		if v, ok := c.Params().Get(k); ok {
			formParams.Set(k, v)
		}
	}
	data["FormAction"] = helper.URLWithQuery(path, formParams)
	data["CheckDrawSelectedColumns"] = ganttCheckBox("draw_selected_columns", q.DrawSelectedColumns(),
		rails.NewHash("data-enables", "#list-definition .query-columns select, #list-definition .query-columns input"))
	data["CheckDrawRelations"] = ganttCheckBox("draw_relations", q.DrawRelations(), nil)
	data["CheckDrawProgressLine"] = ganttCheckBox("draw_progress_line", q.DrawProgressLine(), nil)

	// gantt_zoom_link
	page := c.Page()
	zoomIn := a.Helpers.Icon(page, "zoom-in", c.L("text_zoom_in"), nil)
	if g.Zoom < 4 {
		data["ZoomIn"] = rails.LinkTo(zoomIn, withReq(g.Params().Set("zoom", g.Zoom+1)), rails.NewHash("class", "icon icon-zoom-in"))
	} else {
		data["ZoomIn"] = rails.ContentTag("span", zoomIn, rails.NewHash("class", "icon icon-zoom-in"))
	}
	zoomOut := a.Helpers.Icon(page, "zoom-out", c.L("text_zoom_out"), nil)
	if g.Zoom > 1 {
		data["ZoomOut"] = rails.LinkTo(zoomOut, withReq(g.Params().Set("zoom", g.Zoom-1)), rails.NewHash("class", "icon icon-zoom-out"))
	} else {
		data["ZoomOut"] = rails.ContentTag("span", zoomOut, rails.NewHash("class", "icon icon-zoom-out"))
	}
	// link_to_previous_month / link_to_next_month
	year, month := g.YearFrom, g.MonthFrom
	py, pm := year, month-1
	if month == 1 {
		py, pm = year-1, 12
	}
	pname := c.Loc.MonthName(pm)
	if pm == 12 {
		pname += " " + strconv.Itoa(py)
	}
	ny, nm := year, month+1
	if month == 12 {
		ny, nm = year+1, 1
	}
	nname := c.Loc.MonthName(nm)
	if nm == 1 {
		nname += " " + strconv.Itoa(ny)
	}
	data["PrevMonth"] = rails.LinkTo("« "+pname, withReq(rails.NewHash("year", py, "month", pm)), rails.NewHash("accesskey", "p"))
	data["NextMonth"] = rails.LinkTo(nname+" »", withReq(rails.NewHash("year", ny, "month", nm)), rails.NewHash("accesskey", "n"))
	data["MonthsField"] = rails.Tag("input", rails.NewHash("type", "number", "name", "months", "id", "months", "value", g.Months,
		"min", 1, "max", a.Settings.Int("gantt_months_limit"), "autocomplete", "false"))
	data["MonthSelect"] = selectMonthHTML(c, g.MonthFrom)
	data["YearSelect"] = selectYearHTML(g.YearFrom)
	data["ClearURL"] = path + "?set_filter=1"
	data["CanSave"] = qv.NewRecord() && qv.CanSave
	data["Editable"] = qv.Editable
	data["EditQueryURL"] = "/queries/" + strconv.FormatInt(q.ID, 10) + "/edit?gantt=1"
	data["QueryURL"] = "/queries/" + strconv.FormatInt(q.ID, 10) + "?gantt=1"
	data["BackURL"] = urlroot.Path(helper.URLWithQuery(path, reqParams))
	data["DrawTypesJSON"] = ganttDrawTypesJSON()
	data["UnavailableColumns"] = strings.Join(ganttUnavailableColumns, ",")

	sidebar, err := a.sidebarQueriesHTML(c, query.KindIssue, q, path)
	if err != nil {
		return nil, err
	}
	data["SidebarQueries"] = sidebar

	if !qv.Valid {
		return data, nil
	}

	// ---- show.html.erb の計算
	zoom := 1
	for i := 0; i < g.Zoom; i++ {
		zoom *= 2
	}
	subjectWidth := 330
	headerHeight := 18
	headersHeight := headerHeight
	showWeeks, showDays, showDayNum := false, false, false
	if g.Zoom > 1 {
		showWeeks = true
		headersHeight = 2 * headerHeight
		if g.Zoom > 2 {
			showDays = true
			headersHeight = 3 * headerHeight
			if g.Zoom > 3 {
				showDayNum = true
				headersHeight = 4 * headerHeight
			}
		}
	}
	nDays := int(ganttDays(g.DateTo) - ganttDays(g.DateFrom) + 1)
	gWidth := nDays * zoom
	opts := ganttOptions{top: headersHeight + 8, zoom: zoom, gWidth: gWidth, subjectWidth: subjectWidth}
	g.render(opts)
	gHeight := max(20*(g.NumberOfRows()+6)+150, 206)
	tHeight := gHeight + headersHeight
	itoa := strconv.Itoa

	data["Truncated"] = g.Truncated
	data["TruncatedNotice"] = c.L("notice_gantt_chart_truncated", map[string]any{"max": g.MaxRows})
	subjectTD := subjectWidth + 2
	if q.DrawSelectedColumns() {
		subjectTD = subjectWidth + 1
	}
	data["SubjectTDWidth"] = subjectTD
	data["SubjectsContainerStyle"] = "position:relative;" + "height: " + itoa(tHeight+24) + "px;" + "width: " + itoa(subjectWidth+1) + "px;"
	containerClass := "gantt_subjects_container "
	if q.DrawSelectedColumns() {
		containerClass += "draw_selected_columns"
	}
	data["SubjectsContainerClass"] = containerClass
	data["SubjectsHdrStyle1"] = "width: " + itoa(subjectWidth+1) + "px;" + "height: " + itoa(headersHeight) + "px;" + "background: #eee;"
	data["SubjectsHdrStyle2"] = "z-index: 1;" + "width: " + itoa(subjectWidth+1) + "px;" + "height: " + itoa(tHeight) + "px;" + "overflow: hidden;"
	data["Subjects"] = rails.HTML(g.subjects.String())

	cols, err := q.Columns(ctx)
	if err != nil {
		return nil, err
	}
	var colViews []ganttColumnView
	for i, col := range cols {
		if containsString(ganttUnavailableColumns, col.Name) {
			continue
		}
		content := g.selectedColumnContent(col, ganttOptions{top: headersHeight + 8, zoom: zoom, gWidth: gWidth})
		colViews = append(colViews, ganttColumnView{Name: strings.ReplaceAll(col.Name, ".", "_"), Caption: qv.Caption(col),
			Last: i == len(cols)-1, Content: rails.HTML(content)})
	}
	if g.l.err != nil {
		return nil, g.l.err
	}
	data["Columns"] = colViews
	data["ColumnContainerStyle"] = "position: relative;" + "height: " + itoa(tHeight+24) + "px;"
	data["ColumnHdrStyle1"] = "height: " + itoa(tHeight) + "px;" + "overflow: hidden;"
	data["ColumnHdrStyle2"] = "height: " + itoa(headersHeight) + "px;" + "background: #eee;"
	data["AreaHeight"] = tHeight + 24
	data["AreaHeaders"] = rails.HTML(g.areaHeaders(c, zoom, gWidth, gHeight, headerHeight, headersHeight, showWeeks, showDays, showDayNum))
	data["Lines"] = rails.HTML(g.lines.String())
	if !g.today.Before(g.DateFrom) && !g.today.After(g.DateTo) {
		todayLeft := int(ganttDays(g.today)-ganttDays(g.DateFrom)+1)*zoom - 1
		data["TodayStyle"] = "position: absolute;" + "height: " + itoa(gHeight) + "px;" + "top: " + itoa(headersHeight+1) + "px;" +
			"left: " + itoa(todayLeft) + "px;" + "width:10px;" + "border-left: 1px dashed red;"
	}
	data["DrawAreaStyle"] = "position: absolute;" + "height: " + itoa(gHeight) + "px;" + "top: " + itoa(headersHeight+1) + "px;" +
		"left: 0px;" + "width: " + itoa(gWidth-1) + "px;"

	// ページ送り・他形式
	data["PrevURL"] = withReq(g.ParamsPrevious())
	data["NextURL"] = withReq(g.ParamsNext())
	pdf := httpx.NewParams()
	skip := []string{"page", "format", "controller", "action", "project_id", "zoom", "year", "month", "months"}
	reqParams.Each(func(k string, v any) {
		if !containsString(skip, k) {
			pdf.Set(k, v)
		}
	})
	for _, e := range g.Params().Entries() {
		if e.Key != "controller" && e.Key != "action" && e.Key != "project_id" {
			pdf.Set(e.Key, e.Value)
		}
	}
	data["PDFURL"] = helper.URLWithQuery(path+".pdf", pdf)
	return data, nil
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// ganttCheckBox は check_box 'query', name, :id => name（hidden の "0" と checkbox の "1"）。
func ganttCheckBox(name string, checked bool, extra *rails.Hash) rails.HTML {
	field := "query[" + name + "]"
	hidden := rails.Tag("input", rails.NewHash("name", field, "type", "hidden", "value", "0", "autocomplete", "off"))
	opts := rails.NewHash("id", name)
	if extra != nil {
		opts.Update(extra)
	}
	opts.Set("type", "checkbox")
	opts.Set("value", "1")
	if checked {
		opts.Set("checked", "checked")
	}
	opts.Set("name", field)
	return hidden + rails.Tag("input", opts)
}

// areaHeaders は #gantt_area の見出し（月・週・日番号・曜日）の HTML（ERB の空白を含めて再現する）。
func (g *ganttChart) areaHeaders(c *Req, zoom, gWidth, gHeight, headerHeight, headersHeight int, showWeeks, showDays, showDayNum bool) string {
	itoa := strconv.Itoa
	var b strings.Builder
	b.WriteString(string(rails.ContentTag("div", rails.HTML("&nbsp;"), rails.NewHash("style",
		"width: "+itoa(gWidth-1)+"px;"+"height: "+itoa(headersHeight)+"px;"+"background: #eee;", "class", "gantt_hdr"))))
	b.WriteString("\n\n")
	path := ganttPath(g.Project)

	// Months headers
	monthF := g.DateFrom
	left := 0
	height := headerHeight + gHeight
	if showWeeks {
		height = headerHeight
	}
	for i := 0; i < g.Months; i++ {
		next := monthF.AddDate(0, 1, 0)
		width := int(ganttDays(next)-ganttDays(monthF))*zoom - 1
		style := "left: " + itoa(left) + "px;" + "width: " + itoa(width) + "px;" + "height: " + itoa(height) + "px;"
		params := rails.NewHash("zoom", g.Zoom, "year", monthF.Year(), "month", int(monthF.Month()), "months", g.Months)
		link := rails.LinkTo(itoa(monthF.Year())+"-"+itoa(int(monthF.Month())), helper.URLWithQuery(path, params),
			rails.NewHash("title", c.Loc.MonthName(int(monthF.Month()))+" "+itoa(monthF.Year())))
		b.WriteString("  " + string(rails.Tag("div", rails.NewHash("style", style, "class", "gantt_hdr"), true)))
		b.WriteString("\n    " + string(link) + "\n</div>")
		left = left + width + 1
		monthF = next
	}
	b.WriteString("\n")

	// Weeks headers
	if showWeeks {
		left = 0
		height = headerHeight - 1 + gHeight
		if showDays {
			height = headerHeight - 1
		}
		cwday := isoWeekday(g.DateFrom)
		var weekF time.Time
		if cwday == 1 {
			weekF = g.DateFrom
		} else {
			weekF = g.DateFrom.AddDate(0, 0, 7-cwday+1)
			width := (7-cwday+1)*zoom - 1
			style := "left: " + itoa(left) + "px;" + "top: 19px;" + "width: " + itoa(width) + "px;" + "height: " + itoa(height) + "px;"
			b.WriteString("    " + string(rails.ContentTag("div", rails.HTML("&nbsp;"), rails.NewHash("style", style, "class", "gantt_hdr"))) + "\n")
			left = left + width + 1
		}
		for !weekF.After(g.DateTo) {
			var width int
			if !weekF.AddDate(0, 0, 6).After(g.DateTo) {
				width = 7*zoom - 1
			} else {
				width = int(ganttDays(g.DateTo)-ganttDays(weekF)+1)*zoom - 1
			}
			style := "left: " + itoa(left) + "px;" + "top: 19px;" + "width: " + itoa(width) + "px;" + "height: " + itoa(height) + "px;"
			b.WriteString("    " + string(rails.Tag("div", rails.NewHash("style", style, "class", "gantt_hdr"), true)))
			b.WriteString("\n      <small>")
			if width >= 16 {
				_, wk := weekF.ISOWeek()
				b.WriteString("\n        " + itoa(wk) + "\n")
			} else {
				b.WriteString("\n")
			}
			b.WriteString("</small></div>")
			left = left + width + 1
			weekF = weekF.AddDate(0, 0, 7)
		}
	}
	b.WriteString("\n")

	nDays := int(ganttDays(g.DateTo) - ganttDays(g.DateFrom) + 1)
	// Day numbers headers
	if showDayNum {
		left = 0
		height = gHeight + headerHeight*2 - 1
		wday := isoWeekday(g.DateFrom)
		dayNum := g.DateFrom
		for i := 0; i < nDays; i++ {
			width := zoom - 1
			style := "left:" + itoa(left) + "px;" + "top:37px;" + "width:" + itoa(width) + "px;" + "height:" + itoa(height) + "px;" + "font-size:0.7em;"
			cls := "gantt_hdr"
			if containsInt(g.nonWorking, wday) {
				cls += " nwday"
			}
			b.WriteString("    " + string(rails.Tag("div", rails.NewHash("style", style, "class", cls), true)))
			b.WriteString("\n      " + itoa(dayNum.Day()) + "\n</div>")
			left = left + width + 1
			dayNum = dayNum.AddDate(0, 0, 1)
			wday++
			if wday > 7 {
				wday = 1
			}
		}
	}
	b.WriteString("\n")

	// Days headers
	if showDays {
		left = 0
		height = gHeight + headerHeight - 1
		top := 37
		if showDayNum {
			top = 55
		}
		for i := 0; i < nDays; i++ {
			d := g.DateFrom.AddDate(0, 0, i)
			width := zoom - 1
			style := "left: " + itoa(left) + "px;" + "top: " + itoa(top) + "px;" + "width: " + itoa(width) + "px;" + "height: " + itoa(height) + "px;" + "font-size:0.7em;"
			cls := "gantt_hdr"
			if containsInt(g.nonWorking, isoWeekday(d)) {
				cls += " nwday"
			}
			b.WriteString("    " + string(rails.Tag("div", rails.NewHash("style", style, "class", cls), true)))
			b.WriteString("\n      " + string(rails.H(c.Loc.DayLetter(isoWeekday(d)))) + "\n</div>")
			left = left + width + 1
		}
	}
	b.WriteString("\n")
	return b.String()
}

// isoWeekday は Date#cwday（月曜 = 1 … 日曜 = 7）。
func isoWeekday(t time.Time) int {
	w := int(t.Weekday())
	if w == 0 {
		return 7
	}
	return w
}

func containsInt(xs []int, n int) bool {
	for _, x := range xs {
		if x == n {
			return true
		}
	}
	return false
}
