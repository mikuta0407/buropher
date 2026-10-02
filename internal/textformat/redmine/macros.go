// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

package redmine

// Redmine::WikiFormatting::Macros（lib/redmine/wiki_formatting/macros.rb）と
// catch_macros / inject_macros（application_helper.rb:1449-1503）。

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"html/template"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// MacroFunc はマクロ本体。args は引数（ParseArgs が false なら Raw に文字列全体）、
// text はブロック（無ければ hasText=false）。
// 戻り値は template.HTML（html_safe）か string（出力時にエスケープされる）。エラーは
// "Error executing the <strong>name</strong> macro (msg)" として表示される。
type MacroFunc func(c *MacroContext) (any, error)

// Macro はマクロの定義。
type Macro struct {
	Name string
	Desc string
	// RawArgs は :parse_args => false（引数を分割しない）。
	RawArgs bool
	// AcceptsBlock はブロック付きで呼べる（Ruby のブロック引数が 3 つ）。
	AcceptsBlock bool
	Func         MacroFunc
}

// MacroContext はマクロ呼び出しの文脈。
type MacroContext struct {
	R      *Renderer
	Object *Object
	// Args は分割済みの引数。RawArgs のマクロでは Raw を使う。
	Args []string
	Raw  string
	// Text はブロック（strip 済み）。HasText はブロックが与えられたか。
	Text    string
	HasText bool
	// InlineAttachments は Macros.inline_attachments（呼び出し元の :inline_attachments）。
	InlineAttachments bool
}

var macroRegistry []*Macro

// RegisterMacro はマクロを登録する（Redmine::WikiFormatting::Macros.register / macro 相当）。
// 同名のマクロは置き換える。名前は小文字に正規化される。
func RegisterMacro(m *Macro) {
	m.Name = strings.ToLower(m.Name)
	for i, x := range macroRegistry {
		if x.Name == m.Name {
			macroRegistry[i] = m
			return
		}
	}
	macroRegistry = append(macroRegistry, m)
}

// Macros は登録済みマクロを登録順で返す。
func Macros() []*Macro { return append([]*Macro(nil), macroRegistry...) }

func findMacro(name string) *Macro {
	for _, m := range macroRegistry {
		if m.Name == name {
			return m
		}
	}
	return nil
}

// MACROS_RE / MACRO_SUB_RE
var (
	reMacros   = rxm(`((!)?(\{\{([a-zA-Z0-9_]+)(\(([^\n\r]*?)\))?([\n\r].*?[\n\r])?\}\}))`)
	reMacroSub = rx(`(\{\{macro\((\d+)\)\}\})`)
)

// catchMacros は catch_macros。
func (r *Renderer) catchMacros(text string, macros map[int]string) string {
	if !strings.Contains(text, "{{") {
		return text
	}
	return gsub(reMacros, text, func(m md) string {
		all, name := m.s(1), strings.ToLower(m.s(4))
		if findMacro(name) != nil || matches(reMacroSub, all) {
			idx := len(macros)
			macros[idx] = all
			return "{{macro(" + strconv.Itoa(idx) + ")}}"
		}
		return all
	})
}

// injectMacros は inject_macros。
func (r *Renderer) injectMacros(text string, obj *Object, macros map[int]string, execute bool, opts Options) string {
	return gsub(reMacroSub, text, func(m md) string {
		all := m.s(1)
		idx, _ := strconv.Atoi(m.s(2))
		orig, ok := macros[idx]
		delete(macros, idx)
		if execute && ok {
			if mm := match(reMacros, orig); mm != nil {
				esc, all := mm.ok(2), mm.s(3)
				name, args := strings.ToLower(mm.s(4)), mm.s(6)
				block, hasBlock := mm.s(7), mm.ok(7)
				if esc {
					return h(all)
				}
				if out, ok := r.execMacro(name, obj, args, rubyStrip(block), hasBlock, opts); ok {
					return safeOrEscape(out)
				}
				return h(all)
			}
		}
		if ok {
			return h(orig)
		}
		return h(all)
	})
}

