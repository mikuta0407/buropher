package handler

import (
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/csvexport"
	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは timelog#report（Redmine::Helpers::TimeReport、timelog/report, _report_criteria、
// TimelogHelper#report_to_csv）の移植。

// teCriterion は TimeReport#available_criteria の 1 項目。
type teCriterion struct {
	Key   string
	SQL   string
	Join  string
	Klass string // project / status / version / category / user / tracker / activity / issue（CF は ""）
	Label string // 翻訳キー（CF は名前）
	CF    *customfield.CustomField
}

// teReportRow は @report.hours の 1 行（条件と期間の値は文字列。NULL は ""）。
type teReportRow struct {
	Values map[string]string
	Hours  float64
}

// teReport は Redmine::Helpers::TimeReport。
type teReport struct {
	Criteria  []string
	Columns   string
	Available []*teCriterion
	Hours     []*teReportRow
	Periods   []string
	// uniq は teUniqValues(Hours, key) のキャッシュ (Hours は作成後に変わらない)。
	uniq map[string][]string
}

// uniqValues は teUniqValues(r.Hours, key) をキャッシュして返す。
func (r *teReport) uniqValues(key string) []string {
	if v, ok := r.uniq[key]; ok {
		return v
	}
	if r.uniq == nil {
		r.uniq = map[string][]string{}
	}
	v := teUniqValues(r.Hours, key)
	r.uniq[key] = v
	return v
}

func (r *teReport) criterion(key string) *teCriterion {
	for _, c := range r.Available {
		if c.Key == key {
			return c
		}
	}
	return nil
}

// teAvailableCriteria は load_available_criteria。
func (a *App) teAvailableCriteria(c *Req) ([]*teCriterion, error) {
	ctx := c.Ctx()
	out := []*teCriterion{
		{Key: "project", SQL: "time_entries.project_id", Klass: "project", Label: "label_project"},
		{Key: "status", SQL: "issues.status_id", Klass: "status", Label: "field_status"},
		{Key: "version", SQL: "issues.fixed_version_id", Klass: "version", Label: "label_version"},
		{Key: "category", SQL: "issues.category_id", Klass: "category", Label: "field_category"},
		{Key: "user", SQL: "time_entries.user_id", Klass: "user", Label: "label_user"},
		{Key: "tracker", SQL: "issues.tracker_id", Klass: "tracker", Label: "label_tracker"},
		{Key: "activity", SQL: "COALESCE(time_entry_activities.parent_id, time_entry_activities.id)", Klass: "activity", Label: "field_activity"},
		{Key: "issue", SQL: "time_entries.issue_id", Klass: "issue", Label: "label_issue"},
	}
	vis, err := customfield.VisibleCondition(ctx, c.Authz())
	if err != nil {
		return nil, err
	}
	var pid *int64
	if c.Project != nil {
		pid = &c.Project.ID
	}
	ids, err := repository.TimelogReportCustomFieldIDs(ctx, a.DB, pid, vis)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		cf, err := customfield.Get(ctx, a.DB, id)
		if err != nil {
			return nil, err
		}
		if cf == nil || cf.Multiple || (cf.FieldFormat != "list" && cf.FieldFormat != "bool") {
			continue
		}
		key := "cf_" + strconv.FormatInt(cf.ID, 10)
		if slices.ContainsFunc(out, func(x *teCriterion) bool { return x.Key == key }) {
			continue
		}
		cvis, err := customfield.VisibilityByProjectCondition(ctx, c.Authz(), cf, "", "")
		if err != nil {
			return nil, err
		}
		out = append(out, &teCriterion{Key: key, SQL: cf.GroupStatement(), Join: cf.JoinForOrderStatement(cvis), Label: cf.Name, CF: cf})
	}
	return out, nil
}

