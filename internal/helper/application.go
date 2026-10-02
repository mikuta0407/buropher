package helper

import (
	"crypto/sha256"
	"encoding/hex"
	"html/template"
	"net/url"
	"sort"
	"strconv"
	"strings"
	ttemplate "text/template"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// RequestFuncs は view.Options.RequestFuncs に登録するテンプレート関数群を返す。
// 名前収集のためダミーの Render でも呼ばれるので、ここでは Page を参照しない（関数内で遅延参照する）。
func (d *Deps) RequestFuncs(r *view.Render) ttemplate.FuncMap {
	pg := func() *Page { return PageOf(r) }
	fm := ttemplate.FuncMap{
		// --- Setting / User.current ---
		"setting":        func(name string) string { return pg().setting(name) },
		"setting_bool":   func(name string) bool { return pg().settingBool(name) },
		"logged":         func() bool { return pg().logged() },
		"admin":          func() bool { return pg().admin() },
		"login_required": func() bool { return pg().settingBool("login_required") },
		"app_name":       func() string { return AppName },
		"app_url":        func() string { return AppURL },
		"param": func(key string) any {
			if v, ok := pg().Params().Get(key); ok {
				return v
			}
			return nil
		},
		"param_blank": func(key string) bool { return httpx.IsBlank(pg().Params().String(key)) },
		"question": func() any {
			if q := pg().Question; q != "" || pg().QuestionSet {
				return q
			}
			return nil
		},

		// --- head ---
		"favicon":                   func() html { return d.favicon(pg()) },
		"stylesheet_link_tag":       func(args ...any) html { return d.stylesheetLinkTag(pg(), args...) },
		"javascript_importmap_tags": func() html { return d.importmapTags() },
		"javascript_heads":          func() html { return d.javascriptHeads(pg()) },
		"heads_for_theme":           func() html { return d.headsForTheme(pg()) },
		"heads_for_i18n":            func() html { return d.headsForI18n(pg()) },
		"heads_for_auto_complete":   func(project any) html { return d.headsForAutoComplete(toProject(project)) },
		"auto_discovery_link_tag":   func(typ, href string, args ...any) html { return autoDiscoveryLinkTag(typ, href, optHash(args)) },
		"image_tag":                 func(src string, args ...any) html { return d.imageTag(r, pg(), src, optHash(args)) },
		// call_hook はプラグイン非対応のため常に空（Redmine::Hook の既定と同じ出力）。
		"call_hook": func(name string, args ...any) html { return "" },

		// --- icons / avatars / users ---
		"sprite_icon": func(name any, args ...any) html {
			var label any
			var opts *rails.Hash
			switch len(args) {
			case 0:
			case 1:
				if h, ok := args[0].(*rails.Hash); ok {
					opts = h
				} else {
					label = args[0]
				}
			default:
				label = args[0]
				opts = optHash(args[1:])
			}
			return d.spriteIcon(pg(), rails.ToS(name), label, opts)
		},
		"notice_icon":  func(typ string) html { return d.noticeIcon(pg(), typ) },
		"avatar":       func(u any, args ...any) html { return d.avatar(r, pg(), toUser(u), optHash(args)) },
		"link_to_user": func(u any, args ...any) html { return d.linkToUser(pg(), u, optHash(args)) },
		"user_path":    func(u any) string { return userPath(toUser(u)) },

		// --- menus ---
		"display_main_menu": func(project any) bool { return pg().displayMainMenu(toProject(project)) },
		"render_main_menu":  func(project any) html { return d.renderMainMenu(pg(), toProject(project)) },
		"render_menu": func(name string, project ...any) html {
			var p *domain.Project
			if len(project) > 0 {
				p = toProject(project[0])
			}
			return d.renderMenu(pg(), name, p)
		},
		"current_menu_item": func() string { return pg().currentMenuItem() },

		// --- header / search ---
		"search_path":                  func(args ...any) string { return searchPath(optHash(args)) },
		"default_search_scope":         func() string { return pg().DefaultSearchScope },
		"default_search_project_scope": func() any { return defaultSearchProjectScope(pg(), pg().Project) },
		"accesskey":                    func(name string) any { return pg().accesskey(name) },
		"render_project_jump_box":      func() html { return d.renderProjectJumpBox(pg()) },
		"page_header_title":            func() html { return pageHeaderTitle(pg()) },
		"sidebar_content":              func() bool { return r.HasContentFor("sidebar") },
		// view_layouts_base_sidebar_hook_response（プラグイン非対応のため空）
		"view_layouts_base_sidebar_hook_response": func() html { return "" },

		// --- forms / misc ---
		"back_url":                  func() string { return backURL(pg()) },
		"back_url_hidden_field_tag": func() html { return backURLHiddenFieldTag(pg()) },
		"textilizable":              func(text any, args ...any) html { return d.textilizable(pg(), text, args...) },
		"link_to_project": func(p any, args ...any) html {
			var opts, htmlOpts *rails.Hash
			if len(args) > 0 {
				opts, _ = args[0].(*rails.Hash)
			}
			if len(args) > 1 {
				htmlOpts, _ = args[1].(*rails.Hash)
			}
			if opts == nil {
				opts = rails.NewHash()
			}
			return linkToProject(toProject(p), opts, htmlOpts)
		},
		"project_path": func(p any) string { return projectPath(toProject(p)) },
		"news_path":    newsPath,
		"authoring": func(created time.Time, author any, args ...any) html {
			return d.authoring(pg(), created, author, optHash(args))
		},
		"time_tag":     func(t time.Time) html { return d.timeTag(pg(), t) },
		"format_time":  func(t time.Time, args ...any) string { return formatTime(pg(), t, len(args) == 0 || truthy(args[0])) },
		"format_date":  func(t time.Time) string { return formatDate(pg(), t) },
		"url_for_atom": func(path string, key string) string { return atomURL(pg(), path, key) },
	}
	for k, v := range d.mastersFuncs(r, pg) {
		fm[k] = v
	}
	// 機能別のファイルで registerFuncs により追加された関数（並列開発での衝突を避けるため）
	for _, f := range extraFuncs {
		for k, v := range f(d, r, pg) {
			fm[k] = v
		}
	}
	return fm
}

// extraFuncs は registerFuncs で登録された追加のテンプレート関数群。
var extraFuncs []func(d *Deps, r *view.Render, pg func() *Page) ttemplate.FuncMap

// registerFuncs は機能別ファイルのテンプレート関数を RequestFuncs に追加する（init で呼ぶ）。
func registerFuncs(f func(d *Deps, r *view.Render, pg func() *Page) ttemplate.FuncMap) {
	extraFuncs = append(extraFuncs, f)
}

// Funcs は名前だけが必要な関数（Render 外で使うものはない）。将来の拡張用。
func (d *Deps) Funcs() ttemplate.FuncMap { return ttemplate.FuncMap{} }

func optHash(args []any) *rails.Hash {
	for i := len(args) - 1; i >= 0; i-- {
		if h, ok := args[i].(*rails.Hash); ok {
			return h
		}
	}
	return rails.NewHash()
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	}
	return true
}

