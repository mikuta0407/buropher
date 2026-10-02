package query

import (
	"context"
	"slices"
	"strings"

	"github.com/mikuta0407/buropher/internal/customfield"
)

// Option はフィルタ値の選択肢 ([label, value] または [label, value, group])。
type Option struct {
	Label string
	Value string
	// Group は 3 番目の要素 (ユーザのステータス・バージョンのステータス等)。空なら 2 要素。
	Group string
}

// FilterDef は QueryFilter (利用可能なフィルタの定義)。
type FilterDef struct {
	Field string
	// Type はフィルタ型 (list / list_status / date / string / ...)。
	Type string
	// Name は表示名。
	Name string
	// Values は固定の選択肢 (ValuesFunc が nil の場合)。
	Values []Option
	// ValuesFunc は選択肢を遅延計算する関数 (Redmine の lambda)。
	ValuesFunc func(ctx context.Context) ([]Option, error)
	// Remote は選択肢を別リクエストで取得する (lambda の場合の既定値)。
	Remote bool
	// CustomField はカスタムフィールドのフィルタの :field。
	CustomField *customfield.CustomField
	// Through は連鎖カスタムフィールドのフィルタの :through。
	Through *customfield.CustomField

	values       []Option
	valuesLoaded bool
}

// HasValues は選択肢を持つ (固定値または関数) なら true。
func (f *FilterDef) HasValues() bool { return f.Values != nil || f.ValuesFunc != nil }

// LoadValues は values (lambda は一度だけ評価してキャッシュする)。
func (f *FilterDef) LoadValues(ctx context.Context) ([]Option, error) {
	if f.ValuesFunc == nil {
		return f.Values, nil
	}
	if !f.valuesLoaded {
		v, err := f.ValuesFunc(ctx)
		if err != nil {
			return nil, err
		}
		f.values, f.valuesLoaded = v, true
	}
	return f.values, nil
}

// Operators はこのフィルタの型で使える演算子。
func (f *FilterDef) Operators() []string { return OperatorsByFilterType[f.Type] }

// filterSet は利用可能なフィルタ (挿入順を保つ)。
type filterSet struct {
	keys []string
	m    map[string]*FilterDef
}

func newFilterSet() *filterSet { return &filterSet{m: map[string]*FilterDef{}} }

// Has はフィルタが利用可能なら true。
func (s *filterSet) Has(field string) bool { _, ok := s.m[field]; return ok }

// Get はフィルタ定義を返す (無ければ nil)。
func (s *filterSet) Get(field string) *FilterDef { return s.m[field] }

// Keys はフィールド名を追加順で返す。
func (s *filterSet) Keys() []string { return slices.Clone(s.keys) }

// Defs はフィルタ定義を追加順で返す。
func (s *filterSet) Defs() []*FilterDef {
	out := make([]*FilterDef, len(s.keys))
	for i, k := range s.keys {
		out[i] = s.m[k]
	}
	return out
}

func (s *filterSet) add(d *FilterDef) {
	if _, ok := s.m[d.Field]; !ok {
		s.keys = append(s.keys, d.Field)
	}
	s.m[d.Field] = d
}

func (s *filterSet) delete(field string) {
	if _, ok := s.m[field]; !ok {
		return
	}
	delete(s.m, field)
	s.keys = slices.DeleteFunc(s.keys, func(k string) bool { return k == field })
}

// AvailableFilters は available_filters (初回に initialize_available_filters を呼ぶ)。
func (q *Query) AvailableFilters(ctx context.Context) (*AvailableFilterSet, error) {
	if q.availableFilters == nil {
		q.availableFilters = newFilterSet()
		if err := q.impl.initializeAvailableFilters(ctx, q); err != nil {
			q.availableFilters = nil
			return nil, err
		}
	}
	return &AvailableFilterSet{q.availableFilters}, nil
}

// AvailableFilterSet は利用可能なフィルタの読み取り専用ビュー。
type AvailableFilterSet struct{ s *filterSet }

