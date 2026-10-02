package customfield

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/view/rails"
)

// Format は Redmine::FieldFormat::Base のサブクラス（lib/redmine/field_format.rb）のインスタンス。
// クラス属性（class_attribute）を公開フィールドで、振る舞い（検証・キャスト・表示・編集タグ ...）を
// メソッドで提供する。書式は 13 種類で FindFormat(name) で得る（formats.go に定義）。
type Format struct {
	// Name は書式名（custom_fields.field_format。format_name）。
	Name string
	// Label は書式名の i18n キー（label）。
	Label string
	// MultipleSupported は複数値をサポートする（multiple_supported）。
	MultipleSupported bool
	// IsFilterSupported はフィルタとして使える（is_filter_supported）。
	IsFilterSupported bool
	// SearchableSupported は全文検索の対象にできる（searchable_supported）。
	SearchableSupported bool
	// TotalableSupported は合計できる（totalable_supported）。
	TotalableSupported bool
	// BulkEditSupported は一括編集できる（bulk_edit_supported）。
	BulkEditSupported bool
	// Target は値が参照するレコード種別（RecordList#target_class）。"user" / "version" / "enumeration" / ""。
	Target string
	// CustomizedKinds は追加できるカスタムフィールド種別（customized_class_names）。nil は制限なし。
	CustomizedKinds []OwnerKind
	// FilterType は query_filter_options の :type。
	FilterType string
	// FormPartial は form_partial（管理画面の書式別オプションの部分テンプレート）。
	FormPartial string
	// ChangeAsDiff は change_as_diff（履歴を差分で表示する）。
	ChangeAsDiff bool
	// ChangeNoDetails は change_no_details。
	ChangeNoDetails bool
	// FieldAttributes は field_attributes で宣言された format_store のキー（共通の url_pattern, full_width_layout を含む）。
	FieldAttributes []string

	numeric   bool // Numeric のサブクラス（CAST によるソート）
	groupable bool // group_statement を持つ（Base/StringFormat 系は持たない）

	// 以下はサブクラスでの上書きに相当するフック（nil なら Base の既定動作）。
	castSingle         func(env *Env, cf *CustomField, v string, customized *Customized) any
	validateSingle     func(env *Env, cf *CustomField, v string, customized *Customized) []string
	validateValue      func(env *Env, cv *CustomValue) []string
	validateField      func(env *Env, cf *CustomField) []FieldError
	possibleValues     func(env *Env, cf *CustomField, object any) []Option
	possibleCustomVals func(env *Env, cv *CustomValue) []Option
	formatted          func(f *Format, env *Env, cf *CustomField, value any, customized *Customized, html bool) any
	editTag            func(f *Format, env *Env, tagID, tagName string, cv *CustomValue, opts *rails.Hash) rails.HTML
	bulkEditTag        func(f *Format, env *Env, tagID, tagName string, cf *CustomField, objects []*Customized, value any, opts *rails.Hash) rails.HTML
	setValue           func(env *Env, cf *CustomField, cv *CustomValue, value any) any
	beforeSave         func(env *Env, cf *CustomField)
	valueFromKeyword   func(f *Format, env *Env, cf *CustomField, keyword string, object any) any
}

// Numeric は数値書式（int / float / progressbar）なら true。
func (f *Format) Numeric() bool { return f.numeric }

// Groupable は group_statement が nil でない書式なら true。
func (f *Format) Groupable() bool { return f.groupable }

// IsRecordList は値がレコード id の書式（user / version / enumeration）なら true。
func (f *Format) IsRecordList() bool { return f.Target != "" }

// JoinAlias は join_alias(custom_field)（"cf_<id>"）。
func JoinAlias(cf *CustomField) string { return "cf_" + strconv.FormatInt(cf.ID, 10) }

// valueJoinAlias は RecordList#value_join_alias（"cf_<id>_<format>"）。
func valueJoinAlias(cf *CustomField) string { return JoinAlias(cf) + "_" + cf.FieldFormat }

// TargetTable は RecordList の参照先テーブル。
func (f *Format) TargetTable() string {
	switch f.Target {
	case "user":
		return "principals"
	case "version":
		return "versions"
	case "enumeration":
		return "custom_field_enumerations"
	}
	return ""
}

// CastSingleValue は cast_single_value のクエリ向けの移植（DB を引かない）。戻り値の型は書式により
// string / int64 / float64 / bool / db 日付文字列（YYYY-MM-DD）/ レコード id（int64）。
// RecordList / attachment は存在確認を行わず id を返す（Redmine はレコードを返す）。
// 表示用のキャスト（レコードの名前・time.Time の日付）は CastSingle / Cast を使う。
func (f *Format) CastSingleValue(v string) any {
	switch f.Name {
	case "int":
		return RubyToI(v)
	case "progressbar":
		n := RubyToI(v)
		return max(0, min(100, n))
	case "float":
		return RubyToF(v)
	case "date":
		t, err := time.Parse("2006-01-02", strings.TrimSpace(v))
		if err != nil {
			return nil
		}
		return t.Format("2006-01-02")
	case "bool":
		return v == "1"
	case "user", "version", "enumeration", "attachment":
		if strings.TrimSpace(v) == "" {
			return nil
		}
		return RubyToI(v)
	}
	return v
}

