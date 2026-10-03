// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package customfield

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mikuta0407/buropher/internal/view/rails"
)

// 各書式（lib/redmine/field_format.rb の定義順に登録する）。
var (
	// StringFormat は 'string'（Unbounded）。
	StringFormat = &Format{
		Name: "string", Label: "label_string", IsFilterSupported: true, SearchableSupported: true,
		BulkEditSupported: true, FormPartial: "custom_fields/formats/string", FieldAttributes: []string{"text_formatting"},
		validateSingle: validateUnbounded,
		formatted:      formattedString,
		FilterType:     "string",
	}
	// TextFormat は 'text'。
	TextFormat = &Format{
		Name: "text", Label: "label_text", IsFilterSupported: true, SearchableSupported: true,
		BulkEditSupported: true, FormPartial: "custom_fields/formats/text", ChangeAsDiff: true,
		FieldAttributes: []string{"text_formatting"},
		validateSingle:  validateUnbounded,
		formatted:       formattedText,
		editTag:         textEditTag,
		bulkEditTag:     textBulkEditTag,
		FilterType:      "text",
	}
	// LinkFormat は 'link'（StringFormat のサブクラス）。
	LinkFormat = &Format{
		Name: "link", Label: "label_link", IsFilterSupported: true, BulkEditSupported: true,
		FormPartial: "custom_fields/formats/link", FieldAttributes: []string{"text_formatting"},
		validateSingle: validateUnbounded,
		formatted:      formattedLink,
		FilterType:     "string",
	}
	// IntFormat は 'int'（Numeric）。
	IntFormat = &Format{
		Name: "int", Label: "label_integer", IsFilterSupported: true, TotalableSupported: true,
		BulkEditSupported: true, FormPartial: "custom_fields/formats/numeric", FieldAttributes: []string{"thousands_delimiter"},
		castSingle: func(env *Env, cf *CustomField, v string, c *Customized) any { return RubyToI(v) },
		validateSingle: func(env *Env, cf *CustomField, v string, c *Customized) []string {
			errs := validateUnbounded(env, cf, v, c)
			if !intRe.MatchString(trimSpace(v)) {
				errs = append(errs, env.l("activerecord.errors.messages.not_a_number"))
			}
			return errs
		},
		FilterType: "integer",
		numeric:    true,
		groupable:  true,
	}
	// FloatFormat は 'float'（Numeric）。
	FloatFormat = &Format{
		Name: "float", Label: "label_float", IsFilterSupported: true, TotalableSupported: true,
		BulkEditSupported: true, FormPartial: "custom_fields/formats/numeric", FieldAttributes: []string{"thousands_delimiter"},
		castSingle: func(env *Env, cf *CustomField, v string, c *Customized) any { return RubyToF(v) },
		validateSingle: func(env *Env, cf *CustomField, v string, c *Customized) []string {
			errs := validateUnbounded(env, cf, v, c)
			if _, ok := kernelFloat(v); !ok {
				errs = append(errs, env.l("activerecord.errors.messages.invalid"))
			}
			return errs
		},
		FilterType: "float",
		numeric:    true,
	}
	// DateFormat は 'date'。
	DateFormat = &Format{
		Name: "date", Label: "label_date", IsFilterSupported: true, BulkEditSupported: true,
		FormPartial: "custom_fields/formats/date",
		castSingle: func(env *Env, cf *CustomField, v string, c *Customized) any {
			if t, ok := toDate(v); ok {
				return t
			}
			return nil
		},
		validateSingle: func(env *Env, cf *CustomField, v string, c *Customized) []string {
			if dateRe.MatchString(v) {
				if _, ok := toDate(v); ok {
					return nil
				}
			}
			return []string{env.l("activerecord.errors.messages.not_a_date")}
		},
		editTag:     dateEditTag,
		bulkEditTag: dateBulkEditTag,
		FilterType:  "date",
		groupable:   true,
	}
	// ListFormat は 'list'（List）。
	ListFormat = &Format{
		Name: "list", Label: "label_list", MultipleSupported: true, IsFilterSupported: true,
		SearchableSupported: true, BulkEditSupported: true, FormPartial: "custom_fields/formats/list",
		FieldAttributes: []string{"edit_tag_style"},
		possibleValues: func(env *Env, cf *CustomField, object any) []Option {
			out := make([]Option, len(cf.PossibleValues))
			for i, v := range cf.PossibleValues {
				out[i] = Option{v, v}
			}
			return out
		},
		possibleCustomVals: func(env *Env, cv *CustomValue) []Option {
			opts := Find("list").PossibleValuesOptions(env, cv.CustomField, nil)
			// [custom_value.value].flatten.reject(&:blank?) - options
			for _, v := range wrap(cv.Value) {
				if isBlank(v) || slices.Contains(cv.CustomField.PossibleValues, v) || containsValue(opts, v) {
					continue
				}
				opts = append(opts, Option{v, v})
			}
			return opts
		},
		validateField: func(env *Env, cf *CustomField) []FieldError {
			if len(cf.PossibleValues) == 0 {
				return []FieldError{{"possible_values", "blank"}}
			}
			return nil
		},
		validateValue: func(env *Env, cv *CustomValue) []string {
			was := wrap(cv.ValueWas)
			for _, v := range nonEmpty(cv.Value) {
				if !slices.Contains(was, v) && !slices.Contains(cv.CustomField.PossibleValues, v) {
					return []string{env.l("activerecord.errors.messages.inclusion")}
				}
			}
			return nil
		},
		editTag:     listEditTag,
		bulkEditTag: listBulkEditTag,
		FilterType:  "list_optional",
		groupable:   true,
	}
	// BoolFormat は 'bool'（List）。
	BoolFormat = &Format{
		Name: "bool", Label: "label_boolean", IsFilterSupported: true, BulkEditSupported: true,
		FormPartial: "custom_fields/formats/bool", FieldAttributes: []string{"edit_tag_style"},
		castSingle: func(env *Env, cf *CustomField, v string, c *Customized) any { return v == "1" },
		possibleValues: func(env *Env, cf *CustomField, object any) []Option {
			return []Option{{env.l("general_text_Yes"), "1"}, {env.l("general_text_No"), "0"}}
		},
		editTag:     boolEditTag,
		bulkEditTag: listBulkEditTag,
		FilterType:  "list_optional",
		groupable:   true,
	}
	// EnumerationFormat は 'enumeration'（RecordList。キー・値リスト）。
	EnumerationFormat = &Format{
		Name: "enumeration", Target: "enumeration", Label: "label_field_format_enumeration", MultipleSupported: true,
		IsFilterSupported: true, BulkEditSupported: true, FormPartial: "custom_fields/formats/enumeration",
		FieldAttributes: []string{"edit_tag_style"},
		castSingle:      castRecord("enumeration"),
		possibleValues: func(env *Env, cf *CustomField, object any) []Option {
			if env == nil || env.Enumerations == nil {
				return nil
			}
			var out []Option
			for _, e := range env.Enumerations(cf.ID, true) {
				out = append(out, Option{e.Name, strconv.FormatInt(e.ID, 10)})
			}
			return out
		},
		possibleCustomVals: recordPossibleCustomValueOptions("enumeration"),
		validateValue:      validateRecordList,
		valueFromKeyword: func(f *Format, env *Env, cf *CustomField, keyword string, object any) any {
			var all []*Enumeration
			if env != nil && env.Enumerations != nil {
				all = env.Enumerations(cf.ID, false)
			}
			return parseKeyword(cf, keyword, func(k string) (string, bool) {
				for _, e := range all {
					if likeMatch(e.Name, k) {
						return strconv.FormatInt(e.ID, 10), true
					}
				}
				return "", false
			})
		},
		editTag:     listEditTag,
		bulkEditTag: listBulkEditTag,
		FilterType:  "list_optional",
		groupable:   true,
	}
	// UserFormat は 'user'（RecordList）。
	UserFormat = &Format{
		Name: "user", Target: "user", Label: "label_user", MultipleSupported: true, IsFilterSupported: true,
		BulkEditSupported: true, CustomizedKinds: recordListKinds, FormPartial: "custom_fields/formats/user",
		FieldAttributes: []string{"edit_tag_style", "user_role"},
		castSingle:      castRecord("user"),
		possibleValues: func(env *Env, cf *CustomField, object any) []Option {
			users := userRecords(env, cf, object)
			var out []Option
			if env != nil && env.CurrentUserID != 0 && containsValue(users, strconv.FormatInt(env.CurrentUserID, 10)) {
				out = append(out, Option{"<< " + env.l("label_me") + " >>", strconv.FormatInt(env.CurrentUserID, 10)})
			}
			return append(out, users...)
		},
		possibleCustomVals: recordPossibleCustomValueOptions("user"),
		validateValue:      validateRecordList,
		beforeSave:         func(env *Env, cf *CustomField) { compactListSetting(cf, "user_role") },
		valueFromKeyword: func(f *Format, env *Env, cf *CustomField, keyword string, object any) any {
			users := userRecords(env, cf, object)
			return parseKeyword(cf, keyword, func(k string) (string, bool) {
				for _, u := range users {
					if strings.EqualFold(u.Label, k) {
						return u.Value, true
					}
				}
				return "", false
			})
		},
		editTag:     listEditTag,
		bulkEditTag: listBulkEditTag,
		FilterType:  "list_optional",
		groupable:   true,
	}
	// VersionFormat は 'version'（RecordList）。
	VersionFormat = &Format{
		Name: "version", Target: "version", Label: "label_version", MultipleSupported: true, IsFilterSupported: true,
		BulkEditSupported: true, CustomizedKinds: recordListKinds, FormPartial: "custom_fields/formats/version",
		FieldAttributes:    []string{"edit_tag_style", "version_status"},
		castSingle:         castRecord("version"),
		possibleValues:     versionRecords,
		possibleCustomVals: recordPossibleCustomValueOptions("version"),
		validateValue:      validateRecordList,
		beforeSave:         func(env *Env, cf *CustomField) { compactListSetting(cf, "version_status") },
		editTag:            listEditTag,
		bulkEditTag:        listBulkEditTag,
		FilterType:         "list_optional",
		groupable:          true,
	}
	// AttachmentFormat は 'attachment'。
	AttachmentFormat = &Format{
		Name: "attachment", Label: "label_attachment", IsFilterSupported: false, BulkEditSupported: false,
		FormPartial: "custom_fields/formats/attachment", ChangeNoDetails: true, FieldAttributes: []string{"extensions_allowed"},
		castSingle: castRecord("attachment"),
		// TODO(attachment): set_custom_field_value（トークン・アップロード）と after_save_custom_value は添付機能の実装時に移植する。
		validateValue: func(env *Env, cv *CustomValue) []string { return nil },
		editTag: func(f *Format, env *Env, tagID, tagName string, cv *CustomValue, opts *rails.Hash) rails.HTML {
			out := rails.HiddenFieldTag(tagName+"[blank]", "", nil)
			if env != nil && env.AttachmentForm != nil {
				out += env.AttachmentForm(tagName, cv)
			}
			return out
		},
		FilterType: "string",
	}
	// ProgressbarFormat は 'progressbar'（Numeric）。
	ProgressbarFormat = &Format{
		Name: "progressbar", Label: "label_progressbar", IsFilterSupported: true, BulkEditSupported: true,
		FormPartial: "custom_fields/formats/progressbar", FieldAttributes: []string{"thousands_delimiter", "ratio_interval"},
		castSingle: func(env *Env, cf *CustomField, v string, c *Customized) any {
			return min(max(RubyToI(v), 0), 100)
		},
		validateSingle: func(env *Env, cf *CustomField, v string, c *Customized) []string {
			errs := validateUnbounded(env, cf, v, c)
			if !digitsOnlyRe.MatchString(trimSpace(v)) {
				errs = append(errs, env.l("activerecord.errors.messages.not_a_number"))
			}
			if n := RubyToI(v); n < 0 || n > 100 {
				errs = append(errs, env.l("activerecord.errors.messages.invalid"))
			}
			return errs
		},
		beforeSave: func(env *Env, cf *CustomField) {
			if isBlank(cf.SettingValue("ratio_interval")) {
				cf.SetSetting("ratio_interval", strconv.Itoa(env.doneRatioInterval()))
			}
		},
		formatted:   formattedProgressbar,
		editTag:     progressbarEditTag,
		bulkEditTag: progressbarBulkEditTag,
		FilterType:  "integer",
		numeric:     true,
		groupable:   true,
	}
)

