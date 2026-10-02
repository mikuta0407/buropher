package handler

import (
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/urlroot"
)

// このファイルは projects#index の Atom（render_feed + common/feed.atom.builder）。
//
// TODO(dedupe): 活動・ニュース等の Atom を移植する際は render_feed を共通化する。

var atomTextEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
var atomAttrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

// renderProjectsAtom は format.atom（最新のプロジェクトを created_on の降順で Setting.feeds_limit 件）。
func (a *App) renderProjectsAtom(c *Req, q *query.Query) {
	ctx := c.Ctx()
	ps, err := q.Projects(ctx, query.ListOptions{})
	if err != nil {
		a.queryStatementError(c, err)
		return
	}
	// project_scope(:order => {:created_on => :desc})（同時刻はツリー順）
	slices.SortStableFunc(ps, func(x, y *domain.Project) int { return y.CreatedAt.Compare(x.CreatedAt) })
	if n := a.Settings.Int("feeds_limit"); n >= 0 && len(ps) > n {
		ps = ps[:n]
	}
	title := a.Settings.String("app_title") + ": " + c.L("label_project_latest")
	base := httpx.RequestBaseURL(c.R)
	selfQ := c.R.URL.Query()
	altQ := url.Values{}
	for k, v := range selfQ {
		if k != "key" && k != "format" {
			altQ[k] = v
		}
	}
	self := base + urlroot.Path("/projects.atom")
	if s := railsToQuery(selfQ); s != "" {
		self += "?" + s
	}
	alt := base + urlroot.Path("/projects")
	if s := railsToQuery(altQ); s != "" {
		alt += "?" + s
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<feed xmlns="http://www.w3.org/2005/Atom">` + "\n")
	b.WriteString("  <title>" + atomTextEscaper.Replace(truncateSingleLine(title, 300)) + "</title>\n")
	b.WriteString(`  <link rel="self" href="` + atomAttrEscaper.Replace(self) + `"/>` + "\n")
	b.WriteString(`  <link rel="alternate" href="` + atomAttrEscaper.Replace(alt) + `"/>` + "\n")
	b.WriteString("  <id>" + atomTextEscaper.Replace(base+urlroot.Path("/")) + "</id>\n")
	icon := "/favicon.ico"
	if a.Assets != nil {
		icon = a.Assets.AssetPath("favicon.ico")
	}
	b.WriteString("  <icon>" + atomTextEscaper.Replace(base+urlroot.Path(icon)) + "</icon>\n")
	updated := a.now()
	if len(ps) > 0 {
		updated = ps[0].CreatedAt
	}
	b.WriteString("  <updated>" + apiTime(updated) + "</updated>\n")
	b.WriteString("  <author>\n    <name>" + atomTextEscaper.Replace(a.Settings.String("app_title")) + "</name>\n  </author>\n")
	b.WriteString(`  <generator uri="` + helper.AppURL + `">` + "\n" + helper.AppName + "  </generator>\n")
	for _, p := range ps {
		u := base + urlroot.Path("/projects/"+p.Identifier)
		b.WriteString("  <entry>\n")
		b.WriteString("    <title>" + atomTextEscaper.Replace(truncateSingleLine(p.Name+" - "+c.L("label_project")+": "+p.Name, 300)) + "</title>\n")
		b.WriteString(`    <link rel="alternate" href="` + atomAttrEscaper.Replace(u) + `"/>` + "\n")
		b.WriteString("    <id>" + atomTextEscaper.Replace(u) + "</id>\n")
		b.WriteString("    <updated>" + apiTime(p.CreatedAt) + "</updated>\n")
		b.WriteString(`    <content type="html">` + "\n" + atomTextEscaper.Replace(string(helper.Textilizable(p.Description))) + "    </content>\n")
		b.WriteString("  </entry>\n")
	}
	b.WriteString("</feed>\n")
	c.W.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	c.W.WriteHeader(http.StatusOK)
	_, _ = c.W.Write([]byte(b.String()))
	c.Halt()
}

// truncateSingleLine は truncate_single_line_raw(string, length)（改行を空白にして切り詰める）。
func truncateSingleLine(s string, length int) string {
	s = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(s)
	rs := []rune(s)
	if len(rs) > length {
		return string(rs[:length-3]) + "..."
	}
	return s
}
