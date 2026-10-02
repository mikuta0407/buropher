// Package customfield は Redmine のカスタムフィールド書式（lib/redmine/field_format.rb の
// Redmine::FieldFormat）の移植。
//
// 書式は 13 種類（string, text, link, int, float, date, list, bool, enumeration, user, version,
// attachment, progressbar）で、Find(name) で *Format を得る。Format は Redmine の書式クラスの
// クラス属性（Info: multiple_supported, is_filter_supported, searchable_supported, form_partial ...）と
// 振る舞いを持つ:
//
//   - 値の正規化・型変換: SetValue（set_custom_field_value）, Cast（cast_value）
//   - 検証: ValidateCustomField（validate_custom_field）, ValidateValue（validate_custom_value）,
//     ValidateSingleValue（validate_single_value）。CustomField 全体の検証は ValidateField（CustomField#validate_custom_field）
//   - 選択肢: PossibleValuesOptions / PossibleCustomValueOptions
//   - 表示: FormattedValue（formatted_value）。URL パターンによるリンクは URLFromPattern
//   - フォーム: EditTag / BulkEditTag（edit_tag_style による drop-down / check_box / radio）
//   - クエリ: QueryFilterType（query_filter_options の :type）, OrderNumeric
//   - 保存前処理: BeforeSave（before_custom_field_save）, ApplyFieldRules（set_searchable）
//
// DB やビューに依存する処理（キー・値リストの選択肢、プロジェクトのユーザー、共有バージョン、
// テキスト整形、カレンダー、添付フォーム）は Env の関数フィールドで呼び出し側から受け取る。
// 未設定の関数は Redmine で該当データが無い場合と同じ結果（空の選択肢など）になる。
//
// 値は Redmine と同じく文字列（単一値）または []string（複数値）で扱う（nil は未設定）。
package customfield

