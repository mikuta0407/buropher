// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package customfield

import (
	"strconv"

	"github.com/mikuta0407/buropher/internal/view/rails"
)

// EditTag は edit_tag(view, tag_id, tag_name, custom_value, options)（チケット等の編集フォームの入力要素）。
// opts は custom_field_tag が渡す {class:, placeholder:, data:}（nil 可）。
func (f *Format) EditTag(env *Env, tagID, tagName string, cv *CustomValue, opts *rails.Hash) rails.HTML {
	if opts == nil {
		opts = rails.NewHash()
	}
	if f.editTag != nil {
		return f.editTag(f, env, tagID, tagName, cv, opts)
	}
	return rails.TextFieldTag(tagName, cv.Value, merge(opts, "id", tagID))
}

// BulkEditTag は bulk_edit_tag(view, tag_id, tag_name, custom_field, objects, value, options)（一括編集）。
func (f *Format) BulkEditTag(env *Env, tagID, tagName string, cf *CustomField, objects []*Customized, value any, opts *rails.Hash) rails.HTML {
	if opts == nil {
		opts = rails.NewHash()
	}
	if f.bulkEditTag != nil {
		return f.bulkEditTag(f, env, tagID, tagName, cf, objects, value, opts)
	}
	return rails.TextFieldTag(tagName, value, merge(opts, "id", tagID)) + bulkClearTag(env, tagID, tagName, cf, value)
}

// merge は options.merge(k => v, ...)（既存のキーは位置を保って上書き）。
func merge(h *rails.Hash, kv ...any) *rails.Hash {
	out := h.Clone()
	for i := 0; i+1 < len(kv); i += 2 {
		out.Set(kv[i].(string), kv[i+1])
	}
	return out
}

// bulkClearTag は Base#bulk_clear_tag。
func bulkClearTag(env *Env, tagID, tagName string, cf *CustomField, value any) rails.HTML {
	if cf.IsRequired {
		return ""
	}
	cb := rails.CheckBoxTag(tagName, "__none__", rails.ToS(value) == "__none__",
		rails.NewHash("id", nil, "data", rails.NewHash("disables", "#"+tagID)))
	return rails.ContentTag("label", cb+rails.H(env.l("button_clear")), rails.NewHash("class", "inline"))
}

func textEditTag(f *Format, env *Env, tagID, tagName string, cv *CustomValue, opts *rails.Hash) rails.HTML {
	return rails.TextAreaTag(tagName, cv.Value, merge(opts, "id", tagID, "rows", 8))
}

func textBulkEditTag(f *Format, env *Env, tagID, tagName string, cf *CustomField, objects []*Customized, value any, opts *rails.Hash) rails.HTML {
	return rails.TextAreaTag(tagName, value, merge(opts, "id", tagID, "rows", 8)) + "<br />" +
		bulkClearTag(env, tagID, tagName, cf, value)
}

func calendarFor(env *Env, id string) rails.HTML {
	if env != nil && env.CalendarFor != nil {
		return env.CalendarFor(id)
	}
	return ""
}

func dateEditTag(f *Format, env *Env, tagID, tagName string, cv *CustomValue, opts *rails.Hash) rails.HTML {
	return rails.DateFieldTag(tagName, cv.Value, merge(opts, "id", tagID, "size", 10)) + calendarFor(env, tagID)
}

func dateBulkEditTag(f *Format, env *Env, tagID, tagName string, cf *CustomField, objects []*Customized, value any, opts *rails.Hash) rails.HTML {
	return rails.DateFieldTag(tagName, value, merge(opts, "id", tagID, "size", 10)) + calendarFor(env, tagID) +
		bulkClearTag(env, tagID, tagName, cf, value)
}

// optionPairs は []Option を options_for_select のコンテナにする。
func optionPairs(opts []Option) []any {
	out := make([]any, len(opts))
	for i, o := range opts {
		out[i] = o.Pair()
	}
	return out
}

// selectedValue は options_for_select の selected（配列ならそのまま）。
func selectedValue(v any) any {
	if isArray(v) {
		ws := wrap(v)
		out := make([]any, len(ws))
		for i, s := range ws {
			out[i] = s
		}
		return out
	}
	return v
}

func listEditTag(f *Format, env *Env, tagID, tagName string, cv *CustomValue, opts *rails.Hash) rails.HTML {
	if cv.CustomField.EditTagStyle() == "check_box" {
		return checkBoxEditTag(f, env, tagID, tagName, cv, opts)
	}
	return selectEditTag(f, env, tagID, tagName, cv, opts)
}

