// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

// issues/_list（チケット一覧の表）と QueriesHelper#column_content / csv_content / grouped_query_results /
// IssuesHelper#grouped_issue_list の移植。

import (
	"context"
	"html/template"
	"math"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// issueListView は issues/_list の locals（issues, query）。
type issueListView struct {
	l   *issueLookup
	qv  *queryView
	ctx context.Context

	Inline  []*query.Column
	Block   []*query.Column
	Rows    []*issueListRow
	BackURL string
	// Grouped は query.grouped?。
	Grouped bool
}

// issueListRow は grouped_issue_list の 1 行。
type issueListRow struct {
	Issue *query.IssueRow
	Level int
	// GroupName は group_name（nil ならグループ見出しなし）。
	GroupName   any
	GroupCount  any
	GroupTotals template.HTML
	CSS         string
	Cells       []issueCell
	Blocks      []issueBlock
}

type issueCell struct {
	Content template.HTML
	CSS     string
}

type issueBlock struct {
	Caption string
	Content template.HTML
	CSS     string
}

// QV は @query の queryView。
func (v *issueListView) QV() *queryView { return v.qv }

// ColSpan は query.inline_columns.size + 2。
func (v *issueListView) ColSpan() int { return len(v.Inline) + 2 }

// MultipleBlocks は query.block_columns.count > 1。
func (v *issueListView) MultipleBlocks() bool { return len(v.Block) > 1 }

// ColumnsHiddenTags は query_columns_hidden_tags(query)。
func (v *issueListView) ColumnsHiddenTags() (template.HTML, error) {
	cols, err := v.qv.Q.Columns(v.ctx)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, c := range cols {
		b.WriteString(string(rails.HiddenFieldTag("c[]", c.Name, rails.NewHash("id", nil))))
	}
	return template.HTML(b.String()), nil
}

// newIssueListView は一覧の行を組み立てる。
func (a *App) newIssueListView(l *issueLookup, qv *queryView, rows []*query.IssueRow) (*issueListView, error) {
	ctx := l.ctx
	q := qv.Q
	v := &issueListView{l: l, qv: qv, ctx: ctx}
	var err error
	if v.Inline, err = q.InlineColumns(ctx); err != nil {
		return nil, err
	}
	if v.Block, err = q.BlockColumns(ctx); err != nil {
		return nil, err
	}
	v.BackURL = requestQueryParamsURL(l.c)
	l.addIssues(rows)
	l.markVisible(rows)
	ids := make([]int64, len(rows))
	var pids []int64
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
	}
	l.preloadChildren(ids)
	l.preloadPrincipals(pids)

	// grouped_query_results
	gcol, err := q.GroupByColumn(ctx)
	if err != nil {
		return nil, err
	}
	v.Grouped = gcol != nil
	var counts map[query.GroupKey]int64
	var totals []query.GroupTotal
	if gcol != nil {
		if counts, err = q.ResultCountByGroup(ctx); err != nil {
			return nil, err
		}
		if totals, err = q.TotalsByGroup(ctx); err != nil {
			return nil, err
		}
	}
	var ancestors []*query.IssueRow
	var prev query.GroupKey
	first := true
	for _, r := range rows {
		row := &issueListRow{Issue: r}
		if gcol != nil {
			key, val := l.groupValue(gcol, r)
			if first || key != prev {
				if key.Null || rails.IsBlank(val) && val != false {
					row.GroupName = "(" + l.L("label_blank_value") + ")"
				} else {
					row.GroupName = l.formatObject(val, true, gcol.CustomField)
				}
				if counts != nil {
					if n, ok := counts[key]; ok {
						row.GroupCount = n
					}
				}
				var parts []string
				for _, t := range totals {
					parts = append(parts, string(qv.issueTotalTag(t.Column, t.ByGroup[key])))
				}
				row.GroupTotals = template.HTML(strings.Join(parts, " "))
			}
			prev, first = key, false
		}
		// grouped_issue_list: 階層の深さ
		for len(ancestors) > 0 && !isDescendantOf(r, ancestors[len(ancestors)-1]) {
			ancestors = ancestors[:len(ancestors)-1]
		}
		row.Level = len(ancestors)
		if l.hasChildren(r.ID) {
			ancestors = append(ancestors, r)
		}
		row.CSS = l.cssClasses(r)
		for _, c := range v.Inline {
			row.Cells = append(row.Cells, issueCell{Content: l.columnContent(c, r), CSS: c.CSSClasses()})
		}
		for _, c := range v.Block {
			content := l.columnContent(c, r)
			if rails.IsPresent(string(content)) {
				row.Blocks = append(row.Blocks, issueBlock{Caption: c.CaptionText(q.Env()), Content: content, CSS: c.CSSClasses()})
			}
		}
		v.Rows = append(v.Rows, row)
	}
	return v, l.err
}

