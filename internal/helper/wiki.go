// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package helper

import (
	"encoding/json"
	"strconv"
	"strings"
	ttemplate "text/template"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/urlroot"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは WikiHelper と、Wiki の画面で使う ApplicationHelper / WatchersHelper / AttachmentsHelper の
// ヘルパー（authorize_for, breadcrumb, render_page_hierarchy, other_formats_links, watcher_link,
// link_to_attachments, wikitoolbar_for ...）。
//
// TODO: チケット等の画面と共通のもの（watcher_link, link_to_attachments, other_formats_links, wikitoolbar_for）は
// 他の画面の実装が揃ったら共通ファイルへ移す。

func init() {
	registerFuncs(func(d *Deps, r *view.Render, pg func() *Page) ttemplate.FuncMap {
		headsIncluded := false
		return ttemplate.FuncMap{
			// authorize_for(controller, action)
			"authorize_for": func(ctrl, action string) bool {
				p := pg()
				return p.AllowedTo(domain.ControllerAction(ctrl, action), p.Project)
			},
			// allowed_to(permission, project)（User.current.allowed_to?(:perm, project)）
			"allowed_to": func(perm string, project any) bool {
				p := pg()
				pr := toProject(project)
				if pr == nil {
					pr = p.Project
				}
				return p.AllowedTo(domain.Perm(perm), pr)
			},
			"breadcrumb":          func(elements ...any) html { return breadcrumb(elements...) },
			"robot_exclusion_tag": func() html { return `<meta name="robots" content="noindex,follow,noarchive" />` },
			"wiki_page_path":      func(project any, title string) string { return redmine.WikiPagePath(toProject(project), title) },
			"wiki_page_breadcrumb": func(pages []*domain.WikiPage) html {
				return wikiPageBreadcrumb(pages)
			},
			"render_page_hierarchy": func(tree any, node int64, timestamp bool) html {
				return renderPageHierarchy(pg(), tree, node, timestamp)
			},
			"wiki_page_options_for_select": func(pages []*domain.WikiPage, selected int64) html {
				return wikiPageOptionsForSelect(pages, selected)
			},
			"wiki_content_update_info": func(c *domain.WikiContentVersion) html {
				p := pg()
				return html(p.l("label_updated_time_by", map[string]any{
					"author": string(d.linkToUser(p, contentAuthor(c), rails.NewHash())),
					"age":    string(d.timeTag(p, c.UpdatedOn)),
				}))
			},
			"content_author": func(c *domain.WikiContentVersion) any { return contentAuthor(c) },
			"other_formats_open": func() html {
				return html(`<p class="other-formats hide-when-print">` + rails.EscapeString(pg().l("label_export_to")))
			},
			"other_formats_close":    func() html { return "</p>" },
			"other_format_link":      func(name, url string) html { return otherFormatLink(name, url) },
			"number_to_human_size":   func(n any) string { return pg().Loc.NumberToHumanSize(n) },
			"distance_of_time_words": func(t time.Time) string { return pg().Loc.DistanceOfTimeInWords(pg().now(), t) },
			"link_to_attachments": func(kind string, id int64, atts []*domain.Attachment, opts *rails.Hash) (html, error) {
				return d.linkToAttachments(r, pg(), kind, id, atts, opts)
			},
			"link_to_attachment": func(a *domain.Attachment, opts *rails.Hash) html {
				return d.linkToDomainAttachment(pg(), a, opts)
			},
			"thumbnail_tag": func(a *domain.Attachment) html {
				return d.WikiRenderer(pg()).ThumbnailTag(refAttachment(a), thumbnailsSize(pg()))
			},
			"ref_attachments": func(atts []*domain.Attachment) []*redmine.Attachment { return refAttachments(atts) },
			"wikitoolbar_for": func(fieldID, previewURL string) html {
				p := pg()
				return d.wikitoolbarFor(r, p, fieldID, previewURL, &headsIncluded)
			},
			// heads_for_wiki_formatter（wikitoolbar_for と同じく header_tags に 1 回だけ加える）
			"heads_for_wiki_formatter": func() string {
				d.headsForWikiFormatter(r, pg(), &headsIncluded)
				return ""
			},
			"list_autofill_data_attributes": func() *rails.Hash { return listAutofillDataAttributes(pg()) },
			"update_data_sources_for_auto_complete": func(sources *rails.Hash) html {
				return updateDataSourcesForAutoComplete(sources)
			},
			"max_file_size": func() int64 { return int64(rubyToI(pg().setting("attachment_max_size"))) * 1024 },
			"merge_hash": func(a, b *rails.Hash) *rails.Hash {
				out := rails.NewHash()
				if a != nil {
					out.Update(a)
				}
				if b != nil {
					out.Update(b)
				}
				return out
			},
			"str_slice_contains_id": func(ids []int64, id int64) bool {
				for _, x := range ids {
					if x == id {
						return true
					}
				}
				return false
			},
			"itoa": func(n any) string { return rails.ToS(n) },
		}
	})
}

func rubyToI(s string) int {
	n := 0
	s = strings.TrimSpace(s)
	neg := false
	if strings.HasPrefix(s, "-") {
		neg, s = true, s[1:]
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	if neg {
		return -n
	}
	return n
}

func contentAuthor(c *domain.WikiContentVersion) any {
	if c == nil || c.Author == nil {
		return nil
	}
	return c.Author
}

// breadcrumb は ApplicationHelper#breadcrumb(*args)。
func breadcrumb(elements ...any) html {
	var parts []string
	for _, e := range elements {
		switch x := e.(type) {
		case []any:
			for _, y := range x {
				parts = append(parts, string(rails.H(y)))
			}
		case []html:
			for _, y := range x {
				parts = append(parts, string(y))
			}
		case nil:
		default:
			parts = append(parts, string(rails.H(x)))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return rails.ContentTag("p", html(strings.Join(parts, " » ")+" » "), rails.NewHash("class", "breadcrumb"))
}

// wikiPageBreadcrumb は WikiHelper#wiki_page_breadcrumb（pages は page.ancestors.reverse）。
func wikiPageBreadcrumb(pages []*domain.WikiPage) html {
	var links []html
	for _, p := range pages {
		links = append(links, rails.LinkTo(rails.H(p.PrettyTitle()), redmine.WikiPagePath(p.Project, p.Title), nil))
	}
	return breadcrumb(links)
}

// renderPageHierarchy は ApplicationHelper#render_page_hierarchy(pages, node, :timestamp => timestamp)。
// tree は parent_id（根は 0）→ ページの map。
func renderPageHierarchy(p *Page, tree any, node int64, timestamp bool) html {
	pages := wikiTree(tree)
	if pages == nil {
		return ""
	}
	var b strings.Builder
	var walk func(node int64)
	walk = func(node int64) {
		list, ok := pages[node]
		if !ok {
			return
		}
		b.WriteString("<ul class=\"pages-hierarchy\">\n")
		for _, pg := range list {
			b.WriteString("<li>")
			href := "#" + pg.Title
			if !(p.Controller == "wiki" && p.Action == "export") {
				href = redmine.WikiPagePath(pg.Project, pg.Title)
			}
			var title any
			if timestamp && pg.HasContent {
				title = p.l("label_updated_time", p.Loc.DistanceOfTimeInWords(p.now(), pg.UpdatedOn))
			}
			b.WriteString(string(rails.LinkTo(rails.H(pg.PrettyTitle()), href, rails.NewHash("title", title))))
			if _, ok := pages[pg.ID]; ok {
				b.WriteString("\n")
				walk(pg.ID)
			}
			b.WriteString("</li>\n")
		}
		b.WriteString("</ul>\n")
	}
	walk(node)
	return html(b.String())
}

// wikiTree は render_page_hierarchy の pages 引数を map に変換する。
func wikiTree(v any) map[int64][]*domain.WikiPage {
	switch t := v.(type) {
	case map[int64][]*domain.WikiPage:
		return t
	default:
		// 名前付きの map 型（handler.WikiPagesTree）
		if m, ok := rails.Send(v, "to_map").(map[int64][]*domain.WikiPage); ok {
			return m
		}
	}
	return nil
}

// wikiPageOptionsForSelect は WikiHelper#wiki_page_options_for_select(pages, selected)。
func wikiPageOptionsForSelect(pages []*domain.WikiPage, selected int64) html {
	byParent := map[int64][]*domain.WikiPage{}
	ids := map[int64]bool{}
	for _, p := range pages {
		ids[p.ID] = true
	}
	for _, p := range pages {
		var k int64
		if p.ParentID != nil && ids[*p.ParentID] {
			k = *p.ParentID
		} else if p.ParentID != nil {
			// pages.group_by(&:parent): 親が一覧に無ければその親のキーに入り、表示されない
			k = -*p.ParentID
		}
		byParent[k] = append(byParent[k], p)
	}
	var b strings.Builder
	var walk func(parent int64, level int)
	walk = func(parent int64, level int) {
		for _, p := range byParent[parent] {
			indent := ""
			if level > 0 {
				indent = strings.Repeat("&nbsp;", level*2) + "&#187; "
			}
			b.WriteString(string(rails.ContentTag("option", html(indent+string(rails.H(p.PrettyTitle()))),
				rails.NewHash("value", strconv.FormatInt(p.ID, 10), "selected", selected != 0 && selected == p.ID))))
			walk(p.ID, level+1)
		}
	}
	walk(0, 0)
	return html(b.String())
}

// otherFormatLink は Redmine::Views::OtherFormatsBuilder#link_to(name, url)。
func otherFormatLink(name, url string) html {
	return rails.ContentTag("span", rails.LinkTo(name, url, rails.NewHash("class", strings.ToLower(name), "rel", "nofollow")), nil)
}

// ---------------------------------------------------------------- 添付

func refAttachment(a *domain.Attachment) *redmine.Attachment {
	r := &redmine.Attachment{ID: a.ID, Filename: a.Filename, Filesize: a.Filesize}
	r.Description.String, r.Description.Valid = a.Description, a.Description != ""
	r.ContentType.String, r.ContentType.Valid = a.ContentType, a.ContentType != ""
	r.CreatedAt.Time = a.CreatedOn
	return r
}

func refAttachments(atts []*domain.Attachment) []*redmine.Attachment {
	out := make([]*redmine.Attachment, len(atts))
	for i, a := range atts {
		out[i] = refAttachment(a)
	}
	return out
}

func thumbnailsSize(p *Page) int {
	n := rubyToI(p.setting("thumbnails_size"))
	if n <= 0 {
		n = 100
	}
	return n
}

// linkToDomainAttachment は link_to_attachment(attachment, options)。
func (d *Deps) linkToDomainAttachment(p *Page, a *domain.Attachment, opts *rails.Hash) html {
	o := redmine.LinkToAttachmentOptions{HTML: rails.NewHash()}
	if opts != nil {
		for _, e := range opts.Entries() {
			switch e.Key {
			case "text":
				o.Text = rails.ToS(e.Value)
			case "icon":
				o.Icon = rails.ToS(e.Value)
			case "download":
				o.Download = truthy(e.Value)
			case "only_path":
				o.FullURL = e.Value == false
			default:
				o.HTML.Set(e.Key, e.Value)
			}
		}
	}
	return d.WikiRenderer(p).LinkToAttachment(refAttachment(a), o)
}

// linkToAttachments は AttachmentsHelper#link_to_attachments(container, options)（attachments/_links を描画）。
// kind / id はコンテナ、atts は container.attachments（author 込み）。opts: thumbnails, author, editable, deletable。
func (d *Deps) linkToAttachments(r *view.Render, p *Page, kind string, id int64, atts []*domain.Attachment, opts *rails.Hash) (html, error) {
	if len(atts) == 0 {
		return "", nil
	}
	o := rails.NewHash("author", true)
	if opts != nil {
		o.Update(opts)
	}
	thumbnails := truthy(o.Get("thumbnails")) && p.settingBool("thumbnails_enabled")
	var images []*domain.Attachment
	for _, a := range atts {
		if a.Thumbnailable() {
			images = append(images, a)
		}
	}
	containerPath := "/attachments/" + pluralKind(kind) + "/" + strconv.FormatInt(id, 10)
	return r.Partial("attachments/links", map[string]any{
		"attachments":   atts,
		"options":       o,
		"thumbnails":    thumbnails,
		"images":        images,
		"editPath":      containerPath + "/edit",
		"downloadPath":  containerPath + "/download",
		"containerKind": kind,
	})
}

// pluralKind は container_attachments_edit_path の object_type（Rails の複数形）。
func pluralKind(kind string) string {
	switch kind {
	case "news":
		return "news"
	}
	return kind + "s"
}

// ---------------------------------------------------------------- ツールバー

// wikitoolbarFor は Redmine::WikiFormatting::*::Helper#wikitoolbar_for(field_id, preview_url)。
func (d *Deps) wikitoolbarFor(r *view.Render, p *Page, fieldID, previewURL string, included *bool) html {
	switch p.setting("text_formatting") {
	case "textile", "common_mark", "markdown":
	default:
		return ""
	}
	d.headsForWikiFormatter(r, p, included)
	return rails.JavascriptTag("var wikiToolbar = new jsToolBar(document.getElementById('"+fieldID+"')); "+
		"wikiToolbar.setHelpLink('"+rails.EscapeJavascriptString(urlroot.Path("/help/wiki_syntax"))+"'); "+
		"wikiToolbar.setPreviewUrl('"+rails.EscapeJavascriptString(urlroot.Path(previewURL))+"'); "+
		"wikiToolbar.draw();", nil)
}

// headsForWikiFormatter は heads_for_wiki_formatter（jsToolBar の JS / CSS を header_tags に 1 回だけ加える）。
func (d *Deps) headsForWikiFormatter(r *view.Render, p *Page, included *bool) {
	var lib string
	switch p.setting("text_formatting") {
	case "textile":
		lib = "textile"
	case "common_mark", "markdown":
		lib = "common_mark"
	default:
		return
	}
	if !*included {
		lang := []string{"c", "cpp", "csharp", "css", "diff", "go", "groovy", "html", "java", "javascript", "objc", "perl", "php", "python", "r", "ruby", "sass", "scala", "shell", "sql", "swift", "xml", "yaml"}
		langJSON, _ := json.Marshal(lang)
		mimes := `["image/avif","image/gif","image/jpeg","image/png","image/tiff","image/webp","image/x-ms-bmp"]`
		locale := "en"
		if p.Loc != nil {
			locale = strings.ToLower(p.Loc.Lang)
		}
		head := d.jsInclude("jstoolbar/jstoolbar") + d.jsInclude("jstoolbar/"+lib) + d.jsInclude("jstoolbar/lang/jstoolbar-"+locale) +
			rails.JavascriptTag("var wikiImageMimeTypes = "+mimes+";var userHlLanguages = "+string(langJSON)+";", nil) +
			d.stylesheetLinkTag(p, "jstoolbar")
		if r != nil {
			r.ContentFor("header_tags", head)
		}
		*included = true
	}
}

// listAutofillDataAttributes は ApplicationHelper#wiki_textarea_stimulus_attributes（6.1 の list_autofill_data_attributes）。
func listAutofillDataAttributes(p *Page) *rails.Hash {
	return WikiTextareaStimulusAttributes(p.setting("text_formatting"))
}

// WikiTextareaStimulusAttributes は ApplicationHelper#wiki_textarea_stimulus_attributes
// （Setting.text_formatting が空なら {}。リストの自動補完・Tab による字下げ #44061・表の貼り付け #43950）。
func WikiTextareaStimulusAttributes(textFormatting string) *rails.Hash {
	if strings.TrimSpace(textFormatting) == "" {
		return rails.NewHash()
	}
	return rails.NewHash(
		"controller", "list-autofill selection-indent table-paste",
		"action", "beforeinput->list-autofill#handleBeforeInput keydown.tab->selection-indent#run keydown.shift+tab->selection-indent#run paste->table-paste#handlePaste",
		"list_autofill_text_formatting_param", textFormatting,
		"selection_indent_text_formatting_param", textFormatting,
		"table_paste_text_formatting_param", textFormatting)
}

// updateDataSourcesForAutoComplete は ApplicationHelper#update_data_sources_for_auto_complete。
func updateDataSourcesForAutoComplete(sources *rails.Hash) html {
	var b strings.Builder
	b.WriteString("{")
	for i, e := range sources.Entries() {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(rails.ToJSON(e.Key) + ":" + rails.ToJSON(urlroot.Path(rails.ToS(e.Value))))
	}
	b.WriteString("}")
	return rails.JavascriptTag("rm.AutoComplete.dataSources = Object.assign(rm.AutoComplete.dataSources, JSON.parse('"+b.String()+"'));", nil)
}
