package handler

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/apibuilder"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/pagination"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/commonmark"
	"github.com/mikuta0407/buropher/internal/textformat/textile"
	"github.com/mikuta0407/buropher/internal/validation"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/internal/wikidiff"
)

// ---------------------------------------------------------------- index / date_index

// loadPagesForIndex は load_pages_for_index（wiki.pages.with_updated_on、LOWER(title) 順）。
func (a *App) loadPagesForIndex(c *Req) ([]*domain.WikiPage, error) {
	pages, err := repository.WikiPages(c.Ctx(), a.DB, c.wikiState().Wiki.ID)
	for _, p := range pages {
		p.Project = c.Project
	}
	return pages, err
}

// WikiIndex は wiki#index（タイトル順の索引・API の一覧）。
func (a *App) WikiIndex(c *Req) {
	pages, err := a.loadPagesForIndex(c)
	if err != nil {
		a.wikiError(c, err)
		return
	}
	if httpx.IsAPIRequest(c.R) {
		byID := map[int64]*domain.WikiPage{}
		for _, p := range pages {
			byID[p.ID] = p
		}
		c.RenderAPI(0, func(b apibuilder.Builder) {
			b.Array("wiki_pages", nil, func() {
				for _, p := range pages {
					b.Object("wiki_page", func() {
						b.Value("title", p.Title)
						if p.ParentID != nil {
							if parent := byID[*p.ParentID]; parent != nil {
								b.Attrs("parent", apibuilder.A("title", parent.Title))
							}
						}
						b.Value("version", wikiPageVersionValue(p))
						b.Value("created_on", p.CreatedAt)
						b.Value("updated_on", wikiPageUpdatedOnValue(p))
					})
				}
			})
		})
		return
	}
	if !a.wikiHTMLFormat(c) {
		return
	}
	c.Render("wiki/index", a.wikiIndexData(c, pages))
}

func wikiPageVersionValue(p *domain.WikiPage) any {
	if !p.HasContent {
		return nil
	}
	return p.CurrentVersion
}

func wikiPageUpdatedOnValue(p *domain.WikiPage) any {
	if !p.HasContent {
		return nil
	}
	return p.UpdatedOn
}

// wikiHTMLFormat は respond_to の format.html のみのアクションで html 以外を 406 にする。
func (a *App) wikiHTMLFormat(c *Req) bool {
	switch c.Params().String("format") {
	case "", "html":
		return true
	}
	wikiNotAcceptable(c)
	c.Halt()
	return false
}

func (a *App) wikiIndexData(c *Req, pages []*domain.WikiPage) map[string]any {
	return map[string]any{
		"Pages":      pages,
		"PagesTree":  wikiPagesTree(pages),
		"Sidebar":    a.wikiSidebar(c),
		"Wiki":       c.wikiState().Wiki,
		"CanEdit":    c.AllowedTo(domain.Perm("edit_wiki_pages"), c.Project),
		"CanManage":  c.AllowedTo(domain.Perm("manage_wiki"), c.Project),
		"CanExport":  c.AllowedTo(domain.Perm("export_wiki_pages"), c.Project),
		"AtomKey":    c.AtomKey(),
		"ProjectKey": projectParam(c.Project),
	}
}

// WikiPagesTree は render_page_hierarchy の pages（parent_id → 子ページ、0 は根）。
type WikiPagesTree = map[int64][]*domain.WikiPage

func wikiPagesTree(pages []*domain.WikiPage) WikiPagesTree {
	t := WikiPagesTree{}
	ids := map[int64]bool{}
	for _, p := range pages {
		ids[p.ID] = true
	}
	for _, p := range pages {
		var k int64
		if p.ParentID != nil {
			k = *p.ParentID
		}
		t[k] = append(t[k], p)
	}
	return t
}

// WikiDateIndex は wiki#date_index（更新日ごとの索引）。
func (a *App) WikiDateIndex(c *Req) {
	pages, err := a.loadPagesForIndex(c)
	if err != nil {
		a.wikiError(c, err)
		return
	}
	if !a.wikiHTMLFormat(c) {
		return
	}
	type group struct {
		Date  time.Time
		Pages []*domain.WikiPage
	}
	byDate := map[string]*group{}
	for _, p := range pages {
		// p.updated_on.to_date（Time.zone のローカル日付）
		t := p.UpdatedOn.Local()
		if c.Loc.Location != nil {
			t = p.UpdatedOn.In(c.Loc.Location)
		}
		d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		k := d.Format("2006-01-02")
		if byDate[k] == nil {
			byDate[k] = &group{Date: d}
		}
		byDate[k].Pages = append(byDate[k].Pages, p)
	}
	var groups []*group
	for _, g := range byDate {
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Date.After(groups[j].Date) })
	data := a.wikiIndexData(c, pages)
	data["PagesByDate"] = groups
	c.Render("wiki/date_index", data)
}

// ---------------------------------------------------------------- show

// WikiShow は wiki#show（version 指定・保護ページ・存在しなければ編集フォーム・エクスポート・API）。
func (a *App) WikiShow(c *Req) {
	ws := c.wikiState()
	page := ws.Page
	versionParam := c.Params().String("version")
	if _, ok := c.Params().Get("version"); ok && !c.AllowedTo(domain.Perm("view_wiki_edits"), c.Project) {
		c.DenyAccess()
		return
	}
	content, err := a.contentForVersion(c, page, versionParam)
	if err != nil {
		a.wikiError(c, err)
		return
	}
	if content == nil {
		if httpx.IsBlank(versionParam) && c.AllowedTo(domain.Perm("edit_wiki_pages"), c.Project) && c.wikiEditable(page) && !httpx.IsAPIRequest(c.R) {
			a.wikiEdit(c)
			return
		}
		c.Render404("")
		return
	}
	format := c.Params().String("format")
	if c.AllowedTo(domain.Perm("export_wiki_pages"), c.Project) {
		switch format {
		case "pdf":
			// TODO: PDF 出力（Redmine::Export::PDF::WikiPdfHelper#wiki_page_to_pdf）は未実装。
			wikiNotAcceptable(c)
			c.Halt()
			return
		case "html":
			a.wikiSendExport(c, page, content)
			return
		case "txt":
			sendData(c, []byte(content.Text), "text/plain", page.Title+".txt")
			return
		}
	}
	current := content.Version == page.CurrentVersion
	editable := c.wikiEditable(page)
	sectionsEditable := editable && c.AllowedTo(domain.Perm("edit_wiki_pages"), c.wikiPageProject(page)) &&
		current && a.supportsSectionEdit()

	if httpx.IsAPIRequest(c.R) {
		a.renderWikiPageAPI(c, page, content, 0)
		return
	}
	if format != "" && format != "html" {
		wikiNotAcceptable(c)
		c.Halt()
		return
	}
	data, err := a.wikiPageData(c, page)
	if err != nil {
		a.wikiError(c, err)
		return
	}
	data["Content"] = content
	data["CurrentVersion"] = current
	data["Editable"] = editable
	data["SectionsEditable"] = sectionsEditable
	data["TextObject"], err = a.wikiTextObject(c, page, content, current)
	if err != nil {
		a.wikiError(c, err)
		return
	}
	if !current {
		prev, err := repository.WikiPreviousVersion(c.Ctx(), a.DB, page.ID, content.Version)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			a.wikiError(c, err)
			return
		}
		next, err := repository.WikiNextVersion(c.Ctx(), a.DB, page.ID, content.Version)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			a.wikiError(c, err)
			return
		}
		data["Previous"], data["Next"] = prev, next
	}
	n, err := repository.WikiVersionsCount(c.Ctx(), a.DB, page.ID)
	if err != nil {
		a.wikiError(c, err)
		return
	}
	data["VersionsCount"] = n
	data["VersionParam"] = versionParam
	atts, err := a.wikiAttachments(c, page)
	if err != nil {
		a.wikiError(c, err)
		return
	}
	data["Attachments"] = atts
	// acts_as_attachable: attachments_editable? = visible? && edit_wiki_pages、
	// attachments_deletable? = editable_by? && visible? && delete_wiki_pages_attachments
	pr := c.wikiPageProject(page)
	visible := c.AllowedTo(domain.Perm("view_wiki_pages"), pr)
	data["AttachmentsEditable"] = visible && c.AllowedTo(domain.Perm("edit_wiki_pages"), pr)
	data["AttachmentsDeletable"] = visible && a.wikiAttachmentsDeletable(c, page)
	data["CanAddAttachment"] = c.Authorize0("wiki", "add_attachment")
	data["ShowWatchers"] = a.wikiShowWatchers(c, page)
	c.Render("wiki/show", data)
}