// RowClass は tr の class（cycle の後ろの部分）。
func (r *issueListRow) LevelClass() any {
	if r.Level > 0 {
		return "idnt idnt-" + strconv.Itoa(r.Level)
	}
	return nil
}

// isDescendantOf は issue.is_descendant_of?(other)。
func isDescendantOf(r, other *query.IssueRow) bool {
	return r.RootID == other.RootID && r.ID != other.ID && strings.HasPrefix(r.HierPath, other.HierPath)
}

// FormatURL は OtherFormatsBuilder#link_to_with_query_parameters の URL
// （request.query_parameters から page / format を除き、format を付ける。key は Atom の :key）。
func (v *issueListView) FormatURL(format, key string) string {
	qp := v.l.c.Page().QueryParameters()
	qp.Delete("page")
	qp.Delete("format")
	if key != "" {
		qp.Set("key", key)
	}
	u := v.l.c.R.URL.Path
	u = strings.TrimSuffix(u, "."+formatOf(v.l.c)) + "." + format
	if qs := helper.ToQuery(qp); qs != "" {
		u += "?" + qs
	}
	return u
}

// requestQueryParamsURL は url_for(:params => request.query_parameters)。
func requestQueryParamsURL(c *Req) string {
	u := c.R.URL.Path
	if qs := helper.ToQuery(c.Page().QueryParameters()); qs != "" {
		u += "?" + qs
	}
	return u
}

// ---------------------------------------------------------------- グループ

