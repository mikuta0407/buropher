package handler

// このファイルは Version モデル（app/models/version.rb の FixedIssuesExtension・completed? 等）と
// VersionsHelper（version_anchor, version_filtered_issues_path, render_issue_status_by,
// link_to_new_issue）のうち、VersionsController の画面と API が使う計算。

import (
	"context"
	"errors"
	"html/template"
	"math"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// versionKBSum は Ruby の Array#sum（Float は Kahan-Babuska の補正付き加算）。
func versionKBSum(vs []float64) float64 {
	if len(vs) == 0 {
		return 0
	}
	f, c := 0.0, 0.0
	for _, x := range vs {
		t := f + x
		if math.Abs(f) >= math.Abs(x) {
			c += (f - t) + x
		} else {
			c += (x - t) + f
		}
		f = t
	}
	return f + c
}

// versionIssueSet は fixed_issues（または visible_fixed_issues）の集計（FixedIssuesExtension）。
type versionIssueSet struct {
	Issues []*repository.VersionIssue
	// totalEst は total_estimated_hours.to_f（issue id → 値）。
	totalEst map[int64]float64
}

// Count は count。
func (s *versionIssueSet) Count() int { return len(s.Issues) }

// OpenCount は open_count。
func (s *versionIssueSet) OpenCount() int {
	n := 0
	for _, i := range s.Issues {
		if !i.StatusClosed {
			n++
		}
	}
	return n
}

// ClosedCount は closed_count。
func (s *versionIssueSet) ClosedCount() int { return s.Count() - s.OpenCount() }

// estimatedAverage は estimated_average。
func (s *versionIssueSet) estimatedAverage() float64 {
	var vals []float64
	for _, i := range s.Issues {
		if v := s.totalEst[i.ID]; v > 0 {
			vals = append(vals, v)
		}
	}
	if len(vals) == 0 {
		return 1.0
	}
	return versionKBSum(vals) / float64(len(vals))
}

// issuesProgress は issues_progress(open)。
func (s *versionIssueSet) issuesProgress(open bool) float64 {
	count := s.Count()
	if count == 0 {
		return 0
	}
	avg := s.estimatedAverage()
	var vals []float64
	for _, i := range s.Issues {
		if i.StatusClosed == open {
			continue
		}
		est := s.totalEst[i.ID]
		if !(est > 0) {
			est = avg
		}
		ratio := 100
		if open {
			ratio = i.DoneRatio
		}
		vals = append(vals, est*float64(ratio))
	}
	return versionKBSum(vals) / (avg * float64(count))
}

// CompletedPercent は completed_percent。
func (s *versionIssueSet) CompletedPercent() float64 {
	if s.Count() == 0 {
		return 0
	}
	if s.OpenCount() == 0 {
		return 100
	}
	return s.issuesProgress(false) + s.issuesProgress(true)
}

// ClosedPercent は closed_percent。
func (s *versionIssueSet) ClosedPercent() float64 {
	if s.Count() == 0 {
		return 0
	}
	if s.OpenCount() == 0 {
		return 100
	}
	return s.issuesProgress(false)
}

// loadVersionIssueSet は versionID の fixed_issues を読み込む（visibleCond が空でなければ visible のみ）。
// total_estimated_hours の子孫の合計は常に User.current に見えるチケットだけを数える（Issue#total_estimated_hours）。
func (a *App) loadVersionIssueSet(ctx context.Context, issueVis string, versionID int64, visibleCond string) (*versionIssueSet, error) {
	sets, err := a.loadVersionIssueSets(ctx, issueVis, []int64{versionID}, visibleCond)
	if err != nil {
		return nil, err
	}
	return sets[versionID], nil
}

// loadVersionIssueSets は versionIDs の各バージョンについて loadVersionIssueSet をまとめて行う
// （チケットと子孫の見積時間の合計をバージョン数・チケット数によらない回数のクエリで読む）。
func (a *App) loadVersionIssueSets(ctx context.Context, issueVis string, versionIDs []int64, visibleCond string) (map[int64]*versionIssueSet, error) {
	// 並びは issues.id（バージョンごとに分けても id 順のまま）
	rows, err := repository.VersionIssues(ctx, a.DB, versionIDs, visibleCond, "", "")
	if err != nil {
		return nil, err
	}
	sets, err := a.versionIssueSets(ctx, issueVis, versionIDs, rows, func(*repository.VersionIssue) bool { return true })
	if err != nil {
		return nil, err
	}
	return sets[0], nil
}

// versionIssueSets は rows を versionIDs ごとの集計に分ける。filters の数だけ集合を作り、
// 各行は filters[k] が真の集合 k に入る。子を持つチケットの見積時間は子孫の合計（issueVis で可視なもの）。
func (a *App) versionIssueSets(ctx context.Context, issueVis string, versionIDs []int64, rows []*repository.VersionIssue,
	filters ...func(*repository.VersionIssue) bool) ([]map[int64]*versionIssueSet, error) {
	out := make([]map[int64]*versionIssueSet, len(filters))
	for k := range filters {
		out[k] = make(map[int64]*versionIssueSet, len(versionIDs))
		for _, id := range versionIDs {
			out[k][id] = &versionIssueSet{totalEst: map[int64]float64{}}
		}
	}
	var parents []int64
	for _, i := range rows {
		if i.HasChildren {
			parents = append(parents, i.ID)
		}
	}
	sums, err := repository.IssuesSubtreeEstimatedHours(ctx, a.DB, parents, issueVis)
	if err != nil {
		return nil, err
	}
	for _, i := range rows {
		est := i.EstimatedHours.Float64
		if i.HasChildren {
			est = sums[i.ID]
		}
		for k, f := range filters {
			s := out[k][i.FixedVersionID]
			if s == nil || !f(i) {
				continue
			}
			s.Issues = append(s.Issues, i)
			s.totalEst[i.ID] = est
		}
	}
	return out, nil
}

// versionModel は 1 つのバージョンの表示・API に必要な値。
type versionModel struct {
	*domain.Version
	Project *domain.Project
	// All は fixed_issues、Visible は visible_fixed_issues。
	All, Visible *versionIssueSet
	today        time.Time
}

// Completed は completed?。
func (v *versionModel) Completed() bool {
	if v.IsClosed() {
		return true
	}
	return v.EffectiveDate != nil && v.EffectiveDate.Before(v.today) && v.All.OpenCount() == 0
}

// CSSClasses は css_classes。
func (v *versionModel) CSSClasses() string {
	s := "version-incompleted"
	if v.Completed() {
		s = "version-completed"
	}
	return s + " version-" + v.Status
}

// DescriptionString は description（nil は ""）。
func (v *versionModel) DescriptionString() string {
	if v.Description == nil {
		return ""
	}
	return *v.Description
}

// WikiPageTitleString は wiki_page_title（nil は ""）。
func (v *versionModel) WikiPageTitleString() string {
	if v.WikiPageTitle == nil {
		return ""
	}
	return *v.WikiPageTitle
}

// versionCtx はリクエスト内で使い回す値（可視性の SQL・今日の日付など）。
type versionCtx struct {
	a        *App
	c        *Req
	issueVis string
	today    time.Time
	projects map[int64]*domain.Project
	pg       *helper.Page
	// all / visible は preload で読み込んだ fixed_issues / visible_fixed_issues（バージョン id → 集計）。
	all, visible map[int64]*versionIssueSet
}

// preload は versions の fixed_issues / visible_fixed_issues をまとめて読み込み、model で使う。
func (vc *versionCtx) preload(versions []*domain.Version) error {
	ctx := vc.c.Ctx()
	ids := make([]int64, len(versions))
	for i, v := range versions {
		ids[i] = v.ID
	}
	rows, err := repository.VersionIssuesWithVisibility(ctx, vc.a.DB, ids, vc.issueVis)
	if err != nil {
		return err
	}
	sets, err := vc.a.versionIssueSets(ctx, vc.issueVis, ids, rows,
		func(*repository.VersionIssue) bool { return true },
		func(i *repository.VersionIssue) bool { return i.Visible })
	if err != nil {
		return err
	}
	vc.all, vc.visible = sets[0], sets[1]
	return nil
}

func (a *App) newVersionCtx(c *Req) (*versionCtx, error) {
	vis, err := c.Authz().IssueVisibleCondition(c.Ctx(), authz.ConditionOptions{})
	if err != nil {
		return nil, err
	}
	return &versionCtx{a: a, c: c, issueVis: vis, today: a.userToday(c), projects: map[int64]*domain.Project{}}, nil
}

func (vc *versionCtx) project(id int64) (*domain.Project, error) {
	if p, ok := vc.projects[id]; ok {
		return p, nil
	}
	p, err := repository.GetProject(vc.c.Ctx(), vc.a.DB, id)
	if err != nil {
		return nil, err
	}
	vc.projects[id] = p
	return p, nil
}

// model は v の versionModel を作る。
func (vc *versionCtx) model(v *domain.Version) (*versionModel, error) {
	ctx := vc.c.Ctx()
	p, err := vc.project(v.ProjectID)
	if err != nil {
		return nil, err
	}
	all, visible := vc.all[v.ID], vc.visible[v.ID]
	if all == nil || visible == nil {
		if all, err = vc.a.loadVersionIssueSet(ctx, vc.issueVis, v.ID, ""); err != nil {
			return nil, err
		}
		if visible, err = vc.a.loadVersionIssueSet(ctx, vc.issueVis, v.ID, vc.issueVis); err != nil {
			return nil, err
		}
	}
	return &versionModel{Version: v, Project: p, All: all, Visible: visible, today: vc.today}, nil
}

// ---------------------------------------------------------------- ヘルパー

// versionAnchor は version_anchor(version)（anchor は空白を _ に）。
func versionAnchor(current *domain.Project, v *versionModel) string {
	s := v.Name
	if current == nil || current.ID != v.ProjectID {
		s = v.Project.Identifier + "-" + v.Name
	}
	return strings.ReplaceAll(s, " ", "_")
}

// formatVersionName は format_version_name(version)。
func formatVersionName(current *domain.Project, v *versionModel) string {
	if current != nil && current.ID == v.ProjectID {
		return v.Name
	}
	return v.Project.Name + " - " + v.Name
}

// versionQuery は Hash#to_query（"k=v" の文字列でソート。配列の値は key[]=v を & で連結して 1 要素）。
func versionQuery(pairs ...any) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		k := pairs[i].(string)
		switch x := pairs[i+1].(type) {
		case []string:
			var arr []string
			for _, s := range x {
				arr = append(arr, url.QueryEscape(k+"[]")+"="+url.QueryEscape(s))
			}
			parts = append(parts, strings.Join(arr, "&"))
		default:
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(rails.ToS(x)))
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "&")
}

