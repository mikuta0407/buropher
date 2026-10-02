package handler

import (
	"archive/zip"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/apibuilder"
	"github.com/mikuta0407/buropher/internal/attachments"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/mimetype"
	"github.com/mikuta0407/buropher/internal/pagination"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
	"github.com/mikuta0407/buropher/internal/unifieddiff"
	"github.com/mikuta0407/buropher/internal/validation"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは AttachmentsController のうち attachments.go（upload / download）以外のアクション:
// show / thumbnail / update / destroy / edit_all / update_all / download_all。

// routesAttachmentsMore は attachments コントローラの残りのルートを登録する。
//
//	get 'attachments/:id/:filename', :to => 'attachments#show', :id => /\d+/, :filename => /.*/, :format => 'html'
//	get 'attachments/thumbnail/:id(/:size)', :to => 'attachments#thumbnail', :id => /\d+/, :size => /\d+/
//	resources :attachments, :only => [:show, :update, :destroy]
//	get 'attachments/:object_type/:object_id/edit', :to => 'attachments#edit_all'
//	patch 'attachments/:object_type/:object_id', :to => 'attachments#update_all'
//	get 'attachments/:object_type/:object_id/download', :to => 'attachments#download_all'
func (a *App) routesAttachmentsMore(r Router) {
	at := AttachmentsController
	find := Before(a.findAttachmentFilter)
	readable := Before(a.attachmentFileReadable)
	readAuth := Before(func(c *Req) {
		if !c.AttachmentVisible(c.attachment()) {
			c.DenyAccess()
		}
	})
	a.Handle(r.With(forceFormat("html"), routeFormat("html")), http.MethodGet, "/attachments/{id:[0-9]+}/*", at, "show", a.AttachmentsShow,
		find, readable, readAuth, AcceptAPIAuth())
	a.Handle(r, http.MethodGet, "/attachments/thumbnail/{id:[0-9]+}", at, "thumbnail", a.AttachmentsThumbnail,
		find, readable, readAuth, AcceptAPIAuth())
	a.Handle(r, http.MethodGet, "/attachments/thumbnail/{id:[0-9]+}/{size:[0-9]+}", at, "thumbnail", a.AttachmentsThumbnail,
		find, readable, readAuth, AcceptAPIAuth())
	a.Handle(r, http.MethodGet, "/attachments/{id:[0-9]+}", at, "show", a.AttachmentsShow, find, readable, readAuth, AcceptAPIAuth())
	updateAuth := Before(func(c *Req) {
		if !a.attachmentPermitted(c, c.attachment(), "edit") {
			c.DenyAccess()
		}
	})
	deleteAuth := Before(func(c *Req) {
		if !a.attachmentPermitted(c, c.attachment(), "delete") {
			c.DenyAccess()
		}
	})
	a.Handle(r, http.MethodPatch, "/attachments/{id:[0-9]+}", at, "update", a.AttachmentsUpdate, find, updateAuth, AcceptAPIAuth())
	a.Handle(r, http.MethodPut, "/attachments/{id:[0-9]+}", at, "update", a.AttachmentsUpdate, find, updateAuth, AcceptAPIAuth())
	a.Handle(r, http.MethodDelete, "/attachments/{id:[0-9]+}", at, "destroy", a.AttachmentsDestroy, find, deleteAuth, AcceptAPIAuth())
	// Redmine::Acts::Attachable::ObjectTypeConstraint
	for _, ot := range []string{"issues", "versions", "news", "messages", "wiki_pages", "projects", "documents", "journals"} {
		ot := ot
		set := Before(func(c *Req) { c.setValue(objectTypeCtxKey{}, ot) })
		base := "/attachments/" + ot + "/{object_id}"
		a.Handle(r, http.MethodGet, base+"/edit", at, "edit_all", a.AttachmentsEditAll,
			set, Before(a.findAttachmentContainer), Before(a.findEditableAttachments))
		a.Handle(r, http.MethodPatch, base, at, "update_all", a.AttachmentsUpdateAll,
			set, Before(a.findAttachmentContainer), Before(a.findEditableAttachments))
		a.Handle(r, http.MethodGet, base+"/download", at, "download_all", a.AttachmentsDownloadAll,
			set, Before(a.findAttachmentContainer), Before(a.findDownloadableAttachments))
	}
}

func init() {
	// current_menu_item: 添付・コンテナが無ければ nil
	AttachmentsController.MenuItem = func(string) string { return helper.NoMenuItem }
}

// routeFormat はルートの :format 固定を記録する（helper.WithRouteFormat）。
func routeFormat(format string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, helper.WithRouteFormat(r, format))
		})
	}
}

