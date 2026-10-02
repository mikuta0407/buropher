package handler

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルはマイページのチケットブロックが描画する issues/_list（IssuesHelper#grouped_issue_list、
// QueriesHelper#column_header / column_content / query_columns_hidden_tags /
// render_query_columns_selection）の移植。
// TODO(issues): チケット一覧（issues#index）の issues/_list 実装ができたら共通化する。
// 未対応の列: relations / attachments（空欄）、カスタムフィールドは値の文字列のみ（書式の formatted_value は未適用）。

// myIssueList は issues/_list に渡す query と issues（query_options の sort_param / sort_link_options を含む）。
type myIssueList struct {
	c   *Req
	a   *App
	q   *query.Query
	env *query.Env
	r   *redmine.Renderer
	// SortParam は query_options[:sort_param]（"settings[<block>][sort]"）。
	SortParam string
	// ColumnsTagName は render_query_columns_selection の :name（"settings[<block>][columns]"）。
	ColumnsTagName string

	Issues []*query.IssueRow
	Count  int64

	refs       map[int64]*repository.RefIssue
	projects   map[int64]*domain.Project
	users      map[int64]*domain.User
	groups     map[int64]*domain.Group
	trackers   map[int64]string
	statuses   map[int64]string
	priorities map[int64]string
	categories map[int64]string
	versions   map[int64]*repository.MyPageVersion
	cols       []*query.Column
	blockCols  []*query.Column
}

// newMyIssueList は query.issues(:limit => limit) を読み込み、列の表示に必要な関連を preload する。
func (a *App) newMyIssueList(c *Req, q *query.Query, env *query.Env, block string, limit int) (*myIssueList, error) {
	ctx := c.Ctx()
	l := &myIssueList{c: c, a: a, q: q, env: env, r: a.Helpers.WikiRenderer(c.Page()),
		SortParam: "settings[" + block + "][sort]", ColumnsTagName: "settings[" + block + "][columns]"}
	var err error
	if l.Count, err = q.IssueCount(ctx); err != nil {
		return nil, err
	}
	if l.Issues, err = q.Issues(ctx, query.ListOptions{Limit: limit}); err != nil {
		return nil, err
	}
	if l.cols, err = q.InlineColumns(ctx); err != nil {
		return nil, err
	}
	if l.blockCols, err = q.BlockColumns(ctx); err != nil {
		return nil, err
	}
	return l, l.preload(ctx)
}

func (l *myIssueList) preload(ctx context.Context) error {
	db := l.a.DB
	var ids, pids, uids, tids, sids, prids, cids, vids []int64
	for _, is := range l.Issues {
		ids = append(ids, is.ID)
		pids = append(pids, is.ProjectID)
		uids = append(uids, is.AuthorID)
		if is.AssignedToID != nil {
			uids = append(uids, *is.AssignedToID)
		}
		if is.LastUpdatedByID != nil {
			uids = append(uids, *is.LastUpdatedByID)
		}
		uids = append(uids, is.WatcherIDs...)
		if is.ParentID != nil {
			ids = append(ids, *is.ParentID)
		}
		tids = append(tids, is.TrackerID)
		sids = append(sids, is.StatusID)
		prids = append(prids, is.PriorityID)
		if is.CategoryID != nil {
			cids = append(cids, *is.CategoryID)
		}
		if is.FixedVersionID != nil {
			vids = append(vids, *is.FixedVersionID)
		}
	}
	var err error
	if l.refs, err = repository.RefIssuesByIDs(ctx, db, ids); err != nil {
		return err
	}
	if l.projects, err = repository.ProjectsByIDs(ctx, db, pids); err != nil {
		return err
	}
	if l.users, err = repository.UsersByIDs(ctx, db, uids); err != nil {
		return err
	}
	l.groups = map[int64]*domain.Group{}
	for _, id := range uids {
		if _, ok := l.users[id]; ok {
			continue
		}
		if g, err := repository.GetGroup(ctx, db, id); err == nil {
			l.groups[id] = g
		}
	}
	if l.trackers, err = repository.MyPageNamesByIDs(ctx, db, "trackers", tids); err != nil {
		return err
	}
	if l.statuses, err = repository.MyPageNamesByIDs(ctx, db, "issue_statuses", sids); err != nil {
		return err
	}
	if l.priorities, err = repository.MyPageNamesByIDs(ctx, db, "issue_priorities", prids); err != nil {
		return err
	}
	if l.categories, err = repository.MyPageNamesByIDs(ctx, db, "issue_categories", cids); err != nil {
		return err
	}
	if l.versions, err = repository.MyPageVersionsByIDs(ctx, db, vids); err != nil {
		return err
	}
	return nil
}