// linkToVersion は link_to_version(version, options)（title => format_date(effective_date) を先頭に）。
func (vc *versionCtx) linkToVersion(v *versionModel, name string) template.HTML {
	var title any
	if v.EffectiveDate != nil {
		title = vc.c.Loc.FormatDate(*v.EffectiveDate)
	}
	opts := rails.NewHash("title", title)
	if name != "" {
		opts.Set("name", name)
	}
	visible, _ := vc.c.Authz().AllowedTo(vc.c.Ctx(), domain.Perm("view_issues"), v.Project)
	return rails.LinkToIf(visible, formatVersionName(vc.c.Project, v), "/versions/"+strconv.FormatInt(v.ID, 10), opts)
}

// filteredIssuesPath は version_filtered_issues_path(version, :status_id => status)。
func (vc *versionCtx) filteredIssuesPath(v *versionModel, status string) string {
	q := versionQuery("fixed_version_id", v.ID, "set_filter", 1, "status_id", status)
	var p *domain.Project
	switch v.Sharing {
	case "tree":
		ctx := vc.c.Ctx()
		roots, err := repository.ProjectSelfAndAncestors(ctx, vc.a.DB, v.ProjectID)
		if err == nil && len(roots) > 0 {
			root := roots[0]
			for _, r := range roots {
				if r.ParentID == nil {
					root = r
				}
			}
			visible, _ := vc.c.Authz().ProjectVisible(ctx, root)
			allowed, _ := vc.c.Authz().AllowedTo(ctx, domain.Perm("view_issues"), root)
			if visible && allowed {
				p = root
			}
		}
	case "system":
	default:
		p = v.Project
	}
	if p == nil {
		return "/issues?" + q
	}
	return "/projects/" + p.Identifier + "/issues?" + q
}

