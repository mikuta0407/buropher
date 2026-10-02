package handler

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/activity"
	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/calendar"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルはマイページの各ブロック（MyHelper#render_*_block と my/blocks/_*.erb のデータ）の移植。

// myBlockView は render_block の 1 ブロック（Partial を Locals で描画し、mypage-box で囲む）。
type myBlockView struct {
	Block   string
	Partial string
	Locals  map[string]any
	// RemoveURL は url_for(:action => "remove_block", :block => block)。
	RemoveURL string
}

// queryEnv は User.current の query.Env。
func (a *App) queryEnv(c *Req) (*query.Env, error) {
	env, err := query.NewEnv(c.Ctx(), a.DB, c.User, a.Settings)
	if err != nil {
		return nil, err
	}
	env.L = c.Loc
	env.Now = a.now
	return env, nil
}

// settingStrings はブロック設定の配列値（文字列は 1 要素）。
func settingStrings(v any) []string {
	switch x := v.(type) {
	case []any:
		var out []string
		for _, e := range x {
			out = append(out, httpx.ValueString(e))
		}
		return out
	case []string:
		return x
	case nil:
		return nil
	default:
		return []string{httpx.ValueString(x)}
	}
}

// applySortSetting は query.sort_criteria = settings[:sort]（文字列なら "a:desc,b"、配列なら [[a, desc], ...]）。
func applySortSetting(q *query.Query, v any) {
	switch x := v.(type) {
	case string:
		q.SetSortParam(x)
	case []any:
		var sc query.SortCriteria
		for _, e := range x {
			if pair, ok := e.([]any); ok && len(pair) > 0 {
				k := httpx.ValueString(pair[0])
				o := ""
				if len(pair) > 1 {
					o = httpx.ValueString(pair[1])
				}
				sc = append(sc, [2]string{k, o})
			} else {
				sc = append(sc, [2]string{httpx.ValueString(e), ""})
			}
		}
		q.SetSortCriteria(sc)
	}
}

// myBlockContent は render_block_content(block, user)。未知のブロックは nil。
func (a *App) myBlockContent(c *Req, p *myPagePref, block string) (*myBlockView, error) {
	def := findMyBlock(block)
	if def == nil {
		a.logger().Warn("Unknown block found in preferences", "block", block, "user", c.User.Login, "id", c.User.ID)
		return nil, nil
	}
	settings := p.blockSettings(block)
	v := &myBlockView{Block: block, Locals: map[string]any{"block": block, "settings": settings},
		RemoveURL: helper.URLWithQuery("/my/remove_block", rails.NewHash("block", block))}
	var err error
	switch def.Name {
	case "issuesassignedtome", "issuesreportedbyme", "issuesupdatedbyme", "issueswatched":
		err = a.myIssuesBlock(c, v, def, settings)
	case "issuequery":
		err = a.myIssueQueryBlock(c, v, settings)
	case "news":
		err = a.myNewsBlock(c, v)
	case "calendar":
		err = a.myCalendarBlock(c, v)
	case "documents":
		err = a.myDocumentsBlock(c, v)
	case "timelog":
		err = a.myTimelogBlock(c, v, settings)
	case "activity":
		err = a.myActivityBlock(c, v)
	}
	if err != nil {
		return nil, err
	}
	return v, nil
}

// myIssuesBlock は render_issuesassignedtome_block 等（IssueQuery を組み立てて my/blocks/issues）。
func (a *App) myIssuesBlock(c *Req, v *myBlockView, def *myBlockDef, settings map[string]any) error {
	ctx := c.Ctx()
	env, err := a.queryEnv(c)
	if err != nil {
		return err
	}
	q, err := query.New(ctx, env, query.KindIssue, nil)
	if err != nil {
		return err
	}
	q.Name = c.L(def.Label)
	uid := c.User.ID
	q.UserID = &uid
	filter := map[string]string{
		"issuesassignedtome": "assigned_to_id", "issuesreportedbyme": "author_id",
		"issuesupdatedbyme": "updated_by", "issueswatched": "watcher_id",
	}[def.Name]
	if err := q.AddFilter(ctx, filter, "=", []string{"me"}); err != nil {
		return err
	}
	if err := q.AddFilter(ctx, "project.status", "=", []string{strconv.Itoa(domain.ProjectStatusActive)}); err != nil {
		return err
	}
	cols := settingStrings(settings["columns"])
	if len(cols) == 0 || strings.Join(cols, "") == "" {
		cols = []string{"project", "tracker", "status", "subject"}
	}
	if err := q.SetColumnNames(ctx, cols); err != nil {
		return err
	}
	if s := settings["sort"]; s != nil && rails.IsPresent(s) {
		applySortSetting(q, s)
	} else if def.Name == "issuesassignedtome" {
		q.SetSortCriteria(query.SortCriteria{{"priority", "desc"}, {"updated_on", "desc"}})
	} else {
		q.SetSortCriteria(query.SortCriteria{{"updated_on", "desc"}})
	}
	list, err := a.newMyIssueList(c, q, env, v.Block, 10)
	if err != nil {
		return err
	}
	v.Partial = "my/blocks/issues"
	v.Locals["query"] = list
	v.Locals["atom_key"] = c.AtomKey()
	return nil
}