// toUser はテンプレートの値を *domain.User にする（ユーザーでなければ nil）。
func toUser(v any) *domain.User {
	if u, ok := v.(*domain.User); ok {
		return u
	}
	return nil
}

// toProject はテンプレートの値を *domain.Project にする（プロジェクトでなければ nil）。
func toProject(v any) *domain.Project {
	if p, ok := v.(*domain.Project); ok {
		return p
	}
	return nil
}

// ---- head ----

// favicon は ApplicationHelper#favicon。
func (d *Deps) favicon(p *Page) html {
	icon := "favicon.ico"
	if t := d.currentTheme(p); t != nil && t.Favicon() != "" {
		icon = t.FaviconPath()
	}
	if d.Assets == nil {
		return html(`<link rel="shortcut icon" type="image/x-icon" href="/` + rails.EscapeString(icon) + `" />`)
	}
	return d.Assets.FaviconLinkTag(icon)
}

// stylesheetLinkTag は ApplicationHelper#stylesheet_link_tag（テーマの CSS への差し替え付き）。
func (d *Deps) stylesheetLinkTag(p *Page, args ...any) html {
	opts := rails.NewHash()
	var sources []string
	for _, a := range args {
		if h, ok := a.(*rails.Hash); ok {
			opts = h
			continue
		}
		sources = append(sources, rails.ToS(a))
	}
	plugin := rails.ToS(opts.Get("plugin"))
	theme := d.currentTheme(p)
	for i, s := range sources {
		switch {
		case plugin != "":
			sources[i] = "plugin_assets/" + plugin + "/" + s
		case theme != nil && theme.HasStylesheet(s):
			sources[i] = theme.StylesheetPath(s)
		}
	}
	if d.Assets == nil {
		return ""
	}
	return d.Assets.StylesheetLinkTagMedia(rails.ToS(opts.Get("media")), sources...)
}

