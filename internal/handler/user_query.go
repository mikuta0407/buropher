package handler

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは users#index の UserQuery（internal/query の KindUser）を QueriesHelper の
// retrieve_query で取得し、queries/_query_form・users/_list の描画に必要な値を提供する
// （QueriesHelper#column_header / column_content / filters_options_for_select / query_as_hidden_field_tags）。

const userQuerySessionKey = "user_query"

type defaultStatusKey struct{}

// userQuerySessionData は session[:user_query] に保存する内容。
type userQuerySessionData struct {
	State      *query.SessionState `json:"state"`
	HasFilters bool                `json:"has_filters"`
}

// retrieveUserQuery は retrieve_query(UserQuery, use_session)。
func (a *App) retrieveUserQuery(c *Req, useSession bool) (*query.Query, *query.Env, error) {
	env, err := query.NewEnv(c.Ctx(), a.DB, c.User, a.Settings)
	if err != nil {
		return nil, nil, err
	}
	env.L = c.Loc
	env.Now = a.now
	var sess *query.SessionState
	s := c.Session()
	if useSession && s != nil {
		if raw := s.GetString(userQuerySessionKey); raw != "" {
			var w userQuerySessionData
			if json.Unmarshal([]byte(raw), &w) == nil && w.State != nil {
				sess = w.State
				// SessionState の filters は omitempty のため、空のフィルタを明示的に戻す
				if w.HasFilters && sess.Filters == nil {
					sess.Filters = []query.SessionFilter{}
				}
			}
		}
	}
	p := query.ParseParams(requestValues(c))
	api := httpx.IsAPIRequest(c.R)
	q, newSess, err := query.Retrieve(c.Ctx(), env, query.KindUser, nil, p, sess, query.RetrieveOptions{UseSession: useSession, API: api})
	if err != nil {
		return nil, nil, err
	}
	// 既定フィルタ（status = [1]）は Redmine では整数の 1 を保持する（JS の addFilter に [1] と出る）
	fresh := api || p.SetFilter || !useSession || sess == nil
	defaultStatus := false
	if fresh {
		defaultStatus = !p.HasFields && p.Short["status"] == ""
	} else if sess.Extra != nil {
		defaultStatus = sess.Extra["default_status"] == "1"
	}
	if useSession && s != nil && newSess != nil {
		if newSess.Extra == nil {
			newSess.Extra = map[string]string{}
		}
		if defaultStatus {
			newSess.Extra["default_status"] = "1"
		} else {
			delete(newSess.Extra, "default_status")
		}
		b, _ := json.Marshal(userQuerySessionData{State: newSess, HasFilters: newSess.Filters != nil})
		s.Set(userQuerySessionKey, string(b))
	}
	if defaultStatus {
		c.setValue(defaultStatusKey{}, true)
	}
	return q, env, nil
}

// requestValues は params（クエリと本文）を url.Values にする（query.ParseParams 用）。
func requestValues(c *Req) url.Values {
	v := url.Values{}
	for k, vals := range c.R.URL.Query() {
		v[k] = append(v[k], vals...)
	}
	if c.R.PostForm != nil {
		for k, vals := range c.R.PostForm {
			v[k] = append(v[k], vals...)
		}
	}
	return v
}

// userQueryView はテンプレートに渡す UserQuery の表示用データ。
type userQueryView struct {
	q   *query.Query
	env *query.Env
	c   *Req
	app *App
	// cvs はカスタムフィールド列の値（user id → 値）。
	cvs map[int64][]principalCustomValue
	// srcs は auth_source.name 列のための認証方式名。
	srcs map[int64]string
	// csv は CSV 出力（csv_value）用の値にする。
	csv bool
}

func (v *userQueryView) inlineColumns() []*query.Column {
	cols, err := v.q.InlineColumns(v.c.Ctx())
	if err != nil {
		v.app.logger().Error("query columns", "err", err)
	}
	return cols
}

// Columns は inline_columns。
func (v *userQueryView) Columns() []*query.Column { return v.inlineColumns() }

// ColumnCaption は column.caption。
func (v *userQueryView) ColumnCaption(col *query.Column) string { return col.CaptionText(v.env) }

// SortParam は sort_criteria.to_param。
func (v *userQueryView) SortParam() string { return v.q.SortCriteria().ToParam() }

// ColumnHeader は QueriesHelper#column_header。
func (v *userQueryView) ColumnHeader(col *query.Column) rails.HTML {
	caption := v.ColumnCaption(col)
	var content any = caption
	if col.IsSortable() {
		sc := v.q.SortCriteria()
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
		qp := pageQueryParameters(v.c)
		qp.Set("sort", sc.Add(col.Name, order).ToParam())
		var label any = caption
		if icon != "" {
			label = v.app.Helpers.SpriteIcon(v.c.Page(), icon, caption, nil)
		}
		content = rails.LinkTo(label, helper.URLWithQuery(v.c.R.URL.Path, qp),
			rails.NewHash("title", v.c.L("label_sort_by", "\""+caption+"\""), "class", css))
	}
	return rails.ContentTag("th", content, rails.NewHash("class", col.CSSClasses()))
}

