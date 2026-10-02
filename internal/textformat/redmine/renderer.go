// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

// Package redmine は Redmine 6.1.2 の textilizable（app/helpers/application_helper.rb:901-946）と
// 関連ヘルパー・マクロ（lib/redmine/wiki_formatting/macros.rb）・引用返信（lib/redmine/quote_reply.rb）の移植。
//
// 処理順は Redmine と同じ:
//
//  1. catch_macros（{{name(args)}} をプレースホルダへ退避）
//  2. 書式変換（Setting.text_formatting: textile / common_mark / それ以外は NullFormatter）
//  3. parse_sections（セクション編集リンク）
//  4. parse_non_pre_blocks（<pre>/<code> の外側だけ）: parse_inline_attachments → parse_hires_images →
//     parse_wiki_links → parse_redmine_links → inject_macros
//  5. parse_headings（見出しのアンカー）
//  6. replace_toc（{{toc}} の目次）
//
// HTML は Redmine と同じく文字列への正規表現置換で処理する（DOM にすると出力がずれるため）。
// データの参照は Store（User.current の可視性込み）を通して行う。
package redmine

import (
	"html/template"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/clock"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/textformat/commonmark"
	"github.com/mikuta0407/buropher/internal/textformat/highlight"
	"github.com/mikuta0407/buropher/internal/textformat/textile"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// Object は textilizable の :object（添付・プロジェクト・Wiki ページの解決に使う）。
type Object struct {
	// Kind は issue|journal|wiki_content|wiki_content_version|news|message|document|version|project|changeset。
	Kind string
	ID   int64
	// Project は obj.project（Kind が project のときは nil で Self を使う）。
	Project *domain.Project
	// Self は Kind が project のときのプロジェクト自身。
	Self *domain.Project
	// JournalizedID は journal のときのチケット id。
	JournalizedID int64
	// Page は wiki_content / wiki_content_version のときのページ。
	Page *WikiPage
}

func (o *Object) isWikiContent() bool {
	return o != nil && (o.Kind == "wiki_content" || o.Kind == "wiki_content_version") && o.Page != nil
}

// project は obj.respond_to?(:project) ? obj.project : nil。
func (o *Object) project() *domain.Project {
	if o == nil || o.Kind == "project" {
		return nil
	}
	return o.Project
}

// attachmentContainer は obj.attachments の (container_kind, id)。添付を持たなければ ok=false。
// Journal の場合は journalized（チケット）の添付になる（parse_inline_attachments / thumbnail マクロ）。
func (o *Object) attachmentContainer() (string, int64, bool) {
	if o == nil {
		return "", 0, false
	}
	switch o.Kind {
	case "issue", "news", "message", "document", "version", "project":
		return o.Kind, o.ID, true
	case "journal":
		return "issue", o.JournalizedID, o.JournalizedID != 0
	case "wiki_content", "wiki_content_version":
		if o.Page != nil {
			return "wiki_page", o.Page.ID, true
		}
	}
	return "", 0, false
}

// EditSectionLinks は :edit_section_links（wiki#edit の URL パラメータ）。
type EditSectionLinks struct {
	// ProjectID はプロジェクトの to_param（識別子）。
	ProjectID string
	// ID はページのタイトル。
	ID string
}

// Options は textilizable のオプション。
type Options struct {
	// Object は :object。
	Object *Object
	// Project は :project（nil なら Renderer.Project、それも nil なら Object のプロジェクト）。
	Project *domain.Project
	// FullURL は :only_path => false（既定は相対パス）。
	FullURL bool
	// NoHeadings は :headings => false。
	NoHeadings bool
	// NoInlineAttachments は :inline_attachments => false。
	NoInlineAttachments bool
	// EditSectionLinks は :edit_section_links。
	EditSectionLinks *EditSectionLinks
	// WikiLinks は :wiki_links（"" / "local" / "anchor"）。
	WikiLinks string
	// NoFormatting は :formatting => false。
	NoFormatting bool
	// Attachments は :attachments。
	Attachments []*Attachment
}

// Renderer は textilizable を呼ぶビューコンテキスト（1 リクエスト分の状態）。
type Renderer struct {
	// Store はデータの参照先（User.current の可視性込み）。
	Store Store
	// User は User.current（nil なら匿名）。
	User *domain.User
	// Project は @project。
	Project *domain.Project
	// Loc は l() の翻訳（nil なら英語の既定 Bundle）。
	Loc *i18n.Localizer
	// TextFormatting は Setting.text_formatting。
	TextFormatting string
	// UserFormat は Setting.user_format。
	UserFormat string
	// IconsPath は asset_path("icons.svg")（空なら "/assets/icons.svg"）。
	IconsPath string
	// BaseURL は only_path: false のときの "http://host"（末尾スラッシュなし）。
	BaseURL string
	// Now は現在時刻（nil なら clock.Now）。
	Now func() time.Time
	// Today は User.current.today（nil なら Now の日付（UTC））。
	Today func() time.Time
	// RandomHex は Redmine::Utils.random_hex（nil なら暗号乱数）。
	RandomHex func(n int) string
	// ControllerPath / ActionName は controller_path / action_name（thumbnail マクロ・render_page_hierarchy が参照）。
	ControllerPath, ActionName string
	// PreviewAttachments は @attachments（プレビュー時に thumbnail マクロが参照）。
	PreviewAttachments []*Attachment
	// DisableHardBreaks は common_mark_enable_hardbreaks: false。
	DisableHardBreaks bool
	// Logger はデータ参照エラーの記録先（nil なら slog.Default()）。
	Logger *slog.Logger

	onlyPath          bool
	includedWikiPages []int64
	issueCSS          map[int64]string
	groupIDs          []int64
	groupIDsLoaded    bool
}

func (r *Renderer) l(key string, args ...any) string {
	if r.Loc == nil {
		r.Loc = i18n.Default().NewLocalizer("en", i18n.Settings{}, nil)
	}
	return r.Loc.L(key, args...)
}

func (r *Renderer) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return clock.Now()
}

