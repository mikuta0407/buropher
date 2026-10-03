package handler

import (
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/calendar"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/urlroot"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
	"github.com/mikuta0407/buropher/web"
)

// CalendarsController（app/controllers/calendars_controller.rb）。menu_item :calendar。
var CalendarsController = &Controller{Name: "calendars", MainMenu: true,
	MenuItem: func(string) string { return "calendar" }}

// routesCalendars は calendars コントローラのルートを登録する。
func (a *App) routesCalendars(r Router) {
	// get '/projects/:project_id/issues/calendar', :to => 'calendars#show', :as => 'project_calendar'
	a.Handle(r, http.MethodGet, "/projects/{project_id}/issues/calendar", CalendarsController, "show", a.CalendarsShow, FindOptionalProject())
	// get '/issues/calendar', :to => 'calendars#show'
	a.Handle(r, http.MethodGet, "/issues/calendar", CalendarsController, "show", a.CalendarsShow, FindOptionalProject())
}

// calendarRetrieveIssueQuery は QueriesHelper#retrieve_query(IssueQuery, true)。
func (a *App) calendarRetrieveIssueQuery(c *Req) (*query.Query, *query.Env, error) {
	env, err := query.NewEnv(c.Ctx(), a.DB, c.User, a.Settings)
	if err != nil {
		return nil, nil, err
	}
	env.L = c.Loc
	env.Now = a.now
	// session[:issue_query] はチケット一覧と共有する（同じ形式で読み書きする）
	sess := c.issueQuerySession()
	p := query.ParseParams(requestValues(c))
	q, newSess, err := query.Retrieve(c.Ctx(), env, query.KindIssue, c.Project, p, sess, query.RetrieveOptions{UseSession: true})
	if err != nil {
		return nil, nil, err
	}
	if newSess != nil {
		c.setIssueQuerySession(newSess)
	}
	return q, env, nil
}

// newCalendar は Redmine::Helpers::Calendar.new(date, current_language, period)
// （週の開始日・今日・休業日・翻訳を設定済み）。
func (a *App) newCalendar(c *Req, date time.Time, period calendar.Period) *calendar.Calendar {
	cal := calendar.New(date, c.Loc.StartOfWeek(), period)
	cal.Loc = c.Loc
	cal.Today = a.userToday(c)
	var nwd []int
	for _, s := range a.Settings.Strings("non_working_week_days") {
		nwd = append(nwd, int(settings.RubyToI(s)))
	}
	if len(nwd) < 7 {
		cal.NonWorkingDays = nwd
	}
	return cal
}