// ProgressBarHTML は progress_bar([closed, done], :titles => [...], :legend => ...)。
func versionProgressBar(closed, done float64, titles []string, legend string) template.HTML {
	pcts := []int{int(math.Floor(closed)), int(math.Floor(done))}
	pcts[1] = pcts[1] - pcts[0]
	pcts = append(pcts, 100-pcts[1]-pcts[0])
	for len(titles) < 3 {
		titles = append(titles, "")
	}
	if titles[0] == "" {
		titles[0] = strconv.Itoa(pcts[0]) + "%"
	}
	title := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	var cells template.HTML
	if pcts[0] > 0 {
		cells += rails.ContentTag("td", "", rails.NewHash("style", "width: "+strconv.Itoa(pcts[0])+"%;", "class", "closed", "title", title(titles[0])))
	}
	if pcts[1] > 0 {
		cells += rails.ContentTag("td", "", rails.NewHash("style", "width: "+strconv.Itoa(pcts[1])+"%;", "class", "done", "title", title(titles[1])))
	}
	if pcts[2] > 0 {
		cells += rails.ContentTag("td", "", rails.NewHash("style", "width: "+strconv.Itoa(pcts[2])+"%;", "class", "todo", "title", title(titles[2])))
	}
	return rails.ContentTag("table", rails.ContentTag("tr", cells, nil), rails.NewHash("class", "progress progress-"+strconv.Itoa(pcts[0]))) +
		rails.ContentTag("p", legend, rails.NewHash("class", "percent"))
}