func (d *Deps) importmapTags() html {
	if d.Assets == nil {
		return ""
	}
	return d.Assets.ImportmapTags()
}

func (d *Deps) jsInclude(sources ...string) html {
	if d.Assets == nil {
		return ""
	}
	return d.Assets.JavascriptIncludeTag(sources...)
}

// javascriptHeads は ApplicationHelper#javascript_heads。
func (d *Deps) javascriptHeads(p *Page) html {
	tags := d.jsInclude("jquery-3.7.1-ui-1.13.3", "rails-ujs", "tribute-5.1.3.min")
	if p.settingBool("wiki_tablesort_enabled") {
		tags += d.jsInclude("tablesort-5.2.1.min.js", "tablesort-5.2.1.number.min.js")
	}
	tags += d.jsInclude("application-legacy", "responsive")
	if p.pref().WarnOnLeavingUnsaved {
		warn := rails.EscapeJavascriptString(p.l("text_warn_on_leaving_unsaved"))
		tags += "\n" + rails.JavascriptTag("$(window).on('load', function(){ warnLeavingUnsaved('"+warn+"'); });", nil)
	}
	return tags
}

// headsForTheme は Redmine::Themes::Helper#heads_for_theme。
func (d *Deps) headsForTheme(p *Page) html {
	if t := d.currentTheme(p); t != nil && t.HasJavascript("theme") {
		return d.jsInclude(t.JavascriptPath("theme"))
	}
	return ""
}

// headsForI18n は ApplicationHelper#heads_for_i18n。
func (d *Deps) headsForI18n(p *Page) html {
	return rails.JavascriptTag("rm = window.rm || {};"+
		"rm.I18n = rm.I18n || {};"+
		"rm.I18n = Object.freeze({buttonCopy: '"+p.l("button_copy")+"'});", nil)
}

// headsForAutoComplete は ApplicationHelper#heads_for_auto_complete。
func (d *Deps) headsForAutoComplete(project *domain.Project) html {
	q := "?q="
	if project != nil {
		q = "?project_id=" + url.QueryEscape(project.Identifier) + "&q="
	}
	js := `{"issues":` + rails.ToJSON("/issues/auto_complete"+q) + `,"wiki_pages":` + rails.ToJSON("/wiki_pages/auto_complete"+q) + `}`
	return rails.JavascriptTag("rm = window.rm || {};"+
		"rm.AutoComplete = rm.AutoComplete || {};"+
		"rm.AutoComplete.dataSources = JSON.parse('"+js+"');", nil)
}