type attachmentCtxKey struct{}

func (c *Req) attachment() *domain.Attachment {
	att, _ := c.value(attachmentCtxKey{}).(*domain.Attachment)
	return att
}

// findAttachmentFilter は find_attachment（@project の設定と current_menu_item の選択を含む）。
func (a *App) findAttachmentFilter(c *Req) {
	id, ok := paramInt64(c, "id")
	if !ok {
		c.Render404("")
		return
	}
	att, err := repository.GetAttachment(c.Ctx(), a.DB, id)
	if err != nil {
		a.notFoundOr500(c, "find attachment", err)
		return
	}
	// @attachment は設定済み（current_menu_item はコンテナで決まる）で、@project の設定前に 404
	if att.ContainerID != nil {
		c.setAttachmentMenuItem(attachmentContainerMenuItem(att.ContainerKind))
	}
	if fn, ok := c.Params().Get("*"); ok && httpx.ValueString(fn) != "" && httpx.ValueString(fn) != att.Filename {
		c.Render404("")
		return
	}
	if att.ContainerID != nil {
		if pid, err := repository.AttachmentContainerProject(c.Ctx(), a.DB, att.ContainerKind, *att.ContainerID); err == nil {
			if p, err := repository.GetProject(c.Ctx(), a.DB, pid); err == nil {
				c.Project = p
			}
		}
	}
	c.setValue(attachmentCtxKey{}, att)
}

// setAttachmentMenuItem は AttachmentsController#current_menu_item（コンテナの種類で決まる）。
func (c *Req) setAttachmentMenuItem(item string) {
	if item == "" {
		return
	}
	ctrl := *c.Controller
	ctrl.MenuItem = func(string) string { return item }
	c.Controller = &ctrl
}

// attachmentContainerMenuItem は current_menu_item（WikiPage → wiki, Message → boards, Project / Version → files、
// それ以外はクラス名の複数形）。
func attachmentContainerMenuItem(kind string) string {
	switch kind {
	case domain.AttachmentContainerWikiPage:
		return "wiki"
	case domain.AttachmentContainerMessage:
		return "boards"
	case domain.AttachmentContainerProject, domain.AttachmentContainerVersion:
		return "files"
	case domain.AttachmentContainerNews:
		return "news"
	case domain.AttachmentContainerIssue:
		return "issues"
	case domain.AttachmentContainerDocument:
		return "documents"
	}
	return ""
}

// attachmentFileReadable は file_readable（ファイルが無ければ 404）。
func (a *App) attachmentFileReadable(c *Req) {
	att := c.attachment()
	if !a.AttachmentStore.Readable(att) {
		a.logger().Error(fmt.Sprintf("Cannot send attachment, %s does not exist or is unreadable.", a.AttachmentStore.Diskfile(att)))
		c.Render404("")
	}
}

// attachmentPermitted は Attachment#editable? / deletable?（未紐付けなら作成者本人、
// 紐付け済みなら container.attachments_editable? / attachments_deletable?）。action は edit / delete。
func (a *App) attachmentPermitted(c *Req, att *domain.Attachment, action string) bool {
	if att.ContainerID == nil {
		return att.AuthorID == c.User.ID
	}
	ct, err := a.loadAttachmentContainer(c, att.ContainerKind, *att.ContainerID)
	if err != nil {
		return false
	}
	return a.containerAttachmentsPermitted(c, ct, action)
}