// calendarEvents はチケット（issueIDs の順）とバージョン（versionIDs の順）をカレンダーのイベントにする
// （link_to_issue・css_classes・render_issue_tooltip の値を用意する）。
func (a *App) calendarEvents(c *Req, issueIDs, versionIDs []int64) ([]*calendar.Event, error) {
	ctx := c.Ctx()
	issues, err := repository.CalendarIssuesByIDs(ctx, a.DB, issueIDs)
	if err != nil {
		return nil, err
	}
	versions, err := repository.CalendarVersionsByIDs(ctx, a.DB, versionIDs)
	if err != nil {
		return nil, err
	}
	var projectIDs, principalIDs []int64
	for _, i := range issues {
		projectIDs = append(projectIDs, i.ProjectID)
		if i.AssignedToID.Valid {
			principalIDs = append(principalIDs, i.AssignedToID.Int64)
		}
	}
	for _, v := range versions {
		projectIDs = append(projectIDs, v.ProjectID)
	}
	projects := map[int64]*domain.Project{}
	if len(projectIDs) > 0 {
		ps, err := repository.LoadProjects(ctx, a.DB, "projects.id IN ("+joinIDs(projectIDs)+")")
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			projects[p.ID] = p
		}
	}
	principals, err := repository.PrincipalMapByIDs(ctx, a.DB, principalIDs)
	if err != nil {
		return nil, err
	}
	var userIDs []int64
	for id, p := range principals {
		if p.Kind.IsUser() {
			userIDs = append(userIDs, id)
		}
	}
	users, err := repository.UsersByIDs(ctx, a.DB, userIDs)
	if err != nil {
		return nil, err
	}
	page := c.Page()
	r := a.Helpers.WikiRenderer(page)
	var out []*calendar.Event
	for _, i := range issues {
		ri := &i.RefIssue
		e := &calendar.Event{IsIssue: true, ID: i.ID, Project: projects[i.ProjectID],
			StartDate: dateOrNilPtr(i.StartDate), DueDate: dateOrNilPtr(i.DueDate),
			CSSClasses: r.IssueCSSClasses(ri),
			LinkShort:  r.LinkToIssue(ri, redmine.LinkToIssueOptions{Truncate: 30}),
			Link:       r.LinkToIssue(ri, redmine.LinkToIssueOptions{}),
			StatusName: i.StatusName, Closed: i.StatusClosed, ClosedOn: i.ClosedOn.Ptr(), PriorityName: i.PriorityName}
		if i.AssignedToID.Valid {
			if p := principals[i.AssignedToID.Int64]; p != nil {
				if u := users[p.ID]; u != nil {
					e.AssignedTo = u
					e.AssignedToName = page.UserName(u)
				} else {
					g := &domain.Group{Principal: *p}
					e.AssignedToName = helper.GroupName(page, g)
					e.AssignedToAvatar = a.calendarGroupAvatar(c)
				}
			}
		}
		out = append(out, e)
	}
	for _, v := range versions {
		out = append(out, &calendar.Event{ID: v.ID, Project: projects[v.ProjectID], VersionName: v.Name,
			StartDate: dateOrNilPtr(v.StartDate), DueDate: dateOrNilPtr(v.EffectiveDate)})
	}
	return out, nil
}

// calendarGroupAvatar は avatar(group, :size => '13', :title => l(:field_assigned_to))（group_avatar）。
func (a *App) calendarGroupAvatar(c *Req) template.HTML {
	src := "/images/group.png"
	if a.Assets != nil {
		src = a.Assets.AssetPath("group.png")
	}
	return rails.Tag("img", rails.NewHash("alt", "", "title", c.L("field_assigned_to"), "class", "group-avatar avatar",
		"src", src, "width", "13", "height", "13"))
}

// renderPublic404 は Rails が rescue されない ActiveRecord::RecordNotFound に返す public/404.html。
func renderPublic404(c *Req) {
	c.Halt()
	b, err := fs.ReadFile(web.Public(), "404.html")
	if err != nil {
		http.NotFound(c.W, c.R)
		return
	}
	c.W.Header().Set("Content-Type", "text/html; charset=utf-8")
	c.W.WriteHeader(http.StatusNotFound)
	_, _ = c.W.Write(b)
}

func dateOrNilPtr(d db.NullDate) *time.Time {
	if !d.Valid {
		return nil
	}
	t := calendar.DateOnly(d.Date.Time)
	return &t
}

func joinIDs(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}

// maxCalendarYear はカレンダー・ガントで受け付ける年の上限。
const maxCalendarYear = 9999

