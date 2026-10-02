package handler

// Mailer#document_added / attachments_added / news_added / news_comment_added / message_posted /
// wiki_content_added / wiki_content_updated。

import (
	"html"
	"html/template"
	"net/url"
	"strconv"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/mail"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
)

func strDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (m *mailer) project(id int64) *domain.Project {
	p, err := repository.GetProject(m.ctx, m.a.DB, id)
	if err != nil {
		return nil
	}
	return p
}

func (m *mailer) textilize(text string, obj *redmine.Object, p *domain.Project) template.HTML {
	r := m.a.Helpers.WikiRenderer(m.c.Page())
	return r.Textilizable(text, redmine.Options{FullURL: true, Object: obj, Project: p})
}

func (m *mailer) userMailTo() ([]string, error) {
	return m.mailTo([]*domain.User{m.user}, nil)
}

// documentAdded は Mailer#document_added(user, document, author)。
func (m *mailer) documentAdded() (*mail.Message, error) {
	d, err := repository.GetMailDocument(m.ctx, m.a.DB, m.p.DocumentID)
	if err != nil {
		return nil, nil
	}
	p := m.project(d.ProjectID)
	if p == nil {
		return nil, nil
	}
	m.redmineHeaders("Project", p.Identifier)
	m.author = m.getUser(m.p.AuthorID)
	u := m.url("/documents/" + strconv.FormatInt(d.ID, 10))
	m.data["Title"] = d.Title
	m.data["CategoryName"] = d.CategoryName
	m.data["DocumentURL"] = u
	m.data["Description"] = strDeref(d.Description)
	m.data["DescriptionHTML"] = m.textilize(strDeref(d.Description), &redmine.Object{Kind: "document", ID: d.ID, Project: p}, nil)
	to, err := m.userMailTo()
	if err != nil {
		return nil, err
	}
	return m.finish(to, "["+p.Name+"] "+m.l("label_document_new")+": "+d.Title, "document_added")
}

// attachmentsAdded は Mailer#attachments_added(user, attachments)。
func (m *mailer) attachmentsAdded() (*mail.Message, error) {
	atts, err := repository.MailAttachmentsByIDs(m.ctx, m.a.DB, m.p.AttachmentIDs)
	if err != nil {
		return nil, err
	}
	if len(atts) == 0 || atts[0].ContainerKind == nil || atts[0].ContainerID == nil {
		return nil, nil
	}
	first := atts[0]
	m.author = m.getUser(first.AuthorID)
	var p *domain.Project
	var addedTo, addedToURL string
	switch *first.ContainerKind {
	case "project":
		p = m.project(*first.ContainerID)
		if p == nil {
			return nil, nil
		}
		addedToURL = m.url("/projects/" + p.Identifier + "/files")
		addedTo = m.l("label_project") + ": " + p.Name
	case "version":
		pid, name, err := repository.VersionProject(m.ctx, m.a.DB, *first.ContainerID)
		if err != nil {
			return nil, nil
		}
		p = m.project(pid)
		if p == nil {
			return nil, nil
		}
		addedToURL = m.url("/projects/" + p.Identifier + "/files")
		addedTo = m.l("label_version") + ": " + name
	case "document":
		d, err := repository.GetMailDocument(m.ctx, m.a.DB, *first.ContainerID)
		if err != nil {
			return nil, nil
		}
		p = m.project(d.ProjectID)
		if p == nil {
			return nil, nil
		}
		addedToURL = m.url("/documents/" + strconv.FormatInt(d.ID, 10))
		addedTo = m.l("label_document") + ": " + d.Title
	default:
		return nil, nil
	}
	summary := m.l("label_attachment_summary", i18n.Vars{"filename": first.Filename, "count": len(atts) - 1})
	m.redmineHeaders("Project", p.Identifier)
	m.data["Attachments"] = m.mailAttachments(atts)
	m.data["AddedTo"] = addedTo
	m.data["AddedToURL"] = addedToURL
	to, err := m.userMailTo()
	if err != nil {
		return nil, err
	}
	return m.finish(to, "["+p.Name+"] "+m.l("label_attachment_new")+": "+summary, "attachments_added")
}

