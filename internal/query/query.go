// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package query は Redmine のクエリシステム (app/models/query.rb, issue_query.rb,
// time_entry_query.rb, project_query.rb, project_admin_query.rb, user_query.rb) を移植する。
//
// Query は保存クエリ (queries 行) またはリクエストパラメータから組み立てた一時クエリで、
// フィルタ (演算子と値)・列・ソート・グループ・合計列・表示形式を持つ。評価は Env
// (User.current / Setting / 時刻) に依存するため、Query は生成時に Env を束縛する。
//
//	env, _ := query.NewEnv(ctx, d, user, st)
//	q, _ := query.New(ctx, env, query.KindIssue, project)
//	q.BuildFromParams(ctx, params)
//	ids, _ := q.IssueIDs(ctx, query.ListOptions{Limit: 25})
//
// SQL は Redmine と同じ意味になるよう組み立てるが、lft/rgt は閉包テーブル (projects) /
// root_id + hier_path (issues) で表し、利用者由来の値はプレースホルダで渡す。
package query

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

// Kind は queries.kind (旧 type の STI)。
type Kind string

const (
	KindIssue        Kind = "issue"         // IssueQuery
	KindTimeEntry    Kind = "time_entry"    // TimeEntryQuery
	KindProject      Kind = "project"       // ProjectQuery
	KindProjectAdmin Kind = "project_admin" // ProjectAdminQuery
	KindUser         Kind = "user"          // UserQuery
)

// 可視性 (Query::VISIBILITY_*)。
const (
	VisibilityPrivate = 0
	VisibilityRoles   = 1
	VisibilityPublic  = 2
)

// Filter は 1 フィールドのフィルタ (filters[field] = {:operator, :values})。
type Filter struct {
	Operator string   `json:"operator"`
	Values   []string `json:"values"`
}

// Filters は挿入順を保つフィルタの集合 (Ruby の Hash と同じ順序で評価・表示する)。
type Filters struct {
	keys []string
	m    map[string]Filter
}

// NewFilters は空のフィルタ集合を返す。
func NewFilters() *Filters { return &Filters{m: map[string]Filter{}} }

// Set はフィルタを設定する (既存キーは位置を保って上書き)。
func (f *Filters) Set(field string, flt Filter) {
	if _, ok := f.m[field]; !ok {
		f.keys = append(f.keys, field)
	}
	f.m[field] = flt
}

// Get はフィルタを返す。
func (f *Filters) Get(field string) (Filter, bool) {
	if f == nil {
		return Filter{}, false
	}
	flt, ok := f.m[field]
	return flt, ok
}

// Has は has_filter?(field)。
func (f *Filters) Has(field string) bool {
	_, ok := f.Get(field)
	return ok
}

// Delete はフィルタを削除する。
func (f *Filters) Delete(field string) {
	if _, ok := f.m[field]; !ok {
		return
	}
	delete(f.m, field)
	f.keys = slices.DeleteFunc(f.keys, func(k string) bool { return k == field })
}

// Keys はフィールド名を挿入順で返す。
func (f *Filters) Keys() []string {
	if f == nil {
		return nil
	}
	return slices.Clone(f.keys)
}

// Len はフィルタ数。
func (f *Filters) Len() int {
	if f == nil {
		return 0
	}
	return len(f.keys)
}

// Clone は複製を返す。
func (f *Filters) Clone() *Filters {
	c := NewFilters()
	for _, k := range f.Keys() {
		v := f.m[k]
		c.Set(k, Filter{Operator: v.Operator, Values: slices.Clone(v.Values)})
	}
	return c
}