func safeOrEscape(v any) string {
	switch x := v.(type) {
	case template.HTML:
		return string(x)
	case string:
		return h(x)
	}
	return ""
}

var (
	reMacroArgSplit = rx(`[` + spIn + `]*,[` + spIn + `]*(?=(?:[^"]*"[^"]*")*[^"]*$)`)
	reMacroArgQuote = rx(`^"(.*)"$`)
)

// splitMacroArgs は exec_macro の引数分割（ダブルクォート内のカンマでは分割しない）。
func splitMacroArgs(args string) []string {
	if args == "" {
		return []string{}
	}
	var parts []string
	prev := 0
	runes := []rune(args)
	for _, m := range scan(reMacroArgSplit, args) {
		if m.m.Length == 0 {
			continue
		}
		parts = append(parts, string(runes[prev:m.m.Index]))
		prev = m.m.Index + m.m.Length
	}
	parts = append(parts, string(runes[prev:]))
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	for i, p := range parts {
		p = gsub(reMacroArgQuote, p, func(m md) string { return m.s(1) })
		parts[i] = strings.ReplaceAll(p, `""`, `"`)
	}
	return parts
}

// execMacro は exec_macro。マクロが存在しなければ ok=false。
func (r *Renderer) execMacro(name string, obj *Object, args, text string, hasText bool, opts Options) (out any, ok bool) {
	m := findMacro(name)
	if m == nil {
		return nil, false
	}
	c := &MacroContext{R: r, Object: obj, Raw: args, Text: text, HasText: hasText, InlineAttachments: !opts.NoInlineAttachments}
	if !m.RawArgs {
		c.Args = splitMacroArgs(args)
	}
	var err error
	if !m.AcceptsBlock && hasText {
		err = errors.New(r.l("error_macro_does_not_accept_block"))
	} else {
		out, err = m.Func(c)
	}
	if err != nil {
		return template.HTML(`<div class="flash error">` +
			r.l("error_can_not_execute_macro_html", i18n.Vars{"name": h(name), "error": h(err.Error())}) + `</div>`), true
	}
	if out == nil {
		return nil, false
	}
	return out, true
}

var reMacroOption = rx(`^(.+?)\=(.+)$`)

// ExtractMacroOptions は extract_macro_options（末尾の key=value を取り出す）。
func ExtractMacroOptions(args []string, keys ...string) ([]string, map[string]string) {
	opts := map[string]string{}
	args = append([]string(nil), args...)
	for len(args) > 0 {
		mm := match(reMacroOption, rubyStrip(args[len(args)-1]))
		if mm == nil {
			break
		}
		k := strings.ToLower(mm.s(1))
		found := false
		for _, x := range keys {
			if x == k {
				found = true
			}
		}
		if !found {
			break
		}
		opts[k] = gsub(reMacroArgQuote, mm.s(2), func(m md) string { return m.s(1) })
		args = args[:len(args)-1]
	}
	return args, opts
}

// currentProject は Macros::Definitions#current_project。
func (c *MacroContext) currentProject() *domain.Project {
	if c.R.Project != nil {
		return c.R.Project
	}
	if c.Object != nil && c.Object.Kind == "project" {
		return c.Object.Self
	}
	return c.Object.project()
}

// className は obj.class.name。
func (o *Object) className() string {
	if o == nil {
		return "NilClass"
	}
	switch o.Kind {
	case "issue":
		return "Issue"
	case "journal":
		return "Journal"
	case "wiki_content":
		return "WikiContent"
	case "wiki_content_version":
		return "WikiContentVersion"
	case "news":
		return "News"
	case "message":
		return "Message"
	case "document":
		return "Document"
	case "version":
		return "Version"
	case "project":
		return "Project"
	case "changeset":
		return "Changeset"
	}
	return o.Kind
}

