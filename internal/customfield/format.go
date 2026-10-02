package customfield

import (
	"strconv"
	"strings"
	"time"
)

// Format は Redmine::FieldFormat::Base のサブクラス (lib/redmine/field_format.rb) のうち、
// クエリ (フィルタ・列・ソート・グループ・合計) が必要とする性質をまとめたもの。
type Format struct {
	// Name は書式名 (custom_fields.field_format)。
	Name string
	// Label は書式名の i18n キー。
	Label string
	// MultipleSupported は複数値をサポートする (multiple_supported)。
	MultipleSupported bool
	// IsFilterSupported はフィルタとして使える (is_filter_supported)。
	IsFilterSupported bool
	// SearchableSupported は全文検索の対象にできる (searchable_supported)。
	SearchableSupported bool
	// TotalableSupported は合計できる (totalable_supported)。
	TotalableSupported bool
	// Target は値が参照するレコード種別 (RecordList#target_class)。"user" / "version" / "enumeration" / ""。
	Target string
	// CustomizedKinds は追加できるカスタムフィールド種別 (customized_class_names)。nil は制限なし。
	CustomizedKinds []OwnerKind
	// FilterType は query_filter_options の :type。
	FilterType string

	numeric   bool // Numeric のサブクラス (CAST によるソート)
	groupable bool // group_statement を持つ (Base/StringFormat 系は持たない)
}

var recordListKinds = []OwnerKind{KindIssue, KindTimeEntry, KindVersion, KindDocument, KindProject}

var formats = map[string]*Format{
	"string":      {Name: "string", Label: "label_string", IsFilterSupported: true, SearchableSupported: true, FilterType: "string"},
	"text":        {Name: "text", Label: "label_text", IsFilterSupported: true, SearchableSupported: true, FilterType: "text"},
	"link":        {Name: "link", Label: "label_link", IsFilterSupported: true, FilterType: "string"},
	"int":         {Name: "int", Label: "label_integer", IsFilterSupported: true, TotalableSupported: true, FilterType: "integer", numeric: true, groupable: true},
	"float":       {Name: "float", Label: "label_float", IsFilterSupported: true, TotalableSupported: true, FilterType: "float", numeric: true},
	"date":        {Name: "date", Label: "label_date", IsFilterSupported: true, FilterType: "date", groupable: true},
	"list":        {Name: "list", Label: "label_list", MultipleSupported: true, IsFilterSupported: true, SearchableSupported: true, FilterType: "list_optional", groupable: true},
	"bool":        {Name: "bool", Label: "label_boolean", IsFilterSupported: true, FilterType: "list_optional", groupable: true},
	"enumeration": {Name: "enumeration", Label: "label_field_format_enumeration", MultipleSupported: true, IsFilterSupported: true, Target: "enumeration", FilterType: "list_optional", groupable: true},
	"user":        {Name: "user", Label: "label_user", MultipleSupported: true, IsFilterSupported: true, Target: "user", CustomizedKinds: recordListKinds, FilterType: "list_optional", groupable: true},
	"version":     {Name: "version", Label: "label_version", MultipleSupported: true, IsFilterSupported: true, Target: "version", CustomizedKinds: recordListKinds, FilterType: "list_optional", groupable: true},
	"attachment":  {Name: "attachment", Label: "label_attachment", FilterType: "string"},
	"progressbar": {Name: "progressbar", Label: "label_progressbar", IsFilterSupported: true, FilterType: "integer", numeric: true, groupable: true},
}

// FormatNames は利用可能な書式名 (Redmine::FieldFormat.available_formats の登録順)。
var FormatNames = []string{"string", "text", "link", "int", "float", "date", "list", "bool", "enumeration", "user", "version", "attachment", "progressbar"}

// FindFormat は書式を返す。未知の書式は Base 相当 (string と同じ扱い) を返す。
func FindFormat(name string) *Format {
	if f, ok := formats[name]; ok {
		return f
	}
	return &Format{Name: name, Label: "label_" + name, IsFilterSupported: true, FilterType: "string"}
}

// Numeric は数値書式 (int / float / progressbar) なら true。
func (f *Format) Numeric() bool { return f.numeric }

// IsRecordList は値がレコード id の書式 (user / version / enumeration) なら true。
func (f *Format) IsRecordList() bool { return f.Target != "" }

// JoinAlias は join_alias(custom_field) ("cf_<id>")。
func JoinAlias(cf *CustomField) string { return "cf_" + strconv.FormatInt(cf.ID, 10) }

// valueJoinAlias は RecordList#value_join_alias ("cf_<id>_<format>")。
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

// CastSingleValue は cast_single_value の移植。戻り値の型は書式により
// string / int64 / float64 / bool / db 日付文字列 (YYYY-MM-DD) / レコード id (int64)。
// RecordList / attachment は存在確認を行わず id を返す (Redmine はレコードを返す)。
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

// RubyToI は String#to_i (先頭の符号付き整数部分。解釈できなければ 0)。
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

// RubyToF は String#to_f (先頭の浮動小数部分。解釈できなければ 0)。
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
