package handler

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/mikuta0407/buropher/internal/apibuilder"
	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/textformat/redmine"
)

// このファイルは WikiController の補助（textilizable の :object、添付、send_data）。

// wikiTextObject は textilizable の :object（WikiContent / WikiContentVersion）。
func (a *App) wikiTextObject(c *Req, page *domain.WikiPage, v *domain.WikiContentVersion, current bool) (*redmine.Object, error) {
	ref, err := repository.GetWikiPage(c.Ctx(), a.DB, page.ID)
	if err != nil {
		return nil, err
	}
	kind := "wiki_content"
	if !current {
		kind = "wiki_content_version"
	}
	return &redmine.Object{Kind: kind, ID: v.ID, Project: c.wikiPageProject(page), Page: ref}, nil
}

func authzOpts() authz.ConditionOptions { return authz.ConditionOptions{} }

// sendData は send_data(data, :type => type, :filename => filename_for_content_disposition(filename))。
func sendData(c *Req, data []byte, contentType, filename string) {
	h := c.W.Header()
	if contentType == "text/html" || contentType == "text/plain" {
		contentType += "; charset=utf-8"
	}
	h.Set("Content-Type", contentType)
	h.Set("Content-Disposition", ContentDisposition("attachment", filenameForContentDisposition(c, filename)))
	h.Set("Content-Transfer-Encoding", "binary")
	c.W.WriteHeader(http.StatusOK)
	_, _ = c.W.Write(data)
	c.Halt()
}

// sendDataNoName は filename_for_content_disposition を通さない send_data。
func sendDataNoName(c *Req, data []byte, contentType, filename string) {
	h := c.W.Header()
	h.Set("Content-Type", contentType+"; charset=utf-8")
	h.Set("Content-Disposition", ContentDisposition("attachment", filename))
	h.Set("Content-Transfer-Encoding", "binary")
	c.W.WriteHeader(http.StatusOK)
	_, _ = c.W.Write(data)
	c.Halt()
}

// filenameForContentDisposition は ApplicationController#filename_for_content_disposition
// （IE / Edge では URL エンコードする）。
func filenameForContentDisposition(c *Req, name string) string {
	ua := c.R.UserAgent()
	for _, s := range []string{"MSIE", "Trident", "Edge"} {
		if containsStr(ua, s) {
			return url.PathEscape(name)
		}
	}
	return name
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func queryEscape(s string) string { return url.QueryEscape(s) }

// wikiNotAcceptable は respond_to に一致する形式が無い場合（ActionController::UnknownFormat → 406、本文なし）。
func wikiNotAcceptable(c *Req) {
	c.W.Header().Set("Content-Type", "text/html; charset=utf-8")
	c.W.WriteHeader(http.StatusNotAcceptable)
	c.Halt()
}

// jsonString は to_json（Rails と同じく < > & を \u エスケープする）。
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// ---------------------------------------------------------------- 添付

// wikiAttachments は page.attachments（作成日時・id 順、author 込み）。
func (a *App) wikiAttachments(c *Req, page *domain.WikiPage) ([]*domain.Attachment, error) {
	if page.NewRecord() {
		return nil, nil
	}
	return repository.ContainerAttachmentList(c.Ctx(), a.DB, "wiki_page", page.ID)
}

// wikiAttachmentsDeletable は WikiPage#attachments_deletable?（編集でき、delete_wiki_pages_attachments 権限）。
func (a *App) wikiAttachmentsDeletable(c *Req, page *domain.WikiPage) bool {
	return c.wikiEditable(page) && c.AllowedTo(domain.Perm("delete_wiki_pages_attachments"), c.wikiPageProject(page))
}

// deleteWikiAttachments はページの添付を削除する（ids が nil ならすべて）。
func (a *App) deleteWikiAttachments(c *Req, tx *db.Tx, page *domain.WikiPage, ids []int64) error {
	if ids == nil {
		deleted, err := repository.DeleteContainerAttachments(c.Ctx(), tx, "wiki_page", []int64{page.ID})
		if err != nil {
			return err
		}
		if a.AttachmentStore != nil {
			return a.AttachmentStore.DeleteFromDisk(c.Ctx(), tx, deleted...)
		}
		return nil
	}
	atts, err := repository.ContainerAttachmentList(c.Ctx(), tx, "wiki_page", page.ID)
	if err != nil {
		return err
	}
	want := map[int64]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for _, at := range atts {
		if !want[at.ID] {
			continue
		}
		if err := repository.DeleteAttachment(c.Ctx(), tx, at.ID); err != nil {
			return err
		}
		if a.AttachmentStore != nil {
			if err := a.AttachmentStore.DeleteFromDisk(c.Ctx(), tx, at); err != nil {
				return err
			}
		}
	}
	return nil
}

// wikiAttachFiles は Attachment.attach_files(@page, params) と render_attachment_warning_if_needed。
func (a *App) wikiAttachFiles(c *Req, page *domain.WikiPage, params *httpx.Params) {
	if params == nil || a.AttachmentStore == nil {
		return
	}
	res, err := a.AttachmentStore.AttachFiles(c.Ctx(), a.DB, "wiki_page", page.ID, params, c.User, c.Loc)
	if err != nil {
		a.logger().Error("attach files", "err", err)
		return
	}
	if w := res.WarningNotSaved(c.Loc); w != "" {
		c.Flash().SetWarning(w)
	}
}

// renderAPIAttachment は render_api_attachment(attachment, api)。
func (a *App) renderAPIAttachment(c *Req, b apibuilder.Builder, at *domain.Attachment) {
	base := httpx.RequestBaseURL(c.R)
	b.Object("attachment", func() {
		b.Value("id", at.ID)
		b.Value("filename", at.Filename)
		b.Value("filesize", at.Filesize)
		b.Value("content_type", at.ContentType)
		b.Value("description", at.Description)
		b.Value("content_url", base+"/attachments/download/"+strconv.FormatInt(at.ID, 10)+"/"+escapePathSegment(at.Filename))
		if at.Thumbnailable() {
			b.Value("thumbnail_url", base+"/attachments/thumbnail/"+strconv.FormatInt(at.ID, 10))
		}
		if at.Author != nil {
			b.Attrs("author", apibuilder.A("id", at.Author.ID, "name", at.Author.Name(a.Settings.String("user_format"))))
		}
		b.Value("created_on", at.CreatedOn)
	})
}