// attContainer は添付のコンテナ（acts_as_attachable のモデル）。
type attContainer struct {
	Kind    string
	ID      int64
	Project *domain.Project
	// Protected は wiki_page の protected?（attachments_deletable? の editable_by?）。
	Protected bool
	// Title / Path はリンク表示用（link_to_attachment_container）。
	Link template.HTML
}

// ObjectType は container.class.name.underscore.pluralize（container_attachments_path の object_type）。
func (ct *attContainer) ObjectType() string {
	switch ct.Kind {
	case domain.AttachmentContainerNews:
		return "news"
	}
	return ct.Kind + "s"
}

// loadAttachmentContainer はコンテナを読み込む（無ければ repository.ErrNotFound）。
func (a *App) loadAttachmentContainer(c *Req, kind string, id int64) (*attContainer, error) {
	pid, err := repository.AttachmentContainerProject(c.Ctx(), a.DB, kind, id)
	if err != nil {
		return nil, err
	}
	p, err := repository.GetProject(c.Ctx(), a.DB, pid)
	if err != nil {
		return nil, err
	}
	ct := &attContainer{Kind: kind, ID: id, Project: p}
	if kind == domain.AttachmentContainerWikiPage {
		if page, err := repository.GetWikiPageByID(c.Ctx(), a.DB, 0, id); err == nil {
			ct.Protected = page.Protected
		}
	}
	return ct, nil
}

// containerAttachmentPerms は acts_as_attachable の view / edit / delete 権限。
var containerAttachmentPerms = map[string][3]string{
	domain.AttachmentContainerProject:  {"view_files", "manage_files", "manage_files"},
	domain.AttachmentContainerVersion:  {"view_files", "manage_files", "manage_files"},
	domain.AttachmentContainerNews:     {"view_news", "manage_news", "manage_news"},
	domain.AttachmentContainerDocument: {"view_documents", "edit_documents", "delete_documents"},
	domain.AttachmentContainerWikiPage: {"view_wiki_pages", "edit_wiki_pages", "delete_wiki_pages_attachments"},
	domain.AttachmentContainerMessage:  {"view_messages", "edit_messages", "edit_messages"},
}

// containerVisible は container.visible?。
func (a *App) containerVisible(c *Req, ct *attContainer) bool {
	switch ct.Kind {
	case domain.AttachmentContainerIssue:
		is, err := redmine.NewDBStore(c.Ctx(), a.DB, c.Authz()).VisibleIssue(ct.ID)
		return err == nil && is != nil
	case domain.AttachmentContainerProject:
		return c.AllowedTo(domain.Perm("view_project"), ct.Project)
	case domain.AttachmentContainerVersion:
		return c.AllowedTo(domain.Perm("view_issues"), ct.Project)
	case domain.AttachmentContainerNews:
		return c.AllowedTo(domain.Perm("view_news"), ct.Project)
	case domain.AttachmentContainerDocument:
		return c.AllowedTo(domain.Perm("view_documents"), ct.Project)
	case domain.AttachmentContainerWikiPage:
		return c.AllowedTo(domain.Perm("view_wiki_pages"), ct.Project)
	case domain.AttachmentContainerMessage:
		return c.AllowedTo(domain.Perm("view_messages"), ct.Project)
	}
	return false
}

// containerAttachmentsPermitted は attachments_editable? / attachments_deletable?（action は edit / delete）。
func (a *App) containerAttachmentsPermitted(c *Req, ct *attContainer, action string) bool {
	if ct.Kind == domain.AttachmentContainerIssue {
		env := issues.NewEnv(a.DB, a.Settings, c.User)
		iss, err := env.Load(c.Ctx(), ct.ID)
		if err != nil {
			return false
		}
		ok, err := env.AttachmentsEditable(c.Ctx(), iss, c.User)
		return err == nil && ok
	}
	perms, ok := containerAttachmentPerms[ct.Kind]
	if !ok || !a.containerVisible(c, ct) {
		return false
	}
	perm := perms[1]
	if action == "delete" {
		perm = perms[2]
		// WikiPage#attachments_deletable?: editable_by?(usr) && super
		if ct.Kind == domain.AttachmentContainerWikiPage && ct.Protected && !c.AllowedTo(domain.Perm("protect_wiki_pages"), ct.Project) {
			return false
		}
	}
	return c.AllowedTo(domain.Perm(perm), ct.Project)
}

