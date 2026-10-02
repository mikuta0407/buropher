package handler

import (
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルはニュース一覧・フォーラムの Atom（ApplicationController#render_feed と
// app/views/common/feed.atom.builder）。Builder::XmlMarkup（indent: 2）の出力を直接組み立てる。

// feedItem は acts_as_event のオブジェクト（event_title / event_url / event_datetime / event_author /
// event_description / project）。
type feedItem struct {
	Title       string
	Path        string
	Datetime    time.Time
	Author      *domain.User
	Project     *domain.Project
	Description string
	// Object は textilizable の :object（添付・リンクの解決に使う）。
	Object *redmine.Object
}

func newsTextObject(n *domain.News) *redmine.Object {
	return &redmine.Object{Kind: "news", ID: n.ID, Project: n.Project}
}

func messageTextObject(m *domain.Message, p *domain.Project) *redmine.Object {
	return &redmine.Object{Kind: "message", ID: m.ID, Project: p}
}

func feedXMLText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func feedXMLAttr(s string) string {
	return strings.NewReplacer("\n", "&#10;", "\r", "&#13;", `"`, "&quot;").Replace(feedXMLText(s))
}

var reFeedNewlines = regexp.MustCompile(`[\r\n]+`)

// feedTruncate は truncate_single_line_raw(string, length)。
func feedTruncate(s string, length int) string {
	return reFeedNewlines.ReplaceAllString(rails.StringTruncate(s, length, "", nil), " ")
}

func feedTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

// renderContentFeed は render_feed(items, :title => title)。
func (a *App) renderContentFeed(c *Req, items []feedItem, title string) {
	items = append([]feedItem(nil), items...)
	sort.SliceStable(items, func(i, j int) bool { return items[i].Datetime.After(items[j].Datetime) })
	if n := int(settings.RubyToI(a.Settings.String("feeds_limit"))); n >= 0 && len(items) > n {
		items = items[:n]
	}
	base := httpx.RequestBaseURL(c.R)
	page := c.Page()
	path := strings.TrimSuffix(c.R.URL.Path, ".atom")
	qp := page.QueryParameters()
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<feed xmlns="http://www.w3.org/2005/Atom">` + "\n")
	b.WriteString("  <title>" + feedXMLText(feedTruncate(title, 300)) + "</title>\n")
	b.WriteString(`  <link rel="self" href="` + feedXMLAttr(base+helper.URLWithQuery(path+".atom", qp)) + `"/>` + "\n")
	b.WriteString(`  <link rel="alternate" href="` + feedXMLAttr(base+helper.URLWithQuery(path, qp.Except("format", "key"))) + `"/>` + "\n")
	b.WriteString("  <id>" + feedXMLText(base+"/") + "</id>\n")
	b.WriteString("  <icon>" + feedXMLText(base+a.Helpers.ContentFaviconPath(page)) + "</icon>\n")
	updated := a.now()
	if len(items) > 0 {
		updated = items[0].Datetime
	}
	b.WriteString("  <updated>" + feedTime(updated) + "</updated>\n")
	b.WriteString("  <author>\n    <name>" + feedXMLText(a.Settings.String("app_title")) + "</name>\n  </author>\n")
	b.WriteString(`  <generator uri="` + feedXMLAttr(helper.AppURL) + `">` + "\n" + feedXMLText(helper.AppName) + "  </generator>\n")
	renderer := a.Helpers.WikiRenderer(page)
	for _, e := range items {
		url := base + e.Path
		b.WriteString("  <entry>\n")
		t := e.Title
		if !(c.Project != nil && e.Project != nil && c.Project.ID == e.Project.ID) && !(c.Project == nil && e.Project == nil) {
			pname := ""
			if e.Project != nil {
				pname = e.Project.Name
			}
			t = pname + " - " + e.Title
		}
		b.WriteString("    <title>" + feedXMLText(feedTruncate(t, 300)) + "</title>\n")
		b.WriteString(`    <link rel="alternate" href="` + feedXMLAttr(url) + `"/>` + "\n")
		b.WriteString("    <id>" + feedXMLText(url) + "</id>\n")
		b.WriteString("    <updated>" + feedTime(e.Datetime) + "</updated>\n")
		if u := e.Author; u != nil {
			b.WriteString("    <author>\n      <name>" + feedXMLText(page.UserNameOf(u)) + "</name>\n")
			if u.Mail != "" && u.Logged() {
				pref, err := repository.GetUserPreference(c.Ctx(), a.DB, u.ID)
				if err == nil && !pref.HideMail {
					b.WriteString("      <email>" + feedXMLText(u.Mail) + "</email>\n")
				}
			}
			b.WriteString("    </author>\n")
		}
		content := renderer.Textilizable(e.Description, redmine.Options{Object: e.Object, FullURL: true})
		b.WriteString(`    <content type="html">` + "\n" + feedXMLText(string(content)) + "    </content>\n")
		b.WriteString("  </entry>\n")
	}
	b.WriteString("</feed>\n")
	c.halted = true
	c.W.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	c.W.WriteHeader(http.StatusOK)
	_, _ = c.W.Write([]byte(b.String()))
}