// teNewReport は TimeReport.new(project, criteria, columns, scope) と run。
func (a *App) teNewReport(c *Req, q *query.Query) (*teReport, error) {
	ctx := c.Ctx()
	avail, err := a.teAvailableCriteria(c)
	if err != nil {
		return nil, err
	}
	r := &teReport{Available: avail, Columns: "month"}
	for _, k := range c.Params().Strings("criteria") {
		if r.criterion(k) != nil && !slices.Contains(r.Criteria, k) {
			r.Criteria = append(r.Criteria, k)
		}
	}
	if len(r.Criteria) > 3 {
		r.Criteria = r.Criteria[:3]
	}
	switch col := c.Params().String("columns"); col {
	case "year", "month", "week", "day":
		r.Columns = col
	}
	if len(r.Criteria) == 0 {
		return r, nil
	}
	var exprs, joins []string
	for _, k := range r.Criteria {
		cr := r.criterion(k)
		exprs = append(exprs, cr.SQL)
		if cr.Join != "" {
			joins = append(joins, cr.Join)
		}
	}
	exprs = append(exprs, "time_entries.tyear", "time_entries.tmonth", "time_entries.tweek", "time_entries.spent_on")
	rows, err := q.GroupedSums(ctx, exprs, joins, "time_entries.hours")
	if err != nil {
		return nil, err
	}
	var minD, maxD time.Time
	for _, row := range rows {
		h := &teReportRow{Values: map[string]string{}, Hours: row.Sum}
		for i, k := range r.Criteria {
			h.Values[k] = teReportKey(row.Keys[i])
		}
		n := len(r.Criteria)
		tyear, tmonth, tweek := teReportKey(row.Keys[n]), teReportKey(row.Keys[n+1]), teReportKey(row.Keys[n+2])
		spent := teReportKey(row.Keys[n+3])
		if len(spent) > 10 {
			spent = spent[:10]
		}
		d, _ := time.Parse("2006-01-02", spent)
		switch r.Columns {
		case "year":
			h.Values["year"] = tyear
		case "month":
			h.Values["month"] = tyear + "-" + tmonth
		case "week":
			y, _ := d.ISOWeek()
			h.Values["week"] = strconv.Itoa(y) + "-" + tweek
		case "day":
			h.Values["day"] = spent
		}
		if minD.IsZero() || d.Before(minD) {
			minD = d
		}
		if maxD.IsZero() || d.After(maxD) {
			maxD = d
		}
		r.Hours = append(r.Hours, h)
	}
	today := a.teEnv(c).Today()
	if minD.IsZero() {
		minD = today
	}
	if maxD.IsZero() {
		maxD = today
	}
	from := minD
	for !from.After(maxD) && len(r.Periods) < 100 {
		switch r.Columns {
		case "year":
			r.Periods = append(r.Periods, strconv.Itoa(from.Year()))
			from = time.Date(from.Year()+1, 1, 1, 0, 0, 0, 0, time.UTC)
		case "month":
			r.Periods = append(r.Periods, strconv.Itoa(from.Year())+"-"+strconv.Itoa(int(from.Month())))
			from = time.Date(from.Year(), from.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		case "week":
			y, w := from.ISOWeek()
			r.Periods = append(r.Periods, strconv.Itoa(y)+"-"+strconv.Itoa(w))
			next := from.AddDate(0, 0, 7)
			wd := (int(next.Weekday()) + 6) % 7 // 月曜 = 0
			from = next.AddDate(0, 0, -wd)
		case "day":
			r.Periods = append(r.Periods, from.Format("2006-01-02"))
			from = from.AddDate(0, 0, 1)
		}
	}
	return r, nil
}

// teReportKey はグループのキーを to_s した文字列（NULL は ""、浮動小数の整数は整数表記）。
func teReportKey(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case time.Time:
		return x.Format("2006-01-02")
	case bool:
		if x {
			return "1"
		}
		return "0"
	}
	return fmt.Sprint(v)
}

// teGroupHours は select_hours(data, criteria, value) を全ての value について一度に行う。
// 各グループ内の順序は data の順 (select の結果と同じ)。
// value ごとに data を走査し直すと値の種類 × 行数の計算量になるため、まとめて振り分ける。
func teGroupHours(data []*teReportRow, key string) map[string][]*teReportRow {
	out := map[string][]*teReportRow{}
	for _, r := range data {
		v := r.Values[key]
		out[v] = append(out[v], r)
	}
	return out
}

// tePeriodSums は sum_hours(select_hours(data, columns, period)) を全ての period について求める。
// 加算の順序は data の順なので、period ごとに選んでから合計した値と一致する。
func tePeriodSums(data []*teReportRow, columns string) map[string]float64 {
	out := map[string]float64{}
	for _, r := range data {
		out[r.Values[columns]] += r.Hours
	}
	return out
}