// containerLink は link_to_attachment_container(container)。
func (a *App) containerLink(c *Req, ct *attContainer) template.HTML {
	id := strconv.FormatInt(ct.ID, 10)
	switch ct.Kind {
	case domain.AttachmentContainerProject, domain.AttachmentContainerVersion:
		return rails.LinkTo(c.L("project_module_files"), "/projects/"+ct.Project.Identifier+"/files", nil)
	case domain.AttachmentContainerIssue:
		st := redmine.NewDBStore(c.Ctx(), a.DB, c.Authz())
		is, err := st.VisibleIssue(ct.ID)
		if err != nil || is == nil {
			return ""
		}
		return a.Helpers.WikiRenderer(c.Page()).LinkToIssue(is, redmine.LinkToIssueOptions{NoSubject: true})
	case domain.AttachmentContainerNews:
		if n, err := repository.GetNews(c.Ctx(), a.DB, ct.ID); err == nil {
			return rails.LinkTo(n.Title, "/news/"+id, nil)
		}
	case domain.AttachmentContainerDocument:
		if d, err := repository.GetDocument(c.Ctx(), a.DB, ct.ID); err == nil {
			return rails.LinkTo(d.Title, "/documents/"+id, nil)
		}
	case domain.AttachmentContainerMessage:
		if m, err := repository.GetMessage(c.Ctx(), a.DB, ct.ID); err == nil {
			return rails.LinkTo(rails.StringTruncate(m.Subject, 60, "...", nil), boardMessagePath(m), nil)
		}
	case domain.AttachmentContainerWikiPage:
		if page, err := repository.GetWikiPageByID(c.Ctx(), a.DB, 0, ct.ID); err == nil {
			return rails.LinkTo(domain.WikiPrettyTitle(page.Title), wikiPagePath(ct.Project, page.Title), nil)
		}
	}
	return ""
}

// boardMessagePath は link_to_message の URL。
func boardMessagePath(m *domain.Message) string {
	u := "/boards/" + itoa(m.BoardID) + "/topics/" + itoa(m.RootID())
	if m.ParentID != nil {
		u += "?r=" + itoa(m.ID) + "#message-" + itoa(m.ID)
	}
	return u
}

// ---------------------------------------------------------------- show