// autoDiscoveryLinkTag は auto_discovery_link_tag(type, url, tag_options)。
func autoDiscoveryLinkTag(typ, href string, opts *rails.Hash) html {
	rel := opts.Get("rel")
	if rel == nil {
		rel = "alternate"
	}
	mime := opts.Get("type")
	if mime == nil {
		switch typ {
		case "rss":
			mime = "application/rss+xml"
		case "atom":
			mime = "application/atom+xml"
		default:
			mime = "application/" + typ + "+xml"
		}
	}
	title := opts.Get("title")
	if title == nil {
		title = strings.ToUpper(typ)
	}
	return rails.Tag("link", rails.NewHash("rel", rel, "type", mime, "title", title, "href", href))
}

// atomURL は url_for(controller:, action: 'index', key:, format: 'atom') の絶対 URL。
func atomURL(p *Page, path, key string) string {
	u := path + ".atom"
	if key != "" {
		u += "?key=" + url.QueryEscape(key)
	}
	if p.Request != nil {
		return httpx.RequestBaseURL(p.Request) + u
	}
	return u
}

// imageTag は ApplicationHelper#image_tag（テーマの画像への差し替え付き）。
func (d *Deps) imageTag(r *view.Render, p *Page, source string, opts *rails.Hash) html {
	opts = opts.Clone()
	if plugin, ok := opts.Delete("plugin"); ok && truthy(plugin) {
		source = "plugin_assets/" + rails.ToS(plugin) + "/" + source
	} else if t := d.currentTheme(p); t != nil && t.HasImage(source) {
		source = t.ImagePath(source)
	}
	return r.Rails.ImageTag(source, opts)
}

// ---- icons ----

// assetPath は asset_path(source)。
func (d *Deps) assetPath(source string) string {
	if d.Assets == nil {
		return "/" + source
	}
	return d.Assets.AssetPath(source)
}

// spriteIcon は IconsHelper#sprite_icon。
// TODO(themes): テーマ独自のアイコンスプライト（Theme#icons）は未対応。
func (d *Deps) spriteIcon(p *Page, name string, label any, opts *rails.Hash) html {
	if opts == nil {
		opts = rails.NewHash()
	}
	size := "18"
	if v, ok := opts.Lookup("size"); ok {
		size = rails.ToS(v)
	}
	sprite := "icons"
	if v, ok := opts.Lookup("sprite"); ok && truthy(v) {
		sprite = rails.ToS(v)
	}
	source := sprite + ".svg"
	if plugin := opts.Get("plugin"); truthy(plugin) {
		source = "plugin_assets/" + rails.ToS(plugin) + "/" + sprite + ".svg"
	}
	css := "s" + size + " icon-svg"
	if rails.ToS(opts.Get("style")) == "filled" {
		css += " icon-svg-filled"
	}
	if v := opts.Get("css_class"); v != nil {
		css += " " + rails.ToS(v)
	}
	if truthy(opts.Get("rtl")) {
		css += " icon-rtl"
	}
	svg := rails.ContentTag("svg",
		rails.ContentTag("use", "", rails.NewHash("href", d.assetPath(source)+"#icon--"+name)),
		rails.NewHash("class", css, "aria", rails.NewHash("hidden", true)))
	if label == nil {
		return svg
	}
	classes := "icon-label"
	if truthy(opts.Get("icon_only")) {
		classes += " hidden"
	}
	return svg + rails.ContentTag("span", label, rails.NewHash("class", classes))
}

// noticeIcon は IconsHelper#notice_icon。
func (d *Deps) noticeIcon(p *Page, typ string) html {
	name := ""
	switch typ {
	case "notice":
		name = "checked"
	case "warning", "error":
		name = "warning"
	}
	return d.spriteIcon(p, name, nil, nil)
}

// FlashIcon は view.Options.FlashIcon に渡す関数。
func (d *Deps) FlashIcon(r *view.Render, kind string) template.HTML {
	return d.noticeIcon(PageOf(r), kind)
}

// ---- avatars / users ----

