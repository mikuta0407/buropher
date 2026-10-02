package customfield

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// Errors は ActiveModel::Errors（追加順を保つ属性ごとのエラー）。
type Errors struct {
	items []errItem
}

type errItem struct{ attr, msg string }

// Add は errors.add(attr, message)。message は翻訳済みの文。attr が "base" なら full_messages に属性名を付けない。
func (e *Errors) Add(attr, msg string) { e.items = append(e.items, errItem{attr, msg}) }

// Any は errors.any?。
func (e *Errors) Any() bool { return e != nil && len(e.items) > 0 }

// On は errors[attr]。
func (e *Errors) On(attr string) []string {
	if e == nil {
		return nil
	}
	var out []string
	for _, it := range e.items {
		if it.attr == attr {
			out = append(out, it.msg)
		}
	}
	return out
}

// FullMessages は errors.full_messages（human は属性名の表示名。base は文のみ）。
func (e *Errors) FullMessages(human func(attr string) string) []string {
	if e == nil {
		return nil
	}
	out := make([]string, 0, len(e.items))
	for _, it := range e.items {
		if it.attr == "base" {
			out = append(out, it.msg)
			continue
		}
		out = append(out, human(it.attr)+" "+it.msg)
	}
	return out
}

// HumanAttributeName は CustomField.human_attribute_name（url_pattern は url として l("field_url")）。
func HumanAttributeName(env *Env, attr string) string {
	if attr == "url_pattern" {
		attr = "url"
	}
	attr = strings.TrimSuffix(attr, "_id")
	return env.l("field_" + attr)
}

// ApplyFieldRules は before_validation :set_searchable（書式が対応しない searchable / multiple を偽にする）。
func ApplyFieldRules(cf *domain.CustomField) {
	f := MustFind(cf.FieldFormat)
	if !f.info.SearchableSupported {
		cf.Searchable = false
	}
	if !f.info.MultipleSupported {
		cf.Multiple = false
	}
}

// ValidateField は CustomField の検証（validates_presence_of :name, :field_format / validates_uniqueness_of :name /
// validates_length_of :name, :regexp / validates_inclusion_of :field_format / validate_custom_field と
// IssueCustomField・TimeEntryCustomField のロール必須）。set_searchable は先に ApplyFieldRules で適用すること。
// nameTaken は同じ種類に同名のフィールドがあるか（呼び出し側が DB で調べる）。
func ValidateField(env *Env, cf *domain.CustomField, nameTaken bool) *Errors {
	errs := &Errors{}
	msg := func(key string, args ...any) string { return env.l("activerecord.errors.messages."+key, args...) }
	if isBlank(cf.Name) {
		errs.Add("name", msg("blank"))
	}
	if isBlank(cf.FieldFormat) {
		errs.Add("field_format", msg("blank"))
	}
	if nameTaken {
		errs.Add("name", msg("taken"))
	}
	if utf8.RuneCountInString(cf.Name) > 30 {
		errs.Add("name", msg("too_long", map[string]any{"count": 30}))
	}
	if utf8.RuneCountInString(cf.RegexpString()) > 255 {
		errs.Add("regexp", msg("too_long", map[string]any{"count": 255}))
	}
	f := Find(cf.FieldFormat)
	if f == nil {
		errs.Add("field_format", msg("inclusion"))
		f = baseFormat
	}
	// validate_custom_field
	for _, fe := range f.ValidateCustomField(env, cf) {
		errs.Add(fe.Attr, msg(fe.Message))
	}
	if re := cf.RegexpString(); trimSpace(re) != "" {
		if _, err := CompileRegexp(re); err != nil {
			errs.Add("regexp", msg("invalid"))
		}
	}
	if dv := cf.DefaultValueString(); trimSpace(dv) != "" && len(errs.On("regexp")) == 0 {
		for _, m := range ValidateFieldValue(env, cf, dv) {
			errs.Add("default_value", m)
		}
	}
	if (cf.OwnerKind == domain.CFOwnerIssue || cf.OwnerKind == domain.CFOwnerTimeEntry) && !cf.Visible && len(cf.RoleIDs) == 0 {
		errs.Add("base", env.l("label_role_plural")+" "+msg("blank"))
	}
	return errs
}

// ValidateCustomValue は CustomField#validate_custom_value（書式の検証に加え、複数値でない配列と必須の空値を検出する）。
func ValidateCustomValue(env *Env, cv *CustomValue) []string {
	cf := cv.CustomField
	f := MustFind(cf.FieldFormat)
	errs := f.ValidateValue(env, cv)
	if len(errs) > 0 {
		return errs
	}
	if isArray(cv.Value) {
		if !cf.Multiple {
			errs = append(errs, env.l("activerecord.errors.messages.invalid"))
		}
		if cf.IsRequired && !slices.ContainsFunc(wrap(cv.Value), func(s string) bool { return !isBlank(s) }) {
			errs = append(errs, env.l("activerecord.errors.messages.blank"))
		}
	} else if cf.IsRequired && isBlank(cv.Value) {
		errs = append(errs, env.l("activerecord.errors.messages.blank"))
	}
	return errs
}

// ValidateFieldValue は CustomField#validate_field_value(value)（所有者なしの値として検証する）。
func ValidateFieldValue(env *Env, cf *domain.CustomField, value any) []string {
	cv := &CustomValue{CustomField: cf}
	cv.Value = MustFind(cf.FieldFormat).SetValue(env, cf, cv, value)
	return ValidateCustomValue(env, cv)
}

// SetPossibleValues は CustomField#possible_values=（配列なら strip して空を除く、文字列なら改行で分割する）。
func SetPossibleValues(cf *domain.CustomField, v any) {
	var lines []string
	switch x := v.(type) {
	case []string:
		lines = x
	case []any:
		for _, e := range x {
			if e != nil {
				lines = append(lines, rails.ToS(e))
			}
		}
	default:
		lines = splitLines(rails.ToS(v))
	}
	out := []string{}
	for _, l := range lines {
		l = trimSpace(l)
		if l != "" {
			out = append(out, l)
		}
	}
	cf.PossibleValues = out
}

// splitLines は String#split(/[\n\r]+/)。
func splitLines(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' })
}