// CalendarsShow は calendars#show（GET /issues/calendar, /projects/:project_id/issues/calendar）。
func (a *App) CalendarsShow(c *Req) {
	ctx := c.Ctx()
	p := c.Params()
	today := a.userToday(c)
	year, month := 0, 0
	// 上限は buropher の制限（time.Date は巨大な年を黙って折り返し、前後の年の列挙も溢れる）
	if y := int(httpx.RubyToI(p.String("year"))); y > 1900 && y <= maxCalendarYear {
		year = y
		if m := int(httpx.RubyToI(p.String("month"))); m > 0 && m < 13 {
			month = m
		}
	}
	if year == 0 {
		year = today.Year()
	}
	if month == 0 {
		month = int(today.Month())
	}
	cal := a.newCalendar(c, time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC), calendar.Month)
	cal.BackURL = helper.URLWithQuery(c.R.URL.Path, pageQueryParameters(c))

	q, env, err := a.calendarRetrieveIssueQuery(c)
	if err != nil {
		switch {
		case errors.Is(err, query.ErrNotFound):
			// IssueQuery の find が ActiveRecord::RecordNotFound を投げ、calendars では rescue されない（public/404.html）
			renderPublic404(c)
		case errors.Is(err, query.ErrUnauthorized):
			c.DenyAccess()
		default:
			a.serverError(c, err)
		}
		return
	}
	q.GroupBy = ""
	q.SetSortCriteria(nil)
	errMsgs, err := q.Errors(ctx)
	if err != nil {
		a.serverError(c, err)
		return
	}
	if len(errMsgs) == 0 {
		start, end := cal.Startdt, cal.Enddt
		sd := db.NewDate(start.Year(), start.Month(), start.Day())
		ed := db.NewDate(end.Year(), end.Month(), end.Day())
		issueIDs, err := q.IssueIDs(ctx, query.ListOptions{
			Conditions:    "((issues.start_date BETWEEN ? AND ?) OR (issues.due_date BETWEEN ? AND ?))",
			ConditionArgs: []any{sd, ed, sd, ed},
		})
		if err != nil {
			a.serverError(c, err)
			return
		}
		versionIDs, err := q.VersionIDs(ctx, query.ListOptions{
			Conditions: "versions.effective_date BETWEEN ? AND ?", ConditionArgs: []any{sd, ed},
		})
		if err != nil {
			a.serverError(c, err)
			return
		}
		events, err := a.calendarEvents(c, issueIDs, versionIDs)
		if err != nil {
			a.serverError(c, err)
			return
		}
		cal.SetEvents(events)
	}

	qv := &calendarQueryView{userQueryView: &userQueryView{q: q, env: env, c: c, app: a}}
	canSave := false
	if q.ID == 0 {
		// User.current.allowed_to?(:save_queries, @project, :global => true)
		if c.Project != nil {
			canSave = c.AllowedTo(domain.Perm("save_queries"), c.Project)
		} else {
			canSave = c.AllowedToGlobally(domain.Perm("save_queries"))
		}
	}
	editable := false
	if q.ID != 0 {
		if editable, err = q.EditableBy(ctx, c.Authz()); err != nil {
			a.serverError(c, err)
			return
		}
	}
	sidebar, err := a.renderSidebarIssueQueries(c, q, env)
	if err != nil {
		a.serverError(c, err)
		return
	}
	path := strings.TrimSuffix(c.R.URL.Path, ".html")
	monthLink := func(name string, y, m int, accesskey string) rails.HTML {
		qp := pageQueryParameters(c)
		qp.Set("year", y)
		qp.Set("month", m)
		return rails.LinkTo(name, helper.URLWithQuery(path, qp), rails.NewHash("accesskey", accesskey))
	}
	// link_to_previous_month / link_to_next_month
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
	formAction := "/issues/calendar"
	clearURL := "/issues/calendar?set_filter=1"
	newQueryPath := "/queries/new"
	filtersURL := "/queries/filter?type=IssueQuery"
	if c.Project != nil {
		pp := "/projects/" + c.Project.Identifier
		formAction = pp + "/issues/calendar"
		clearURL = formAction + "?set_filter=1"
		newQueryPath = pp + "/queries/new"
		filtersURL = "/queries/filter?project_id=" + strconv.FormatInt(c.Project.ID, 10) + "&type=IssueQuery"
	}
	var ropts []RenderOptions
	if httpx.IsXHR(c.R) {
		ropts = append(ropts, RenderOptions{Layout: view.NoLayout})
	}
	c.Render("calendars/show", map[string]any{
		"Query":        q,
		"QueryView":    qv,
		"Calendar":     cal,
		"Errors":       errMsgs,
		"FormAction":   formAction,
		"FiltersURL":   urlroot.Path(filtersURL),
		"PrevLink":     monthLink("« "+pname, py, pm, "p"),
		"NextLink":     monthLink(nname+" »", ny, nm, "n"),
		"MonthSelect":  selectMonthHTML(c, month),
		"YearSelect":   selectYearHTML(year),
		"ClearURL":     clearURL,
		"CanSave":      canSave,
		"NewQueryPath": urlroot.Path(newQueryPath),
		"Editable":     editable,
		"EditQueryURL": "/queries/" + strconv.FormatInt(q.ID, 10) + "/edit?calendar=1",
		"QueryURL":     "/queries/" + strconv.FormatInt(q.ID, 10) + "?calendar=1",
		"Sidebar":      sidebar,
	}, ropts...)
}