// groupValue は query.group_by_column.group_value(issue) とその GroupKey（result_count_by_group のキー）。
func (l *issueLookup) groupValue(c *query.Column, r *query.IssueRow) (query.GroupKey, any) {
	idKey := func(id *int64) query.GroupKey {
		if id == nil {
			return query.GroupKey{Null: true}
		}
		return query.GroupKey{Value: strconv.FormatInt(*id, 10)}
	}
	if c.CustomField != nil {
		if r.CustomValues == nil {
			// 列に CF が無いと preload されないので読み込む（Redmine は issue.custom_values を遅延読み込みする）
			cv, err := repository.CustomValues(l.ctx, l.a.DB, "issue", r.ID)
			l.fail(err)
			r.CustomValues = cv
		}
		vals := r.CustomValues[c.CustomField.ID]
		if !l.cfVisibleBy(c.CustomField, l.project(r.ProjectID)) || len(vals) == 0 {
			return query.GroupKey{Null: true}, nil
		}
		if len(vals) > 1 {
			var out []any
			for _, s := range sortedStrings(vals) {
				out = append(out, l.castCF(c.CustomField, s))
			}
			return query.GroupKey{Value: "multi"}, out
		}
		s := vals[0]
		cast := c.CustomField.CastValue(&s)
		if cast == nil {
			return query.GroupKey{Null: true}, nil
		}
		return query.GroupKey{Value: groupKeyString(cast)}, l.castCF(c.CustomField, s)
	}
	switch c.Name {
	case "project":
		id := r.ProjectID
		return idKey(&id), l.project(r.ProjectID)
	case "tracker":
		id := r.TrackerID
		return idKey(&id), l.tracker(r.TrackerID)
	case "status":
		id := r.StatusID
		return idKey(&id), l.status(r.StatusID)
	case "priority":
		id := r.PriorityID
		return idKey(&id), l.priority(r.PriorityID)
	case "author":
		id := r.AuthorID
		return idKey(&id), l.principal(r.AuthorID)
	case "assigned_to":
		if r.AssignedToID == nil {
			return idKey(nil), nil
		}
		return idKey(r.AssignedToID), l.principalPtr(r.AssignedToID)
	case "category":
		if c := l.category(r.CategoryID); c != nil {
			return idKey(r.CategoryID), c
		}
		return idKey(nil), nil
	case "fixed_version":
		if v := l.version(r.FixedVersionID); v != nil {
			return idKey(r.FixedVersionID), v
		}
		return idKey(nil), nil
	case "start_date", "due_date":
		d := r.StartDate
		if c.Name == "due_date" {
			d = r.DueDate
		}
		if d == nil {
			return idKey(nil), nil
		}
		return query.GroupKey{Value: d.Format("2006-01-02")}, dateValue{*d}
	case "done_ratio":
		return query.GroupKey{Value: strconv.Itoa(r.DoneRatio)}, int64(r.DoneRatio)
	case "is_private":
		return query.GroupKey{Value: strconv.FormatBool(r.IsPrivate)}, r.IsPrivate
	case "created_on", "updated_on", "closed_on":
		var t *time.Time
		switch c.Name {
		case "created_on":
			t = &r.CreatedAt
		case "updated_on":
			t = &r.UpdatedAt
		default:
			t = r.ClosedAt
		}
		if t == nil {
			return idKey(nil), nil
		}
		d := l.userDate(*t)
		return query.GroupKey{Value: d.Format("2006-01-02")}, dateValue{d}
	}
	return query.GroupKey{Null: true}, nil
}