// Name は query.name。
func (l *myIssueList) Name() string { return l.q.Name }

// Project は query.project。
func (l *myIssueList) Project() *domain.Project { return l.q.Project }

// IssuesPath は _project_issues_path(query.project, query.as_params)（format が空でなければ .atom 等と key を付ける）。
func (l *myIssueList) IssuesPath(format, key string) string {
	path := "/issues"
	if p := l.q.Project; p != nil {
		path = "/projects/" + p.Identifier + "/issues"
	}
	if format != "" {
		path += "." + format
	}
	v := l.q.AsParams()
	if key != "" {
		v.Set("key", key)
	}
	return helper.URLWithQuery(path, v)
}

// CSSClasses は query.css_classes。
func (l *myIssueList) CSSClasses() string { return l.q.CSSClasses() }

// InlineColumns は query.inline_columns。
func (l *myIssueList) InlineColumns() []*query.Column { return l.cols }

// Colspan は query.inline_columns.size + 2。
func (l *myIssueList) Colspan() int { return len(l.cols) + 2 }

// ColumnsHiddenTags は query_columns_hidden_tags(query)。
func (l *myIssueList) ColumnsHiddenTags() rails.HTML {
	cols, _ := l.q.Columns(l.c.Ctx())
	var s rails.HTML
	for _, col := range cols {
		s += rails.HiddenFieldTag("c[]", col.Name, rails.NewHash("id", nil))
	}
	return s
}

var reColumnsTagID = regexp.MustCompile(`[\[\]]+`)

// ColumnsTagID は queries/_columns の tag_id（tag_name.gsub(/[\[\]]+/, '_').sub(/_+$/, '')）。
func (l *myIssueList) ColumnsTagID() string {
	return strings.TrimRight(reColumnsTagID.ReplaceAllString(l.ColumnsTagName+"[]", "_"), "_")
}

// caption は column.caption。
func (l *myIssueList) caption(col *query.Column) string { return col.CaptionText(l.env) }

// ColumnHeader は column_header(query, column, query_options)。
func (l *myIssueList) ColumnHeader(col *query.Column) rails.HTML {
	caption := l.caption(col)
	var content any = caption
	if col.IsSortable() {
		sc := l.q.SortCriteria()
		var css any
		order := col.DefaultOrder
		icon := ""
		if sc.FirstKey() == col.Name {
			if sc.FirstAsc() {
				css, icon, order = "sort asc icon icon-sorted-desc", "angle-up", "desc"
			} else {
				css, icon, order = "sort desc icon icon-sorted-asc", "angle-down", "asc"
			}
		}
		qp := pageQueryParameters(l.c)
		qp.Set(l.SortParam, sc.Add(col.Name, order).ToParam())
		var label any = caption
		if icon != "" {
			label = l.a.Helpers.SpriteIcon(l.c.Page(), icon, caption, nil)
		}
		content = rails.LinkTo(label, helper.URLWithQuery("/my/page", qp),
			rails.NewHash("title", l.c.L("label_sort_by", "\""+caption+"\""), "class", css, "method", "post", "remote", true))
	}
	return rails.ContentTag("th", content, rails.NewHash("class", col.CSSClasses()))
}