// Authorize0 は authorize_for(controller, action)（@project で判定。描画しない）。
func (c *Req) Authorize0(ctrl, action string) bool {
	return c.AllowedTo(domain.ControllerAction(ctrl, action), c.Project)
}

// wikiShowWatchers は User.current.allowed_to?(:add_wiki_page_watchers) || (watchers.present? && view_wiki_page_watchers)。
func (a *App) wikiShowWatchers(c *Req, page *domain.WikiPage) bool {
	if c.AllowedTo(domain.Perm("add_wiki_page_watchers"), c.Project) {
		return true
	}
	ws, err := repository.Watchers(c.Ctx(), a.DB, "wiki_page", page.ID)
	return err == nil && len(ws) > 0 && c.AllowedTo(domain.Perm("view_wiki_page_watchers"), c.Project)
}

// contentForVersion は WikiPage#content_for_version(version)（本文が無ければ nil）。
func (a *App) contentForVersion(c *Req, page *domain.WikiPage, version string) (*domain.WikiContentVersion, error) {
	if page.NewRecord() || page.CurrentVersion == 0 {
		return nil, nil
	}
	v := page.CurrentVersion
	if version != "" {
		v = int(pagination.RubyToI(version))
	}
	cv, err := repository.WikiPageVersion(c.Ctx(), a.DB, page.ID, v)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, nil
	}
	return cv, err
}

// supportsSectionEdit は Redmine::WikiFormatting.supports_section_edit?。
func (a *App) supportsSectionEdit() bool {
	switch a.Settings.String("text_formatting") {
	case "textile", "common_mark", "markdown":
		return true
	}
	return false
}

// wikiPageData はページ表示系（show / edit / history ...）に共通のデータ。
func (a *App) wikiPageData(c *Req, page *domain.WikiPage) (map[string]any, error) {
	anc, err := a.wikiAncestors(c, page)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"Page":      page,
		"Wiki":      c.wikiState().Wiki,
		"Ancestors": anc,
		"Sidebar":   a.wikiSidebar(c),
		"CanEdit":   c.AllowedTo(domain.Perm("edit_wiki_pages"), c.Project),
		"CanExport": c.AllowedTo(domain.Perm("export_wiki_pages"), c.Project),
		"CanEdits":  c.AllowedTo(domain.Perm("view_wiki_edits"), c.Project),
	}, nil
}

// wikiAncestors は page.ancestors.reverse（根から親の順）。
func (a *App) wikiAncestors(c *Req, page *domain.WikiPage) ([]*domain.WikiPage, error) {
	if page.ParentID == nil {
		return nil, nil
	}
	anc, err := repository.WikiPageAncestors(c.Ctx(), a.DB, page)
	if err != nil {
		return nil, err
	}
	out := make([]*domain.WikiPage, 0, len(anc))
	for i := len(anc) - 1; i >= 0; i-- {
		anc[i].Project = c.wikiPageProject(page)
		out = append(out, anc[i])
	}
	return out, nil
}

// WikiSidebar は wiki/_sidebar のデータ。
type WikiSidebar struct {
	// Editable は edit_wiki_pages かつ Sidebar ページを編集できる。
	Editable bool
	// Content は wiki.sidebar.content（無ければ nil）。
	Content *domain.WikiContentVersion
	Object  any
}

func (a *App) wikiSidebar(c *Req) *WikiSidebar {
	ws := c.wikiState()
	sb := &WikiSidebar{}
	if ws.Wiki == nil {
		return sb
	}
	if c.AllowedTo(domain.Perm("edit_wiki_pages"), c.Project) {
		p, _, err := a.findOrNewPage(c, ws.Wiki, "Sidebar")
		if err == nil && c.wikiEditable(p) {
			sb.Editable = true
		}
	}
	p, _, err := a.findPage(c, ws.Wiki, "Sidebar", false)
	if err == nil && p != nil && p.CurrentVersion > 0 {
		if v, err := repository.WikiPageVersion(c.Ctx(), a.DB, p.ID, p.CurrentVersion); err == nil {
			sb.Content = v
			sb.Object, _ = a.wikiTextObject(c, p, v, true)
		}
	}
	return sb
}

// wikiError は内部エラー。
func (a *App) wikiError(c *Req, err error) {
	a.logger().Error("wiki", "action", c.Action, "err", err)
	c.renderInternalError()
	c.Halt()
}

// ---------------------------------------------------------------- API

// renderWikiPageAPI は wiki/show.api.rsb。
func (a *App) renderWikiPageAPI(c *Req, page *domain.WikiPage, content *domain.WikiContentVersion, status int) {
	var parent *domain.WikiPage
	if page.ParentID != nil {
		parent, _ = repository.GetWikiPageByID(c.Ctx(), a.DB, 0, *page.ParentID)
	}
	var atts []*domain.Attachment
	include := c.IncludeInAPIResponse("attachments")
	if include {
		atts, _ = repository.ContainerAttachmentList(c.Ctx(), a.DB, "wiki_page", page.ID)
	}
	userFormat := a.Settings.String("user_format")
	c.RenderAPI(status, func(b apibuilder.Builder) {
		b.Object("wiki_page", func() {
			b.Value("title", page.Title)
			if parent != nil {
				b.Attrs("parent", apibuilder.A("title", parent.Title))
			}
			b.Value("text", content.Text)
			b.Value("version", content.Version)
			if content.AuthorID != nil {
				name := ""
				if content.Author != nil {
					name = content.Author.Name(userFormat)
				}
				b.Attrs("author", apibuilder.A("id", *content.AuthorID, "name", name))
			}
			if content.Comments == nil {
				b.Value("comments", nil)
			} else {
				b.Value("comments", *content.Comments)
			}
			b.Value("created_on", page.CreatedAt)
			b.Value("updated_on", content.UpdatedOn)
			if include {
				b.Array("attachments", nil, func() {
					for _, at := range atts {
						a.renderAPIAttachment(c, b, at)
					}
				})
			}
		})
	})
}