// AttachmentsShow は attachments#show（HTML はファイルの種類に応じたプレビュー、API は添付の情報）。
func (a *App) AttachmentsShow(c *Req) {
	att := c.attachment()
	switch httpx.Negotiate(c.R, "html", "xml", "json") {
	case "xml", "json":
		c.RenderAPI(0, func(b apibuilder.Builder) { renderAPIAttachment(c, b, att) })
		return
	case "html":
	default:
		respondNotAcceptable(c)
		return
	}
	data := map[string]any{"Attachment": att}
	if att.ContainerID != nil {
		ct, err := a.loadAttachmentContainer(c, att.ContainerKind, *att.ContainerID)
		if err == nil {
			data["ContainerLink"] = a.containerLink(c, ct)
		}
		list, err := repository.ContainerAttachmentList(c.Ctx(), a.DB, att.ContainerKind, *att.ContainerID)
		if err != nil {
			a.internalError(c, "container attachments", err)
			return
		}
		for i, x := range list {
			if x.ID == att.ID {
				data["Paginator"] = pagination.New(len(list), 1, i+1)
				data["PageAttachments"] = list
			}
		}
	}
	switch {
	case att.IsDiff():
		raw, err := os.ReadFile(a.AttachmentStore.Diskfile(att))
		if err != nil {
			c.Render404("")
			return
		}
		// params[:type] || User.current.pref[:diff_type] || 'inline'
		prefType := ""
		if c.User.Logged() {
			if v, err := repository.UserPrefExtra(c.Ctx(), a.DB, c.User.ID, "diff_type"); err == nil && v != nil {
				prefType = httpx.ValueString(v)
			}
		}
		diffType := c.Params().String("type")
		if diffType == "" {
			diffType = prefType
		}
		if diffType != "inline" && diffType != "sbs" {
			diffType = "inline"
		}
		if c.User.Logged() && diffType != prefType {
			if err := repository.SetUserPrefExtra(c.Ctx(), a.DB, c.User.ID, "diff_type", diffType); err != nil {
				a.logger().Error("save diff type", "err", err)
			}
		}
		data["Kind"] = "diff"
		data["DiffType"] = diffType
		data["Diff"] = string(raw)
	case att.IsText() && att.Filesize <= int64(a.Settings.Int("file_max_size_displayed"))*1024:
		raw, err := os.ReadFile(a.AttachmentStore.Diskfile(att))
		if err != nil {
			c.Render404("")
			return
		}
		content := toUTF8BySetting(raw)
		switch {
		case att.IsMarkdown():
			data["Kind"] = "markup"
			data["Markup"] = a.markupToHTML(c, "common_mark", content)
		case att.IsTextile():
			data["Kind"] = "markup"
			data["Markup"] = a.markupToHTML(c, "textile", content)
		default:
			data["Kind"] = "file"
			data["Content"] = content
		}
	case att.IsImageType():
		data["Kind"] = "image"
	default:
		data["Kind"] = "other"
		switch {
		case att.IsVideo():
			data["MediaKind"] = "video"
		case att.IsAudio():
			data["MediaKind"] = "audio"
		}
		data["DownloadURL"] = httpx.RequestBaseURL(c.R) + downloadNamedAttachmentPath(att)
	}
	c.Render("attachments/show", data)
}

// toUTF8BySetting は Redmine::CodesetUtil.to_utf8_by_setting（UTF-8 として不正なバイトは "?" に置き換える）。
// TODO: Setting.repositories_encodings による変換。
func toUTF8BySetting(b []byte) string {
	return unifieddiff.ReplaceInvalidUTF8(string(b))
}

// markupToHTML は Redmine::WikiFormatting.to_html(format, text)（マクロ・リンクの解決なし）。
func (a *App) markupToHTML(c *Req, format, text string) template.HTML {
	r := *a.Helpers.WikiRenderer(c.Page())
	r.TextFormatting = format
	return template.HTML(r.ToHTML(text))
}

// ---------------------------------------------------------------- thumbnail

// AttachmentsThumbnail は attachments#thumbnail。
func (a *App) AttachmentsThumbnail(c *Req) {
	att := c.attachment()
	size, _ := strconv.Atoi(c.Params().String("size"))
	path, ok := a.AttachmentStore.Thumbnail(att, size)
	if !ok {
		httpx.Head(c.W, c.R, http.StatusNotFound)
		c.Halt()
		return
	}
	c.Halt()
	etag := weakETag(path)
	h := c.W.Header()
	h.Set("ETag", etag)
	if etagMatches(c.R.Header.Get("If-None-Match"), etag) {
		c.W.WriteHeader(http.StatusNotModified)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		httpx.Head(c.W, c.R, http.StatusNotFound)
		return
	}
	defer f.Close()
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	h.Set("Content-Type", detectContentType(att, true))
	h.Set("Content-Disposition", httpx.ContentDisposition("attachment", att.Filename))
	h.Set("Content-Transfer-Encoding", "binary")
	if st, err := f.Stat(); err == nil {
		h.Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	}
	c.W.WriteHeader(http.StatusOK)
	if c.R.Method != http.MethodHead {
		_, _ = io.Copy(c.W, f)
	}
}

// ---------------------------------------------------------------- update / destroy

