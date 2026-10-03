// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package helper

import (
	"net/url"
	"strconv"
	"strings"
	ttemplate "text/template"

	"github.com/mikuta0407/buropher/internal/customfield"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// CustomFieldModel はフォーム用のラッパーからカスタムフィールドを取り出すためのインターフェース。
type CustomFieldModel interface {
	CustomFieldModel() *customfield.CustomField
}

func toCustomField(v any) *customfield.CustomField {
	switch x := v.(type) {
	case *customfield.CustomField:
		return x
	case CustomFieldModel:
		if x == nil {
			return nil
		}
		return x.CustomFieldModel()
	}
	return nil
}

// VersionStatuses は Version::VERSION_STATUSES。
var VersionStatuses = []string{"open", "locked", "closed"}

func (h adminH) customFieldFuncs() ttemplate.FuncMap {
	return ttemplate.FuncMap{
		"custom_field_title":                 h.customFieldTitle,
		"select_type_radio_buttons":          h.selectTypeRadioButtons,
		"custom_field_formats_for_select":    h.customFieldFormatsForSelect,
		"edit_tag_style_tag":                 h.editTagStyleTag,
		"render_custom_field_format_partial": h.renderCustomFieldFormatPartial,
		"version_statuses":                   func() []string { return VersionStatuses },
		"custom_field_format_label": func(v any) string {
			if cf := toCustomField(v); cf != nil {
				return customfield.FindFormat(cf.FieldFormat).Label
			}
			return ""
		},
		"custom_field_project_nested_lists": h.customFieldProjectNestedLists,
		// prepend_list は [x] + list。
		"prepend_list": func(x any, rest []any) []any { return append([]any{x}, rest...) },
		// enumeration_options は enumerations.map {|v| [v.name, v.id.to_s]}。
		"enumeration_options": func(es []*customfield.Enumeration) []any {
			out := make([]any, len(es))
			for i, e := range es {
				out[i] = []any{e.Name, strconv.FormatInt(e.ID, 10)}
			}
			return out
		},
	}
}

// customFieldTitle は CustomFieldsHelper#custom_field_title(custom_field)。
func (h adminH) customFieldTitle(v any) html {
	cf := toCustomField(v)
	items := []any{[]any{h.l("label_custom_field_plural"), "/custom_fields"}}
	if cf != nil {
		if t := cf.Type(); t != nil {
			items = append(items, []any{h.l(t.TypeName), "/custom_fields?tab=" + url.QueryEscape(t.ClassName)})
		}
	}
	if cf == nil || cf.ID == 0 {
		items = append(items, h.l("label_custom_field_new"))
	} else {
		items = append(items, cf.Name)
	}
	return h.title(items...)
}

// selectTypeRadioButtons は CustomFieldsHelper#select_type_radio_buttons(default_type)。
func (h adminH) selectTypeRadioButtons(defaultType any) html {
	def := rails.ToS(defaultType)
	if customfield.TypeByClass(def) == nil {
		def = "IssueCustomField"
	}
	parts := make([]string, 0, len(customfield.Types))
	for _, t := range customfield.Types {
		parts = append(parts, string(rails.ContentTag("label",
			rails.RadioButtonTag("type", t.ClassName, t.ClassName == def, nil)+rails.H(h.l(t.TabLabel)),
			rails.NewHash("style", "display:block;"))))
	}
	return html(strings.Join(parts, "\n"))
}

// customFieldFormatsForSelect は custom_field_formats_for_select(custom_field)。
func (h adminH) customFieldFormatsForSelect(v any) []any {
	cf := toCustomField(v)
	var kind customfield.OwnerKind
	if cf != nil {
		kind = cf.OwnerKind
	}
	var out []any
	for _, o := range customfield.AsSelect(h.l, kind) {
		out = append(out, o.Pair())
	}
	return out
}

// editTagStyleTag は edit_tag_style_tag(form, :include_radio => include_radio)。
func (h adminH) editTagStyleTag(f *rails.FormBuilder, includeRadio ...bool) html {
	opts := []any{[]any{h.l("label_drop_down_list"), ""}, []any{h.l("label_checkboxes"), "check_box"}}
	if len(includeRadio) > 0 && includeRadio[0] {
		opts = append(opts, []any{h.l("label_radio_buttons"), "radio"})
	}
	return f.Select("edit_tag_style", opts, rails.NewHash("label", rails.Symbol("label_display")))
}

// renderCustomFieldFormatPartial は render_custom_field_format_partial(form, custom_field)。
func (h adminH) renderCustomFieldFormatPartial(f *rails.FormBuilder, v any) (html, error) {
	cf := toCustomField(v)
	if cf == nil {
		return "", nil
	}
	partial := customfield.FindFormat(cf.FieldFormat).FormPartial
	if partial == "" {
		return "", nil
	}
	return h.r.Partial(partial, map[string]any{"f": f, "custom_field": v})
}

// customFieldProjectNestedLists は _visibility_by_project_selector の
// render_project_nested_lists(Project.all) { |p| label(check_box_tag('custom_field[project_ids][]', ...) + ' ' + p) }。
func (h adminH) customFieldProjectNestedLists(projects []NestedProject, v any) html {
	cf := toCustomField(v)
	return RenderProjectNestedLists(projects, func(p *domain.Project) html {
		checked := cf != nil && cf.HasProject(p.ID)
		return rails.ContentTag("label", rails.CheckBoxTag("custom_field[project_ids][]", p.ID, checked, rails.NewHash("id", nil))+" "+rails.H(p.Name), nil)
	})
}