// Query は Redmine の Query (と各サブクラス)。
type Query struct {
	ID          int64 // 0 = 未保存 (new_record?)
	Kind        Kind
	Project     *domain.Project // nil = 全プロジェクト
	UserID      *int64          // 作成者 (nil = システム)
	Name        string
	Description string
	Visibility  int
	RoleIDs     []int64
	// Filters はフィルタ。
	Filters *Filters
	// GroupBy はグループ化する列名 ("" = なし)。
	GroupBy string
	// Options は display_type / totalable_names 以外のオプション
	// (draw_relations, draw_progress_line, draw_selected_columns 等。値は文字列)。
	Options map[string]string

	columnNames    []string // nil = 既定列
	sortCriteria   SortCriteria
	totalableNames []string
	totalableSet   bool
	displayType    string

	// savedProjectID は保存済みの project_id (is_global? の判定用)。
	savedProjectID *int64

	env              *Env
	impl             kindImpl
	availableFilters *filterSet
	availableCols    []*Column
	projectLeaf      *bool
	subprojectIDs    []int64
	subprojectsDone  bool
	nestedSetExpr    string
}

// New は Env を束縛した新しいクエリを返す。Redmine の各コントローラと同じく名前は "_"
// (名前が空のクエリは valid? でなくなりフィルタが評価されないため)。
// 既定フィルタ (IssueQuery: status_id = o 等) が設定される。
func New(ctx context.Context, env *Env, kind Kind, project *domain.Project) (*Query, error) {
	impl, err := implFor(kind)
	if err != nil {
		return nil, err
	}
	q := &Query{Kind: kind, Project: project, Name: "_", Options: map[string]string{}, env: env, impl: impl}
	q.Filters = impl.defaultFilters()
	return q, nil
}

func implFor(kind Kind) (kindImpl, error) {
	switch kind {
	case KindIssue:
		return issueKind{}, nil
	case KindTimeEntry:
		return timeEntryKind{}, nil
	case KindProject:
		return projectKind{}, nil
	case KindProjectAdmin:
		return projectKind{admin: true}, nil
	case KindUser:
		return userKind{}, nil
	}
	return nil, fmt.Errorf("query: unknown kind %q", kind)
}

// Env はクエリに束縛された評価環境。
func (q *Query) Env() *Env { return q.env }

// ProjectID は project_id。
func (q *Query) ProjectID() *int64 {
	if q.Project == nil {
		return nil
	}
	id := q.Project.ID
	return &id
}

// ViewPermission はクエリの閲覧に必要な権限 (view_permission)。
func (q *Query) ViewPermission() string { return q.impl.viewPermission() }

// IsPrivate は is_private?。
func (q *Query) IsPrivate() bool { return q.Visibility == VisibilityPrivate }

// IsPublic は is_public? (ロール限定も含む)。
func (q *Query) IsPublic() bool { return !q.IsPrivate() }

// IsGlobal は is_global? (保存済みなら保存時の project_id で判定する)。
func (q *Query) IsGlobal() bool {
	if q.ID == 0 {
		return q.Project == nil
	}
	return q.savedProjectID == nil
}

// invalidate は利用可能フィルタ・列のキャッシュを捨てる (プロジェクトを変えた後など)。
func (q *Query) invalidate() {
	q.availableFilters = nil
	q.availableCols = nil
	q.projectLeaf = nil
	q.subprojectsDone = false
}

// SetProject はプロジェクトを変更する。
func (q *Query) SetProject(p *domain.Project) {
	q.Project = p
	q.invalidate()
}

// ---------------------------------------------------------------- フィルタ値の取得

// HasFilter は has_filter?(field)。
func (q *Query) HasFilter(field string) bool { return q.Filters.Has(field) }

// OperatorFor は operator_for(field) (無ければ "")。
func (q *Query) OperatorFor(field string) string {
	f, _ := q.Filters.Get(field)
	return f.Operator
}

// ValuesFor は values_for(field) (無ければ nil)。
func (q *Query) ValuesFor(field string) []string {
	f, ok := q.Filters.Get(field)
	if !ok {
		return nil
	}
	return f.Values
}

// ValueFor は value_for(field, index)。
func (q *Query) ValueFor(field string, index int) string {
	v := q.ValuesFor(field)
	if index < len(v) {
		return v[index]
	}
	return ""
}

