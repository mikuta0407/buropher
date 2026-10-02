package helper

import (
	"html/template"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	ttemplate "text/template"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/menu"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは ProjectsHelper / MembersHelper と、プロジェクト画面で使う ApplicationHelper の
// ヘルパー（actions_dropdown, link_to_principal, principal_icon, render_tabs, bookmark_link,
// l_hours, format_hours, uri_with_safe_scheme? ...）。
//
// TODO(dedupe): admin users/groups の移植でも render_tabs / actions_dropdown / link_to_principal を
// 実装しているため、マージ時に 1 つにまとめる。

// Tab は render_tabs に渡すタブ（ProjectsHelper#project_settings_tabs 等の 1 要素）。
type Tab struct {
	Name string
	// Label は表示名（翻訳済み）。
	Label string
	// Partial は部分テンプレート名（空ならリンクのみ）。
	Partial string
	// URL はタブのリンク先（tab[:url] または url_for(:tab => name)）。
	URL string
	// OnClick は tab[:onclick]（空なら partial があるとき showTab(...)）。
	OnClick string
	// Locals は部分テンプレートに渡す追加の locals。
	Locals map[string]any
}

// projectsFuncs は RequestFuncs に追加するテンプレート関数。
func (d *Deps) projectsFuncs(r *view.Render, pg func() *Page) ttemplate.FuncMap {
	return ttemplate.FuncMap{
		"actions_dropdown": func(content any) html { return d.actionsDropdownHTML(pg(), content) },
		"link_to_principal": func(v any, args ...any) html {
			return d.linkToPrincipalHTML(pg(), v, optHash(args))
		},
		"principal_icon": func(v any) html { return d.principalIconHTML(pg(), v) },
		// principal_links は _members_box の
		// principals.collect{|p| link_to_principal(p, :class => p.is_a?(Group) ? 'icon icon-group' : nil)}.join(", ")。
		"principal_links": func(ps []*repository.MemberPrincipal) html {
			p := pg()
			parts := make([]string, len(ps))
			for i, m := range ps {
				o := rails.NewHash()
				if m.Group != nil {
					o.Set("class", "icon icon-group")
				}
				parts[i] = string(d.linkToPrincipalHTML(p, m, o))
			}
			return html(strings.Join(parts, ", "))
		},
		"l_hours": func(h any) string {
			p := pg()
			if p.Loc == nil {
				return rails.ToS(h)
			}
			return p.Loc.LHours(toFloat(h))
		},
		"format_hours": func(h any) string {
			p := pg()
			if p.Loc == nil {
				return rails.ToS(h)
			}
			return p.Loc.FormatHours(toFloat(h))
		},
		"render_tabs": func(tabs []Tab, args ...any) (html, error) {
			sel := ""
			if len(args) > 0 {
				sel = rails.ToS(args[0])
			} else {
				sel = pg().Params().String("tab")
			}
			return renderTabsHTML(r, pg(), tabs, sel)
		},
		"get_tab_action": func(t Tab) any {
			if a := tabAction(t); a != "" {
				return a
			}
			return nil
		},
		"bookmark_link":        func(pr *domain.Project) html { return d.bookmarkLinkHTML(pg(), pr) },
		"uri_with_safe_scheme": URIWithSafeScheme,
		"project_css_classes":  func(pr *domain.Project) string { return ProjectCSSClasses(pg(), pr) },
		"render_projects_for_jump_box": func(projects []JumpProject, selected *domain.Project) html {
			return d.renderProjectsForJumpBox(pg(), &JumpBox{Projects: projects}, selected)
		},
		"format_object": func(v any) any { return formatObjectHTML(pg(), v) },
		"tracker_name_tag": func(t *domain.Tracker) html {
			return rails.ContentTag("span", t.Name, rails.NewHash("title", t.DescriptionString()))
		},
		"custom_field_name_tag": func(cf *domain.CustomFieldInfo) html {
			var title, css any
			if t := cf.DescriptionString(); strings.TrimSpace(t) != "" {
				title, css = t, "field-description"
			}
			return rails.ContentTag("span", cf.Name, rails.NewHash("title", title, "class", css))
		},
	}
}

// actionsDropdownHTML は ApplicationHelper#actions_dropdown（中身が空なら何も出さない）。
func (d *Deps) actionsDropdownHTML(p *Page, content any) html {
	c := rails.ToS(content)
	if strings.TrimSpace(c) == "" {
		return ""
	}
	trigger := rails.ContentTag("span", d.spriteIcon(p, "3-bullets", p.l("button_actions"), nil),
		rails.NewHash("class", "icon-only icon-actions", "title", p.l("button_actions")))
	trigger = rails.ContentTag("span", trigger, rails.NewHash("class", "drdn-trigger"))
	inner := rails.ContentTag("div", html(c), rails.NewHash("class", "drdn-items"))
	inner = rails.ContentTag("div", inner, rails.NewHash("class", "drdn-content"))
	return rails.ContentTag("span", trigger+inner, rails.NewHash("class", "drdn"))
}

// principalOf は User / Group / MemberPrincipal を分解する。
func principalOf(v any) (*domain.User, *domain.Group) {
	switch x := v.(type) {
	case *domain.User:
		return x, nil
	case *domain.Group:
		return nil, x
	case *repository.MemberPrincipal:
		if x == nil {
			return nil, nil
		}
		return x.User, x.Group
	}
	return nil, nil
}

// GroupDisplayName は Group#to_s（組込グループは翻訳した名前）。
func GroupDisplayName(p *Page, g *domain.Group) string {
	switch g.Kind {
	case domain.KindGroupAnonymous:
		return p.l("label_group_anonymous")
	case domain.KindGroupNonMember:
		return p.l("label_group_non_member")
	}
	return g.Name
}

// PrincipalName は Principal#to_s / name。
func PrincipalName(p *Page, v any) string {
	u, g := principalOf(v)
	switch {
	case u != nil:
		return p.userName(u, "")
	case g != nil:
		return GroupDisplayName(p, g)
	}
	return ""
}

// linkToPrincipalHTML は ApplicationHelper#link_to_principal。
func (d *Deps) linkToPrincipalHTML(p *Page, v any, opts *rails.Hash) html {
	u, g := principalOf(v)
	switch {
	case u != nil:
		return d.linkToUser(p, u, opts)
	case g != nil:
		css := "group"
		if c := rails.ToS(opts.Get("class")); c != "" {
			css += " " + c
		}
		return rails.LinkTo(d.spriteIcon(p, "group", nil, nil)+rails.H(GroupDisplayName(p, g)), "/groups/"+strconv.FormatInt(g.ID, 10), rails.NewHash("class", css))
	}
	return rails.H(rails.ToS(v))
}

// principalIconHTML は ApplicationHelper#principal_icon。
func (d *Deps) principalIconHTML(p *Page, v any) html {
	// IconsHelper#principal_icon: グループ（組込グループを含む）のみアイコンを出す
	if _, g := principalOf(v); g != nil {
		return d.spriteIcon(p, "group", nil, nil)
	}
	return ""
}

// tabAction は get_tab_action。
func tabAction(t Tab) string {
	if t.OnClick != "" {
		return t.OnClick
	}
	if t.Partial != "" {
		return "showTab('" + t.Name + "', this.href)"
	}
	return ""
}

// renderTabsHTML は ApplicationHelper#render_tabs。
func renderTabsHTML(r *view.Render, p *Page, tabs []Tab, selected string) (html, error) {
	if len(tabs) == 0 {
		return rails.ContentTag("p", p.l("label_no_data"), rails.NewHash("class", "nodata")), nil
	}
	found := false
	for _, t := range tabs {
		if t.Name == selected {
			found = true
		}
	}
	if !found {
		selected = tabs[0].Name
	}
	return r.Partial("common/tabs", map[string]any{"tabs": tabs, "selected_tab": selected})
}

// bookmarkLinkHTML は ProjectsHelper#bookmark_link。
func (d *Deps) bookmarkLinkHTML(p *Page, pr *domain.Project) html {
	if !p.logged() || p.DB == nil || pr == nil {
		return ""
	}
	ids, err := repository.BookmarkedProjectIDs(p.ctx(), p.DB, p.User.ID)
	if err != nil {
		p.logError("bookmarked project ids", err)
	}
	bookmarked := false
	for _, id := range ids {
		if id == pr.ID {
			bookmarked = true
		}
	}
	css := "icon bookmark "
	var icon, method, text string
	if bookmarked {
		css += "icon-bookmark"
		icon, method, text = "bookmark-delete", "delete", p.l("button_project_bookmark_delete")
	} else {
		css += "icon-bookmark-off"
		icon, method, text = "bookmark-add", "post", p.l("button_project_bookmark")
	}
	return rails.LinkTo(d.spriteIcon(p, icon, text, nil), "/projects/"+pr.Identifier+"/bookmark",
		rails.NewHash("remote", true, "method", method, "class", css))
}

// BookmarkLink は bookmark_link（bookmark.js の応答で使う）。
func (d *Deps) BookmarkLink(p *Page, pr *domain.Project) template.HTML { return d.bookmarkLinkHTML(p, pr) }

// RenderProjectsForJumpBox は render_projects_for_jump_box（autocomplete.js / bookmark.js の応答で使う）。
func (d *Deps) RenderProjectsForJumpBox(p *Page, jb *JumpBox, selected *domain.Project) template.HTML {
	return d.renderProjectsForJumpBox(p, jb, selected)
}

var safeSchemeRe = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9+.\-]*):`)

// URIWithSafeScheme は ApplicationHelper#uri_with_safe_scheme?（スキームなし、または
// Setting / 既定の安全なスキーム http, https, ftp, mailto, なし）。
func URIWithSafeScheme(uri string) bool {
	uri = strings.TrimSpace(uri)
	m := safeSchemeRe.FindStringSubmatch(uri)
	if m == nil {
		// スキームなし（相対 URL）は安全
		_, err := url.Parse(uri)
		return err == nil
	}
	switch strings.ToLower(m[1]) {
	case "http", "https", "ftp", "mailto":
		_, err := url.Parse(uri)
		return err == nil
	}
	return false
}

// ProjectCSSClasses は Project#css_classes。
func ProjectCSSClasses(p *Page, pr *domain.Project) string {
	s := "project"
	if pr.IsRoot() {
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

// SetProjectLeaves は Project#leaf? の結果を事前に設定する（一覧の描画で問い合わせを省く）。
func (p *Page) SetProjectLeaves(leaves map[int64]bool) {
	if p.leaf == nil {
		p.leaf = map[int64]bool{}
	}
	for k, v := range leaves {
		p.leaf[k] = v
	}
}

// ProjectMenuItemURL は redirect_to_project_menu_item / redirect_to_menu_item の判定:
// メニュー menuName の項目 name が表示可能ならその URL を返す。
func (d *Deps) ProjectMenuItemURL(p *Page, menuName, name string, pr *domain.Project) (string, bool) {
	m := menus[menuName]
	if m == nil {
		return "", false
	}
	var item *menu.Item
	for _, it := range m.Items {
		if it.Name == name {
			item = it
			break
		}
	}
	if item == nil || item.URL == nil {
		return "", false
	}
	env := menuEnv{d, p}
	mp := toMenuProject(pr)
	if !item.Allowed(env, mp) {
		return "", false
	}
	u := item.URL(env, mp)
	return u, u != ""
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case *float64:
		if x != nil {
			return *x
		}
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}

// formatObjectHTML は ApplicationHelper#format_object の簡易版（真偽・数値・日付・文字列）。
// TODO(customfield): Redmine::FieldFormat の formatted_value に置き換える。
func formatObjectHTML(p *Page, v any) any {
	switch x := v.(type) {
	case nil:
		return ""
	case bool:
		if x {
			return p.l("general_text_Yes")
		}
		return p.l("general_text_No")
	case string:
		return x
	}
	return rails.ToS(v)
}

// RenderProjectsForJumpBoxQuery は render_projects_for_jump_box(projects, query: q)。
// q が空ならブックマーク・最近使ったプロジェクトも出す（projects#autocomplete.js）。
func (d *Deps) RenderProjectsForJumpBoxQuery(p *Page, projects []JumpProject, q string) template.HTML {
	jb := &JumpBox{Projects: projects}
	if strings.TrimSpace(q) == "" {
		if a := p.authorizer(); a != nil && p.logged() && p.DB != nil {
			full, err := LoadJumpBox(p, a)
			if err != nil {
				p.logError("jump box", err)
			} else {
				jb.Bookmarked, jb.Recents = full.Bookmarked, full.Recents
			}
		}
		return d.renderProjectsForJumpBox(p, jb, nil)
	}
	if len(projects) == 0 {
		return ""
	}
	var s strings.Builder
	jump := p.Params().String("jump")
	if jump == "" {
		jump = p.currentMenuItem()
	}
	s.WriteString(string(rails.ContentTag("strong", p.l("label_result_plural"), nil)))
	ProjectTree(projects, func(pr JumpProject, level int) {
		text := rails.ContentTag("span", pr.Name, rails.NewHash("style", "padding-inline-start:"+strconv.Itoa(level*16)+"px;"))
		s.WriteString(string(rails.LinkTo(text, "/projects/"+pr.Identifier+"?jump="+url.QueryEscape(jump),
			rails.NewHash("title", pr.Name, "class", nil))))
	})
	return template.HTML(s.String())
}