// selectMonthHTML は select_month(month, :prefix => "month", :discard_type => true)。
func selectMonthHTML(c *Req, month int) rails.HTML {
	var b strings.Builder
	b.WriteString(`<select id="month" name="month">` + "\n")
	for m := 1; m <= 12; m++ {
		sel := ""
		if m == month {
			sel = ` selected="selected"`
		}
		b.WriteString(`<option value="` + strconv.Itoa(m) + `"` + sel + `>` + string(rails.H(c.Loc.MonthName(m))) + "</option>\n")
	}
	b.WriteString("</select>\n")
	return rails.HTML(b.String())
}

// selectYearHTML は select_year(year, :prefix => "year", :discard_type => true)（前後 5 年）。
func selectYearHTML(year int) rails.HTML {
	var b strings.Builder
	b.WriteString(`<select id="year" name="year">` + "\n")
	for d := -5; d <= 5; d++ {
		y := year + d
		sel := ""
		if y == year {
			sel = ` selected="selected"`
		}
		s := strconv.Itoa(y)
		b.WriteString(`<option value="` + s + `"` + sel + `>` + s + "</option>\n")
	}
	b.WriteString("</select>\n")
	return rails.HTML(b.String())
}

// renderSidebarIssueQueries は render_sidebar_queries(IssueQuery, @project)（calendars#show のリンク）。
// TODO(query): issues/_sidebar の実装と共通化する。
func (a *App) renderSidebarIssueQueries(c *Req, current *query.Query, env *query.Env) (rails.HTML, error) {
	ctx := c.Ctx()
	queries, err := query.ListVisible(ctx, c.Authz(), query.KindIssue, c.Project, c.Project == nil)
	if err != nil {
		return "", err
	}
	def, err := query.Default(ctx, env, query.KindIssue, c.Project)
	if err != nil {
		return "", err
	}
	path := strings.TrimSuffix(c.R.URL.Path, ".html")
	clearBase := "/issues/calendar"
	if c.Project != nil {
		clearBase = "/projects/" + c.Project.Identifier + "/issues/calendar"
	}
	links := func(title string, qs []query.SavedQuery) rails.HTML {
		if len(qs) == 0 {
			return ""
		}
		var items []string
		for _, sq := range qs {
			css := "query"
			clearParams := rails.NewHash("set_filter", 1, "sort", "")
			if def != nil && def.ID == sq.ID {
				css += " default"
				clearParams.Set("without_default", 1)
			}
			clear := rails.HTML("")
			if current != nil && current.ID == sq.ID {
				css += " selected"
				clear = rails.LinkTo(a.Helpers.SpriteIcon(c.Page(), "clear-query", c.L("button_clear"), nil),
					helper.URLWithQuery(clearBase, clearParams), rails.NewHash("class", "icon-only icon-clear-query", "title", c.L("button_clear")))
			}
			var title any
			if sq.Description != "" {
				title = sq.Description
			}
			link := rails.LinkTo(sq.Name, path+"?query_id="+strconv.FormatInt(sq.ID, 10),
				rails.NewHash("class", css, "title", title, "data", rails.NewHash("disable_with", string(rails.H(sq.Name)))))
			items = append(items, string(rails.ContentTag("li", link+clear, nil)))
		}
		return rails.ContentTag("h3", title, nil) + "\n" +
			rails.ContentTag("ul", rails.HTML(strings.Join(items, "\n")), rails.NewHash("class", "queries")) + "\n"
	}
	var mine, others []query.SavedQuery
	for _, sq := range queries {
		if sq.Visibility == query.VisibilityPrivate {
			mine = append(mine, sq)
		} else {
			others = append(others, sq)
		}
	}
	return links(c.L("label_my_queries"), mine) + links(c.L("label_query_plural"), others), nil
}