// AddFilter は add_filter: 利用可能なフィルタなら設定する。values が nil なら [""]。
func (q *Query) AddFilter(ctx context.Context, field, operator string, values []string) error {
	af, err := q.AvailableFilters(ctx)
	if err != nil {
		return err
	}
	if !af.Has(field) {
		return nil
	}
	if values == nil {
		values = []string{""}
	}
	q.Filters.Set(field, Filter{Operator: operator, Values: slices.Clone(values)})
	return nil
}

// AddShortFilter は add_short_filter: "o" / ">=2024-01-01" / "1|2" のような短縮表記を解釈する。
// 演算子は型ごとの演算子を逆順 (sort.reverse) に前方一致で探し、値は "|" 区切り。
func (q *Query) AddShortFilter(ctx context.Context, field, expression string) error {
	af, err := q.AvailableFilters(ctx)
	if err != nil {
		return err
	}
	def := af.Get(field)
	if def == nil {
		return nil
	}
	ops := slices.Clone(OperatorsByFilterType[def.Type])
	slices.Sort(ops)
	slices.Reverse(ops)
	for _, op := range ops {
		if !strings.HasPrefix(expression, op) {
			continue
		}
		rest := expression[len(op):]
		// Ruby の /^op(.*)$/ は最初の行だけを見る
		if i := strings.IndexByte(rest, '\n'); i >= 0 {
			rest = rest[:i]
		}
		values := []string{""}
		if strings.TrimSpace(rest) != "" {
			values = rubySplit(rest, "|")
		}
		return q.AddFilter(ctx, field, op, values)
	}
	return q.AddFilter(ctx, field, "=", rubySplit(expression, "|"))
}