func init() {
	for _, f := range []*Format{StringFormat, TextFormat, LinkFormat, IntFormat, FloatFormat, DateFormat, ListFormat,
		BoolFormat, EnumerationFormat, UserFormat, VersionFormat, AttachmentFormat, ProgressbarFormat} {
		register(f)
	}
}

// recordListKinds は RecordList.customized_class_names。
var recordListKinds = []OwnerKind{KindIssue, KindTimeEntry, KindVersion, KindDocument, KindProject}

var (
	intRe        = regexp.MustCompile(`(?m)^[+-]?\d+$`)
	dateRe       = regexp.MustCompile(`(?m)^\d{4}-\d{2}-\d{2}$`)
	digitsOnlyRe = regexp.MustCompile(`(?m)^\d*$`)
)

// ---------------------------------------------------------------- 値の設定・キャスト

// SetValue は set_custom_field_value(custom_field, custom_field_value, value)。
func (f *Format) SetValue(env *Env, cf *CustomField, cv *CustomValue, value any) any {
	if f.setValue != nil {
		return f.setValue(env, cf, cv, value)
	}
	if isArray(value) {
		var out []string
		for _, v := range wrap(value) {
			if v != "" && !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
		if len(out) == 0 {
			out = []string{""}
		}
		return out
	}
	s := rails.ToS(value)
	if cf.FieldFormat == "float" {
		if env != nil && env.NormalizeFloat != nil {
			return env.NormalizeFloat(s)
		}
	}
	return s
}

// Cast は cast_value(custom_field, value, customized)（blank は nil、配列は各要素をキャストして compact.sort）。
func (f *Format) Cast(env *Env, cf *CustomField, value any, customized *Customized) any {
	if isBlank(value) {
		return nil
	}
	if isArray(value) {
		var out []any
		for _, v := range wrap(value) {
			if c := f.CastSingle(env, cf, v, customized); c != nil {
				out = append(out, c)
			}
		}
		sortValues(out)
		return out
	}
	return f.CastSingle(env, cf, rails.ToS(value), customized)
}

// CastSingle は cast_single_value。
func (f *Format) CastSingle(env *Env, cf *CustomField, v string, customized *Customized) any {
	if f.castSingle != nil {
		return f.castSingle(env, cf, v, customized)
	}
	return v
}

// CastCustomValue は cast_custom_value。
func (f *Format) CastCustomValue(env *Env, cv *CustomValue) any {
	return f.Cast(env, cv.CustomField, cv.Value, cv.Customized)
}

func castRecord(format string) func(env *Env, cf *CustomField, v string, c *Customized) any {
	return func(env *Env, cf *CustomField, v string, c *Customized) any {
		if v == "" || env == nil || env.RecordOptions == nil {
			return nil
		}
		id := strconv.FormatInt(RubyToI(v), 10)
		for _, o := range env.RecordOptions(format, []string{id}) {
			if o.Value == id {
				return o
			}
		}
		return nil
	}
}

// sortValues は Array#sort（同種の値のみ。レコードはラベル順で近似する）。
func sortValues(vs []any) {
	slices.SortStableFunc(vs, func(a, b any) int {
		switch x := a.(type) {
		case int64:
			if y, ok := b.(int64); ok {
				return cmpOrdered(x, y)
			}
		case float64:
			if y, ok := b.(float64); ok {
				return cmpOrdered(x, y)
			}
		case string:
			if y, ok := b.(string); ok {
				return strings.Compare(x, y)
			}
		case time.Time:
			if y, ok := b.(time.Time); ok {
				return x.Compare(y)
			}
		case Option:
			if y, ok := b.(Option); ok {
				return strings.Compare(x.Label, y.Label)
			}
		}
		return 0
	})
}

func cmpOrdered[T int64 | float64](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// ---------------------------------------------------------------- 選択肢

// PossibleValuesOptions は possible_values_options(custom_field, object)。
// object は *Customized、[]*Customized（一括編集。各結果の積集合）または nil。
func (f *Format) PossibleValuesOptions(env *Env, cf *CustomField, object any) []Option {
	if f.possibleValues == nil {
		return nil
	}
	return f.possibleValues(env, cf, object)
}

// PossibleCustomValueOptions は possible_custom_value_options(custom_value)。
func (f *Format) PossibleCustomValueOptions(env *Env, cv *CustomValue) []Option {
	if f.possibleCustomVals != nil {
		return f.possibleCustomVals(env, cv)
	}
	return f.PossibleValuesOptions(env, cv.CustomField, customizedObject(cv.Customized))
}

// PossibleValuesOptionsFor は CustomField#possible_values_options(object)（object が配列なら各要素の結果の積集合）。
func PossibleValuesOptionsFor(env *Env, cf *CustomField, objects []*Customized) []Option {
	f := FindFormat(cf.FieldFormat)
	if objects == nil {
		return f.PossibleValuesOptions(env, cf, nil)
	}
	var acc []Option
	for i, o := range objects {
		opts := f.PossibleValuesOptions(env, cf, o)
		if i == 0 {
			acc = opts
			continue
		}
		acc = slices.DeleteFunc(acc, func(x Option) bool { return !slices.Contains(opts, x) })
	}
	return acc
}

func customizedObject(c *Customized) any {
	if c == nil {
		return nil
	}
	return c
}

func containsValue(opts []Option, v string) bool {
	for _, o := range opts {
		if o.Value == v {
			return true
		}
	}
	return false
}

// recordPossibleCustomValueOptions は RecordList#possible_custom_value_options（value_was の欠けている値を補う）。
func recordPossibleCustomValueOptions(format string) func(env *Env, cv *CustomValue) []Option {
	return func(env *Env, cv *CustomValue) []Option {
		f := Find(format)
		opts := f.PossibleValuesOptions(env, cv.CustomField, customizedObject(cv.Customized))
		var missing []string
		for _, v := range wrap(cv.ValueWas) {
			if !isBlank(v) && !containsValue(opts, v) {
				missing = append(missing, v)
			}
		}
		if len(missing) > 0 && env != nil && env.RecordOptions != nil {
			ids := make([]string, len(missing))
			for i, m := range missing {
				ids[i] = strconv.FormatInt(RubyToI(m), 10)
			}
			opts = append(opts, env.RecordOptions(format, ids)...)
		}
		return opts
	}
}

func validateRecordList(env *Env, cv *CustomValue) []string {
	f := FindFormat(cv.CustomField.FieldFormat)
	opts := f.PossibleCustomValueOptions(env, cv)
	for _, v := range nonEmpty(cv.Value) {
		if !containsValue(opts, v) {
			return []string{env.l("activerecord.errors.messages.inclusion")}
		}
	}
	return nil
}

func projectsOf(object any) (ids []int64, ok bool) {
	switch o := object.(type) {
	case []*Customized:
		for _, c := range o {
			if c != nil && c.ProjectID != 0 && !slices.Contains(ids, c.ProjectID) {
				ids = append(ids, c.ProjectID)
			}
		}
		return ids, true
	case *Customized:
		if o != nil && o.ProjectID != 0 {
			return []int64{o.ProjectID}, false
		}
	}
	return nil, false
}

func intersectOptions(lists [][]Option) []Option {
	if len(lists) == 0 {
		return nil
	}
	acc := lists[0]
	for _, l := range lists[1:] {
		acc = slices.DeleteFunc(slices.Clone(acc), func(x Option) bool { return !slices.Contains(l, x) })
	}
	return acc
}

// userRecords は UserFormat#possible_values_records を [name, id] で返す。
func userRecords(env *Env, cf *CustomField, object any) []Option {
	if env == nil || env.ProjectUsers == nil {
		return nil
	}
	var roleIDs []int64
	if roles := cf.SettingList("user_role"); roles != nil {
		for _, r := range roles {
			if trimSpace(r) != "" {
				roleIDs = append(roleIDs, RubyToI(r))
			}
		}
	}
	projects, isArray := projectsOf(object)
	if isArray {
		var lists [][]Option
		for _, p := range projects {
			lists = append(lists, env.ProjectUsers(p, roleIDs))
		}
		return intersectOptions(lists)
	}
	if len(projects) == 1 {
		return env.ProjectUsers(projects[0], roleIDs)
	}
	return nil
}

// versionRecords は VersionFormat#possible_values_options。
func versionRecords(env *Env, cf *CustomField, object any) []Option {
	if env == nil {
		return nil
	}
	var statuses []string
	if st := cf.SettingList("version_status"); st != nil {
		for _, s := range st {
			if trimSpace(s) != "" {
				statuses = append(statuses, s)
			}
		}
	}
	projects, isArray := projectsOf(object)
	switch {
	case isArray:
		if env.SharedVersions == nil {
			return nil
		}
		var lists [][]Option
		for _, p := range projects {
			// 配列のときは version_status で絞らない（possible_values_records(custom_field, project) の再帰で all_statuses = false
			// になるため絞る）
			lists = append(lists, env.SharedVersions(p, statuses))
		}
		return intersectOptions(lists)
	case len(projects) == 1:
		if env.SharedVersions == nil {
			return nil
		}
		return env.SharedVersions(projects[0], statuses)
	case object == nil || object == (*Customized)(nil):
		if env.SystemVersions == nil {
			return nil
		}
		return env.SystemVersions(statuses)
	}
	return nil
}

// compactListSetting は before_custom_field_save の user_role / version_status の正規化（to_s して空を除く）。
func compactListSetting(cf *CustomField, key string) {
	if l := cf.SettingList(key); l != nil {
		out := []string{}
		for _, s := range l {
			if s != "" {
				out = append(out, s)
			}
		}
		outAny := make([]any, len(out))
		for i, s := range out {
			outAny[i] = s
		}
		cf.SetSetting(key, outAny)
	}
}

// ---------------------------------------------------------------- 検証

// ValidateCustomField は validate_custom_field(custom_field)（書式固有の検証。URL パターンのスキーム検査を含む）。
func (f *Format) ValidateCustomField(env *Env, cf *CustomField) []FieldError {
	if f.validateField != nil {
		// ListFormat は Base の url_pattern 検査を呼ばない（super しない）
		return f.validateField(env, cf)
	}
	pattern := cf.URLPattern()
	if trimSpace(pattern) != "" && !URIWithSafeScheme(urlPatternWithoutTokens(pattern)) {
		return []FieldError{{"url_pattern", "invalid"}}
	}
	return nil
}

// ValidateValue は validate_custom_value(custom_value)（空でない各値を validate_single_value で検証、重複を除く）。
func (f *Format) ValidateValue(env *Env, cv *CustomValue) []string {
	if f.validateValue != nil {
		return f.validateValue(env, cv)
	}
	var errs []string
	for _, v := range nonEmpty(cv.Value) {
		for _, e := range f.ValidateSingleValue(env, cv.CustomField, v, cv.Customized) {
			if !slices.Contains(errs, e) {
				errs = append(errs, e)
			}
		}
	}
	return errs
}

// ValidateSingleValue は validate_single_value。
func (f *Format) ValidateSingleValue(env *Env, cf *CustomField, v string, customized *Customized) []string {
	if f.validateSingle != nil {
		return f.validateSingle(env, cf, v, customized)
	}
	return nil
}

// validateUnbounded は Unbounded#validate_single_value（正規表現・最小/最大長）。
func validateUnbounded(env *Env, cf *CustomField, v string, c *Customized) []string {
	var errs []string
	if re := cf.RegexpString(); trimSpace(re) != "" {
		if r, err := CompileRegexp(re); err != nil || !r.MatchString(v) {
			errs = append(errs, env.l("activerecord.errors.messages.invalid"))
		}
	}
	n := utf8.RuneCountInString(v)
	if cf.MinLength != nil && n < *cf.MinLength {
		errs = append(errs, env.l("activerecord.errors.messages.too_short", map[string]any{"count": *cf.MinLength}))
	}
	if cf.MaxLength != nil && *cf.MaxLength > 0 && n > *cf.MaxLength {
		errs = append(errs, env.l("activerecord.errors.messages.too_long", map[string]any{"count": *cf.MaxLength}))
	}
	return errs
}

// CompileRegexp は Regexp.new(source) の近似（Ruby の ^ $ は行頭・行末なので (?m) を付ける）。
// Go の RE2 で表せない構文（後方参照・先読み等）はエラーになる。
func CompileRegexp(source string) (*regexp.Regexp, error) {
	return regexp.Compile("(?m)" + source)
}

// ---------------------------------------------------------------- 保存前処理

// BeforeSave は before_custom_field_save(custom_field)。
func (f *Format) BeforeSave(env *Env, cf *CustomField) {
	if f.beforeSave != nil {
		f.beforeSave(env, cf)
	}
}

// ValueFromKeyword は value_from_keyword(custom_field, keyword, object)（メール受信のキーワード → 値）。
// 複数値なら []string、単一値なら string（見つからなければ nil）を返す。
func (f *Format) ValueFromKeyword(env *Env, cf *CustomField, keyword string, object any) any {
	if f.valueFromKeyword != nil {
		return f.valueFromKeyword(f, env, cf, keyword, object)
	}
	opts := f.PossibleValuesOptions(env, cf, object)
	if len(opts) == 0 {
		return keyword
	}
	return parseKeyword(cf, keyword, func(k string) (string, bool) {
		for _, o := range opts {
			if strings.EqualFold(k, o.Label) {
				return o.Value, true
			}
		}
		return "", false
	})
}

// parseKeyword は Base#parse_keyword（複数値はカンマ区切りを最長一致で分割する）。
func parseKeyword(cf *CustomField, keyword string, find func(k string) (string, bool)) any {
	if !cf.Multiple {
		if v, ok := find(trimSpace(keyword)); ok {
			return v
		}
		return nil
	}
	values := []string{}
	for len(keyword) > 0 {
		k := keyword
		for {
			if v, ok := find(trimSpace(k)); ok {
				values = append(values, v)
				break
			}
			i := strings.LastIndex(k, ",")
			if i < 0 {
				break
			}
			k = k[:i]
		}
		// keyword.slice!(/\A#{Regexp.escape k},?/)
		keyword = strings.TrimPrefix(keyword, k)
		keyword = strings.TrimPrefix(keyword, ",")
	}
	return values
}

// likeMatch は LOWER(name) LIKE LOWER(k)（% と _ のワイルドカードを含む）。
func likeMatch(name, pattern string) bool {
	var b strings.Builder
	b.WriteString("(?is)^")
	for _, r := range pattern {
		switch r {
		case '%':
			b.WriteString(".*")
		case '_':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	return err == nil && re.MatchString(name)
}

// ---------------------------------------------------------------- 数値変換（Ruby 互換）

var kernelFloatRe = regexp.MustCompile(`^[ \t\n\v\f\r]*[+-]?(?:\d+(?:_\d+)*(?:\.\d+(?:_\d+)*)?(?:[eE][+-]?\d+(?:_\d+)*)?|0[xX][0-9a-fA-F]+(?:_[0-9a-fA-F]+)*)[ \t\n\v\f\r]*$`)

// kernelFloat は Kernel.Float(value, exception: false)（厳密な浮動小数点数の解析）。
func kernelFloat(s string) (float64, bool) {
	if !kernelFloatRe.MatchString(s) {
		return 0, false
	}
	t := strings.ReplaceAll(trimSpace(s), "_", "")
	if strings.Contains(strings.ToLower(t), "0x") {
		neg := strings.HasPrefix(t, "-")
		t = strings.TrimLeft(t, "+-")
		n, err := strconv.ParseInt(t[2:], 16, 64)
		if err != nil {
			return 0, false
		}
		if neg {
			n = -n
		}
		return float64(n), true
	}
	f, err := strconv.ParseFloat(t, 64)
	return f, err == nil
}

// toDate は String#to_date（Date.parse の主要な形式）。
func toDate(s string) (time.Time, bool) {
	s = trimSpace(s)
	for _, layout := range []string{"2006-01-02", "2006-1-2", "2006/01/02", "2006/1/2", "20060102"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