// myIssueQueryBlock は render_issuequery_block（保存クエリ。未選択ならクエリの選択フォーム）。
func (a *App) myIssueQueryBlock(c *Req, v *myBlockView, settings map[string]any) error {
	ctx := c.Ctx()
	env, err := a.queryEnv(c)
	if err != nil {
		return err
	}
	var q *query.Query
	if id, err := strconv.ParseInt(strings.TrimSpace(httpx.ValueString(settings["query_id"])), 10, 64); err == nil && id > 0 {
		if q, err = query.Load(ctx, env, id, query.KindIssue); err != nil {
			return err
		}
		if q != nil {
			ok, err := q.VisibleTo(ctx, env.Auth)
			if err != nil {
				return err
			}
			if !ok {
				q = nil
			}
		}
	}
	if q != nil {
		if cols := settingStrings(settings["columns"]); len(cols) > 0 && strings.Join(cols, "") != "" {
			if err := q.SetColumnNames(ctx, cols); err != nil {
				return err
			}
		}
		if s := settings["sort"]; s != nil && rails.IsPresent(s) {
			applySortSetting(q, s)
		}
		list, err := a.newMyIssueList(c, q, env, v.Block, 10)
		if err != nil {
			return err
		}
		v.Partial = "my/blocks/issues"
		v.Locals["query"] = list
		v.Locals["atom_key"] = c.AtomKey()
		return nil
	}
	queries, err := query.ListVisible(ctx, c.Authz(), query.KindIssue, nil, false)
	if err != nil {
		return err
	}
	var opts []any
	for _, sq := range queries {
		opts = append(opts, []any{sq.Name, sq.ID})
	}
	v.Partial = "my/blocks/issue_query_selection"
	v.Locals["query_options"] = rails.OptionsForSelect(opts, httpx.ValueString(settings["query_id"]))
	return nil
}

// myNewsBlock は render_news_block。
func (a *App) myNewsBlock(c *Req, v *myBlockView) error {
	cond, err := c.Authz().AllowedToCondition(c.Ctx(), "view_news", authz.ConditionOptions{}, nil)
	if err != nil {
		return err
	}
	news, err := repository.MyPageNews(c.Ctx(), a.DB, cond, c.User.ID, 10)
	if err != nil {
		return err
	}
	v.Partial = "my/blocks/news"
	v.Locals["news"] = news
	return nil
}

// myDocumentItem は documents/_document の 1 件。
type myDocumentItem struct {
	*repository.MyPageDocument
	Project *domain.Project
	// Description は truncate_lines(document.description)。
	Description string
}

// truncateLines は ApplicationHelper#truncate_lines(string, :length => 80)。
func truncateLines(s string) string {
	// Ruby の /\A(.{80}.*?)$/m（m は改行にも . を一致させる。$ は行末）
	rs := []rune(s)
	if len(rs) <= 80 {
		return s
	}
	rest := string(rs[80:])
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		return string(rs[:80]) + rest[:i] + "..."
	}
	return s + "..."
}

// myDocumentsBlock は render_documents_block。
func (a *App) myDocumentsBlock(c *Req, v *myBlockView) error {
	cond, err := c.Authz().AllowedToCondition(c.Ctx(), "view_documents", authz.ConditionOptions{}, nil)
	if err != nil {
		return err
	}
	docs, err := repository.MyPageDocuments(c.Ctx(), a.DB, cond, 10)
	if err != nil {
		return err
	}
	var pids []int64
	for _, d := range docs {
		pids = append(pids, d.ProjectID)
	}
	projects, err := repository.ProjectsByIDs(c.Ctx(), a.DB, pids)
	if err != nil {
		return err
	}
	items := make([]*myDocumentItem, len(docs))
	for i, d := range docs {
		items[i] = &myDocumentItem{MyPageDocument: d, Project: projects[d.ProjectID], Description: truncateLines(d.Description)}
	}
	v.Partial = "my/blocks/documents"
	v.Locals["documents"] = items
	return nil
}

// myTimelogDay は作業時間ブロックの 1 日分。
type myTimelogDay struct {
	Label   string
	Hours   rails.HTML
	Entries []myTimelogEntry
}

// myTimelogEntry は作業時間ブロックの 1 行。
type myTimelogEntry struct {
	*repository.MyPageTimeEntry
	IssueLink rails.HTML
	HoursHTML rails.HTML
}

