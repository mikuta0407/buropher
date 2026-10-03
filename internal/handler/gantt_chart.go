// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

// このファイルは Redmine::Helpers::Gantt（lib/redmine/helpers/gantt.rb）の HTML 形式の移植。
// 期間（year / month / months）・拡大率（zoom）の決定、プロジェクト・バージョン・チケットの木の並び、
// 左側の題名（subjects）・右側の線（lines）・選択列（selected_column_content）の描画を行う。
// PDF 形式（to_pdf）は gantt_pdf.go。PNG 形式（to_image）は未対応。

import (
	"context"
	"errors"
	"html/template"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// ganttDrawTypes は Gantt::DRAW_TYPES（描画する関連の種類。to_json の順）。
var ganttDrawTypes = []struct {
	Type            string
	LandscapeMargin int
	Color           string
}{
	{"blocks", 16, "#F34F4F"},
	{"precedes", 20, "#628FEA"},
}

// ganttUnavailableColumns は Gantt::UNAVAILABLE_COLUMNS。
var ganttUnavailableColumns = []string{"tracker", "id", "subject"}

// ganttDrawTypesJSON は DRAW_TYPES.to_json。
func ganttDrawTypesJSON() string {
	var b strings.Builder
	b.WriteString("{")
	for i, t := range ganttDrawTypes {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"` + t.Type + `":{"landscape_margin":` + strconv.Itoa(t.LandscapeMargin) + `,"color":"` + t.Color + `"}`)
	}
	b.WriteString("}")
	return b.String()
}

// errGanttMaxLines は Gantt::MaxLinesLimitReached。
var errGanttMaxLines = errors.New("gantt: max lines limit reached")

// ganttChart は Redmine::Helpers::Gantt のインスタンス。
type ganttChart struct {
	a *App
	c *Req
	l *issueLookup

	Project   *domain.Project
	YearFrom  int
	MonthFrom int
	DateFrom  time.Time
	DateTo    time.Time
	Zoom      int
	Months    int
	// MaxRows は max_rows（hasMaxRows が偽なら nil = 無制限）。
	MaxRows    int
	hasMaxRows bool
	Truncated  bool

	query      *query.Query
	today      time.Time
	nonWorking []int
	issueVis   string

	issues          []*query.IssueRow
	projects        []*domain.Project
	nested          map[int64]repository.NestedSetValue
	issuesByProject map[int64][]*query.IssueRow
	versionsByProj  map[int64][]*ganttVersion
	relations       map[int64][]*repository.IssueRelation
	versions        map[int64]*ganttVersion
	projectInfo     map[int64]*ganttProjectInfo

	numberOfRows int
	subjects     strings.Builder
	lines        strings.Builder
	columns      map[string]*strings.Builder
}

// ganttVersion はガントに描くバージョン（Version の必要な値）。
type ganttVersion struct {
	*repository.Version
	Project *domain.Project
	// StartDate は start_date（fixed_issues の最小開始日）。
	StartDate *time.Time
	// CompletedPercent は completed_percent（fixed_issues）、VisiblePercent は visible_fixed_issues.completed_percent。
	CompletedPercent float64
	VisiblePercent   float64
	OpenCount        int
	HasFixedIssues   bool
}

// ganttProjectInfo は Project#start_date / due_date と issues.exists? || versions.exists?。
type ganttProjectInfo struct {
	StartDate, DueDate *time.Time
	HasChildren        bool
}

// ganttOptions は render の options。
type ganttOptions struct {
	top, topIncrement       int
	indent, indentIncrement int
	zoom, gWidth            int
	subjectWidth            int
	only                    string // "" / "subjects" / "lines" / "selected_columns"
	column                  *query.Column
	// pdf は :format => :pdf のときの描画先（gantt_pdf.go）。
	pdf *ganttPDF
}

// newGanttChart は Gantt.new(params)（個人設定 gantt_zoom / gantt_months の保存を含む）。
func (a *App) newGanttChart(c *Req) (*ganttChart, error) {
	ctx := c.Ctx()
	p := c.Params()
	g := &ganttChart{a: a, c: c, today: a.userToday(c), columns: map[string]*strings.Builder{}}
	if y := int(httpx.RubyToI(p.String("year"))); p.Has("year") && y > 0 && y <= maxCalendarYear {
		g.YearFrom = y
		if m := int(httpx.RubyToI(p.String("month"))); p.Has("month") && m >= 1 && m <= 12 {
			g.MonthFrom = m
		} else {
			g.MonthFrom = 1
		}
	} else {
		g.MonthFrom = int(g.today.Month())
		g.YearFrom = g.today.Year()
	}
	var prefZoom, prefMonths any
	if c.User.Logged() {
		var err error
		if prefZoom, err = repository.UserPrefExtra(ctx, a.DB, c.User.ID, "gantt_zoom"); err != nil {
			return nil, err
		}
		if prefMonths, err = repository.UserPrefExtra(ctx, a.DB, c.User.ID, "gantt_months"); err != nil {
			return nil, err
		}
	}
	zoomSrc := prefToS(prefZoom)
	if p.Has("zoom") {
		zoomSrc = p.String("zoom")
	}
	if z := int(httpx.RubyToI(zoomSrc)); z > 0 && z < 5 {
		g.Zoom = z
	} else {
		g.Zoom = 2
	}
	monthsSrc := prefToS(prefMonths)
	if p.Has("months") {
		monthsSrc = p.String("months")
	}
	if m := int(httpx.RubyToI(monthsSrc)); m > 0 && m < a.Settings.Int("gantt_months_limit")+1 {
		g.Months = m
	} else {
		g.Months = 6
	}
	// Save gantt parameters as user preference (zoom and months count)
	if c.User.Logged() && (!prefIntEq(prefZoom, g.Zoom) || !prefIntEq(prefMonths, g.Months)) {
		if err := repository.SetUserPrefExtra(ctx, a.DB, c.User.ID, "gantt_zoom", g.Zoom); err != nil {
			return nil, err
		}
		if err := repository.SetUserPrefExtra(ctx, a.DB, c.User.ID, "gantt_months", g.Months); err != nil {
			return nil, err
		}
	}
	g.DateFrom = time.Date(g.YearFrom, time.Month(g.MonthFrom), 1, 0, 0, 0, 0, time.UTC)
	g.DateTo = g.DateFrom.AddDate(0, g.Months, 0).AddDate(0, 0, -1)
	if s := a.Settings.String("gantt_items_limit"); strings.TrimSpace(s) != "" {
		g.MaxRows, g.hasMaxRows = settings.RubyToI(s), true
	}
	for _, s := range a.Settings.Strings("non_working_week_days") {
		g.nonWorking = append(g.nonWorking, settings.RubyToI(s))
	}
	if len(g.nonWorking) >= 7 {
		g.nonWorking = nil
	}
	return g, nil
}

// prefToS は User.current.pref[:gantt_zoom] の to_s（to_i に渡す値）。
func prefToS(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case float64:
		return strconv.FormatInt(int64(x), 10)
	case string:
		return x
	}
	return ""
}

// prefIntEq は @zoom == User.current.pref[:gantt_zoom]（Integer 同士の比較。文字列や nil は不一致）。
func prefIntEq(v any, n int) bool {
	f, ok := v.(float64)
	return ok && f == float64(n) && f == math.Trunc(f)
}

// commonParams は common_params（controller, action, project_id）。
func (g *ganttChart) commonParams() *rails.Hash {
	h := rails.NewHash("controller", "gantts", "action", "show")
	if g.Project != nil {
		h.Set("project_id", g.Project.Identifier)
	} else {
		h.Set("project_id", nil)
	}
	return h
}

// Params は params。
func (g *ganttChart) Params() *rails.Hash {
	return g.commonParams().Update(rails.NewHash("zoom", g.Zoom, "year", g.YearFrom, "month", g.MonthFrom, "months", g.Months))
}

// ParamsPrevious は params_previous。
func (g *ganttChart) ParamsPrevious() *rails.Hash {
	d := g.DateFrom.AddDate(0, -g.Months, 0)
	return g.commonParams().Update(rails.NewHash("year", d.Year(), "month", int(d.Month()), "zoom", g.Zoom, "months", g.Months))
}

// ParamsNext は params_next。
func (g *ganttChart) ParamsNext() *rails.Hash {
	d := g.DateFrom.AddDate(0, g.Months, 0)
	return g.commonParams().Update(rails.NewHash("year", d.Year(), "month", int(d.Month()), "zoom", g.Zoom, "months", g.Months))
}

// NumberOfRows は number_of_rows（描画後は描画した行数）。
func (g *ganttChart) NumberOfRows() int { return g.numberOfRows }

// load は issues / projects / relations とバージョン・プロジェクトの値を読み込む。
func (g *ganttChart) load(ctx context.Context) error {
	q := g.query
	if q == nil {
		return nil
	}
	ns, err := repository.ProjectNestedSet(ctx, g.a.DB)
	if err != nil {
		return err
	}
	g.nested = ns
	// issues: order projects.lft ASC, issues.id ASC, limit max_rows
	// 並べ替えと件数制限は SQL で行う (全件を読み込んでから切り詰めると大規模データで遅い)。
	var rows []*query.IssueRow
	if !g.hasMaxRows || g.MaxRows > 0 {
		lft, err := q.ProjectsLftOrder(ctx)
		if err != nil {
			return err
		}
		opts := query.ListOptions{Order: []string{lft, "issues.id ASC"}}
		if g.hasMaxRows {
			opts.Limit = g.MaxRows
		}
		if rows, err = q.Issues(ctx, opts); err != nil {
			return err
		}
	}
	g.issues = rows
	g.l.addIssues(rows)
	g.l.markVisible(rows)
	ids := make([]int64, len(rows))
	var pids, vids, projectIDs []int64
	seenProject := map[int64]bool{}
	for i, r := range rows {
		ids[i] = r.ID
		pids = append(pids, r.AuthorID)
		if r.AssignedToID != nil {
			pids = append(pids, *r.AssignedToID)
		}
		if r.LastUpdatedByID != nil {
			pids = append(pids, *r.LastUpdatedByID)
		}
		pids = append(pids, r.WatcherIDs...)
		if r.FixedVersionID != nil {
			vids = append(vids, *r.FixedVersionID)
		}
		if !seenProject[r.ProjectID] {
			seenProject[r.ProjectID] = true
			projectIDs = append(projectIDs, r.ProjectID)
		}
	}
	g.l.preloadChildren(ids)
	g.l.preloadPrincipals(pids)
	g.l.preloadVersions(vids)

	// relations
	g.relations = map[int64][]*repository.IssueRelation{}
	rels, err := repository.GanttRelations(ctx, g.a.DB, ids)
	if err != nil {
		return err
	}
	for _, r := range rels {
		g.relations[r.IssueFromID] = append(g.relations[r.IssueFromID], r)
	}

	// projects（チケットのプロジェクトと、その可視な祖先）
	vis, err := g.c.Authz().VisibleCondition(ctx, authz.ConditionOptions{})
	if err != nil {
		return err
	}
	ps, err := repository.GanttProjects(ctx, g.a.DB, projectIDs, vis)
	if err != nil {
		return err
	}
	slices.SortStableFunc(ps, func(a, b *domain.Project) int { return ns[a.ID].Lft - ns[b.ID].Lft })
	g.projects = ps

	g.issuesByProject = map[int64][]*query.IssueRow{}
	for _, r := range rows {
		g.issuesByProject[r.ProjectID] = append(g.issuesByProject[r.ProjectID], r)
	}
	g.issueVis, err = g.c.Authz().IssueVisibleCondition(ctx, issueVisOpts())
	if err != nil {
		return err
	}
	g.versions = map[int64]*ganttVersion{}
	g.versionsByProj = map[int64][]*ganttVersion{}
	g.projectInfo = map[int64]*ganttProjectInfo{}
	for _, p := range ps {
		var vs []*ganttVersion
		seen := map[int64]bool{}
		for _, r := range g.issuesByProject[p.ID] {
			if r.FixedVersionID == nil || seen[*r.FixedVersionID] {
				continue
			}
			seen[*r.FixedVersionID] = true
			v, err := g.version(ctx, *r.FixedVersionID)
			if err != nil {
				return err
			}
			if v != nil {
				vs = append(vs, v)
			}
		}
		// Version.where(id: ids).to_a は id 順、その後 sort_versions!（Version#<=>）
		slices.SortFunc(vs, func(a, b *ganttVersion) int { return cmpInt64(a.ID, b.ID) })
		slices.SortStableFunc(vs, func(a, b *ganttVersion) int { return a.Version.Compare(b.Version) })
		g.versionsByProj[p.ID] = vs
		info := &ganttProjectInfo{}
		if info.StartDate, info.DueDate, err = repository.GanttProjectDates(ctx, g.a.DB, p); err != nil {
			return err
		}
		if info.HasChildren, err = repository.ProjectHasIssuesOrVersions(ctx, g.a.DB, p.ID); err != nil {
			return err
		}
		g.projectInfo[p.ID] = info
	}
	return nil
}

// version はバージョンの値を読み込む（キャッシュあり）。
func (g *ganttChart) version(ctx context.Context, id int64) (*ganttVersion, error) {
	if v, ok := g.versions[id]; ok {
		return v, nil
	}
	rv := g.l.version(&id)
	if rv == nil {
		g.versions[id] = nil
		return nil, nil
	}
	v := &ganttVersion{Version: rv, Project: g.l.project(rv.ProjectID)}
	all, err := g.a.loadVersionIssueSet(ctx, g.issueVis, id, "")
	if err != nil {
		return nil, err
	}
	visible, err := g.a.loadVersionIssueSet(ctx, g.issueVis, id, g.issueVis)
	if err != nil {
		return nil, err
	}
	v.CompletedPercent = all.CompletedPercent()
	v.VisiblePercent = visible.CompletedPercent()
	v.OpenCount = all.OpenCount()
	v.HasFixedIssues = all.Count() > 0
	if v.StartDate, err = repository.VersionStartDate(ctx, g.a.DB, id); err != nil {
		return nil, err
	}
	g.versions[id] = v
	return v, nil
}

// ---------------------------------------------------------------- 日付

// ganttDays は日付の通日。
func ganttDays(t time.Time) int64 { return t.Unix() / 86400 }

const ganttDayNS = int64(86400) * 1e9

// ganttFracDate は小数部を持つ Date（Date + Float の結果。小数部はナノ秒に丸められる）。
type ganttFracDate struct {
	days int64
	ns   int64 // 0 <= ns < ganttDayNS
}

// cmp は d と通日 day の比較。
func (d ganttFracDate) cmp(day int64) int {
	switch {
	case d.days < day:
		return -1
	case d.days > day:
		return 1
	case d.ns > 0:
		return 1
	}
	return 0
}

// diffZoomFloor は ((d - from) * zoom).floor。
func (d ganttFracDate) diffZoomFloor(from int64, zoom int) int {
	return int((d.days-from)*int64(zoom) + (d.ns*int64(zoom))/ganttDayNS)
}

// calcProgressDate は calc_progress_date（start_date + (end_date - start_date + 1) * (progress / 100.0)）。
func calcProgressDate(start, end time.Time, progress float64) ganttFracDate {
	o := float64(ganttDays(end)-ganttDays(start)+1) * (progress / 100.0)
	return ganttDateAddFloat(ganttDays(start), o)
}

// ganttDateAddFloat は Date#+(Float)（date_core.c の d_lite_plus: 日・秒・ナノ秒（四捨五入）に分解する）。
func ganttDateAddFloat(day int64, o float64) ganttFracDate {
	s := int64(1)
	if o < 0 {
		s = -1
		o = -o
	}
	ip, fp := math.Modf(o)
	jd := int64(ip)
	fp *= 86400
	sec, frac := math.Modf(fp)
	df := int64(sec)
	sf := int64(math.Round(frac * 1e9))
	total := s * (jd*ganttDayNS + df*1e9 + sf)
	days := day + floorDiv64(total, ganttDayNS)
	ns := total - floorDiv64(total, ganttDayNS)*ganttDayNS
	return ganttFracDate{days: days, ns: ns}
}

func floorDiv64(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// ganttCoords は coordinates の結果（無いキーは nil）。
type ganttCoords struct {
	start, end, barStart, barEnd, barProgressEnd, barLateEnd *int
}

func intp(n int) *int { return &n }

// coordinates は coordinates(start_date, end_date, progress, zoom)。progress が nil なら進捗なし。
func (g *ganttChart) coordinates(startDate, endDate *time.Time, progress *float64, zoom int) ganttCoords {
	var c ganttCoords
	from, to := ganttDays(g.DateFrom), ganttDays(g.DateTo)
	if startDate == nil || endDate == nil {
		return c
	}
	sd, ed := ganttDays(*startDate), ganttDays(*endDate)
	if !(sd <= to && ed >= from) {
		return c
	}
	z := int64(zoom)
	if sd >= from {
		c.start = intp(int((sd - from) * z))
		c.barStart = intp(int((sd - from) * z))
	} else {
		c.barStart = intp(0)
	}
	if ed <= to {
		c.end = intp(int((ed - from + 1) * z))
		c.barEnd = intp(int((ed - from + 1) * z))
	} else {
		c.barEnd = intp(int((to - from + 1) * z))
	}
	if progress != nil {
		pd := calcProgressDate(*startDate, *endDate, *progress)
		if pd.cmp(from) > 0 && pd.cmp(sd) > 0 {
			if pd.cmp(to) < 0 {
				c.barProgressEnd = intp(pd.diffZoomFloor(from, zoom))
			} else {
				c.barProgressEnd = intp(int((to - from + 1) * z))
			}
		}
		if pd.cmp(ganttDays(g.today)) <= 0 {
			late := min(ganttDays(g.today), ed) + 1
			if late > from && late > sd {
				if late < to {
					c.barLateEnd = intp(int((late - from) * z))
				} else {
					c.barLateEnd = intp(int((to - from + 1) * z))
				}
			}
		}
	}
	return c
}

// ---------------------------------------------------------------- 描画

// render は render(options)。
func (g *ganttChart) render(o ganttOptions) {
	if o.topIncrement == 0 {
		o.topIncrement = 20
	}
	if o.indentIncrement == 0 {
		o.indentIncrement = 20
	}
	indent := 4
	if o.pdf != nil {
		indent = 0
	}
	switch o.only {
	case "":
		g.subjects.Reset()
		g.lines.Reset()
	case "selected_columns":
		g.columns[o.column.Name] = &strings.Builder{}
	}
	g.numberOfRows = 0
	err := g.projectTree(func(p *domain.Project, level int) error {
		o.indent = indent + level*o.indentIncrement
		return g.renderProject(p, &o)
	})
	if errors.Is(err, errGanttMaxLines) {
		g.Truncated = true
	}
	if o.pdf != nil {
		o.pdf.renderEnd()
	}
}

// projectTree は Project.project_tree(projects)。
func (g *ganttChart) projectTree(fn func(p *domain.Project, level int) error) error {
	var ancestors []*domain.Project
	for _, p := range g.projects {
		for len(ancestors) > 0 {
			a := g.nested[ancestors[len(ancestors)-1].ID]
			n := g.nested[p.ID]
			if a.Lft < n.Lft && n.Rgt < a.Rgt {
				break
			}
			ancestors = ancestors[:len(ancestors)-1]
		}
		if err := fn(p, len(ancestors)); err != nil {
			return err
		}
		ancestors = append(ancestors, p)
	}
	return nil
}

func (g *ganttChart) renderProject(p *domain.Project, o *ganttOptions) error {
	if err := g.renderObjectRow(p, o); err != nil {
		return err
	}
	o.indent += o.indentIncrement
	var noVersion []*query.IssueRow
	for _, r := range g.issuesByProject[p.ID] {
		if r.FixedVersionID == nil {
			noVersion = append(noVersion, r)
		}
	}
	if err := g.renderIssues(noVersion, o); err != nil {
		return err
	}
	for _, v := range g.versionsByProj[p.ID] {
		if err := g.renderVersion(p, v, o); err != nil {
			return err
		}
	}
	o.indent -= o.indentIncrement
	return nil
}

func (g *ganttChart) renderVersion(p *domain.Project, v *ganttVersion, o *ganttOptions) error {
	if err := g.renderObjectRow(v, o); err != nil {
		return err
	}
	o.indent += o.indentIncrement
	var issues []*query.IssueRow
	for _, r := range g.issuesByProject[p.ID] {
		if r.FixedVersionID != nil && *r.FixedVersionID == v.ID {
			issues = append(issues, r)
		}
	}
	if err := g.renderIssues(issues, o); err != nil {
		return err
	}
	o.indent -= o.indentIncrement
	return nil
}

// sortIssueKey は sort_issue_logic（祖先からの [start_date || Date.new, id] の配列）。
type ganttSortKey struct {
	date time.Time
	id   int64
}

func (g *ganttChart) sortIssueLogic(r *query.IssueRow) []ganttSortKey {
	var out []ganttSortKey
	cur := r
	for depth := 0; cur != nil && depth < 1000; depth++ {
		var d time.Time // Date.new（ユリウス暦 -4712-01-01。どの日付より前）
		if cur.StartDate != nil {
			d = *cur.StartDate
		}
		out = append([]ganttSortKey{{d, cur.ID}}, out...)
		if cur.ParentID == nil {
			break
		}
		cur = g.l.issue(*cur.ParentID)
	}
	return out
}

func compareGanttSortKeys(a, b []ganttSortKey) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := a[i].date.Compare(b[i].date); c != 0 {
			return c
		}
		if c := cmpInt64(a[i].id, b[i].id); c != 0 {
			return c
		}
	}
	return len(a) - len(b)
}

func (g *ganttChart) renderIssues(issues []*query.IssueRow, o *ganttOptions) error {
	keys := map[int64][]ganttSortKey{}
	for _, r := range issues {
		keys[r.ID] = g.sortIssueLogic(r)
	}
	issues = slices.Clone(issues)
	slices.SortStableFunc(issues, func(a, b *query.IssueRow) int { return compareGanttSortKeys(keys[a.ID], keys[b.ID]) })
	var ancestors []*query.IssueRow
	for _, r := range issues {
		for len(ancestors) > 0 && !isDescendantOf(r, ancestors[len(ancestors)-1]) {
			ancestors = ancestors[:len(ancestors)-1]
			o.indent -= o.indentIncrement
		}
		if err := g.renderObjectRow(r, o); err != nil {
			return err
		}
		if g.l.hasChildren(r.ID) {
			ancestors = append(ancestors, r)
			o.indent += o.indentIncrement
		}
	}
	o.indent -= o.indentIncrement * len(ancestors)
	return nil
}

func (g *ganttChart) renderObjectRow(obj any, o *ganttOptions) error {
	if o.pdf != nil {
		g.pdfObjectRow(obj, o)
	} else if o.only != "lines" && o.only != "selected_columns" {
		switch x := obj.(type) {
		case *domain.Project:
			g.htmlSubject(o, x)
		case *ganttVersion:
			g.htmlSubject(o, x)
		case *query.IssueRow:
			g.htmlSubject(o, x)
		}
	}
	if o.pdf == nil && o.only != "subjects" && o.only != "selected_columns" {
		switch x := obj.(type) {
		case *domain.Project:
			g.lineForProject(x, o)
		case *ganttVersion:
			g.lineForVersion(x, o)
		case *query.IssueRow:
			g.lineForIssue(x, o)
		}
	}
	if o.only == "selected_columns" && o.column != nil {
		if r, ok := obj.(*query.IssueRow); ok {
			g.columnContentForIssue(r, o)
		}
	}
	o.top += o.topIncrement
	g.numberOfRows++
	if g.hasMaxRows && g.numberOfRows >= g.MaxRows {
		return errGanttMaxLines
	}
	return nil
}

// ---------------------------------------------------------------- 判定（モデルのメソッド）

func (g *ganttChart) issueDueBefore(r *query.IssueRow) *time.Time {
	if r.DueDate != nil {
		return r.DueDate
	}
	if v := g.l.version(r.FixedVersionID); v != nil && v.EffectiveDate.Valid {
		t := v.EffectiveDate.Date.Time
		return &t
	}
	return nil
}

func (g *ganttChart) issueClosed(r *query.IssueRow) bool { return g.l.status(r.StatusID).IsClosed }

// issueOverdue は Issue#overdue?。
func (g *ganttChart) issueOverdue(r *query.IssueRow) bool {
	return r.DueDate != nil && r.DueDate.Before(g.today) && !g.issueClosed(r)
}

// issueBehindSchedule は Issue#behind_schedule?。
func (g *ganttChart) issueBehindSchedule(r *query.IssueRow) bool {
	if r.StartDate == nil || r.DueDate == nil {
		return false
	}
	n := ganttDays(*r.DueDate) - ganttDays(*r.StartDate) + 1
	done := ganttDays(*r.StartDate) + floorDiv64(n*int64(g.l.doneRatio(r)), 100)
	return done <= ganttDays(g.today)
}

// versionBehindSchedule は Version#behind_schedule?。
func (g *ganttChart) versionBehindSchedule(v *ganttVersion) bool {
	if !v.EffectiveDate.Valid || v.StartDate == nil || v.CompletedPercent == 100 {
		return false
	}
	n := float64(ganttDays(v.EffectiveDate.Date.Time) - ganttDays(*v.StartDate) + 1)
	done := ganttDays(*v.StartDate) + int64(math.Floor(n*v.CompletedPercent/100))
	return done <= ganttDays(g.today)
}

// versionOverdue は Version#overdue?。
func (g *ganttChart) versionOverdue(v *ganttVersion) bool {
	return v.EffectiveDate.Valid && v.EffectiveDate.Date.Time.Before(g.today) && v.OpenCount > 0
}

// ---------------------------------------------------------------- subjects

func (g *ganttChart) icon(name string) template.HTML {
	return g.a.Helpers.Icon(g.l.page, name, nil, nil)
}

// htmlSubjectContent は html_subject_content(object)。
func (g *ganttChart) htmlSubjectContent(obj any) template.HTML {
	switch x := obj.(type) {
	case *query.IssueRow:
		css := ""
		if g.issueOverdue(x) {
			css += " issue-overdue"
		}
		if g.issueBehindSchedule(x) {
			css += " issue-behind-schedule"
		}
		if x.AssignedToID == nil {
			css += " icon icon-issue"
		}
		if g.issueClosed(x) {
			css += " issue-closed"
		}
		done := g.l.doneRatio(x)
		if due := g.issueDueBefore(x); x.StartDate != nil && due != nil {
			pd := calcProgressDate(*x.StartDate, *due, float64(done))
			if pd.cmp(ganttDays(g.DateFrom)) < 0 {
				css += " behind-start-date"
			}
			if pd.cmp(ganttDays(g.DateTo)) > 0 && done > 0 {
				css += " over-end-date"
			}
		}
		var s template.HTML
		if x.AssignedToID == nil {
			s += g.icon("issue")
		}
		if u := g.l.principalPtr(x.AssignedToID); u != nil {
			s += g.assigneeAvatar(u)
		}
		s += g.l.linkToIssue(x, redmine.LinkToIssueOptions{})
		s += rails.ContentTag("input", nil, rails.NewHash("type", "checkbox", "name", "ids[]", "value", x.ID,
			"style", "display:none;", "class", "toggle-selection"))
		return rails.ContentTag("span", s, rails.NewHash("class", css))
	case *ganttVersion:
		cls := "icon icon-package "
		if g.versionBehindSchedule(x) {
			cls += "version-behind-schedule"
		}
		cls += " "
		if g.versionOverdue(x) {
			cls += "version-overdue"
		}
		if !x.IsOpen() {
			cls += " version-closed"
		}
		if x.EffectiveDate.Valid && x.StartDate != nil {
			pd := calcProgressDate(*x.StartDate, x.EffectiveDate.Date.Time, x.VisiblePercent)
			if pd.cmp(ganttDays(g.DateFrom)) < 0 {
				cls += " behind-start-date"
			}
			if pd.cmp(ganttDays(g.DateTo)) > 0 && x.VisiblePercent > 0 {
				cls += " over-end-date"
			}
		}
		s := g.icon("package") + g.linkToVersion(x)
		return rails.ContentTag("span", s, rails.NewHash("class", cls))
	case *domain.Project:
		cls := "icon icon-projects "
		if g.projectOverdue(x) {
			cls += "project-overdue"
		}
		s := g.icon("projects") + helper.LinkToProject(x)
		return rails.ContentTag("span", s, rails.NewHash("class", cls))
	}
	return ""
}

func (v *ganttVersion) IsOpen() bool { return v.Status == "open" }

// projectOverdue は Project#overdue?。
func (g *ganttChart) projectOverdue(p *domain.Project) bool {
	info := g.projectInfo[p.ID]
	return p.Active() && info != nil && info.DueDate != nil && info.DueDate.Before(g.today)
}

// assigneeAvatar は assignee_avatar(user, :size => 13, :class => 'icon-avatar')。
func (g *ganttChart) assigneeAvatar(u *domain.User) template.HTML {
	return g.a.Helpers.Avatar(g.l.page, u, rails.NewHash("size", 13, "class", "icon-avatar",
		"title", g.c.L("field_assigned_to")+": "+g.l.principalName(u)))
}

// linkToVersion は link_to_version(version)。
func (g *ganttChart) linkToVersion(v *ganttVersion) template.HTML {
	name := v.Name
	if g.c.Project == nil || g.c.Project.ID != v.ProjectID {
		name = v.ProjectName + " - " + v.Name
	}
	var title any
	if v.EffectiveDate.Valid {
		title = g.c.Loc.FormatDate(v.EffectiveDate.Date.Time)
	}
	visible := v.Project != nil && g.c.AllowedTo(domain.Perm("view_issues"), v.Project)
	return rails.LinkToIf(visible, name, "/versions/"+strconv.FormatInt(v.ID, 10), rails.NewHash("title", title))
}

// htmlSubject は html_subject(params, subject, object)。
func (g *ganttChart) htmlSubject(o *ganttOptions, obj any) {
	content := g.htmlSubjectContent(obj)
	opts := rails.NewHash()
	hasChildren := false
	var objID string
	switch x := obj.(type) {
	case *query.IssueRow:
		opts.Set("id", "issue-"+strconv.FormatInt(x.ID, 10))
		opts.Set("class", "issue-subject hascontextmenu")
		opts.Set("title", x.Subject)
		if g.l.hasChildren(x.ID) {
			for _, child := range g.issuesByProject[x.ProjectID] {
				if child.ParentID != nil && *child.ParentID == x.ID && int64PtrEq(child.FixedVersionID, x.FixedVersionID) {
					hasChildren = true
					break
				}
			}
		}
		objID = "issue-" + strconv.FormatInt(x.ID, 10)
	case *ganttVersion:
		opts.Set("id", "version-"+strconv.FormatInt(x.ID, 10))
		opts.Set("class", "version-name")
		hasChildren = x.HasFixedIssues
		objID = "version-" + strconv.FormatInt(x.ID, 10)
	case *domain.Project:
		opts.Set("class", "project-name")
		if info := g.projectInfo[x.ID]; info != nil {
			hasChildren = info.HasChildren
		}
		objID = "project-" + strconv.FormatInt(x.ID, 10)
	}
	opts.Set("data", rails.NewHash(
		"collapse_expand", `{"top_increment":`+strconv.Itoa(o.topIncrement)+`,"obj_id":`+jsonString(objID)+`}`,
		"number_of_rows", g.numberOfRows))
	indent := o.indent
	if hasChildren {
		content = rails.ContentTag("span", g.icon("angle-down"), rails.NewHash("class", "icon icon-expanded expander")) + content
		opts.Set("class", rails.ToS(opts.Get("class"))+" open")
	} else {
		indent += 18
	}
	style := "position: absolute;top:" + strconv.Itoa(o.top) + "px;left:" + strconv.Itoa(indent) + "px;"
	if o.subjectWidth != 0 {
		style += "width:" + strconv.Itoa(o.subjectWidth-indent) + "px;"
	}
	opts.Set("style", style)
	g.subjects.WriteString(string(rails.ContentTag("div", content, opts)))
}

func int64PtrEq(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// ---------------------------------------------------------------- lines

func (g *ganttChart) lineForProject(p *domain.Project, o *ganttOptions) {
	info := g.projectInfo[p.ID]
	if info == nil || info.StartDate == nil || info.DueDate == nil {
		return
	}
	g.htmlTask(o, g.coordinates(info.StartDate, info.DueDate, nil, o.zoom), true, rails.H(p.Name), p)
}

func (g *ganttChart) lineForVersion(v *ganttVersion, o *ganttOptions) {
	if !v.EffectiveDate.Valid || v.StartDate == nil {
		return
	}
	// label = "#{h(version)} #{h(pct.round)}%" は html_safe でない文字列のため content_tag で再度エスケープされる
	label := rails.H(string(rails.H(v.Name)) + " " + strconv.FormatInt(int64(math.Round(v.VisiblePercent)), 10) + "%")
	if g.Project == nil || g.Project.ID != v.ProjectID {
		pname := ""
		if v.Project != nil {
			pname = v.Project.Name
		}
		label = rails.H(pname+" -") + label
	}
	due := v.EffectiveDate.Date.Time
	pct := v.VisiblePercent
	g.htmlTask(o, g.coordinates(v.StartDate, &due, &pct, o.zoom), true, label, v)
}

func (g *ganttChart) lineForIssue(r *query.IssueRow, o *ganttOptions) {
	due := g.issueDueBefore(r)
	if due == nil {
		return
	}
	label := g.l.status(r.StatusID).Name
	if !slices.Contains(g.l.tracker(r.TrackerID).DisabledCoreFields, "done_ratio") {
		label += " " + strconv.Itoa(g.l.doneRatio(r)) + "%"
	}
	done := float64(g.l.doneRatio(r))
	g.htmlTask(o, g.coordinates(r.StartDate, due, &done, o.zoom), g.l.hasChildren(r.ID), rails.H(label), r)
}

// issueRelationsJSON は issue_relations(issue).to_json（関連が無ければ ""）。
func (g *ganttChart) issueRelationsJSON(id int64) string {
	rels := g.relations[id]
	if len(rels) == 0 {
		return ""
	}
	var order []string
	m := map[string][]int64{}
	for _, r := range rels {
		if _, ok := m[r.RelationType]; !ok {
			order = append(order, r.RelationType)
		}
		m[r.RelationType] = append(m[r.RelationType], r.IssueToID)
	}
	var b strings.Builder
	b.WriteString("{")
	for i, t := range order {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(jsonString(t) + ":[")
		for j, id := range m[t] {
			if j > 0 {
				b.WriteString(",")
			}
			b.WriteString(strconv.FormatInt(id, 10))
		}
		b.WriteString("]")
	}
	b.WriteString("}")
	return b.String()
}

// htmlTask は html_task(params, coords, markers, label, object)。
func (g *ganttChart) htmlTask(o *ganttOptions, c ganttCoords, markers bool, label template.HTML, obj any) {
	var out strings.Builder
	var objKey string
	var issue *query.IssueRow
	css := "task "
	switch x := obj.(type) {
	case *domain.Project:
		objKey = "project-" + strconv.FormatInt(x.ID, 10)
		css += "project"
	case *ganttVersion:
		objKey = "version-" + strconv.FormatInt(x.ID, 10)
		css += "version"
	case *query.IssueRow:
		issue = x
		objKey = "issue-" + strconv.FormatInt(x.ID, 10)
		if g.l.hasChildren(x.ID) {
			css += "parent"
		} else {
			css += "leaf"
		}
	}
	dataOpts := func() *rails.Hash {
		return rails.NewHash("collapse_expand", objKey, "number_of_rows", g.numberOfRows)
	}
	top := "top:" + strconv.Itoa(o.top) + "px;"
	nbsp := template.HTML("&nbsp;")
	if c.barStart != nil && c.barEnd != nil {
		width := *c.barEnd - *c.barStart - 2
		style := top + "left:" + strconv.Itoa(*c.barStart) + "px;" + "width:" + strconv.Itoa(width) + "px;"
		var htmlID any
		switch x := obj.(type) {
		case *query.IssueRow:
			htmlID = "task-todo-issue-" + strconv.FormatInt(x.ID, 10)
		case *ganttVersion:
			htmlID = "task-todo-version-" + strconv.FormatInt(x.ID, 10)
		}
		data := rails.NewHash()
		if issue != nil {
			if rels := g.issueRelationsJSON(issue.ID); rels != "" {
				data.Set("rels", rels)
			}
		}
		data.Update(dataOpts())
		out.WriteString(string(rails.ContentTag("div", nbsp, rails.NewHash("style", style, "class", css+" task_todo", "id", htmlID, "data", data))))
		if c.barLateEnd != nil {
			width := *c.barLateEnd - *c.barStart - 2
			style := top + "left:" + strconv.Itoa(*c.barStart) + "px;" + "width:" + strconv.Itoa(width) + "px;"
			out.WriteString(string(rails.ContentTag("div", nbsp, rails.NewHash("style", style, "class", css+" task_late", "data", dataOpts()))))
		}
		if c.barProgressEnd != nil {
			width := *c.barProgressEnd - *c.barStart - 2
			style := top + "left:" + strconv.Itoa(*c.barStart) + "px;" + "width:" + strconv.Itoa(width) + "px;"
			var htmlID any
			switch x := obj.(type) {
			case *query.IssueRow:
				htmlID = "task-done-issue-" + strconv.FormatInt(x.ID, 10)
			case *ganttVersion:
				htmlID = "task-done-version-" + strconv.FormatInt(x.ID, 10)
			}
			out.WriteString(string(rails.ContentTag("div", nbsp, rails.NewHash("style", style, "class", css+" task_done", "id", htmlID, "data", dataOpts()))))
		}
	}
	if markers {
		if c.start != nil {
			style := top + "left:" + strconv.Itoa(*c.start) + "px;" + "width:15px;"
			out.WriteString(string(rails.ContentTag("div", nbsp, rails.NewHash("style", style, "class", css+" marker starting", "data", dataOpts()))))
		}
		if c.end != nil {
			style := top + "left:" + strconv.Itoa(*c.end) + "px;" + "width:15px;"
			out.WriteString(string(rails.ContentTag("div", nbsp, rails.NewHash("style", style, "class", css+" marker ending", "data", dataOpts()))))
		}
	}
	// label（常に存在する）
	{
		be := 0
		if c.barEnd != nil {
			be = *c.barEnd
		}
		style := top + "left:" + strconv.Itoa(be+8) + "px;" + "width:15px;"
		out.WriteString(string(rails.ContentTag("div", label, rails.NewHash("style", style, "class", css+" label", "data", dataOpts()))))
	}
	if issue != nil && c.barStart != nil && c.barEnd != nil {
		s := rails.ContentTag("span", g.issueTooltip(issue), rails.NewHash("class", "tip"))
		s += rails.ContentTag("input", nil, rails.NewHash("type", "checkbox", "name", "ids[]", "value", issue.ID,
			"style", "display:none;", "class", "toggle-selection"))
		style := "position: absolute;" + top + "left:" + strconv.Itoa(*c.barStart) + "px;" +
			"width:" + strconv.Itoa(*c.barEnd-*c.barStart) + "px;" + "height:12px;"
		out.WriteString(string(rails.ContentTag("div", s, rails.NewHash("style", style, "class", "tooltip hascontextmenu", "data", dataOpts()))))
	}
	g.lines.WriteString(out.String())
}

// issueTooltip は render_issue_tooltip(issue)。
func (g *ganttChart) issueTooltip(r *query.IssueRow) template.HTML {
	c := g.c
	date := func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return c.Loc.FormatDate(*t)
	}
	st := g.l.status(r.StatusID)
	status := string(rails.H(st.Name))
	if st.IsClosed {
		status += " (" + date(r.ClosedAt) + ")"
	}
	var assignee string
	if u := g.l.principalPtr(r.AssignedToID); u != nil {
		assignee = string(g.a.Helpers.Avatar(g.l.page, u, rails.NewHash("size", "13", "title", c.L("field_assigned_to")))) +
			" " + string(rails.H(g.l.principalName(u)))
	} else {
		assignee = " "
	}
	return g.l.linkToIssue(r, redmine.LinkToIssueOptions{}) + "<br /><br />" +
		template.HTML("<strong>"+c.L("field_project")+"</strong>: "+string(helper.LinkToProject(g.l.project(r.ProjectID)))+"<br />") +
		template.HTML("<strong>"+c.L("field_status")+"</strong>: "+status+"<br />") +
		template.HTML("<strong>"+c.L("field_start_date")+"</strong>: "+date(r.StartDate)+"<br />") +
		template.HTML("<strong>"+c.L("field_due_date")+"</strong>: "+date(r.DueDate)+"<br />") +
		template.HTML("<strong>"+c.L("field_assigned_to")+"</strong>: "+assignee+"<br />") +
		template.HTML("<strong>"+c.L("field_priority")+"</strong>: "+string(rails.H(g.l.priority(r.PriorityID).Name)))
}

// ---------------------------------------------------------------- selected columns

// columnContentForIssue は column_content_for_issue(issue, options)。
func (g *ganttChart) columnContentForIssue(r *query.IssueRow, o *ganttOptions) {
	name := o.column.Name
	style := "position: absolute;top: " + strconv.Itoa(o.top) + "px; font-size: 0.8em;"
	content := rails.ContentTag("div", g.l.columnContent(o.column, r), rails.NewHash("style", style, "class", "issue_"+name,
		"id", name+"_issue_"+strconv.FormatInt(r.ID, 10),
		"data", rails.NewHash("collapse_expand", "issue-"+strconv.FormatInt(r.ID, 10), "number_of_rows", g.numberOfRows)))
	if b := g.columns[name]; b != nil {
		b.WriteString(string(content))
	}
}

// SelectedColumnContent は selected_column_content(:column => column, ...)。
func (g *ganttChart) selectedColumnContent(col *query.Column, o ganttOptions) string {
	if b, ok := g.columns[col.Name]; ok {
		return b.String()
	}
	o.only = "selected_columns"
	o.column = col
	g.render(o)
	return g.columns[col.Name].String()
}
