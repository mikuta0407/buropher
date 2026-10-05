// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package helper

// チケット画面のテンプレート関数（ApplicationHelper / IssuesHelper のうちモデルに依存しないもの）。
// TODO(dedupe): actions_dropdown / context_menu / link_to_context_menu / capitalize は admin users・projects
// ブランチにも同名のテンプレート関数がある（同名の登録は後勝ちで、出力は同じ）。

import (
	"html/template"
	ttemplate "text/template"

	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

const contextMenuIncludedKey = "helper.context_menu_included"

func init() {
	registerFuncs(func(d *Deps, r *view.Render, pg func() *Page) ttemplate.FuncMap {
		return ttemplate.FuncMap{
			"actions_dropdown":     func(content any) html { return d.issuesActionsDropdown(pg(), content) },
			"context_menu":         func() html { return d.issuesContextMenu(r, pg()) },
			"link_to_context_menu": func() html { return d.issuesLinkToContextMenu(pg()) },
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