// rubyToI は String#to_i。
func rubyToI(s string) int {
	s = strings.TrimLeft(s, " \t\n\v\f\r")
	neg := false
	if s != "" && (s[0] == '-' || s[0] == '+') {
		neg = s[0] == '-'
		s = s[1:]
	}
	n := 0
	prevDigit := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
			prevDigit = true
		} else if c == '_' && prevDigit && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' {
			continue
		} else {
			break
		}
	}
	if neg {
		return -n
	}
	return n
}

// wikiFindPage は Wiki.find_page(title, project:)（本文のあるページのみ）。
func (r *Renderer) wikiFindPage(title string, project *domain.Project) *WikiPage {
	if pm := match(reWikiProject, title); pm != nil {
		project = r.findProjectByIdentifierOrName(pm.s(1))
		title = pm.s(2)
	}
	w := r.wiki(project)
	if w == nil {
		return nil
	}
	p := r.findWikiPage(w, title)
	if p == nil || !p.HasContent {
		return nil
	}
	return p
}

func (r *Renderer) projectOfPage(p *WikiPage) *domain.Project {
	pr, err := r.Store.ProjectByID(p.ProjectID)
	r.logErr("project", err)
	return pr
}

func (r *Renderer) randomHex(n int) string {
	if r.RandomHex != nil {
		return r.RandomHex(n)
	}
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// RenderPageHierarchy は render_page_hierarchy(pages, node)（タイムスタンプなし）。
// pages は parent_id → 子ページ（0 はルート）。
func (r *Renderer) RenderPageHierarchy(pages map[int64][]*WikiPage, node int64) string {
	children, ok := pages[node]
	if !ok {
		return ""
	}
	var b strings.Builder
	b.WriteString("<ul class=\"pages-hierarchy\">\n")
	for _, p := range children {
		b.WriteString("<li>")
		var href string
		if r.ControllerPath == "wiki" && r.ActionName == "export" {
			href = "#" + p.Title
		} else {
			href = WikiPagePath(r.projectOfPage(p), p.Title)
		}
		b.WriteString(string(rails.LinkTo(template.HTML(h(PrettyTitle(p.Title))), href, nil)))
		if _, ok := pages[p.ID]; ok {
			b.WriteString("\n" + r.RenderPageHierarchy(pages, p.ID))
		}
		b.WriteString("</li>\n")
	}
	b.WriteString("</ul>\n")
	return b.String()
}

// selfAndDescendants は page.self_and_descendants(depth)（acts_as_tree）。
func (r *Renderer) selfAndDescendants(p *WikiPage, depth int) []*WikiPage {
	return append([]*WikiPage{p}, r.descendants(p, depth)...)
}

func (r *Renderer) descendants(p *WikiPage, depth int) []*WikiPage {
	children, err := r.Store.WikiPageChildren(p.ID)
	r.logErr("wiki page children", err)
	result := append([]*WikiPage(nil), children...)
	if depth != 1 {
		for _, c := range children {
			result = append(result, r.descendants(c, depth-1)...)
		}
	}
	return result
}

func parentKey(p *WikiPage) int64 {
	if p.ParentID.Valid {
		return p.ParentID.Int64
	}
	return 0
}

func init() {
	RegisterMacro(&Macro{Name: "hello_world", Desc: "Sample macro.", AcceptsBlock: true, Func: macroHelloWorld})
	RegisterMacro(&Macro{Name: "macro_list", Desc: "Displays a list of all available macros, including description if available.", Func: macroMacroList})
	RegisterMacro(&Macro{Name: "child_pages", Desc: "Displays a list of child pages. With no argument, it displays the child pages of the current wiki page. Examples:\n\n" +
		"{{child_pages}} -- can be used from a wiki page only\n" +
		"{{child_pages(depth=2)}} -- display 2 levels nesting only\n" +
		"{{child_pages(Foo)}} -- lists all children of page Foo\n" +
		"{{child_pages(Foo, parent=1)}} -- same as above with a link to page Foo", Func: macroChildPages})
	RegisterMacro(&Macro{Name: "recent_pages", Desc: "Displays a list of recently updated wiki pages. With no argument, it displays pages that have been updated within the last 7 days. Examples:\n\n" +
		"{{recent_pages}} -- displays pages updated within the last 7 days\n" +
		"{{recent_pages(days=3)}} -- displays pages updated within the last 3 days\n" +
		"{{recent_pages(limit=5)}} -- limits the maximum number of pages to display to 5\n" +
		"{{recent_pages(time=true)}} -- displays pages updated within the last 7 days with updated time\n" +
		"{{recent_pages(project=identifier)}} -- displays pages updated within the last 7 days from a specific project\n" +
		"{{recent_pages(include_subprojects=true)}} -- includes pages from subprojects", Func: macroRecentPages})
	RegisterMacro(&Macro{Name: "include", Desc: "Includes a wiki page. Examples:\n\n" +
		"{{include(Foo)}}\n" +
		"{{include(projectname:Foo)}} -- to include a page of a specific project wiki", Func: macroInclude})
	RegisterMacro(&Macro{Name: "collapse", Desc: "Inserts of collapsed block of text. Examples:\n\n" +
		"{{collapse\nThis is a block of text that is collapsed by default.\nIt can be expanded by clicking a link.\n}}\n\n" +
		"{{collapse(View details...)\nWith custom link text.\n}}", AcceptsBlock: true, Func: macroCollapse})
	RegisterMacro(&Macro{Name: "thumbnail", Desc: "Displays a clickable thumbnail of an attached image.\n" +
		"Default size is 200 pixels. Examples:\n\n" +
		"{{thumbnail(image.png)}}\n" +
		"{{thumbnail(image.png, size=300, title=Thumbnail)}} -- with custom title and size", Func: macroThumbnail})
	RegisterMacro(&Macro{Name: "issue", Desc: "Displays an issue link including additional information. Examples:\n\n" +
		"{{issue(123)}}                              -- Issue #123: Enhance macro capabilities\n" +
		"{{issue(123, project=true)}}                -- Andromeda - Issue #123: Enhance macro capabilities\n" +
		"{{issue(123, tracker=false)}}               -- #123: Enhance macro capabilities\n" +
		"{{issue(123, subject=false, project=true)}} -- Andromeda - Issue #123\n", Func: macroIssue})
}

func macroHelloWorld(c *MacroContext) (any, error) {
	s := "Hello world! Object: " + c.Object.className() + ", "
	if len(c.Args) == 0 {
		s += "Called with no argument"
	} else {
		s += "Arguments: " + strings.Join(c.Args, ", ")
	}
	s += " and "
	if c.HasText && !blank(c.Text) {
		s += "a " + strconv.Itoa(len([]rune(c.Text))) + " bytes long block of text."
	} else {
		s += "no block of text."
	}
	return template.HTML(h(s)), nil
}

func macroMacroList(c *MacroContext) (any, error) {
	var b strings.Builder
	for _, m := range macroRegistry {
		b.WriteString(string(rails.ContentTag("dt", rails.ContentTag("code", m.Name, nil), nil)))
		b.WriteString(string(rails.ContentTag("dd", rails.ContentTag("pre", m.Desc, nil), nil)))
	}
	return rails.ContentTag("dl", template.HTML(b.String()), nil), nil
}

func macroChildPages(c *MacroContext) (any, error) {
	r := c.R
	args, opts := ExtractMacroOptions(c.Args, "parent", "depth")
	depth := 0
	if v, ok := opts["depth"]; ok && !blank(v) {
		depth = rubyToI(v)
	}
	var page *WikiPage
	switch {
	case len(args) > 0:
		page = r.wikiFindPage(args[0], r.Project)
	case c.Object.isWikiContent():
		page = c.Object.Page
	default:
		return nil, errors.New(r.l("error_childpages_macro_no_argument"))
	}
	if page == nil || !r.allowedTo("view_wiki_pages", r.projectOfPage(page)) {
		return nil, errors.New(r.l("error_page_not_found"))
	}
	pages := map[int64][]*WikiPage{}
	for _, p := range r.selfAndDescendants(page, depth) {
		k := parentKey(p)
		pages[k] = append(pages[k], p)
	}
	node := page.ID
	if _, ok := opts["parent"]; ok {
		node = parentKey(page)
	}
	return template.HTML(r.RenderPageHierarchy(pages, node)), nil
}

func macroRecentPages(c *MacroContext) (any, error) {
	r := c.R
	_, opts := ExtractMacroOptions(c.Args, "days", "limit", "time", "project", "include_subprojects")
	var project *domain.Project
	if v := opts["project"]; !blank(v) {
		p, err := r.Store.ProjectByIdentifier(v)
		r.logErr("project", err)
		project = p
	} else {
		project = c.currentProject()
	}
	if project == nil {
		return "", nil
	}
	days := 7
	if v := opts["days"]; !blank(v) {
		days = rubyToI(v)
	}
	limit := -1
	if v, ok := opts["limit"]; ok && !blank(v) {
		limit = rubyToI(v)
		if limit < 0 {
			limit = -1
		}
	}
	showTime := opts["time"] == "true"
	var ids []int64
	if opts["include_subprojects"] == "true" {
		var err error
		ids, err = r.Store.ProjectIDsAllowedInTree(project, "view_wiki_pages")
		r.logErr("projects", err)
	} else if r.allowedTo("view_wiki_pages", project) {
		ids = []int64{project.ID}
	}
	if len(ids) == 0 {
		return "", nil
	}
	since := r.now().Add(-time.Duration(days) * 24 * time.Hour)
	var pages []*WikiPage
	if limit != 0 {
		var err error
		pages, err = r.Store.RecentWikiPages(ids, since, limit)
		r.logErr("recent pages", err)
	}
	var b strings.Builder
	b.WriteString("<ul>")
	for _, p := range pages {
		b.WriteString("<li>")
		b.WriteString(string(rails.LinkTo(template.HTML(h(PrettyTitle(p.Title))), WikiPagePath(r.projectOfPage(p), p.Title), nil)))
		if showTime && p.UpdatedOn.Valid {
			b.WriteString(h(" (" + r.timeAgoInWords(p.UpdatedOn.Time) + ")"))
		}
		b.WriteString("</li>")
	}
	b.WriteString("</ul>")
	return template.HTML(b.String()), nil
}

func macroInclude(c *MacroContext) (any, error) {
	r := c.R
	project := c.currentProject()
	title := ""
	if len(c.Args) > 0 {
		title = c.Args[0]
	}
	page := r.wikiFindPage(title, project)
	if page == nil || !r.allowedTo("view_wiki_pages", r.projectOfPage(page)) {
		return nil, errors.New(r.l("error_page_not_found"))
	}
	for _, id := range r.includedWikiPages {
		if id == page.ID {
			return nil, errors.New(r.l("error_circular_inclusion"))
		}
	}
	r.includedWikiPages = append(r.includedWikiPages, page.ID)
	text, _, err := r.Store.WikiPageText(page.ID)
	r.logErr("wiki text", err)
	atts, err := r.Store.Attachments("wiki_page", page.ID)
	r.logErr("attachments", err)
	obj := &Object{Kind: "wiki_content", ID: page.ID, Project: r.projectOfPage(page), Page: page}
	out := r.Textilizable(text, Options{Object: obj, Attachments: atts, NoHeadings: true, NoInlineAttachments: !c.InlineAttachments})
	r.includedWikiPages = r.includedWikiPages[:len(r.includedWikiPages)-1]
	return out, nil
}

func macroCollapse(c *MacroContext) (any, error) {
	r := c.R
	id := "collapse-" + r.randomHex(4)
	show := r.l("button_show")
	if len(c.Args) > 0 {
		show = c.Args[0]
	}
	hide := r.l("button_hide")
	if len(c.Args) > 1 {
		hide = c.Args[1]
	} else if len(c.Args) > 0 {
		hide = c.Args[0]
	}
	js := "$('#" + id + "-show, #" + id + "-hide').toggle(); $('#" + id + "').fadeToggle(150);"
	var b strings.Builder
	b.WriteString(string(linkToFunction(r.spriteIcon("angle-right", show, true), js, rails.NewHash("id", id+"-show", "class", "icon icon-collapsed collapsible"))))
	b.WriteString(string(linkToFunction(r.spriteIcon("angle-down", hide, false), js, rails.NewHash("id", id+"-hide", "class", "icon icon-expanded collapsible", "style", "display:none;"))))
	var inner template.HTML
	if c.HasText {
		inner = r.Textilizable(c.Text, Options{Object: c.Object, NoHeadings: true, NoInlineAttachments: !c.InlineAttachments})
	}
	b.WriteString(string(rails.ContentTag("div", inner, rails.NewHash("id", id, "class", "collapsed-text", "style", "display:none;"))))
	return template.HTML(b.String()), nil
}

// linkToFunction は link_to_function(name, function, html_options)。
func linkToFunction(name template.HTML, function string, htmlOpts *rails.Hash) template.HTML {
	opts := rails.NewHash("href", "#", "onclick", function+"; return false;")
	for _, e := range htmlOpts.Entries() {
		opts.Set(e.Key, e.Value)
	}
	return rails.ContentTag("a", name, opts)
}

var reDigits = regexp.MustCompile(`^\d+$`)

func macroThumbnail(c *MacroContext) (any, error) {
	r := c.R
	args, opts := ExtractMacroOptions(c.Args, "size", "title")
	filename := ""
	if len(args) > 0 {
		filename = args[0]
	}
	if blank(filename) {
		return nil, errors.New(r.l("error_filename_required"))
	}
	sizeStr, hasSize := opts["size"]
	if hasSize && !reDigits.MatchString(sizeStr) {
		return nil, errors.New(r.l("error_invalid_size_parameter"))
	}
	size := rubyToI(sizeStr)
	if size <= 0 {
		size = 200
	}
	var atts []*Attachment
	if kind, id, ok := c.Object.attachmentContainer(); ok {
		a, err := r.Store.Attachments(kind, id)
		r.logErr("attachments", err)
		atts = a
	}
	if (r.ControllerPath == "previews" || r.ActionName == "preview") && len(r.PreviewAttachments) > 0 {
		atts = append(atts, r.PreviewAttachments...)
	}
	a := latestAttach(atts, filename)
	if a == nil {
		return nil, errors.New(r.l("error_attachment_not_found", i18n.Vars{"name": filename}))
	}
	title, ok := opts["title"]
	if !ok {
		title = a.Filename
		if !blank(a.Description.String) {
			title += " (" + a.Description.String + ")"
		}
	}
	id := strconv.FormatInt(a.ID, 10)
	thumb := r.url("/attachments/thumbnail/" + id + "/" + strconv.Itoa(size))
	img := `<img alt="` + h(a.Filename) + `" src="` + h(thumb) + `" />`
	return rails.LinkTo(template.HTML(img), r.url("/attachments/"+id), rails.NewHash("class", "thumbnail", "title", title)), nil
}

func macroIssue(c *MacroContext) (any, error) {
	r := c.R
	args, opts := ExtractMacroOptions(c.Args, "project", "subject", "tracker")
	id := ""
	if len(args) > 0 {
		id = args[0]
	}
	var is *Issue
	if n := rubyToI(id); id != "" && n != 0 {
		var err error
		is, err = r.Store.VisibleIssue(int64(n))
		r.logErr("issue", err)
	}
	if is == nil {
		return "#" + id, nil
	}
	o := LinkToIssueOptions{}
	if v := opts["project"]; v == "true" {
		o.Project = true
	}
	if v := opts["subject"]; v == "false" {
		o.NoSubject = true
	}
	if v := opts["tracker"]; v == "false" {
		o.NoTracker = true
	}
	return r.LinkToIssue(is, o), nil
}
