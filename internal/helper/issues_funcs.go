package helper

// チケット画面のテンプレート関数（ApplicationHelper / IssuesHelper のうちモデルに依存しないもの）。
// TODO(dedupe): actions_dropdown / context_menu / link_to_context_menu / capitalize は admin users・projects
// ブランチにも同名のテンプレート関数がある（同名の登録は後勝ちで、出力は同じ）。

import (
	"html/template"
	"regexp"
	"strings"
	ttemplate "text/template"
	"unicode"
	"unicode/utf8"

	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

var bracketsRe = regexp.MustCompile(`[\[\]]+`)

const contextMenuIncludedKey = "helper.context_menu_included"

func init() {
	registerFuncs(func(d *Deps, r *view.Render, pg func() *Page) ttemplate.FuncMap {
		return ttemplate.FuncMap{
			"actions_dropdown":     func(content any) html { return d.issuesActionsDropdown(pg(), content) },
			"context_menu":         func() html { return d.issuesContextMenu(r, pg()) },
			"link_to_context_menu": func() html { return d.issuesLinkToContextMenu(pg()) },
			"capitalize":           func(s any) string { return RubyCapitalize(rails.ToS(s)) },
			"error_messages_for_list": func(msgs []string) html {
				return RenderErrorMessages(d, pg(), msgs)
			},
			"wikitoolbar_for":          func(fieldID, previewURL string) html { return d.wikitoolbarFor(r, pg(), fieldID, previewURL) },
			"heads_for_wiki_formatter": func() string { d.headsForWikiFormatter(r, pg()); return "" },
			// tag_name.gsub(/[\[\]]+/, '_').sub(/_+$/, '')（queries/_columns）
			"columns_tag_id": func(name string) string {
				return strings.TrimRight(bracketsRe.ReplaceAllString(name, "_"), "_")
			},
		}
	})
}

// issuesLinkToContextMenu は ApplicationHelper#link_to_context_menu。
func (d *Deps) issuesLinkToContextMenu(p *Page) html {
	return rails.LinkTo(d.spriteIcon(p, "3-bullets", p.l("button_actions"), nil), "#",
		rails.NewHash("title", p.l("button_actions"), "class", "icon-only icon-actions js-contextmenu "))
}

// issuesContextMenu は ApplicationHelper#context_menu（JS / CSS を header_tags に 1 回だけ加える）。
func (d *Deps) issuesContextMenu(r *view.Render, p *Page) html {
	ctx := r.Ctx
	if ctx.Values == nil {
		ctx.Values = map[string]any{}
	}
	if ctx.Values[contextMenuIncludedKey] == true {
		return ""
	}
	ctx.Values[contextMenuIncludedKey] = true
	r.ContentFor("header_tags", d.jsInclude("context_menu")+d.stylesheetLinkTag(p, "context_menu"))
	if p.l("direction") == "rtl" {
		r.ContentFor("header_tags", d.stylesheetLinkTag(p, "context_menu_rtl"))
	}
	return ""
}

// issuesActionsDropdown は ApplicationHelper#actions_dropdown（content は capture した中身）。
func (d *Deps) issuesActionsDropdown(p *Page, content any) html {
	c := rails.ToS(content)
	if !rails.IsPresent(c) {
		return ""
	}
	trigger := rails.ContentTag("span", d.spriteIcon(p, "3-bullets", p.l("button_actions"), nil),
		rails.NewHash("class", "icon-only icon-actions", "title", p.l("button_actions")))
	trigger = rails.ContentTag("span", trigger, rails.NewHash("class", "drdn-trigger"))
	body := rails.ContentTag("div", template.HTML(c), rails.NewHash("class", "drdn-items"))
	body = rails.ContentTag("div", body, rails.NewHash("class", "drdn-content"))
	return rails.ContentTag("span", trigger+body, rails.NewHash("class", "drdn"))
}

// RubyCapitalize は String#capitalize（先頭を大文字、残りを小文字）。
func RubyCapitalize(s string) string {
	if s == "" {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + strings.ToLower(s[n:])
}

const wikiFormatterIncludedKey = "helper.heads_for_wiki_formatter_included"

// defaultToolbarLanguageOptions は UserPreference::DEFAULT_TOOLBAR_LANGUAGE_OPTIONS。
var defaultToolbarLanguageOptions = []string{"c", "cpp", "csharp", "css", "diff", "go", "groovy", "html", "java", "javascript",
	"objc", "perl", "php", "python", "r", "ruby", "sass", "scala", "shell", "sql", "swift", "xml", "yaml"}

// imageMimeTypes は Redmine::MimeType.by_type('image')。
var imageMimeTypes = []string{"image/gif", "image/jpeg", "image/png", "image/tiff", "image/webp", "image/x-ms-bmp"}

// headsForWikiFormatter は heads_for_wiki_formatter（Setting.text_formatting の jsToolBar を header_tags に 1 回だけ加える）。
// TODO(pref): toolbar_language_options（個人設定）は未対応のため既定の言語一覧を使う。
func (d *Deps) headsForWikiFormatter(r *view.Render, p *Page) {
	ctx := r.Ctx
	if ctx.Values == nil {
		ctx.Values = map[string]any{}
	}
	if ctx.Values[wikiFormatterIncludedKey] == true {
		return
	}
	ctx.Values[wikiFormatterIncludedKey] = true
	formatter := "common_mark"
	if p.setting("text_formatting") == "textile" {
		formatter = "textile"
	}
	if p.setting("text_formatting") == "" {
		return
	}
	lang := "en"
	if p.Loc != nil {
		lang = strings.ToLower(p.Loc.Lang)
	}
	tags := d.jsInclude("jstoolbar/jstoolbar") + d.jsInclude("jstoolbar/"+formatter) + d.jsInclude("jstoolbar/lang/jstoolbar-"+lang) +
		rails.JavascriptTag("var wikiImageMimeTypes = "+rails.ToJSON(imageMimeTypes)+";var userHlLanguages = "+rails.ToJSON(defaultToolbarLanguageOptions)+";", nil) +
		d.stylesheetLinkTag(p, "jstoolbar")
	r.ContentFor("header_tags", tags)
}

// wikitoolbarFor は wikitoolbar_for(field_id, preview_url)。
func (d *Deps) wikitoolbarFor(r *view.Render, p *Page, fieldID, previewURL string) html {
	if p.setting("text_formatting") == "" {
		return ""
	}
	d.headsForWikiFormatter(r, p)
	return rails.JavascriptTag("var wikiToolbar = new jsToolBar(document.getElementById('"+fieldID+"')); "+
		"wikiToolbar.setHelpLink('"+rails.EscapeJavascriptString("/help/wiki_syntax")+"'); "+
		"wikiToolbar.setPreviewUrl('"+rails.EscapeJavascriptString(previewURL)+"'); "+
		"wikiToolbar.draw();", nil)
}
