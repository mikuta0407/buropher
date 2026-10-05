// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package helper

import (
	"regexp"
	"strconv"
	"strings"
	ttemplate "text/template"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは管理画面（ロール・トラッカー・ステータス・列挙）で使う ApplicationHelper /
// CustomFieldsHelper のヘルパー群（title, reorder_handle, delete_link, toggle_checkboxes_link,
// check_all_links, checked_image, error_messages_for, render_project_nested_lists ...）。

// mastersFuncs は RequestFuncs に追加するテンプレート関数。
func (d *Deps) mastersFuncs(r *view.Render, pg func() *Page) ttemplate.FuncMap {
	return ttemplate.FuncMap{
		"title": func(args ...any) html { return title(r, args...) },
		// deref はポインタの値（nil なら nil）。
		"deref": func(v any) any {
			switch x := v.(type) {
			case *int:
				if x != nil {
					return *x
				}
			case *int64:
				if x != nil {
					return *x
				}
			case *string:
				if x != nil {
					return *x
				}
			default:
				return v
			}
			return nil
		},
		// ternary は Ruby の cond ? a : b。
		"ternary": func(cond, a, b any) any {
			if truthy(cond) {
				return a
			}
			return b
		},
		"reorder_handle": func(url, param string) html {
			return d.reorderHandle(pg(), url, param)
		},
		"delete_link": func(url string, args ...any) html {
			return d.deleteLink(pg(), url, optHash(args))
		},
		"remove_link": func(url string, args ...any) html {
			return d.removeLink(pg(), url, optHash(args))
		},
		"toggle_checkboxes_link": func(selector string, args ...any) html {
			return d.toggleCheckboxesLink(pg(), selector, optHash(args))
		},
		"check_all_links": func(formName string) html { return checkAllLinks(pg(), formName) },
		"link_to_function": func(name any, function string, args ...any) html {
			return linkToFunction(name, function, optHash(args))
		},
		"checked_image": func(args ...any) html {
			if len(args) > 0 && !truthy(args[0]) {
				return ""
			}
			return d.checkedImage(pg())
		},
		"error_messages_for": func(errs ...any) html { return errorMessagesFor(d, pg(), errs...) },
		"l_or_humanize": func(s any, prefix string) string {
			p := pg()
			if p.Loc == nil {
				return rails.Humanize(rails.ToS(s))
			}
			return p.Loc.LOrHumanize(rails.ToS(s), prefix)
		},
		"role_name": func(role *domain.Role) string { return RoleName(pg(), role) },
		"render_project_nested_check_boxes": func(name string, projects []*domain.Project, selected []int64) html {
			return renderProjectNestedCheckBoxes(pg(), name, projects, selected)
		},
		"export_csv_encoding_select_tag":  func() html { return exportCSVEncodingSelectTag(pg()) },
		"export_csv_separator_select_tag": func() html { return exportCSVSeparatorSelectTag(pg()) },
		"custom_field_tag_with_label": func(name string, v *domain.CustomFieldValue) html {
			return customFieldTagWithLabel(pg(), name, v)
		},
	}
}

// title は ApplicationHelper#title（[テキスト, URL] の配列はリンクにし、html_title も設定する）。
func title(r *view.Render, args ...any) html {
	var parts []string
	titles := make([]any, 0, len(args))
	for _, a := range args {
		if xs, ok := a.([]any); ok && len(xs) >= 2 {
			parts = append(parts, string(rails.LinkTo(xs[0], xs[1], nil)))
		} else {
			parts = append(parts, string(rails.H(rails.ToS(a))))
		}
	}
	for i := len(args) - 1; i >= 0; i-- {
		a := args[i]
		if xs, ok := a.([]any); ok && len(xs) > 0 {
			titles = append(titles, rails.ToS(xs[0]))
		} else {
			titles = append(titles, rails.ToS(a))
		}
	}
	if r != nil {
		// html_title args.reverse.map {...}（Redmine は配列 1 つを渡すため、空の要素も " - " で連結される）
		r.AddTitle(titles)
	}
	return rails.ContentTag("h2", html(strings.Join(parts, " &#187; ")), nil)
}

// reorderHandle は ApplicationHelper#reorder_handle。
func (d *Deps) reorderHandle(p *Page, url, param string) html {
	data := rails.NewHash("reorder_url", url, "reorder_param", param)
	return rails.ContentTag("span", d.spriteIcon(p, "reorder", "", nil),
		rails.NewHash("class", "icon-only icon-sort-handle sort-handle", "data", data, "title", p.l("button_sort")))
}

// deleteLink は ApplicationHelper#delete_link(url, options, button_name)。
func (d *Deps) deleteLink(p *Page, url string, opts *rails.Hash) html {
	buttonName := p.l("button_delete")
	if v, ok := opts.Lookup("button_name"); ok {
		buttonName = rails.ToS(v)
		opts = opts.Except("button_name")
	}
	o := rails.NewHash("method", "delete", "data", rails.NewHash("confirm", p.l("text_are_you_sure")), "class", "icon icon-del").Update(opts)
	return rails.LinkTo(d.spriteIcon(p, "del", buttonName, nil), url, o)
}

// removeLink は ApplicationHelper#remove_link（Redmine 7.0 #34917。関連を外すだけの操作用）。
func (d *Deps) removeLink(p *Page, url string, opts *rails.Hash) html {
	o := rails.NewHash("method", "delete", "data", rails.NewHash("confirm", p.l("text_are_you_sure")), "class", "icon icon-link-break").Update(opts)
	return rails.LinkTo(d.spriteIcon(p, "link-break", p.l("button_remove"), nil), url, o)
}

// linkToFunction は ApplicationHelper#link_to_function。
func linkToFunction(name any, function string, opts *rails.Hash) html {
	o := rails.NewHash("href", "#", "onclick", function+"; return false;").Update(opts)
	return rails.ContentTag("a", name, o)
}

// toggleCheckboxesLink は ApplicationHelper#toggle_checkboxes_link。
func (d *Deps) toggleCheckboxesLink(p *Page, selector string, opts *rails.Hash) html {
	css := "icon icon-checked"
	if c := opts.Get("class"); c != nil {
		css += " " + rails.ToS(c)
	}
	return linkToFunction(d.spriteIcon(p, "checked", "", nil), "toggleCheckboxesBySelector('"+selector+"')",
		rails.NewHash("title", p.l("button_check_all")+" / "+p.l("button_uncheck_all"), "class", css))
}

// checkAllLinks は ApplicationHelper#check_all_links。
func checkAllLinks(p *Page, formName string) html {
	return linkToFunction(p.l("button_check_all"), "checkAll('"+formName+"', true)", nil) +
		" | " +
		linkToFunction(p.l("button_uncheck_all"), "checkAll('"+formName+"', false)", nil)
}

// checkedImage は ApplicationHelper#checked_image(true)。
func (d *Deps) checkedImage(p *Page) html {
	return rails.ContentTag("span", d.spriteIcon(p, "checked", nil, nil), rails.NewHash("class", "icon-only icon-checked"))
}

// errorMessagesFor は ApplicationHelper#error_messages_for（引数は *domain.ValidationErrors）。
func errorMessagesFor(d *Deps, p *Page, objs ...any) html {
	var msgs []string
	tr := func(key string, args ...any) string { return p.l(key, args...) }
	for _, o := range objs {
		if e, ok := o.(*domain.ValidationErrors); ok && e != nil {
			msgs = append(msgs, e.FullMessages(tr)...)
		} else if m, ok := o.(ErrorMessenger); ok && m != nil {
			msgs = append(msgs, m.FullErrorMessages()...)
		}
	}
	return RenderErrorMessages(d, p, msgs)
}

// RenderErrorMessages は ApplicationHelper#render_error_messages。
func RenderErrorMessages(d *Deps, p *Page, errors []string) html {
	if len(errors) == 0 {
		return ""
	}
	var s strings.Builder
	s.WriteString("<div id='errorExplanation'>\n")
	s.WriteString(string(d.noticeIcon(p, "error")))
	s.WriteString("<ul>\n")
	for _, e := range errors {
		s.WriteString("<li>" + rails.EscapeString(e) + "</li>\n")
	}
	s.WriteString("</ul></div>\n")
	return html(s.String())
}

// RoleName は Role#name（組込ロールは label_role_non_member / label_role_anonymous の訳、無ければ DB の名前）。
func RoleName(p *Page, r *domain.Role) string {
	if r == nil {
		return ""
	}
	key := ""
	switch r.Builtin {
	case domain.RoleBuiltinNonMember:
		key = "label_role_non_member"
	case domain.RoleBuiltinAnonymous:
		key = "label_role_anonymous"
	default:
		return r.Name
	}
	if p.Loc == nil || !p.Loc.Bundle.Exists(p.Loc.Lang, key) {
		return r.Name
	}
	return p.l(key)
}

// renderProjectNestedCheckBoxes は
//
//	render_project_nested_lists(projects) do |p|
//	  content_tag('label', check_box_tag(name, p.id, selected.include?(p.id), :id => nil) + ' ' + h(p))
//	end
//
// （ApplicationHelper#render_project_nested_lists。並びと入れ子は lft 相当のツリー順）。
func renderProjectNestedCheckBoxes(p *Page, name string, projects []*domain.Project, selected []int64) html {
	if len(projects) == 0 {
		return ""
	}
	sel := map[int64]bool{}
	for _, id := range selected {
		sel[id] = true
	}
	var ns map[int64]repository.NestedSetValue
	if p.DB != nil {
		var err error
		if ns, err = repository.ProjectNestedSet(p.ctx(), p.DB); err != nil {
			p.logError("project nested set", err)
		}
	}
	items := make([]JumpProject, 0, len(projects))
	byID := map[int64]*domain.Project{}
	for i, pr := range projects {
		v, ok := ns[pr.ID]
		if !ok {
			v = repository.NestedSetValue{Lft: i * 2, Rgt: i*2 + 1}
		}
		items = append(items, JumpProject{ID: pr.ID, Name: pr.Name, Identifier: pr.Identifier, Lft: v.Lft, Rgt: v.Rgt})
		byID[pr.ID] = pr
	}
	sortJump(items)
	isDescendant := func(a, of JumpProject) bool { return of.Lft < a.Lft && a.Rgt < of.Rgt }
	var s strings.Builder
	var ancestors []JumpProject
	for _, it := range items {
		pr := byID[it.ID]
		if len(ancestors) == 0 || isDescendant(it, ancestors[len(ancestors)-1]) {
			root := ""
			if len(ancestors) == 0 {
				root = "root"
			}
			s.WriteString("<ul class='projects " + root + "'>\n")
		} else {
			ancestors = ancestors[:len(ancestors)-1]
			s.WriteString("</li>")
			for len(ancestors) > 0 && !isDescendant(it, ancestors[len(ancestors)-1]) {
				ancestors = ancestors[:len(ancestors)-1]
				s.WriteString("</ul></li>\n")
			}
		}
		classes := "root"
		if len(ancestors) > 0 {
			classes = "child"
		}
		if pr.Archived() {
			classes += " archived"
		}
		s.WriteString("<li class='" + classes + "'><div class='" + classes + "'>")
		content := rails.ContentTag("label",
			rails.CheckBoxTag(name, pr.ID, sel[pr.ID], rails.NewHash("id", nil))+" "+rails.H(pr.Name), nil)
		s.WriteString(string(content))
		s.WriteString("</div>\n")
		ancestors = append(ancestors, it)
	}
	s.WriteString(strings.Repeat("</li></ul>\n", len(ancestors)))
	return html(s.String())
}

func sortJump(items []JumpProject) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].Lft < items[j-1].Lft; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