// newsAdded は Mailer#news_added(user, news)。
func (m *mailer) newsAdded() (*mail.Message, error) {
	n, err := repository.GetMailNews(m.ctx, m.a.DB, m.p.NewsID)
	if err != nil {
		return nil, nil
	}
	p := m.project(n.ProjectID)
	if p == nil {
		return nil, nil
	}
	m.redmineHeaders("Project", p.Identifier)
	m.author = m.getUser(n.AuthorID)
	tok := tokenObject{"news", n.ID, n.Created.Time}
	m.messageIDFor(tok)
	m.referencesFor(tok)
	u := m.url("/news/" + strconv.FormatInt(n.ID, 10))
	m.data["NewsTitle"] = n.Title
	m.data["NewsURL"] = u
	m.data["AuthorName"] = m.userName(m.author)
	m.data["Description"] = strDeref(n.Description)
	m.data["DescriptionHTML"] = m.textilize(strDeref(n.Description), &redmine.Object{Kind: "news", ID: n.ID, Project: p}, nil)
	atts, err := repository.MailContainerAttachments(m.ctx, m.a.DB, "news", n.ID)
	if err != nil {
		return nil, err
	}
	m.data["Attachments"] = m.mailAttachments(atts)
	m.data["AttachmentsLabel"] = ljust(m.l("label_attachment_plural"), 37, "-")
	to, err := m.userMailTo()
	if err != nil {
		return nil, err
	}
	return m.finish(to, "["+p.Name+"] "+m.l("label_news")+": "+n.Title, "news_added")
}

// newsCommentAdded は Mailer#news_comment_added(user, comment)。
func (m *mailer) newsCommentAdded() (*mail.Message, error) {
	c, err := repository.GetMailComment(m.ctx, m.a.DB, m.p.CommentID)
	if err != nil {
		return nil, nil
	}
	n, err := repository.GetMailNews(m.ctx, m.a.DB, c.NewsID)
	if err != nil {
		return nil, nil
	}
	p := m.project(n.ProjectID)
	if p == nil {
		return nil, nil
	}
	m.redmineHeaders("Project", p.Identifier)
	m.author = m.getUser(c.AuthorID)
	m.messageIDFor(tokenObject{"comment", c.ID, c.Created.Time})
	m.referencesFor(tokenObject{"news", n.ID, n.Created.Time})
	author := m.userName(m.author)
	m.data["NewsTitle"] = n.Title
	m.data["NewsURL"] = m.url("/news/" + strconv.FormatInt(n.ID, 10))
	m.data["WroteText"] = m.l("text_user_wrote", i18n.Vars{"value": author})
	m.data["WroteHTML"] = template.HTML(m.l("text_user_wrote", i18n.Vars{"value": html.EscapeString(author)}))
	m.data["Comments"] = strDeref(c.Content)
	// Comment は project を持たないため、プロジェクト文脈なしで整形する（Redmine と同じ）
	m.data["CommentsHTML"] = m.textilize(strDeref(c.Content), nil, nil)
	to, err := m.userMailTo()
	if err != nil {
		return nil, err
	}
	return m.finish(to, "Re: ["+p.Name+"] "+m.l("label_news")+": "+n.Title, "news_comment_added")
}