// avatar は AvatarsHelper#avatar。
func (d *Deps) avatar(r *view.Render, p *Page, u *domain.User, opts *rails.Hash) html {
	opts = opts.Clone()
	cls := "avatar"
	if c := opts.Get("class"); c != nil {
		cls += " " + rails.ToS(c)
	}
	opts.Set("class", cls)
	if u == nil {
		return ""
	}
	if !u.Logged() {
		// anonymous_avatar
		opts.Set("class", "anonymous-avatar "+cls)
		o := rails.NewHash("size", 24, "alt", "", "title", "").Update(opts)
		return d.imageTag(r, p, "anonymous.png", o)
	}
	if p.settingBool("gravatar_enabled") {
		return d.gravatarAvatarTag(r, p, u, opts)
	}
	// initials_avatar_tag
	size := 24
	if v, ok := opts.Delete("size"); ok && v != nil {
		size, _ = strconv.Atoi(rails.ToS(v))
	}
	css := "avatar-color-" + strconv.FormatInt(u.ID%8, 10) + " s" + strconv.Itoa(size) + " " + rails.ToS(opts.Get("class"))
	return rails.ContentTag("span", u.Initials(p.userFormat()), rails.NewHash("role", "img", "class", css, "title", opts.Get("title")))
}

// gravatarAvatarTag は AvatarsHelper#gravatar_avatar_tag + GravatarHelper#gravatar。
func (d *Deps) gravatarAvatarTag(r *view.Render, p *Page, u *domain.User, opts *rails.Hash) html {
	opts.Set("default", p.setting("gravatar_default"))
	opts.Set("class", "gravatar "+rails.ToS(opts.Get("class")))
	if opts.Get("title") == nil {
		opts.Set("title", p.userName(u, ""))
	}
	if initials := u.Initials(p.userFormat()); rails.ToS(opts.Get("default")) == "initials" && initials != "" {
		opts.Set("initials", initials)
	}
	email := u.Mail
	if email == "" {
		return ""
	}
	o := rails.NewHash("default", nil, "size", 24, "rating", "PG", "alt", "", "title", "", "class", "gravatar").Update(opts)
	src := gravatarURL(strings.ToLower(email), o)
	for _, k := range []string{"class", "alt", "title"} {
		o.Set(k, string(rails.H(o.Get(k))))
	}
	sz, _ := strconv.Atoi(rails.ToS(o.Get("size")))
	o.Set("srcset", gravatarURL(strings.ToLower(email), o.Clone().Set("size", sz*2))+" 2x")
	return r.Rails.ImageTag(string(rails.H(src)), o.Except("rating", "size", "default", "ssl"))
}

func gravatarURL(email string, o *rails.Hash) string {
	sum := sha256.Sum256([]byte(email))
	u := "https://www.gravatar.com/avatar/" + hex.EncodeToString(sum[:])
	var opts []string
	for _, k := range []string{"rating", "size", "default", "initials"} {
		v := o.Get(k)
		if v == nil {
			continue
		}
		s := rails.ToS(v)
		if k == "default" {
			s = url.QueryEscape(s)
		}
		opts = append(opts, k+"="+string(rails.H(s)))
	}
	if len(opts) > 0 {
		u += "?" + strings.Join(opts, "&")
	}
	return u
}

func userPath(u *domain.User) string {
	if u == nil {
		return ""
	}
	return "/users/" + strconv.FormatInt(u.ID, 10)
}

// linkToUser は ApplicationHelper#link_to_user / link_to_principal（User の場合）。
func (d *Deps) linkToUser(p *Page, v any, opts *rails.Hash) html {
	u := toUser(v)
	if u == nil {
		return rails.H(rails.ToS(v))
	}
	name := p.userName(u, rails.ToS(opts.Get("format")))
	if truthy(opts.Get("mention")) {
		name = "@" + name
	}
	if !(u.Active() || (p.admin() && u.Logged())) {
		return rails.H(name)
	}
	css := u.CSSClasses()
	if c := rails.ToS(opts.Get("class")); c != "" {
		css += " " + c
	}
	return rails.LinkTo(name, userPath(u), rails.NewHash("class", css))
}