// teUniqValues は data.collect {|h| h[key]}.uniq（出現順）。
func teUniqValues(data []*teReportRow, key string) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range data {
		v := r.Values[key]
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// ---------------------------------------------------------------- 条件値の表示

// teCriteriaFormatter は format_criteria_value の参照先。
type teCriteriaFormatter struct {
	a        *App
	c        *Req
	report   *teReport
	names    map[string]map[int64]string
	lookup   *teLookup
	versions map[int64]*repository.TimelogVersionRef
	vis      map[int64]bool
	issues   map[int64]*repository.RefIssue
}

func (a *App) newTECriteriaFormatter(c *Req, r *teReport) (*teCriteriaFormatter, error) {
	ctx := c.Ctx()
	f := &teCriteriaFormatter{a: a, c: c, report: r, names: map[string]map[int64]string{}}
	ids := map[string][]int64{}
	for _, k := range r.Criteria {
		for _, row := range r.Hours {
			if n, err := strconv.ParseInt(row.Values[k], 10, 64); err == nil {
				ids[k] = append(ids[k], n)
			}
		}
	}
	f.lookup = &teLookup{a: a, c: c}
	var err error
	if f.lookup.projects, err = repository.ProjectsByIDs(ctx, a.DB, ids["project"]); err != nil {
		return nil, err
	}
	if f.lookup.users, err = repository.UsersByIDs(ctx, a.DB, ids["user"]); err != nil {
		return nil, err
	}
	for k, table := range map[string]string{"status": "issue_statuses", "category": "issue_categories", "tracker": "trackers", "activity": "time_entry_activities"} {
		if f.names[k], err = repository.TimelogNames(ctx, a.DB, table, ids[k]); err != nil {
			return nil, err
		}
	}
	if f.versions, err = repository.TimelogVersions(ctx, a.DB, ids["version"]); err != nil {
		return nil, err
	}
	if f.issues, err = repository.TimelogRefIssues(ctx, a.DB, ids["issue"], ""); err != nil {
		return nil, err
	}
	f.vis = map[int64]bool{}
	if len(f.issues) > 0 {
		cond, err := c.Authz().IssueVisibleCondition(ctx, authz.ConditionOptions{})
		if err != nil {
			return nil, err
		}
		v, err := repository.TimelogRefIssues(ctx, a.DB, ids["issue"], cond)
		if err != nil {
			return nil, err
		}
		for id := range v {
			f.vis[id] = true
		}
	}
	return f, nil
}

// format は format_criteria_value(criteria_options, value, html)。
func (f *teCriteriaFormatter) format(cr *teCriterion, value string, html bool) any {
	if strings.TrimSpace(value) == "" {
		return "[" + f.c.L("label_none") + "]"
	}
	if cr.CF != nil {
		return f.lookup.formatCF(cr.CF, []string{value}, true)
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return ""
	}
	switch cr.Klass {
	case "project":
		return f.lookup.formatProject(f.lookup.projects[id], html)
	case "user":
		return f.lookup.formatUser(f.lookup.users[id], html)
	case "status", "category", "tracker", "activity":
		return f.names[cr.Klass][id]
	case "version":
		v := f.versions[id]
		if v == nil {
			return ""
		}
		if !html {
			return v.Name
		}
		name := v.Name
		if f.c.Project == nil || f.c.Project.ID != v.ProjectID {
			name = v.ProjectName + " - " + v.Name
		}
		var title any
		if v.Date.Valid {
			title = helper.FormatDate(f.c.Page(), v.Date.Date.Time)
		}
		return rails.LinkTo(name, "/versions/"+strconv.FormatInt(v.ID, 10), rails.NewHash("title", title))
	case "issue":
		is := f.issues[id]
		if is == nil {
			return ""
		}
		if !f.vis[id] {
			return "#" + strconv.FormatInt(id, 10)
		}
		if html {
			return f.a.Helpers.WikiRenderer(f.c.Page()).LinkToIssue(is, redmine.LinkToIssueOptions{})
		}
		return is.TrackerName + " #" + strconv.FormatInt(id, 10) + ": " + is.Subject
	}
	return value
}

var teHoursRe = regexp.MustCompile(`(\d+)([\.,:])(\d+)`)

// teHTMLHours は html_hours(format_hours(sum))。
func (a *App) teHTMLHours(c *Req, h float64) string {
	return teHoursRe.ReplaceAllString(string(rails.H(c.Loc.FormatHours(h))), `<span class="hours hours-int">$1</span><span class="hours hours-dec">$2$3</span>`)
}

// teReportTable は time-report の tbody（_report_criteria と合計行）。
func (a *App) teReportBody(c *Req, r *teReport, f *teCriteriaFormatter) string {
	var b strings.Builder
	a.teReportCriteria(c, &b, r, f, r.Hours, 0)
	b.WriteString("\n")
	b.WriteString("  <tr class=\"total\">\n  <td>" + string(rails.H(c.L("label_total_time"))) + "</td>\n  ")
	b.WriteString(strings.Repeat("<td></td>", len(r.Criteria)-1) + "\n")
	total := 0.0
	sums := tePeriodSums(r.Hours, r.Columns)
	for _, p := range r.Periods {
		sum := sums[p]
		total += sum
		b.WriteString("    <td class=\"hours\">")
		if sum > 0 {
			b.WriteString(a.teHTMLHours(c, sum))
		}
		b.WriteString("</td>\n")
	}
	b.WriteString("  <td class=\"hours\">")
	if total > 0 {
		b.WriteString(a.teHTMLHours(c, total))
	}
	b.WriteString("</td>\n  </tr>\n")
	return b.String()
}

// teReportCriteria は timelog/_report_criteria。
func (a *App) teReportCriteria(c *Req, b *strings.Builder, r *teReport, f *teCriteriaFormatter, hours []*teReportRow, level int) {
	key := r.Criteria[level]
	cr := r.criterion(key)
	groups := teGroupHours(hours, key)
	for _, value := range r.uniqValues(key) {
		hv := groups[value]
		if len(hv) == 0 {
			continue
		}
		class := "last-level"
		if len(r.Criteria) > level+1 {
			class = "subtotal"
		}
		b.WriteString("<tr class=\"" + class + "\">\n")
		b.WriteString(strings.Repeat("<td></td>", level) + "\n")
		b.WriteString("<td class=\"name\">" + string(rails.H(f.format(cr, value, true))) + "</td>\n")
		b.WriteString(strings.Repeat("<td></td>", len(r.Criteria)-level-1))
		total := 0.0
		sums := tePeriodSums(hv, r.Columns)
		for _, p := range r.Periods {
			sum := sums[p]
			total += sum
			b.WriteString("    <td class=\"hours\">")
			if sum > 0 {
				b.WriteString(a.teHTMLHours(c, sum))
			}
			b.WriteString("</td>\n")
		}
		b.WriteString("  <td class=\"hours\">")
		if total > 0 {
			b.WriteString(a.teHTMLHours(c, total))
		}
		b.WriteString("</td>\n</tr>\n")
		if len(r.Criteria) > level+1 {
			b.WriteString("  ")
			a.teReportCriteria(c, b, r, f, hv, level+1)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
}

// ---------------------------------------------------------------- action

// TimelogReport は timelog#report（html / csv）。
func (a *App) TimelogReport(c *Req) {
	format := teFormat(c, "html", "csv")
	if format == "" {
		c.head(http.StatusNotAcceptable)
		return
	}
	q, ok := a.teRetrieveQuery(c)
	if !ok {
		return
	}
	r, err := a.teNewReport(c, q)
	if err != nil {
		a.teQueryFailed(c, err)
		return
	}
	f, err := a.newTECriteriaFormatter(c, r)
	if err != nil {
		a.internalError(c, "report formatter", err)
		return
	}
	if format == "csv" {
		a.teReportCSV(c, r, f)
		return
	}
	qv, err := a.newQueryView(c, q, "TimeEntryQuery", teReportPath(c.Project))
	if err != nil {
		a.teQueryFailed(c, err)
		return
	}
	data := a.teCommonData(c, q, qv, teReportPath(c.Project))
	data["NewTimeEntryPath"] = teNewTimeEntryPath(c.Project, nil)
	data["CanManageActivities"] = c.Project != nil && c.AllowedTo(domain.Perm("manage_project_activities"), c.Project) &&
		c.AllowedTo(domain.ControllerAction("projects", "settings"), c.Project)
	data["Report"] = r
	data["ColumnsOptions"] = [][]any{{c.L("label_year"), "year"}, {c.L("label_month"), "month"}, {c.L("label_week"), "week"},
		{teTitleize(c.L("label_day_plural")), "day"}}
	crit := [][]any{{}}
	for _, cr := range r.Available {
		if !slices.Contains(r.Criteria, cr.Key) {
			crit = append(crit, []any{c.Loc.LOrHumanize(cr.Label, ""), cr.Key})
		}
	}
	data["CriteriaOptions"] = crit
	data["CriteriaDisabled"] = len(r.Criteria) >= 3
	qp := pageQueryParameters(c)
	clear := qp.Clone()
	clear.Set("criteria", nil)
	data["ClearURL"] = helper.URLWithQuery(teReportPath(c.Project), clear)
	enc := c.L("general_csv_encoding")
	if !strings.EqualFold(enc, "UTF-8") {
		data["Encoding"] = enc
	}
	if len(r.Criteria) > 0 && len(r.Hours) > 0 {
		var heads []string
		for _, k := range r.Criteria {
			heads = append(heads, c.Loc.LOrHumanize(r.criterion(k).Label, ""))
		}
		data["CriteriaHeads"] = heads
		data["ColumnsWidth"] = 40 / (len(r.Periods) + 1)
		data["ReportBody"] = template.HTML(a.teReportBody(c, r, f))
		data["CSVURL"] = helper.URLWithQuery(teReportPath(c.Project)+".csv", qp.Except("page", "format"))
	}
	opts := RenderOptions{}
	if httpx.IsXHR(c.R) {
		opts.Layout = view.NoLayout
	}
	c.Render("timelog/report", data, opts)
}

// teReportCSV は report_to_csv(@report)。
func (a *App) teReportCSV(c *Req, r *teReport, f *teCriteriaFormatter) {
	p := c.Params()
	w := csvexport.New(csvexport.Options{
		Separator:       c.L("general_csv_separator"),
		Encoding:        p.String("encoding"),
		DefaultEncoding: c.L("general_csv_encoding"),
	})
	dec := c.L("general_csv_decimal_separator")
	num := func(v float64) string { return strings.ReplaceAll(strconv.FormatFloat(v, 'f', 2, 64), ".", dec) }
	var head []string
	for _, k := range r.Criteria {
		head = append(head, c.Loc.LOrHumanize(r.criterion(k).Label, ""))
	}
	head = append(head, r.Periods...)
	head = append(head, c.L("label_total_time"))
	w.Strings(head...)
	var rec func(hours []*teReportRow, level int)
	rec = func(hours []*teReportRow, level int) {
		key := r.Criteria[level]
		cr := r.criterion(key)
		groups := teGroupHours(hours, key)
		for _, value := range teUniqValues(hours, key) {
			hv := groups[value]
			if len(hv) == 0 {
				continue
			}
			row := make([]string, level)
			row = append(row, rails.ToS(f.format(cr, value, false)))
			row = append(row, make([]string, len(r.Criteria)-level-1)...)
			total := 0.0
			sums := tePeriodSums(hv, r.Columns)
			for _, p := range r.Periods {
				sum := sums[p]
				total += sum
				if sum > 0 {
					row = append(row, num(sum))
				} else {
					row = append(row, "")
				}
			}
			row = append(row, num(total))
			w.Strings(row...)
			if len(r.Criteria) > level+1 {
				rec(hv, level+1)
			}
		}
	}
	if len(r.Criteria) > 0 {
		rec(r.Hours, 0)
	}
	row := []string{c.L("label_total_time")}
	if len(r.Criteria) > 1 {
		row = append(row, make([]string, len(r.Criteria)-1)...)
	}
	total := 0.0
	sums := tePeriodSums(r.Hours, r.Columns)
	for _, p := range r.Periods {
		sum := sums[p]
		total += sum
		if sum > 0 {
			row = append(row, num(sum))
		} else {
			row = append(row, "")
		}
	}
	if total == 0 {
		row = append(row, "0")
	} else {
		row = append(row, num(total))
	}
	w.Strings(row...)
	c.halted = true
	c.W.Header().Set("Content-Type", "text/csv; header=present")
	c.W.Header().Set("Content-Disposition", `attachment; filename="timelog.csv"; filename*=UTF-8''timelog.csv`)
	c.W.WriteHeader(http.StatusOK)
	_, _ = c.W.Write(w.Bytes())
}

// teTitleize は String#titleize（単語の先頭を大文字、残りを小文字）。
func teTitleize(s string) string {
	words := strings.Fields(strings.ReplaceAll(s, "_", " "))
	for i, w := range words {
		words[i] = helper.RubyCapitalize(w)
	}
	return strings.Join(words, " ")
}