// selectEditTag は List#select_edit_tag。
func selectEditTag(f *Format, env *Env, tagID, tagName string, cv *CustomValue, opts *rails.Hash) rails.HTML {
	cf := cv.CustomField
	var blank rails.HTML
	if !cf.Multiple {
		if cf.IsRequired {
			if isBlank(cf.DefaultValueString()) {
				blank = rails.ContentTag("option", "--- "+env.l("actionview_instancetag_blank_option")+" ---", rails.NewHash("value", ""))
			}
		} else {
			blank = rails.ContentTag("option", rails.HTML("&nbsp;"), rails.NewHash("value", ""))
		}
	}
	tags := blank + rails.OptionsForSelect(optionPairs(f.PossibleCustomValueOptions(env, cv)), selectedValue(cv.Value))
	s := rails.SelectTag(tagName, tags, merge(opts, "id", tagID, "multiple", cf.Multiple))
	if cf.Multiple {
		s += rails.HiddenFieldTag(tagName, "", nil)
	}
	return s
}

// checkBoxEditTag は List#check_box_edit_tag（複数値ならチェックボックス、単一値ならラジオボタン）。
func checkBoxEditTag(f *Format, env *Env, tagID, tagName string, cv *CustomValue, opts *rails.Hash) rails.HTML {
	cf := cv.CustomField
	var options []Option
	if !cf.Multiple && !cf.IsRequired {
		options = append(options, Option{"(" + env.l("label_none") + ")", ""})
	}
	options = append(options, f.PossibleCustomValueOptions(env, cv)...)
	var s rails.HTML
	values := wrap(cv.Value)
	for _, o := range options {
		checked := (isArray(cv.Value) && contains(values, o.Value)) || rails.ToS(cv.Value) == o.Value
		var tag rails.HTML
		if cf.Multiple {
			tag = rails.CheckBoxTag(tagName, o.Value, checked, rails.NewHash("id", nil))
		} else {
			tag = rails.RadioButtonTag(tagName, o.Value, checked, rails.NewHash("id", nil))
		}
		s += rails.ContentTag("label", tag+" "+rails.H(o.Label), nil)
	}
	if cf.Multiple {
		s += rails.HiddenFieldTag(tagName, "", rails.NewHash("id", nil))
	}
	css := rails.ToS(opts.Get("class")) + " check_box_group"
	return rails.ContentTag("span", s, merge(opts, "class", css))
}

func boolEditTag(f *Format, env *Env, tagID, tagName string, cv *CustomValue, opts *rails.Hash) rails.HTML {
	switch cv.CustomField.EditTagStyle() {
	case "check_box":
		s := rails.HiddenFieldTag(tagName, "0", rails.NewHash("id", nil))
		s += rails.CheckBoxTag(tagName, "1", rails.ToS(cv.Value) == "1", rails.NewHash("id", tagID))
		return rails.ContentTag("span", s, opts)
	case "radio":
		return checkBoxEditTag(f, env, tagID, tagName, cv, opts)
	default:
		return selectEditTag(f, env, tagID, tagName, cv, opts)
	}
}

// listBulkEditTag は List#bulk_edit_tag。
func listBulkEditTag(f *Format, env *Env, tagID, tagName string, cf *CustomField, objects []*Customized, value any, opts *rails.Hash) rails.HTML {
	var options []Option
	if !cf.Multiple {
		options = append(options, Option{env.l("label_no_change_option"), ""})
	}
	if !cf.IsRequired {
		options = append(options, Option{env.l("label_none"), "__none__"})
	}
	var obj any
	if objects != nil {
		obj = objects
	}
	options = append(options, f.PossibleValuesOptions(env, cf, obj)...)
	return rails.SelectTag(tagName, rails.OptionsForSelect(optionPairs(options), selectedValue(value)),
		merge(opts, "multiple", cf.Multiple))
}

func ratioSteps(cf *CustomField) []any {
	step := int(RubyToI(cf.Setting("ratio_interval")))
	if step <= 0 {
		step = 10
	}
	var out []any
	for r := 0; r <= 100; r += step {
		out = append(out, []any{strconv.Itoa(r) + " %", r})
	}
	return out
}

func progressbarEditTag(f *Format, env *Env, tagID, tagName string, cv *CustomValue, opts *rails.Hash) rails.HTML {
	return rails.SelectTag(tagName, rails.OptionsForSelect(ratioSteps(cv.CustomField), selectedValue(cv.Value)),
		merge(opts, "id", tagID, "style", "width: 75px;"))
}

func progressbarBulkEditTag(f *Format, env *Env, tagID, tagName string, cf *CustomField, objects []*Customized, value any, opts *rails.Hash) rails.HTML {
	container := append([]any{[]any{env.l("label_no_change_option"), ""}}, ratioSteps(cf)...)
	return rails.SelectTag(tagName, rails.OptionsForSelect(container, nil), merge(opts, "id", tagID, "style", "width: 75px;")) +
		bulkClearTag(env, tagID, tagName, cf, value)
}