// versionHTMLHours は html_hours(text)。
func versionHTMLHours(s string) template.HTML {
	return template.HTML(versionHoursRe.ReplaceAllString(s, `<span class="hours hours-int">$1</span><span class="hours hours-dec">$2$3</span>`))
}

// versionOverview は versions/_overview の値。
type versionOverview struct {
	Completed       bool
	HasCustomFields bool
	CompletedDate   string
	DueWords        string
	DueDate         string
	Description     string
	CustomValues    []versionCustomValueView
	HasIssues       bool
	ProgressBar     template.HTML
	IssuesLink      template.HTML
	ClosedLink      template.HTML
	OpenLink        template.HTML
}

// overview は versions/_overview を組み立てる。
func (vc *versionCtx) overview(v *versionModel) (*versionOverview, error) {
	c := vc.c
	o := &versionOverview{Description: v.DescriptionString()}
	if v.Completed() {
		o.Completed = true
		if v.EffectiveDate != nil {
			o.CompletedDate = c.Loc.FormatDate(*v.EffectiveDate)
		}
	} else if v.EffectiveDate != nil {
		key := "label_roadmap_due_in"
		if v.EffectiveDate.Before(vc.today) {
			key = "label_roadmap_overdue"
		}
		o.DueWords = c.L(key, c.Loc.DistanceOfDateInWords(vc.today, *v.EffectiveDate))
		o.DueDate = c.Loc.FormatDate(*v.EffectiveDate)
	}
	all, err := vc.a.versionCustomValues(c, v.Version, v.Project, false)
	if err != nil {
		return nil, err
	}
	o.HasCustomFields = len(all) > 0
	cvs, err := vc.a.versionCustomValues(c, v.Version, v.Project, true)
	if err != nil {
		return nil, err
	}
	for _, cv := range cvs {
		if s := strings.Join(cv.Values, ", "); s != "" {
			o.CustomValues = append(o.CustomValues, versionCustomValueView{Name: cv.Field.Name, Formatted: s})
		}
	}
	vis := v.Visible
	if vis.Count() > 0 {
		o.HasIssues = true
		cp, dp := vis.ClosedPercent(), vis.CompletedPercent()
		o.ProgressBar = versionProgressBar(cp, dp, []string{
			c.L("label_closed_issues_plural") + ": " + strconv.Itoa(int(cp)) + "%",
			c.L("field_done_ratio") + ": " + strconv.Itoa(int(dp)) + "%",
		}, strconv.Itoa(int(dp))+"%")
		o.IssuesLink = rails.LinkTo(c.L("label_x_issues", map[string]any{"count": vis.Count()}), vc.filteredIssuesPath(v, "*"), nil)
		o.ClosedLink = rails.LinkToIf(vis.ClosedCount() > 0, c.L("label_x_closed_issues_abbr", map[string]any{"count": vis.ClosedCount()}), vc.filteredIssuesPath(v, "c"), nil)
		o.OpenLink = rails.LinkToIf(vis.OpenCount() > 0, c.L("label_x_open_issues_abbr", map[string]any{"count": vis.OpenCount()}), vc.filteredIssuesPath(v, "o"), nil)
	}
	return o, nil
}

