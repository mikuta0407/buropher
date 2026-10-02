package handler

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/apibuilder"
	"github.com/mikuta0407/buropher/internal/attachments"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/mimetype"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
)

// AttachmentsController（app/controllers/attachments_controller.rb）。
//
// 実装済み: upload（POST /uploads）、download（GET /attachments/download/:id(/:filename)）。
// TODO: show / thumbnail / update / destroy / edit_all / update_all / download_all。
var AttachmentsController = &Controller{Name: "attachments", MainMenu: true}

// routesAttachments は attachments コントローラのルートを登録する。
func (a *App) routesAttachments(r Router) {
	// get 'attachments/download/:id/:filename', :to => 'attachments#download', :id => /\d+/, :filename => /.*/,
	//   :as => 'download_named_attachment', :format => 'html'
	// filename は "/" や拡張子を含みうるので catch-all で受け、format は html に固定する（拡張子を format とみなさない）。
	a.Handle(r.With(forceFormat("html")), http.MethodGet, "/attachments/download/{id:[0-9]+}/*", AttachmentsController, "download",
		a.AttachmentsDownload, AcceptAPIAuth())
	// get 'attachments/download/:id', :to => 'attachments#download', :id => /\d+/
	a.Handle(r, http.MethodGet, "/attachments/download/{id:[0-9]+}", AttachmentsController, "download",
		a.AttachmentsDownload, AcceptAPIAuth())
	// match 'uploads', :to => 'attachments#upload', :via => :post
	// before_action :authorize_global, :only => :upload / accept_api_auth :upload
	a.Handle(r, http.MethodPost, "/uploads", AttachmentsController, "upload", a.AttachmentsUpload,
		AcceptAPIAuth(), AuthorizeGlobal())
}

// forceFormat はルートの :format => 'html' 指定（パスの拡張子や Accept によらず request.format を固定する）。
func forceFormat(format string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, httpx.WithFormats(r, format))
		})
	}
}

// AttachmentsUpload は attachments#upload（生の本文を application/octet-stream で受け取り、未紐付けの添付を作る）。
func (a *App) AttachmentsUpload(c *Req) {
	// API の利用者が Content-Type を指定するように（Rails が本文をパラメータとして解析しないように）
	if httpx.MediaType(c.R) != "application/octet-stream" {
		httpx.Head(c.W, c.R, http.StatusNotAcceptable)
		c.Halt()
		return
	}
	filename := c.Params().String("filename")
	if strings.TrimSpace(filename) == "" {
		filename = attachments.RandomHex(16)
	}
	contentType := c.Params().String("content_type")
	if strings.TrimSpace(contentType) == "" {
		contentType = ""
	}
	size := c.R.ContentLength
	if size < 0 {
		size = -1
	}
	att, errs, err := a.AttachmentStore.Create(c.Ctx(), a.DB, attachments.Upload{
		Filename: filename, ContentType: contentType, Body: c.R.Body, Size: size,
	}, c.User, c.Loc)
	if err != nil {
		a.logger().Error("upload attachment", "err", err)
		c.renderInternalError()
		c.Halt()
		return
	}

	switch httpx.Negotiate(c.R, "js", "xml", "json") {
	case "js":
		data := map[string]any{
			"AttachmentID": c.Params().String("attachment_id"),
			"NewRecord":    att.NewRecord(),
		}
		if att.NewRecord() {
			data["ErrorMessages"] = strings.Join(errs.FullMessages(c.Loc), ", ")
		} else {
			data["Token"] = att.Token()
			data["AttachmentPath"] = attachmentPathJS(att, c.Params())
		}
		c.Render("attachments/upload", data, RenderOptions{Format: "js"})
	case "xml", "json":
		if att.NewRecord() {
			c.RenderValidationErrors(errs)
			return
		}
		// app/views/attachments/upload.api.rsb
		c.RenderAPI(http.StatusCreated, func(b apibuilder.Builder) {
			b.Object("upload", func() {
				b.Value("id", att.ID)
				b.Value("token", att.Token())
			})
		})
	default:
		httpx.Head(c.W, c.R, http.StatusNotAcceptable)
		c.Halt()
	}
}

// attachmentPathJS は attachment_path(@attachment, :attachment_id => params[:attachment_id], :format => 'js')。
func attachmentPathJS(att *domain.Attachment, p *httpx.Params) string {
	path := "/attachments/" + strconv.FormatInt(att.ID, 10) + ".js"
	if v, ok := p.Get("attachment_id"); ok && v != nil {
		path += "?attachment_id=" + url.QueryEscape(httpx.ValueString(v))
	}
	return path
}

// findAttachment は AttachmentsController#find_attachment（URL のファイル名が違えば 404。@project を設定）。
func (a *App) findAttachment(c *Req) *domain.Attachment {
	id, err := strconv.ParseInt(chi.URLParam(c.R, "id"), 10, 64)
	if err != nil {
		c.Render404("")
		return nil
	}
	att, err := repository.GetAttachment(c.Ctx(), a.DB, id)
	if err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			a.logger().Error("find attachment", "id", id, "err", err)
		}
		c.Render404("")
		return nil
	}
	if filename, ok := c.Params().Get("*"); ok && httpx.ValueString(filename) != "" && httpx.ValueString(filename) != att.Filename {
		c.Render404("")
		return nil
	}
	if att.ContainerID != nil {
		if pid, err := repository.AttachmentContainerProject(c.Ctx(), a.DB, att.ContainerKind, *att.ContainerID); err == nil {
			if p, err := repository.GetProject(c.Ctx(), a.DB, pid); err == nil {
				c.Project = p
			}
		}
	}
	return att
}

