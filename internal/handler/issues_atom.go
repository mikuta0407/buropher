// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

// Atom フィード（common/feed.atom.builder の issues#index と journals/index.builder の issues#show / journals#index）。
// 出力は Builder::XmlMarkup（:indent => 2）と同じ空白の規則で組み立てる。

import (
	"html/template"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/urlroot"
)

// xmlMarkup は Builder::XmlMarkup（indent 2）の最小限の移植。
type xmlMarkup struct {
	b     strings.Builder
	level int
}

func (x *xmlMarkup) indent() { x.b.WriteString(strings.Repeat("  ", x.level)) }

func xmlAttrs(attrs [][2]string) string {
	var s strings.Builder
	for _, a := range attrs {
		s.WriteString(" " + a[0] + `="` + httpx.XMLEscapeAttr(a[1]) + `"`)
	}
	return s.String()
}

// instruct は xml.instruct!。
func (x *xmlMarkup) instruct() { x.b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n") }

// tag は xml.name(text, attrs)（text が nil なら空要素）。
func (x *xmlMarkup) tag(name string, text *string, attrs ...[2]string) {
	x.indent()
	if text == nil {
		x.b.WriteString("<" + name + xmlAttrs(attrs) + "/>\n")
		return
	}
	x.b.WriteString("<" + name + xmlAttrs(attrs) + ">" + httpx.XMLEscapeText(*text) + "</" + name + ">\n")
}

// block は xml.name(attrs) do ... end。
func (x *xmlMarkup) block(name string, fn func(), attrs ...[2]string) {
	x.indent()
	x.b.WriteString("<" + name + xmlAttrs(attrs) + ">\n")
	x.level++
	fn()
	x.level--
	x.indent()
	x.b.WriteString("</" + name + ">\n")
}

// text は xml.text!(s)。
func (x *xmlMarkup) text(s string) { x.b.WriteString(httpx.XMLEscapeText(s)) }

func sp(s string) *string { return &s }

// xmlschemaLocal は Time.now.xmlschema（サーバのローカル時刻。UTC でも "+00:00" になる）。
func xmlschemaLocal(t time.Time) string { return t.In(time.Local).Format("2006-01-02T15:04:05-07:00") }

func (c *Req) renderAtom(x *xmlMarkup) {
	c.W.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	c.W.WriteHeader(http.StatusOK)
	_, _ = c.W.Write([]byte(x.b.String()))
	c.Halt()
}

// homeURL は home_url。
func homeURL(c *Req) string { return httpx.RequestBaseURL(c.R) + urlroot.Path("/") }

// faviconURL は favicon_url。
func (a *App) faviconURL(c *Req) string {
	return httpx.RequestBaseURL(c.R) + urlroot.Path(a.Helpers.AssetPath("favicon.ico"))
}

// ---------------------------------------------------------------- issues#index（render_feed）

// renderIssuesIndexAtom は format.atom（render_feed(issues, :title => "#{@project || Setting.app_title}: Issues")）。
func (a *App) renderIssuesIndexAtom(c *Req, q *query.Query) {
	limit := a.Settings.Int("feeds_limit")
	rows, err := q.Issues(c.Ctx(), query.ListOptions{Limit: limit})
	if err != nil {
		a.queryFailed(c, err)
		return
	}
	l := a.newIssueLookup(c)
	l.addIssues(rows)
	l.markVisible(rows)
	// @items.sort! {|x, y| y.event_datetime <=> x.event_datetime}
	slices.SortStableFunc(rows, func(x, y *query.IssueRow) int { return y.CreatedAt.Compare(x.CreatedAt) })
	if len(rows) > limit {
		rows = rows[:limit]
	}
	title := a.Settings.String("app_title")
	if c.Project != nil {
		title = c.Project.Name
	}
	title += ": " + c.L("label_issue_plural")
	base := httpx.RequestBaseURL(c.R)
	qp := c.Page().QueryParameters()
	self := base + urlroot.Path(strings.TrimSuffix(c.R.URL.Path, ".atom")+".atom")
	if s := helper.ToQuery(qp); s != "" {
		self += "?" + s
	}
	qp.Delete("format")
	qp.Delete("key")
	alt := base + urlroot.Path(strings.TrimSuffix(c.R.URL.Path, ".atom"))
	if s := helper.ToQuery(qp); s != "" {
		alt += "?" + s
	}
	x := &xmlMarkup{}
	x.instruct()
	x.block("feed", func() {
		x.tag("title", sp(truncateSingleLineRaw(title, 300)))
		x.tag("link", nil, [2]string{"rel", "self"}, [2]string{"href", self})
		x.tag("link", nil, [2]string{"rel", "alternate"}, [2]string{"href", alt})
		x.tag("id", sp(homeURL(c)))
		x.tag("icon", sp(a.faviconURL(c)))
		updated := xmlschemaLocal(a.now())
		if len(rows) > 0 {
			updated = xmlschema(rows[0].CreatedAt)
		}
		x.tag("updated", sp(updated))
		x.block("author", func() { x.tag("name", sp(a.Settings.String("app_title"))) })
		x.block("generator", func() { x.text(helper.AppName) }, [2]string{"uri", helper.AppURL})
		for _, r := range rows {
			x.block("entry", func() {
				u := base + urlroot.Path("/issues/"+strconv.FormatInt(r.ID, 10))
				st := l.status(r.StatusID)
				et := l.tracker(r.TrackerID).Name + " #" + strconv.FormatInt(r.ID, 10) + " (" + st.Name + "): " + r.Subject
				if c.Project == nil || c.Project.ID != r.ProjectID {
					et = l.project(r.ProjectID).Name + " - " + et
				}
				x.tag("title", sp(truncateSingleLineRaw(et, 300)))
				x.tag("link", nil, [2]string{"rel", "alternate"}, [2]string{"href", u})
				x.tag("id", sp(u))
				x.tag("updated", sp(xmlschema(r.CreatedAt)))
				if au := l.principal(r.AuthorID); au != nil {
					x.block("author", func() {
						x.tag("name", sp(l.principalName(au)))
						if mail := l.authorMail(au); mail != "" {
							x.tag("email", sp(mail))
						}
					})
				}
				x.block("content", func() {
					x.text(string(l.renderer().Textilizable(r.Description, redmine.Options{FullURL: true,
						Object: &redmine.Object{Kind: "issue", ID: r.ID, Project: l.project(r.ProjectID)}})))
				}, [2]string{"type", "html"})
			})
		}
	}, [2]string{"xmlns", "http://www.w3.org/2005/Atom"})
	c.renderAtom(x)
}

// authorMail は author.mail（表示しない設定なら ""）。
func (l *issueLookup) authorMail(u *domain.User) string {
	if !u.Kind.IsUser() || u.Mail == "" {
		return ""
	}
	pref, err := repository.GetUserPreference(l.ctx, l.a.DB, u.ID)
	if err != nil {
		return ""
	}
	if pref.HideMail {
		return ""
	}
	return u.Mail
}

// ---------------------------------------------------------------- journals/index.builder

// renderJournalsAtom は journals/index.builder。
func (a *App) renderJournalsAtom(c *Req, l *issueLookup, title *string, selfURL string, journals []*journalView) {
	base := httpx.RequestBaseURL(c.R)
	x := &xmlMarkup{}
	x.instruct()
	x.block("feed", func() {
		x.tag("title", title)
		x.tag("link", nil, [2]string{"rel", "self"}, [2]string{"href", selfURL})
		x.tag("link", nil, [2]string{"rel", "alternate"}, [2]string{"href", homeURL(c)})
		x.tag("id", sp(homeURL(c)))
		x.tag("sprite_icon", sp(a.faviconURL(c)))
		updated := xmlschemaLocal(a.now())
		if len(journals) > 0 {
			updated = xmlschema(journals[0].CreatedAt)
		}
		x.tag("updated", sp(updated))
		x.block("author", func() { x.tag("name", sp(a.Settings.String("app_title"))) })
		for _, j := range journals {
			m := j.issue
			x.block("entry", func() {
				x.tag("title", sp(m.Project.Name+" - "+m.Tracker.Name+" #"+strconv.FormatInt(m.Row.ID, 10)+": "+m.Row.Subject))
				x.tag("link", nil, [2]string{"rel", "alternate"}, [2]string{"href", base + urlroot.Path("/issues/"+strconv.FormatInt(m.Row.ID, 10))})
				x.tag("id", sp(base+urlroot.Path("/issues/"+strconv.FormatInt(m.Row.ID, 10)+"?journal_id="+strconv.FormatInt(j.ID, 10))))
				x.tag("updated", sp(xmlschema(j.CreatedAt)))
				x.block("author", func() {
					x.tag("name", sp(l.principalName(j.User)))
					if j.User != nil {
						if mail := l.authorMail(j.User); mail != "" {
							x.tag("email", sp(mail))
						}
					}
				})
				x.block("content", func() {
					x.text("<ul>")
					for _, s := range l.detailsToStrings(j.VisibleDetails(), m, false, true) {
						x.text("<li>" + string(s) + "</li>")
					}
					x.text("</ul>")
					if j.HasNotes() {
						x.text(string(l.renderer().Textilizable(j.NotesString(), redmine.Options{FullURL: true,
							Object: &redmine.Object{Kind: "journal", ID: j.ID, Project: m.Project, JournalizedID: j.IssueID}})))
					}
				}, [2]string{"type", "html"})
			})
		}
	}, [2]string{"xmlns", "http://www.w3.org/2005/Atom"})
	c.renderAtom(x)
}

// issuesShowAtom は issues#show の format.atom（journals/index を @journals で描画。@title は nil）。
func (a *App) issuesShowAtom(c *Req) {
	l := a.newIssueLookup(c)
	m := l.model(c.currentIssue())
	js := m.visibleJournals()
	if c.Pref().CommentsSorting == "desc" {
		slices.Reverse(js)
	}
	self := httpx.RequestBaseURL(c.R) + urlroot.Path("/issues/"+strconv.FormatInt(m.Row.ID, 10)+".atom")
	if k := c.AtomKey(); k != "" {
		self += "?key=" + k
	}
	if l.err != nil {
		a.internalError(c, "issue atom", l.err)
		return
	}
	a.renderJournalsAtom(c, l, nil, self, js)
}

// detailHTML は details_to_strings の結果（atom 用、完全 URL）。
var _ = template.HTML("")