// rubySplit は String#split(sep) (末尾の空要素は除く)。
func rubySplit(s, sep string) []string {
	parts := strings.Split(s, sep)
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// TypeFor は type_for(field) (利用可能でなければ "")。
func (q *Query) TypeFor(ctx context.Context, field string) (string, error) {
	af, err := q.AvailableFilters(ctx)
	if err != nil {
		return "", err
	}
	if d := af.Get(field); d != nil {
		return d.Type, nil
	}
	return "", nil
}

// LabelFor は label_for(field)。
func (q *Query) LabelFor(ctx context.Context, field string) (string, error) {
	af, err := q.AvailableFilters(ctx)
	if err != nil {
		return "", err
	}
	if d := af.Get(field); d != nil && d.Name != "" {
		return d.Name, nil
	}
	return humanAttributeName(q.env, field), nil
}

// humanAttributeName は human_attribute_name(field, default: field) の近似
// (field_<name> の訳、無ければ humanize)。
func humanAttributeName(e *Env, field string) string {
	key := "field_" + strings.TrimSuffix(field, "_id")
	if s := e.l(key); !strings.HasPrefix(s, "Translation missing") {
		return s
	}
	return field
}

// ---------------------------------------------------------------- 列・合計・ソート・表示形式

// ColumnNames は column_names (nil = 既定列)。
func (q *Query) ColumnNames() []string { return slices.Clone(q.columnNames) }

// SetColumnNames は column_names=: 空要素を除き、:all_inline を展開し、既定列と同じなら nil にする。
func (q *Query) SetColumnNames(ctx context.Context, names []string) error {
	if names == nil {
		q.columnNames = nil
		return nil
	}
	var ns []string
	for _, n := range names {
		if strings.TrimSpace(n) != "" {
			ns = append(ns, n)
		}
	}
	if i := slices.Index(ns, "all_inline"); i >= 0 {
		ns = slices.DeleteFunc(ns, func(s string) bool { return s == "all_inline" })
		cols, err := q.AvailableColumns(ctx)
		if err != nil {
			return err
		}
		var inline []string
		for _, c := range cols {
			if c.Inline {
				inline = append(inline, c.Name)
			}
		}
		ns = unionStrings(inline, ns)
	}
	if slices.Equal(ns, q.DefaultColumnNames()) {
		ns = nil
	}
	if ns == nil {
		ns = []string{}
	}
	q.columnNames = ns
	return nil
}

// HasDefaultColumns は has_default_columns?。
func (q *Query) HasDefaultColumns() bool { return len(q.columnNames) == 0 }

// DefaultColumnNames は default_columns_names。
func (q *Query) DefaultColumnNames() []string { return q.impl.defaultColumnNames(q) }

// TotalableNames は totalable_names (未設定なら既定値)。
func (q *Query) TotalableNames() []string {
	if q.totalableSet && q.totalableNames != nil {
		return slices.Clone(q.totalableNames)
	}
	return q.impl.defaultTotalableNames(q)
}

// SetTotalableNames は totalable_names= (空要素を除く。nil は未設定 = 既定値)。
func (q *Query) SetTotalableNames(names []string) {
	if names == nil {
		q.totalableNames, q.totalableSet = nil, false
		return
	}
	ns := []string{}
	for _, n := range names {
		if strings.TrimSpace(n) != "" {
			ns = append(ns, n)
		}
	}
	q.totalableNames, q.totalableSet = ns, true
}

// storedTotalableNames は保存用の totalable_names (未設定なら nil)。
func (q *Query) storedTotalableNames() []string {
	if !q.totalableSet {
		return nil
	}
	return q.totalableNames
}

// SortCriteria は sort_criteria (空なら既定のソート)。
func (q *Query) SortCriteria() SortCriteria {
	if len(q.sortCriteria) == 0 {
		return q.impl.defaultSortCriteria().normalize()
	}
	return q.sortCriteria.normalize()
}

// SetSortCriteria は sort_criteria= (正規化して保存)。
func (q *Query) SetSortCriteria(c SortCriteria) { q.sortCriteria = c.normalize() }

// SetSortParam は sort_criteria= に文字列 ("priority:desc,id") を渡した場合。
func (q *Query) SetSortParam(s string) { q.sortCriteria = ParseSortCriteria(s) }

// DisplayType は display_type。
func (q *Query) DisplayType() string {
	if q.Kind == KindProjectAdmin {
		return "list"
	}
	if q.displayType != "" {
		return q.displayType
	}
	return q.impl.defaultDisplayType(q)
}

// SetDisplayType は display_type= (利用できない値は先頭の表示形式)。
func (q *Query) SetDisplayType(t string) {
	types := q.AvailableDisplayTypes()
	if t == "" || !slices.Contains(types, t) {
		t = types[0]
	}
	q.displayType = t
}

// AvailableDisplayTypes は available_display_types。
func (q *Query) AvailableDisplayTypes() []string { return q.impl.availableDisplayTypes(q) }

// CSSClasses は css_classes ("sort-by-<key> sort-<asc|desc>")。
func (q *Query) CSSClasses() string {
	c := q.SortCriteria()
	if len(c) == 0 {
		return ""
	}
	return "sort-by-" + strings.ReplaceAll(c[0][0], "_", "-") + " sort-" + c[0][1]
}

// option は IssueQuery の draw_* オプション。
func (q *Query) option(name string) (string, bool) {
	v, ok := q.Options[name]
	return v, ok
}

// DrawRelations は IssueQuery#draw_relations (未設定または '1' なら true)。
func (q *Query) DrawRelations() bool {
	v, ok := q.option("draw_relations")
	return !ok || v == "1"
}

// SetDrawRelations は draw_relations= ('0' のときだけ '0' を保存)。
func (q *Query) SetDrawRelations(arg string) {
	if arg == "0" {
		q.Options["draw_relations"] = "0"
	} else {
		delete(q.Options, "draw_relations")
	}
}

// DrawProgressLine は draw_progress_line ('1' なら true)。
func (q *Query) DrawProgressLine() bool { v, _ := q.option("draw_progress_line"); return v == "1" }

// SetDrawProgressLine は draw_progress_line=。
func (q *Query) SetDrawProgressLine(arg string) { q.setFlag("draw_progress_line", arg) }

// DrawSelectedColumns は draw_selected_columns ('1' なら true)。
func (q *Query) DrawSelectedColumns() bool {
	v, _ := q.option("draw_selected_columns")
	return v == "1"
}

// SetDrawSelectedColumns は draw_selected_columns=。
func (q *Query) SetDrawSelectedColumns(arg string) { q.setFlag("draw_selected_columns", arg) }

func (q *Query) setFlag(name, arg string) {
	if arg == "1" {
		q.Options[name] = "1"
	} else {
		delete(q.Options, name)
	}
}

// ---------------------------------------------------------------- 検証

var (
	reInteger   = mustRe(`\A[+-]?\d+(,[+-]?\d+)*\z`)
	reFloat     = mustRe(`\A[+-]?\d+(\.\d*)?\z`)
	reDateValue = mustRe(`\A\d{4}-\d{2}-\d{2}(T\d{2}((:)?\d{2}){0,2}(Z|\d{2}:?\d{2})?)?\z`)
	reDays      = mustRe(`^\d+$`)
)

// noValueOperators は値を必要としない演算子。
var noValueOperators = []string{"o", "c", "!*", "*", "nd", "t", "ld", "nw", "w", "lw", "l2w", "nm", "m", "lm", "y", "*o", "!o"}

// Errors は valid? で検出したエラーメッセージ (errors.full_messages)。
func (q *Query) Errors(ctx context.Context) ([]string, error) {
	var errs []string
	add := func(s string) { errs = append(errs, s) }
	msg := func(key string, args ...any) string { return q.env.l("activerecord.errors.messages."+key, args...) }
	if strings.TrimSpace(q.Name) == "" {
		add(q.env.l("field_name") + " " + msg("blank"))
	}
	if len([]rune(q.Name)) > 255 {
		add(q.env.l("field_name") + " " + msg("too_long", map[string]any{"count": 255}))
	}
	if len([]rune(q.Description)) > 255 {
		add(q.env.l("field_description") + " " + msg("too_long", map[string]any{"count": 255}))
	}
	if q.Visibility != VisibilityPublic && q.Visibility != VisibilityRoles && q.Visibility != VisibilityPrivate {
		add(q.env.l("field_visible") + " " + msg("inclusion"))
	}
	fe, err := q.filterErrors(ctx)
	if err != nil {
		return nil, err
	}
	errs = append(errs, fe...)
	if q.Visibility == VisibilityRoles && len(q.RoleIDs) == 0 {
		add(q.env.l("label_role_plural") + " " + msg("blank"))
	}
	if (q.Kind == KindProject || q.Kind == KindProjectAdmin) && q.Project != nil {
		add(q.env.l("field_project") + " " + msg("exclusion"))
	}
	return errs, nil
}

// Valid は valid?。
func (q *Query) Valid(ctx context.Context) (bool, error) {
	errs, err := q.Errors(ctx)
	return len(errs) == 0, err
}

// filterErrors は validate_query_filters。
func (q *Query) filterErrors(ctx context.Context) ([]string, error) {
	var errs []string
	addErr := func(field, key string) error {
		label, err := q.LabelFor(ctx, field)
		if err != nil {
			return err
		}
		errs = append(errs, label+" "+q.env.l("activerecord.errors.messages."+key))
		return nil
	}
	for _, field := range q.Filters.Keys() {
		values := q.ValuesFor(field)
		if values != nil {
			typ, err := q.TypeFor(ctx, field)
			if err != nil {
				return nil, err
			}
			invalid := false
			switch typ {
			case "integer":
				invalid = slices.ContainsFunc(values, func(v string) bool { return strings.TrimSpace(v) != "" && !reInteger.MatchString(v) })
			case "float":
				invalid = slices.ContainsFunc(values, func(v string) bool { return strings.TrimSpace(v) != "" && !reFloat.MatchString(v) })
			case "date", "date_past":
				switch q.OperatorFor(field) {
				case "=", ">=", "<=", "><":
					invalid = slices.ContainsFunc(values, func(v string) bool {
						if strings.TrimSpace(v) == "" {
							return false
						}
						if !reDateValue.MatchString(v) {
							return true
						}
						_, ok := q.parseDate(v)
						return !ok
					})
				case ">t-", "<t-", "t-", ">t+", "<t+", "t+", "><t+", "><t-":
					invalid = slices.ContainsFunc(values, func(v string) bool { return strings.TrimSpace(v) != "" && !reDays.MatchString(v) })
				}
			}
			if invalid {
				if err := addErr(field, "invalid"); err != nil {
					return nil, err
				}
			}
		}
		if !(len(values) > 0 && strings.TrimSpace(values[0]) != "") && !slices.Contains(noValueOperators, q.OperatorFor(field)) {
			if err := addErr(field, "blank"); err != nil {
				return nil, err
			}
		}
	}
	return errs, nil
}

// ---------------------------------------------------------------- プロジェクト関連の補助

// projectIsLeaf は project.leaf?。
func (q *Query) projectIsLeaf(ctx context.Context) (bool, error) {
	if q.Project == nil {
		return true, nil
	}
	if q.projectLeaf == nil {
		leaf, err := repository.IsProjectLeaf(ctx, q.env.Q, q.Project.ID)
		if err != nil {
			return false, err
		}
		q.projectLeaf = &leaf
	}
	return *q.projectLeaf, nil
}

// subprojectIDsNotArchived は project.descendants.where.not(status: ARCHIVED).ids。
func (q *Query) subprojectIDsNotArchived(ctx context.Context) ([]int64, error) {
	if q.Project == nil {
		return nil, nil
	}
	if !q.subprojectsDone {
		var ids []int64
		if err := q.env.Q.Select(ctx, &ids, `SELECT p.id FROM projects p JOIN project_closure pc ON pc.descendant_id = p.id
WHERE pc.ancestor_id = ? AND pc.depth > 0 AND p.status <> ? ORDER BY p.id`, q.Project.ID, domain.ProjectStatusArchived); err != nil {
			return nil, err
		}
		q.subprojectIDs, q.subprojectsDone = ids, true
	}
	return q.subprojectIDs, nil
}

// nestedSet はプロジェクトのツリー順 (lft/rgt 相当)。Env.ProjectNestedSet があればそれを使う。
func (q *Query) nestedSet(ctx context.Context) (map[int64]repository.NestedSetValue, error) {
	if q.env.ProjectNestedSet != nil {
		return q.env.ProjectNestedSet(ctx)
	}
	return repository.ProjectNestedSet(ctx, q.env.Q)
}

// projectsLftExpr は projects.lft 相当の SQL 式 (ツリー順の CASE 式)。
func (q *Query) projectsLftExpr(ctx context.Context, table string) (string, error) {
	if q.nestedSetExpr == "" {
		ns, err := q.nestedSet(ctx)
		if err != nil {
			return "", err
		}
		ids := make([]int64, 0, len(ns))
		for id := range ns {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		var b strings.Builder
		b.WriteString("(CASE %s.id")
		for _, id := range ids {
			fmt.Fprintf(&b, " WHEN %d THEN %d", id, ns[id].Lft)
		}
		b.WriteString(" ELSE 0 END)")
		q.nestedSetExpr = b.String()
	}
	return strings.ReplaceAll(q.nestedSetExpr, "%s", table), nil
}

// ProjectsLftOrder は "projects.lft ASC" 相当の ORDER BY 項目 (ツリー順)。
// 結果の FROM に projects が結合されていること (IssueQuery の base_scope は結合している)。
func (q *Query) ProjectsLftOrder(ctx context.Context) (string, error) {
	e, err := q.projectsLftExpr(ctx, "projects")
	if err != nil {
		return "", err
	}
	return e + " ASC", nil
}

func unionStrings(a, b []string) []string {
	out := slices.Clone(a)
	for _, s := range b {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	// Ruby の Array#| は重複も除く
	var dedup []string
	for _, s := range out {
		if !slices.Contains(dedup, s) {
			dedup = append(dedup, s)
		}
	}
	return dedup
}