// versionCustomValueView は _overview のカスタムフィールド 1 行。
type versionCustomValueView struct {
	Name      string
	Formatted string
}

// relatedIssueRow は関連チケット一覧の 1 行。
type relatedIssueRow struct {
	ID       int64
	CSS      string
	Assignee *domain.User
	Link     template.HTML
}

// relatedIssueRows は一覧の行を組み立てる（link_to_issue(issue, :project => (@project != issue.project))）。
func (vc *versionCtx) relatedIssueRows(list []*repository.VersionIssue) ([]*relatedIssueRow, error) {
	if len(list) == 0 {
		return nil, nil
	}
	var uids []int64
	for _, i := range list {
		if i.AssignedToID.Valid {
			uids = append(uids, i.AssignedToID.Int64)
		}
	}
	users, err := repository.UsersByIDs(vc.c.Ctx(), vc.a.DB, uids)
	if err != nil {
		return nil, err
	}
	r := vc.a.Helpers.WikiRenderer(vc.page())
	out := make([]*relatedIssueRow, len(list))
	for k, i := range list {
		ri := redmine.Issue(i.RefIssue)
		row := &relatedIssueRow{ID: i.ID, CSS: r.IssueCSSClasses(&ri)}
		if i.AssignedToID.Valid {
			row.Assignee = users[i.AssignedToID.Int64]
		}
		row.Link = r.LinkToIssue(&ri, redmine.LinkToIssueOptions{Project: vc.c.Project == nil || vc.c.Project.ID != i.ProjectID})
		out[k] = row
	}
	return out, nil
}

func (vc *versionCtx) page() *helper.Page {
	if vc.pg == nil {
		vc.pg = vc.c.Page()
	}
	return vc.pg
}

var versionHoursRe = regexp.MustCompile(`(\d+)([\.,:])(\d+)`)

// statusByCriterias は VersionsHelper::STATUS_BY_CRITERIAS。
var statusByCriterias = []string{"tracker", "status", "priority", "author", "assigned_to", "category"}

// statusByRow は render_issue_status_by の 1 行。
type statusByRow struct {
	Link        template.HTML
	ProgressBar template.HTML
}

// statusBy は versions/_issue_counts の値。
type statusBy struct {
	Criteria string
	Options  template.HTML
	Rows     []statusByRow
	URL      string
	FormPath string
}

// statusByGroup は集計のキー（関連オブジェクト）。
type statusByGroup struct {
	id    int64
	name  string
	pos   int
	isGrp bool
}