// exportCSVEncodingSelectTag は ApplicationHelper#export_csv_encoding_select_tag。
func exportCSVEncodingSelectTag(p *Page) html {
	enc := p.l("general_csv_encoding")
	if strings.EqualFold(enc, "UTF-8") {
		return ""
	}
	sel := rails.SelectTag("encoding", rails.OptionsForSelect([]any{"UTF-8", enc}, "UTF-8"), nil)
	return rails.ContentTag("p", rails.ContentTag("label", html(rails.EscapeString(p.l("label_encoding")+" "))+sel, nil), nil)
}

// exportCSVSeparatorSelectTag は ApplicationHelper#export_csv_separator_select_tag。
func exportCSVSeparatorSelectTag(p *Page) html {
	sep := p.l("general_csv_separator")
	options := []any{[]any{p.l("label_comma_char"), ","}, []any{p.l("label_semi_colon_char"), ";"}}
	if sep != "," && sep != ";" {
		options = append(options, []any{sep, sep})
	}
	sel := rails.SelectTag("field_separator", rails.OptionsForSelect(options, sep), nil)
	return rails.ContentTag("p", rails.ContentTag("label", html(rails.EscapeString(p.l("label_fields_separator")+" "))+sel, nil), nil)
}

// ---- カスタムフィールドの入力欄（CustomFieldsHelper / Redmine::FieldFormat の edit_tag の簡易版） ----
// TODO: Redmine::FieldFormat の移植ができたら置き換える（現状は string/text/link/int/float/date/list/bool のみ）。