// BackURL は url_for(:params => request.query_parameters)。
func (l *myIssueList) BackURL() string {
	return helper.URLWithQuery("/my/page", pageQueryParameters(l.c))
}

// AvailableColumnsOptions は query_available_inline_columns_options(query)。
func (l *myIssueList) AvailableColumnsOptions() rails.HTML {
	ctx := l.c.Ctx()
	avail, _ := l.q.AvailableInlineColumns(ctx)
	cols, _ := l.q.Columns(ctx)
	sel := map[string]bool{}
	for _, col := range cols {
		sel[col.Name] = true
	}
	var opts []any
	for _, col := range avail {
		if !sel[col.Name] && !col.Frozen {
			opts = append(opts, []any{l.caption(col), col.Name})
		}
	}
	return rails.OptionsForSelect(opts, nil)
}

// SelectedColumnsOptions は query_selected_inline_columns_options(query)。
func (l *myIssueList) SelectedColumnsOptions() rails.HTML {
	avail, _ := l.q.AvailableInlineColumns(l.c.Ctx())
	ok := map[string]bool{}
	for _, col := range avail {
		ok[col.Name] = true
	}
	var opts []any
	for _, col := range l.cols {
		if ok[col.Name] && !col.Frozen {
			opts = append(opts, []any{l.caption(col), col.Name})
		}
	}
	return rails.OptionsForSelect(opts, nil)
}

// myIssueRow は grouped_issue_list の 1 行。
type myIssueRow struct {
	Issue *query.IssueRow
	Level int
	// HasGroup は group_name が nil でない（グループの見出し行を出す）。
	HasGroup    bool
	GroupName   rails.HTML
	GroupCount  any
	GroupTotals rails.HTML
	CSS         string
	Cells       []rails.HTML
	BlockCells  []myBlockCell
}

// myBlockCell はブロック列（description / last_notes 等）の行。
type myBlockCell struct {
	CSS     string
	Caption string
	Text    rails.HTML
}

// ShowBlockCaption は query.block_columns.count > 1。
func (l *myIssueList) ShowBlockCaption() bool { return len(l.blockCols) > 1 }

