package query

import (
	"regexp"
	"slices"
	"strings"
)

func mustRe(s string) *regexp.Regexp { return regexp.MustCompile(s) }

// Operator は Query.operators の 1 項目 (演算子とラベルの i18n キー)。
type Operator struct {
	Op       string
	LabelKey string
	// LabelArgs はラベルの補間引数 (l2w の count: 2)。
	LabelArgs map[string]any
}

// Operators は Query.operators (定義順)。
var Operators = []Operator{
	{Op: "=", LabelKey: "label_equals"},
	{Op: "!", LabelKey: "label_not_equals"},
	{Op: "o", LabelKey: "label_open_issues"},
	{Op: "c", LabelKey: "label_closed_issues"},
	{Op: "!*", LabelKey: "label_none"},
	{Op: "*", LabelKey: "label_any"},
	{Op: ">=", LabelKey: "label_greater_or_equal"},
	{Op: "<=", LabelKey: "label_less_or_equal"},
	{Op: "><", LabelKey: "label_between"},
	{Op: "<t+", LabelKey: "label_in_less_than"},
	{Op: ">t+", LabelKey: "label_in_more_than"},
	{Op: "><t+", LabelKey: "label_in_the_next_days"},
	{Op: "t+", LabelKey: "label_in"},
	{Op: "nd", LabelKey: "label_tomorrow"},
	{Op: "t", LabelKey: "label_today"},
	{Op: "ld", LabelKey: "label_yesterday"},
	{Op: "nw", LabelKey: "label_next_week"},
	{Op: "w", LabelKey: "label_this_week"},
	{Op: "lw", LabelKey: "label_last_week"},
	{Op: "l2w", LabelKey: "label_last_n_weeks", LabelArgs: map[string]any{"count": 2}},
	{Op: "nm", LabelKey: "label_next_month"},
	{Op: "m", LabelKey: "label_this_month"},
	{Op: "lm", LabelKey: "label_last_month"},
	{Op: "y", LabelKey: "label_this_year"},
	{Op: ">t-", LabelKey: "label_less_than_ago"},
	{Op: "<t-", LabelKey: "label_more_than_ago"},
	{Op: "><t-", LabelKey: "label_in_the_past_days"},
	{Op: "t-", LabelKey: "label_ago"},
	{Op: "~", LabelKey: "label_contains"},
	{Op: "!~", LabelKey: "label_not_contains"},
	{Op: "*~", LabelKey: "label_contains_any_of"},
	{Op: "^", LabelKey: "label_starts_with"},
	{Op: "$", LabelKey: "label_ends_with"},
	{Op: "=p", LabelKey: "label_any_issues_in_project"},
	{Op: "=!p", LabelKey: "label_any_issues_not_in_project"},
	{Op: "!p", LabelKey: "label_no_issues_in_project"},
	{Op: "*o", LabelKey: "label_any_open_issues"},
	{Op: "!o", LabelKey: "label_no_open_issues"},
	{Op: "ev", LabelKey: "label_has_been"},
	{Op: "!ev", LabelKey: "label_has_never_been"},
	{Op: "cf", LabelKey: "label_changed_from"},
}

// IsOperator は既知の演算子なら true。
func IsOperator(op string) bool {
	return slices.ContainsFunc(Operators, func(o Operator) bool { return o.Op == op })
}

// OperatorsByFilterType は Query.operators_by_filter_type。
var OperatorsByFilterType = map[string][]string{
	"list":                       {"=", "!"},
	"list_with_history":          {"=", "!", "ev", "!ev", "cf"},
	"list_status":                {"o", "=", "!", "ev", "!ev", "cf", "c", "*"},
	"list_optional":              {"=", "!", "!*", "*"},
	"list_optional_with_history": {"=", "!", "ev", "!ev", "cf", "!*", "*"},
	"list_subprojects":           {"*", "!*", "=", "!"},
	"date":                       {"=", ">=", "<=", "><", "<t+", ">t+", "><t+", "t+", "nd", "t", "ld", "nw", "w", "lw", "l2w", "nm", "m", "lm", "y", ">t-", "<t-", "><t-", "t-", "!*", "*"},
	"date_past":                  {"=", ">=", "<=", "><", ">t-", "<t-", "><t-", "t-", "t", "ld", "w", "lw", "l2w", "m", "lm", "y", "!*", "*"},
	"string":                     {"~", "*~", "=", "!~", "!", "^", "$", "!*", "*"},
	"text":                       {"~", "*~", "!~", "^", "$", "!*", "*"},
	"search":                     {"~", "*~", "!~"},
	"integer":                    {"=", ">=", "<=", "><", "!*", "*"},
	"float":                      {"=", ">=", "<=", "><", "!*", "*"},
	"relation":                   {"=", "!", "=p", "=!p", "!p", "*o", "!o", "!*", "*"},
	"tree":                       {"=", "~", "!*", "*"},
}