// renderIssueStatusBy は render_issue_status_by(version, criteria)。
func (vc *versionCtx) renderIssueStatusBy(v *versionModel, criteria string) (*statusBy, error) {
	c := vc.c
	ctx := c.Ctx()
	if !slices.Contains(statusByCriterias, criteria) {
		criteria = "tracker"
	}
	col := criteria + "_id"
	total, err := repository.VersionIssueCountsBy(ctx, vc.a.DB, v.ID, col, vc.issueVis, false)
	if err != nil {
		return nil, err
	}
	open, err := repository.VersionIssueCountsBy(ctx, vc.a.DB, v.ID, col, vc.issueVis, true)
	if err != nil {
		return nil, err
	}
	type cnt struct{ total, open int }
	counts := map[int64]*cnt{}
	var keys []int64
	hasNil := false
	var nilCnt cnt
	for _, r := range total {
		if !r.Key.Valid {
			hasNil = true
			nilCnt.total = r.Count
			continue
		}
		counts[r.Key.Int64] = &cnt{total: r.Count}
		keys = append(keys, r.Key.Int64)
	}
	for _, r := range open {
		if !r.Key.Valid {
			nilCnt.open = r.Count
			continue
		}
		if x, ok := counts[r.Key.Int64]; ok {
			x.open = r.Count
		}
	}
	groups, err := vc.statusByGroups(criteria, keys)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(keys, func(i, j int) bool {
		gi, gj := groups[keys[i]], groups[keys[j]]
		return compareStatusByGroup(criteria, gi, gj) < 0
	})
	sb := &statusBy{Criteria: criteria, URL: "/versions/" + strconv.FormatInt(v.ID, 10) + "/status_by", FormPath: versionFormPath(c)}
	var opts []any
	for _, cr := range statusByCriterias {
		opts = append(opts, []any{c.L("field_" + cr), cr})
	}
	sb.Options = rails.OptionsForSelect(opts, criteria)
	link := func(text string, val string, ct cnt) statusByRow {
		u := "/projects/" + v.Project.Identifier + "/issues?" + versionQuery(criteria+"_id", val, "fixed_version_id", v.ID, "set_filter", 1, "status_id", "*")
		closed := ct.total - ct.open
		pct := float64(closed) / float64(ct.total) * 100
		return statusByRow{
			Link:        rails.LinkTo(text, u, nil),
			ProgressBar: versionProgressBar(pct, pct, nil, strconv.Itoa(closed)+"/"+strconv.Itoa(ct.total)),
		}
	}
	for _, k := range keys {
		g := groups[k]
		name := ""
		if g != nil {
			name = g.name
		}
		sb.Rows = append(sb.Rows, link(name, strconv.FormatInt(k, 10), *counts[k]))
	}
	if hasNil {
		sb.Rows = append(sb.Rows, link(c.L("label_none"), "!*", nilCnt))
	}
	return sb, nil
}

func compareStatusByGroup(criteria string, a, b *statusByGroup) int {
	if a == nil || b == nil {
		return 0
	}
	switch criteria {
	case "author", "assigned_to":
		// Principal#<=>（同じ種類なら to_s の casecmp、ユーザーがグループより前）
		if a.isGrp != b.isGrp {
			if a.isGrp {
				return 1
			}
			return -1
		}
		return strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name))
	case "category":
		return strings.Compare(a.name, b.name)
	}
	if a.pos != b.pos {
		if a.pos < b.pos {
			return -1
		}
		return 1
	}
	return 0
}