// Rows は grouped_issue_list(issues, query) の各行。
func (l *myIssueList) Rows() ([]myIssueRow, error) {
	ctx := l.c.Ctx()
	grouped, err := l.q.Grouped(ctx)
	if err != nil {
		return nil, err
	}
	var gcol *query.Column
	var counts map[query.GroupKey]int64
	var gtotals []query.GroupTotal
	if grouped {
		if gcol, err = l.q.GroupByColumn(ctx); err != nil {
			return nil, err
		}
		if counts, err = l.q.ResultCountByGroup(ctx); err != nil {
			return nil, err
		}
		if gtotals, err = l.q.TotalsByGroup(ctx); err != nil {
			return nil, err
		}
	}
	var out []myIssueRow
	var ancestors []*query.IssueRow
	first := true
	var prev query.GroupKey
	for _, is := range l.Issues {
		for len(ancestors) > 0 && !isDescendantOf(is, ancestors[len(ancestors)-1]) {
			ancestors = ancestors[:len(ancestors)-1]
		}
		row := myIssueRow{Issue: is, Level: len(ancestors)}
		if grouped && gcol != nil {
			key := l.groupKey(gcol, is)
			if first || key != prev {
				row.HasGroup = true
				row.GroupName = l.groupName(gcol, is, key)
				if n, ok := counts[key]; ok {
					row.GroupCount = n
				}
				var parts []string
				for _, t := range gtotals {
					parts = append(parts, string(l.totalTag(t.Column, t.ByGroup[key])))
				}
				row.GroupTotals = rails.HTML(strings.Join(parts, " "))
			}
			prev, first = key, false
		}
		if ref := l.refs[is.ID]; ref != nil {
			row.CSS = l.r.IssueCSSClasses(ref)
			if ref.HasChildren {
				ancestors = append(ancestors, is)
			}
		}
		for _, col := range l.cols {
			row.Cells = append(row.Cells, rails.ContentTag("td", l.columnContent(col, is), rails.NewHash("class", col.CSSClasses())))
		}
		for _, col := range l.blockCols {
			if text := l.columnContent(col, is); rails.IsPresent(text) {
				row.BlockCells = append(row.BlockCells, myBlockCell{CSS: col.CSSClasses(), Caption: l.caption(col), Text: text})
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// isDescendantOf は issue.is_descendant_of?(other)。
func isDescendantOf(is, other *query.IssueRow) bool {
	return is.RootID == other.RootID && is.ID != other.ID && strings.HasPrefix(is.HierPath, other.HierPath)
}

// groupKey は query.group_by_column.group_value(issue) を GroupKey にする。
func (l *myIssueList) groupKey(col *query.Column, is *query.IssueRow) query.GroupKey {
	id := func(p *int64) query.GroupKey {
		if p == nil {
			return query.GroupKey{Null: true}
		}
		return query.GroupKey{Value: strconv.FormatInt(*p, 10)}
	}
	date := func(t *time.Time) query.GroupKey {
		if t == nil {
			return query.GroupKey{Null: true}
		}
		return query.GroupKey{Value: t.Format("2006-01-02")}
	}
	ts := func(t time.Time) query.GroupKey {
		return query.GroupKey{Value: l.userTime(t).Format("2006-01-02")}
	}
	switch col.Name {
	case "project":
		return id(&is.ProjectID)
	case "tracker":
		return id(&is.TrackerID)
	case "status":
		return id(&is.StatusID)
	case "priority":
		return id(&is.PriorityID)
	case "author":
		return id(&is.AuthorID)
	case "assigned_to":
		return id(is.AssignedToID)
	case "category":
		return id(is.CategoryID)
	case "fixed_version":
		return id(is.FixedVersionID)
	case "start_date":
		return date(is.StartDate)
	case "due_date":
		return date(is.DueDate)
	case "done_ratio":
		return query.GroupKey{Value: strconv.Itoa(is.DoneRatio)}
	case "is_private":
		return query.GroupKey{Value: strconv.FormatBool(is.IsPrivate)}
	case "created_on":
		return ts(is.CreatedAt)
	case "updated_on":
		return ts(is.UpdatedAt)
	case "closed_on":
		if is.ClosedAt == nil {
			return query.GroupKey{Null: true}
		}
		return ts(*is.ClosedAt)
	}
	if col.CustomField != nil {
		vals := is.CustomValues[col.CustomField.ID]
		if len(vals) == 0 || vals[0] == "" {
			return query.GroupKey{Null: true}
		}
		v := vals[0]
		cast := col.CustomField.CastValue(&v)
		if cast == nil {
			return query.GroupKey{Null: true}
		}
		return query.GroupKey{Value: fmt.Sprint(cast)}
	}
	return query.GroupKey{Null: true}
}

// groupName は grouped_query_results の group_name（空なら "(blank)"）。
func (l *myIssueList) groupName(col *query.Column, is *query.IssueRow, key query.GroupKey) rails.HTML {
	if key.Null || key.Value == "" {
		return rails.H("(" + l.c.L("label_blank_value") + ")")
	}
	if col.CustomField != nil {
		return rails.H(key.Value)
	}
	return l.columnContent(col, is)
}

// totalTag は total_tag(column, value)。
func (l *myIssueList) totalTag(col *query.Column, v float64) rails.HTML {
	label := rails.ContentTag("span", l.caption(col)+":", nil)
	var value string
	switch col.Name {
	case "hours", "spent_hours", "total_spent_hours", "estimated_hours", "total_estimated_hours", "estimated_remaining_hours":
		value = l.c.Loc.FormatHours(v)
	default:
		value = fmt.Sprintf("%.2f", v)
	}
	val := rails.ContentTag("span", value, rails.NewHash("class", "value"))
	return rails.ContentTag("span", label+" "+val, rails.NewHash("class", "total-for-"+strings.ReplaceAll(col.Name, "_", "-")))
}

func (l *myIssueList) userTime(t time.Time) time.Time {
	if l.c.Loc != nil && l.c.Loc.Location != nil {
		return t.In(l.c.Loc.Location)
	}
	return t
}

// linkToPrincipal は link_to_principal(principal)。
func (l *myIssueList) linkToPrincipal(id int64) rails.HTML {
	if u := l.users[id]; u != nil {
		return l.linkToUser(u)
	}
	if g := l.groups[id]; g != nil {
		return rails.LinkTo(g.Name, "/groups/"+itoa(g.ID), rails.NewHash("class", "group"))
	}
	return ""
}

// linkToUser は link_to_user(user)（相対パス）。
func (l *myIssueList) linkToUser(u *domain.User) rails.HTML {
	name := l.c.Page().UserName(u)
	if !(u.Active() || (l.c.User.IsAdmin() && u.Logged())) {
		return rails.H(name)
	}
	return rails.LinkTo(name, "/users/"+itoa(u.ID), rails.NewHash("class", u.CSSClasses()))
}

// linkToProjectPath は link_to_project(project)（アーカイブ済みなら名前のみ）。
func linkToProjectPath(p *domain.Project) rails.HTML {
	if p.Archived() {
		return rails.H(p.Name)
	}
	return rails.LinkTo(p.Name, "/projects/"+projectToParam(p), nil)
}

// projectToParam は Project#to_param（数字だけの識別子は id）。
func projectToParam(p *domain.Project) string {
	for _, r := range p.Identifier {
		if r < '0' || r > '9' {
			return p.Identifier
		}
	}
	return itoa(p.ID)
}

// formatHoursOrBlank は format_hours（nil は ""）。
func (l *myIssueList) formatHoursOrBlank(h *float64) string {
	if h == nil {
		return ""
	}
	return l.c.Loc.FormatHours(*h)
}

// columnContent は column_content(column, issue)。
func (l *myIssueList) columnContent(col *query.Column, is *query.IssueRow) rails.HTML {
	c := l.c
	issuePath := "/issues/" + itoa(is.ID)
	switch col.Name {
	case "id":
		return rails.LinkTo(is.ID, issuePath, nil)
	case "subject":
		return rails.LinkTo(is.Subject, issuePath, nil)
	case "project":
		if p := l.projects[is.ProjectID]; p != nil {
			return linkToProjectPath(p)
		}
	case "tracker":
		return rails.H(l.trackers[is.TrackerID])
	case "status":
		return rails.H(l.statuses[is.StatusID])
	case "priority":
		return rails.H(l.priorities[is.PriorityID])
	case "category":
		if is.CategoryID != nil {
			return rails.H(l.categories[*is.CategoryID])
		}
	case "author":
		return l.linkToPrincipal(is.AuthorID)
	case "assigned_to":
		if is.AssignedToID != nil {
			return l.linkToPrincipal(*is.AssignedToID)
		}
	case "last_updated_by":
		if is.LastUpdatedByID != nil {
			return l.linkToPrincipal(*is.LastUpdatedByID)
		}
	case "fixed_version":
		if is.FixedVersionID != nil {
			if v := l.versions[*is.FixedVersionID]; v != nil {
				p := l.projects[v.ProjectID]
				if p == nil {
					p = &domain.Project{ID: v.ProjectID, Name: v.ProjectName}
				}
				visible := v.Sharing == "system" || c.AllowedTo(domain.Perm("view_issues"), p)
				return l.r.LinkToVersion(&redmine.VersionRef{ID: v.ID, Name: v.Name, Project: p, EffectiveDate: v.EffectiveDate, Visible: visible})
			}
		}
	case "start_date":
		if is.StartDate != nil {
			return rails.H(c.Loc.FormatDate(*is.StartDate))
		}
	case "due_date":
		if is.DueDate != nil {
			return rails.H(c.Loc.FormatDate(*is.DueDate))
		}
	case "created_on":
		return rails.H(c.Loc.FormatTime(is.CreatedAt, true))
	case "updated_on":
		return rails.H(c.Loc.FormatTime(is.UpdatedAt, true))
	case "closed_on":
		if is.ClosedAt != nil {
			return rails.H(c.Loc.FormatTime(*is.ClosedAt, true))
		}
	case "estimated_hours":
		return rails.H(l.formatHoursOrBlank(is.EstimatedHours))
	case "estimated_remaining_hours":
		if is.EstimatedHours != nil {
			v := *is.EstimatedHours * float64(100-is.DoneRatio) / 100
			return rails.H(c.Loc.FormatHours(v))
		}
	case "spent_hours", "total_spent_hours":
		h := is.SpentHours
		prefix := ""
		if col.Name == "total_spent_hours" {
			h, prefix = is.TotalSpentHours, "~"
		}
		if h == nil {
			return ""
		}
		text := c.Loc.FormatHours(*h)
		p := l.projects[is.ProjectID]
		if p == nil {
			return rails.H(text)
		}
		return rails.LinkToIf(*h > 0, text, helper.URLWithQuery("/projects/"+p.Identifier+"/time_entries", rails.NewHash("issue_id", prefix+itoa(is.ID))), nil)
	case "done_ratio":
		return helper.ProgressBar(is.DoneRatio, "")
	case "is_private":
		if is.IsPrivate {
			return rails.H(c.L("general_text_Yes"))
		}
		return rails.H(c.L("general_text_No"))
	case "parent":
		if is.ParentID != nil {
			if ref := l.refs[*is.ParentID]; ref != nil && l.issueVisible(*is.ParentID) {
				return l.r.LinkToIssue(ref, redmine.LinkToIssueOptions{NoSubject: true})
			}
			return rails.H("#" + itoa(*is.ParentID))
		}
	case "parent.subject":
		if is.ParentID != nil {
			if ref := l.refs[*is.ParentID]; ref != nil {
				return rails.H(ref.Subject)
			}
		}
	case "watcher_users":
		var items []string
		for _, id := range is.WatcherIDs {
			items = append(items, string(rails.ContentTag("li", l.linkToPrincipal(id), nil)))
		}
		return rails.ContentTag("ul", rails.HTML(strings.Join(items, "")), nil)
	case "description":
		if strings.TrimSpace(is.Description) != "" {
			return rails.ContentTag("div", l.a.Helpers.WikiRenderer(c.Page()).Textilizable(is.Description,
				redmine.Options{Project: l.projects[is.ProjectID]}), rails.NewHash("class", "wiki"))
		}
	case "last_notes":
		if is.LastNotes != nil && strings.TrimSpace(*is.LastNotes) != "" {
			return rails.ContentTag("div", l.a.Helpers.WikiRenderer(c.Page()).Textilizable(*is.LastNotes,
				redmine.Options{Project: l.projects[is.ProjectID]}), rails.NewHash("class", "wiki"))
		}
	}
	if col.CustomField != nil {
		return rails.H(strings.Join(is.CustomValues[col.CustomField.ID], ", "))
	}
	return ""
}

// issueVisible は issue.visible?（親チケットの表示用）。
func (l *myIssueList) issueVisible(id int64) bool {
	cond, err := l.c.Authz().IssueVisibleCondition(l.c.Ctx(), authz.ConditionOptions{})
	if err != nil {
		return false
	}
	_, err = repository.FindVisibleIssue(l.c.Ctx(), l.a.DB, id, cond)
	return err == nil
}