// ---------------------------------------------------------------- edit / update

// wikiContentForm は edit フォームの @content（form_for @content, :as => :content）。
type wikiContentForm struct {
	Version   int
	Comments  any // nil なら value 属性を出さない
	persisted bool
	errs      *validation.Errors
}

func (f *wikiContentForm) Persisted() bool { return f.persisted }

// ValidationErrors は error_messages_for 'content' のエラー。
func (f *wikiContentForm) ValidationErrors() *validation.Errors { return f.errs }

// wikiEditView は wiki/edit のデータ。
type wikiEditView struct {
	Page        *domain.WikiPage
	Content     *wikiContentForm
	Text        string
	Section     int
	HasSection  bool
	SectionHash string
	Parent      *domain.WikiPage
	// DeletedAttachmentIDs は @page.deleted_attachment_ids。
	DeletedAttachmentIDs []int64
}

// WikiEdit は wiki#edit。
func (a *App) WikiEdit(c *Req) { a.wikiEdit(c) }

func (a *App) wikiEdit(c *Req) {
	page := c.wikiState().Page
	if !c.wikiEditable(page) {
		c.Render403("")
		return
	}
	ev := &wikiEditView{Page: page, Content: &wikiContentForm{Version: 1, persisted: !page.NewRecord() && page.CurrentVersion > 0}}
	if page.NewRecord() && c.Params().Present("parent") {
		parent, _, err := a.findPage(c, c.wikiState().Wiki, c.Params().String("parent"), true)
		if err != nil {
			a.wikiError(c, err)
			return
		}
		if parent != nil {
			page.ParentID = &parent.ID
			ev.Parent = parent
		}
	}
	content, err := a.contentForVersion(c, page, c.Params().String("version"))
	if err != nil {
		a.wikiError(c, err)
		return
	}
	text := ""
	if content != nil {
		text = content.Text
	}
	if httpx.IsBlank(text) {
		text = a.initialPageContent(page)
	}
	// To prevent StaleObjectError exception when reverting to a previous version
	if page.CurrentVersion > 0 {
		ev.Content.Version = page.CurrentVersion
	}
	ev.Text = text
	if c.Params().Present("section") && a.supportsSectionEdit() {
		ev.Section = int(pagination.RubyToI(c.Params().String("section")))
		ev.HasSection = true
		ev.Text, ev.SectionHash = a.getSection(text, ev.Section)
		if httpx.IsBlank(ev.Text) {
			c.Render404("")
			return
		}
	}
	a.renderWikiEdit(c, ev)
}

// initialPageContent は initial_page_content(page)（テキスト書式のヘルパーごと）。
func (a *App) initialPageContent(page *domain.WikiPage) string {
	switch a.Settings.String("text_formatting") {
	case "textile":
		return "h1. " + page.PrettyTitle()
	case "common_mark", "markdown":
		return "# " + page.PrettyTitle()
	}
	return page.PrettyTitle()
}

func (a *App) isCommonMark() bool {
	f := a.Settings.String("text_formatting")
	return f == "common_mark" || f == "markdown"
}

// getSection は formatter.get_section(index)。
func (a *App) getSection(text string, index int) (string, string) {
	if a.isCommonMark() {
		return commonmark.GetSection(text, index)
	}
	return textile.GetSection(text, index)
}

// updateSection は formatter.update_section(index, update, hash)。
func (a *App) updateSection(text string, index int, update, hash string) (string, error) {
	if a.isCommonMark() {
		return commonmark.UpdateSection(text, index, update, hash)
	}
	return textile.UpdateSection(text, index, update, hash)
}

func isStaleSection(err error) bool {
	return errors.Is(err, textile.ErrStaleSection) || errors.Is(err, commonmark.ErrStaleSection)
}

func (a *App) renderWikiEdit(c *Req, ev *wikiEditView) {
	page := ev.Page
	data, err := a.wikiPageData(c, page)
	if err != nil {
		a.wikiError(c, err)
		return
	}
	if page.NewRecord() && ev.Parent != nil {
		// 新規ページの親（パンくずは page.ancestors）
		anc, err := a.wikiAncestors(c, &domain.WikiPage{ParentID: page.ParentID, Project: page.Project})
		if err != nil {
			a.wikiError(c, err)
			return
		}
		data["Ancestors"] = anc
	}
	data["Edit"] = ev
	data["Content"] = ev.Content
	// 親ページの選択肢: @page.safe_attribute_names.include?('parent_id') && @wiki.pages.any?
	if page.NewRecord() || c.AllowedTo(domain.Perm("rename_wiki_pages"), c.wikiPageProject(page)) {
		pages, err := a.loadPagesForIndex(c)
		if err != nil {
			a.wikiError(c, err)
			return
		}
		if len(pages) > 0 {
			opts, err := a.parentPageOptions(c, page, pages)
			if err != nil {
				a.wikiError(c, err)
				return
			}
			data["ParentOptions"] = opts
		}
	}
	atts, err := a.wikiAttachments(c, page)
	if err != nil {
		a.wikiError(c, err)
		return
	}
	data["Attachments"] = atts
	data["AttachmentsDeletable"] = len(atts) > 0 && a.wikiAttachmentsDeletable(c, page)
	data["CanAddWatchers"] = c.AllowedTo(domain.Perm("add_wiki_page_watchers"), c.Project)
	c.Render("wiki/edit", data)
}

// parentPageOptions は wiki_page_options_for_select(@wiki.pages - @page.self_and_descendants, @page.parent)。
func (a *App) parentPageOptions(c *Req, page *domain.WikiPage, pages []*domain.WikiPage) (*WikiPageOptions, error) {
	exclude := map[int64]bool{}
	if !page.NewRecord() {
		exclude[page.ID] = true
		ids, err := repository.WikiPageDescendantIDs(c.Ctx(), a.DB, page.ID)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			exclude[id] = true
		}
	}
	var list []*domain.WikiPage
	for _, p := range pages {
		if !exclude[p.ID] {
			list = append(list, p)
		}
	}
	var sel int64
	if page.ParentID != nil {
		sel = *page.ParentID
	}
	return &WikiPageOptions{Pages: list, Selected: sel}, nil
}

// WikiPageOptions は wiki_page_options_for_select の引数（pages と選択中のページ id）。
type WikiPageOptions struct {
	Pages    []*domain.WikiPage
	Selected int64
}