// messagePosted は Mailer#message_posted(user, message)。
func (m *mailer) messagePosted() (*mail.Message, error) {
	msg, err := repository.GetMailMessage(m.ctx, m.a.DB, m.p.MessageID)
	if err != nil {
		return nil, nil
	}
	p := m.project(msg.ProjectID)
	if p == nil {
		return nil, nil
	}
	m.redmineHeaders("Project", p.Identifier, "Topic-Id", msg.RootID())
	if msg.AuthorID != nil {
		m.author = m.getUser(*msg.AuthorID)
	}
	m.messageIDFor(tokenObject{"message", msg.ID, msg.Created.Time})
	root := msg
	if msg.ParentID != nil {
		if r, err := repository.GetMailMessage(m.ctx, m.a.DB, *msg.ParentID); err == nil {
			root = r
		}
	}
	m.referencesFor(tokenObject{"message", root.ID, root.Created.Time})
	// Message#event_url
	u := "/boards/" + strconv.FormatInt(msg.BoardID, 10) + "/topics/" + strconv.FormatInt(msg.RootID(), 10)
	if msg.ParentID != nil {
		u += "?r=" + strconv.FormatInt(msg.ID, 10) + "#message-" + strconv.FormatInt(msg.ID, 10)
	}
	m.data["MessageURL"] = m.url(u)
	m.data["AuthorName"] = m.userName(m.author)
	m.data["ProjectName"] = p.Name
	m.data["BoardName"] = msg.BoardName
	m.data["Subject"] = msg.Subject
	m.data["Content"] = strDeref(msg.Content)
	m.data["ContentHTML"] = m.textilize(strDeref(msg.Content), &redmine.Object{Kind: "message", ID: msg.ID, Project: p}, nil)
	atts, err := repository.MailContainerAttachments(m.ctx, m.a.DB, "message", msg.ID)
	if err != nil {
		return nil, err
	}
	m.data["Attachments"] = m.mailAttachments(atts)
	m.data["AttachmentsLabel"] = ljust(m.l("label_attachment_plural"), 37, "-")
	to, err := m.userMailTo()
	if err != nil {
		return nil, err
	}
	return m.finish(to, "["+p.Name+" - "+msg.BoardName+" - msg"+strconv.FormatInt(root.ID, 10)+"] "+msg.Subject, "message_posted")
}

// wikiContent は Mailer#wiki_content_added / wiki_content_updated(user, wiki_content)。
func (m *mailer) wikiContent(updated bool) (*mail.Message, error) {
	w, err := repository.GetMailWikiContent(m.ctx, m.a.DB, m.p.WikiPageID, m.p.WikiVersion)
	if err != nil {
		return nil, nil
	}
	p := m.project(w.ProjectID)
	if p == nil {
		return nil, nil
	}
	m.redmineHeaders("Project", p.Identifier, "Wiki-Page-Id", w.PageID)
	if w.AuthorID != nil {
		m.author = m.getUser(*w.AuthorID)
	}
	// WikiContent の id は buropher にはないため、ページの id を使う（Redmine では wiki_contents.id）
	m.messageIDFor(tokenObject{"wiki_content", w.PageID, w.Updated.Time})
	pretty := domain.WikiPrettyTitle(w.Title)
	pageURL := m.url("/projects/" + p.Identifier + "/wiki/" + url.PathEscape(w.Title))
	author := m.userName(m.author)
	bodyKey, subjKey, view := "mail_body_wiki_content_added", "mail_subject_wiki_content_added", "wiki_content_added"
	if updated {
		bodyKey, subjKey, view = "mail_body_wiki_content_updated", "mail_subject_wiki_content_updated", "wiki_content_updated"
		m.data["DiffURL"] = m.url("/projects/" + p.Identifier + "/wiki/" + url.PathEscape(w.Title) + "/" + strconv.Itoa(w.Version) + "/diff")
	}
	m.data["BodyText"] = m.l(bodyKey, i18n.Vars{"id": html.EscapeString(pretty), "author": html.EscapeString(author)})
	m.data["BodyHTML"] = template.HTML(m.l(bodyKey, i18n.Vars{"id": string(linkTo(pretty, pageURL)), "author": html.EscapeString(author)}))
	m.data["Comments"] = strDeref(w.Comments)
	m.data["WikiURL"] = pageURL
	m.data["PrettyTitle"] = pretty
	to, err := m.userMailTo()
	if err != nil {
		return nil, err
	}
	return m.finish(to, "["+p.Name+"] "+m.l(subjKey, i18n.Vars{"id": pretty}), view)
}