// calendarQueryView は calendars/_query_filters 用の IssueQuery の表示データ
// （QueriesHelper の filters_options_for_select ほか）。
// TODO(query): queries/_filters（issues#index）と共通化する。
type calendarQueryView struct {
	*userQueryView
}

// FilterOptions は filters_options_for_select(query)。
func (v *calendarQueryView) FilterOptions() rails.HTML {
	af, err := v.q.AvailableFilters(v.c.Ctx())
	if err != nil {
		return ""
	}
	type group struct {
		label string
		items []any
	}
	var ungrouped []any
	groups := []*group{{label: "label_string"}, {label: "label_date"}, {label: "label_time_tracking"}, {label: "label_attachment"}}
	find := func(l string) *group {
		for _, g := range groups {
			if g.label == l {
				return g
			}
		}
		g := &group{label: l}
		groups = append(groups, g)
		return g
	}
	for _, d := range af.Defs() {
		item := []any{d.Name, d.Field}
		g := ""
		switch {
		case reCFAssoc.MatchString(d.Field):
			// (field_options[:through] || field_options[:field]).try(:name)（翻訳しない）
			cf := d.Through
			if cf == nil {
				cf = d.CustomField
			}
			if cf != nil {
				g = "\x00" + cf.Name
			}
		case strings.Contains(d.Field, "."):
			g = "field_" + d.Field[:strings.LastIndex(d.Field, ".")]
		case d.Type == "relation":
			g = "label_relations"
		case d.Type == "tree":
			g = "label_relations"
		case d.Field == "member_of_group" || d.Field == "assigned_to_role":
			g = "field_assigned_to"
		case d.Type == "date_past" || d.Type == "date":
			g = "label_date"
		case d.Field == "estimated_hours" || d.Field == "spent_time":
			g = "label_time_tracking"
		case d.Field == "attachment" || d.Field == "attachment_description":
			g = "label_attachment"
		case d.Type == "string" || d.Type == "text" || d.Type == "search":
			g = "label_string"
		}
		if g != "" {
			grp := find(g)
			grp.items = append(grp.items, item)
		} else {
			ungrouped = append(ungrouped, item)
		}
	}
	if dg := find("label_date"); len(dg.items) == 1 {
		ungrouped = append(ungrouped, dg.items[0])
		dg.items = nil
	}
	s := rails.OptionsForSelect(append([]any{[]any{}}, ungrouped...), nil)
	var grouped []any
	for _, g := range groups {
		if len(g.items) == 0 {
			continue
		}
		label := g.label
		if strings.HasPrefix(label, "\x00") {
			label = label[1:]
		} else {
			label = v.c.L(label)
		}
		grouped = append(grouped, []any{label, g.items})
	}
	if len(grouped) > 0 {
		s += rails.GroupedOptionsForSelect(grouped, nil, nil)
	}
	return s
}

// FiltersJS は addFilter の各行（query.filters）。
func (v *calendarQueryView) Filters() []queryFilterView {
	var out []queryFilterView
	for _, k := range v.q.Filters.Keys() {
		out = append(out, queryFilterView{Field: k, Operator: v.q.OperatorFor(k), Values: nonNilStrings(v.q.ValuesFor(k))})
	}
	return out
}

var reCFAssoc = regexp.MustCompile(`^cf_\d+\.`)

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
