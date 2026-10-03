// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"html/template"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/urlroot"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは QueriesHelper（app/helpers/queries_helper.rb）のうち、クエリの種類に依存しない
// フォーム・一覧部品（queries/_query_form, _filters, _columns, column_header, query_as_hidden_field_tags,
// render_query_totals, sidebar_queries ...）の移植。テンプレートからは queryView のメソッドとして呼ぶ。
//
// TODO(dedupe): admin users の移植（users/_query_form 等）はここに置き換えられる。

// queryView はテンプレートに渡すクエリ（@query）と、その描画に必要な値。
type queryView struct {
	c   *Req
	ctx context.Context
	Q   *query.Query
	// Type は @query.type（"ProjectQuery" 等）。
	Type string
	// Path は一覧のパス（projects_path 等。Clear リンク・列見出しのリンクに使う）。
	Path string
	// NewQueryPath は保存フォームの action（new_query_path / new_project_query_path）。
	NewQueryPath string
	// CanSave は User.current.allowed_to?(:save_queries, @project, :global => true)。
	CanSave bool
	// Editable は @query.editable_by?(User.current)。
	Editable bool
	// ErrorMessages は error_messages_for @query の内容。
	ErrorMessages []string
	// Valid は @query.valid?。
	Valid bool
	// IntDefaultStatus は UserQuery の既定フィルタ（status = [1]）を整数のまま出す（Redmine の
	// UserQuery#initialize は整数の User::STATUS_ACTIVE を保持し、addFilter に [1] と出る）。
	IntDefaultStatus bool
}

// newQueryView は q の queryView を作る。
func (a *App) newQueryView(c *Req, q *query.Query, typ, path string) (*queryView, error) {
	ctx := c.Ctx()
	qv := &queryView{c: c, ctx: ctx, Q: q, Type: typ, Path: path, NewQueryPath: "/queries/new"}
	if c.Project != nil {
		qv.NewQueryPath = "/projects/" + c.Project.Identifier + "/queries/new"
	}
	qv.CanSave = c.allowedToGloballyOrProject("save_queries", c.Project)
	if q.ID != 0 {
		ok, err := q.EditableBy(ctx, c.Authz())
		if err != nil {
			return nil, err
		}
		qv.Editable = ok
	}
	msgs, err := q.Errors(ctx)
	if err != nil {
		return nil, err
	}
	qv.ErrorMessages = msgs
	qv.Valid = len(msgs) == 0
	return qv, nil
}

// NewRecord は @query.new_record?。
func (qv *queryView) NewRecord() bool { return qv.Q.ID == 0 }

func (qv *queryView) env() *query.Env { return qv.Q.Env() }

// Caption は column.caption。
func (qv *queryView) Caption(col *query.Column) string { return col.CaptionText(qv.env()) }

// ---------------------------------------------------------------- _filters

// queryRawJSON は raw_json（to_json の / を \/ に置き換える）。
func queryRawJSON(v any) template.HTML {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	_ = enc.Encode(v)
	s := strings.TrimRight(buf.String(), "\n")
	return template.HTML(strings.ReplaceAll(s, "/", `\/`))
}

// orderedJSON は順序付きの JSON オブジェクト（Ruby の Hash#to_json）。
type orderedJSON struct {
	keys []string
	vals map[string]any
}