// attachmentContainerPerms は acts_as_attachable の view_permission と、コンテナの visible? が見る権限。
var attachmentContainerPerms = map[string][2]string{
	// kind: {visible? の権限, attachable_options[:view_permission]}
	domain.AttachmentContainerProject:  {"view_project", "view_files"},
	domain.AttachmentContainerVersion:  {"view_issues", "view_files"},
	domain.AttachmentContainerNews:     {"view_news", "view_news"},
	domain.AttachmentContainerDocument: {"view_documents", "view_documents"},
	domain.AttachmentContainerWikiPage: {"view_wiki_pages", "view_wiki_pages"},
	domain.AttachmentContainerMessage:  {"view_messages", "view_messages"},
	domain.AttachmentContainerIssue:    {"", "view_issues"},
}

// AttachmentVisible は Attachment#visible?(User.current)（未紐付けなら作成者本人のみ、
// 紐付け済みなら container.attachments_visible?）。
// TODO: custom_value（CustomValue#attachments_visible?）は未対応で常に false。
func (c *Req) AttachmentVisible(att *domain.Attachment) bool {
	if att.ContainerID == nil {
		return att.AuthorID == c.User.ID
	}
	perms, ok := attachmentContainerPerms[att.ContainerKind]
	if !ok {
		return false
	}
	pid, err := repository.AttachmentContainerProject(c.Ctx(), c.App.DB, att.ContainerKind, *att.ContainerID)
	if err != nil {
		return false
	}
	p, err := repository.GetProject(c.Ctx(), c.App.DB, pid)
	if err != nil {
		return false
	}
	if att.ContainerKind == domain.AttachmentContainerIssue {
		// Issue#visible?
		is, err := redmine.NewDBStore(c.Ctx(), c.App.DB, c.Authz()).VisibleIssue(*att.ContainerID)
		if err != nil || is == nil {
			return false
		}
	} else if !c.AllowedTo(domain.Perm(perms[0]), p) {
		return false
	}
	return c.AllowedTo(domain.Perm(perms[1]), p)
}

// detectContentType は AttachmentsController#detect_content_type。
func detectContentType(att *domain.Attachment, isThumb bool) string {
	ct := att.ContentType
	if strings.TrimSpace(ct) == "" || ct == "application/octet-stream" {
		ct = mimetype.Of(att.Filename)
		if ct == "" {
			ct = "application/octet-stream"
		}
	}
	if isThumb && ct == "application/pdf" {
		ct = "image/png"
	}
	return ct
}

// attachmentDisposition は AttachmentsController#disposition（PDF は inline）。
func attachmentDisposition(att *domain.Attachment) string {
	if att.IsPDF() {
		return "inline"
	}
	return "attachment"
}

// ContentDisposition は ActionDispatch::Http::ContentDisposition.format（httpx.ContentDisposition）。
func ContentDisposition(disposition, filename string) string {
	return httpx.ContentDisposition(disposition, filename)
}

// AttachmentsDownload は attachments#download（find_attachment → file_readable → read_authorize → send_file）。
func (a *App) AttachmentsDownload(c *Req) {
	att := a.findAttachment(c)
	if att == nil {
		return
	}
	// file_readable
	if !a.AttachmentStore.Readable(att) {
		a.logger().Error(fmt.Sprintf("Cannot send attachment, %s does not exist or is unreadable.", a.AttachmentStore.Diskfile(att)))
		c.Render404("")
		return
	}
	// read_authorize
	if !c.AttachmentVisible(att) {
		c.DenyAccess()
		return
	}
	if att.ContainerKind == domain.AttachmentContainerVersion || att.ContainerKind == domain.AttachmentContainerProject {
		if err := repository.IncrementAttachmentDownloads(c.Ctx(), a.DB, att.ID); err != nil {
			a.logger().Error("increment download", "id", att.ID, "err", err)
		}
	}
	c.Halt()
	// stale?(:etag => @attachment.digest, :template => false)
	sum := md5.Sum([]byte(att.Digest))
	etag := `W/"` + hex.EncodeToString(sum[:]) + `"`
	h := c.W.Header()
	h.Set("ETag", etag)
	if etagMatches(c.R.Header.Get("If-None-Match"), etag) {
		c.W.WriteHeader(http.StatusNotModified)
		return
	}
	f, err := a.AttachmentStore.Open(att)
	if err != nil {
		c.Render404("")
		return
	}
	defer f.Close()
	// send_file（filename_for_content_disposition はファイル名をそのまま返す）
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	h.Set("Content-Type", detectContentType(att, false))
	h.Set("Content-Disposition", httpx.ContentDisposition(attachmentDisposition(att), att.Filename))
	h.Set("Content-Transfer-Encoding", "binary")
	if st, err := f.Stat(); err == nil {
		h.Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	}
	c.W.WriteHeader(http.StatusOK)
	if c.R.Method != http.MethodHead {
		_, _ = io.Copy(c.W, f)
	}
}

// etagMatches は If-None-Match に etag が含まれるか（弱い比較。"*" は常に一致）。
func etagMatches(header, etag string) bool {
	if header == "" {
		return false
	}
	norm := strings.TrimPrefix(etag, "W/")
	for _, t := range strings.Split(header, ",") {
		t = strings.TrimSpace(t)
		if t == "*" || strings.TrimPrefix(t, "W/") == norm {
			return true
		}
	}
	return false
}

// AttachFilesWarning は render_attachment_warning_if_needed(obj)（未保存の添付があれば flash[:warning]）。
func (c *Req) AttachFilesWarning(res *attachments.SaveResult) {
	if msg := res.WarningNotSaved(c.Loc); msg != "" {
		c.Flash().SetWarning(msg)
	}
}