// WikiUpdate は wiki#update（新規作成・更新・セクション編集・競合検出・API）。
func (a *App) WikiUpdate(c *Req) {
	ws := c.wikiState()
	page, _, err := a.findOrNewPage(c, ws.Wiki, c.Params().String("id"))
	if err != nil {
		a.wikiError(c, err)
		return
	}
	ws.Page = page
	if !c.wikiEditable(page) {
		c.Render403("")
		return
	}
	api := httpx.IsAPIRequest(c.R)
	wasNew := page.NewRecord()
	ev := &wikiEditView{Page: page, Content: &wikiContentForm{Version: 1, persisted: !wasNew && page.CurrentVersion > 0}}
	if page.CurrentVersion > 0 {
		ev.Content.Version = page.CurrentVersion
	}

	// @page.safe_attributes = params[:wiki_page]
	pageErrs := validation.New("wiki_page")
	wp := c.Params().Map("wiki_page")
	isStartPage := false
	var deletedAttachmentIDs []int64
	if wp != nil {
		if wasNew || c.AllowedTo(domain.Perm("rename_wiki_pages"), c.wikiPageProject(page)) {
			if v, ok := wp.Get("parent_id"); ok {
				if httpx.IsBlank(v) {
					page.ParentID = nil
				} else {
					id := wp.Int("parent_id")
					page.ParentID = &id
				}
			}
		}
		if c.AllowedTo(domain.Perm("manage_wiki"), c.wikiPageProject(page)) && wp.Has("is_start_page") {
			v := wp.String("is_start_page")
			isStartPage = v == "1" || v == "true"
		}
		if a.wikiAttachmentsDeletable(c, page) {
			deletedAttachmentIDs = wp.Ints("deleted_attachment_ids")
		}
	}
	ev.DeletedAttachmentIDs = deletedAttachmentIDs

	// content_params = params[:content] || params[:wiki_page].slice(:text, :comments, :version)
	cp := c.Params().Map("content")
	if cp == nil && wp != nil && wp.Len() > 0 {
		cp = wp.Only("text", "comments", "version")
	}
	if cp == nil {
		cp = httpx.NewParams()
	}
	var comments *string
	if v, ok := cp.Get("comments"); ok && v != nil {
		s := cp.String("comments")
		comments = &s
	}
	if comments != nil {
		ev.Content.Comments = *comments
	}
	var text *string
	if v, ok := cp.Get("text"); ok && v != nil {
		s := cp.String("text")
		text = &s
	}
	if text != nil {
		ev.Text = *text
	}

	oldText, hasContent := "", false
	if !wasNew && page.CurrentVersion > 0 {
		cur, err := repository.WikiPageVersion(c.Ctx(), a.DB, page.ID, page.CurrentVersion)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			a.wikiError(c, err)
			return
		}
		if cur != nil {
			oldText, hasContent = cur.Text, true
		}
	}
	lockVersion := page.CurrentVersion
	var newText *string
	conflict := false
	if c.Params().Present("section") && a.supportsSectionEdit() {
		ev.Section = int(pagination.RubyToI(c.Params().String("section")))
		ev.HasSection = true
		ev.SectionHash = c.Params().String("section_hash")
		upd := ""
		if text != nil {
			upd = *text
		}
		t, err := a.updateSection(oldText, ev.Section, upd, ev.SectionHash)
		if err != nil {
			if !isStaleSection(err) {
				a.wikiError(c, err)
				return
			}
			conflict = true
		}
		newText = &t
	} else {
		if v := cp.String("version"); v != "" {
			lockVersion = int(pagination.RubyToI(v))
			ev.Content.Version = lockVersion
		}
		newText = text
	}

	// content.text_changed?（新規は nil からの変更）
	textChanged := false
	if !conflict {
		if hasContent {
			textChanged = newText != nil && *newText != oldText || newText == nil
			if newText == nil {
				textChanged = true // text = nil への変更（presence で失敗する）
			}
		} else {
			textChanged = newText != nil
		}
	}

	contentErrs := validation.New("wiki_content")
	if !conflict {
		a.validateWikiPage(c, page, pageErrs)
		if textChanged {
			if newText == nil || httpx.IsBlank(*newText) {
				contentErrs.Add("text", "blank")
			}
			if comments != nil && len([]rune(*comments)) > 1024 {
				contentErrs.Add("comments", "too_long", "count", 1024)
			}
		}
	}
	ev.Content.errs = contentErrs
	saved := false
	if !conflict && pageErrs.Empty() && contentErrs.Empty() {
		now := a.now()
		err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
			if wasNew {
				if err := repository.CreateWikiPage(c.Ctx(), tx, page, now); err != nil {
					return err
				}
			} else if err := repository.UpdateWikiPageAttrs(c.Ctx(), tx, page); err != nil {
				return err
			}
			if isStartPage {
				if err := repository.UpdateWikiStartPage(c.Ctx(), tx, page.WikiID, page.Title); err != nil {
					return err
				}
			}
			if len(deletedAttachmentIDs) > 0 {
				if err := a.deleteWikiAttachments(c, tx, page, deletedAttachmentIDs); err != nil {
					return err
				}
			}
			if textChanged {
				lv := lockVersion
				if wasNew || !hasContent {
					lv = 0
				}
				v, err := repository.AddWikiContentVersion(c.Ctx(), tx, page.ID, lv, authorIDOf(c.User), *newText, comments, now)
				if err != nil {
					return err
				}
				page.CurrentVersion = v
			}
			return nil
		})
		switch {
		case errors.Is(err, repository.ErrStaleObject):
			conflict = true
			if wasNew {
				page.ID = 0
			}
		case err != nil:
			a.wikiError(c, err)
			return
		default:
			saved = true
		}
	}
	if conflict {
		if api {
			httpx.Head(c.W, c.R, http.StatusConflict)
			c.Halt()
			return
		}
		c.Flash().Now("error", c.L("notice_locking_conflict"))
		a.renderWikiEdit(c, ev)
		return
	}
	if !saved {
		if api {
			c.RenderValidationErrors(contentErrs)
			return
		}
		a.renderWikiEdit(c, ev)
		return
	}

	// attachments = Attachment.attach_files(@page, params[:attachments] || params[:wiki_page][:uploads])
	attParams := c.Params().Map("attachments")
	if attParams == nil && wp != nil {
		attParams = wp.Map("uploads")
	}
	a.wikiAttachFiles(c, page, attParams)

	if api {
		if wasNew {
			cur, err := repository.WikiPageVersion(c.Ctx(), a.DB, page.ID, page.CurrentVersion)
			if err != nil {
				a.wikiError(c, err)
				return
			}
			c.W.Header().Set("Location", wikiPagePath(c.Project, page.Title))
			a.renderWikiPageAPI(c, page, cur, http.StatusCreated)
			return
		}
		c.RenderAPIOK()
		return
	}
	path := wikiPagePath(c.Project, page.Title)
	if ev.HasSection {
		path += "#section-" + strconv.Itoa(ev.Section)
	}
	c.Redirect(path)
}

func authorIDOf(u *domain.User) *int64 {
	if u == nil || u.ID == 0 {
		return nil
	}
	id := u.ID
	return &id
}

