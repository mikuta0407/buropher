// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package helper

import (
	"net/url"
	"strconv"
	"strings"
	ttemplate "text/template"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは管理画面（ワークフロー・カスタムフィールド等）で使う ApplicationHelper の汎用ヘルパー
// （title, toggle_checkboxes_link, link_to_function, delete_link, reorder_handle, checked_image,
// render_tabs, error_messages_for, calendar_for, render_project_nested_lists）の移植。
// Go の識別子の衝突を避けるため、実装は adminH のメソッドにまとめる。

// adminH は 1 描画分のヘルパー（*view.Render と Deps を束ねる）。
type adminH struct {
	d *Deps
	r *view.Render
}

func (h adminH) pg() *Page { return PageOf(h.r) }

func (h adminH) l(key string, args ...any) string { return h.pg().l(key, args...) }

func (h adminH) icon(name string, label any, opts *rails.Hash) html {
	return h.d.spriteIcon(h.pg(), name, label, opts)
}

// AdminRequestFuncs は管理画面用のテンプレート関数（view.Options.RequestFuncs に追加する）。
// title / delete_link / reorder_handle / checked_image / error_messages_for などの共通ヘルパーは
// admin_masters.go（RequestFuncs）のものを使い、ここではワークフロー・カスタムフィールドに固有のものと
// render_tabs / calendar_for を登録する。
func (d *Deps) AdminRequestFuncs(r *view.Render) ttemplate.FuncMap {
	h := adminH{d, r}
	fm := ttemplate.FuncMap{
		"render_tabs":                   h.renderTabs,
		"calendar_for":                  h.calendarFor,
		"include_calendar_headers_tags": h.includeCalendarHeadersTags,
		"progress_bar":                  h.progressBar,
	}
	for k, v := range h.workflowFuncs() {
		fm[k] = v
	}
	for k, v := range h.customFieldFuncs() {
		fm[k] = v
	}
	return fm
}

// title は ApplicationHelper#title(*args)。各引数は文字列か [label, url]（list）。
// html_title にも逆順で追加する。
func (h adminH) title(args ...any) html {
	var parts []string
	var titles []any
	for _, a := range args {
		if l, ok := a.([]any); ok && len(l) >= 2 {
			parts = append(parts, string(rails.LinkTo(l[0], l[1], nil)))
			continue
		}
		parts = append(parts, string(rails.H(rails.ToS(a))))
	}
	for i := len(args) - 1; i >= 0; i-- {
		if l, ok := args[i].([]any); ok && len(l) >= 1 {
			titles = append(titles, rails.ToS(l[0]))
		} else {
			titles = append(titles, rails.ToS(args[i]))
		}
	}
	// html_title args.reverse.map {...}（配列 1 つを渡す）
	h.r.AddTitle(titles)
	return rails.ContentTag("h2", html(strings.Join(parts, " &#187; ")), nil)
}

// progressBar は progress_bar(pct, legend:)（単一値のみ）。
func (h adminH) progressBar(pct any, opts ...*rails.Hash) html {
	n, _ := strconv.Atoi(rails.ToS(pct))
	legend := ""
	if len(opts) > 0 && opts[0] != nil {
		legend = rails.ToS(opts[0].Get("legend"))
	}
	return progressBarHTML(n, legend)
}

// ProgressBar は progress_bar(pct, :legend => legend)（Go コードから使う）。

func progressBarHTML(pct int, legend string) html {
	var cells html
	if pct > 0 {
		cells += rails.ContentTag("td", "", rails.NewHash("style", "width: "+strconv.Itoa(pct)+"%;", "class", "closed", "title", strconv.Itoa(pct)+"%"))
	}
	if 100-pct > 0 {
		cells += rails.ContentTag("td", "", rails.NewHash("style", "width: "+strconv.Itoa(100-pct)+"%;", "class", "todo"))
	}
	return rails.ContentTag("table", rails.ContentTag("tr", cells, nil), rails.NewHash("class", "progress progress-"+strconv.Itoa(pct))) +
		rails.ContentTag("p", legend, rails.NewHash("class", "percent"))
}

// Tab は render_tabs に渡すタブ（{:name, :partial, :label, :url, :onclick}）。
type Tab struct {
	Name    string
	Partial string
	// Label は翻訳キー。
	Label   string
	URL     string
	Onclick string
	// Remote は :remote => true（部分テンプレートが無くても空の tab-content を出す）。
	Remote bool
	// Locals は部分テンプレートに tab と一緒に渡す追加の locals。
	Locals map[string]any
}

// renderTabs は render_tabs(tabs, selected = params[:tab])（common/_tabs）。
func (h adminH) renderTabs(tabs []Tab, selected ...string) (html, error) {
	if len(tabs) == 0 {
		return rails.ContentTag("p", h.l("label_no_data"), rails.NewHash("class", "nodata")), nil
	}
	sel := ""
	if len(selected) > 0 {
		sel = selected[0]
	} else {
		sel = h.pg().Params().String("tab")
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
	var b strings.Builder
	b.WriteString("\n<div class=\"tabs\">\n  <ul>\n")
	var defaultAction string
	for _, t := range tabs {
		action := t.Onclick
		if action == "" && t.Partial != "" {
			action = "showTab('" + t.Name + "', this.href)"
		}
		u := t.URL
		if u == "" {
			u = h.r.Ctx.RequestPath + "?tab=" + url.QueryEscape(t.Name)
		}
		var class, onclick any
		if t.Name == sel {
			class = "selected"
		}
		if action != "" {
			onclick = action + "; return false;"
		}
		b.WriteString("    <li>")
		b.WriteString(string(rails.LinkTo(h.l(t.Label), u, rails.NewHash("id", "tab-"+t.Name, "class", class, "onclick", onclick))))
		b.WriteString("</li>\n")
		if t.Name == sel {
			defaultAction = action
		}
	}
	b.WriteString("  </ul>\n  <div class=\"tabs-buttons\" style=\"display:none;\">\n" +
		"    <button class=\"tab-left icon-only\" type=\"button\" onclick=\"moveTabLeft(this);\">\n      " +
		string(h.icon("angle-left", nil, rails.NewHash("rtl", true))) + "\n    </button>\n" +
		"    <button class=\"tab-right icon-only\" type=\"button\" onclick=\"moveTabRight(this);\">\n      " +
		string(h.icon("angle-right", nil, rails.NewHash("rtl", true))) + "\n    </button>\n  </div>\n</div>\n\n")
	for _, t := range tabs {
		if t.Partial == "" && !t.Remote {
			continue
		}
		var content html
		if t.Partial != "" {
			locals := map[string]any{"tab": t}
			for k, v := range t.Locals {
				locals[k] = v
			}
			var err error
			content, err = h.r.Partial(t.Partial, locals)
			if err != nil {
				return "", err
			}
		}
		var style any
		if t.Name != sel {
			style = "display:none"
		}
		b.WriteString("  ")
		b.WriteString(string(rails.ContentTag("div", content, rails.NewHash("id", "tab-content-"+t.Name, "style", style, "class", "tab-content"))))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	if defaultAction != "" {
		b.WriteString(string(rails.JavascriptTag(defaultAction, nil)))
	}
	b.WriteString("\n")
	return html(b.String()), nil
}

// ErrorMessenger はモデルのエラー（errors.full_messages）を返す。
type ErrorMessenger interface {
	FullErrorMessages() []string
}

const calendarIncludedKey = "helper.calendar_headers_tags_included"

// includeCalendarHeadersTags は include_calendar_headers_tags（datepicker の設定を header_tags に 1 回だけ追加する）。
func (h adminH) includeCalendarHeadersTags() string {
	ctx := h.r.Ctx
	if ctx.Values == nil {
		ctx.Values = map[string]any{}
	}
	if ctx.Values[calendarIncludedKey] == true {
		return ""
	}
	ctx.Values[calendarIncludedKey] = true
	p := h.pg()
	sow := p.setting("start_of_week")
	if strings.TrimSpace(sow) == "" {
		sow = p.l("general_first_day_of_week")
	}
	n, _ := strconv.Atoi(strings.TrimSpace(sow))
	tags := rails.JavascriptTag("var datepickerOptions={dateFormat: 'yy-mm-dd', firstDay: "+strconv.Itoa(n%7)+", "+
		"showOn: 'button', buttonImageOnly: true, buttonImage: '"+h.d.assetPath("calendar.png")+
		"', showButtonPanel: true, showWeek: true, showOtherMonths: true, "+
		"selectOtherMonths: true, changeMonth: true, changeYear: true, "+
		"beforeShow: beforeShowDatePicker};", nil)
	locale := ctx.Locale
	if p.Loc != nil && p.Loc.Bundle != nil {
		// l('jquery.locale', :default => current_language.to_s)
		if s, ok := p.Loc.Bundle.Lookup(p.Loc.Lang, "jquery.locale").(string); ok && s != "" {
			locale = s
		}
	}
	if locale != "en" && locale != "" {
		tags += h.d.jsInclude("i18n/datepicker-" + locale + ".js")
	}
	h.r.ContentFor("header_tags", tags)
	return ""
}

// calendarFor は calendar_for(field_id)。
func (h adminH) calendarFor(fieldID string) html {
	h.includeCalendarHeadersTags()
	return rails.JavascriptTag("$(function() { $('#"+fieldID+"').addClass('date').datepickerFallback(datepickerOptions); });", nil)
}

// NestedProject は render_project_nested_lists の 1 項目（lft 順に並べた上で渡す）。
type NestedProject struct {
	Project  *domain.Project
	Lft, Rgt int
}

// RenderProjectNestedLists は render_project_nested_lists(projects) { |p| ... }。
// projects は lft 順。content は各プロジェクトの内容（nil なら名前）。
func RenderProjectNestedLists(projects []NestedProject, content func(p *domain.Project) html) html {
	if len(projects) == 0 {
		return ""
	}
	var b strings.Builder
	var ancestors []NestedProject
	isDesc := func(p, a NestedProject) bool { return p.Lft > a.Lft && p.Rgt < a.Rgt }
	for _, p := range projects {
		if len(ancestors) == 0 || isDesc(p, ancestors[len(ancestors)-1]) {
			root := ""
			if len(ancestors) == 0 {
				root = "root"
			}
			b.WriteString("<ul class='projects " + root + "'>\n")
		} else {
			ancestors = ancestors[:len(ancestors)-1]
			b.WriteString("</li>")
			for len(ancestors) > 0 && !isDesc(p, ancestors[len(ancestors)-1]) {
				ancestors = ancestors[:len(ancestors)-1]
				b.WriteString("</ul></li>\n")
			}
		}
		classes := "child"
		if len(ancestors) == 0 {
			classes = "root"
		}
		if p.Project.Archived() {
			classes += " archived"
		}
		b.WriteString("<li class='" + classes + "'><div class='" + classes + "'>")
		if content != nil {
			b.WriteString(string(content(p.Project)))
		} else {
			b.WriteString(string(rails.H(p.Project.Name)))
		}
		b.WriteString("</div>\n")
		ancestors = append(ancestors, p)
	}
	b.WriteString(strings.Repeat("</li></ul>\n", len(ancestors)))
	return html(b.String())
}
