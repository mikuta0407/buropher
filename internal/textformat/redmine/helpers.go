// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

package redmine

// ApplicationHelper のリンク系ヘルパー（link_to_issue, link_to_attachment, link_to_user, link_to_project,
// link_to_message, link_to_version, thumbnail_tag）と URL の組み立て（Rails の url_for 相当）。

import (
	"fmt"
	"html/template"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// ---- URL エスケープ（ActionDispatch::Journey::Router::Utils） ----

const unreservedChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~"
const subDelims = "!$&'()*+,;="

func escapeWith(s, keep string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if strings.IndexByte(keep, c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// escapeSegment は Journey::Router::Utils.escape_segment（パスの 1 セグメント）。
func escapeSegment(s string) string { return escapeWith(s, unreservedChars+subDelims+":@") }

// escapePath は escape_path（グロブ *path。/ を残す）。
func escapePath(s string) string { return escapeWith(s, unreservedChars+subDelims+":@/") }

// escapeFragment は escape_fragment（#anchor）。
func escapeFragment(s string) string { return escapeWith(s, unreservedChars+subDelims+":@/?") }

// cgiEscape は CGI.escape（to_query の値）。
func cgiEscape(s string) string {
	return strings.ReplaceAll(escapeWith(s, unreservedChars+" "), " ", "+")
}

// cgiUnescape は CGI.unescape（+ を空白に、%XX を復号。不正な % はそのまま）。
func cgiUnescape(s string) string {
	s = strings.ReplaceAll(s, "+", " ")
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			v, _ := strconv.ParseUint(s[i+1:i+3], 16, 8)
			b.WriteByte(byte(v))
			i += 2
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isHex(c byte) bool {
	return ('0' <= c && c <= '9') || ('a' <= c && c <= 'f') || ('A' <= c && c <= 'F')
}

// cgiUnescapeHTML は CGI.unescapeHTML（&amp; &quot; &#39; &lt; &gt; と数値参照のみ）。
func cgiUnescapeHTML(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	return reUnescapeHTML.ReplaceAllStringFunc(s, func(m string) string {
		body := m[1 : len(m)-1]
		switch body {
		case "apos":
			return "'"
		case "amp":
			return "&"
		case "quot":
			return `"`
		case "gt":
			return ">"
		case "lt":
			return "<"
		}
		var n uint64
		var err error
		if strings.HasPrefix(body, "#x") || strings.HasPrefix(body, "#X") {
			n, err = strconv.ParseUint(body[2:], 16, 32)
		} else {
			n, err = strconv.ParseUint(body[1:], 10, 32)
		}
		if err != nil || n > 0x10FFFF {
			return m
		}
		return string(rune(n))
	})
}

var reUnescapeHTML = regexp.MustCompile(`&(apos|amp|quot|gt|lt|#[0-9]+|#[xX][0-9A-Fa-f]+);`)

// projectParam は Project#to_param（数字だけの識別子は id）。
func projectParam(p *domain.Project) string {
	if p == nil {
		return ""
	}
	if regexp.MustCompile(`^\d*$`).MatchString(p.Identifier) {
		return strconv.FormatInt(p.ID, 10)
	}
	return p.Identifier
}

// url は only_path に応じて BaseURL を前置する。
func (r *Renderer) url(path string) string {
	if r.onlyPath {
		return path
	}
	return r.BaseURL + path
}

func withAnchor(u, anchor string) string {
	if anchor == "" {
		return u
	}
	return u + "#" + escapeFragment(anchor)
}

// ---- Wiki ----

var reTitleizeSpace = regexp.MustCompile(`[ \t\n\v\f\r]+`)

// Titleize は Wiki.titleize。
func Titleize(title string) string {
	title = reTitleizeSpace.ReplaceAllString(title, "_")
	title = strings.Map(func(r rune) rune {
		if strings.ContainsRune(",./?;|:", r) {
			return -1
		}
		return r
	}, title)
	if title == "" {
		return title
	}
	rs := []rune(title)
	return strings.ToUpper(string(rs[0])) + string(rs[1:])
}

// PrettyTitle は WikiPage.pretty_title。
func PrettyTitle(s string) string { return strings.ReplaceAll(s, "_", " ") }

// WikiPagePath は project_wiki_page_path(project, title)。
func WikiPagePath(p *domain.Project, title string) string {
	return "/projects/" + escapeSegment(projectParam(p)) + "/wiki/" + escapeSegment(title)
}

// ---- アイコン ----

// spriteIcon は IconsHelper#sprite_icon(name, label, rtl:)。
func (r *Renderer) spriteIcon(name, label string, rtl bool) template.HTML {
	css := "s18 icon-svg"
	if rtl {
		css += " icon-rtl"
	}
	svg := `<svg class="` + css + `" aria-hidden="true"><use href="` + h(r.iconsPath()+"#icon--"+name) + `"></use></svg>`
	return template.HTML(svg + `<span class="icon-label">` + h(label) + `</span>`)
}

// ---- チケット ----

// IssueURL は issue_url(issue, anchor:)。
func (r *Renderer) issueURL(id int64, anchor string) string {
	return withAnchor(r.url("/issues/"+strconv.FormatInt(id, 10)), anchor)
}

func (r *Renderer) userGroupIDs() []int64 {
	if !r.groupIDsLoaded {
		r.groupIDsLoaded = true
		if r.Store != nil && r.currentUser().Logged() {
			ids, err := r.Store.UserGroupIDs()
			r.logErr("user groups", err)
			r.groupIDs = ids
		}
	}
	return r.groupIDs
}

// IssueCSSClasses は Issue#css_classes(User.current)。
func (r *Renderer) IssueCSSClasses(is *Issue) string {
	s := "issue tracker-" + strconv.FormatInt(is.TrackerID, 10) + " status-" + strconv.FormatInt(is.StatusID, 10) +
		" priority-" + strconv.FormatInt(is.PriorityID, 10) + " priority-" + is.PriorityPosName.String
	today := r.today()
	if is.StatusClosed {
		s += " closed"
	}
	if is.DueDate.Valid && is.DueDate.Date.Time.Before(today) && !is.StatusClosed {
		s += " overdue"
	}
	if is.ParentID.Valid {
		s += " child"
	}
	if is.HasChildren {
		s += " parent"
	}
	if is.IsPrivate {
		s += " private"
	}
	if is.StartDate.Valid && is.DueDate.Valid {
		start, due := is.StartDate.Date.Time, is.DueDate.Date.Time
		days := int(due.Sub(start).Hours()/24) + 1
		// ((due - start + 1) * done_ratio / 100).floor（Rational の切り捨て）
		n := days * is.DoneRatio
		off := n / 100
		if n < 0 && n%100 != 0 {
			off--
		}
		done := start.AddDate(0, 0, off)
		if !done.After(today) {
			s += " behind-schedule"
		}
	}
	u := r.currentUser()
	if u.Logged() {
		if is.AuthorID == u.ID {
			s += " created-by-me"
		}
		if is.AssignedToID.Valid && is.AssignedToID.Int64 == u.ID {
			s += " assigned-to-me"
		}
		if is.AssignedToID.Valid {
			for _, g := range r.userGroupIDs() {
				if g == is.AssignedToID.Int64 {
					s += " assigned-to-my-group"
					break
				}
			}
		}
	}
	return s
}

// LinkToIssueOptions は link_to_issue のオプション。
type LinkToIssueOptions struct {
	NoTracker bool // :tracker => false
	NoSubject bool // :subject => false
	Project   bool // :project => true
	Truncate  int  // :truncate
	// FullURL は :only_path => false（既定は相対パス。textilizable の only_path には従わない）。
	FullURL bool
}

// LinkToIssue は link_to_issue(issue, options)。
func (r *Renderer) LinkToIssue(is *Issue, o LinkToIssueOptions) template.HTML {
	text := is.TrackerName + " #" + strconv.FormatInt(is.ID, 10)
	if o.NoTracker {
		text = "#" + strconv.FormatInt(is.ID, 10)
	}
	var title any
	subject := ""
	hasSubject := false
	if o.NoSubject {
		title = rails.StringTruncate(is.Subject, 60, "", nil)
	} else {
		subject = is.Subject
		hasSubject = true
		if o.Truncate > 0 {
			subject = rails.StringTruncate(subject, o.Truncate, "", nil)
		}
	}
	u := "/issues/" + strconv.FormatInt(is.ID, 10)
	if o.FullURL {
		u = r.BaseURL + u
	}
	s := string(rails.LinkTo(text, u, rails.NewHash("class", r.IssueCSSClasses(is), "title", title)))
	if hasSubject {
		s += h(": " + subject)
	}
	if o.Project {
		s = h(is.ProjectName+" - ") + s
	}
	return template.HTML(s)
}

// ---- 添付 ----

// AttachmentURL は attachment_url(attachment)。
func (r *Renderer) attachmentURL(a *Attachment) string {
	return r.url("/attachments/" + strconv.FormatInt(a.ID, 10))
}

// downloadNamedAttachmentURL は download_named_attachment_url(attachment, filename)。
func (r *Renderer) downloadNamedAttachmentURL(a *Attachment) string {
	return r.url("/attachments/download/" + strconv.FormatInt(a.ID, 10) + "/" + escapeSegment(a.Filename))
}

// linkToAttachment は link_to_attachment(attachment, class:)（:download なし）。
func (r *Renderer) linkToAttachment(a *Attachment, class string) template.HTML {
	return rails.LinkTo(a.Filename, r.attachmentURL(a), rails.NewHash("class", class))
}

// LinkToAttachmentOptions は link_to_attachment のオプション。
type LinkToAttachmentOptions struct {
	Text     string      // :text（空ならファイル名）
	Icon     string      // :icon（sprite_icon(icon, text) をラベルにする）
	Download bool        // :download
	FullURL  bool        // :only_path => false
	HTML     *rails.Hash // それ以外（class, title 等）
}

// LinkToAttachment は link_to_attachment(attachment, options)。
func (r *Renderer) LinkToAttachment(a *Attachment, o LinkToAttachmentOptions) template.HTML {
	text := o.Text
	if text == "" {
		text = a.Filename
	}
	u := "/attachments/" + strconv.FormatInt(a.ID, 10)
	if o.Download {
		u = "/attachments/download/" + strconv.FormatInt(a.ID, 10) + "/" + escapeSegment(a.Filename)
	}
	if o.FullURL {
		u = r.BaseURL + u
	}
	var label any = text
	if o.Icon != "" {
		label = r.spriteIcon(o.Icon, text, false)
	}
	return rails.LinkTo(label, u, o.HTML)
}

// latestAttach は Attachment.latest_attach（created_on, id の新しい順で大文字小文字を無視して一致するもの）。
func latestAttach(atts []*Attachment, filename string) *Attachment {
	var best *Attachment
	for _, a := range atts {
		if !strings.EqualFold(a.Filename, filename) {
			continue
		}
		if best == nil || a.CreatedAt.Time.After(best.CreatedAt.Time) ||
			(a.CreatedAt.Time.Equal(best.CreatedAt.Time) && a.ID > best.ID) {
			best = a
		}
	}
	return best
}

// ThumbnailTag は thumbnail_tag(attachment)（size は Setting.thumbnails_size）。
func (r *Renderer) ThumbnailTag(a *Attachment, size int) template.HTML {
	tp := "/attachments/thumbnail/" + strconv.FormatInt(a.ID, 10) + "/" + strconv.Itoa(size*2)
	img := `<img srcset="` + h(tp+" 2x") + `" style="` + h("max-width: "+strconv.Itoa(size)+"px; max-height: "+strconv.Itoa(size)+"px;") +
		`" alt="` + h(a.Filename) + `" loading="lazy" src="` + h(tp) + `" />`
	link := rails.LinkTo(template.HTML(img), "/attachments/"+strconv.FormatInt(a.ID, 10), nil)
	return template.HTML(`<div class="thumbnail" title="` + h(a.Filename) + `">` + string(link) + `</div>`)
}

// ---- ユーザー・プロジェクト・その他 ----

// userName は User#name(format)。
func (r *Renderer) userName(u *domain.User) string {
	if u.Anonymous() {
		return r.l("label_user_anonymous")
	}
	f := r.UserFormat
	if f == "" || !domain.ValidUserFormat(f) {
		f = "firstname_lastname"
	}
	return u.Name(f)
}

// LinkToUser は link_to_user(user, class:, mention:)。
func (r *Renderer) LinkToUser(u *domain.User, class string, mention bool) template.HTML {
	name := r.userName(u)
	if mention {
		name = "@" + name
	}
	cu := r.currentUser()
	if !(u.Active() || (cu.IsAdmin() && u.Logged())) {
		return template.HTML(h(name))
	}
	css := u.CSSClasses()
	if class != "" {
		css += " " + class
	}
	return rails.LinkTo(name, r.url("/users/"+strconv.FormatInt(u.ID, 10)), rails.NewHash("class", css))
}

// LinkToProject は link_to_project(project, {}, html_options)。
func (r *Renderer) LinkToProject(p *domain.Project, htmlOpts *rails.Hash) template.HTML {
	if p.Archived() {
		return template.HTML(h(p.Name))
	}
	return rails.LinkTo(p.Name, r.url("/projects/"+escapeSegment(projectParam(p))), htmlOpts)
}

// linkToMessage は link_to_message(message, {}, html_options)。
func (r *Renderer) linkToMessage(m *Message, htmlOpts *rails.Hash) template.HTML {
	topic := m.ID
	q := ""
	anchor := ""
	if m.ParentID.Valid {
		topic = m.ParentID.Int64
		q = "?r=" + strconv.FormatInt(m.ID, 10)
		anchor = "message-" + strconv.FormatInt(m.ID, 10)
	}
	u := withAnchor(r.url("/boards/"+strconv.FormatInt(m.BoardID, 10)+"/topics/"+strconv.FormatInt(topic, 10)+q), anchor)
	return rails.LinkTo(rails.StringTruncate(m.Subject, 60, "", nil), u, htmlOpts)
}

// truncateSingleLineRaw は truncate_single_line_raw。
func truncateSingleLineRaw(s string, n int) string {
	return reNewlines.ReplaceAllString(rails.StringTruncate(s, n, "", nil), " ")
}

var reNewlines = regexp.MustCompile(`[\r\n]+`)

// toPathParam は to_path_param。
func toPathParam(path string) string {
	var parts []string
	for _, p := range regexp.MustCompile(`[/\\]`).Split(path, -1) {
		if !blank(p) {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "/")
}

// changesetIdentifier は Changeset#identifier（git / mercurial は scmid）。
func changesetIdentifier(repo *Repository, cs *Changeset) string {
	if repo.SCM == "git" || repo.SCM == "mercurial" {
		return cs.Scmid.String
	}
	return cs.Revision
}

// identifierParam は Repository#identifier_param。
func identifierParam(repo *Repository) string {
	if repo.Identifier.Valid && !blank(repo.Identifier.String) {
		return repo.Identifier.String
	}
	return strconv.FormatInt(repo.ID, 10)
}

var reRevConstraint = regexp.MustCompile(`\A[a-z0-9\.\-_]+\z`)

// repositoryEntryURL は url_for(controller: repositories, action: entry|raw, id:, repository_id:, path:, rev:, anchor:)。
func (r *Renderer) repositoryEntryURL(p *domain.Project, repo *Repository, action, path, rev, anchor string) string {
	base := "/projects/" + escapeSegment(projectParam(p)) + "/repository/" + escapeSegment(identifierParam(repo))
	var u string
	query := ""
	if rev != "" && reRevConstraint.MatchString(rev) {
		u = base + "/revisions/" + escapeSegment(rev) + "/" + action
	} else {
		u = base + "/" + action
		if rev != "" {
			query = "?rev=" + cgiEscape(rev)
		}
	}
	if path != "" {
		u += "/" + escapePath(path)
	}
	return withAnchor(r.url(u+query), anchor)
}

// timeAgoInWords は time_ago_in_words。
func (r *Renderer) timeAgoInWords(t time.Time) string {
	if r.Loc == nil {
		r.l("")
	}
	return r.Loc.Bundle.DistanceOfTimeInWords(r.Loc.Lang, t, r.now(), false)
}