// validateWikiPage は WikiPage の検証（title・parent）。
func (a *App) validateWikiPage(c *Req, page *domain.WikiPage, errs *validation.Errors) {
	if httpx.IsBlank(page.Title) {
		errs.Add("title", "blank")
	}
	if !domain.ValidWikiTitleFormat(page.Title) {
		errs.Add("title", "invalid")
	}
	if page.Title != "" {
		taken, err := repository.WikiTitleTaken(c.Ctx(), a.DB, page.WikiID, page.Title, page.ID)
		if err == nil && taken {
			errs.Add("title", "taken")
		}
	}
	if len([]rune(page.Title)) > 255 {
		errs.Add("title", "too_long", "count", 255)
	}
	if page.ParentID != nil {
		parent, err := repository.GetWikiPageByID(c.Ctx(), a.DB, 0, *page.ParentID)
		if err != nil {
			// 存在しない親は belongs_to の検証がない（Redmine は nil として扱う）
			page.ParentID = nil
			return
		}
		if !page.NewRecord() {
			if parent.ID == page.ID {
				errs.Add("parent_title", "circular_dependency")
			} else if anc, err := repository.WikiPageAncestors(c.Ctx(), a.DB, parent); err == nil {
				for _, x := range anc {
					if x.ID == page.ID {
						errs.Add("parent_title", "circular_dependency")
						break
					}
				}
			}
		}
		if parent.WikiID != page.WikiID {
			errs.Add("parent_title", "not_same_project")
		}
	}
}

// ---------------------------------------------------------------- new

// wikiNewForm は new フォームの @page（labelled_form_for :page）。
type wikiNewForm struct {
	// Title は title（nil なら value 属性を出さない）。
	Title any
	errs  *validation.Errors
}

// WikiNew は wiki#new（タイトル入力フォームと検証）。
func (a *App) WikiNew(c *Req) {
	ws := c.wikiState()
	page := newWikiPage(c, ws.Wiki, c.Params().String("title"))
	if _, ok := c.Params().Get("title"); !ok {
		page.Title = ""
	}
	if !c.AllowedTo(domain.Perm("edit_wiki_pages"), c.Project) {
		c.Render403("")
		return
	}
	errs := validation.New("wiki_page")
	if c.R.Method == http.MethodPost {
		if !c.wikiEditable(page) {
			page.Title = ""
		}
		a.validateWikiPage(c, page, errs)
		titleErrs := validation.New("wiki_page")
		for _, e := range errs.List() {
			if e.Attr == "title" {
				titleErrs.Add(e.Attr, e.Key, mapVars(e.Vars)...)
			}
		}
		errs = titleErrs
		if errs.Empty() {
			path := wikiPagePath(c.Project, page.Title)
			if p := c.Params().String("parent"); c.Params().Has("parent") && p != "" {
				path += "?parent=" + queryEscape(p)
			}
			if httpx.Format(c.R) == "js" {
				httpx.SetContentType(c.W, "js", true)
				c.W.WriteHeader(http.StatusOK)
				_, _ = c.W.Write([]byte("window.location = " + jsonString(path)))
				c.Halt()
				return
			}
			c.Redirect(path)
			return
		}
	}
	data := map[string]any{
		"Form":     newFormOf(page.Title, c.Params().Has("title"), errs),
		"Errors":   errs.FullMessages(c.Loc),
		"ParentPr": c.Params().Present("parent"),
		"Parent":   c.Params().String("parent"),
	}
	if httpx.Format(c.R) == "js" {
		c.Render("wiki/new", data, RenderOptions{Format: "js"})
		return
	}
	c.Render("wiki/new", data)
}

func newFormOf(title string, present bool, errs *validation.Errors) *wikiNewForm {
	f := &wikiNewForm{errs: errs}
	if present {
		f.Title = title
	}
	return f
}

// ErrorsOn は errors[attr]（labelled_form_for のラベルに class="error" を付ける）。
func (f *wikiNewForm) ErrorsOn(attr string) []string { return errorsOn(f.errs, attr) }

// ErrorsOn は errors[attr]。
func (f *wikiRenameForm) ErrorsOn(attr string) []string { return errorsOn(f.errs, attr) }

func errorsOn(e *validation.Errors, attr string) []string {
	if e == nil {
		return nil
	}
	var out []string
	for _, x := range e.List() {
		if x.Attr == attr {
			out = append(out, x.Key+x.Message)
		}
	}
	return out
}

func mapVars(m map[string]any) []any {
	var out []any
	for k, v := range m {
		out = append(out, k, v)
	}
	return out
}

// ---------------------------------------------------------------- rename

// wikiRenameForm は rename フォームの @page（labelled_form_for :wiki_page）。
type wikiRenameForm struct {
	Title                 string
	IsStartPage           bool
	RedirectExistingLinks any
	WikiID                int64
	ParentID              *int64
	errs                  *validation.Errors
}

func (f *wikiRenameForm) Persisted() bool { return true }

// ValidationErrors は error_messages_for 'page'。
func (f *wikiRenameForm) ValidationErrors() *validation.Errors { return f.errs }

// WikiRename は wiki#rename（タイトル変更・リダイレクト・親ページ変更・他プロジェクトの Wiki への移動）。
func (a *App) WikiRename(c *Req) {
	ws := c.wikiState()
	page := ws.Page
	if !c.wikiEditable(page) {
		c.Render403("")
		return
	}
	oldTitle, oldWikiID, oldParent := page.Title, page.WikiID, page.ParentID
	originalTitle := page.PrettyTitle()
	form := &wikiRenameForm{Title: page.Title, RedirectExistingLinks: true, WikiID: page.WikiID, ParentID: page.ParentID}
	startPage := ws.Wiki.StartPage == oldTitle
	form.IsStartPage = startPage
	canRename := c.AllowedTo(domain.Perm("rename_wiki_pages"), c.wikiPageProject(page))
	canManage := c.AllowedTo(domain.Perm("manage_wiki"), c.wikiPageProject(page))

	var targetWiki *domain.Wiki = ws.Wiki
	if wp := c.Params().Map("wiki_page"); wp != nil {
		if canRename {
			// wiki_id は先に処理する（移動先プロジェクトで rename_wiki_pages が必要）
			if v := wp.String("wiki_id"); v != "" {
				if w, err := repository.GetWiki(c.Ctx(), a.DB, wp.Int("wiki_id")); err == nil {
					if p, err := repository.GetProject(c.Ctx(), a.DB, w.ProjectID); err == nil && c.AllowedTo(domain.Perm("rename_wiki_pages"), p) {
						targetWiki = w
						page.WikiID = w.ID
						form.WikiID = w.ID
					}
				}
			}
			if v, ok := wp.Get("title"); ok {
				page.Title = domain.WikiTitleize(httpx.ValueString(v))
				form.Title = page.Title
			}
			if v, ok := wp.Get("parent_id"); ok {
				if httpx.IsBlank(v) {
					page.ParentID = nil
				} else {
					id := wp.Int("parent_id")
					page.ParentID = &id
				}
				form.ParentID = page.ParentID
			}
			if v, ok := wp.Get("redirect_existing_links"); ok {
				form.RedirectExistingLinks = httpx.ValueString(v)
			}
		}
		if canManage && wp.Has("is_start_page") {
			v := wp.String("is_start_page")
			form.IsStartPage = v == "1"
		}
	}
	errs := validation.New("wiki_page")
	if c.R.Method == http.MethodPost {
		parentChanged := !sameIDPtr(oldParent, page.ParentID)
		// handle_rename_or_move: 移動先の Wiki に親が無ければ親を外す
		movedWiki := page.WikiID != oldWikiID
		if movedWiki && page.ParentID != nil && !parentChanged {
			if parent, err := repository.GetWikiPageByID(c.Ctx(), a.DB, 0, *page.ParentID); err != nil || parent.WikiID != page.WikiID {
				page.ParentID = nil
			}
		}
		a.validateWikiPage(c, page, errs)
		if !parentChanged {
			// validate_parent_title の not_same_project は parent_id が変わったときのみ
			errs = withoutKey(errs, "parent_title", "not_same_project")
		}
		if errs.Empty() {
			now := a.now()
			createRedirect := rails2s(form.RedirectExistingLinks) != "0"
			err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
				if page.Title != oldTitle || page.WikiID != oldWikiID {
					if err := repository.WikiHandleRename(c.Ctx(), tx, oldWikiID, oldTitle, page.WikiID, page.Title, createRedirect, now); err != nil {
						return err
					}
				}
				if err := repository.UpdateWikiPageAttrs(c.Ctx(), tx, page); err != nil {
					return err
				}
				if form.IsStartPage && canManage {
					if err := repository.UpdateWikiStartPage(c.Ctx(), tx, page.WikiID, page.Title); err != nil {
						return err
					}
				}
				if movedWiki {
					return a.moveWikiChildren(c, tx, page, oldWikiID, createRedirect, now)
				}
				return nil
			})
			if err != nil {
				a.wikiError(c, err)
				return
			}
			_ = targetWiki
			if err := a.loadPageProject(c, page); err != nil {
				a.wikiError(c, err)
				return
			}
			c.Flash().SetNotice(c.L("notice_successful_update"))
			c.Redirect(wikiPagePath(page.Project, page.Title))
			return
		}
	}
	data, err := a.wikiPageData(c, page)
	if err != nil {
		a.wikiError(c, err)
		return
	}
	data["OriginalTitle"] = originalTitle
	data["Form"] = form
	form.errs = errs
	data["OriginalPageTitle"] = oldTitle
	data["CanManage"] = canManage
	data["StartPageDisabled"] = startPage
	data["CanMove"] = canRename
	pages, err := a.loadPagesForIndex(c)
	if err != nil {
		a.wikiError(c, err)
		return
	}
	opts, err := a.parentPageOptions(c, &domain.WikiPage{ID: page.ID, ParentID: page.ParentID}, pages)
	if err != nil {
		a.wikiError(c, err)
		return
	}
	data["ParentOptions"] = opts
	if canRename {
		wo, err := a.wikiOptionsForSelect(c, page)
		if err != nil {
			a.wikiError(c, err)
			return
		}
		data["WikiOptions"] = wo
	}
	c.Render("wiki/rename", data)
}