var tagIDRe = regexp.MustCompile(` id="(.+?)"`)

func customFieldTagWithLabel(p *Page, name string, v *domain.CustomFieldValue) html {
	tag := customFieldTag(p, name, v)
	var forID any
	if ids := tagIDRe.FindAllStringSubmatch(string(tag), -1); len(ids) == 1 {
		forID = ids[0][1]
	}
	cf := v.Field
	var title, css any
	if d := cf.DescriptionString(); d != "" {
		title, css = d, "field-description"
	}
	content := rails.ContentTag("span", cf.Name, rails.NewHash("title", title, "class", css))
	if cf.IsRequired {
		content += html(` <span class="required">*</span>`)
	}
	return rails.ContentTag("label", content, rails.NewHash("for", forID, "class", nil)) + tag
}

func customFieldTag(p *Page, prefix string, v *domain.CustomFieldValue) html {
	cf := v.Field
	id := prefix + "_custom_field_values_" + strconv.FormatInt(cf.ID, 10)
	name := prefix + "[custom_field_values][" + strconv.FormatInt(cf.ID, 10) + "]"
	if cf.Multiple {
		name += "[]"
	}
	var placeholder any
	if d := cf.DescriptionString(); d != "" {
		if cf.FieldFormat != "text" {
			d = strings.ReplaceAll(d, "\n", " ")
		}
		placeholder = d
	}
	opts := rails.NewHash("class", cf.CSSClasses(), "placeholder", placeholder, "data", nil)
	switch cf.FieldFormat {
	case "text":
		return rails.TextAreaTag(name, v.Value(), opts.Update(rails.NewHash("id", id, "rows", 8)))
	case "date":
		return rails.DateFieldTag(name, v.Value(), opts.Update(rails.NewHash("id", id, "size", 10)))
	case "bool":
		choices := [][2]string{{p.l("general_text_Yes"), "1"}, {p.l("general_text_No"), "0"}}
		switch cf.EditTagStyle {
		case "check_box":
			s := rails.HiddenFieldTag(name, "0", rails.NewHash("id", nil)) +
				rails.CheckBoxTag(name, "1", v.Value() == "1", rails.NewHash("id", id))
			return rails.ContentTag("span", s, opts)
		case "radio":
			return checkBoxEditTag(p, name, cf, v, choices, opts)
		}
		return selectEditTag(p, name, id, cf, v, choices, opts)
	case "list":
		var choices [][2]string
		for _, pv := range cf.PossibleValues {
			choices = append(choices, [2]string{pv, pv})
		}
		for _, x := range v.Values {
			if x != "" && !containsChoice(choices, x) {
				choices = append(choices, [2]string{x, x})
			}
		}
		if cf.EditTagStyle == "check_box" {
			return checkBoxEditTag(p, name, cf, v, choices, opts)
		}
		return selectEditTag(p, name, id, cf, v, choices, opts)
	default:
		return rails.TextFieldTag(name, v.Value(), opts.Update(rails.NewHash("id", id)))
	}
}

