package handler

import (
	"encoding/json"
	"net/url"
	"sort"
	"strings"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは UserQuery（app/models/user_query.rb）と QueriesHelper#retrieve_query のうち
// ユーザー一覧に必要な部分の暫定的な移植。
//
// TODO(query): internal/query（Query / QueryColumn / QueryFilter の汎用実装）が入ったら置き換える。
// それまでは保存クエリ（query_id）・グループ化・カスタムフィールドのフィルタ／列には対応しない。

// userQueryColumn は UserQuery の QueryColumn。
type userQueryColumn struct {
	Name    string
	Caption string // i18n キー
	// Sortable は並べ替えの repository.UserSortColumns のキー（空なら並べ替え不可）。
	Sortable string
	// Totalable は合計を表示できる列。
	Totalable bool
	// CustomFieldID はカスタムフィールド列の id（0 なら通常の列）。
	CustomFieldID int64
	// CaptionText は翻訳済みの見出し（カスタムフィールド列）。
	CaptionText string
}

// userQueryColumns は UserQuery.available_columns（twofa_scheme は Setting.twofa? のときのみ）。
func userQueryColumns(twofa bool) []userQueryColumn {
	cols := []userQueryColumn{
		{Name: "login", Caption: "field_login", Sortable: "login"},
		{Name: "firstname", Caption: "field_firstname", Sortable: "firstname"},
		{Name: "lastname", Caption: "field_lastname", Sortable: "lastname"},
		{Name: "mail", Caption: "field_mail", Sortable: "mail"},
		{Name: "admin", Caption: "field_admin", Sortable: "admin"},
		{Name: "created_on", Caption: "field_created_on", Sortable: "created_on"},
		{Name: "updated_on", Caption: "field_updated_on", Sortable: "updated_on"},
		{Name: "last_login_on", Caption: "field_last_login_on", Sortable: "last_login_on"},
		{Name: "passwd_changed_on", Caption: "field_passwd_changed_on", Sortable: "passwd_changed_on"},
		{Name: "status", Caption: "field_status", Sortable: "status"},
		{Name: "auth_source.name", Caption: "field_auth_source", Sortable: "auth_source"},
	}
	if twofa {
		cols = append(cols, userQueryColumn{Name: "twofa_scheme", Caption: "field_twofa_scheme", Sortable: "twofa_scheme"})
	}
	return cols
}

// userQueryDefaultColumns は UserQuery#default_columns_names。
var userQueryDefaultColumns = []string{"login", "firstname", "lastname", "mail", "admin", "created_on", "last_login_on"}

// userQueryFilterDef は UserQuery の available_filters の 1 項目。
type userQueryFilterDef struct {
	Field  string
	Type   string
	Label  string // i18n キー
	Remote bool
}

// queryOperatorKeys は Query.operators のキー（定義順）とラベル。
var queryOperatorKeys = []struct{ Op, Label string }{
	{"=", "label_equals"}, {"!", "label_not_equals"}, {"o", "label_open_issues"}, {"c", "label_closed_issues"},
	{"!*", "label_none"}, {"*", "label_any"}, {">=", "label_greater_or_equal"}, {"<=", "label_less_or_equal"},
	{"><", "label_between"}, {"<t+", "label_in_less_than"}, {">t+", "label_in_more_than"},
	{"><t+", "label_in_the_next_days"}, {"t+", "label_in"}, {"nd", "label_tomorrow"}, {"t", "label_today"},
	{"ld", "label_yesterday"}, {"nw", "label_next_week"}, {"w", "label_this_week"}, {"lw", "label_last_week"},
	{"l2w", "label_last_n_weeks"}, {"nm", "label_next_month"}, {"m", "label_this_month"}, {"lm", "label_last_month"},
	{"y", "label_this_year"}, {">t-", "label_less_than_ago"}, {"<t-", "label_more_than_ago"},
	{"><t-", "label_in_the_past_days"}, {"t-", "label_ago"}, {"~", "label_contains"}, {"!~", "label_not_contains"},
	{"*~", "label_contains_any_of"}, {"^", "label_starts_with"}, {"$", "label_ends_with"},
	{"=p", "label_any_issues_in_project"}, {"=!p", "label_any_issues_not_in_project"}, {"!p", "label_no_issues_in_project"},
	{"*o", "label_any_open_issues"}, {"!o", "label_no_open_issues"}, {"ev", "label_has_been"},
	{"!ev", "label_has_never_been"}, {"cf", "label_changed_from"},
}

// queryOperatorsByType は Query.operators_by_filter_type（定義順）。
var queryOperatorsByType = []struct {
	Type string
	Ops  []string
}{
	{"list", []string{"=", "!"}},
	{"list_with_history", []string{"=", "!", "ev", "!ev", "cf"}},
	{"list_status", []string{"o", "=", "!", "ev", "!ev", "cf", "c", "*"}},
	{"list_optional", []string{"=", "!", "!*", "*"}},
	{"list_optional_with_history", []string{"=", "!", "ev", "!ev", "cf", "!*", "*"}},
	{"list_subprojects", []string{"*", "!*", "=", "!"}},
	{"date", []string{"=", ">=", "<=", "><", "<t+", ">t+", "><t+", "t+", "nd", "t", "ld", "nw", "w", "lw", "l2w", "nm", "m", "lm", "y", ">t-", "<t-", "><t-", "t-", "!*", "*"}},
	{"date_past", []string{"=", ">=", "<=", "><", ">t-", "<t-", "><t-", "t-", "t", "ld", "w", "lw", "l2w", "m", "lm", "y", "!*", "*"}},
	{"string", []string{"~", "*~", "=", "!~", "!", "^", "$", "!*", "*"}},
	{"text", []string{"~", "*~", "!~", "^", "$", "!*", "*"}},
	{"search", []string{"~", "*~", "!~"}},
	{"integer", []string{"=", ">=", "<=", "><", "!*", "*"}},
	{"float", []string{"=", ">=", "<=", "><", "!*", "*"}},
	{"relation", []string{"=", "!", "=p", "=!p", "!p", "*o", "!o", "!*", "*"}},
	{"tree", []string{"=", "~", "!*", "*"}},
}

func operatorsFor(typ string) []string {
	for _, t := range queryOperatorsByType {
		if t.Type == typ {
			return t.Ops
		}
	}
	return nil
}

// userQueryFilters は UserQuery#initialize_available_filters（カスタムフィールドを除く）。
func userQueryFilters(twofa bool) []userQueryFilterDef {
	f := []userQueryFilterDef{
		{"status", "list_optional", "field_status", true},
		{"auth_source_id", "list_optional", "field_auth_source", true},
		{"is_member_of_group", "list_optional", "field_is_member_of_group", true},
	}
	if twofa {
		f = append(f, userQueryFilterDef{"twofa_scheme", "list_optional", "field_twofa_scheme", true})
	}
	return append(f,
		userQueryFilterDef{"name", "text", "field_name_or_email_or_login", false},
		userQueryFilterDef{"login", "string", "field_login", false},
		userQueryFilterDef{"firstname", "string", "field_firstname", false},
		userQueryFilterDef{"lastname", "string", "field_lastname", false},
		userQueryFilterDef{"mail", "string", "field_mail", false},
		userQueryFilterDef{"created_on", "date_past", "field_created_on", false},
		userQueryFilterDef{"last_login_on", "date_past", "field_last_login_on", false},
		userQueryFilterDef{"admin", "list", "field_admin", false},
	)
}

// queryFilter は query.filters の 1 項目（{operator:, values:}）。値は any（既定の status は整数 1）。
type queryFilter struct {
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Values   []any  `json:"values"`
}

// userQuery は UserQuery のインスタンス。
type userQuery struct {
	Filters     []queryFilter
	ColumnNames []string // nil = 既定の列
	Totalable   []string
	Sort        [][2]string
	twofa       bool
	defs        []userQueryFilterDef
	cols        []userQueryColumn
}

func newUserQuery(twofa bool) *userQuery {
	return &userQuery{
		Filters: []queryFilter{{Field: "status", Operator: "=", Values: []any{domain.StatusActive}}},
		twofa:   twofa, defs: userQueryFilters(twofa), cols: userQueryColumns(twofa),
	}
}

func (q *userQuery) filterDef(field string) *userQueryFilterDef {
	for i := range q.defs {
		if q.defs[i].Field == field {
			return &q.defs[i]
		}
	}
	return nil
}

// filter は has_filter?(field)。
func (q *userQuery) filter(field string) *queryFilter {
	for i := range q.Filters {
		if q.Filters[i].Field == field {
			return &q.Filters[i]
		}
	}
	return nil
}

// addFilter は Query#add_filter（未知のフィールドは無視。values が nil なら [""]）。
func (q *userQuery) addFilter(field, op string, values []any) {
	if q.filterDef(field) == nil {
		return
	}
	if values == nil {
		values = []any{""}
	}
	if f := q.filter(field); f != nil {
		f.Operator, f.Values = op, values
		return
	}
	q.Filters = append(q.Filters, queryFilter{Field: field, Operator: op, Values: values})
}

// addShortFilter は Query#add_short_filter。
func (q *userQuery) addShortFilter(field, expr string) {
	def := q.filterDef(field)
	if def == nil {
		return
	}
	ops := append([]string(nil), operatorsFor(def.Type)...)
	sort.Sort(sort.Reverse(sort.StringSlice(ops)))
	for _, op := range ops {
		if strings.HasPrefix(expr, op) {
			rest := expr[len(op):]
			var vals []any
			if strings.TrimSpace(rest) != "" {
				for _, v := range strings.Split(rest, "|") {
					vals = append(vals, v)
				}
			} else {
				vals = []any{""}
			}
			q.addFilter(field, op, vals)
			return
		}
	}
	var vals []any
	for _, v := range strings.Split(expr, "|") {
		vals = append(vals, v)
	}
	q.addFilter(field, "=", vals)
}

func anySlice(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case nil:
		return nil
	default:
		return []any{httpx.ValueString(x)}
	}
}

