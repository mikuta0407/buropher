package helper

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	ttemplate "text/template"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/pagination"
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


func init() {
	registerFuncs(func(d *Deps, r *view.Render, pg func() *Page) ttemplate.FuncMap {
		return ttemplate.FuncMap{
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
			"current_path_with": func(params any) string { return URLWithQuery(pg().requestPath(), params) },
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
			"email_delivery_enabled": func() bool {
				// ActionMailer::Base.perform_deliveries（既定 true）。メール送信の実装は通知機能で行う。
				return true
			},
			"group_name":   func(g *domain.Group) string { return GroupName(pg(), g) },
			"user_name":    func(u *domain.User) string { return pg().userName(u, "") },
			"user_css":     func(u *domain.User) string { return u.CSSClasses() },
			"current_user": func() *domain.User { return pg().User },
			"auth_source_options": func(srcs []domain.AuthSource, internal string) []any {
				out := []any{[]any{internal, ""}}
				for _, s := range srcs {
					out = append(out, []any{s.Name, s.ID})
				}
				return out
			},
			"has_id": func(ids []int64, id int64) bool {
				for _, x := range ids {
					if x == id {
						return true
					}
				}
				return false
			},
			"auto_watch_on_tags": func(selected []string) html { return autoWatchOnTags(pg(), selected) },
			"textarea_font_options": func() []any {
				out := []any{[]any{pg().l("label_font_default"), ""}}
				for _, o := range domain.TextareaFontOptions {
					out = append(out, []any{pg().l("label_font_" + o), o})
				}
				return out
			},
			"history_default_tab_options": func() []any {
				p := pg()
				return []any{[]any{p.l("label_issue_history_notes"), "notes"}, []any{p.l("label_history"), "history"},
					[]any{p.l("label_issue_history_properties"), "properties"}, []any{p.l("label_time_entry_plural"), "time_entries"},
					[]any{p.l("label_associated_revisions"), "changesets"}, []any{p.l("label_last_tab_visited"), "last_tab_visited"}}
			},
			"change_status_link": func(u *domain.User) html { return d.changeStatusLink(pg(), u) },
			"user_edit_title": func(u *domain.User) html {
				// page_title.insert(page_title.rindex(' ') + 1, avatar(@user).to_s)
				t := string(title(r, []any{pg().l("label_user_plural"), "/users"}, u.Login))
				i := strings.LastIndex(t, " ")
				return html(t[:i+1] + string(d.avatar(r, pg(), u, rails.NewHash())) + t[i+1:])
			},
			"index_or_zero": func(m map[int64]int, id int64) int { return m[id] },
			"render_principals_for_new_group_users": func(g *domain.Group, users []*domain.User, pages *pagination.Paginator, count int, q string) html {
				p := pg()
				sel := rails.ContentTag("div", rails.ContentTag("div", d.principalsCheckBoxTags(r, p, "user_ids[]", users), rails.NewHash("id", "principals")),
					rails.NewHash("class", "objects-selection"))
				links := paginationLinksEach(p, pages, false, func(text string, params *rails.Hash, opts *rails.Hash) html {
					qp := params.Clone()
					if q != "" {
						qp.Set("q", q)
					}
					return rails.LinkTo(text, URLWithQuery("/groups/"+strconv.FormatInt(g.ID, 10)+"/autocomplete_for_user.js", qp), rails.NewHash("remote", true))
				})
				return sel + rails.ContentTag("span", links, rails.NewHash("class", "pagination"))
			},
			"user_emails": func(emails []string) html {
				parts := make([]string, len(emails))
				for i, e := range emails {
					parts[i] = string(rails.MailTo(e, nil, rails.NewHash()))
				}
				return html(strings.Join(parts, ", "))
			},
			"deref_time": func(t *time.Time) time.Time {
				if t == nil {
					return time.Time{}
				}
				return *t
			},
			"searchable_auth_sources": func(srcs []domain.AuthSource) bool {
				for _, s := range srcs {
					if s.Searchable {
						return true
					}
				}
				return false
			},
			"capitalize": func(s string) string {
				if s == "" {
					return s
				}
				r := []rune(strings.ToLower(s))
				return strings.ToUpper(string(r[0])) + string(r[1:])
			},
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


// autoWatchOnTags は users/_auto_watch_on の
// pref_fields.collection_check_boxes :auto_watch_on, auto_watch_on_options, :last, :first の出力。
func autoWatchOnTags(p *Page, selected []string) html {
	var b strings.Builder
	b.WriteString(`  <input type="hidden" name="pref[auto_watch_on][]" value="" autocomplete="off" />`)
	for _, o := range domain.AutoWatchOnOptions {
		id := "pref_auto_watch_on_" + o
		checked := any(nil)
		for _, s := range selected {
			if s == o {
				checked = "checked"
			}
		}
		cb := rails.Tag("input", rails.NewHash("type", "checkbox", "value", o, "checked", checked, "name", "pref[auto_watch_on][]", "id", id))
		lbl := rails.ContentTag("label", p.l("label_auto_watch_on_"+o), rails.NewHash("for", id))
		b.WriteString("\n    <p>" + string(cb) + " " + string(lbl) + "</p>\n")
	}
	return html(b.String())
}

// changeStatusLink は UsersHelper#change_status_link。
func (d *Deps) changeStatusLink(p *Page, u *domain.User) html {
	params := p.Params()
	q := rails.NewHash("page", nilIfBlankAny(params.String("page")), "status", nilIfBlankAny(params.String("status")))
	link := func(status int, icon, label string) html {
		qq := q.Clone().Set("user", rails.NewHash("status", status))
		return rails.LinkTo(d.spriteIcon(p, icon, p.l(label), nil), URLWithQuery("/users/"+strconv.FormatInt(u.ID, 10), qq),
			rails.NewHash("method", "put", "class", "icon icon-"+icon))
	}
	switch {
	case u.Locked():
		return link(domain.StatusActive, "unlock", "button_unlock")
	case u.Registered():
		return link(domain.StatusActive, "unlock", "button_activate")
	case p.User == nil || u.ID != p.User.ID:
		return link(domain.StatusLocked, "lock", "button_lock")
	}
	return ""
}

func nilIfBlankAny(s string) any {
	if s == "" {
		return nil
	}
	return s
}



// requestPath は現在のリクエストのパス（url_for で現在のアクションのパスを作る場合に使う）。
func (p *Page) requestPath() string {
	if p == nil || p.Request == nil {
		return "/"
	}
	return p.Request.URL.Path
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
		default:
			// 他の管理画面の検証エラー（*domain.ValidationErrors）
			if s := errorMessagesFor(d, p, o); s != "" {
				return s
			}
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
			if u.Kind.IsGroup() {
				// グループ（ウォッチャー候補など User と Group が混在する一覧）
				add(u.ID, rails.ContentTag("span", d.spriteIcon(p, "group", nil, nil), rails.NewHash("class", "name icon icon-"+strings.ToLower(u.Kind.RedmineType()))), PrincipalUserName(p, u))
				continue
			}
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
	if pr.IsPublic {
		s += " public"
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
		names[i] = RoleName(p, r)
	}
	return strings.Join(names, ", ")
}

var _ = fmt.Sprint
