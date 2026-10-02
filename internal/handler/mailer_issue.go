package handler

// Mailer#issue_add / issue_edit / reminder と IssuesHelper#email_issue_attributes。

import (
	"html"
	"html/template"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/mail"
	"github.com/mikuta0407/buropher/internal/query"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// mailAttachment はメール本文の添付 1 件。
type mailAttachment struct {
	Filename string
	Size     string
	Link     template.HTML
}

// ljust は String#ljust(width, pad)（文字数で数える）。
func ljust(s string, width int, pad string) string {
	n := utf8.RuneCountInString(s)
	if n >= width {
		return s
	}
	return s + strings.Repeat(pad, width-n)
}

// mailAttachments は添付の一覧（link_to_attachment(download: true, only_path: false) と number_to_human_size）。
func (m *mailer) mailAttachments(atts []*repository.MailAttachment) []mailAttachment {
	r := m.a.Helpers.WikiRenderer(m.c.Page())
	var out []mailAttachment
	for _, a := range atts {
		ra := &redmine.Attachment{ID: a.ID, Filename: a.Filename, Filesize: a.Filesize}
		out = append(out, mailAttachment{
			Filename: a.Filename,
			Size:     m.c.Loc.NumberToHumanSize(a.Filesize),
			Link:     r.LinkToAttachment(ra, redmine.LinkToAttachmentOptions{Download: true, FullURL: true, HTML: rails.NewHash()}),
		})
	}
	return out
}

// issueContext はチケットのメールで共通の状態。
type issueContext struct {
	l   *issueLookup
	row *query.IssueRow
	im  *issueModel
	url string
}

func (m *mailer) loadIssue(id int64) *issueContext {
	l := m.a.newIssueLookup(m.c)
	row := l.issue(id)
	if row == nil {
		return nil
	}
	im := l.model(row)
	if im.I == nil {
		return nil
	}
	return &issueContext{l: l, row: row, im: im}
}

// issueHeaders は issue_add / issue_edit の redmine_headers。
func (m *mailer) issueHeaders(ic *issueContext) {
	r := ic.row
	author := ic.l.principal(r.AuthorID)
	var assignee any
	if r.AssignedToID != nil {
		if u := ic.l.principal(*r.AssignedToID); u != nil {
			if u.Kind.IsGroup() {
				assignee = "Group (" + u.Principal.Name + ")"
			} else {
				assignee = u.Login
			}
		}
	}
	login := ""
	if author != nil {
		login = author.Login
	}
	m.redmineHeaders("Project", ic.im.Project.Identifier, "Issue-Tracker", ic.im.Tracker.Name, "Issue-Id", r.ID,
		"Issue-Author", login, "Issue-Assignee", assignee)
	if ic.im.Priority != nil {
		m.redmineHeaders("Issue-Priority", ic.im.Priority.Name)
	}
}

// emailIssueAttributes は email_issue_attributes(issue, user, html)。
func (m *mailer) emailIssueAttributes(ic *issueContext, htmlMode bool) []string {
	l, r, im := ic.l, ic.row, ic.im
	var items []string
	add := func(label, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		if htmlMode {
			items = append(items, "<strong>"+html.EscapeString(label+": ")+"</strong>"+html.EscapeString(value))
		} else {
			items = append(items, label+": "+value)
		}
	}
	disabled := func(attr string) bool {
		for _, f := range im.Tracker.DisabledCoreFields {
			if f == attr || f == attr+"_id" {
				return true
			}
		}
		return false
	}
	date := func(t interface{ Format(string) string }) string { return t.Format("2006-01-02") }
	for _, attr := range []string{"author", "status", "priority", "assigned_to", "category", "fixed_version", "start_date", "due_date", "parent_issue"} {
		if disabled(attr) {
			continue
		}
		var v string
		switch attr {
		case "author":
			v = m.principalName(l.principal(r.AuthorID))
		case "status":
			if im.Status != nil {
				v = im.Status.Name
			}
		case "priority":
			if im.Priority != nil {
				v = im.Priority.Name
			}
		case "assigned_to":
			if r.AssignedToID != nil {
				v = m.principalName(l.principal(*r.AssignedToID))
			}
		case "category":
			if c := l.category(r.CategoryID); c != nil {
				v = c.Name
			}
		case "fixed_version":
			if ver := l.version(r.FixedVersionID); ver != nil {
				v = ver.Name
			}
		case "start_date":
			if r.StartDate != nil {
				v = date(*r.StartDate)
			}
		case "due_date":
			if r.DueDate != nil {
				v = date(*r.DueDate)
			}
		case "parent_issue":
			if r.ParentID != nil {
				if p := l.issue(*r.ParentID); p != nil {
					v = l.tracker(p.TrackerID).Name + " #" + strconv.FormatInt(p.ID, 10) + ": " + p.Subject
				}
			}
		}
		add(m.l("field_"+attr), v)
	}
	for _, cv := range im.VisibleCustomFieldValues() {
		v := rails.ToS(l.formatCustomValue(cv.CF, cv.Value(), im.customized(), false))
		add(cv.CF.Name, v)
	}
	return items
}

// setIssueData は mailer/_issue パーシャルのデータ。
func (m *mailer) setIssueData(ic *issueContext) {
	r := ic.row
	d := m.data
	d["IssueID"] = r.ID
	d["IssueTitle"] = ic.im.Tracker.Name + " #" + strconv.FormatInt(r.ID, 10) + ": " + r.Subject
	d["IssueURL"] = ic.url
	d["Description"] = r.Description
	text := m.emailIssueAttributes(ic, false)
	for i, s := range text {
		text[i] = "* " + s
	}
	d["AttributesText"] = strings.Join(text, "\n")
	items := m.emailIssueAttributes(ic, true)
	var lis []string
	for _, s := range items {
		lis = append(lis, "<li>"+s+"</li>")
	}
	d["AttributesHTML"] = template.HTML(`<ul class="details">` + strings.Join(lis, "\n") + `</ul>`)
	badgeKey, badgeClass := "label_open_issues", "badge badge-status-open"
	if ic.im.Status != nil && ic.im.Status.IsClosed {
		badgeKey, badgeClass = "label_closed_issues", "badge badge-status-closed"
	}
	d["StatusBadge"] = template.HTML(`<span class="` + badgeClass + `">` + html.EscapeString(m.l(badgeKey)) + `</span>`)
	rr := ic.l.renderer()
	d["DescriptionHTML"] = rr.Textilizable(r.Description, redmine.Options{FullURL: true,
		Object: &redmine.Object{Kind: "issue", ID: r.ID, Project: ic.im.Project}})
	atts, err := repository.MailContainerAttachments(m.ctx, m.a.DB, "issue", r.ID)
	if err == nil {
		d["Attachments"] = m.mailAttachments(atts)
	}
	d["AttachmentsLabel"] = ljust(m.l("label_attachment_plural"), 37, "-")
}

func (m *mailer) issueSubjectPrefix(ic *issueContext) string {
	return "[" + ic.im.Project.Name + " - " + ic.im.Tracker.Name + " #" + strconv.FormatInt(ic.row.ID, 10) + "]"
}

// issueAdd は Mailer#issue_add。
func (m *mailer) issueAdd() (*mail.Message, error) {
	ic := m.loadIssue(m.p.IssueID)
	if ic == nil {
		return nil, nil
	}
	r := ic.row
	m.issueHeaders(ic)
	tok := tokenObject{"issue", r.ID, r.CreatedAt}
	m.messageIDFor(tok)
	m.referencesFor(tok)
	m.author = ic.l.principal(r.AuthorID)
	ic.url = m.url("/issues/" + strconv.FormatInt(r.ID, 10))
	subject := m.issueSubjectPrefix(ic)
	if m.a.Settings.Bool("show_status_changes_in_mail_subject") && ic.im.Status != nil {
		subject += " (" + ic.im.Status.Name + ")"
	}
	subject += " " + r.Subject
	m.setIssueData(ic)
	authorName := m.userName(m.author)
	m.data["AddedText"] = m.l("text_issue_added", i18n.Vars{"id": "#" + strconv.FormatInt(r.ID, 10), "author": authorName})
	m.data["AddedHTML"] = template.HTML(m.l("text_issue_added", i18n.Vars{
		"id": string(linkTo("#"+strconv.FormatInt(r.ID, 10), ic.url)), "author": html.EscapeString(authorName)}))
	to, err := m.mailTo([]*domain.User{m.user}, nil)
	if err != nil {
		return nil, err
	}
	return m.finish(to, subject, "issue_add")
}

// issueEdit は Mailer#issue_edit。
func (m *mailer) issueEdit() (*mail.Message, error) {
	ic := m.loadIssue(m.p.IssueID)
	if ic == nil {
		return nil, nil
	}
	env := ic.l.issuesEnv()
	j, err := env.FindJournal(m.ctx, m.p.JournalID)
	if err != nil || j == nil {
		return nil, nil
	}
	r := ic.row
	m.issueHeaders(ic)
	m.messageIDFor(tokenObject{"journal", j.ID, j.CreatedAt})
	m.referencesFor(tokenObject{"issue", r.ID, r.CreatedAt})
	m.author = ic.l.principal(j.UserID)
	s := m.issueSubjectPrefix(ic) + " "
	if j.NewValueFor("status_id") != nil && m.a.Settings.Bool("show_status_changes_in_mail_subject") && ic.im.Status != nil {
		s += "(" + ic.im.Status.Name + ") "
	}
	s += r.Subject
	ic.url = m.url("/issues/" + strconv.FormatInt(r.ID, 10) + "#change-" + strconv.FormatInt(j.ID, 10))
	m.setIssueData(ic)
	details, err := env.VisibleDetails(m.ctx, j, ic.im.I, m.c.User)
	if err != nil {
		return nil, err
	}
	var textDetails []string
	for _, d := range ic.l.detailsToStrings(details, ic.im, true, false) {
		textDetails = append(textDetails, string(d))
	}
	m.data["DetailsText"] = textDetails
	m.data["DetailsHTML"] = ic.l.detailsToStrings(details, ic.im, false, false)
	m.data["PrivateNotes"] = j.PrivateNotes
	m.data["Notes"] = j.Notes
	m.data["HasNotes"] = j.HasNotes()
	m.data["NotesHTML"] = ic.l.renderer().Textilizable(j.Notes, redmine.Options{FullURL: true,
		Object: &redmine.Object{Kind: "journal", ID: j.ID, Project: ic.im.Project, JournalizedID: r.ID}})
	userName := m.userName(m.author)
	m.data["UpdatedText"] = m.l("text_issue_updated", i18n.Vars{"id": "#" + strconv.FormatInt(r.ID, 10), "author": userName})
	m.data["UpdatedHTML"] = template.HTML(m.l("text_issue_updated", i18n.Vars{
		"id": string(linkTo("#"+strconv.FormatInt(r.ID, 10), ic.url)), "author": html.EscapeString(userName)}))
	to, err := m.mailTo([]*domain.User{m.user}, nil)
	if err != nil {
		return nil, err
	}
	return m.finish(to, s, "issue_edit")
}

// reminder は Mailer#reminder（期日が近い担当チケットの一覧）。
func (m *mailer) reminder() (*mail.Message, error) {
	l := m.a.newIssueLookup(m.c)
	l.preloadIssues(m.p.IssueIDs)
	type item struct {
		Text string
		HTML template.HTML
	}
	var rows []*query.IssueRow
	for _, id := range m.p.IssueIDs {
		if r := l.issue(id); r != nil {
			rows = append(rows, r)
		}
	}
	if len(rows) == 0 {
		return nil, nil
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.DueDate != nil && b.DueDate != nil && !a.DueDate.Equal(*b.DueDate) {
			return a.DueDate.Before(*b.DueDate)
		}
		return a.ID < b.ID
	})
	var items []item
	for _, r := range rows {
		p := l.project(r.ProjectID)
		dist := ""
		if r.DueDate != nil {
			dist = l.dueDateDistanceInWords(*r.DueDate)
		}
		text := p.Name + " - " + l.tracker(r.TrackerID).Name + " #" + strconv.FormatInt(r.ID, 10) + ": " + r.Subject + " (" + dist + ")"
		link := l.linkToIssue(r, redmine.LinkToIssueOptions{Project: true, FullURL: true})
		items = append(items, item{Text: text, HTML: link + template.HTML(" ("+html.EscapeString(dist)+")")})
	}
	days := m.p.Days
	openURL := m.url("/issues?" + "assigned_to_id=me&set_filter=1&sort=due_date%3Aasc")
	remURL := m.url("/issues?" + "f%5B%5D=status_id&f%5B%5D=assigned_to_id&f%5B%5D=due_date&op%5Bassigned_to_id%5D=%3D&op%5Bdue_date%5D=%3Ct%2B&op%5Bstatus_id%5D=o&set_filter=1&sort=due_date%3Aasc&v%5Bassigned_to_id%5D%5B%5D=me&v%5Bdue_date%5D%5B%5D=" + strconv.Itoa(days))
	count, err := m.openAssignedIssueCount()
	if err != nil {
		return nil, err
	}
	m.data["Items"] = items
	m.data["Days"] = days
	m.data["OpenIssuesURL"] = openURL
	m.data["BodyText"] = m.l("mail_body_reminder", i18n.Vars{"count": len(rows), "days": days})
	m.data["BodyHTML"] = template.HTML(m.l("mail_body_reminder", i18n.Vars{"count": string(linkTo(strconv.Itoa(len(rows)), remURL)), "days": days}))
	m.data["OpenCount"] = m.l("label_x_open_issues_abbr", i18n.Vars{"count": count})
	m.data["ViewAllLink"] = linkTo(m.l("label_issue_view_all"), openURL)
	to, err := m.mailTo([]*domain.User{m.user}, nil)
	if err != nil {
		return nil, err
	}
	return m.finish(to, m.l("mail_subject_reminder", i18n.Vars{"count": len(rows), "days": days}), "reminder")
}

// openAssignedIssueCount は IssueQuery（status: 未完了, assigned_to_id: me）の issue_count（User.current = 受信者）。
func (m *mailer) openAssignedIssueCount() (int, error) {
	vis, err := m.c.Authz().IssueVisibleCondition(m.ctx, authz.ConditionOptions{})
	if err != nil {
		return 0, err
	}
	ids := []int64{m.c.User.ID}
	gids, err := m.c.Authz().GroupIDs(m.ctx)
	if err != nil {
		return 0, err
	}
	ids = append(ids, gids...)
	return repository.CountOpenIssuesAssignedTo(m.ctx, m.a.DB, vis, ids)
}

var _ = issues.NotifyIssueAdd
