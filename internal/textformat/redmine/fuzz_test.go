// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package redmine

import (
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/secoracle"
)

var textilizableSeeds = []string{
	"#1 r1 commit:abc document:\"Test document\" version:1.0 attachment:error281.txt",
	"[[CookBook documentation|doc]] [[ecookbook:Another page#anchor]] {{toc}} {{child_pages(depth=2)}}",
	"{{include(Another page)}} {{thumbnail(image.png, size=300)}} {{collapse(x)\nbody\n}}",
	"h1. Title\n\n* item\n\n<pre><code class=\"ruby\">x</code></pre> !image.png! \"link\":http://x",
	"# head\n\n```ruby\nx\n```\n[a](#1) ##123 source:\"repo|a/b@1#L2\" user:jsmith @admin forum#1 message#1 news#1 project:ecookbook",
	"{{issue(1, project=true)}} {{recent_pages(time=true, days=0)}} {{macro_list}} {{hello_world(a,b)}}",
	"\"#1\":http://x/#2 [[#3]] !{{toc}}! {{collapse(\"><img src=x onerror=alert(1)>, x)\nr1\n}} h2. #1 \"x\":javascript:alert(1)",
	"[#1](javascript:alert(1)) <a href=\"#1\" title=\"r1 [[Wiki]]\">x</a> ![r1](#2) [[Foo|<script>]] {{child_pages(Foo\" onclick=\"x)}}",
	"![x](http://a/i.png \"{{include(Another page)}}\") !http://a/i.png({{include(Another page)}})! <span title=\"{{collapse(a)}}\">y</span>",
}

// FuzzTextilizable は textilizable（Redmine のリンク・マクロ・書式）が任意の入力で panic しないことを確かめる。
//
//	go test -run '^$' -fuzz FuzzTextilizable ./internal/textformat/redmine
func FuzzTextilizable(f *testing.F) {
	for _, s := range textilizableSeeds {
		f.Add(s, false)
		f.Add(s, true)
	}
	e := newTestEnv(f)
	f.Fuzz(func(t *testing.T, text string, markdown bool) {
		formatting := "textile"
		if markdown {
			formatting = "common_mark"
		}
		r, st := e.renderer(t, "admin", "ecookbook", formatting)
		var o Options
		if obj, err := st.LoadObject("issue", 1); err == nil {
			o.Object = obj
		}
		_ = r.Textilizable(text, o)
	})
}

// hostileName は参照先のオブジェクト名に入れる値。HTML の特殊文字、属性からの脱出、
// Redmine リンク・マクロ・自動リンクとして再解釈され得る文字列を含む。
const hostileName = `x"'<>&<script>alert(1)</script><img src=x onerror=alert(1)>"onmouseover="alert(1)` +
	` #1 r1 [[Wiki]] {{toc}} commit:abc http://e.x/"onclick="alert(1) javascript:alert(1) @admin &amp;lt;`

// hostileStore は DBStore の結果のうち、表示に使われる名前（件名・題名・ファイル名・利用者名など）を
// hostileName に差し替える偽の参照解決。
type hostileStore struct{ *DBStore }

func (s hostileStore) ProjectByIdentifier(id string) (*domain.Project, error) {
	return hostileProject(s.DBStore.ProjectByIdentifier(id))
}
func (s hostileStore) ProjectByName(name string) (*domain.Project, error) {
	return hostileProject(s.DBStore.ProjectByName(name))
}
func (s hostileStore) ProjectByID(id int64) (*domain.Project, error) {
	return hostileProject(s.DBStore.ProjectByID(id))
}
func (s hostileStore) VisibleProjectByIdentifier(id string) (*domain.Project, error) {
	return hostileProject(s.DBStore.VisibleProjectByIdentifier(id))
}
func (s hostileStore) VisibleProjectByID(id int64) (*domain.Project, error) {
	return hostileProject(s.DBStore.VisibleProjectByID(id))
}
func (s hostileStore) VisibleProjectByIdentifierOrLowerName(v string) (*domain.Project, error) {
	return hostileProject(s.DBStore.VisibleProjectByIdentifierOrLowerName(v))
}

func hostileProject(p *domain.Project, err error) (*domain.Project, error) {
	if p == nil {
		return p, err
	}
	c := *p
	c.Name = hostileName
	return &c, err
}

func (s hostileStore) FindWikiPage(w *Wiki, title string) (*WikiPage, error) {
	p, err := s.DBStore.FindWikiPage(w, title)
	if p == nil {
		return p, err
	}
	c := *p
	c.Title = hostileName
	return &c, err
}

func (s hostileStore) WikiPageChildren(id int64) ([]*WikiPage, error) {
	ps, err := s.DBStore.WikiPageChildren(id)
	return hostilePages(ps), err
}

func (s hostileStore) RecentWikiPages(ids []int64, since time.Time, limit int) ([]*WikiPage, error) {
	ps, err := s.DBStore.RecentWikiPages(ids, since, limit)
	return hostilePages(ps), err
}

