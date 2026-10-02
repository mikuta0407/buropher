package helper

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	ttemplate "text/template"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/validation"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは管理画面（ユーザー・グループ）で使う ApplicationHelper / UsersHelper /
// GroupsHelper / MembersHelper / EmailAddressesHelper のヘルパーの移植:
// title, render_tabs, delete_link, link_to_function, toggle_checkboxes_link, check_all_links,
// lang_options_for_select, time_zone_options, render_project_nested_lists, link_to_group,
// link_to_context_menu, context_menu, actions_dropdown, error_messages_for, principals_check_box_tags,
// project_css_classes, role_name, roles_to_s など。

// Tab は render_tabs の 1 タブ（{:name, :partial, :label}）。
type Tab struct {
	Name    string
	Partial string
	Label   string
	OnClick string
	URL     string
}

func init() {
	registerFuncs(func(d *Deps, r *view.Render, pg func() *Page) ttemplate.FuncMap {
		return ttemplate.FuncMap{
			"title":          func(args ...any) html { return pageTitle(r, args...) },
			"render_tabs":    func(tabs []Tab, selected ...string) (html, error) { return renderTabs(r, pg(), tabs, selected...) },
			"get_tab_action": getTabAction,
			"tab_link":       func(t Tab, selected string) html { return tabLink(pg(), t, selected) },
			"tab_hidden_style": func(t Tab, selected string) any {
				if t.Name != selected {
					return "display:none"
				}
				return nil
			},
			"cond": func(c bool, a, b any) any {
				if c {
					return a
				}
				return b
			},
			"presence": func(v any) any {
				if rails.IsBlank(v) {
					return nil
				}
				return v
			},
			"current_path_with":      func(params any) string { return URLWithQuery(pg().requestPath(), params) },
			"delete_link":            func(url any, args ...any) html { return d.deleteLink(pg(), url, args...) },
			"link_to_function":       func(name any, fn string, args ...any) html { return linkToFunction(name, fn, optHash(args)) },
			"toggle_checkboxes_link": func(selector string, args ...any) html { return d.toggleCheckboxesLink(pg(), selector, optHash(args)) },
			"check_all_links":        func(form string) html { return checkAllLinks(pg(), form) },
			"lang_options_for_select": func(blank ...bool) [][2]string {
				return langOptionsForSelect(pg(), len(blank) == 0 || blank[0])
			},
			"time_zone_options": func() [][2]string { return i18n.TimeZoneOptions() },
			"render_project_nested_lists": func(projects []*domain.Project, partial string, locals ...map[string]any) (html, error) {
				return renderProjectNestedLists(r, pg(), projects, partial, locals...)
			},
			"link_to_group":        func(g *domain.Group) html { return linkToGroup(pg(), g) },
			"link_to_principal":    func(v any) html { return d.linkToPrincipal(pg(), v) },
			"link_to_context_menu": func() html { return d.linkToContextMenu(pg()) },
			"context_menu":         func() html { return d.contextMenu(r, pg()) },
			"actions_dropdown":     func(content any) html { return d.actionsDropdown(pg(), content) },
			"error_messages_for":   func(objs ...any) html { return d.errorMessagesFor(pg(), objs...) },
			"render_error_messages": func(msgs []string) html {
				return d.renderErrorMessages(pg(), msgs)
			},
			"principals_check_box_tags": func(name string, principals any) html {
				return d.principalsCheckBoxTags(r, pg(), name, principals)
			},
			"project_css_classes": func(p *domain.Project) string { return projectCSSClasses(pg(), p) },
			"role_name":           func(role *domain.Role) string { return roleName(pg(), role) },
			"email_delivery_enabled": func() bool {
				// ActionMailer::Base.perform_deliveries（既定 true）。メール送信の実装は通知機能で行う。
				return true
			},
			"group_name":   func(g *domain.Group) string { return GroupName(pg(), g) },
			"user_name":    func(u *domain.User) string { return pg().userName(u, "") },
			"user_css":     func(u *domain.User) string { return u.CSSClasses() },
			"current_user": func() *domain.User { return pg().User },
			"include_calendar_headers_tags": func() string { d.includeCalendarHeadersTags(r, pg()); return "" },
			"export_csv_encoding_select_tag": func() html { return exportCSVEncodingSelectTag(pg()) },
			"export_csv_separator_select_tag": func() html { return exportCSVSeparatorSelectTag(pg()) },
			// current_path_with_format は OtherFormatsBuilder#link_to_with_query_parameters の URL
			// （現在のパス + .format、クエリは page / format を除く）。
			"current_path_with_format": func(format string) string {
				p := pg()
				qp := p.QueryParameters()
				qp.Delete("page")
				qp.Delete("format")
				return URLWithQuery(strings.TrimSuffix(p.requestPath(), "."+format)+"."+format, qp)
			},
		}
	})
}