// buildFromParams は Query#build_from_params。
func (q *userQuery) buildFromParams(p *httpx.Params) {
	fields, hasF := p.Get("f")
	if !hasF {
		fields, hasF = p.Get("fields")
	}
	if hasF {
		q.Filters = nil
		ops := p.Map("op")
		if ops == nil {
			ops = p.Map("operators")
		}
		vals := p.Map("v")
		if vals == nil {
			vals = p.Map("values")
		}
		if fs := anySlice(fields); len(fs) > 0 && ops != nil {
			for _, f := range fs {
				field := httpx.ValueString(f)
				op := ops.String(field)
				var values []any
				if vals != nil {
					if v, ok := vals.Get(field); ok {
						arr, isArr := v.([]any)
						if !isArr {
							// values must be an array
							continue
						}
						values = make([]any, len(arr))
						for i, e := range arr {
							values[i] = httpx.ValueString(e)
						}
					}
				}
				q.addFilter(field, op, values)
			}
		}
	} else {
		for _, d := range q.defs {
			if v, ok := p.Get(d.Field); ok && v != nil {
				q.addShortFilter(d.Field, httpx.ValueString(v))
			}
		}
	}
	if c, ok := p.Get("c"); ok {
		q.setColumnNames(stringsOf(anySlice(c)))
	} else if c, ok := p.Get("column_names"); ok {
		q.setColumnNames(stringsOf(anySlice(c)))
	}
	if t, ok := p.Get("t"); ok {
		q.setTotalable(stringsOf(anySlice(t)))
	} else if t, ok := p.Get("totalable_names"); ok {
		q.setTotalable(stringsOf(anySlice(t)))
	}
	if s := p.String("sort"); s != "" {
		q.setSort(s)
	}
}