// AttachmentsUpdate は attachments#update（API のみ。filename / content_type / description）。
func (a *App) AttachmentsUpdate(c *Req) {
	att := c.attachment()
	if !httpx.IsAPIRequest(c.R) {
		respondNotAcceptable(c)
		return
	}
	filenameChanged := false
	if p := c.Params().Map("attachment"); p != nil {
		if v, ok := p.StringOK("filename"); ok {
			if san := attachments.SanitizeFilename(v); san != att.Filename {
				att.Filename = san
				filenameChanged = true
			}
		}
		if v, ok := p.StringOK("content_type"); ok {
			att.ContentType = v
		}
		if v, ok := p.StringOK("description"); ok {
			att.Description = v
		}
	}
	errs := a.AttachmentStore.Validate(att, -1, filenameChanged, c.Loc)
	if errs.Any() {
		c.RenderValidationErrors(errs)
		return
	}
	if err := repository.UpdateAttachment(c.Ctx(), a.DB, att); err != nil {
		a.internalError(c, "update attachment", err)
		return
	}
	c.RenderAPIOK()
}

// AttachmentsDestroy は attachments#destroy（チケットの添付ならジャーナルに記録する）。
func (a *App) AttachmentsDestroy(c *Req) {
	att := c.attachment()
	var jres *issues.SaveResult
	err := a.withTx(c, func(tx *db.Tx) error {
		if att.ContainerKind == domain.AttachmentContainerIssue && att.ContainerID != nil {
			// container.init_journal(User.current); container.attachments.delete(@attachment)
			// （Issue#attachment_removed がジャーナルに記録して保存する）
			env := issues.NewEnv(tx, a.Settings, c.User)
			env.Now = a.now
			iss, err := env.Load(c.Ctx(), *att.ContainerID)
			if err != nil {
				return err
			}
			j, err := env.InitJournal(c.Ctx(), iss, c.User, "")
			if err != nil {
				return err
			}
			if err := repository.DeleteAttachment(c.Ctx(), tx, att.ID); err != nil {
				return err
			}
			j.JournalizeAttachment(att.ID, att.Filename, false)
			_, jres, err = env.SaveJournal(c.Ctx(), iss, j)
			return err
		}
		return repository.DeleteAttachment(c.Ctx(), tx, att.ID)
	})
	if err != nil {
		a.internalError(c, "destroy attachment", err)
		return
	}
	a.deleteAttachmentsAfterCommit(c, []*domain.Attachment{att})
	// Journal の after_create_commit :send_notification（添付の削除も issue_updated の通知になる）
	a.dispatchIssueNotifications(c, jres)
	switch httpx.Negotiate(c.R, "html", "js", "xml", "json") {
	case "html":
		def := "/"
		if c.Project != nil {
			def = "/projects/" + c.Project.Identifier
		}
		if ref := c.R.Header.Get("Referer"); ref != "" {
			c.Redirect(ref)
			return
		}
		c.Redirect(def)
	case "js":
		c.Render("attachments/destroy", map[string]any{"AttachmentID": c.Params().String("attachment_id")}, RenderOptions{Format: "js"})
	case "xml", "json":
		c.RenderAPIOK()
	default:
		respondNotAcceptable(c)
	}
}

// ---------------------------------------------------------------- edit_all / update_all / download_all

type attachmentContainerCtxKey struct{}

// attachmentsState は find_container / find_editable_attachments の結果。
type attachmentsState struct {
	Container   *attContainer
	Attachments []*domain.Attachment
}

func (c *Req) attachmentsState() *attachmentsState {
	s, _ := c.value(attachmentContainerCtxKey{}).(*attachmentsState)
	if s == nil {
		s = &attachmentsState{}
	}
	return s
}

// objectTypeKinds は object_type（複数形）→ container_kind。
var objectTypeKinds = map[string]string{
	"issues": domain.AttachmentContainerIssue, "versions": domain.AttachmentContainerVersion,
	"news": domain.AttachmentContainerNews, "messages": domain.AttachmentContainerMessage,
	"wiki_pages": domain.AttachmentContainerWikiPage, "projects": domain.AttachmentContainerProject,
	"documents": domain.AttachmentContainerDocument,
}

// objectTypeCtxKey はルートで決まる params[:object_type]（Params は都度組み立てられるため context に持つ）。
type objectTypeCtxKey struct{}

// objectTypeParam は params[:object_type]（ルートで固定されていればその値）。
func (c *Req) objectTypeParam() string {
	if s, ok := c.value(objectTypeCtxKey{}).(string); ok {
		return s
	}
	return c.Params().String("object_type")
}

