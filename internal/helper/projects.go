package helper

import (
	"encoding/json"
	"html/template"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	ttemplate "text/template"
	"time"

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
		// render_project_action_links は ProjectsHelper#render_project_action_links（引数は add_project の可否）。
		"render_project_action_links": func(canAdd bool) html {
			p := pg()
			var links html
			if canAdd {
				links += rails.LinkTo(d.spriteIcon(p, "add", p.l("label_project_new"), nil), "/projects/new", rails.NewHash("class", "icon icon-add"))
			}
			if p.admin() {
				links += rails.LinkTo(d.spriteIcon(p, "settings", p.l("label_administration"), nil), "/admin/projects", rails.NewHash("class", "icon icon-settings"))
			}
			return links
		},
		// columns_tag_id は queries/_columns の tag_name.gsub(/[\[\]]+/, '_').sub(/_+$/, '')。
		"columns_tag_id": func(name string) string {
			return strings.TrimRight(bracketsRe.ReplaceAllString(name, "_"), "_")
		},
		// link_to_context_menu は ApplicationHelper#link_to_context_menu。
		"link_to_context_menu": func() html {
			p := pg()
			return rails.LinkTo(d.spriteIcon(p, "3-bullets", p.l("button_actions"), nil), "#",
				rails.NewHash("title", p.l("button_actions"), "class", "icon-only icon-actions js-contextmenu "))
		},
		// context_menu は ApplicationHelper#context_menu（header_tags に context_menu の JS / CSS を 1 回だけ追加）。
		"context_menu": func() html {
			p := pg()
			if !p.contextMenuIncluded {
				p.contextMenuIncluded = true
				tags := d.jsInclude("context_menu") + d.stylesheetLinkTag(p, "context_menu")
				r.ContentFor("header_tags", tags)
				if p.l("direction") == "rtl" {
					r.ContentFor("header_tags", d.stylesheetLinkTag(p, "context_menu_rtl"))
				}
			}
			return ""
		},
		// wikitoolbar_for は Redmine::WikiFormatting::*::Helper#wikitoolbar_for。
		"wikitoolbar_for": func(fieldID string, args ...any) html {
			preview := "/preview/text"
			if len(args) > 0 {
				preview = rails.ToS(args[0])
			}
			return d.wikitoolbarFor(r, pg(), fieldID, preview)
		},
		// list_autofill_data_attributes は ApplicationHelper#list_autofill_data_attributes。
		"list_autofill_data_attributes": func() *rails.Hash {
			p := pg()
			tf := p.setting("text_formatting")
			if strings.TrimSpace(tf) == "" {
				return rails.NewHash()
			}
			return rails.NewHash("controller", "list-autofill", "action", "beforeinput->list-autofill#handleBeforeInput",
				"list_autofill_text_formatting_param", tf)
		},
		// format_date_ptr は format_date(date)（nil なら nil）。
		"format_date_ptr": func(t *time.Time) any {
			if t == nil {
				return nil
			}
			return formatDate(pg(), *t)
		},
		// nil_if_blank は値が空なら nil（text_field_tag の value 省略用）。
		"nil_if_blank": func(v any) any {
			if strings.TrimSpace(rails.ToS(v)) == "" {
				return nil
			}
			return v
		},
		// scm_name は Repository::<SCM>.scm_name。
		"scm_name": func(scm string) string {
			if n, ok := scmNames[scm]; ok {
				return n
			}
			return scm
		},
		// custom_field_tag は CustomFieldsHelper#custom_field_tag(prefix, custom_value)。
		"custom_field_tag": func(prefix string, v *domain.CustomFieldValue) html { return customFieldTag(pg(), prefix, v) },
		// principals_check_box_tags は ApplicationHelper#principals_check_box_tags(name, principals)。
		"principals_check_box_tags": func(name string, list []*repository.MemberPrincipal) html {
			p := pg()
			var s strings.Builder
			for _, m := range list {
				var id int64
				var icon html
				if m.User != nil {
					id = m.User.ID
					icon = d.avatar(r, p, m.User, rails.NewHash("size", 16))
				}
				if m.Group != nil {
					id = m.Group.ID
				}
				if icon == "" {
					cls := "group"
					if m.Group != nil {
						cls = strings.ToLower(m.Group.Kind.RedmineType())
					} else if m.User != nil {
						cls = strings.ToLower(m.User.Kind.RedmineType())
					}
					icon = rails.ContentTag("span", d.principalIconHTML(p, m), rails.NewHash("class", "name icon icon-"+cls))
				}
				s.WriteString(string(rails.ContentTag("label",
					rails.CheckBoxTag(name, id, false, rails.NewHash("id", nil))+icon+rails.H(PrincipalName(p, m)), nil)))
			}
			return html(s.String())
		},
		// context_menu_link は ContextMenusHelper#context_menu_link。
		"context_menu_link": func(name any, url string, args ...any) html {
			opts := optHash(args).Clone()
			cls := rails.ToS(opts.Get("class"))
			if truthy(opts.Get("selected")) {
				cls += " icon-checked disabled"
				opts.Set("disabled", true)
			}
			opts.Delete("selected")
			if truthy(opts.Get("disabled")) {
				opts.Delete("method")
				opts.Delete("data")
				opts.Set("onclick", "return false;")
				cls += " disabled"
				url = "#"
			}
			opts.Delete("disabled")
			opts.Set("class", cls)
			return rails.LinkTo(name, url, opts)
		},
		// has_id は ids.include?(id)。
		"has_id": func(ids []int64, id int64) bool { return slices.Contains(ids, id) },
		// capitalize は String#capitalize（先頭を大文字、残りを小文字）。
		"capitalize": func(s any) string { return RubyCapitalize(rails.ToS(s)) },
		// error_messages_for_list は error_messages_for（エラーメッセージの配列を渡す版）。
		"error_messages_for_list": func(msgs []string) html { return RenderErrorMessages(d, pg(), msgs) },
		// include_calendar_headers_tags は ApplicationHelper#include_calendar_headers_tags
		// （出力なし。テンプレートでは {{$_ := include_calendar_headers_tags}} の形で行ごと消す）。
		"include_calendar_headers_tags": func() string {
			d.includeCalendarHeadersTagsOnce(r, pg())
			return ""
		},
		"link_to_principal": func(v any, args ...any) html {
			return d.linkToPrincipalHTML(pg(), v, optHash(args))
		},
		"principal_icon": func(v any) html { return d.principalIconHTML(pg(), v) },
		// link_to_principal_user は link_to_user(principal)（ユーザーならリンク、それ以外は名前）。
		"link_to_principal_user": func(v any) html {
			p := pg()
			if u, _ := principalOf(v); u != nil {
				return d.linkToUser(p, u, rails.NewHash())
			}
			return rails.H(PrincipalName(p, v))
		},
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
		// tracker_name_tag は TrackersHelper#tracker_name_tag。
		"tracker_name_tag": func(t *domain.Tracker) html {
			var title, css any
			if d := t.DescriptionString(); strings.TrimSpace(d) != "" {
				title, css = d, "field-description"
			}
			return rails.ContentTag("span", t.Name, rails.NewHash("class", css, "title", title))
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

// defaultToolbarLanguageOptions は UserPreference::DEFAULT_TOOLBAR_LANGUAGE_OPTIONS。
var defaultToolbarLanguageOptions = []string{"c", "cpp", "csharp", "css", "diff", "go", "groovy", "html", "java", "javascript",
	"objc", "perl", "php", "python", "r", "ruby", "sass", "scala", "shell", "sql", "swift", "xml", "yaml"}

// wikitoolbarFor は wikitoolbar_for（common_mark / textile。text_formatting が空なら何も出さない）。
// TODO(dedupe): テキスト整形（プレビュー）の移植で同じヘルパーを実装する場合はそちらに統合する。
// TODO: pref.toolbar_language_options（未移植）を反映する。
func (d *Deps) wikitoolbarFor(r *view.Render, p *Page, fieldID, preview string) html {
	tf := p.setting("text_formatting")
	if tf != "common_mark" && tf != "textile" {
		return ""
	}
	if r != nil && !p.wikiFormatterHeadsIncluded {
		p.wikiFormatterHeadsIncluded = true
		lang := "en"
		if p.Loc != nil {
			lang = strings.ToLower(p.Loc.Lang)
		}
		langJSON, _ := json.Marshal(defaultToolbarLanguageOptions)
		tags := d.jsInclude("jstoolbar/jstoolbar") + d.jsInclude("jstoolbar/"+tf) + d.jsInclude("jstoolbar/lang/jstoolbar-"+lang) +
			rails.JavascriptTag(`var wikiImageMimeTypes = ["image/gif","image/jpeg","image/png","image/tiff","image/webp","image/x-ms-bmp"];`+
				"var userHlLanguages = "+string(langJSON)+";", nil) +
			d.stylesheetLinkTag(p, "jstoolbar")
		r.ContentFor("header_tags", tags)
	}
	js := "var wikiToolbar = new jsToolBar(document.getElementById('" + fieldID + "')); " +
		"wikiToolbar.setHelpLink('" + rails.EscapeJavascriptString("/help/wiki_syntax") + "'); " +
		"wikiToolbar.setPreviewUrl('" + rails.EscapeJavascriptString(preview) + "'); "
	if tf == "common_mark" {
		js += "wikiToolbar.draw();"
	} else {
		js += "wikiToolbar.draw();"
	}
	return rails.JavascriptTag(js, nil)
}

// RubyCapitalize は String#capitalize。
func RubyCapitalize(s string) string {
	if s == "" {
		return s
	}
	rs := []rune(strings.ToLower(s))
	rs[0] = []rune(strings.ToUpper(string(rs[0])))[0]
	return string(rs)
}

// includeCalendarHeadersTagsOnce は include_calendar_headers_tags（1 リクエストに 1 回だけ header_tags に追加）。
func (d *Deps) includeCalendarHeadersTagsOnce(r *view.Render, p *Page) {
	if r == nil || p.calendarHeadersIncluded {
		return
	}
	p.calendarHeadersIncluded = true
	sow := p.setting("start_of_week")
	if strings.TrimSpace(sow) == "" {
		sow = p.l("general_first_day_of_week")
		if sow == "general_first_day_of_week" || sow == "" {
			sow = "1"
		}
	}
	n, _ := strconv.Atoi(strings.TrimSpace(sow))
	tags := rails.JavascriptTag("var datepickerOptions={dateFormat: 'yy-mm-dd', firstDay: "+strconv.Itoa(n%7)+", "+
		"showOn: 'button', buttonImageOnly: true, buttonImage: '"+d.assetPath("calendar.png")+
		"', showButtonPanel: true, showWeek: true, showOtherMonths: true, "+
		"selectOtherMonths: true, changeMonth: true, changeYear: true, "+
		"beforeShow: beforeShowDatePicker};", nil)
	loc := ""
	if p.Loc != nil {
		loc = p.Loc.L("jquery.locale")
		if loc == "" || strings.Contains(loc, "jquery.locale") {
			loc = p.Loc.Lang
		}
	}
	if loc != "en" && loc != "" {
		tags += d.jsInclude("i18n/datepicker-" + loc + ".js")
	}
	r.ContentFor("header_tags", tags)
}

// SpriteIconHTML は sprite_icon(name, label)（ハンドラで HTML を組み立てる場合に使う）。
func (d *Deps) SpriteIconHTML(p *Page, name string, label any) template.HTML {
	return d.spriteIcon(p, name, label, nil)
}

// SpriteIconOnly は sprite_icon(name, label, icon_only: true)。
func (d *Deps) SpriteIconOnly(p *Page, name string, label any) template.HTML {
	return d.spriteIcon(p, name, label, rails.NewHash("icon_only", true))
}

// BookmarkLink は bookmark_link（bookmark.js の応答で使う）。
func (d *Deps) BookmarkLink(p *Page, pr *domain.Project) template.HTML {
	return d.bookmarkLinkHTML(p, pr)
}

// RenderProjectsForJumpBox は render_projects_for_jump_box（autocomplete.js / bookmark.js の応答で使う）。
func (d *Deps) RenderProjectsForJumpBox(p *Page, jb *JumpBox, selected *domain.Project) template.HTML {
	return d.renderProjectsForJumpBox(p, jb, selected)
}

// scmNames は repositories.scm → Repository::<SCM>.scm_name。
var scmNames = map[string]string{
	"subversion": "Subversion", "git": "Git", "mercurial": "Mercurial",
	"cvs": "CVS", "bazaar": "Bazaar", "filesystem": "Filesystem",
}

var bracketsRe = regexp.MustCompile(`[\[\]]+`)

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