func withoutKey(e *validation.Errors, attr, key string) *validation.Errors {
	out := validation.New(e.Model)
	for _, x := range e.List() {
		if x.Attr == attr && x.Key == key {
			continue
		}
		if x.Message != "" {
			out.AddMessage(x.Attr, x.Message)
		} else {
			out.Add(x.Attr, x.Key, mapVars(x.Vars)...)
		}
	}
	return out
}

func rails2s(v any) string {
	switch x := v.(type) {
	case bool:
		if x {
			return "true"
		}
		return "false"
	case nil:
		return ""
	}
	return httpx.ValueString(v)
}

// moveWikiChildren は handle_children_move（子ページも同じ Wiki へ移す。保存できなければ親を外す）。
func (a *App) moveWikiChildren(c *Req, tx *db.Tx, page *domain.WikiPage, oldWikiID int64, createRedirect bool, now time.Time) error {
	children, err := repository.WikiPageChildPages(c.Ctx(), tx, page.ID)
	if err != nil {
		return err
	}
	for _, ch := range children {
		taken, err := repository.WikiTitleTaken(c.Ctx(), tx, page.WikiID, ch.Title, ch.ID)
		if err != nil {
			return err
		}
		if taken {
			if err := repository.SetWikiPageParent(c.Ctx(), tx, ch.ID, nil); err != nil {
				return err
			}
			continue
		}
		if err := repository.WikiHandleRename(c.Ctx(), tx, ch.WikiID, ch.Title, page.WikiID, ch.Title, createRedirect, now); err != nil {
			return err
		}
		ch.WikiID = page.WikiID
		if err := repository.UpdateWikiPageAttrs(c.Ctx(), tx, ch); err != nil {
			return err
		}
		if err := a.moveWikiChildren(c, tx, ch, oldWikiID, createRedirect, now); err != nil {
			return err
		}
	}
	return nil
}

// wikiOptionsForSelect は wiki_page_wiki_options_for_select(page)。
func (a *App) wikiOptionsForSelect(c *Req, page *domain.WikiPage) ([]helper.WikiProjectOption, error) {
	cond, err := c.Authz().AllowedToCondition(c.Ctx(), "rename_wiki_pages", authzOpts(), nil)
	if err != nil {
		return nil, err
	}
	projects, wikis, err := repository.ProjectsWithWiki(c.Ctx(), a.DB, cond)
	if err != nil {
		return nil, err
	}
	found := false
	for _, p := range projects {
		if p.ID == page.Project.ID {
			found = true
		}
	}
	if !found {
		projects = append(projects, page.Project)
		if w, err := repository.FindWiki(c.Ctx(), a.DB, page.Project.ID); err == nil {
			wikis[page.Project.ID] = w.ID
		}
	}
	if err := repository.SortProjectsByTree(c.Ctx(), a.DB, projects); err != nil {
		return nil, err
	}
	levels, err := projectTreeLevels(c, projects)
	if err != nil {
		return nil, err
	}
	out := make([]helper.WikiProjectOption, len(projects))
	for i, p := range projects {
		out[i] = helper.WikiProjectOption{Project: p, WikiID: wikis[p.ID], Level: levels[i], Selected: wikis[p.ID] == page.WikiID}
	}
	return out, nil
}

// projectTreeLevels は project_tree の level（並び済みの projects の中での祖先の数）。
func projectTreeLevels(c *Req, projects []*domain.Project) ([]int, error) {
	ns, err := repository.ProjectNestedSet(c.Ctx(), c.App.DB)
	if err != nil {
		return nil, err
	}
	levels := make([]int, len(projects))
	var stack []repository.NestedSetValue
	for i, p := range projects {
		v := ns[p.ID]
		for len(stack) > 0 && !(stack[len(stack)-1].Lft < v.Lft && v.Rgt < stack[len(stack)-1].Rgt) {
			stack = stack[:len(stack)-1]
		}
		levels[i] = len(stack)
		stack = append(stack, v)
	}
	return levels, nil
}

// ---------------------------------------------------------------- protect / history / diff / annotate

// WikiProtect は wiki#protect。
func (a *App) WikiProtect(c *Req) {
	page := c.wikiState().Page
	v := strings.ToLower(c.Params().String("protected"))
	protected := !(v == "" || v == "0" || v == "f" || v == "false" || v == "off")
	if err := repository.SetWikiPageProtected(c.Ctx(), a.DB, page.ID, protected); err != nil {
		a.wikiError(c, err)
		return
	}
	c.Redirect(wikiPagePath(c.Project, page.Title))
}