// findAttachmentContainer は find_container（見えなければ 403、無ければ 404）。
func (a *App) findAttachmentContainer(c *Req) {
	kind, ok := objectTypeKinds[c.objectTypeParam()]
	id, okID := paramInt64(c, "object_id")
	if !ok || !okID {
		// journals は Journal#attachments（添付の作成元）として扱わない
		c.Render404("")
		return
	}
	ct, err := a.loadAttachmentContainer(c, kind, id)
	if err != nil {
		a.notFoundOr500(c, "find container", err)
		return
	}
	if !a.containerVisible(c, ct) {
		c.Render403("")
		return
	}
	c.Project = ct.Project
	c.setAttachmentMenuItem(attachmentContainerMenuItem(kind))
	c.setValue(attachmentContainerCtxKey{}, &attachmentsState{Container: ct})
}

// findEditableAttachments は find_editable_attachments（編集できる添付が無ければ 404）。
func (a *App) findEditableAttachments(c *Req) {
	st := c.attachmentsState()
	list, err := repository.ContainerAttachmentList(c.Ctx(), a.DB, st.Container.Kind, st.Container.ID)
	if err != nil {
		a.internalError(c, "container attachments", err)
		return
	}
	if !a.containerAttachmentsPermitted(c, st.Container, "edit") {
		list = nil
	}
	if len(list) == 0 {
		c.Render404("")
		return
	}
	st.Attachments = list
}

// findDownloadableAttachments は find_downloadable_attachments（合計サイズの上限を超えれば戻る）。
func (a *App) findDownloadableAttachments(c *Req) {
	st := c.attachmentsState()
	list, err := repository.ContainerAttachmentList(c.Ctx(), a.DB, st.Container.Kind, st.Container.ID)
	if err != nil {
		a.internalError(c, "container attachments", err)
		return
	}
	var readable []*domain.Attachment
	var total int64
	for _, att := range list {
		// attachments.select(&:readable?)
		if a.AttachmentStore.Readable(att) {
			readable = append(readable, att)
			total += att.Filesize
		}
	}
	max := int64(a.Settings.Int("bulk_download_max_size")) * 1024
	if total > max {
		c.Flash().SetError(c.L("error_bulk_download_size_too_big", map[string]any{"max_size": c.Loc.NumberToHumanSize(max)}))
		c.RedirectBackOrDefault(a.containerURL(c, st.Container), true)
		return
	}
	st.Attachments = readable
}

// containerURL は AttachmentsController#container_url。
func (a *App) containerURL(c *Req, ct *attContainer) string {
	base := httpx.RequestBaseURL(c.R)
	id := strconv.FormatInt(ct.ID, 10)
	switch ct.Kind {
	case domain.AttachmentContainerMessage:
		if m, err := repository.GetMessage(c.Ctx(), a.DB, ct.ID); err == nil {
			return base + boardMessagePath(m)
		}
	case domain.AttachmentContainerProject, domain.AttachmentContainerVersion:
		return base + "/projects/" + ct.Project.Identifier + "/files"
	case domain.AttachmentContainerWikiPage:
		if page, err := repository.GetWikiPageByID(c.Ctx(), a.DB, 0, ct.ID); err == nil {
			return base + wikiPagePath(ct.Project, page.Title)
		}
	case domain.AttachmentContainerIssue:
		return base + "/issues/" + id
	case domain.AttachmentContainerNews:
		return base + "/news/" + id
	case domain.AttachmentContainerDocument:
		return base + "/documents/" + id
	}
	return base + "/"
}

// attachmentEdit は edit_all の 1 行（filename_was と入力値・エラー）。
type attachmentEdit struct {
	*domain.Attachment
	FilenameWas string
	errs        *validation.Errors
}

// AttachmentsEditAll は attachments#edit_all。
func (a *App) AttachmentsEditAll(c *Req) {
	st := c.attachmentsState()
	edits := make([]*attachmentEdit, len(st.Attachments))
	for i, att := range st.Attachments {
		edits[i] = &attachmentEdit{Attachment: att, FilenameWas: att.Filename}
	}
	a.renderEditAll(c, st, edits)
}