func stringsOf(xs []any) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = httpx.ValueString(x)
	}
	return out
}

// setColumnNames は Query#column_names=。
func (q *userQuery) setColumnNames(names []string) {
	var out []string
	for _, n := range names {
		if n == "" {
			continue
		}
		if n == "all_inline" {
			for _, c := range q.cols {
				if !contains(out, c.Name) {
					out = append(out, c.Name)
				}
			}
			continue
		}
		if !contains(out, n) {
			out = append(out, n)
		}
	}
	if equalStrings(out, userQueryDefaultColumns) {
		out = nil
	}
	q.ColumnNames = out
}

func (q *userQuery) setTotalable(names []string) {
	var out []string
	for _, n := range names {
		if n != "" {
			out = append(out, n)
		}
	}
	q.Totalable = out
}

// setSort は sort_criteria=（Redmine::SortCriteria の正規化: 空キー除去・重複除去・最大 3 つ）。
func (q *userQuery) setSort(s string) {
	var out [][2]string
	for _, part := range strings.Split(s, ",") {
		kv := strings.SplitN(part, ":", 3)
		if strings.TrimSpace(kv[0]) == "" {
			continue
		}
		dup := false
		for _, o := range out {
			if o[0] == kv[0] {
				dup = true
			}
		}
		if dup {
			continue
		}
		order := "asc"
		if len(kv) > 1 && kv[1] == "desc" {
			order = "desc"
		}
		out = append(out, [2]string{kv[0], order})
	}
	if len(out) > 3 {
		out = out[:3]
	}
	q.Sort = out
}