// ---- header ----

// searchPath は search_path(id: project, scope: scope)。
func searchPath(opts *rails.Hash) string {
	path := "/search"
	if p := toProject(opts.Get("id")); p != nil {
		path = "/projects/" + p.Identifier + "/search"
	}
	if s := opts.Get("scope"); s != nil && rails.ToS(s) != "" {
		path += "?scope=" + url.QueryEscape(rails.ToS(s))
	}
	return path
}

// defaultSearchProjectScope は default_search_project_scope（nil なら nil）。
func defaultSearchProjectScope(pg *Page, p *domain.Project) any {
	if p != nil && !pg.projectLeaf(p) {
		return "subprojects"
	}
	return nil
}

// accessKeys は Redmine::AccessKeys::ACCESSKEYS。
var accessKeys = map[string]string{
	"edit": "e", "preview": "r", "quick_search": "f", "search": "4", "new_issue": "7", "previous": "p", "next": "n",
}

// accesskey は ApplicationHelper#accesskey（同じキーは 1 ページで 1 度だけ返す）。
func (p *Page) accesskey(name string) any {
	key, ok := accessKeys[name]
	if !ok {
		return nil
	}
	for _, k := range p.accessKeys {
		if k == key {
			return nil
		}
	}
	p.accessKeys = append(p.accessKeys, key)
	return key
}

// newsPath は news_path(news)。
func newsPath(n *domain.News) string {
	if n == nil {
		return ""
	}
	return "/news/" + strconv.FormatInt(n.ID, 10)
}

func projectPath(p *domain.Project) string {
	if p == nil {
		return ""
	}
	return "/projects/" + p.Identifier
}

// linkToProject は ApplicationHelper#link_to_project(project, options, html_options)。
func linkToProject(p *domain.Project, opts, htmlOpts *rails.Hash) html {
	if p == nil {
		return ""
	}
	if p.Archived() {
		return rails.H(p.Name)
	}
	u := projectPath(p)
	if opts.Len() > 0 {
		u += "?" + toQuery(opts)
	}
	return rails.LinkTo(p.Name, u, htmlOpts)
}

// toQuery は Hash#to_query（キーでソート）。
func toQuery(h *rails.Hash) string {
	keys := h.Keys()
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		v := h.Get(k)
		if v == nil {
			continue
		}
		parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(rails.ToS(v)))
	}
	return strings.Join(parts, "&")
}

// pageHeaderTitle は ApplicationHelper#page_header_title。
func pageHeaderTitle(p *Page) html {
	if p.Project == nil || p.Project.ID == 0 {
		return rails.H(p.setting("app_title"))
	}
	var b []html
	ancestors := p.visibleAncestors(p.Project)
	if len(ancestors) > 0 {
		jump := rails.NewHash()
		if item := p.currentMenuItem(); item != "" {
			jump.Set("jump", item)
		}
		root := ancestors[0]
		ancestors = ancestors[1:]
		b = append(b, linkToProject(root, jump, rails.NewHash("class", "root")))
		if len(ancestors) > 2 {
			b = append(b, rails.H("\u2026"))
			ancestors = ancestors[len(ancestors)-2:]
		}
		for _, a := range ancestors {
			b = append(b, linkToProject(a, jump, rails.NewHash("class", "ancestor")))
		}
	}
	b = append(b, rails.ContentTag("span", rails.H(p.Project.Name), rails.NewHash("class", "current-project")))
	if len(b) > 1 {
		sep := rails.ContentTag("span", html(" &raquo; "), rails.NewHash("class", "separator"))
		var path strings.Builder
		for _, x := range b[:len(b)-1] {
			path.WriteString(string(x))
			path.WriteString(string(sep))
		}
		b = []html{rails.ContentTag("span", html(path.String()), rails.NewHash("class", "breadcrumbs")), b[len(b)-1]}
	}
	var out strings.Builder
	for _, x := range b {
		out.WriteString(string(x))
	}
	return html(out.String())
}