func (a *App) renderEditAll(c *Req, st *attachmentsState, edits []*attachmentEdit) {
	var errs []string
	for _, e := range edits {
		if e.errs != nil {
			errs = append(errs, e.errs.FullMessages(c.Loc)...)
		}
	}
	c.Render("attachments/edit_all", map[string]any{
		"Container":     st.Container,
		"ContainerLink": a.containerLink(c, st.Container),
		"Edits":         edits,
		"Errors":        errs,
	})
}

// AttachmentsUpdateAll は attachments#update_all（Attachment.update_attachments）。
func (a *App) AttachmentsUpdateAll(c *Req) {
	st := c.attachmentsState()
	params := c.Params().Map("attachments")
	if params == nil {
		// params.require(:attachments) → ActionController::ParameterMissing（400）
		httpx.HeadAs(c.W, c.R, http.StatusBadRequest, "html")
		c.Halt()
		return
	}
	edits := make([]*attachmentEdit, len(st.Attachments))
	saved := true
	for i, att := range st.Attachments {
		e := &attachmentEdit{Attachment: att, FilenameWas: att.Filename}
		edits[i] = e
		p := params.Map(strconv.FormatInt(att.ID, 10))
		if p == nil {
			continue
		}
		changed := false
		if v, ok := p.StringOK("filename"); ok {
			if san := attachments.SanitizeFilename(v); san != att.Filename {
				att.Filename = san
				changed = true
			}
		}
		if v, ok := p.StringOK("description"); ok {
			att.Description = v
		}
		// saved &&= attachment.save（最初の失敗以降は保存しない）
		if saved {
			if verrs := a.AttachmentStore.Validate(att, -1, changed, c.Loc); verrs.Any() {
				e.errs = verrs
				saved = false
			}
		}
	}
	if saved {
		err := a.withTx(c, func(tx *db.Tx) error {
			for _, att := range st.Attachments {
				if err := repository.UpdateAttachment(c.Ctx(), tx, att); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			a.internalError(c, "update attachments", err)
			return
		}
		c.RedirectBackOrDefault("/", false)
		return
	}
	a.renderEditAll(c, st, edits)
}

// AttachmentsDownloadAll は attachments#download_all（ZIP にまとめて送る）。
func (a *App) AttachmentsDownloadAll(c *Req) {
	st := c.attachmentsState()
	if len(st.Attachments) == 0 {
		c.Render404("")
		return
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	used := map[string]bool{}
	for _, att := range st.Attachments {
		name := att.Filename
		for n := 1; used[name]; n++ {
			ext := filepath.Ext(att.Filename)
			name = strings.TrimSuffix(att.Filename, ext) + "(" + strconv.Itoa(n) + ")" + ext
		}
		used[name] = true
		w, err := zw.Create(name)
		if err != nil {
			a.internalError(c, "zip", err)
			return
		}
		f, err := a.AttachmentStore.Open(att)
		if err != nil {
			a.internalError(c, "zip open", err)
			return
		}
		_, err = io.Copy(w, f)
		f.Close()
		if err != nil {
			a.internalError(c, "zip copy", err)
			return
		}
	}
	if err := zw.Close(); err != nil {
		a.internalError(c, "zip", err)
		return
	}
	// "#{@container.class.to_s.downcase}-#{@container.id}-attachments.zip"
	cls := map[string]string{
		domain.AttachmentContainerWikiPage: "wikipage",
	}[st.Container.Kind]
	if cls == "" {
		cls = st.Container.Kind
	}
	name := cls + "-" + strconv.FormatInt(st.Container.ID, 10) + "-attachments.zip"
	sendData(c, buf.Bytes(), mimetype.Of(name), name)
	c.Halt()
}

// weakETag は stale?(:etag => value) の ETag（W/"md5(value)"）。
func weakETag(v string) string {
	sum := md5.Sum([]byte(v))
	return `W/"` + hex.EncodeToString(sum[:]) + `"`
}