func containsChoice(cs [][2]string, v string) bool {
	for _, c := range cs {
		if c[1] == v {
			return true
		}
	}
	return false
}

func choicesAny(cs [][2]string) []any {
	out := make([]any, len(cs))
	for i, c := range cs {
		out[i] = []any{c[0], c[1]}
	}
	return out
}

func selectEditTag(p *Page, name, id string, cf *domain.CustomFieldInfo, v *domain.CustomFieldValue, choices [][2]string, opts *rails.Hash) html {
	var blank html
	if !cf.Multiple {
		if cf.IsRequired {
			if cf.DefaultValueString() == "" {
				blank = rails.ContentTag("option", "--- "+p.l("actionview_instancetag_blank_option")+" ---", rails.NewHash("value", ""))
			}
		} else {
			blank = rails.ContentTag("option", html("&nbsp;"), rails.NewHash("value", ""))
		}
	}
	var selected any = v.Value()
	if cf.Multiple {
		sel := make([]any, len(v.Values))
		for i, x := range v.Values {
			sel[i] = x
		}
		selected = sel
	}
	tags := blank + rails.OptionsForSelect(choicesAny(choices), selected)
	s := rails.SelectTag(name, tags, opts.Update(rails.NewHash("id", id, "multiple", cf.Multiple)))
	if cf.Multiple {
		s += rails.HiddenFieldTag(name, "", nil)
	}
	return s
}

func checkBoxEditTag(p *Page, name string, cf *domain.CustomFieldInfo, v *domain.CustomFieldValue, choices [][2]string, opts *rails.Hash) html {
	var all [][2]string
	if !cf.Multiple && !cf.IsRequired {
		all = append(all, [2]string{"(" + p.l("label_none") + ")", ""})
	}
	all = append(all, choices...)
	var s html
	for _, c := range all {
		checked := false
		for _, x := range v.Values {
			if x == c[1] {
				checked = true
			}
		}
		if !cf.Multiple && v.Value() == c[1] {
			checked = true
		}
		var tag html
		if cf.Multiple {
			tag = rails.CheckBoxTag(name, c[1], checked, rails.NewHash("id", nil))
		} else {
			tag = rails.RadioButtonTag(name, c[1], checked, rails.NewHash("id", nil))
		}
		s += rails.ContentTag("label", tag+" "+rails.H(c[0]), nil)
	}
	if cf.Multiple {
		s += rails.HiddenFieldTag(name, "", rails.NewHash("id", nil))
	}
	o := opts.Clone()
	o.Set("class", rails.ToS(opts.Get("class"))+" check_box_group")
	return rails.ContentTag("span", s, o)
}
