package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/apibuilder"
	"github.com/mikuta0407/buropher/internal/attachments"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/validation"
)

// このファイルはニュース・文書・ファイル・フォーラムのコントローラで共通に使う部品
// （フォームのモデル、添付の保存、通知フック、API の添付の出力）。

// contentForm は labelled_form_for に渡すモデルの共通部分（model_name / persisted? / errors）。
type contentForm struct {
	key  string
	id   int64
	errs *validation.Errors
	loc  *i18n.Localizer
}

func newContentForm(c *Req, key string, id int64) contentForm {
	return contentForm{key: key, id: id, errs: validation.New(key), loc: c.Loc}
}

// ParamKey は model_name.param_key。
func (f *contentForm) ParamKey() string { return f.key }

// Persisted は persisted?。
func (f *contentForm) Persisted() bool { return f.id != 0 }

// ToParam は to_param。
func (f *contentForm) ToParam() string { return strconv.FormatInt(f.id, 10) }

// ErrorsOn は errors[attr]。
func (f *contentForm) ErrorsOn(attr string) []string { return f.errs.Messages(f.loc, attr) }

// HumanAttributeName は human_attribute_name(attr)。
func (f *contentForm) HumanAttributeName(attr string) string { return f.errs.HumanAttributeName(f.loc, attr) }

// ValidationErrors は errors（error_messages_for に渡す）。
func (f *contentForm) ValidationErrors() *validation.Errors { return f.errs }

// Notifier はメール通知のフック（Mailer.deliver_*）。メール送信は未実装のため既定は何もしない。
// 通知機能を実装するときに App.Notify を差し替える。
type Notifier interface {
	// Notify は event（news_added / news_comment_added / document_added / file_added / message_posted /
	// attachments_added）と対象（*domain.News など）を受け取る。
	Notify(c *Req, event string, obj any)
}

// notify は Setting.notified_events に event が含まれていれば通知フックを呼ぶ。
func (a *App) notify(c *Req, event string, obj any) {
	if contentNotifier == nil {
		return
	}
	for _, e := range a.Settings.Strings("notified_events") {
		if e == event {
			contentNotifier.Notify(c, event, obj)
			return
		}
	}
}

// contentNotifier は通知フックの実装（nil なら通知しない）。SetContentNotifier で設定する。
var contentNotifier Notifier

// SetContentNotifier はニュース・文書・ファイル・フォーラムのメール通知フックを設定する。
func SetContentNotifier(n Notifier) { contentNotifier = n }

// saveContainerAttachments は container.save_attachments(params) をコンテナ保存前に行う
// （Attachment.create はコンテナのトランザクションの外で行われる）。
func (a *App) saveContainerAttachments(c *Req, params any) (*attachments.SaveResult, error) {
	return a.AttachmentStore.SaveAttachments(c.Ctx(), a.DB, params, c.User, c.Loc)
}

// paramAttachments は params[:attachments] の値（無ければ nil）。
func paramAttachments(c *Req) any {
	v, ok := c.Params().Get("attachments")
	if !ok {
		return nil
	}
	return v
}

// deleteAttachmentsAfterCommit はコンテナ削除のトランザクション後にファイルを消す（after_commit :delete_from_disk）。
func (a *App) deleteAttachmentsAfterCommit(c *Req, atts []*domain.Attachment) {
	if len(atts) == 0 {
		return
	}
	if err := a.AttachmentStore.DeleteFromDisk(c.Ctx(), a.DB, atts...); err != nil {
		a.logger().Error("delete attachments from disk", "err", err)
	}
}

// withTx はトランザクション内で fn を実行する。
func (a *App) withTx(c *Req, fn func(tx *db.Tx) error) error { return a.DB.WithTx(c.Ctx(), fn) }

// notFoundOr500 は ErrNotFound なら 404、それ以外は 500 を描画する。
func (a *App) notFoundOr500(c *Req, what string, err error) {
	if errors.Is(err, repository.ErrNotFound) {
		c.Render404("")
		return
	}
	a.internalError(c, what, err)
}

// paramInt64 は params[name] の整数（Rails の find(params[:id]) と同じく先頭の数字を使う。無効なら 0, false）。
func paramInt64(c *Req, name string) (int64, bool) {
	s := strings.TrimSpace(c.Params().String(name))
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// renderAPIAttachmentAttributes は AttachmentsHelper#render_api_attachment_attributes。
func renderAPIAttachmentAttributes(c *Req, b apibuilder.Builder, att *domain.Attachment) {
	base := httpx.RequestBaseURL(c.R)
	b.Value("id", att.ID)
	b.Value("filename", att.Filename)
	b.Value("filesize", att.Filesize)
	b.Value("content_type", nilIfEmptyString(att.ContentType))
	b.Value("description", nilIfEmptyString(att.Description))
	b.Value("content_url", base+downloadNamedAttachmentPath(att))
	if att.Thumbnailable() {
		b.Value("thumbnail_url", base+"/attachments/thumbnail/"+strconv.FormatInt(att.ID, 10))
	}
	if att.Author != nil {
		b.Attrs("author", apibuilder.A("id", att.Author.ID, "name", c.Page().UserName(att.Author)))
	}
	b.Value("created_on", att.CreatedOn)
}

// renderAPIAttachment は AttachmentsHelper#render_api_attachment。
func renderAPIAttachment(c *Req, b apibuilder.Builder, att *domain.Attachment) {
	b.Object("attachment", func() { renderAPIAttachmentAttributes(c, b, att) })
}

// downloadNamedAttachmentPath は download_named_attachment_path(attachment, attachment.filename)。
func downloadNamedAttachmentPath(att *domain.Attachment) string {
	return "/attachments/download/" + strconv.FormatInt(att.ID, 10) + "/" + escapePathSegment(att.Filename)
}

// namedAttachmentPath は named_attachment_path(attachment, attachment.filename)。
func namedAttachmentPath(att *domain.Attachment) string {
	return "/attachments/" + strconv.FormatInt(att.ID, 10) + "/" + escapePathSegment(att.Filename)
}

func nilIfEmptyString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// respondNotAcceptable は respond_to に該当する形式が無い場合（ActionController::UnknownFormat → 406）。
func respondNotAcceptable(c *Req) {
	httpx.Head(c.W, c.R, http.StatusNotAcceptable)
	c.Halt()
}
