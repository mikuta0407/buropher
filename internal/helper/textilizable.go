// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package helper

// ApplicationHelper#textilizable のテンプレート関数（本体は internal/textformat/redmine）。

import (
	"html/template"
	"time"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// TextObject は textilizable の :object に渡せる値（ドメインの型はこれを実装して渡す）。
type TextObject interface {
	TextObject() *redmine.Object
}

// WikiRenderer は Page に対応する redmine.Renderer（リクエスト内で 1 つ）。
func (d *Deps) WikiRenderer(p *Page) *redmine.Renderer {
	if p.wikiRenderer != nil {
		return p.wikiRenderer
	}
	u := p.User
	if u == nil {
		u = &domain.User{Principal: domain.Principal{Kind: domain.KindAnonymousUser}}
	}
	r := &redmine.Renderer{
		User:               u,
		Project:            p.Project,
		Loc:                p.Loc,
		TextFormatting:     p.setting("text_formatting"),
		UserFormat:         p.userFormat(),
		IconsPath:          d.assetPath("icons.svg"),
		Now:                p.now,
		ControllerPath:     p.Controller,
		ActionName:         p.Action,
		PreviewAttachments: p.PreviewAttachments,
		Logger:             p.Logger,
		TablesortEnabled:   p.settingBool("wiki_tablesort_enabled"),
	}
	if p.Loc != nil && p.Loc.Location != nil {
		loc := p.Loc.Location
		r.Today = func() time.Time { return p.now().In(loc) }
	}
	if p.BaseURL != "" {
		r.BaseURL = p.BaseURL
	} else if p.Request != nil {
		r.BaseURL = httpx.RequestRootURL(p.Request)
	}
	if p.DB != nil {
		az := p.authorizer()
		if az == nil {
			az = authz.New(p.DB, u)
		}
		r.Store = redmine.NewDBStore(p.ctx(), p.DB, az)
	}
	p.wikiRenderer = r
	return r
}

// textilizable は textilizable(text, options) / textilizable(object, attribute, options)。
//
//	{{textilizable .Text}}
//	{{textilizable .Text (hash "object" .Obj "headings" false)}}
//	{{textilizable .Issue "description" (hash ...)}}（.Issue は TextObject を実装し、attribute は rails.Send で読む）
//
// オプション: object, project, only_path, headings, inline_attachments, edit_section_links
// （*redmine.EditSectionLinks または hash "project_id" "id"）, wiki_links, formatting, attachments。
func (d *Deps) textilizable(p *Page, v any, args ...any) template.HTML {
	var opts *rails.Hash
	if n := len(args); n > 0 {
		if hh, ok := args[n-1].(*rails.Hash); ok {
			opts = hh
			args = args[:n-1]
		}
	}
	var obj any
	text := ""
	switch len(args) {
	case 0:
		text = rails.ToS(v)
		if opts != nil {
			obj = opts.Get("object")
		}
	case 1:
		obj = v
		text = rails.ToS(rails.Send(v, rails.ToS(args[0])))
	default:
		return ""
	}
	o := redmine.Options{Object: toTextObject(obj)}
	if opts != nil {
		if pr := toProject(opts.Get("project")); pr != nil {
			o.Project = pr
		}
		o.FullURL = opts.Get("only_path") == false
		o.NoHeadings = opts.Get("headings") == false
		o.NoInlineAttachments = opts.Get("inline_attachments") == false
		o.NoFormatting = opts.Get("formatting") == false
		if w := opts.Get("wiki_links"); w != nil {
			o.WikiLinks = rails.ToS(w)
		}
		switch e := opts.Get("edit_section_links").(type) {
		case *redmine.EditSectionLinks:
			o.EditSectionLinks = e
		case *rails.Hash:
			o.EditSectionLinks = &redmine.EditSectionLinks{ProjectID: rails.ToS(e.Get("project_id")), ID: rails.ToS(e.Get("id"))}
		}
		if a, ok := opts.Get("attachments").([]*redmine.Attachment); ok {
			o.Attachments = a
		}
	}
	return d.WikiRenderer(p).Textilizable(text, o)
}

func toTextObject(v any) *redmine.Object {
	switch o := v.(type) {
	case *redmine.Object:
		return o
	case TextObject:
		return o.TextObject()
	}
	return nil
}