// OperatorLabelsJSON は raw_json Query.operators_labels。
func (v *userQueryView) OperatorLabelsJSON() rails.HTML {
	labels := v.env.OperatorsLabels()
	var b strings.Builder
	b.WriteByte('{')
	for i, o := range query.Operators {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(rawJSON(o.Op) + ":" + rawJSON(labels[o.Op]))
	}
	b.WriteByte('}')
	return rails.HTML(b.String())
}

// operatorTypeOrder は Query.operators_by_filter_type の定義順。
var operatorTypeOrder = []string{"list", "list_with_history", "list_status", "list_optional", "list_optional_with_history",
	"list_subprojects", "date", "date_past", "string", "text", "search", "integer", "float", "relation", "tree"}

// OperatorByTypeJSON は raw_json Query.operators_by_filter_type。
func (v *userQueryView) OperatorByTypeJSON() rails.HTML {
	var b strings.Builder
	b.WriteByte('{')
	for i, t := range operatorTypeOrder {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(rawJSON(t) + ":" + rawJSON(query.OperatorsByFilterType[t]))
	}
	b.WriteByte('}')
	return rails.HTML(b.String())
}

// AvailableFiltersJSON は raw_json query.available_filters_as_json。
func (v *userQueryView) AvailableFiltersJSON() rails.HTML {
	ctx := v.c.Ctx()
	af, err := v.q.AvailableFilters(ctx)
	if err != nil {
		v.app.logger().Error("available filters", "err", err)
		return "{}"
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, d := range af.Defs() {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(rawJSON(d.Field) + `:{"type":` + rawJSON(d.Type) + `,"name":` + rawJSON(d.Name))
		if d.Remote {
			b.WriteString(`,"remote":true`)
		}
		if v.q.HasFilter(d.Field) || !d.Remote {
			if !d.HasValues() {
				b.WriteString(`,"values":null`)
			} else {
				opts, err := d.LoadValues(ctx)
				if err != nil {
					v.app.logger().Error("filter values", "err", err)
				}
				vals := make([][]string, len(opts))
				for j, o := range opts {
					if o.Group != "" {
						vals[j] = []string{o.Label, o.Value, o.Group}
					} else {
						vals[j] = []string{o.Label, o.Value}
					}
				}
				b.WriteString(`,"values":` + rawJSON(vals))
			}
		}
		b.WriteByte('}')
	}
	b.WriteByte('}')
	return rails.HTML(b.String())
}

// queryFilterView は addFilter の引数。
type queryFilterView struct {
	Field    string
	Operator string
	Values   any
}

// Filters は query.filters（既定の status フィルタは整数の値）。
func (v *userQueryView) Filters() []queryFilterView {
	def, _ := v.c.value(defaultStatusKey{}).(bool)
	var out []queryFilterView
	for _, k := range v.q.Filters.Keys() {
		f, _ := v.q.Filters.Get(k)
		var vals any = f.Values
		if f.Values == nil {
			vals = []string{}
		}
		if def && k == "status" && f.Operator == "=" && len(f.Values) == 1 && f.Values[0] == "1" {
			vals = []int{domain.StatusActive}
		}
		out = append(out, queryFilterView{Field: k, Operator: f.Operator, Values: vals})
	}
	return out
}

// RawJSON は raw_json(arg)。
func (v *userQueryView) RawJSON(x any) rails.HTML { return rails.HTML(rawJSON(x)) }

// FilterOptions は filters_options_for_select(query)。
func (v *userQueryView) FilterOptions() rails.HTML {
	af, err := v.q.AvailableFilters(v.c.Ctx())
	if err != nil {
		return ""
	}
	var ungrouped [][2]string
	type group struct {
		label string
		items [][2]string
	}
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
		item := [2]string{d.Name, d.Field}
		g := ""
		switch d.Type {
		case "date_past", "date":
			g = "label_date"
		case "string", "text", "search":
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
	ctx := v.c.Ctx()
	avail, _ := v.q.AvailableInlineColumns(ctx)
	cols, _ := v.q.Columns(ctx)
	sel := map[string]bool{}
	for _, c := range cols {
		sel[c.Name] = true
	}
	var opts []any
	for _, c := range avail {
		if !sel[c.Name] && !c.Frozen {
			opts = append(opts, []any{v.ColumnCaption(c), c.Name})
		}
	}
	return rails.OptionsForSelect(opts, nil)
}

// SelectedColumnsOptions は query_selected_inline_columns_options。
func (v *userQueryView) SelectedColumnsOptions() rails.HTML {
	var opts []any
	for _, c := range v.inlineColumns() {
		if !c.Frozen {
			opts = append(opts, []any{v.ColumnCaption(c), c.Name})
		}
	}
	return rails.OptionsForSelect(opts, nil)
}

// TotalableColumns は available_totalable_columns。
func (v *userQueryView) TotalableColumns() []*query.Column {
	cols, _ := v.q.AvailableTotalableColumns(v.c.Ctx())
	return cols
}

// IsTotalable は query.totalable_columns.include?(column)。
func (v *userQueryView) IsTotalable(col *query.Column) bool {
	return contains(v.q.TotalableNames(), col.Name)
}

// Totals は render_query_totals(query)（合計値は format_object(Float) = "%.2f"）。
func (v *userQueryView) Totals() rails.HTML {
	totals, err := v.q.Totals(v.c.Ctx())
	if err != nil || len(totals) == 0 {
		return ""
	}
	var parts []string
	for _, t := range totals {
		label := rails.ContentTag("span", t.Column.CaptionText(v.env)+":", nil)
		val := rails.ContentTag("span", fmt.Sprintf("%.2f", t.Value), rails.NewHash("class", "value"))
		parts = append(parts, string(rails.ContentTag("span", label+" "+val, rails.NewHash("class", "total-for-"+strings.ReplaceAll(t.Column.Name, "_", "-")))))
	}
	return rails.ContentTag("p", rails.HTML(strings.Join(parts, " ")), rails.NewHash("class", "query-totals"))
}

// HiddenFieldTags は query_as_hidden_field_tags(query)。
func (v *userQueryView) HiddenFieldTags() rails.HTML {
	h := func(name string, value any) rails.HTML {
		return rails.HiddenFieldTag(name, value, rails.NewHash("id", nil))
	}
	tags := h("set_filter", "1")
	if v.q.Filters.Len() > 0 {
		for _, k := range v.q.Filters.Keys() {
			f, _ := v.q.Filters.Get(k)
			tags += h("f[]", k)
			tags += h("op["+k+"]", f.Operator)
			for _, val := range f.Values {
				tags += h("v["+k+"][]", val)
			}
		}
	} else {
		tags += h("f[]", "")
	}
	cols, _ := v.q.Columns(v.c.Ctx())
	for _, c := range cols {
		tags += h("c[]", c.Name)
	}
	for _, t := range v.q.TotalableNames() {
		tags += h("t[]", t)
	}
	if v.q.GroupBy != "" {
		tags += h("group_by", v.q.GroupBy)
	}
	if sc := v.q.SortCriteria(); len(sc) > 0 {
		tags += h("sort", sc.ToParam())
	}
	return tags
}

// Cell は users/_list のセル（login は編集画面へのリンク）。
func (v *userQueryView) Cell(col *query.Column, u *domain.User) rails.HTML {
	if col.Name == "login" {
		return rails.ContentTag("td", rails.LinkTo(u.Login, "/users/"+itoa(u.ID)+"/edit", nil), rails.NewHash("class", col.CSSClasses()))
	}
	return rails.ContentTag("td", v.Value(col, u), rails.NewHash("class", col.CSSClasses()))
}

// Value は column_content / csv_content の値（format_object）。
func (v *userQueryView) Value(col *query.Column, u *domain.User) string {
	c := v.c
	switch col.Name {
	case "login":
		return u.Login
	case "firstname":
		return u.Firstname
	case "lastname":
		return u.Lastname
	case "mail":
		return u.Mail
	case "admin":
		if u.AdminFlag {
			return c.L("general_text_Yes")
		}
		return c.L("general_text_No")
	case "created_on":
		return c.Loc.FormatTime(u.CreatedAt, true)
	case "updated_on":
		return c.Loc.FormatTime(u.UpdatedAt, true)
	case "last_login_on":
		if u.LastLoginAt == nil {
			return ""
		}
		return c.Loc.FormatTime(*u.LastLoginAt, true)
	case "passwd_changed_on":
		if u.PasswordChangedAt == nil {
			return ""
		}
		return c.Loc.FormatTime(*u.PasswordChangedAt, true)
	case "status":
		// UserQueriesHelper#user_status_label
		switch u.Status {
		case domain.StatusActive:
			return c.L("status_active")
		case domain.StatusRegistered:
			return c.L("status_registered")
		case domain.StatusLocked:
			return c.L("status_locked")
		}
		return ""
	case "twofa_scheme":
		if v.csv {
			// UserQueriesHelper#twofa_scheme_label
			if u.TwofaScheme == "" {
				return c.L("label_disabled")
			}
			return c.L("twofa__" + u.TwofaScheme + "__name")
		}
		return u.TwofaScheme
	case "auth_source.name":
		if u.AuthSourceID == nil {
			return ""
		}
		return v.srcs[*u.AuthSourceID]
	}
	if col.CustomField != nil {
		for _, cv := range v.cvs[u.ID] {
			if cv.Field.ID == col.CustomField.ID {
				return cv.ValueString()
			}
		}
	}
	return ""
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

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// anySlice は配列パラメータを []any にする（スカラーは 1 要素）。
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