import (
	"sort"
	"strings"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// Info は書式クラスのクラス属性（class_attribute）。
type Info struct {
	// Name は format_name。
	Name string
	// Label は label（翻訳キー）。
	Label string
	// MultipleSupported は multiple_supported。
	MultipleSupported bool
	// IsFilterSupported は is_filter_supported。
	IsFilterSupported bool
	// SearchableSupported は searchable_supported。
	SearchableSupported bool
	// TotalableSupported は totalable_supported。
	TotalableSupported bool
	// BulkEditSupported は bulk_edit_supported。
	BulkEditSupported bool
	// CustomizedClassNames は customized_class_names（nil ならすべてのクラスで使える）。
	CustomizedClassNames []string
	// FormPartial は form_partial（管理画面の書式別オプションの部分テンプレート）。
	FormPartial string
	// ChangeAsDiff は change_as_diff（履歴を差分で表示する）。
	ChangeAsDiff bool
	// ChangeNoDetails は change_no_details。
	ChangeNoDetails bool
	// FieldAttributes は field_attributes で宣言された format_store のキー（共通の url_pattern, full_width_layout を含む）。
	FieldAttributes []string
}

// Option は選択肢 1 件（[label, value]）。list 書式のように値とラベルが同じ場合は Label == Value。
type Option struct {
	Label string
	Value string
}

// String は to_s（レコードの表示名）。
func (o Option) String() string { return o.Label }

// Pair は options_for_select に渡す [label, value]。
func (o Option) Pair() []any { return []any{o.Label, o.Value} }

// Format は 1 つのカスタムフィールド書式（Redmine::FieldFormat::Base のサブクラスのインスタンス）。
type Format struct {
	info Info

	// 以下はサブクラスでの上書きに相当するフック（nil なら Base の既定動作）。
	castSingle         func(env *Env, cf *domain.CustomField, v string, customized *Customized) any
	validateSingle     func(env *Env, cf *domain.CustomField, v string, customized *Customized) []string
	validateValue      func(env *Env, cv *CustomValue) []string
	validateField      func(env *Env, cf *domain.CustomField) []FieldError
	possibleValues     func(env *Env, cf *domain.CustomField, object any) []Option
	possibleCustomVals func(env *Env, cv *CustomValue) []Option
	formatted          func(f *Format, env *Env, cf *domain.CustomField, value any, customized *Customized, html bool) any
	editTag            func(f *Format, env *Env, tagID, tagName string, cv *CustomValue, opts *rails.Hash) rails.HTML
	bulkEditTag        func(f *Format, env *Env, tagID, tagName string, cf *domain.CustomField, objects []*Customized, value any, opts *rails.Hash) rails.HTML
	setValue           func(env *Env, cf *domain.CustomField, cv *CustomValue, value any) any
	beforeSave         func(env *Env, cf *domain.CustomField)
	valueFromKeyword   func(f *Format, env *Env, cf *domain.CustomField, keyword string, object any) any
	filterType         string
	orderNumeric       bool
	groupable          bool
	recordList         bool
}

// Info は書式のクラス属性。
func (f *Format) Info() Info { return f.info }

// Name は format_name。
func (f *Format) Name() string { return f.info.Name }

// Label は label（翻訳キー）。
func (f *Format) Label() string { return f.info.Label }

// QueryFilterType は query_filter_options の :type（string, text, integer, float, date, list_optional）。
// attachment は is_filter_supported が偽だが Base と同じ string を返す。
func (f *Format) QueryFilterType() string { return f.filterType }

// OrderNumeric は order_statement が数値（CAST ... AS decimal）で並べる書式なら true（int, float, progressbar）。
func (f *Format) OrderNumeric() bool { return f.orderNumeric }

// Groupable は group_statement が nil でない書式なら true（int, date, list, bool, enumeration, user, version, progressbar）。
func (f *Format) Groupable() bool { return f.groupable }

// RecordList は RecordList（user / version / enumeration）なら true（値がレコードの id）。
func (f *Format) RecordList() bool { return f.recordList }

// 登録順は Redmine の FieldFormat.add の呼び出し順（field_format.rb の定義順）。
var (
	registry []*Format
	byName   = map[string]*Format{}
)

func register(f *Format) *Format {
	f.info.FieldAttributes = append([]string{"url_pattern", "full_width_layout"}, f.info.FieldAttributes...)
	registry = append(registry, f)
	byName[f.info.Name] = f
	return f
}

// Find は Redmine::FieldFormat.find(name)。未知の名前なら nil。
func Find(name string) *Format { return byName[name] }

// MustFind は Find の結果を返す。未知の名前なら Redmine の Hash.new(Base.instance) と同じく
// どの書式にも属さない Base 相当（string と同じ振る舞いの無名書式）を返す。
func MustFind(name string) *Format {
	if f := byName[name]; f != nil {
		return f
	}
	return baseFormat
}

// All は FieldFormat.all.values（登録順）。
func All() []*Format { return append([]*Format(nil), registry...) }

// AvailableFormats は FieldFormat.available_formats（登録順の名前）。
func AvailableFormats() []string {
	out := make([]string, len(registry))
	for i, f := range registry {
		out[i] = f.info.Name
	}
	return out
}

// AvailableFor は className（Issue, User ... = customized_class の名前）で使える書式（登録順）。
// FieldFormat.formats_for_custom_field_class。
func AvailableFor(className string) []*Format {
	var out []*Format
	for _, f := range registry {
		if f.info.CustomizedClassNames == nil || contains(f.info.CustomizedClassNames, className) {
			out = append(out, f)
		}
	}
	return out
}

// AsSelect は FieldFormat.as_select(class_name)（翻訳したラベルでソートした [label, name]）。
// className が空ならすべての書式。
func AsSelect(t func(key string, args ...any) string, className string) []Option {
	var out []Option
	for _, f := range registry {
		if className != "" && f.info.CustomizedClassNames != nil && !contains(f.info.CustomizedClassNames, className) {
			continue
		}
		out = append(out, Option{Label: t(f.info.Label), Value: f.info.Name})
	}
	// Ruby の sort_by(&:first)（文字列のバイト順。安定ソートではないが同一ラベルはない）
	sort.SliceStable(out, func(i, j int) bool { return out[i].Label < out[j].Label })
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

// FieldError は validate_custom_field が返す [attribute, message]（message は errors のシンボル: invalid, blank）。
type FieldError struct {
	Attr    string
	Message string
}

// Customized はカスタム値の所有者（CustomValue#customized）のうち書式が参照する属性。
type Customized struct {
	// Kind は custom_values.customized_kind（issue, project ...）。
	Kind string
	// ID は所有者の id（新規なら 0）。
	ID int64
	// ProjectID / ProjectIdentifier は customized.project（無ければ 0 / ""）。
	ProjectID         int64
	ProjectIdentifier string
}

// CustomValue は CustomValue / CustomFieldValue（カスタムフィールド・所有者・値）。
type CustomValue struct {
	CustomField *domain.CustomField
	Customized  *Customized
	// Value は値（string / []string / nil）。
	Value any
	// ValueWas は変更前の値（value_was。list / RecordList の検証で使う）。
	ValueWas any
}

// wrap は Array.wrap(value).map(&:to_s)。
func wrap(v any) []string {
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		return []string{x}
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			out = append(out, rails.ToS(e))
		}
		return out
	default:
		return []string{rails.ToS(x)}
	}
}

// nonEmpty は Array.wrap(value).reject {|v| v.to_s == ''}。
func nonEmpty(v any) []string {
	var out []string
	for _, s := range wrap(v) {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// isBlank は Ruby の blank?（nil, 空白のみの文字列, 空配列）。
func isBlank(v any) bool { return rails.IsBlank(v) }

func isArray(v any) bool {
	switch v.(type) {
	case []string, []any:
		return true
	}
	return false
}

func trimSpace(s string) string { return strings.Trim(s, " \t\n\v\f\r\x00") }