// Has はフィルタが利用可能なら true。
func (a *AvailableFilterSet) Has(field string) bool { return a.s.Has(field) }

// Get はフィルタ定義 (無ければ nil)。
func (a *AvailableFilterSet) Get(field string) *FilterDef { return a.s.Get(field) }

// Keys はフィールド名 (追加順)。
func (a *AvailableFilterSet) Keys() []string { return a.s.Keys() }

// Defs はフィルタ定義 (追加順)。
func (a *AvailableFilterSet) Defs() []*FilterDef { return a.s.Defs() }

// filterOpt は add_available_filter のオプション。
type filterOpt struct {
	Type       string
	Name       string // 明示した :name
	Label      string // :label (i18n キー)
	Values     []Option
	ValuesFunc func(ctx context.Context) ([]Option, error)
	Remote     *bool
	Field      *customfield.CustomField
	Through    *customfield.CustomField
}

// addAvailableFilter は add_available_filter。:name 未指定なら l(:label || "field_<field>" から _id を除いたもの)。
func (q *Query) addAvailableFilter(field string, o filterOpt) {
	name := o.Name
	if name == "" {
		key := o.Label
		if key == "" {
			key = strings.TrimSuffix("field_"+field, "_id")
		}
		name = q.env.l(key)
	}
	remote := o.ValuesFunc != nil
	if o.Remote != nil {
		remote = *o.Remote
	}
	q.availableFilters.add(&FilterDef{Field: field, Type: o.Type, Name: name, Values: o.Values, ValuesFunc: o.ValuesFunc,
		Remote: remote, CustomField: o.Field, Through: o.Through})
}

func (q *Query) deleteAvailableFilter(field string) { q.availableFilters.delete(field) }

// ---------------------------------------------------------------- カスタムフィールドのフィルタ

// queryFilterOptions は CustomField#query_filter_options(query)。
func (q *Query) queryFilterOptions(cf *customfield.CustomField) filterOpt {
	f := cf.Format()
	o := filterOpt{Type: f.FilterType}
	switch f.Name {
	case "list", "bool", "enumeration", "user", "version":
		o.ValuesFunc = func(ctx context.Context) ([]Option, error) { return q.customFieldFilterValues(ctx, cf) }
	}
	return o
}

// addCustomFieldFilter は add_custom_field_filter(field, assoc)。
func (q *Query) addCustomFieldFilter(cf *customfield.CustomField, assoc string) {
	o := q.queryFilterOptions(cf)
	id := "cf_" + itoa(cf.ID)
	name := cf.Name
	if assoc != "" {
		id = assoc + "." + id
		name = q.env.l("label_attribute_of_"+assoc, map[string]any{"name": name})
	}
	o.Name, o.Field = name, cf
	q.addAvailableFilter(id, o)
}

// addChainedCustomFieldFilters は add_chained_custom_field_filters(field)。
func (q *Query) addChainedCustomFieldFilters(ctx context.Context, cf *customfield.CustomField) error {
	var kind customfield.OwnerKind
	switch cf.Format().Target {
	case "user":
		kind = customfield.KindUser
	case "version":
		kind = customfield.KindVersion
	default:
		return nil
	}
	// CustomField.where(:is_filter => true, :type => ...) (可視性は見ない。並びは DB 依存なので id 順)
	chained, err := customfield.Load(ctx, q.env.Q, "custom_fields.is_filter = ? AND custom_fields.owner_kind = ?", true, string(kind))
	if err != nil {
		return err
	}
	slices.SortStableFunc(chained, func(a, b *customfield.CustomField) int { return int(a.ID - b.ID) })
	for _, c := range chained {
		o := q.queryFilterOptions(c)
		o.Name = q.env.l("label_attribute_of_object", map[string]any{"name": c.Name, "object_name": cf.Name})
		o.Field, o.Through = c, cf
		q.addAvailableFilter("cf_"+itoa(cf.ID)+".cf_"+itoa(c.ID), o)
	}
	return nil
}