func (r *Renderer) today() time.Time {
	if r.Today != nil {
		t := r.Today()
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	}
	t := r.now().UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func (r *Renderer) logErr(what string, err error) {
	if err == nil {
		return
	}
	lg := r.Logger
	if lg == nil {
		lg = slog.Default()
	}
	lg.Error("textilizable: "+what, "err", err)
}

func (r *Renderer) currentUser() *domain.User {
	if r.User == nil {
		return &domain.User{Principal: domain.Principal{Kind: domain.KindAnonymousUser}}
	}
	return r.User
}

func (r *Renderer) allowedTo(perm string, p *domain.Project) bool {
	if p == nil || r.Store == nil {
		return false
	}
	ok, err := r.Store.AllowedTo(perm, p)
	r.logErr("allowed_to", err)
	return ok
}

// pageState は 1 回の textilizable 呼び出しの状態（@parsed_headings 等）。
type pageState struct {
	parsedHeadings []heading
	headingAnchors map[string]int
	currentSection int
}

type heading struct {
	level  int
	anchor string
	item   string
}

// Textilizable は textilizable(text, options)。
func (r *Renderer) Textilizable(text string, opts Options) template.HTML {
	if blank(text) {
		return ""
	}
	obj := opts.Object
	project := opts.Project
	if project == nil {
		project = r.Project
	}
	if project == nil {
		project = obj.project()
	}
	r.onlyPath = !opts.FullURL

	macros := map[int]string{}
	text = r.catchMacros(text, macros)

	if opts.NoFormatting {
		text = h(text)
	} else {
		text = r.toHTML(text)
	}

	st := &pageState{headingAnchors: map[string]int{}}
	text = r.parseSections(st, text, opts)
	text = r.parseNonPreBlocks(text, obj, macros, opts, func(txt string) string {
		txt = r.parseInlineAttachments(txt, obj, opts)
		txt = parseHiresImages(txt)
		txt = r.parseWikiLinks(txt, project, obj, opts)
		txt = r.parseRedmineLinks(txt, project, obj, opts)
		return txt
	})
	text = r.parseHeadings(st, text, obj, opts)
	if len(st.parsedHeadings) > 0 {
		text = r.replaceTOC(text, st.parsedHeadings)
	}
	return template.HTML(text)
}

// toHTML は Redmine::WikiFormatting.to_html(Setting.text_formatting, text)。
func (r *Renderer) toHTML(text string) string {
	switch r.TextFormatting {
	case "textile":
		return textile.Format(text, &textile.Options{Highlight: textileHighlighter})
	case "common_mark":
		return commonmark.Format(text, commonmark.Options{
			DisableHardBreaks: r.DisableHardBreaks,
			IconsPath:         r.iconsPath(),
			Translate:         func(key string) string { return r.l(key) },
		})
	default:
		return NullFormat(text)
	}
}

func textileHighlighter(lang, code string) (string, bool) {
	if !highlight.LanguageSupported(lang) {
		return "", false
	}
	return highlight.HighlightByLanguage(code, lang), true
}

func (r *Renderer) iconsPath() string {
	if r.IconsPath != "" {
		return r.IconsPath
	}
	return "/assets/icons.svg"
}

// NullFormat は NullFormatter::Formatter#to_html（text_formatting が空・未知のとき）。
func NullFormat(text string) string {
	t := h(text)
	t = textile.AutoLink(t)
	t = textile.AutoMailto(t)
	t = textile.RestoreRedmineLinks(t)
	return string(rails.SimpleFormat(template.HTML(t), nil, rails.NewHash("sanitize", false)))
}

// ---- parse_non_pre_blocks ----

var reNonPre = rxmi(`(.*?)(<(/)?(pre|code)(.*?)>|\z)`)

// parseNonPreBlocks は parse_non_pre_blocks。<pre>/<code> の外側だけ f を適用し、マクロを挿入する。
func (r *Renderer) parseNonPreBlocks(text string, obj *Object, macros map[int]string, opts Options, f func(string) string) string {
	var tags []string
	var parsed strings.Builder
	rest := text
	first := true
	for first || rest != "" {
		first = false
		m := match(reNonPre, rest)
		if m == nil {
			break
		}
		consumed := len(m.all())
		chunk, fullTag, closing, tag := m.s(1), m.s(2), m.ok(3), m.s(4)
		if len(tags) == 0 {
			chunk = f(chunk)
			if len(macros) > 0 {
				chunk = r.injectMacros(chunk, obj, macros, true, opts)
			}
		} else if len(macros) > 0 {
			chunk = r.injectMacros(chunk, obj, macros, false, opts)
		}
		parsed.WriteString(chunk)
		if m.ok(4) {
			if closing {
				if n := len(tags); n > 0 && strings.EqualFold(tags[n-1], tag) {
					tags = tags[:n-1]
				}
			} else {
				tags = append(tags, strings.ToLower(tag))
			}
			parsed.WriteString(fullTag)
		}
		rest = rest[consumed:]
		if consumed == 0 {
			break
		}
	}
	for i := len(tags) - 1; i >= 0; i-- {
		parsed.WriteString("</" + tags[i] + ">")
	}
	return parsed.String()
}

// ---- parse_sections / parse_headings / replace_toc ----

var reHeading = rxmi(`(<h(\d)( [^>]+)?>(.+?)</h(\d)>)`)

// parseSections は parse_sections（:edit_section_links 指定時のみ）。
func (r *Renderer) parseSections(st *pageState, text string, opts Options) string {
	if opts.EditSectionLinks == nil {
		return text
	}
	return gsub(reHeading, text, func(m md) string {
		hd, level := m.s(1), m.s(2)
		st.currentSection++
		if st.currentSection <= 1 {
			return hd
		}
		label := r.l("button_edit_section")
		u := "/projects/" + escapeSegment(opts.EditSectionLinks.ProjectID) + "/wiki/" + escapeSegment(opts.EditSectionLinks.ID) +
			"/edit?section=" + strconv.Itoa(st.currentSection)
		link := rails.LinkTo(r.spriteIcon("edit", label, false), u, rails.NewHash("class", "icon-only icon-edit"))
		return string(rails.ContentTag("div", link, rails.NewHash("class", "contextual heading-"+level,
			"title", label, "id", "section-"+strconv.Itoa(st.currentSection)))) + hd
	})
}

// parseHeadings は parse_headings。
func (r *Renderer) parseHeadings(st *pageState, text string, obj *Object, opts Options) string {
	if opts.NoHeadings {
		return text
	}
	return gsub(reHeading, text, func(m md) string {
		level, _ := strconv.Atoi(m.s(2))
		attrs, content := m.s(3), m.s(4)
		item := rubyStrip(string(rails.StripTags(content)))
		anchor := sanitizeAnchorName(item)
		if opts.WikiLinks == "anchor" && obj.isWikiContent() {
			anchor = obj.Page.Title + "_" + anchor
		}
		st.headingAnchors[anchor]++
		if idx := st.headingAnchors[anchor]; idx > 1 {
			anchor = anchor + "-" + strconv.Itoa(idx)
		}
		st.parsedHeadings = append(st.parsedHeadings, heading{level, anchor, item})
		lv := strconv.Itoa(level)
		return `<a name="` + anchor + `"></a>` + "\n" + `<h` + lv + ` ` + attrs + `>` + content +
			`<a href="#` + anchor + `" class="wiki-anchor">&para;</a></h` + lv + `>`
	})
}

var reTOC = rxi(`<p>\{\{((<|&lt;)|(>|&gt;))?toc\}\}</p>`)

// replaceTOC は replace_toc。
func (r *Renderer) replaceTOC(text string, headings []heading) string {
	return gsub(reTOC, text, func(m md) string {
		left, right := m.ok(2), m.ok(3)
		var hs []heading
		for _, hd := range headings {
			if hd.level <= 4 {
				hs = append(hs, hd)
			}
		}
		if len(hs) == 0 {
			return ""
		}
		divClass := "toc"
		if right {
			divClass += " right"
		}
		if left {
			divClass += " left"
		}
		var out strings.Builder
		out.WriteString(`<ul class="` + divClass + `"><li><strong>` + r.l("label_table_of_contents") + `</strong></li><li>`)
		root := hs[0].level
		for _, hd := range hs {
			if hd.level < root {
				root = hd.level
			}
		}
		current := root
		started := false
		for _, hd := range hs {
			switch {
			case hd.level > current:
				out.WriteString(strings.Repeat("<ul><li>", hd.level-current))
			case hd.level < current:
				out.WriteString(strings.Repeat("</li></ul>\n", current-hd.level) + "</li><li>")
			case started:
				out.WriteString("</li><li>")
			}
			out.WriteString(`<a href="#` + hd.anchor + `">` + hd.item + `</a>`)
			current = hd.level
			started = true
		}
		out.WriteString(strings.Repeat("</li></ul>", current-root))
		out.WriteString("</li></ul>")
		return out.String()
	})
}

var (
	reAnchorStrip = rx(`[^` + spIn + `\-` + wordIn + `]`)
	reAnchorSpace = rx(`[` + spIn + `]+(?:\-+[` + spIn + `]*)?`)
)

// SanitizeAnchorName は sanitize_anchor_name。
func SanitizeAnchorName(anchor string) string { return sanitizeAnchorName(anchor) }

func sanitizeAnchorName(anchor string) string {
	anchor = gsub(reAnchorStrip, anchor, func(md) string { return "" })
	return gsub(reAnchorSpace, anchor, func(md) string { return "-" })
}

// ---- hires images ----

var reHires = rxi(`src="([^"]+@(\dx)\.(bmp|gif|jpg|jpe|jpeg|png))"`)

// parseHiresImages は parse_hires_images。
func parseHiresImages(text string) string {
	return gsub(reHires, text, func(m md) string {
		return m.all() + ` srcset="` + m.s(1) + ` ` + m.s(2) + `"`
	})
}