// SpriteIcon は sprite_icon(name, label, options)（ハンドラから使う）。
func (d *Deps) SpriteIcon(p *Page, name string, label any, opts *rails.Hash) html {
	return d.spriteIcon(p, name, label, opts)
}

// includeCalendarHeadersTags は ApplicationHelper#include_calendar_headers_tags。
func (d *Deps) includeCalendarHeadersTags(r *view.Render, p *Page) {
	if p.calendarHeadersIncluded {
		return
	}
	p.calendarHeadersIncluded = true
	sow := p.setting("start_of_week")
	if sow == "" {
		sow = p.l("general_first_day_of_week")
		if sow == "" {
			sow = "1"
		}
	}
	n, _ := strconv.Atoi(sow)
	tags := rails.JavascriptTag("var datepickerOptions={dateFormat: 'yy-mm-dd', firstDay: "+strconv.Itoa(n%7)+", "+
		"showOn: 'button', buttonImageOnly: true, buttonImage: '"+d.assetPath("calendar.png")+
		"', showButtonPanel: true, showWeek: true, showOtherMonths: true, "+
		"selectOtherMonths: true, changeMonth: true, changeYear: true, "+
		"beforeShow: beforeShowDatePicker};", nil)
	locale := "en"
	if p.Loc != nil {
		locale = p.Loc.Lang
		if p.Loc.Bundle.Exists(p.Loc.Lang, "jquery.locale") {
			locale = p.l("jquery.locale")
		}
	}
	if locale != "en" {
		tags += d.jsInclude("i18n/datepicker-" + locale + ".js")
	}
	r.ContentFor("header_tags", tags)
}

// exportCSVEncodingSelectTag は ApplicationHelper#export_csv_encoding_select_tag。
func exportCSVEncodingSelectTag(p *Page) html {
	enc := p.l("general_csv_encoding")
	if strings.EqualFold(enc, "UTF-8") {
		return ""
	}
	sel := rails.SelectTag("encoding", rails.OptionsForSelect([]any{"UTF-8", enc}, "UTF-8"), rails.NewHash())
	return rails.ContentTag("p", rails.ContentTag("label", html(rails.H(p.l("label_encoding")+" "))+sel, nil), nil)
}

// exportCSVSeparatorSelectTag は ApplicationHelper#export_csv_separator_select_tag。
func exportCSVSeparatorSelectTag(p *Page) html {
	opts := []any{[]any{p.l("label_comma_char"), ","}, []any{p.l("label_semi_colon_char"), ";"}}
	sep := p.l("general_csv_separator")
	if sep != "," && sep != ";" {
		opts = append(opts, []any{sep, sep})
	}
	sel := rails.SelectTag("field_separator", rails.OptionsForSelect(opts, sep), rails.NewHash())
	return rails.ContentTag("p", rails.ContentTag("label", html(rails.H(p.l("label_fields_separator")+" "))+sel, nil), nil)
}

// pageTitle は ApplicationHelper#title。引数は文字列か [text, url] のリスト（list で渡す）。
func pageTitle(r *view.Render, args ...any) html {
	var strs []string
	var titles []any
	for _, a := range args {
		if xs, ok := a.([]any); ok && len(xs) >= 2 {
			strs = append(strs, string(rails.LinkTo(xs[0], xs[1], rails.NewHash())))
		} else {
			strs = append(strs, string(rails.H(rails.ToS(a))))
		}
	}
	for i := len(args) - 1; i >= 0; i-- {
		if xs, ok := args[i].([]any); ok && len(xs) >= 1 {
			titles = append(titles, rails.ToS(xs[0]))
		} else {
			titles = append(titles, rails.ToS(args[i]))
		}
	}
	if r != nil {
		r.AddTitle(titles...)
	}
	return rails.ContentTag("h2", rails.Raw(strings.Join(strs, " &#187; ")), nil)
}

