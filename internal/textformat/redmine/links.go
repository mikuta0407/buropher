// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package redmine

// parse_inline_attachments / parse_wiki_links / parse_redmine_links（application_helper.rb:989-1388）。

import (
	"html/template"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// objectAttachments は options[:attachments] + obj.attachments（Journal は journalized の添付）。
func (r *Renderer) objectAttachments(obj *Object, extra []*Attachment) []*Attachment {
	atts := append([]*Attachment(nil), extra...)
	if kind, id, ok := obj.attachmentContainer(); ok && r.Store != nil {
		a, err := r.Store.Attachments(kind, id)
		r.logErr("attachments", err)
		atts = append(atts, a...)
	}
	return atts
}

// inlineAttachmentFinder は InlineAttachmentsScrubber の find_attachment（options[:attachments] と
// オブジェクトの添付から、ファイル名が一致する最新の添付を探す）。添付の読み込みは最初の画像まで遅らせる
// （Redmine 7.0.1 #44348: 添付の多いオブジェクトで整形が遅くならないように）。
func (r *Renderer) inlineAttachmentFinder(obj *Object, opts Options) func(string) (string, string, bool) {
	var atts []*Attachment
	loaded := false
	return func(filename string) (string, string, bool) {
		if !loaded {
			atts = r.objectAttachments(obj, opts.Attachments)
			loaded = true
		}
		if len(atts) == 0 {
			return "", "", false
		}
		name := cgiUnescape(filename)
		if !utf8.ValidString(name) {
			return "", "", false
		}
		found := latestAttach(atts, name)
		if found == nil {
			return "", "", false
		}
		// 説明の '"' はスクラバが除く。< と > は直列化で属性値でもエスケープするため、後段のリンク置換で
		// 属性が閉じても生の HTML にはならない（htmldom.RenderHTML5 を参照）
		return r.downloadNamedAttachmentURL(found), found.Description.String, true
	}
}

// ---- Wiki リンク ----

var (
	reWikiLink      = rx(`(!)?(\[\[([^\n\|]+?)(\|([^\n\|]+?))?\]\])`)
	reWikiAnchor    = rx(`^\#(.+)$`)
	reWikiProject   = rx(`^([^\:]+)\:(.*)$`)
	reWikiPageAnchr = rx(`^(.+?)\#(.+)$`)
)

// findProjectByIdentifierOrName は Project.find_by_identifier(x) || Project.find_by_name(x)。
func (r *Renderer) findProjectByIdentifierOrName(s string) *domain.Project {
	if r.Store == nil {
		return nil
	}
	p, err := r.Store.ProjectByIdentifier(s)
	r.logErr("project", err)
	if p == nil {
		p, err = r.Store.ProjectByName(s)
		r.logErr("project", err)
	}
	return p
}

// findWikiPage は project.wiki.find_page(title)（title が空なら開始ページ）。
func (r *Renderer) findWikiPage(w *Wiki, title string) *WikiPage {
	if blank(title) {
		title = w.StartPage
	}
	p, err := r.Store.FindWikiPage(w, Titleize(title))
	r.logErr("wiki page", err)
	return p
}

func (r *Renderer) wiki(p *domain.Project) *Wiki {
	if p == nil || r.Store == nil {
		return nil
	}
	w, err := r.Store.Wiki(p.ID)
	r.logErr("wiki", err)
	return w
}

// parseWikiLinks は parse_wiki_links。
func (r *Renderer) parseWikiLinks(text string, project *domain.Project, obj *Object, opts Options) string {
	if !strings.Contains(text, "[[") {
		return text
	}
	return gsub(reWikiLink, text, func(m md) string {
		linkProject := project
		all := m.s(2)
		if m.ok(1) {
			return all
		}
		page := cgiUnescapeHTML(m.s(3))
		title, hasTitle := m.s(5), m.ok(5)
		titlePresent := hasTitle && !blank(title)
		if a := match(reWikiAnchor, page); a != nil {
			anchor := sanitizeAnchorName(a.s(1))
			var name any = template.HTML(h(page))
			if titlePresent {
				name = template.HTML(title)
			}
			return string(rails.LinkTo(name, "#"+anchor, rails.NewHash("class", "wiki-page")))
		}
		if pm := match(reWikiProject, page); pm != nil {
			identifier := pm.s(1)
			page = pm.s(2)
			linkProject = r.findProjectByIdentifierOrName(identifier)
			if !hasTitle && blank(page) {
				title = identifier
				titlePresent = !blank(title)
			}
		}
		w := r.wiki(linkProject)
		if w == nil || !r.allowedTo("view_wiki_pages", linkProject) {
			return all
		}
		anchor := ""
		if am := match(reWikiPageAnchr, page); am != nil {
			page, anchor = am.s(1), am.s(2)
		}
		if !blank(anchor) {
			anchor = sanitizeAnchorName(anchor)
		}
		wikiPage := r.findWikiPage(w, page)
		var u string
		if !blank(anchor) && wikiPage != nil && obj.isWikiContent() && obj.Page.ID == wikiPage.ID {
			u = "#" + anchor
		} else {
			switch opts.WikiLinks {
			case "local":
				u = ""
				if !blank(page) {
					u = Titleize(page)
				}
				u += ".html"
				if !blank(anchor) {
					u += "#" + anchor
				}
			case "anchor":
				if !blank(page) {
					u = "#" + Titleize(page)
				} else {
					u = "#" + title
				}
				if !blank(anchor) {
					u += "_" + anchor
				}
			default:
				u = "/projects/" + escapeSegment(projectParam(linkProject)) + "/wiki"
				if !blank(page) {
					u += "/" + escapeSegment(Titleize(page))
				}
				if wikiPage == nil && obj != nil && obj.Kind == "wiki_content" && obj.Page != nil &&
					project != nil && linkProject != nil && project.ID == linkProject.ID {
					u += "?parent=" + cgiEscape(obj.Page.Title)
				}
				u = withAnchor(r.url(u), anchor)
			}
		}
		var name any = template.HTML(h(page))
		if titlePresent {
			name = template.HTML(title)
		}
		cls := "wiki-page"
		if wikiPage == nil {
			cls += " new"
		}
		return string(rails.LinkTo(name, u, rails.NewHash("class", cls)))
	})
}

// ---- Redmine リンク ----

// LINKS_RE（application_helper.rb:1350-1388）
var reLinks = rx(`<a( [^>]+?)?>(?<tag_content>.*?)</a>|` +
	`(?<leading>[` + spIn + `\(,\-\[\>]|^)` +
	`(?<esc>!)?` +
	`(?<project_prefix>(?<project_identifier>[a-z0-9\-_]+):)?` +
	`(?<prefix>attachment|document|version|forum|news|message|project|commit|source|export|user)?` +
	`(` +
	`(` +
	`(?<sep1>\#\#?)|` +
	`(` +
	`(?<repo_prefix>(?<repo_identifier>[a-z0-9\-_]+)\|)?` +
	`(?<sep2>r)` +
	`)` +
	`)` +
	`(` +
	`(?<identifier1>((\d)+|(note)))` +
	`(?<comment_suffix>` +
	`(\#note)?` +
	`-(?<comment_id>\d+)` +
	`)?` +
	`)|` +
	`(` +
	`(?<sep3>:)` +
	`(?<identifier2>[^"` + spIn + `<>][^` + spIn + `<>]*?|"[^"]+?")` +
	`)|` +
	`(` +
	`(?<sep4>@)` +
	`(?<identifier3>[A-Za-z0-9_\-@\.]*?)` +
	`)` +
	`)` +
	`(?=` +
	`(?=[` + punctIn + `][^A-Za-z0-9_/])|` +
	`,|` +
	`[` + spIn + `]|` +
	`\]|` +
	`<|` +
	`$)`)

var reRepoName = rx(`^(([a-z0-9\-_]+)\|)(.+)$`)
var reSourcePath = rx(`^[/\\]*(.*?)(@([^/\\@]+?))?(#(L\d+))?$`)
var reDoubleQuotes = rx(`^"(.*)"$`)

// removeDoubleQuotes は remove_double_quotes。
func removeDoubleQuotes(identifier string) string {
	name := gsub(reDoubleQuotes, identifier, func(m md) string { return m.s(1) })
	return cgiUnescapeHTML(name)
}

func firstOf(m md, names ...string) (string, bool) {
	for _, n := range names {
		if v, ok := m.n(n); ok {
			return v, true
		}
	}
	return "", false
}

// parseRedmineLinks は parse_redmine_links。
func (r *Renderer) parseRedmineLinks(text string, defaultProject *domain.Project, obj *Object, opts Options) string {
	return gsub(reLinks, text, func(m md) string {
		if _, ok := m.n("tag_content"); ok {
			return m.all()
		}
		leading := m.ns("leading")
		_, esc := m.n("esc")
		projectPrefix := m.ns("project_prefix")
		projectIdentifier, hasProjectIdentifier := m.n("project_identifier")
		prefix, hasPrefix := m.n("prefix")
		repoPrefix := m.ns("repo_prefix")
		repoIdentifier, hasRepoIdentifier := m.n("repo_identifier")
		sep, _ := firstOf(m, "sep1", "sep2", "sep3", "sep4")
		identifier, _ := firstOf(m, "identifier1", "identifier2", "identifier3")
		commentSuffix := m.ns("comment_suffix")
		commentID, hasCommentID := m.n("comment_id")

		var link template.HTML
		linked := false
		project := defaultProject
		if hasProjectIdentifier && r.Store != nil {
			p, err := r.Store.VisibleProjectByIdentifier(projectIdentifier)
			r.logErr("project", err)
			project = p
		}
		set := func(h template.HTML) { link, linked = h, true }
		if !esc && r.Store != nil {
			switch {
			case !hasPrefix && sep == "r":
				if project != nil {
					var repo *Repository
					if hasRepoIdentifier {
						repo = r.repositoryByIdentifier(project, repoIdentifier)
					} else {
						repo = r.defaultRepository(project)
					}
					if repo != nil {
						cs, err := r.Store.VisibleChangeset(repo.ID, identifier)
						r.logErr("changeset", err)
						if cs != nil {
							u := r.url("/projects/" + escapeSegment(projectParam(project)) + "/repository/" +
								escapeSegment(identifierParam(repo)) + "/revisions/" + escapeSegment(cs.Revision))
							set(rails.LinkTo(template.HTML(h(projectPrefix+repoPrefix+"r"+identifier)), u,
								rails.NewHash("class", "changeset", "title", truncateSingleLineRaw(cs.Comments.String, 100))))
						}
					}
				}
			case sep == "#" || sep == "##":
				oid, _ := strconv.ParseInt(identifier, 10, 64)
				switch {
				case !hasPrefix:
					if strconv.FormatInt(oid, 10) == identifier {
						is, err := r.Store.VisibleIssue(oid)
						r.logErr("issue", err)
						if is != nil {
							anchor := ""
							if hasCommentID {
								anchor = "note-" + commentID
							}
							u := r.issueURL(oid, anchor)
							if sep == "##" {
								set(rails.LinkTo(is.TrackerName+" #"+identifier+commentSuffix+": "+is.Subject, u,
									rails.NewHash("class", r.IssueCSSClasses(is), "title", r.l("field_status")+": "+is.StatusName)))
							} else {
								set(rails.LinkTo("#"+identifier+commentSuffix, u,
									rails.NewHash("class", r.IssueCSSClasses(is),
										"title", is.TrackerName+": "+rails.StringTruncate(is.Subject, 100, "", nil)+" ("+is.StatusName+")")))
							}
							break
						}
					}
					if identifier == "note" {
						set(rails.LinkTo("#note-"+commentID, "#note-"+commentID, nil))
					}
				case prefix == "document", prefix == "version", prefix == "forum", prefix == "news":
					kind := prefix
					if kind == "forum" {
						kind = "board"
					}
					n, err := r.Store.VisibleNamed(kind, oid)
					r.logErr(kind, err)
					if n != nil {
						set(r.linkToNamed(kind, n))
					}
				case prefix == "message":
					msg, err := r.Store.VisibleMessage(oid)
					r.logErr("message", err)
					if msg != nil {
						set(r.linkToMessage(msg, rails.NewHash("class", "message")))
					}
				case prefix == "project":
					p, err := r.Store.VisibleProjectByID(oid)
					r.logErr("project", err)
					if p != nil {
						set(r.LinkToProject(p, rails.NewHash("class", "project")))
					}
				case prefix == "user":
					u, err := r.Store.VisibleUser(oid)
					r.logErr("user", err)
					if u != nil {
						set(r.LinkToUser(u, "", false))
					}
				}
			case sep == ":":
				name := removeDoubleQuotes(identifier)
				switch prefix {
				case "document", "version", "forum", "news":
					kind := prefix
					if kind == "forum" {
						kind = "board"
					}
					if project != nil {
						n, err := r.Store.VisibleNamedByName(kind, project.ID, name)
						r.logErr(kind, err)
						if n != nil {
							set(r.linkToNamed(kind, n))
						}
					}
				case "commit", "source", "export":
					if project != nil {
						var repo *Repository
						if rm := match(reRepoName, name); rm != nil {
							repoPrefix, name = rm.s(1), rm.s(3)
							repo = r.repositoryByIdentifier(project, rm.s(2))
						} else {
							repo = r.defaultRepository(project)
						}
						if prefix == "commit" {
							if repo != nil {
								cs, err := r.Store.VisibleChangesetByScmidPrefix(repo.ID, name)
								r.logErr("changeset", err)
								if cs != nil {
									u := r.url("/projects/" + escapeSegment(projectParam(project)) + "/repository/" +
										escapeSegment(identifierParam(repo)) + "/revisions/" + escapeSegment(changesetIdentifier(repo, cs)))
									set(rails.LinkTo(template.HTML(h(projectPrefix+repoPrefix+name)), u,
										rails.NewHash("class", "changeset", "title", truncateSingleLineRaw(cs.Comments.String, 100))))
								}
							}
						} else if repo != nil && r.allowedTo("browse_repository", project) {
							sm := match(reSourcePath, name)
							var path, rev, anchor string
							if sm != nil {
								path, rev, anchor = sm.s(1), sm.s(3), sm.s(5)
							}
							action, cls := "entry", "source"
							if prefix == "export" {
								action, cls = "raw", "source download"
							}
							u := r.repositoryEntryURL(project, repo, action, toPathParam(path), rev, anchor)
							set(rails.LinkTo(template.HTML(h(projectPrefix+prefix+":"+repoPrefix+name)), u, rails.NewHash("class", cls)))
						}
						repoPrefix = ""
					}
				case "attachment":
					atts := r.objectAttachments(obj, opts.Attachments)
					if a := latestAttach(atts, name); a != nil {
						set(r.linkToAttachment(a, "attachment"))
					}
				case "project":
					p, err := r.Store.VisibleProjectByIdentifierOrLowerName(strings.ToLower(name))
					r.logErr("project", err)
					if p != nil {
						set(r.LinkToProject(p, rails.NewHash("class", "project")))
					}
				case "user":
					u, err := r.Store.VisibleUserByLogin(strings.ToLower(name))
					r.logErr("user", err)
					if u != nil {
						set(r.LinkToUser(u, "", false))
					}
				}
			case sep == "@":
				name := removeDoubleQuotes(identifier)
				u, err := r.Store.VisibleUserByLogin(strings.ToLower(name))
				r.logErr("user", err)
				if u != nil {
					set(r.LinkToUser(u, "user-mention", true))
				}
			}
		}
		if linked {
			return leading + string(link)
		}
		return leading + projectPrefix + prefix + repoPrefix + sep + identifier + commentSuffix
	})
}

// linkToNamed は文書・バージョン・フォーラム・ニュースへのリンク。
func (r *Renderer) linkToNamed(kind string, n *Named) template.HTML {
	id := strconv.FormatInt(n.ID, 10)
	switch kind {
	case "document":
		return rails.LinkTo(n.Name, r.url("/documents/"+id), rails.NewHash("class", "document"))
	case "version":
		return rails.LinkTo(n.Name, r.url("/versions/"+id), rails.NewHash("class", "version"))
	case "news":
		return rails.LinkTo(n.Name, r.url("/news/"+id), rails.NewHash("class", "news"))
	}
	// board: project_board_url(board.project, board)
	p, err := r.Store.ProjectByID(n.ProjectID)
	r.logErr("project", err)
	return rails.LinkTo(n.Name, r.url("/projects/"+escapeSegment(projectParam(p))+"/boards/"+id), rails.NewHash("class", "board"))
}

func (r *Renderer) repositories(p *domain.Project) []*Repository {
	repos, err := r.Store.Repositories(p.ID)
	r.logErr("repositories", err)
	return repos
}

// repositoryByIdentifier は project.repositories.detect {|repo| repo.identifier == id}。
func (r *Renderer) repositoryByIdentifier(p *domain.Project, id string) *Repository {
	for _, repo := range r.repositories(p) {
		if repo.Identifier.Valid && repo.Identifier.String == id {
			return repo
		}
	}
	return nil
}

// defaultRepository は project.repository（is_default）。
func (r *Renderer) defaultRepository(p *domain.Project) *Repository {
	for _, repo := range r.repositories(p) {
		if repo.IsDefault {
			return repo
		}
	}
	return nil
}