func hostilePages(ps []*WikiPage) []*WikiPage {
	out := make([]*WikiPage, len(ps))
	for i, p := range ps {
		c := *p
		c.Title = hostileName
		out[i] = &c
	}
	return out
}

func (s hostileStore) VisibleIssue(id int64) (*Issue, error) {
	i, err := s.DBStore.VisibleIssue(id)
	if i == nil {
		return i, err
	}
	c := *i
	c.Subject, c.StatusName, c.TrackerName, c.ProjectName = hostileName, hostileName, hostileName, hostileName
	return &c, err
}

func (s hostileStore) Repositories(id int64) ([]*Repository, error) {
	rs, err := s.DBStore.Repositories(id)
	out := make([]*Repository, len(rs))
	for i, r := range rs {
		c := *r
		if c.Identifier.Valid && c.Identifier.String != "" {
			c.Identifier = sql.NullString{String: hostileName, Valid: true}
		}
		out[i] = &c
	}
	return out, err
}

func hostileChangeset(c *Changeset, err error) (*Changeset, error) {
	if c == nil {
		return c, err
	}
	x := *c
	x.Comments = sql.NullString{String: hostileName, Valid: true}
	return &x, err
}

func (s hostileStore) VisibleChangeset(repo int64, rev string) (*Changeset, error) {
	return hostileChangeset(s.DBStore.VisibleChangeset(repo, rev))
}
func (s hostileStore) VisibleChangesetByScmidPrefix(repo int64, prefix string) (*Changeset, error) {
	return hostileChangeset(s.DBStore.VisibleChangesetByScmidPrefix(repo, prefix))
}

func hostileNamed(n *Named, err error) (*Named, error) {
	if n == nil {
		return n, err
	}
	c := *n
	c.Name = hostileName
	return &c, err
}

func (s hostileStore) VisibleNamed(kind string, id int64) (*Named, error) {
	return hostileNamed(s.DBStore.VisibleNamed(kind, id))
}
func (s hostileStore) VisibleNamedByName(kind string, pid int64, name string) (*Named, error) {
	return hostileNamed(s.DBStore.VisibleNamedByName(kind, pid, name))
}

func (s hostileStore) VisibleMessage(id int64) (*Message, error) {
	m, err := s.DBStore.VisibleMessage(id)
	if m == nil {
		return m, err
	}
	c := *m
	c.Subject = hostileName
	return &c, err
}

func hostileUser(u *domain.User, err error) (*domain.User, error) {
	if u == nil {
		return u, err
	}
	c := *u
	c.Firstname, c.Lastname = hostileName, hostileName
	return &c, err
}

func (s hostileStore) VisibleUser(id int64) (*domain.User, error) {
	return hostileUser(s.DBStore.VisibleUser(id))
}
func (s hostileStore) VisibleUserByLogin(login string) (*domain.User, error) {
	return hostileUser(s.DBStore.VisibleUserByLogin(login))
}

func (s hostileStore) Attachments(kind string, id int64) ([]*Attachment, error) {
	as, err := s.DBStore.Attachments(kind, id)
	out := make([]*Attachment, len(as))
	for i, a := range as {
		c := *a
		c.Description = sql.NullString{String: hostileName, Valid: true}
		out[i] = &c
	}
	return out, err
}

// collapseOnclick は collapse マクロが生成する onclick（id は乱数の 16 進）。
var collapseOnclick = regexp.MustCompile(`\A\$\('#collapse-[0-9a-f]+-show, #collapse-[0-9a-f]+-hide'\)\.toggle\(\); \$\('#collapse-[0-9a-f]+'\)\.fadeToggle\(150\);; return false;\z`)

// textilizablePolicy は textilizable の出力に許す内容。
var textilizablePolicy = secoracle.HTMLPolicy{SVG: true, AllowOnclick: collapseOnclick, BareElement: secoracle.TextileBareElement}

// FuzzTextilizableXSS は textilizable の出力（参照先の名前が全て悪意のある文字列）をブラウザと同じ規則で解析し、
// 許可外の要素・イベントハンドラ・危険な URL・CSS が無いこと、innerHTML の往復で変化しないことを確かめる。
//
//	go test -run '^$' -fuzz FuzzTextilizableXSS ./internal/textformat/redmine
func FuzzTextilizableXSS(f *testing.F) {
	for _, s := range textilizableSeeds {
		f.Add(s, false)
		f.Add(s, true)
	}
	e := newTestEnv(f)
	f.Fuzz(func(t *testing.T, text string, markdown bool) {
		formatting := "textile"
		if markdown {
			formatting = "common_mark"
		}
		r, st := e.renderer(t, "admin", "ecookbook", formatting)
		r.Store = hostileStore{st}
		var o Options
		if obj, err := st.LoadObject("issue", 1); err == nil {
			o.Object = obj
		}
		var out string
		secoracle.Bounded(t, len(text), 0, func() { out = string(r.Textilizable(text, o)) })
		if err := secoracle.CheckHTML(out, textilizablePolicy); err != nil {
			t.Fatalf("input %q (markdown=%v)\noutput %q\n%v", text, markdown, out, err)
		}
	})
}