// OperatorsLabels は Query.operators_labels (演算子 → 翻訳済みラベル)。
func (e *Env) OperatorsLabels() map[string]string {
	out := make(map[string]string, len(Operators))
	for _, o := range Operators {
		if o.LabelArgs != nil {
			out[o.Op] = e.l(o.LabelKey, o.LabelArgs)
		} else {
			out[o.Op] = e.l(o.LabelKey)
		}
	}
	return out
}

// SortCriteria は Redmine::SortCriteria ([[key, "asc"|"desc"], ...]、最大 3 件)。
type SortCriteria [][2]string

// ParseSortCriteria は "priority:desc,id" 形式を解釈する。
func ParseSortCriteria(s string) SortCriteria {
	var c SortCriteria
	for _, part := range strings.Split(s, ",") {
		kv := strings.Split(part, ":")
		key := kv[0]
		order := ""
		if len(kv) > 1 {
			order = kv[1]
		}
		c = append(c, [2]string{key, order})
	}
	return c.normalize()
}

// normalize は空キーを除き、キー重複を除き、順序を asc/desc に正規化して先頭 3 件にする。
func (c SortCriteria) normalize() SortCriteria {
	out := SortCriteria{}
	seen := map[string]bool{}
	for _, s := range c {
		if strings.TrimSpace(s[0]) == "" || seen[s[0]] {
			continue
		}
		seen[s[0]] = true
		o := "asc"
		if s[1] == "desc" {
			o = "desc"
		}
		out = append(out, [2]string{s[0], o})
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

// ToParam は to_param ("priority:desc,id")。
func (c SortCriteria) ToParam() string {
	parts := make([]string, len(c))
	for i, s := range c {
		parts[i] = s[0]
		if s[1] == "desc" {
			parts[i] += ":desc"
		}
	}
	return strings.Join(parts, ",")
}

// Add は add(key, order): key を先頭に移す。
func (c SortCriteria) Add(key, order string) SortCriteria {
	out := SortCriteria{{key, order}}
	for _, s := range c {
		if s[0] != key {
			out = append(out, s)
		}
	}
	return out.normalize()
}

// FirstKey は first_key。
func (c SortCriteria) FirstKey() string {
	if len(c) == 0 {
		return ""
	}
	return c[0][0]
}

// FirstAsc は first_asc?。
func (c SortCriteria) FirstAsc() bool { return len(c) > 0 && c[0][1] == "asc" }

// OrderFor は order_for(key) (無ければ "")。
func (c SortCriteria) OrderFor(key string) string {
	for _, s := range c {
		if s[0] == key {
			return s[1]
		}
	}
	return ""
}

var reFixedOrder = regexp.MustCompile(`(?i) (asc|desc)$`)

// sortClause は sort_clause(sortable_columns)。
func (c SortCriteria) sortClause(sortable map[string][]string) []string {
	var sql []string
	for _, s := range c {
		cols, ok := sortable[s[0]]
		if !ok {
			continue
		}
		for _, col := range cols {
			sql = append(sql, appendOrder(col, s[1]))
		}
	}
	return sql
}

// appendOrder は固定順序を持たない式に ASC/DESC を付ける。
func appendOrder(criterion, order string) string {
	if reFixedOrder.MatchString(criterion) {
		return criterion
	}
	return criterion + " " + strings.ToUpper(order)
}