// statusByGroups は集計キーのオブジェクト（名前と並び順）を読み込む。
func (vc *versionCtx) statusByGroups(criteria string, ids []int64) (map[int64]*statusByGroup, error) {
	ctx := vc.c.Ctx()
	out := map[int64]*statusByGroup{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := repository.VersionStatusByObjects(ctx, vc.a.DB, criteria, ids)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		g := &statusByGroup{id: r.ID, name: r.Name, pos: r.Position}
		if criteria == "author" || criteria == "assigned_to" {
			u, err := repository.GetPrincipal(ctx, vc.a.DB, r.ID)
			if err == nil {
				if u.Kind.IsGroup() {
					g.isGrp = true
					g.name = u.Name
				} else {
					uu, err := repository.GetUser(ctx, vc.a.DB, r.ID)
					if err == nil {
						g.name = helper.PrincipalName(vc.page(), uu)
					}
				}
			}
		}
		out[r.ID] = g
	}
	return out, nil
}

// linkToNewIssue は link_to_new_issue(version, project)。
func (vc *versionCtx) linkToNewIssue(v *versionModel, p *domain.Project) (template.HTML, error) {
	c := vc.c
	ctx := c.Ctx()
	if !v.IsOpen() {
		return "", nil
	}
	ok, err := c.Authz().AllowedTo(ctx, domain.Perm("add_issues"), p)
	if err != nil || !ok {
		return "", err
	}
	allowed, err := c.Authz().AllowedTargetTrackerIDs(ctx, p, 0)
	if err != nil {
		return "", err
	}
	env := issues.NewEnv(vc.a.DB, vc.a.Settings, c.User)
	trackers, err := env.ProjectTrackers(ctx, p)
	if err != nil {
		return "", err
	}
	var trackerID int64
	for _, t := range trackers {
		if !slices.Contains(allowed, t.ID) {
			continue
		}
		iss, err := env.New(ctx, p, t.ID, c.User)
		if err != nil {
			return "", err
		}
		if err := env.SetTracker(ctx, iss, t); err != nil {
			return "", err
		}
		safe, err := env.SafeAttribute(ctx, iss, "fixed_version_id", c.User)
		if err != nil {
			return "", err
		}
		if safe {
			trackerID = t.ID
			break
		}
	}
	if trackerID == 0 {
		return "", nil
	}
	u := "/projects/" + p.Identifier + "/issues/new?" + railsToQuery(url.Values{
		"back_url":                {"/versions/" + strconv.FormatInt(v.ID, 10)},
		"issue[fixed_version_id]": {strconv.FormatInt(v.ID, 10)},
		"issue[tracker_id]":       {strconv.FormatInt(trackerID, 10)},
	})
	return rails.LinkTo(vc.a.Helpers.SpriteIconHTML(vc.page(), "add", c.L("label_issue_new")), u, rails.NewHash("class", "icon icon-add")), nil
}

// wikiContent は render(:partial => "wiki/content", :locals => {:content => version.wiki_page.content})
// （version.wiki_page が無ければ空）。
func (vc *versionCtx) wikiContent(v *versionModel) (template.HTML, error) {
	t := v.WikiPageTitleString()
	if t == "" {
		return "", nil
	}
	ctx := vc.c.Ctx()
	w, err := repository.FindWikiByProject(ctx, vc.a.DB, v.ProjectID)
	if errors.Is(err, repository.ErrNotFound) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	page, err := repository.FindWikiPageByTitle(ctx, vc.a.DB, w.ID, wikiTitleize(t))
	if err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			return "", err
		}
		if page, err = repository.FindWikiRedirect(ctx, vc.a.DB, w.ID, wikiTitleize(t)); err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return "", nil
			}
			return "", err
		}
	}
	text, ok, err := repository.WikiPageText(ctx, vc.a.DB, page.ID)
	if err != nil || !ok {
		return "", err
	}
	obj := versionWikiObject{&redmine.Object{Kind: "wiki_content", ID: page.ID, Project: v.Project, Page: page}}
	body := vc.a.Helpers.VersionTextilizable(vc.page(), text, obj)
	return template.HTML("<div class=\"wiki wiki-page\">\n  ") + body + "\n</div>\n", nil
}

// versionWikiObject は textilizable の :object（WikiContent）。
type versionWikiObject struct{ o *redmine.Object }

// TextObject は helper.TextObject。
func (w versionWikiObject) TextObject() *redmine.Object { return w.o }
