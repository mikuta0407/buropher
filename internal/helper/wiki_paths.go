// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package helper

import (
	"net/url"
	"strconv"
	"strings"
	ttemplate "text/template"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/urlroot"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは Wiki の画面で使う URL 生成（url_for の代わり）のテンプレート関数。

// WikiProjectOption は wiki_page_wiki_options_for_select の 1 項目（project_tree の level 付き）。
type WikiProjectOption struct {
	Project  *domain.Project
	WikiID   int64
	Level    int
	Selected bool
}

// wikiProjectOptions は WikiHelper#wiki_page_wiki_options_for_select の出力
// （project_tree_options_for_select に {:value => wiki_id, :selected => ...} を渡したもの）。
func wikiProjectOptions(opts []WikiProjectOption) html {
	var b strings.Builder
	for _, o := range opts {
		prefix := ""
		if o.Level > 0 {
			prefix = strings.Repeat("&nbsp;", 2*o.Level) + "&#187; "
		}
		var wid any
		if o.WikiID != 0 {
			wid = o.WikiID
		}
		b.WriteString(string(rails.ContentTag("option", html(prefix+string(rails.H(o.Project.Name))),
			rails.NewHash("value", wid, "selected", o.Selected))))
	}
	return html(b.String())
}

// escapeSegment は Rails のパスパラメータのエスケープ（Journey::Router::Utils.escape_segment）。
func escapeSegment(s string) string {
	const keep = "-._~!$&'()*+,;=:@"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.IndexByte(keep, ch) >= 0 {
			b.WriteByte(ch)
			continue
		}
		b.WriteString("%" + strings.ToUpper(strconv.FormatInt(int64(ch)|0x100, 16)[1:]))
	}
	return b.String()
}

func wikiProjectParam(p *domain.Project) string {
	if p == nil {
		return ""
	}
	if p.Identifier != "" {
		return p.Identifier
	}
	return strconv.FormatInt(p.ID, 10)
}

func init() {
	registerFuncs(func(d *Deps, r *view.Render, pg func() *Page) ttemplate.FuncMap {
		return ttemplate.FuncMap{
			"project_param":    func(p any) string { return wikiProjectParam(toProject(p)) },
			"url_encode_param": func(s string) string { return url.QueryEscape(s) },
			"url_segment":      escapeSegment,
			"safe_concat": func(parts ...any) html {
				var b strings.Builder
				for _, p := range parts {
					b.WriteString(string(rails.H(p)))
				}
				return html(b.String())
			},
			"wiki_project_options": wikiProjectOptions,
			// wiki_format_url は OtherFormatsBuilder#link_to の URL（{:id => title, :version => params[:version], :format => f}）。
			"wiki_format_url": func(title, format, version string) string {
				path := redmine.WikiPagePath(pg().Project, title)
				if version != "" {
					if _, err := strconv.Atoi(version); err == nil {
						return path + "/" + version + "." + format
					}
					return path + "." + format + "?version=" + url.QueryEscape(version)
				}
				return path + "." + format
			},
			// wiki_activity_atom_url は {:controller => 'activities', :action => 'index', :id => @project,
			// :show_wiki_edits => 1, :key => User.current.atom_key, :format => 'atom'}（full なら絶対 URL）。
			"wiki_activity_atom_url": func(projectKey, key string, full bool) string {
				u := "/projects/" + escapeSegment(projectKey) + "/activity.atom?"
				if key != "" {
					u += "key=" + url.QueryEscape(key) + "&"
				}
				u += "show_wiki_edits=1"
				if full && pg().Request != nil {
					return httpx.RequestBaseURL(pg().Request) + urlroot.Path(u)
				}
				return u
			},
			// wiki_edit_section_links は :edit_section_links => (@sections_editable && {...})。
			"wiki_edit_section_links": func(editable bool, page *domain.WikiPage) any {
				if !editable || page == nil {
					return nil
				}
				return &redmine.EditSectionLinks{ProjectID: wikiProjectParam(page.Project), ID: page.Title}
			},
			// wiki_edit_cancel_path は WikiHelper#wiki_page_edit_cancel_path(page)。
			"wiki_edit_cancel_path": func(page *domain.WikiPage, parent *domain.WikiPage) string {
				if page.NewRecord() {
					if parent != nil {
						return redmine.WikiPagePath(parent.Project, parent.Title)
					}
					return "/projects/" + escapeSegment(wikiProjectParam(page.Project)) + "/wiki/index"
				}
				return redmine.WikiPagePath(page.Project, page.Title)
			},
			// watchers_autocomplete_path は watchers_autocomplete_for_mention_path(project_id:, q: '', object_type: 'wiki_page', object_id: page.id)。
			"watchers_autocomplete_path": func(project any, page *domain.WikiPage) string {
				q := ""
				if !page.NewRecord() {
					q = "object_id=" + strconv.FormatInt(page.ID, 10) + "&"
				}
				return "/watchers/autocomplete_for_mention?" + q + "object_type=wiki_page&project_id=" +
					url.QueryEscape(wikiProjectParam(toProject(project))) + "&q="
			},
			// back_link_url は link_to(..., :back)（Referer があればそれ、無ければ javascript:history.back()）。
			"back_link_url": func() string {
				if p := pg(); p.Request != nil {
					if ref := p.Request.Referer(); ref != "" {
						return ref
					}
				}
				return "javascript:history.back()"
			},
		}
	})
}