// visibleFilterCustomFields は scope.visible.where(:is_filter => true).sorted。
func (q *Query) visibleFilterCustomFields(ctx context.Context, where string, args ...any) ([]*customfield.CustomField, error) {
	vis, err := customfield.VisibleCondition(ctx, q.env.Auth)
	if err != nil {
		return nil, err
	}
	w := "(" + vis + ") AND custom_fields.is_filter = ?"
	a := []any{true}
	if where != "" {
		w += " AND (" + where + ")"
		a = append(a, args...)
	}
	return customfield.Load(ctx, q.env.Q, w, a...)
}

// addCustomFieldsFilters は add_custom_fields_filters(scope, assoc)。
func (q *Query) addCustomFieldsFilters(ctx context.Context, cfs []*customfield.CustomField, assoc string) error {
	for _, cf := range cfs {
		q.addCustomFieldFilter(cf, assoc)
		if assoc != "" {
			continue
		}
		if err := q.addChainedCustomFieldFilters(ctx, cf); err != nil {
			return err
		}
		if cf.Format().Target == "version" {
			q.addAvailableFilter("cf_"+itoa(cf.ID)+".due_date", filterOpt{
				Type: "date", Field: cf,
				Name: q.env.l("label_attribute_of_object", map[string]any{"name": q.env.l("field_effective_date"), "object_name": cf.Name}),
			})
			q.addAvailableFilter("cf_"+itoa(cf.ID)+".status", filterOpt{
				Type: "list", Field: cf,
				Name:   q.env.l("label_attribute_of_object", map[string]any{"name": q.env.l("field_status"), "object_name": cf.Name}),
				Values: q.versionStatusOptions(),
			})
		}
	}
	return nil
}

// assocCustomizedKinds は関連の klass に代入可能なカスタムフィールド種別
// (field_class.customized_class <= association_klass)。
var assocCustomizedKinds = map[string][]customfield.OwnerKind{
	"project":       {customfield.KindProject},
	"author":        {customfield.KindUser},
	"assigned_to":   {customfield.KindUser, customfield.KindGroup},
	"fixed_version": {customfield.KindVersion},
	"user":          {customfield.KindUser},
	"issue":         {customfield.KindIssue},
}

// addAssociationsCustomFieldsFilters は add_associations_custom_fields_filters(*associations)。
func (q *Query) addAssociationsCustomFieldsFilters(ctx context.Context, assocs ...string) error {
	// CustomField.visible.where(:is_filter => true).group_by(&:class): 並びは DB 依存 (id 順とみなす)
	all, err := q.visibleFilterCustomFields(ctx, "")
	if err != nil {
		return err
	}
	slices.SortStableFunc(all, func(a, b *customfield.CustomField) int { return int(a.ID - b.ID) })
	var kinds []customfield.OwnerKind
	byKind := map[customfield.OwnerKind][]*customfield.CustomField{}
	for _, cf := range all {
		if _, ok := byKind[cf.OwnerKind]; !ok {
			kinds = append(kinds, cf.OwnerKind)
		}
		byKind[cf.OwnerKind] = append(byKind[cf.OwnerKind], cf)
	}
	for _, assoc := range assocs {
		for _, k := range kinds {
			if !slices.Contains(assocCustomizedKinds[assoc], k) {
				continue
			}
			fields := slices.Clone(byKind[k])
			slices.SortStableFunc(fields, func(a, b *customfield.CustomField) int { return a.Position - b.Position })
			for _, cf := range fields {
				q.addCustomFieldFilter(cf, assoc)
			}
		}
	}
	return nil
}

// versionStatusOptions は Version::VERSION_STATUSES の選択肢。
func (q *Query) versionStatusOptions() []Option {
	var out []Option
	for _, s := range []string{"open", "locked", "closed"} {
		out = append(out, Option{Label: q.env.l("version_status_" + s), Value: s})
	}
	return out
}

func boolPtr(b bool) *bool { return &b }