// userDate は User.current.time_to_date(time)。
func (l *issueLookup) userDate(t time.Time) time.Time {
	if l.c.Loc != nil && l.c.Loc.Location != nil {
		t = t.In(l.c.Loc.Location)
	}
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// dateValue は Date（format_object で format_date する値）。
type dateValue struct{ t time.Time }

func groupKeyString(v any) string {
	switch x := v.(type) {
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e16 {
			return strconv.FormatFloat(x, 'f', 1, 64)
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case time.Time:
		return x.Format("2006-01-02")
	case string:
		return x
	}
	return rails.ToS(v)
}

func sortedStrings(ss []string) []string {
	out := slices.Clone(ss)
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------- format_object

// formatObject は ApplicationHelper#format_object(object, html)。html なら template.HTML、そうでなければ string を返す。
func (l *issueLookup) formatObject(v any, html bool, cf *customfield.CustomField) any {
	thousands := cf != nil && cf.ThousandsDelimiter()
	out := func(s string) any {
		if html {
			return rails.H(s)
		}
		return s
	}
	switch x := v.(type) {
	case nil:
		return out("")
	case template.HTML:
		return x
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			r := l.formatObject(e, html, cf)
			if html {
				parts = append(parts, string(rails.H(r)))
			} else {
				parts = append(parts, rails.ToS(r))
			}
		}
		if html {
			return template.HTML(strings.Join(parts, ", "))
		}
		return strings.Join(parts, ", ")
	case time.Time:
		return out(l.formatTime(x))
	case dateValue:
		return out(l.formatDate(x.t))
	case int64:
		return out(l.numberWithDelimiter(strconv.FormatInt(x, 10), thousands))
	case int:
		return out(l.numberWithDelimiter(strconv.Itoa(x), thousands))
	case float64:
		return out(l.numberWithDelimiter(strconv.FormatFloat(x, 'f', 2, 64), thousands))
	case bool:
		return out(l.yesNo(x))
	case *domain.User:
		if html {
			return l.linkToPrincipal(x)
		}
		return l.principalName(x)
	case *domain.Project:
		if html {
			return helper.LinkToProject(x)
		}
		return x.Name
	case *repository.Version:
		if html {
			return l.linkToVersion(x)
		}
		return x.Name
	case *domain.Tracker:
		return out(x.Name)
	case *domain.IssueStatus:
		return out(x.Name)
	case *domain.Enumeration:
		return out(x.Name)
	case *repository.IssueCategory:
		return out(x.Name)
	case *query.IssueRow:
		if html && l.issueVisible(x) {
			return l.linkToIssue(x, redmine.LinkToIssueOptions{})
		}
		return out("#" + strconv.FormatInt(x.ID, 10))
	case cfRecord:
		switch x.format {
		case "user":
			if u := l.principal(x.id); u != nil {
				return l.formatObject(u, html, nil)
			}
		case "version":
			if ver := l.version(&x.id); ver != nil {
				return l.formatObject(ver, html, nil)
			}
		}
		return out(x.label)
	case customfield.Option:
		return out(x.Label)
	case string:
		return out(x)
	}
	return out(rails.ToS(v))
}

// numberWithDelimiter は number_with_delimiter(s, delimiter: thousands ? t('number.format.delimiter') : nil)。
func (l *issueLookup) numberWithDelimiter(s string, thousands bool) string {
	if !thousands {
		return s
	}
	return l.c.Loc.NumberWithDelimiter(s, i18n.NumberOptions{})
}

// cfRecord は RecordList 書式のキャスト結果（User / Version / CustomFieldEnumeration）。
type cfRecord struct {
	format string
	id     int64
	label  string
}

// castCF は custom_field.cast_value(value)（レコードは cfRecord）。
func (l *issueLookup) castCF(cf *customfield.CustomField, s string) any {
	switch cf.FieldFormat {
	case "user", "version", "enumeration":
		id := customfield.RubyToI(s)
		if s == "" {
			return nil
		}
		rec := cfRecord{format: cf.FieldFormat, id: id}
		switch cf.FieldFormat {
		case "user":
			u := l.principal(id)
			if u == nil {
				return nil
			}
			rec.label = l.principalName(u)
		case "version":
			v := l.version(&id)
			if v == nil {
				return nil
			}
			rec.label = v.Name
		case "enumeration":
			es, err := customfield.Enumerations(l.ctx, l.a.DB, cf.ID, false)
			l.fail(err)
			found := false
			for _, e := range es {
				if e.ID == id {
					rec.label, found = e.Name, true
				}
			}
			if !found {
				return nil
			}
		}
		return rec
	case "date":
		v := cf.CastValue(&s)
		if t, ok := v.(time.Time); ok {
			return dateValue{t}
		}
		return v
	}
	return cf.CastValue(&s)
}

// cfEnv はカスタムフィールドの書式の環境（cf の値の表示に使う）。
func (l *issueLookup) cfEnv(cf *customfield.CustomField) *customfield.Env {
	env := l.a.cfEnv(l.c)
	env.RecordOptions = func(format string, ids []string) []customfield.Option {
		var out []customfield.Option
		for _, s := range ids {
			if v, ok := l.castCF(&customfield.CustomField{ID: cf.ID, FieldFormat: format}, s).(cfRecord); ok {
				out = append(out, customfield.Option{Label: v.label, Value: strconv.FormatInt(v.id, 10)})
			}
		}
		return out
	}
	env.FormatObject = func(v any, html bool) any {
		if o, ok := v.(customfield.Option); ok && (cf.FieldFormat == "user" || cf.FieldFormat == "version") {
			return l.formatObject(cfRecord{format: cf.FieldFormat, id: customfield.RubyToI(o.Value), label: o.Label}, html, nil)
		}
		return l.formatObject(v, html, cf)
	}
	env.Textilizable = func(text string, customized *customfield.Customized) template.HTML {
		var obj *redmine.Object
		if customized != nil && customized.Kind == "issue" {
			obj = &redmine.Object{Kind: "issue", ID: customized.ID, Project: l.project(customized.ProjectID)}
		}
		return l.renderer().Textilizable(text, redmine.Options{Object: obj})
	}
	env.ProgressBar = func(pct int, legend string) template.HTML { return helper.ProgressBar(pct, legend) }
	return env
}

// formatCustomValue は format_object(custom_value, html)（値は 1 件）。
func (l *issueLookup) formatCustomValue(cf *customfield.CustomField, value any, customized *customfield.Customized, html bool) any {
	env := l.cfEnv(cf)
	f := cf.Format().FormattedValue(env, cf, value, customized, html)
	switch x := f.(type) {
	case nil:
		if html {
			return template.HTML("")
		}
		return ""
	case string:
		if html {
			return rails.H(x)
		}
		return x
	case template.HTML:
		return x
	case customfield.Option:
		if cf.FieldFormat == "user" || cf.FieldFormat == "version" {
			return l.formatObject(cfRecord{format: cf.FieldFormat, id: customfield.RubyToI(x.Value), label: x.Label}, html, nil)
		}
		return l.formatObject(x.Label, html, cf)
	case time.Time:
		if cf.FieldFormat == "date" {
			return l.formatObject(dateValue{x}, html, cf)
		}
	}
	return l.formatObject(f, html, cf)
}

func (l *issueLookup) customized(r *query.IssueRow) *customfield.Customized {
	c := &customfield.Customized{Kind: "issue", ID: r.ID, ProjectID: r.ProjectID}
	if p := l.project(r.ProjectID); p != nil {
		c.ProjectIdentifier = p.Identifier
	}
	return c
}

// ---------------------------------------------------------------- column_content

// columnContent は column_content(column, issue)。
func (l *issueLookup) columnContent(c *query.Column, r *query.IssueRow) template.HTML {
	switch c.Kind {
	case query.ColumnCustomField:
		return l.cfColumnContent(c.CustomField, r, r.CustomValues[c.CustomField.ID], r.ProjectID, l.customized(r), true).(template.HTML)
	case query.ColumnAssocCF:
		return rails.H(l.assocCFContent(c, r, true))
	case query.ColumnAssociation:
		// parent.subject
		if r.ParentID != nil {
			if p := l.issue(*r.ParentID); p != nil && l.issueVisible(p) {
				return rails.H(p.Subject)
			}
		}
		return ""
	}
	idLink := func(text string) template.HTML {
		return rails.LinkTo(text, "/issues/"+strconv.FormatInt(r.ID, 10), nil)
	}
	switch c.Name {
	case "id":
		return idLink(strconv.FormatInt(r.ID, 10))
	case "subject":
		return idLink(r.Subject)
	case "project":
		return rails.H(l.formatObject(l.project(r.ProjectID), true, nil))
	case "tracker":
		return rails.H(l.tracker(r.TrackerID).Name)
	case "status":
		return rails.H(l.status(r.StatusID).Name)
	case "priority":
		return rails.H(l.priority(r.PriorityID).Name)
	case "author":
		return rails.H(l.formatObject(l.principal(r.AuthorID), true, nil))
	case "assigned_to":
		if u := l.principalPtr(r.AssignedToID); u != nil {
			return rails.H(l.formatObject(u, true, nil))
		}
		return ""
	case "category":
		if cat := l.category(r.CategoryID); cat != nil {
			return rails.H(cat.Name)
		}
		return ""
	case "fixed_version":
		if v := l.version(r.FixedVersionID); v != nil {
			return l.linkToVersion(v)
		}
		return ""
	case "parent":
		if r.ParentID == nil {
			return ""
		}
		p := l.issue(*r.ParentID)
		if p != nil && l.issueVisible(p) {
			return l.linkToIssue(p, redmine.LinkToIssueOptions{NoSubject: true})
		}
		return rails.H("#" + strconv.FormatInt(*r.ParentID, 10))
	case "start_date":
		if r.StartDate != nil {
			return rails.H(l.formatDate(*r.StartDate))
		}
		return ""
	case "due_date":
		if r.DueDate != nil {
			return rails.H(l.formatDate(*r.DueDate))
		}
		return ""
	case "estimated_hours":
		return rails.H(l.formatHours(r.EstimatedHours))
	case "total_estimated_hours":
		return rails.H(l.formatHours(l.totalEstimatedHours(r)))
	case "estimated_remaining_hours":
		v := l.estimatedRemainingHours(r)
		return rails.H(l.formatHours(&v))
	case "spent_hours", "total_spent_hours":
		v := r.SpentHours
		param := strconv.FormatInt(r.ID, 10)
		if c.Name == "total_spent_hours" {
			v, param = r.TotalSpentHours, "~"+param
		}
		var hours float64
		if v != nil {
			hours = *v
		}
		p := l.project(r.ProjectID)
		u := "/projects/" + p.Identifier + "/time_entries?issue_id=" + url.QueryEscape(param)
		return rails.LinkToIf(hours > 0, l.c.Loc.FormatHours(hours), u, nil)
	case "done_ratio":
		return helper.ProgressBar(l.doneRatio(r), "")
	case "created_on":
		return rails.H(l.formatTime(r.CreatedAt))
	case "updated_on":
		return rails.H(l.formatTime(r.UpdatedAt))
	case "closed_on":
		if r.ClosedAt != nil {
			return rails.H(l.formatTime(*r.ClosedAt))
		}
		return ""
	case "last_updated_by":
		if u := l.principalPtr(r.LastUpdatedByID); u != nil {
			return rails.H(l.formatObject(u, true, nil))
		}
		return ""
	case "relations":
		return l.relationsContent(r)
	case "attachments":
		atts := l.issueAttachments(r.ID)
		parts := make([]string, len(atts))
		for i, a := range atts {
			parts[i] = string(l.formatAttachment(a))
		}
		return template.HTML(strings.Join(parts, " "))
	case "watcher_users":
		if !l.c.AllowedTo(domain.Perm("view_issue_watchers"), l.project(r.ProjectID)) {
			return rails.ContentTag("ul", "", nil)
		}
		var b strings.Builder
		for _, id := range r.WatcherIDs {
			if u := l.principal(id); u != nil {
				b.WriteString(string(rails.ContentTag("li", l.formatObject(u, true, nil), nil)))
			}
		}
		return rails.ContentTag("ul", template.HTML(b.String()), nil)
	case "is_private":
		return rails.H(l.yesNo(r.IsPrivate))
	case "description":
		if strings.TrimSpace(r.Description) == "" {
			return ""
		}
		return rails.ContentTag("div", l.textilizeIssue(r, r.Description), rails.NewHash("class", "wiki"))
	case "last_notes":
		if r.LastNotes == nil || strings.TrimSpace(*r.LastNotes) == "" {
			return ""
		}
		return rails.ContentTag("div", l.textilizeIssue(r, *r.LastNotes), rails.NewHash("class", "wiki"))
	}
	return ""
}

// textilizeIssue は textilizable(issue, attr)。
func (l *issueLookup) textilizeIssue(r *query.IssueRow, text string) template.HTML {
	return l.renderer().Textilizable(text, redmine.Options{Object: &redmine.Object{Kind: "issue", ID: r.ID, Project: l.project(r.ProjectID)}})
}

// cfColumnContent は CF 列の column_content / csv_content（値が複数なら値で並べて ", " で連結）。
func (l *issueLookup) cfColumnContent(cf *customfield.CustomField, r *query.IssueRow, vals []string, projectID int64, customized *customfield.Customized, html bool) any {
	empty := func() any {
		if html {
			return template.HTML("")
		}
		return ""
	}
	if !l.cfVisibleBy(cf, l.project(projectID)) || len(vals) == 0 {
		return empty()
	}
	if len(vals) == 1 {
		return l.formatCustomValue(cf, vals[0], customized, html)
	}
	var parts []string
	for _, s := range sortedStrings(vals) {
		v := l.formatCustomValue(cf, s, customized, html)
		parts = append(parts, rails.ToS(v))
	}
	if html {
		return template.HTML(strings.Join(parts, ", "))
	}
	return strings.Join(parts, ", ")
}

// assocCFContent は QueryAssociationCustomFieldColumn（project.cf_N 等）の値。
func (l *issueLookup) assocCFContent(c *query.Column, r *query.IssueRow, html bool) any {
	cf := c.CustomField
	switch c.Association {
	case "project":
		p := l.project(r.ProjectID)
		if p == nil {
			break
		}
		vals, err := repository.CustomValues(l.ctx, l.a.DB, "project", p.ID)
		l.fail(err)
		cz := &customfield.Customized{Kind: "project", ID: p.ID, ProjectID: p.ID, ProjectIdentifier: p.Identifier}
		return l.cfColumnContent(cf, r, vals[cf.ID], p.ID, cz, html)
	case "author", "assigned_to", "last_updated_by":
		var u *domain.User
		switch c.Association {
		case "author":
			u = l.principal(r.AuthorID)
		case "assigned_to":
			u = l.principalPtr(r.AssignedToID)
		default:
			u = l.principalPtr(r.LastUpdatedByID)
		}
		if u == nil {
			break
		}
		vals, err := repository.CustomValues(l.ctx, l.a.DB, "principal", u.ID)
		l.fail(err)
		cz := &customfield.Customized{Kind: "principal", ID: u.ID}
		return l.cfColumnContent(cf, r, vals[cf.ID], 0, cz, html)
	case "fixed_version":
		v := l.version(r.FixedVersionID)
		if v == nil || !l.versionVisible(v) {
			break
		}
		vals, err := repository.CustomValues(l.ctx, l.a.DB, "version", v.ID)
		l.fail(err)
		cz := &customfield.Customized{Kind: "version", ID: v.ID, ProjectID: v.ProjectID}
		return l.cfColumnContent(cf, r, vals[cf.ID], v.ProjectID, cz, html)
	}
	if html {
		return template.HTML("")
	}
	return ""
}

// totalEstimatedHours は Issue#total_estimated_hours。
func (l *issueLookup) totalEstimatedHours(r *query.IssueRow) *float64 {
	if !l.hasChildren(r.ID) {
		return r.EstimatedHours
	}
	vis, err := l.c.Authz().IssueVisibleCondition(l.ctx, issueVisOpts())
	l.fail(err)
	var sum float64
	rows, err := repository.ReadIssuesWhere(l.ctx, l.a.DB, "issues.root_id = ? AND issues.hier_path LIKE ? AND ("+vis+")", "", r.RootID, r.HierPath+"%")
	l.fail(err)
	for _, x := range rows {
		if x.EstimatedHours != nil {
			sum += *x.EstimatedHours
		}
	}
	return &sum
}

// estimatedRemainingHours は Issue#estimated_remaining_hours。
func (l *issueLookup) estimatedRemainingHours(r *query.IssueRow) float64 {
	var e float64
	if r.EstimatedHours != nil {
		e = *r.EstimatedHours
	}
	return e * float64(100-l.doneRatio(r)) / 100
}

// relationsContent は :relations 列（関連ごとに span、", " 区切り）。
func (l *issueLookup) relationsContent(r *query.IssueRow) template.HTML {
	rels := l.sortedRelations(r.RelationIDs)
	parts := make([]string, 0, len(rels))
	for _, rel := range rels {
		other := rel.IssueFromID
		if other == r.ID {
			other = rel.IssueToID
		}
		o := l.issue(other)
		var text template.HTML
		if o != nil {
			text = l.linkToIssue(o, redmine.LinkToIssueOptions{NoSubject: true, NoTracker: true})
		}
		parts = append(parts, string(rails.ContentTag("span", template.HTML(l.relationToS(rel, r.ID, string(text))),
			rails.NewHash("class", "rel-"+relationTypeFor(rel, r.ID)))))
	}
	return template.HTML(strings.Join(parts, ", "))
}

// sortedRelations は関連を IssueRelation#<=> の順で返す。
func (l *issueLookup) sortedRelations(ids []int64) []*repository.IssueRelation {
	var miss []int64
	for _, id := range ids {
		if _, ok := l.relations[id]; !ok {
			miss = append(miss, id)
		}
	}
	if len(miss) > 0 {
		m, err := repository.RelationsByIDs(l.ctx, l.a.DB, miss)
		l.fail(err)
		for _, id := range miss {
			l.relations[id] = m[id]
		}
	}
	var out []*repository.IssueRelation
	var others []int64
	for _, id := range ids {
		if rel := l.relations[id]; rel != nil {
			out = append(out, rel)
			others = append(others, rel.IssueFromID, rel.IssueToID)
		}
	}
	l.preloadIssues(others)
	sortRelations(out)
	return out
}

// relationTypes は IssueRelation::TYPES（name, sym_name, order, sym）。
var relationTypes = map[string]struct {
	name, symName string
	order         int
	sym           string
}{
	"relates":     {"label_relates_to", "label_relates_to", 1, "relates"},
	"duplicates":  {"label_duplicates", "label_duplicated_by", 2, "duplicated"},
	"duplicated":  {"label_duplicated_by", "label_duplicates", 3, "duplicates"},
	"blocks":      {"label_blocks", "label_blocked_by", 4, "blocked"},
	"blocked":     {"label_blocked_by", "label_blocks", 5, "blocks"},
	"precedes":    {"label_precedes", "label_follows", 6, "follows"},
	"follows":     {"label_follows", "label_precedes", 7, "precedes"},
	"copied_to":   {"label_copied_to", "label_copied_from", 8, "copied_from"},
	"copied_from": {"label_copied_from", "label_copied_to", 9, "copied_to"},
}

func sortRelations(rs []*repository.IssueRelation) {
	slices.SortStableFunc(rs, func(a, b *repository.IssueRelation) int {
		oa, ob := relationTypes[a.RelationType].order, relationTypes[b.RelationType].order
		if oa != ob {
			return oa - ob
		}
		return int(a.ID - b.ID)
	})
}

// relationTypeFor は relation.relation_type_for(issue)。
func relationTypeFor(rel *repository.IssueRelation, issueID int64) string {
	if rel.IssueFromID == issueID {
		return rel.RelationType
	}
	return relationTypes[rel.RelationType].sym
}

// relationLabelFor は relation.label_for(issue)。
func relationLabelFor(rel *repository.IssueRelation, issueID int64) string {
	t, ok := relationTypes[rel.RelationType]
	if !ok {
		return "unknow"
	}
	if rel.IssueFromID == issueID {
		return t.name
	}
	return t.symName
}

// relationToS は relation.to_s(issue) { |other| text }（text は HTML）。
func (l *issueLookup) relationToS(rel *repository.IssueRelation, issueID int64, text string) string {
	s := []string{string(rails.H(l.L(relationLabelFor(rel, issueID))))}
	if rel.Delay != nil && *rel.Delay != 0 {
		s = append(s, string(rails.H("("+l.L("datetime.distance_in_words.x_days", map[string]any{"count": *rel.Delay})+")")))
	}
	s = append(s, text)
	return strings.Join(s, " ")
}

// issueAttachments は issue.attachments。
func (l *issueLookup) issueAttachments(id int64) []*repository.ReadAttachment {
	atts, err := repository.ReadContainerAttachments(l.ctx, l.a.DB, "issue", id)
	l.fail(err)
	return atts
}

// formatAttachment は format_object(attachment)（html）。
func (l *issueLookup) formatAttachment(a *repository.ReadAttachment) template.HTML {
	id := strconv.FormatInt(a.ID, 10)
	link := rails.LinkTo(a.Filename, "/attachments/"+id, nil)
	dl := rails.LinkTo(l.icon("download", a.Filename), "/attachments/download/"+id+"/"+url.PathEscape(a.Filename),
		rails.NewHash("class", "icon-only icon-download", "title", l.L("button_download")))
	return rails.ContentTag("span", link+dl, rails.NewHash("class", "attachment-filename"))
}