// WikiHistory は wiki#history（版の一覧。差分用のラジオボタン付き）。
func (a *App) WikiHistory(c *Req) {
	page := c.wikiState().Page
	count, err := repository.WikiVersionsCount(c.Ctx(), a.DB, page.ID)
	if err != nil {
		a.wikiError(c, err)
		return
	}
	pg := pagination.New(count, c.PerPageOption(), c.Params().String("page"))
	versions, err := repository.WikiVersionsPage(c.Ctx(), a.DB, page.ID, pg.PerPage+1, pg.Offset())
	if err != nil {
		a.wikiError(c, err)
		return
	}
	data, err := a.wikiPageData(c, page)
	if err != nil {
		a.wikiError(c, err)
		return
	}
	data["Versions"] = versions
	data["VersionCount"] = count
	data["VersionPages"] = pg
	data["ShowDiff"] = len(versions) > 1
	data["CanDelete"] = c.AllowedTo(domain.Perm("delete_wiki_pages"), c.wikiPageProject(page)) && count > 1
	opts := RenderOptions{}
	if httpx.IsXHR(c.R) {
		opts.Layout = view.NoLayout
	}
	c.Render("wiki/history", data, opts)
}

// WikiDiff は wiki#diff（WikiDiff の HTML 差分）。
func (a *App) WikiDiff(c *Req) {
	page := c.wikiState().Page
	to, from, err := a.wikiDiffContents(c, page, c.Params().String("version"), c.Params().String("version_from"))
	if err != nil {
		a.wikiError(c, err)
		return
	}
	if to == nil {
		c.Render404("")
		return
	}
	data, err := a.wikiPageData(c, page)
	if err != nil {
		a.wikiError(c, err)
		return
	}
	data["ContentTo"] = to
	data["ContentFrom"] = from
	data["DiffHTML"] = wikidiff.WordDiffHTML(to.Text, from.Text)
	c.Render("wiki/diff", data)
}

// wikiDiffContents は WikiPage#diff(version_to, version_from) の (content_to, content_from)。
func (a *App) wikiDiffContents(c *Req, page *domain.WikiPage, versionTo, versionFrom string) (*domain.WikiContentVersion, *domain.WikiContentVersion, error) {
	if page.CurrentVersion == 0 {
		return nil, nil, nil
	}
	vt := page.CurrentVersion
	if versionTo != "" {
		vt = int(pagination.RubyToI(versionTo))
	}
	to, err := repository.WikiPageVersion(c.Ctx(), a.DB, page.ID, vt)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var from *domain.WikiContentVersion
	if versionFrom != "" {
		from, err = repository.WikiPageVersion(c.Ctx(), a.DB, page.ID, int(pagination.RubyToI(versionFrom)))
	} else {
		from, err = repository.WikiPreviousVersion(c.Ctx(), a.DB, page.ID, to.Version)
	}
	if errors.Is(err, repository.ErrNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if from.Version > to.Version {
		to, from = from, to
	}
	return to, from, nil
}

// WikiAnnotateLine は annotate の 1 行（[version, author, text]）。
type WikiAnnotateLine struct {
	Num     int
	Version int
	Author  *domain.User
	// HasAuthor は author が分かっている（nil でない）。
	HasAuthor bool
	Text      string
	Color     int
	// First は直前の行と版が異なる（版・作者を表示する）。
	First bool
}

// WikiAnnotate は wiki#annotate。
func (a *App) WikiAnnotate(c *Req) {
	page := c.wikiState().Page
	if page.CurrentVersion == 0 {
		c.Render404("")
		return
	}
	vn := page.CurrentVersion
	if v := c.Params().String("version"); v != "" {
		vn = int(pagination.RubyToI(v))
	}
	all, err := repository.WikiAllVersions(c.Ctx(), a.DB, page.ID)
	if err != nil {
		a.wikiError(c, err)
		return
	}
	idx := -1
	for i, v := range all {
		if v.Version == vn {
			idx = i
		}
	}
	if idx < 0 {
		c.Render404("")
		return
	}
	toV := func(v *domain.WikiContentVersion) wikidiff.Version {
		var aid int64
		if v.AuthorID != nil {
			aid = *v.AuthorID
		}
		return wikidiff.Version{Version: v.Version, AuthorID: aid, Text: v.Text}
	}
	pos := map[int]int{}
	for i, v := range all {
		pos[v.Version] = i
	}
	authors := map[int64]*domain.User{}
	for _, v := range all {
		if v.AuthorID != nil && v.Author != nil {
			authors[*v.AuthorID] = v.Author
		}
	}
	lines := wikidiff.Annotate(toV(all[idx]), func(cur wikidiff.Version) (wikidiff.Version, bool) {
		i := pos[cur.Version]
		if i == 0 {
			return wikidiff.Version{}, false
		}
		return toV(all[i-1]), true
	})
	colors := map[int]int{}
	out := make([]WikiAnnotateLine, len(lines))
	prev := 0
	for i, l := range lines {
		col, ok := colors[l.Version]
		if !ok {
			col = len(colors) % 12
			colors[l.Version] = col
		}
		out[i] = WikiAnnotateLine{Num: i + 1, Version: l.Version, HasAuthor: l.HasAuthor && l.AuthorID != 0, Text: l.Text, Color: col, First: i == 0 || prev != l.Version}
		if l.HasAuthor && l.AuthorID != 0 {
			out[i].Author = authors[l.AuthorID]
			out[i].HasAuthor = out[i].Author != nil
		}
		prev = l.Version
	}
	data, err := a.wikiPageData(c, page)
	if err != nil {
		a.wikiError(c, err)
		return
	}
	data["AnnotateContent"] = all[idx]
	data["Lines"] = out
	c.Render("wiki/annotate", data)
}

// ---------------------------------------------------------------- destroy / destroy_version

// WikiDestroy は wiki#destroy（子ページの扱い: nullify / destroy / reassign）。
func (a *App) WikiDestroy(c *Req) {
	ws := c.wikiState()
	page := ws.Page
	if !c.wikiEditable(page) {
		c.Render403("")
		return
	}
	descendants, err := repository.WikiPageDescendantIDs(c.Ctx(), a.DB, page.ID)
	if err != nil {
		a.wikiError(c, err)
		return
	}
	var toDestroy []int64
	var reassignTo *domain.WikiPage
	if len(descendants) > 0 {
		switch c.Params().String("todo") {
		case "nullify":
		case "destroy":
			toDestroy = descendants
		case "reassign":
			p, err := repository.GetWikiPageByID(c.Ctx(), a.DB, ws.Wiki.ID, c.Params().Int("reassign_to_id"))
			if err != nil {
				// return unless reassign_to（描画は destroy テンプレートになる）
				a.renderWikiDestroy(c, page, len(descendants), descendants)
				return
			}
			reassignTo = p
		default:
			if !httpx.IsAPIRequest(c.R) {
				a.renderWikiDestroy(c, page, len(descendants), descendants)
				return
			}
		}
	}
	err = a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
		if reassignTo != nil {
			children, err := repository.WikiPageChildPages(c.Ctx(), tx, page.ID)
			if err != nil {
				return err
			}
			for _, ch := range children {
				if err := repository.SetWikiPageParent(c.Ctx(), tx, ch.ID, &reassignTo.ID); err != nil {
					return err
				}
			}
		}
		for _, id := range toDestroy {
			p, err := repository.GetWikiPageByID(c.Ctx(), tx, 0, id)
			if errors.Is(err, repository.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if err := a.destroyWikiPage(c, tx, p); err != nil {
				return err
			}
		}
		return a.destroyWikiPage(c, tx, page)
	})
	if err != nil {
		a.wikiError(c, err)
		return
	}
	if httpx.IsAPIRequest(c.R) {
		c.RenderAPIOK()
		return
	}
	c.Flash().SetNotice(c.L("notice_successful_delete"))
	c.Redirect(wikiIndexPath(c.Project))
}