// visibleAncestors は project.ancestors.visible（ルートから順）。ルートなら空。
func (p *Page) visibleAncestors(pr *domain.Project) []*domain.Project {
	if pr.IsRoot() || p.DB == nil {
		return nil
	}
	all, err := repository.ProjectAncestors(p.ctx(), p.DB, pr.ID)
	if err != nil {
		p.logError("project ancestors", err)
		return nil
	}
	var out []*domain.Project
	for _, a := range all {
		if p.AllowedTo(domain.Perm("view_project"), a) {
			out = append(out, a)
		}
	}
	return out
}

// renderProjectsForJumpBox は render_projects_for_jump_box。
func (d *Deps) renderProjectsForJumpBox(p *Page, jb *JumpBox, selected *domain.Project) html {
	var s strings.Builder
	jump := p.Params().String("jump")
	if jump == "" {
		jump = p.currentMenuItem()
	}
	link := func(pr JumpProject, level int) {
		text := rails.ContentTag("span", pr.Name, rails.NewHash("style", "padding-inline-start:"+strconv.Itoa(level*16)+"px;"))
		var cls any
		if selected != nil && selected.ID == pr.ID {
			cls = "selected"
		}
		s.WriteString(string(rails.LinkTo(text, "/projects/"+pr.Identifier+jumpQuery("?", jump),
			rails.NewHash("title", pr.Name, "class", cls))))
	}
	groups := []struct {
		projects []JumpProject
		label    string
		tree     bool
	}{
		{jb.Bookmarked, "label_optgroup_bookmarks", true},
		{jb.Recents, "label_optgroup_recents", false},
		{jb.Projects, "label_project_all", true},
	}
	for _, g := range groups {
		if len(g.projects) == 0 {
			continue
		}
		s.WriteString(string(rails.ContentTag("strong", p.l(g.label), nil)))
		if g.tree {
			ProjectTree(g.projects, link)
		} else {
			for _, pr := range g.projects {
				link(pr, 0)
			}
		}
	}
	return html(s.String())
}

// ProjectTree は Project.project_tree（lft 順に並べ、祖先の深さを level として渡す）。
func ProjectTree(projects []JumpProject, fn func(p JumpProject, level int)) {
	sorted := append([]JumpProject(nil), projects...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Lft < sorted[j].Lft })
	var ancestors []JumpProject
	for _, pr := range sorted {
		for len(ancestors) > 0 {
			a := ancestors[len(ancestors)-1]
			if a.Lft < pr.Lft && pr.Rgt < a.Rgt {
				break
			}
			ancestors = ancestors[:len(ancestors)-1]
		}
		fn(pr, len(ancestors))
		ancestors = append(ancestors, pr)
	}
}

// jumpQuery は :jump => item のクエリ（item が nil（current_menu_item が nil）なら付けない）。
func jumpQuery(sep, item string) string {
	if item == "" {
		return ""
	}
	return sep + "jump=" + url.QueryEscape(item)
}