func (o *orderedJSON) set(k string, v any) {
	if o.vals == nil {
		o.vals = map[string]any{}
	}
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *orderedJSON) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := marshalNoEscapeErr(k)
		b.Write(kb)
		b.WriteByte(':')
		vb, err := marshalNoEscapeErr(o.vals[k])
		if err != nil {
			return nil, err
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func marshalNoEscapeErr(v any) ([]byte, error) {
	if o, ok := v.(*orderedJSON); ok {
		return o.MarshalJSON()
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// operatorFilterTypes は Query.operators_by_filter_type のキーの順序。
var operatorFilterTypes = []string{"list", "list_with_history", "list_status", "list_optional", "list_optional_with_history",
	"list_subprojects", "date", "date_past", "string", "text", "search", "integer", "float", "relation", "tree"}

// OperatorLabelsJSON は raw_json Query.operators_labels。
func (qv *queryView) OperatorLabelsJSON() template.HTML {
	labels := qv.env().OperatorsLabels()
	o := &orderedJSON{}
	for _, op := range query.Operators {
		o.set(op.Op, labels[op.Op])
	}
	return queryRawJSON(o)
}

// OperatorByTypeJSON は raw_json Query.operators_by_filter_type。
func (qv *queryView) OperatorByTypeJSON() template.HTML {
	o := &orderedJSON{}
	for _, t := range operatorFilterTypes {
		o.set(t, query.OperatorsByFilterType[t])
	}
	return queryRawJSON(o)
}

// AvailableFiltersJSON は raw_json query.available_filters_as_json。
func (qv *queryView) AvailableFiltersJSON() (template.HTML, error) {
	af, err := qv.Q.AvailableFilters(qv.ctx)
	if err != nil {
		return "", err
	}
	o := &orderedJSON{}
	for _, def := range af.Defs() {
		f := &orderedJSON{}
		f.set("type", def.Type)
		f.set("name", def.Name)
		if def.Remote {
			f.set("remote", true)
		}
		if qv.Q.HasFilter(def.Field) || !def.Remote {
			vals, err := def.LoadValues(qv.ctx)
			if err != nil {
				return "", err
			}
			f.set("values", filterValuesJSON(def, vals))
		}
		o.set(def.Field, f)
	}
	return queryRawJSON(o), nil
}

// LabelDayPluralJSON は raw_json l(:label_day_plural)。
func (qv *queryView) LabelDayPluralJSON() template.HTML {
	return queryRawJSON(qv.c.L("label_day_plural"))
}

// FiltersURLJSON は raw_json queries_filter_path(:project_id => @query.project.try(:id), :type => @query.type)。
func (qv *queryView) FiltersURLJSON() template.HTML {
	v := url.Values{}
	if qv.Q.Project != nil {
		v.Set("project_id", strconv.FormatInt(qv.Q.Project.ID, 10))
	}
	v.Set("type", qv.Type)
	return queryRawJSON(urlroot.Path("/queries/filter?" + v.Encode()))
}

// filterLine は addFilter の 1 行。
type filterLine struct {
	Field    string
	Operator template.HTML
	Values   template.HTML
}

// FilterLines は query.filters.each の addFilter 呼び出し。
func (qv *queryView) FilterLines() []filterLine {
	var out []filterLine
	for _, k := range qv.Q.Filters.Keys() {
		vals := qv.Q.ValuesFor(k)
		if vals == nil {
			vals = []string{}
		}
		var jv any = vals
		if qv.IntDefaultStatus && k == "status" && qv.Q.OperatorFor(k) == "=" && len(vals) == 1 && vals[0] == "1" {
			jv = []int{domain.StatusActive}
		}
		out = append(out, filterLine{Field: k, Operator: queryRawJSON(qv.Q.OperatorFor(k)), Values: queryRawJSON(jv)})
	}
	return out
}

var cfAssocFilterRe = regexp.MustCompile(`^cf_\d+\.`)
var assocFilterRe = regexp.MustCompile(`^(.+)\.`)

// FilterOptions は filters_options_for_select(query)。
func (qv *queryView) FilterOptions() (template.HTML, error) {
	af, err := qv.Q.AvailableFilters(qv.ctx)
	if err != nil {
		return "", err
	}
	type group struct {
		key   string
		label string
		items [][]any
	}
	var groups []*group
	byKey := map[string]*group{}
	for _, k := range []string{"label_string", "label_date", "label_time_tracking", "label_attachment"} {
		g := &group{key: k}
		groups = append(groups, g)
		byKey[k] = g
	}
	ungrouped := [][]any{}
	for _, def := range af.Defs() {
		field := def.Field
		var gkey, glabel string
		switch {
		case cfAssocFilterRe.MatchString(field):
			cf := def.Through
			if cf == nil {
				cf = def.CustomField
			}
			if cf != nil {
				gkey, glabel = "cf:"+cf.Name, cf.Name
			}
		case assocFilterRe.MatchString(field):
			m := assocFilterRe.FindStringSubmatch(field)
			gkey = "field_" + m[1]
		case def.Type == "relation":
			gkey = "label_relations"
		case def.Type == "tree":
			if qv.Q.Kind == query.KindIssue {
				gkey = "label_relations"
			}
		case field == "member_of_group" || field == "assigned_to_role":
			gkey = "field_assigned_to"
		case def.Type == "date_past" || def.Type == "date":
			gkey = "label_date"
		case field == "estimated_hours" || field == "spent_time":
			gkey = "label_time_tracking"
		case field == "attachment" || field == "attachment_description":
			gkey = "label_attachment"
		case def.Type == "string" || def.Type == "text" || def.Type == "search":
			gkey = "label_string"
		}
		item := []any{def.Name, field}
		if gkey == "" {
			ungrouped = append(ungrouped, item)
			continue
		}
		g := byKey[gkey]
		if g == nil {
			g = &group{key: gkey, label: glabel}
			groups = append(groups, g)
			byKey[gkey] = g
		}
		g.items = append(g.items, item)
	}
	// 空のグループを除き、日付が 1 つだけならグループにしない
	var kept []*group
	for _, g := range groups {
		if len(g.items) == 0 {
			continue
		}
		if g.key == "label_date" && len(g.items) == 1 {
			ungrouped = append(ungrouped, g.items[0])
			continue
		}
		kept = append(kept, g)
	}
	container := append([]any{[]any{}}, toAnyItems(ungrouped)...)
	s := rails.OptionsForSelect(container, nil)
	if len(kept) > 0 {
		var grouped []any
		for _, g := range kept {
			label := g.label
			if label == "" {
				label = qv.c.L(g.key)
			}
			grouped = append(grouped, []any{label, toAnyItems(g.items)})
		}
		s += rails.GroupedOptionsForSelect(grouped, nil, nil)
	}
	return s, nil
}

func toAnyItems(items [][]any) []any {
	out := make([]any, len(items))
	for i, it := range items {
		out[i] = it
	}
	return out
}

// HiddenSortTag は query_hidden_sort_tag(query)。
func (qv *queryView) HiddenSortTag() template.HTML {
	return rails.HiddenFieldTag("sort", qv.Q.SortCriteria().ToParam(), rails.NewHash("id", nil))
}

// ---------------------------------------------------------------- _query_form のオプション

// HasAvailableColumns は @query.available_columns.any?。
func (qv *queryView) HasAvailableColumns() (bool, error) {
	cols, err := qv.Q.AvailableColumns(qv.ctx)
	return len(cols) > 0, err
}

// MultipleDisplayTypes は @query.available_display_types.size > 1。
func (qv *queryView) MultipleDisplayTypes() bool { return len(qv.Q.AvailableDisplayTypes()) > 1 }

// IsList は @query.display_type == 'list'。
func (qv *queryView) IsList() bool { return qv.Q.DisplayType() == "list" }

// DisplayTypeTags は available_display_types_tags(query)。
func (qv *queryView) DisplayTypeTags() template.HTML {
	var b strings.Builder
	for _, t := range qv.Q.AvailableDisplayTypes() {
		id := "display_type_" + t
		b.WriteString(string(rails.RadioButtonTag("display_type", t, qv.Q.DisplayType() == t, rails.NewHash("id", id))))
		b.WriteString(string(rails.ContentTag("label", qv.c.L("label_display_type_"+t), rails.NewHash("for", id, "class", "inline"))))
	}
	return template.HTML(b.String())
}

// AvailableInlineColumnsOptions は query_available_inline_columns_options(query)。
func (qv *queryView) AvailableInlineColumnsOptions() (template.HTML, error) {
	avail, err := qv.Q.AvailableInlineColumns(qv.ctx)
	if err != nil {
		return "", err
	}
	cols, err := qv.Q.Columns(qv.ctx)
	if err != nil {
		return "", err
	}
	var items []any
	for _, c := range avail {
		if slices.Contains(cols, c) || c.Frozen {
			continue
		}
		items = append(items, []any{qv.Caption(c), c.Name})
	}
	return rails.OptionsForSelect(items, nil), nil
}

// SelectedInlineColumnsOptions は query_selected_inline_columns_options(query)。
func (qv *queryView) SelectedInlineColumnsOptions() (template.HTML, error) {
	inline, err := qv.Q.InlineColumns(qv.ctx)
	if err != nil {
		return "", err
	}
	avail, err := qv.Q.AvailableInlineColumns(qv.ctx)
	if err != nil {
		return "", err
	}
	var items []any
	for _, c := range inline {
		if !slices.Contains(avail, c) || c.Frozen {
			continue
		}
		items = append(items, []any{qv.Caption(c), c.Name})
	}
	return rails.OptionsForSelect(items, nil), nil
}

// HasGroupableColumns は @query.groupable_columns.any?。
func (qv *queryView) HasGroupableColumns() (bool, error) {
	cols, err := qv.Q.GroupableColumns(qv.ctx)
	return len(cols) > 0, err
}

// GroupBySelectTag は group_by_column_select_tag(query)。
func (qv *queryView) GroupBySelectTag() (template.HTML, error) {
	cols, err := qv.Q.GroupableColumns(qv.ctx)
	if err != nil {
		return "", err
	}
	items := []any{[]any{}}
	for _, c := range cols {
		items = append(items, []any{qv.Caption(c), c.Name})
	}
	var sel any
	if qv.Q.GroupBy != "" {
		sel = qv.Q.GroupBy
	}
	return rails.SelectTag("group_by", rails.OptionsForSelect(items, sel), nil), nil
}

// HasAvailableBlockColumns は @query.available_block_columns.any?。
func (qv *queryView) HasAvailableBlockColumns() (bool, error) {
	cols, err := qv.Q.AvailableBlockColumns(qv.ctx)
	return len(cols) > 0, err
}

// BlockColumnsTags は available_block_columns_tags(query)。
func (qv *queryView) BlockColumnsTags() (template.HTML, error) {
	cols, err := qv.Q.AvailableBlockColumns(qv.ctx)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, c := range cols {
		has, err := qv.Q.HasColumn(qv.ctx, c.Name)
		if err != nil {
			return "", err
		}
		b.WriteString(string(rails.ContentTag("label",
			rails.CheckBoxTag("c[]", c.Name, has, rails.NewHash("id", nil))+rails.H(" "+qv.Caption(c)),
			rails.NewHash("class", "inline"))))
	}
	return template.HTML(b.String()), nil
}

// HasAvailableTotalableColumns は @query.available_totalable_columns.any?。
func (qv *queryView) HasAvailableTotalableColumns() (bool, error) {
	cols, err := qv.Q.AvailableTotalableColumns(qv.ctx)
	return len(cols) > 0, err
}

// TotalableColumnsTags は available_totalable_columns_tags(query)。
func (qv *queryView) TotalableColumnsTags() (template.HTML, error) {
	cols, err := qv.Q.AvailableTotalableColumns(qv.ctx)
	if err != nil {
		return "", err
	}
	totals, err := qv.Q.TotalableColumns(qv.ctx)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, c := range cols {
		b.WriteString(string(rails.ContentTag("label",
			rails.CheckBoxTag("t[]", c.Name, slices.Contains(totals, c), rails.NewHash("id", nil))+rails.H(" "+qv.Caption(c)),
			rails.NewHash("class", "inline"))))
	}
	b.WriteString(string(rails.HiddenFieldTag("t[]", "", nil)))
	return template.HTML(b.String()), nil
}

// ClearPath は link_to l(:button_clear), { :set_filter => 1, :sort => ”, :project_id => @project }。
func (qv *queryView) ClearPath() string {
	return qv.Path + "?set_filter=1&sort="
}

// SaveQueryOnClick は「保存」リンクの onclick。
func (qv *queryView) SaveQueryOnClick() string {
	return "$('#query_type').prop('disabled',false);$('#query_form').attr('action', '" + urlroot.Path(qv.NewQueryPath) + "').submit()"
}

// EditQueryPath は edit_query_path(@query)。
func (qv *queryView) EditQueryPath() string {
	return "/queries/" + strconv.FormatInt(qv.Q.ID, 10) + "/edit"
}

// QueryPath は query_path(@query)。
func (qv *queryView) QueryPath() string { return "/queries/" + strconv.FormatInt(qv.Q.ID, 10) }

// ---------------------------------------------------------------- 一覧（column_header / totals / hidden tags）

// InlineColumns は @query.inline_columns。
func (qv *queryView) InlineColumns() ([]*query.Column, error) { return qv.Q.InlineColumns(qv.ctx) }

// CSSClasses は @query.css_classes。
func (qv *queryView) CSSClasses() string { return qv.Q.CSSClasses() }

// ColumnHeader は column_header(query, column)。
func (qv *queryView) ColumnHeader(col *query.Column) template.HTML {
	caption := qv.Caption(col)
	var content any = caption
	if col.IsSortable() {
		var css any
		order := col.DefaultOrder
		var icon string
		sc := qv.Q.SortCriteria()
		if col.Name == sc.FirstKey() {
			if sc.FirstAsc() {
				css, icon, order = "sort asc icon icon-sorted-desc", "angle-up", "desc"
			} else {
				css, icon, order = "sort desc icon icon-sorted-asc", "angle-down", "asc"
			}
		}
		sortParam := sc.Add(col.Name, order).ToParam()
		q := qv.c.queryParametersMerge(map[string]string{"sort": sortParam})
		u := qv.c.R.URL.Path
		if q != "" {
			u += "?" + q
		}
		var name any = caption
		if icon != "" {
			name = qv.c.App.Helpers.SpriteIconHTML(qv.c.Page(), icon, caption)
		}
		content = rails.LinkTo(name, u, rails.NewHash("title", qv.c.L("label_sort_by", "\""+caption+"\""), "class", css))
	}
	return rails.ContentTag("th", content, rails.NewHash("class", col.CSSClasses()))
}

// TotalsHTML は render_query_totals(query)（合計列が無ければ空）。
func (qv *queryView) TotalsHTML() (template.HTML, error) {
	cols, err := qv.Q.TotalableColumns(qv.ctx)
	if err != nil || len(cols) == 0 {
		return "", err
	}
	var parts []string
	for _, c := range cols {
		v, err := qv.Q.TotalFor(qv.ctx, c.Name)
		if err != nil {
			return "", err
		}
		parts = append(parts, string(qv.totalTag(c, v)))
	}
	return rails.ContentTag("p", template.HTML(strings.Join(parts, " ")), rails.NewHash("class", "query-totals")), nil
}

// totalTag は total_tag(column, value)。
func (qv *queryView) totalTag(c *query.Column, v float64) template.HTML {
	label := rails.ContentTag("span", qv.Caption(c)+":", nil)
	var value string
	switch c.Name {
	case "hours", "spent_hours", "total_spent_hours", "estimated_hours", "total_estimated_hours", "estimated_remaining_hours":
		value = qv.c.Loc.FormatHours(v)
	default:
		value = formatNumber(v)
	}
	val := rails.ContentTag("span", value, rails.NewHash("class", "value"))
	return rails.ContentTag("span", label+" "+val, rails.NewHash("class", "total-for-"+strings.ReplaceAll(c.Name, "_", "-")))
}

func formatNumber(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}

// AsHiddenFieldTags は query_as_hidden_field_tags(query)。
func (qv *queryView) AsHiddenFieldTags() (template.HTML, error) {
	q := qv.Q
	var b strings.Builder
	noID := func() *rails.Hash { return rails.NewHash("id", nil) }
	b.WriteString(string(rails.HiddenFieldTag("set_filter", "1", noID())))
	if q.Filters.Len() > 0 {
		for _, k := range q.Filters.Keys() {
			f, _ := q.Filters.Get(k)
			b.WriteString(string(rails.HiddenFieldTag("f[]", k, noID())))
			b.WriteString(string(rails.HiddenFieldTag("op["+k+"]", f.Operator, noID())))
			for _, v := range f.Values {
				b.WriteString(string(rails.HiddenFieldTag("v["+k+"][]", v, noID())))
			}
		}
	} else {
		b.WriteString(string(rails.HiddenFieldTag("f[]", "", noID())))
	}
	cols, err := q.Columns(qv.ctx)
	if err != nil {
		return "", err
	}
	for _, c := range cols {
		b.WriteString(string(rails.HiddenFieldTag("c[]", c.Name, noID())))
	}
	for _, n := range q.TotalableNames() {
		b.WriteString(string(rails.HiddenFieldTag("t[]", n, noID())))
	}
	if q.GroupBy != "" {
		b.WriteString(string(rails.HiddenFieldTag("group_by", q.GroupBy, noID())))
	}
	if sc := q.SortCriteria(); len(sc) > 0 {
		b.WriteString(string(rails.HiddenFieldTag("sort", sc.ToParam(), noID())))
	}
	return template.HTML(b.String()), nil
}

// queryParametersMerge は request.query_parameters.deep_merge(extra).to_query（キー順）。
func (c *Req) queryParametersMerge(extra map[string]string) string {
	v := url.Values{}
	for k, vals := range c.R.URL.Query() {
		v[k] = vals
	}
	for k, s := range extra {
		v.Set(k, s)
	}
	return railsToQuery(v)
}

// railsToQuery は Hash#to_query（キー順、配列は key[]=v の形のまま）。
func railsToQuery(v url.Values) string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var parts []string
	for _, k := range keys {
		for _, s := range v[k] {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(s))
		}
	}
	return strings.Join(parts, "&")
}

// ---------------------------------------------------------------- サイドバー（render_sidebar_queries）

// SidebarQueriesHTML は render_sidebar_queries(klass, project)。
func (a *App) sidebarQueriesHTML(c *Req, kind query.Kind, current *query.Query, listPath string) (template.HTML, error) {
	ctx := c.Ctx()
	saved, err := query.ListVisible(ctx, c.Authz(), kind, c.Project, c.Project == nil)
	if err != nil {
		return "", err
	}
	env := current.Env()
	def, err := query.Default(ctx, env, kind, c.Project)
	if err != nil {
		return "", err
	}
	var mine, public []query.SavedQuery
	for _, s := range saved {
		if s.Visibility == query.VisibilityPrivate {
			mine = append(mine, s)
		} else {
			public = append(public, s)
		}
	}
	links := func(title string, qs []query.SavedQuery) string {
		if len(qs) == 0 {
			return ""
		}
		var items []string
		for _, s := range qs {
			css := "query"
			clearParams := "set_filter=1&sort="
			if def != nil && def.ID == s.ID {
				css += " default"
				clearParams = "set_filter=1&sort=&without_default=1"
			}
			clear := ""
			if current != nil && current.ID == s.ID {
				css += " selected"
				clear = string(rails.LinkTo(a.Helpers.SpriteIconHTML(c.Page(), "clear-query", c.L("button_clear")),
					listPath+"?"+clearParams, rails.NewHash("class", "icon-only icon-clear-query", "title", c.L("button_clear"))))
			}
			var title any
			if s.Description != "" {
				title = s.Description
			}
			link := rails.LinkTo(s.Name, listPath+"?query_id="+strconv.FormatInt(s.ID, 10),
				rails.NewHash("class", css, "title", title, "data", rails.NewHash("disable_with", escapeHTML(s.Name))))
			items = append(items, string(rails.ContentTag("li", link+template.HTML(clear), nil)))
		}
		return string(rails.ContentTag("h3", title, nil)) + "\n" +
			string(rails.ContentTag("ul", template.HTML(strings.Join(items, "\n")), rails.NewHash("class", "queries"))) + "\n"
	}
	return template.HTML(links(c.L("label_my_queries"), mine) + links(c.L("label_query_plural"), public)), nil
}

// ---------------------------------------------------------------- retrieve_query

// queryParams は Rails の params を query.Params にする（クエリ文字列と本文）。
func queryParams(c *Req) query.Params { return query.ParseParams(railsParamValues(c)) }

// railsParamValues はクエリ文字列と本文のパラメータを Rails 形式のキーの url.Values にする。
func railsParamValues(c *Req) url.Values {
	v := url.Values{}
	for k, vals := range c.R.URL.Query() {
		v[k] = append(v[k], vals...)
	}
	if c.R.PostForm != nil {
		for k, vals := range c.R.PostForm {
			v[k] = append(v[k], vals...)
		}
	} else if body := httpx.BodyParams(c.R); body.Len() > 0 {
		// 本文はネストした Params として解析済みなので Rails 形式のキー（f[], op[x], query[sort_criteria][0][] ...）に戻す
		flattenParams(v, "", body)
	}
	return v
}

// flattenParams はネストした Params を Rails 形式のキーの url.Values に展開する。
func flattenParams(out url.Values, prefix string, p *httpx.Params) {
	p.Each(func(k string, val any) {
		key := k
		if prefix != "" {
			key = prefix + "[" + k + "]"
		}
		flattenParamValue(out, key, val)
	})
}

func flattenParamValue(out url.Values, key string, val any) {
	switch x := val.(type) {
	case *httpx.Params:
		flattenParams(out, key, x)
	case []any:
		for _, e := range x {
			if sub, ok := e.(*httpx.Params); ok {
				flattenParams(out, key+"[]", sub)
			} else {
				flattenParamValue(out, key+"[]", e)
			}
		}
	case nil:
		out[key] = append(out[key], "")
	case string:
		out[key] = append(out[key], x)
	case *httpx.UploadedFile:
	default:
		out[key] = append(out[key], httpx.ValueString(x))
	}
}

// queryEnv は User.current の query.Env。
func (a *App) queryEnv(c *Req) (*query.Env, error) {
	env, err := query.NewEnv(c.Ctx(), a.DB, c.User, a.Settings)
	if err != nil {
		return nil, err
	}
	env.Auth = c.Authz()
	env.L = c.Loc
	env.Now = a.now
	return env, nil
}

// unused guard（domain / httpx はテンプレート用ヘルパーで使う）
var _ = domain.Perm
var _ = httpx.Format