// destroyWikiPage は WikiPage#destroy（before_destroy :delete_redirects、content・添付・ウォッチャーの削除、子は nullify）。
func (a *App) destroyWikiPage(c *Req, tx *db.Tx, page *domain.WikiPage) error {
	if err := repository.DeleteWikiRedirectsTo(c.Ctx(), tx, page.WikiID, page.Title); err != nil {
		return err
	}
	if err := a.deleteWikiAttachments(c, tx, page, nil); err != nil {
		return err
	}
	if err := repository.DeleteWatchers(c.Ctx(), tx, "wiki_page", []int64{page.ID}); err != nil {
		return err
	}
	if _, err := tx.Exec(c.Ctx(), `UPDATE wiki_pages SET parent_id = NULL WHERE parent_id = ?`, page.ID); err != nil {
		return err
	}
	return repository.DeleteWikiPageRow(c.Ctx(), tx, page.ID)
}

func (a *App) renderWikiDestroy(c *Req, page *domain.WikiPage, count int, descendants []int64) {
	data, err := a.wikiPageData(c, page)
	if err != nil {
		a.wikiError(c, err)
		return
	}
	pages, err := a.loadPagesForIndex(c)
	if err != nil {
		a.wikiError(c, err)
		return
	}
	exclude := map[int64]bool{page.ID: true}
	for _, id := range descendants {
		exclude[id] = true
	}
	var re []*domain.WikiPage
	for _, p := range pages {
		if !exclude[p.ID] {
			re = append(re, p)
		}
	}
	data["DescendantsCount"] = count
	data["Reassignable"] = &WikiPageOptions{Pages: re}
	data["HasReassignable"] = len(re) > 0
	c.Render("wiki/destroy", data)
}

// WikiDestroyVersion は wiki#destroy_version。
func (a *App) WikiDestroyVersion(c *Req) {
	page := c.wikiState().Page
	if !c.wikiEditable(page) {
		c.Render403("")
		return
	}
	vn := int(pagination.RubyToI(c.Params().String("version")))
	if _, err := repository.WikiPageVersion(c.Ctx(), a.DB, page.ID, vn); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.Render404("")
			return
		}
		a.wikiError(c, err)
		return
	}
	err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
		deleted, err := repository.DeleteWikiVersion(c.Ctx(), tx, page.ID, vn)
		if err != nil {
			return err
		}
		if deleted {
			return a.destroyWikiPage(c, tx, page)
		}
		return nil
	})
	if err != nil {
		a.wikiError(c, err)
		return
	}
	// redirect_to_referer_or history_project_wiki_page_path
	if ref := c.R.Referer(); ref != "" {
		c.Redirect(ref)
		return
	}
	c.Redirect(wikiPagePath(c.Project, page.Title) + "/history")
}

// ---------------------------------------------------------------- export / preview / add_attachment

// WikiExport は wiki#export（Wiki 全体の HTML。PDF は未実装）。
func (a *App) WikiExport(c *Req) {
	switch c.Params().String("format") {
	case "", "html":
	default:
		// TODO: format.pdf（wiki_pages_to_pdf）は未実装
		wikiNotAcceptable(c)
		c.Halt()
		return
	}
	pages, err := a.loadPagesForIndex(c)
	if err != nil {
		a.wikiError(c, err)
		return
	}
	type exported struct {
		Page   *domain.WikiPage
		Text   string
		Object any
	}
	var list []exported
	for _, p := range pages {
		e := exported{Page: p}
		if p.CurrentVersion > 0 {
			if v, err := repository.WikiPageVersion(c.Ctx(), a.DB, p.ID, p.CurrentVersion); err == nil {
				e.Text = v.Text
				e.Object, _ = a.wikiTextObject(c, p, v, true)
			}
		}
		list = append(list, e)
	}
	out, err := a.Views.Render(c.ViewContext(), "wiki/export_multiple", map[string]any{
		"Pages": list, "PagesTree": wikiPagesTree(pages), "ProjectName": c.Project.Name,
	}, view.RenderOptions{Layout: view.NoLayout})
	if err != nil {
		a.wikiError(c, err)
		return
	}
	sendDataNoName(c, out, "text/html", "wiki.html")
}

// wikiSendExport は show の format.html（export.html.erb を送る）。
func (a *App) wikiSendExport(c *Req, page *domain.WikiPage, content *domain.WikiContentVersion) {
	obj, err := a.wikiTextObject(c, page, content, content.Version == page.CurrentVersion)
	if err != nil {
		a.wikiError(c, err)
		return
	}
	out, err := a.Views.Render(c.ViewContext(), "wiki/export", map[string]any{
		"Page": page, "Content": content, "TextObject": obj,
	}, view.RenderOptions{Layout: view.NoLayout})
	if err != nil {
		a.wikiError(c, err)
		return
	}
	sendData(c, out, "text/html", page.Title+".html")
}

// WikiPreview は wiki#preview。
func (a *App) WikiPreview(c *Req) {
	ws := c.wikiState()
	page, _, err := a.findPage(c, ws.Wiki, c.Params().String("id"), true)
	if err != nil {
		a.wikiError(c, err)
		return
	}
	if page != nil && !c.wikiEditable(page) {
		c.Render403("")
		return
	}
	var previewed any
	if page != nil {
		atts, err := repository.ContainerAttachments(c.Ctx(), a.DB, "wiki_page", page.ID)
		if err != nil {
			a.wikiError(c, err)
			return
		}
		c.Attachments = append(c.Attachments, atts...)
		if page.CurrentVersion > 0 {
			if v, err := repository.WikiPageVersion(c.Ctx(), a.DB, page.ID, page.CurrentVersion); err == nil {
				previewed, _ = a.wikiTextObject(c, page, v, true)
			}
		}
	}
	data := map[string]any{"Attachments": c.Attachments, "Previewed": previewed}
	if cp := c.Params().Map("content"); c.Params().Present("content") && cp != nil {
		if v, ok := cp.Get("text"); ok && v != nil {
			data["Text"] = cp.String("text")
		}
	} else if v, ok := c.Params().Get("text"); ok && v != nil {
		data["Text"] = c.Params().String("text")
	}
	c.Render("common/_preview", data, RenderOptions{Layout: view.NoLayout})
}

// WikiAddAttachment は wiki#add_attachment。
func (a *App) WikiAddAttachment(c *Req) {
	page := c.wikiState().Page
	if !c.wikiEditable(page) {
		c.Render403("")
		return
	}
	a.wikiAttachFiles(c, page, c.Params().Map("attachments"))
	c.Redirect(wikiPagePath(c.Project, page.Title))
}