// renderProjectJumpBox は ApplicationHelper#render_project_jump_box。
func (d *Deps) renderProjectJumpBox(p *Page) html {
	jb := p.jumpBox()
	text := ""
	if p.Project != nil && p.Project.ID != 0 {
		text = p.Project.Name
	}
	if text == "" {
		text = p.l("label_jump_to_a_project")
	}
	item := p.currentMenuItem()
	u := "/projects/autocomplete.js" + jumpQuery("?", item)
	trigger := rails.ContentTag("span", text, rails.NewHash("class", "drdn-trigger"))
	q := rails.TextFieldTag("q", "", rails.NewHash("id", "projects-quick-search", "class", "autocomplete",
		"data", rails.NewHash("automcomplete_url", u), "autocomplete", "off"))
	var allClass any
	if p.Project == nil && p.MainMenu {
		allClass = "selected"
	}
	all := rails.LinkTo(p.l("label_project_all"), "/projects"+jumpQuery("?", item), rails.NewHash("class", allClass))
	content := rails.ContentTag("div",
		rails.ContentTag("div", d.spriteIcon(p, "search", nil, rails.NewHash("icon_only", true, "size", 18))+q, rails.NewHash("class", "quick-search"))+
			rails.ContentTag("div", d.renderProjectsForJumpBox(p, jb, p.Project), rails.NewHash("class", "drdn-items projects selection"))+
			rails.ContentTag("div", all, rails.NewHash("class", "drdn-items all-projects selection")),
		rails.NewHash("class", "drdn-content"))
	return rails.ContentTag("div", trigger+content, rails.NewHash("id", "project-jump", "class", "drdn"))
}

// ---- forms ----

// backURL は ApplicationController#back_url（params[:back_url] か Referer を CGI.unescape したもの）。
func backURL(p *Page) string {
	if v, ok := p.Params().Get("back_url"); ok && v != nil {
		return httpx.ValueString(v)
	}
	if p.Request != nil {
		if ref := p.Request.Header.Get("Referer"); ref != "" {
			if s, err := url.QueryUnescape(ref); err == nil {
				return s
			}
			return ref
		}
	}
	return ""
}

// backURLHiddenFieldTag は ApplicationHelper#back_url_hidden_field_tag。
func backURLHiddenFieldTag(p *Page) html {
	if p.Request == nil {
		return ""
	}
	u, ok := httpx.ValidateBackURL(p.Request, backURL(p), "")
	if !ok || u == "" {
		return ""
	}
	return rails.HiddenFieldTag("back_url", u, rails.NewHash("id", nil))
}

// Textilizable は DB・ページ文脈なしで text を Setting の既定（common_mark）で整形する簡易版。
// Redmine リンク・マクロは解決されない。テンプレートでは textilizable 関数（Page の文脈を使う）を使うこと。
func Textilizable(text any) template.HTML {
	r := &redmine.Renderer{TextFormatting: "common_mark"}
	return r.Textilizable(rails.ToS(text), redmine.Options{})
}

// ---- time ----

func formatTime(p *Page, t time.Time, includeDate bool) string {
	if p.Loc == nil {
		return t.Format("01/02/2006 03:04 PM")
	}
	return p.Loc.FormatTime(t, includeDate)
}

func formatDate(p *Page, t time.Time) string {
	if p.Loc == nil {
		return t.Format("01/02/2006")
	}
	return p.Loc.FormatDate(t)
}

// timeTag は ApplicationHelper#time_tag。
func (d *Deps) timeTag(p *Page, t time.Time) html {
	if t.IsZero() {
		return ""
	}
	text := ""
	if p.Loc != nil {
		text = p.Loc.DistanceOfTimeInWords(p.now(), t)
	}
	if p.Project != nil {
		from := t
		if p.Loc != nil && p.Loc.Location != nil {
			from = t.In(p.Loc.Location)
		}
		return rails.LinkTo(text, "/projects/"+p.Project.Identifier+"/activity?from="+from.Format("2006-01-02"),
			rails.NewHash("title", formatTime(p, t, true)))
	}
	return rails.ContentTag("abbr", text, rails.NewHash("title", formatTime(p, t, true)))
}

// authoring は ApplicationHelper#authoring。
func (d *Deps) authoring(p *Page, created time.Time, author any, opts *rails.Hash) html {
	label := "label_added_time_by"
	if v := opts.Get("label"); v != nil {
		label = rails.ToS(v)
	}
	if p.Loc == nil {
		return ""
	}
	return html(p.Loc.L(label, map[string]any{"author": string(d.linkToUser(p, author, rails.NewHash())), "age": string(d.timeTag(p, created))}))
}
