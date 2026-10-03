// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/activity"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/urlroot"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは ApplicationController#render_feed と app/views/common/feed.atom.builder の移植。
// Builder::XmlMarkup（indent: 2）の出力をそのまま組み立てる（ERB のエスケープとは規則が違うため
// テンプレートは使わない: テキストは & < > のみ、属性値はさらに " と改行をエスケープする）。

// xmlText は Builder の _escape（& < > のみ）。
func xmlText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// xmlAttr は Builder の _escape_attribute。
func xmlAttr(s string) string {
	return strings.NewReplacer("\n", "&#10;", "\r", "&#13;", `"`, "&quot;").Replace(xmlText(s))
}

var reNewlinesFeed = regexp.MustCompile(`[\r\n]+`)

// truncateSingleLineRaw は truncate_single_line_raw(string, length)。
func truncateSingleLineRaw(s string, length int) string {
	return reNewlinesFeed.ReplaceAllString(rails.StringTruncate(s, length, "", nil), " ")
}

// renderFeed は render_feed(items, :title => title)。
func (a *App) renderFeed(c *Req, items []*activity.Event, title string) {
	items = append([]*activity.Event(nil), items...)
	sort.SliceStable(items, func(i, j int) bool { return items[i].Datetime.After(items[j].Datetime) })
	if n := int(settings.RubyToI(a.Settings.String("feeds_limit"))); n >= 0 && len(items) > n {
		items = items[:n]
	}
	if title == "" {
		title = a.Settings.String("app_title")
	}
	base := httpx.RequestBaseURL(c.R)
	page := c.Page()
	path := strings.TrimSuffix(c.R.URL.Path, ".atom")
	qp := page.QueryParameters()
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<feed xmlns="http://www.w3.org/2005/Atom">` + "\n")
	b.WriteString("  <title>" + xmlText(truncateSingleLineRaw(title, 300)) + "</title>\n")
	b.WriteString(`  <link rel="self" href="` + xmlAttr(base+urlroot.Path(helper.URLWithQuery(path+".atom", qp))) + `"/>` + "\n")
	b.WriteString(`  <link rel="alternate" href="` + xmlAttr(base+urlroot.Path(helper.URLWithQuery(path, qp.Except("format", "key")))) + `"/>` + "\n")
	b.WriteString("  <id>" + xmlText(base+urlroot.Path("/")) + "</id>\n")
	b.WriteString("  <icon>" + xmlText(base+urlroot.Path("/"+strings.TrimLeft(a.Helpers.FaviconPath(page), "/"))) + "</icon>\n")
	updated := a.now()
	if len(items) > 0 {
		updated = items[0].Datetime
	}
	b.WriteString("  <updated>" + xmlschema(updated) + "</updated>\n")
	b.WriteString("  <author>\n    <name>" + xmlText(a.Settings.String("app_title")) + "</name>\n  </author>\n")
	b.WriteString(`  <generator uri="` + xmlAttr(helper.AppURL) + `">` + "\n" + xmlText(helper.AppName) + "  </generator>\n")
	for _, e := range items {
		url := base + urlroot.Path(e.URL)
		b.WriteString("  <entry>\n")
		t := e.Title
		if !(c.Project != nil && e.Project != nil && c.Project.ID == e.Project.ID) && !(c.Project == nil && e.Project == nil) {
			pname := ""
			if e.Project != nil {
				pname = e.Project.Name
			}
			t = pname + " - " + e.Title
		}
		b.WriteString("    <title>" + xmlText(truncateSingleLineRaw(t, 300)) + "</title>\n")
		b.WriteString(`    <link rel="alternate" href="` + xmlAttr(url) + `"/>` + "\n")
		b.WriteString("    <id>" + xmlText(url) + "</id>\n")
		b.WriteString("    <updated>" + xmlschema(e.Datetime) + "</updated>\n")
		switch au := e.Author.(type) {
		case nil:
		case string:
			b.WriteString("    <author>\n      <name>" + xmlText(au) + "</name>\n    </author>\n")
		default:
			if u := e.AuthorUser(); u != nil {
				b.WriteString("    <author>\n      <name>" + xmlText(page.UserName(u)) + "</name>\n")
				if u.Mail != "" && u.Logged() {
					pref, err := repository.GetUserPreference(c.Ctx(), a.DB, u.ID)
					if err == nil && !pref.HideMail {
						b.WriteString("      <email>" + xmlText(u.Mail) + "</email>\n")
					}
				}
				b.WriteString("    </author>\n")
			}
		}
		b.WriteString(`    <content type="html">` + "\n" + xmlText(string(a.Helpers.TextilizeEvent(page, e))) + "    </content>\n")
		b.WriteString("  </entry>\n")
	}
	b.WriteString("</feed>\n")
	c.halted = true
	c.W.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	c.W.WriteHeader(http.StatusOK)
	_, _ = c.W.Write([]byte(b.String()))
}

// xmlschema は Time#xmlschema（UTC は Z）。
func xmlschema(t time.Time) string {
	t = t.UTC()
	return t.Format("2006-01-02T15:04:05Z")
}