// RubyToI は String#to_i（先頭の符号付き整数部分。解釈できなければ 0）。
func RubyToI(s string) int64 {
	s = strings.TrimLeft(s, " \t\n\v\f\r")
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	j := i
	for j < len(s) && (s[j] >= '0' && s[j] <= '9' || (s[j] == '_' && j > i && j+1 < len(s) && s[j+1] >= '0' && s[j+1] <= '9')) {
		j++
	}
	n, err := strconv.ParseInt(strings.ReplaceAll(s[:j], "_", ""), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// RubyToF は String#to_f（先頭の浮動小数部分。解釈できなければ 0）。
func RubyToF(s string) float64 {
	s = strings.TrimLeft(s, " \t\n\v\f\r")
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := func() {
		for i < len(s) && (s[i] >= '0' && s[i] <= '9' || (s[i] == '_' && i > 0 && s[i-1] >= '0' && s[i-1] <= '9' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9')) {
			i++
		}
	}
	start := i
	digits()
	if i < len(s) && s[i] == '.' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' {
		i++
		digits()
	}
	if i == start {
		return 0
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		k := i + 1
		if k < len(s) && (s[k] == '+' || s[k] == '-') {
			k++
		}
		if k < len(s) && s[k] >= '0' && s[k] <= '9' {
			i = k
			digits()
		}
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(s[:i], "_", ""), 64)
	if err != nil {
		return 0
	}
	return f
}

// ---------------------------------------------------------------- 登録

// FormatNames は利用可能な書式名（Redmine::FieldFormat.available_formats の登録順）。
var FormatNames = []string{"string", "text", "link", "int", "float", "date", "list", "bool", "enumeration", "user", "version", "attachment", "progressbar"}

var (
	registry []*Format
	formats  = map[string]*Format{}
)

func register(f *Format) {
	f.FieldAttributes = append([]string{"url_pattern", "full_width_layout"}, f.FieldAttributes...)
	registry = append(registry, f)
	formats[f.Name] = f
}

// FindFormat は書式を返す。未知の書式は Base 相当（string と同じ扱い。Redmine の Hash.new(Base.instance)）を返す。
func FindFormat(name string) *Format {
	if f, ok := formats[name]; ok {
		return f
	}
	return &Format{Name: name, Label: "label_" + name, IsFilterSupported: true, BulkEditSupported: true, FilterType: "string"}
}

// Find は Redmine::FieldFormat.find(name)（未知の名前なら nil。validates_inclusion_of :field_format の判定に使う）。
func Find(name string) *Format { return formats[name] }

// All は FieldFormat.all.values（登録順）。
func All() []*Format { return append([]*Format(nil), registry...) }

// AvailableFor は kind で使える書式（登録順。FieldFormat.formats_for_custom_field_class）。
func AvailableFor(kind OwnerKind) []*Format {
	var out []*Format
	for _, f := range registry {
		if f.CustomizedKinds == nil || containsKind(f.CustomizedKinds, kind) {
			out = append(out, f)
		}
	}
	return out
}

// AsSelect は FieldFormat.as_select(class_name)（翻訳したラベルでソートした [label, name]）。
// kind が空ならすべての書式。
func AsSelect(t func(key string, args ...any) string, kind OwnerKind) []Option {
	var out []Option
	for _, f := range registry {
		if kind != "" && f.CustomizedKinds != nil && !containsKind(f.CustomizedKinds, kind) {
			continue
		}
		out = append(out, Option{Label: t(f.Label), Value: f.Name})
	}
	// Ruby の sort_by(&:first)（文字列のバイト順）
	sort.SliceStable(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

func containsKind(ks []OwnerKind, k OwnerKind) bool {
	for _, x := range ks {
		if x == k {
			return true
		}
	}
	return false
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- 値

// Option は選択肢 1 件（[label, value]）。list 書式のように値とラベルが同じ場合は Label == Value。
type Option struct {
	Label string
	Value string
}

// String は to_s（レコードの表示名）。
func (o Option) String() string { return o.Label }

// Pair は options_for_select に渡す [label, value]。
func (o Option) Pair() []any { return []any{o.Label, o.Value} }

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
	CustomField *CustomField
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

// nonEmpty は Array.wrap(value).reject {|v| v.to_s == ”}。
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