// sortCriteria は sort_criteria（空なら default_sort_criteria = [['login', 'asc']]）。
func (q *userQuery) sortCriteria() [][2]string {
	if len(q.Sort) == 0 {
		return [][2]string{{"login", "asc"}}
	}
	return q.Sort
}

// sortParam は sort_criteria.to_param。
func sortParam(sc [][2]string) string {
	var parts []string
	for _, s := range sc {
		if s[1] == "desc" {
			parts = append(parts, s[0]+":desc")
		} else {
			parts = append(parts, s[0])
		}
	}
	return strings.Join(parts, ",")
}

// addSort は sort_criteria.add(key, order)（先頭に追加して正規化）。
func addSort(sc [][2]string, key, order string) [][2]string {
	if order != "desc" {
		order = "asc"
	}
	out := [][2]string{{key, order}}
	for _, s := range sc {
		if s[0] != key {
			out = append(out, s)
		}
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// column は名前の列定義。
func (q *userQuery) column(name string) *userQueryColumn {
	for i := range q.cols {
		if q.cols[i].Name == name {
			return &q.cols[i]
		}
	}
	return nil
}

// columns は Query#columns（column_names の順。既定なら default_columns_names）。
func (q *userQuery) columns() []userQueryColumn {
	names := q.ColumnNames
	if len(names) == 0 {
		names = userQueryDefaultColumns
	}
	var out []userQueryColumn
	for _, n := range names {
		if c := q.column(n); c != nil {
			out = append(out, *c)
		}
	}
	return out
}

// availableColumnsNotSelected は query_available_inline_columns_options の対象（available - columns）。
func (q *userQuery) availableColumnsNotSelected() []userQueryColumn {
	sel := map[string]bool{}
	for _, c := range q.columns() {
		sel[c.Name] = true
	}
	var out []userQueryColumn
	for _, c := range q.cols {
		if !sel[c.Name] {
			out = append(out, c)
		}
	}
	return out
}

// totalableColumns は Query#totalable_columns。
func (q *userQuery) totalableColumns() []userQueryColumn {
	var out []userQueryColumn
	for _, c := range q.cols {
		if c.Totalable && contains(q.Totalable, c.Name) {
			out = append(out, c)
		}
	}
	return out
}

// repositoryFilter は filters を repository.UserFilter に変換する。
func (q *userQuery) repositoryFilter() repository.UserFilter {
	var f repository.UserFilter
	for _, flt := range q.Filters {
		f.Conditions = append(f.Conditions, repository.UserCondition{Field: flt.Field, Operator: flt.Operator, Values: stringsOf(flt.Values)})
	}
	return f
}

// repositorySort は sort_clause（並べ替え不可の列は無視）。
func (q *userQuery) repositorySort() []repository.SortCriterion {
	var out []repository.SortCriterion
	for _, s := range q.sortCriteria() {
		if c := q.column(s[0]); c != nil && c.Sortable != "" {
			out = append(out, repository.SortCriterion{Column: c.Sortable, Desc: s[1] == "desc"})
		}
	}
	return out
}

// valid は Query#valid?（値が必要な演算子で値が空ならエラー）。エラーメッセージの属性名と値を返す。
func (q *userQuery) validationErrors() [][2]string {
	var errs [][2]string
	for _, f := range q.Filters {
		switch f.Operator {
		case "o", "c", "!*", "*", "t", "ld", "w", "lw", "l2w", "m", "lm", "y", "nd", "nw", "nm", "*o", "!o", "!p":
			continue
		}
		blank := len(f.Values) == 0
		if !blank {
			blank = true
			for _, v := range f.Values {
				if strings.TrimSpace(httpx.ValueString(v)) != "" {
					blank = false
				}
			}
		}
		if blank {
			errs = append(errs, [2]string{f.Field, "blank"})
		}
	}
	return errs
}

// sessionData は session[:user_query]。
type userQuerySession struct {
	Filters     []queryFilter `json:"filters"`
	ColumnNames []string      `json:"column_names"`
	Totalable   []string      `json:"totalable_names"`
	Sort        [][2]string   `json:"sort"`
}

const userQuerySessionKey = "user_query"

// retrieveUserQuery は QueriesHelper#retrieve_query(UserQuery, use_session)。
func (a *App) retrieveUserQuery(c *Req, useSession bool) *userQuery {
	twofa := a.Settings.String("twofa") != "0"
	q := newUserQuery(twofa)
	p := c.Params()
	s := c.Session()
	var stored *userQuerySession
	if useSession && s != nil {
		if raw := s.GetString(userQuerySessionKey); raw != "" {
			var d userQuerySession
			if json.Unmarshal([]byte(raw), &d) == nil {
				stored = &d
			}
		}
	}
	if httpx.IsAPIRequest(c.R) || p.Has("set_filter") || !useSession || stored == nil {
		q.buildFromParams(p)
		if useSession && s != nil {
			a.storeUserQuery(s, q)
		}
	} else {
		q.Filters = stored.Filters
		q.ColumnNames = stored.ColumnNames
		q.Totalable = stored.Totalable
		q.Sort = stored.Sort
		// JSON の数値は float64 になるため整数に戻す
		for i := range q.Filters {
			for j, v := range q.Filters[i].Values {
				if f, ok := v.(float64); ok {
					q.Filters[i].Values[j] = int(f)
				}
			}
		}
	}
	if sp := p.String("sort"); sp != "" {
		q.setSort(sp)
		if useSession && s != nil {
			a.storeUserQuery(s, q)
		}
	}
	return q
}

func (a *App) storeUserQuery(s *httpx.Session, q *userQuery) {
	b, _ := json.Marshal(userQuerySession{Filters: q.Filters, ColumnNames: q.ColumnNames, Totalable: q.Totalable, Sort: q.Sort})
	s.Set(userQuerySessionKey, string(b))
}

// ---------------------------------------------------------------- ビュー用

// userQueryView はテンプレートに渡す UserQuery の表示用データ。
type userQueryView struct {
	q   *userQuery
	c   *Req
	app *App
	// groups は is_member_of_group の値（Group.givable.visible.pluck(:name, :id)）。
	groups      []*domain.Group
	authSources []domain.AuthSource
}

// Columns は inline_columns。
func (v *userQueryView) Columns() []userQueryColumn { return v.q.columns() }

// ColumnCaption は column.caption。
func (v *userQueryView) ColumnCaption(col userQueryColumn) string {
	if col.CaptionText != "" {
		return col.CaptionText
	}
	return v.c.L(col.Caption)
}

// SortParam は sort_criteria.to_param。
func (v *userQueryView) SortParam() string { return sortParam(v.q.sortCriteria()) }

// ColumnHeader は QueriesHelper#column_header。
func (v *userQueryView) ColumnHeader(col userQueryColumn) rails.HTML {
	caption := v.ColumnCaption(col)
	var content any = caption
	if col.Sortable != "" {
		sc := v.q.sortCriteria()
		var css any
		order := ""
		icon := ""
		if len(sc) > 0 && sc[0][0] == col.Name {
			if sc[0][1] == "asc" {
				css, icon, order = "sort asc icon icon-sorted-desc", "angle-up", "desc"
			} else {
				css, icon, order = "sort desc icon icon-sorted-asc", "angle-down", "asc"
			}
		}
		qp := pageQueryParameters(v.c)
		qp.Set("sort", sortParam(addSort(sc, col.Name, order)))
		var label any = caption
		if icon != "" {
			label = v.app.Helpers.SpriteIcon(v.c.Page(), icon, caption, nil)
		}
		content = rails.LinkTo(label, urlWithQuery(v.c.R.URL.Path, qp),
			rails.NewHash("title", v.c.L("label_sort_by", "\""+caption+"\""), "class", css))
	}
	return rails.ContentTag("th", content, rails.NewHash("class", col.Name))
}

// FilterJSON は operatorLabels / operatorByType / availableFilters などの JS 変数。
func (v *userQueryView) OperatorLabelsJSON() rails.HTML {
	var b strings.Builder
	b.WriteByte('{')
	for i, o := range queryOperatorKeys {
		if i > 0 {
			b.WriteByte(',')
		}
		label := v.c.L(o.Label)
		if o.Op == "l2w" {
			label = v.c.L(o.Label, map[string]any{"count": 2})
		}
		b.WriteString(rawJSON(o.Op) + ":" + rawJSON(label))
	}
	b.WriteByte('}')
	return rails.HTML(b.String())
}

// OperatorByTypeJSON は Query.operators_by_filter_type の JSON。
func (v *userQueryView) OperatorByTypeJSON() rails.HTML {
	var b strings.Builder
	b.WriteByte('{')
	for i, t := range queryOperatorsByType {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(rawJSON(t.Type) + ":" + rawJSON(t.Ops))
	}
	b.WriteByte('}')
	return rails.HTML(b.String())
}

// filterValues は filter.values（[[label, value], ...]。nil = 値なし）。
func (v *userQueryView) filterValues(field string) [][2]string {
	switch field {
	case "status":
		return [][2]string{{v.c.L("status_active"), "1"}, {v.c.L("status_registered"), "2"}, {v.c.L("status_locked"), "3"}}
	case "auth_source_id":
		out := [][2]string{}
		srcs := append([]domain.AuthSource(nil), v.authSources...)
		sort.SliceStable(srcs, func(i, j int) bool { return srcs[i].Name < srcs[j].Name })
		for _, s := range srcs {
			out = append(out, [2]string{s.Name, itoa(s.ID)})
		}
		return out
	case "is_member_of_group":
		out := [][2]string{}
		for _, g := range v.groups {
			out = append(out, [2]string{g.Name, itoa(g.ID)})
		}
		return out
	case "twofa_scheme":
		return [][2]string{{v.c.L("twofa__totp__name"), "totp"}}
	case "admin":
		return [][2]string{{v.c.L("general_text_yes"), "1"}, {v.c.L("general_text_no"), "0"}}
	}
	return nil
}

// AvailableFiltersJSON は query.available_filters_as_json。
func (v *userQueryView) AvailableFiltersJSON() rails.HTML {
	var b strings.Builder
	b.WriteByte('{')
	for i, d := range v.q.defs {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(rawJSON(d.Field) + `:{"type":` + rawJSON(d.Type) + `,"name":` + rawJSON(v.c.L(d.Label)))
		if d.Remote {
			b.WriteString(`,"remote":true`)
		}
		if v.q.filter(d.Field) != nil || !d.Remote {
			vals := v.filterValues(d.Field)
			if vals == nil {
				b.WriteString(`,"values":null`)
			} else {
				b.WriteString(`,"values":` + rawJSON(vals))
			}
		}
		b.WriteByte('}')
	}
	b.WriteByte('}')
	return rails.HTML(b.String())
}

// Totals は render_query_totals(query)。
// TODO(query): 合計列（数値のカスタムフィールド）の合計表示。
func (v *userQueryView) Totals() rails.HTML { return "" }

// AddFilterCalls は addFilter("field", operator, values); の行。
func (v *userQueryView) Filters() []queryFilter { return v.q.Filters }

// RawJSON は raw_json(arg)。
func (v *userQueryView) RawJSON(x any) rails.HTML { return rails.HTML(rawJSON(x)) }

// FilterOptions は filters_options_for_select(query)。
func (v *userQueryView) FilterOptions() rails.HTML {
	var ungrouped [][2]string
	type group struct {
		label string
		items [][2]string
	}
	groups := []*group{{label: "label_string"}, {label: "label_date"}}
	for _, d := range v.q.defs {
		item := [2]string{v.c.L(d.Label), d.Field}
		switch d.Type {
		case "date_past", "date":
			groups[1].items = append(groups[1].items, item)
		case "string", "text", "search":
			groups[0].items = append(groups[0].items, item)
		default:
			ungrouped = append(ungrouped, item)
		}
	}
	if len(groups[1].items) == 1 {
		ungrouped = append(ungrouped, groups[1].items[0])
		groups[1].items = nil
	}
	opts := []any{[]any{}}
	for _, u := range ungrouped {
		opts = append(opts, []any{u[0], u[1]})
	}
	s := rails.OptionsForSelect(opts, nil)
	var grouped []any
	for _, g := range groups {
		if len(g.items) == 0 {
			continue
		}
		var items []any
		for _, it := range g.items {
			items = append(items, []any{it[0], it[1]})
		}
		grouped = append(grouped, []any{v.c.L(g.label), items})
	}
	if len(grouped) > 0 {
		s += rails.GroupedOptionsForSelect(grouped, nil, nil)
	}
	return s
}

// AvailableColumnsOptions は query_available_inline_columns_options。
func (v *userQueryView) AvailableColumnsOptions() rails.HTML {
	var opts []any
	for _, c := range v.q.availableColumnsNotSelected() {
		opts = append(opts, []any{v.ColumnCaption(c), c.Name})
	}
	return rails.OptionsForSelect(opts, nil)
}

// SelectedColumnsOptions は query_selected_inline_columns_options。
func (v *userQueryView) SelectedColumnsOptions() rails.HTML {
	var opts []any
	for _, c := range v.q.columns() {
		opts = append(opts, []any{v.ColumnCaption(c), c.Name})
	}
	return rails.OptionsForSelect(opts, nil)
}

// TotalableColumns は available_totalable_columns。
func (v *userQueryView) TotalableColumns() []userQueryColumn {
	var out []userQueryColumn
	for _, c := range v.q.cols {
		if c.Totalable {
			out = append(out, c)
		}
	}
	return out
}

// IsTotalable は query.totalable_columns.include?(column)。
func (v *userQueryView) IsTotalable(col userQueryColumn) bool {
	return contains(v.q.Totalable, col.Name)
}

// HiddenFieldTags は query_as_hidden_field_tags(query)。
func (v *userQueryView) HiddenFieldTags() rails.HTML {
	h := func(name string, value any) rails.HTML {
		return rails.HiddenFieldTag(name, value, rails.NewHash("id", nil))
	}
	tags := h("set_filter", "1")
	if len(v.q.Filters) > 0 {
		for _, f := range v.q.Filters {
			tags += h("f[]", f.Field)
			tags += h("op["+f.Field+"]", f.Operator)
			for _, val := range f.Values {
				tags += h("v["+f.Field+"][]", val)
			}
		}
	} else {
		tags += h("f[]", "")
	}
	for _, c := range v.q.columns() {
		tags += h("c[]", c.Name)
	}
	for _, t := range v.q.Totalable {
		tags += h("t[]", t)
	}
	if sc := v.q.sortCriteria(); len(sc) > 0 {
		tags += h("sort", sortParam(sc))
	}
	return tags
}

// rawJSON は raw_json（to_json の "/" を "\/" にする）。
func rawJSON(x any) string {
	return strings.ReplaceAll(rails.ToJSON(x), "/", `\/`)
}

// pageQueryParameters は request.query_parameters。
func pageQueryParameters(c *Req) *httpx.Params {
	qp, err := httpx.ParseRailsQuery(c.R.URL.RawQuery)
	if err != nil || qp == nil {
		return httpx.NewParams()
	}
	return qp
}

// urlWithQuery は path にクエリを付ける（Hash#to_query）。
func urlWithQuery(path string, params any) string {
	return helper.URLWithQuery(path, params)
}

var _ = url.QueryEscape