// renderTabs は ApplicationHelper#render_tabs（common/_tabs を描画する）。
func renderTabs(r *view.Render, p *Page, tabs []Tab, selected ...string) (html, error) {
	if len(tabs) == 0 {
		return rails.ContentTag("p", p.l("label_no_data"), rails.NewHash("class", "nodata")), nil
	}
	sel := p.Params().String("tab")
	if len(selected) > 0 {
		sel = selected[0]
	}
	found := false
	for _, t := range tabs {
		if t.Name == sel {
			found = true
		}
	}
	if !found {
		sel = tabs[0].Name
	}
	return r.Partial("common/tabs", map[string]any{"tabs": tabs, "selected_tab": sel})
}

// tabLink は common/_tabs のタブのリンク
// （link_to l(tab[:label]), (tab[:url] || {:tab => tab[:name]}), id:, class:, onclick:）。
func tabLink(p *Page, t Tab, selected string) html {
	url := t.URL
	if url == "" {
		url = URLWithQuery(p.requestPath(), rails.NewHash("tab", t.Name))
	}
	var class, onclick any
	if t.Name == selected {
		class = "selected"
	}
	if a := getTabAction(t); a != "" {
		onclick = a + "; return false;"
	}
	return rails.LinkTo(p.l(t.Label), url, rails.NewHash("id", "tab-"+t.Name, "class", class, "onclick", onclick))
}

// requestPath は現在のリクエストのパス（url_for で現在のアクションのパスを作る場合に使う）。
func (p *Page) requestPath() string {
	if p == nil || p.Request == nil {
		return "/"
	}
	return p.Request.URL.Path
}

// getTabAction は ApplicationHelper#get_tab_action。
func getTabAction(t Tab) string {
	if t.OnClick != "" {
		return t.OnClick
	}
	if t.Partial != "" {
		return "showTab('" + t.Name + "', this.href)"
	}
	return ""
}