var reHTMLHours = regexp.MustCompile(`(\d+)([\.:])(\d+)`)

// htmlHours は ApplicationHelper#html_hours(text)。
func htmlHours(text string) rails.HTML {
	return rails.HTML(reHTMLHours.ReplaceAllString(string(rails.H(text)),
		`<span class="hours hours-int">$1</span><span class="hours hours-dec">$2$3</span>`))
}

// myTimelogBlock は render_timelog_block（settings[:days] 日分の自分の作業時間）。
func (a *App) myTimelogBlock(c *Req, v *myBlockView, settings map[string]any) error {
	days := int(httpx.RubyToI(httpx.ValueString(settings["days"])))
	if days < 1 || days > 365 {
		days = 7
	}
	today := a.userToday(c)
	entries, err := repository.MyPageTimeEntries(c.Ctx(), a.DB, c.User.ID, today.AddDate(0, 0, -(days-1)), today)
	if err != nil {
		return err
	}
	var issueIDs []int64
	total := 0.0
	for _, e := range entries {
		total += e.Hours
		if e.IssueID != nil {
			issueIDs = append(issueIDs, *e.IssueID)
		}
	}
	refs, err := repository.RefIssuesByIDs(c.Ctx(), a.DB, issueIDs)
	if err != nil {
		return err
	}
	r := a.Helpers.WikiRenderer(c.Page())
	byDay := map[time.Time]*myTimelogDay{}
	var dayKeys []time.Time
	sums := map[time.Time]float64{}
	for _, e := range entries {
		d := e.SpentOn
		day := byDay[d]
		if day == nil {
			label := c.Loc.FormatDate(d)
			if d.Equal(today) {
				label = redmine.Titleize(c.L("label_today"))
			}
			day = &myTimelogDay{Label: label}
			byDay[d] = day
			dayKeys = append(dayKeys, d)
		}
		sums[d] += e.Hours
		te := myTimelogEntry{MyPageTimeEntry: e, HoursHTML: htmlHours(c.Loc.FormatHours(e.Hours))}
		if e.IssueID != nil {
			if ref := refs[*e.IssueID]; ref != nil {
				te.IssueLink = rails.H(" - ") + r.LinkToIssue(ref, redmine.LinkToIssueOptions{Truncate: 50})
			}
		}
		day.Entries = append(day.Entries, te)
	}
	// entries_by_day.keys.sort.reverse_each
	slices.SortFunc(dayKeys, func(x, y time.Time) int { return y.Compare(x) })
	var daysOut []*myTimelogDay
	for _, d := range dayKeys {
		byDay[d].Hours = htmlHours(c.Loc.FormatHours(sums[d]))
		daysOut = append(daysOut, byDay[d])
	}
	v.Partial = "my/blocks/timelog"
	v.Locals["days"] = days
	v.Locals["total"] = c.Loc.LHoursShort(total)
	v.Locals["entries_by_day"] = daysOut
	v.Locals["can_log_time"] = c.AllowedToGlobally(domain.Perm("log_time"))
	return nil
}

// myActivityBlock は render_activity_block（自分の最近の活動 10 件）。
func (a *App) myActivityBlock(c *Req, v *myBlockView) error {
	f, err := activity.NewFetcher(c.Ctx(), a.DB, c.Authz(), c.Loc, activity.Options{Author: c.User})
	if err != nil {
		return err
	}
	events, err := f.Events(c.Ctx(), nil, nil, 10)
	if err != nil {
		return err
	}
	days := helper.GroupActivityEvents(events, c.Loc.Location)
	params := rails.NewHash("user_id", c.User.ID)
	if len(days) > 0 {
		params.Set("from", days[0].Day.Format("2006-01-02"))
	}
	v.Partial = "my/blocks/activity"
	v.Locals["events_by_day"] = days
	v.Locals["activity_url"] = helper.URLWithQuery("/activity", params)
	return nil
}

// myCalendarBlock は render_calendar_block（今週の、自分のプロジェクトの可視なチケットの開始日・期日）。
func (a *App) myCalendarBlock(c *Req, v *myBlockView) error {
	cal := a.newCalendar(c, a.userToday(c), calendar.Week)
	cond, err := c.Authz().IssueVisibleCondition(c.Ctx(), authz.ConditionOptions{})
	if err != nil {
		return err
	}
	ids, err := repository.MyPageCalendarIssueIDs(c.Ctx(), a.DB, cond, c.User.ID, cal.Startdt, cal.Enddt)
	if err != nil {
		return err
	}
	events, err := a.calendarEvents(c, ids, nil)
	if err != nil {
		return err
	}
	cal.SetEvents(events)
	v.Partial = "my/blocks/calendar"
	v.Locals["calendar"] = cal
	return nil
}
