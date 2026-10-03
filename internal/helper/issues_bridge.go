// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package helper

// このファイルはチケット画面（handler の issues*.go）が HTML を組み立てるときに使う、
// ApplicationHelper の関数の公開版（ハンドラ側から呼ぶためのもの）。

import (
	"html/template"
	"strconv"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// Icon は sprite_icon(name, label, opts)。
func (d *Deps) Icon(p *Page, name string, label any, opts *rails.Hash) template.HTML {
	return d.spriteIcon(p, name, label, opts)
}

// AssetPath は asset_path(source)。
func (d *Deps) AssetPath(source string) string { return d.assetPath(source) }

// LinkToProject は link_to_project(project)。
func LinkToProject(p *domain.Project) template.HTML { return linkToProject(p, rails.NewHash(), nil) }

// PrincipalUserName は Principal#to_s（domain.User で表したプリンシパル。User は user_format の名前、匿名・組込グループは翻訳名、グループは名前）。
func PrincipalUserName(p *Page, u *domain.User) string {
	if u == nil {
		return ""
	}
	switch u.Kind {
	case domain.KindGroup:
		return u.Principal.Name
	case domain.KindGroupAnonymous:
		return p.l("label_group_anonymous")
	case domain.KindGroupNonMember:
		return p.l("label_group_non_member")
	}
	return p.userName(u, "")
}

// LinkToPrincipal は link_to_principal(principal, options)（:class のみ対応）。
func (d *Deps) LinkToPrincipal(p *Page, u *domain.User, class string) template.HTML {
	if u == nil {
		return ""
	}
	if u.Kind.IsGroup() {
		name := PrincipalUserName(p, u)
		css := "group"
		if class != "" {
			css += " " + class
		}
		return rails.LinkTo(d.spriteIcon(p, "group", nil, nil)+rails.H(name), "/groups/"+strconv.FormatInt(u.ID, 10), rails.NewHash("class", css))
	}
	opts := rails.NewHash()
	if class != "" {
		opts.Set("class", class)
	}
	return d.linkToUser(p, u, opts)
}

// FormatDate は format_date(date)。
func FormatDate(p *Page, t time.Time) string { return formatDate(p, t) }

// FormatTime は format_time(time, include_date)。
func FormatTime(p *Page, t time.Time, includeDate bool) string { return formatTime(p, t, includeDate) }

// ProgressBar は progress_bar(pct, :legend => legend)。
func ProgressBar(pct int, legend string) template.HTML { return progressBarHTML(pct, legend) }

// TimeTag は time_tag(time)。
func (d *Deps) TimeTag(p *Page, t time.Time) template.HTML { return d.timeTag(p, t) }

// L は l(key, args...)。
func (p *Page) L(key string, args ...any) string { return p.l(key, args...) }

// Admin は User.current.admin?。
func (p *Page) Admin() bool { return p.admin() }

// LinkToContextMenu は link_to_context_menu。
func (d *Deps) LinkToContextMenu(p *Page) template.HTML { return d.issuesLinkToContextMenu(p) }