// deleteLink は ApplicationHelper#delete_link(url, options = {}, button_name = l(:button_delete))。
func (d *Deps) deleteLink(p *Page, url any, args ...any) html {
	opts := rails.NewHash()
	name := p.l("button_delete")
	for _, a := range args {
		switch x := a.(type) {
		case *rails.Hash:
			opts = x
		case string:
			name = x
		}
	}
	o := rails.NewHash("method", "delete", "data", rails.NewHash("confirm", p.l("text_are_you_sure")), "class", "icon icon-del").Update(opts)
	return rails.LinkTo(d.spriteIcon(p, "del", name, nil), url, o)
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
func checkAllLinks(p *Page, form string) html {
	return linkToFunction(p.l("button_check_all"), "checkAll('"+form+"', true)", rails.NewHash()) + " | " +
		linkToFunction(p.l("button_uncheck_all"), "checkAll('"+form+"', false)", rails.NewHash())
}

// langOptionsForSelect は ApplicationHelper#lang_options_for_select（[表示名, 値]）。
func langOptionsForSelect(p *Page, blank bool) [][2]string {
	var out [][2]string
	if blank {
		out = append(out, [2]string{"(" + p.l("label_option_auto_lang") + ")", ""})
	}
	if p.Loc != nil {
		out = append(out, p.Loc.Bundle.LanguagesOptions()...)
	}
	return out
}

// renderProjectNestedLists は ApplicationHelper#render_project_nested_lists。
// 各プロジェクトの中身は部分テンプレート partial（locals に project を加えたもの）で描画する。
func renderProjectNestedLists(r *view.Render, p *Page, projects []*domain.Project, partial string, locals ...map[string]any) (html, error) {
	if len(projects) == 0 {
		return "", nil
	}
	ns := map[int64]repository.NestedSetValue{}
	if p.DB != nil {
		var err error
		if ns, err = repository.ProjectNestedSet(p.ctx(), p.DB); err != nil {
			return "", err
		}
	}
	sorted := append([]*domain.Project(nil), projects...)
	sort.SliceStable(sorted, func(i, j int) bool { return ns[sorted[i].ID].Lft < ns[sorted[j].ID].Lft })
	isDescendant := func(a, b *domain.Project) bool {
		return ns[b.ID].Lft < ns[a.ID].Lft && ns[a.ID].Rgt < ns[b.ID].Rgt
	}
	var s strings.Builder
	var ancestors []*domain.Project
	for _, pr := range sorted {
		if len(ancestors) == 0 || isDescendant(pr, ancestors[len(ancestors)-1]) {
			root := ""
			if len(ancestors) == 0 {
				root = "root"
			}
			s.WriteString("<ul class='projects " + root + "'>\n")
		} else {
			ancestors = ancestors[:len(ancestors)-1]
			s.WriteString("</li>")
			for len(ancestors) > 0 && !isDescendant(pr, ancestors[len(ancestors)-1]) {
				ancestors = ancestors[:len(ancestors)-1]
				s.WriteString("</ul></li>\n")
			}
		}
		classes := "child"
		if len(ancestors) == 0 {
			classes = "root"
		}
		if pr.Archived() {
			classes += " archived"
		}
		s.WriteString("<li class='" + classes + "'><div class='" + classes + "'>")
		if partial != "" {
			l := map[string]any{}
			if len(locals) > 0 {
				for k, v := range locals[0] {
					l[k] = v
				}
			}
			l["project"] = pr
			out, err := r.Partial(partial, l)
			if err != nil {
				return "", err
			}
			s.WriteString(string(out))
		} else {
			s.WriteString(string(rails.H(pr.Name)))
		}
		s.WriteString("</div>\n")
		ancestors = append(ancestors, pr)
	}
	s.WriteString(strings.Repeat("</li></ul>\n", len(ancestors)))
	return html(s.String()), nil
}

// GroupName は Group#name（組込グループは翻訳した名前）。
func GroupName(p *Page, g *domain.Group) string {
	switch g.Kind {
	case domain.KindGroupAnonymous:
		return p.l("label_group_anonymous")
	case domain.KindGroupNonMember:
		return p.l("label_group_non_member")
	}
	return g.Name
}

// linkToGroup は ApplicationHelper#link_to_group。
func linkToGroup(p *Page, g *domain.Group) html {
	if g == nil {
		return ""
	}
	name := rails.H(GroupName(p, g))
	if p.admin() {
		return rails.LinkTo(name, "/groups/"+strconv.FormatInt(g.ID, 10)+"/edit", rails.NewHash())
	}
	return name
}

// linkToPrincipal は ApplicationHelper#link_to_principal。
func (d *Deps) linkToPrincipal(p *Page, v any) html {
	switch x := v.(type) {
	case *domain.User:
		return d.linkToUser(p, x, rails.NewHash())
	case *domain.Group:
		name := GroupName(p, x)
		return rails.LinkTo(d.spriteIcon(p, "group", nil, nil)+rails.H(name), "/groups/"+strconv.FormatInt(x.ID, 10), rails.NewHash("class", "group"))
	}
	return rails.H(rails.ToS(v))
}

// linkToContextMenu は ApplicationHelper#link_to_context_menu。
func (d *Deps) linkToContextMenu(p *Page) html {
	return rails.LinkTo(d.spriteIcon(p, "3-bullets", p.l("button_actions"), nil), "#",
		rails.NewHash("title", p.l("button_actions"), "class", "icon-only icon-actions js-contextmenu "))
}

// contextMenu は ApplicationHelper#context_menu（context_menu の JS / CSS を header_tags に加える）。
func (d *Deps) contextMenu(r *view.Render, p *Page) html {
	if p.contextMenuIncluded {
		return ""
	}
	p.contextMenuIncluded = true
	tags := d.jsInclude("context_menu") + d.stylesheetLinkTag(p, "context_menu")
	r.ContentFor("header_tags", tags)
	if p.l("direction") == "rtl" {
		r.ContentFor("header_tags", d.stylesheetLinkTag(p, "context_menu_rtl"))
	}
	return ""
}

// actionsDropdown は ApplicationHelper#actions_dropdown（content は capture した中身）。
func (d *Deps) actionsDropdown(p *Page, content any) html {
	c := rails.ToS(content)
	if !rails.IsPresent(c) {
		return ""
	}
	trigger := rails.ContentTag("span", d.spriteIcon(p, "3-bullets", p.l("button_actions"), nil),
		rails.NewHash("class", "icon-only icon-actions", "title", p.l("button_actions")))
	trigger = rails.ContentTag("span", trigger, rails.NewHash("class", "drdn-trigger"))
	body := rails.ContentTag("div", rails.Raw(c), rails.NewHash("class", "drdn-items"))
	body = rails.ContentTag("div", body, rails.NewHash("class", "drdn-content"))
	return rails.ContentTag("span", trigger+body, rails.NewHash("class", "drdn"))
}

// ErrorsProvider は error_messages_for に渡せるモデル（errors.full_messages を返す）。
type ErrorsProvider interface {
	ValidationErrors() *validation.Errors
}

// errorMessagesFor は ApplicationHelper#error_messages_for。
func (d *Deps) errorMessagesFor(p *Page, objs ...any) html {
	var msgs []string
	for _, o := range objs {
		switch x := o.(type) {
		case ErrorsProvider:
			if e := x.ValidationErrors(); e != nil {
				msgs = append(msgs, e.FullMessages(p.Loc)...)
			}
		case *validation.Errors:
			msgs = append(msgs, x.FullMessages(p.Loc)...)
		case []string:
			msgs = append(msgs, x...)
		}
	}
	return d.renderErrorMessages(p, msgs)
}

// renderErrorMessages は ApplicationHelper#render_error_messages。
func (d *Deps) renderErrorMessages(p *Page, msgs []string) html {
	if len(msgs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<div id='errorExplanation'>\n")
	b.WriteString(string(d.noticeIcon(p, "error")))
	b.WriteString("<ul>\n")
	for _, m := range msgs {
		b.WriteString("<li>" + string(rails.H(m)) + "</li>\n")
	}
	b.WriteString("</ul></div>\n")
	return html(b.String())
}

// principalsCheckBoxTags は ApplicationHelper#principals_check_box_tags。
func (d *Deps) principalsCheckBoxTags(r *view.Render, p *Page, name string, principals any) html {
	var b strings.Builder
	add := func(id int64, icon html, label string) {
		cb := rails.CheckBoxTag(name, id, false, rails.NewHash("id", nil))
		b.WriteString(string(rails.ContentTag("label", cb+icon+rails.H(label), nil)))
	}
	switch xs := principals.(type) {
	case []*domain.User:
		for _, u := range xs {
			icon := d.avatar(r, p, u, rails.NewHash("size", 16))
			if icon == "" {
				icon = rails.ContentTag("span", "", rails.NewHash("class", "name icon icon-user"))
			}
			add(u.ID, icon, p.userName(u, ""))
		}
	case []*domain.Group:
		for _, g := range xs {
			add(g.ID, rails.ContentTag("span", d.spriteIcon(p, "group", nil, nil), rails.NewHash("class", "name icon icon-"+strings.ReplaceAll(g.Kind.RedmineType(), "Group", "group"))), GroupName(p, g))
		}
	}
	return html(b.String())
}

// projectCSSClasses は Project#css_classes。
func projectCSSClasses(p *Page, pr *domain.Project) string {
	s := "project"
	if pr.ParentID == nil {
		s += " root"
	} else {
		s += " child"
	}
	if p.projectLeaf(pr) {
		s += " leaf"
	} else {
		s += " parent"
	}
	if !pr.Active() {
		if pr.Archived() {
			s += " archived"
		} else {
			s += " closed"
		}
	}
	return s
}

// roleName は Role#to_s / name（組込ロールは翻訳した名前）。
func roleName(p *Page, r *domain.Role) string {
	if r == nil {
		return ""
	}
	switch r.Builtin {
	case domain.RoleBuiltinNonMember:
		return p.l("label_role_non_member")
	case domain.RoleBuiltinAnonymous:
		return p.l("label_role_anonymous")
	}
	return r.Name
}

// SortRoles は Role#<=>（builtin、position の順）で並べる。
func SortRoles(roles []*domain.Role) {
	sort.SliceStable(roles, func(i, j int) bool {
		if roles[i].Builtin != roles[j].Builtin {
			return roles[i].Builtin < roles[j].Builtin
		}
		return roles[i].Position < roles[j].Position
	})
}

// RolesToS は roles.sort.collect(&:to_s).join(', ')。
func RolesToS(p *Page, roles []*domain.Role) string {
	rs := append([]*domain.Role(nil), roles...)
	SortRoles(rs)
	names := make([]string, len(rs))
	for i, r := range rs {
		names[i] = roleName(p, r)
	}
	return strings.Join(names, ", ")
}

var _ = fmt.Sprint
