package notify

// チケット以外のイベント通知（モデルの after_create_commit 等で Mailer.deliver_* を呼ぶ部分）。
// いずれもコミット後に呼ぶ。s が nil なら何もしない。エラーは記録だけする（deliver_later と同じく
// 通知の失敗で操作を失敗させない）。

import (
	"context"

	"github.com/mikuta0407/buropher/internal/domain"
)

func (s *Service) fire(ctx context.Context, what string, p Payload, recips []int64, err error) {
	if err != nil {
		s.logErr(what+": recipients", err)
		return
	}
	s.logErr(what, s.deliverToUsers(ctx, p, recips))
}

// NewsAdded は News#send_notification（Setting.notified_events に news_added があれば）。
func (s *Service) NewsAdded(ctx context.Context, newsID int64) {
	if s == nil || !s.eventEnabled(EventNewsAdded) {
		return
	}
	r, err := s.NewsRecipients(ctx, newsID)
	s.fire(ctx, "news_added", Payload{Kind: KindNewsAdded, Event: EventNewsAdded, NewsID: newsID}, r, err)
}

// NewsCommentAdded は Comment#send_notification（news_comment_added）。
func (s *Service) NewsCommentAdded(ctx context.Context, commentID int64) {
	if s == nil || !s.eventEnabled(EventNewsCommentAdded) {
		return
	}
	r, err := s.CommentRecipients(ctx, commentID)
	s.fire(ctx, "news_comment_added", Payload{Kind: KindNewsCommentAdded, Event: EventNewsCommentAdded, CommentID: commentID}, r, err)
}

// DocumentAdded は Document#send_notification（document_added。author は User.current）。
func (s *Service) DocumentAdded(ctx context.Context, documentID int64, author *domain.User) {
	if s == nil || !s.eventEnabled(EventDocumentAdded) {
		return
	}
	r, err := s.DocumentRecipients(ctx, documentID)
	p := Payload{Kind: KindDocumentAdded, Event: EventDocumentAdded, DocumentID: documentID}
	if author != nil && author.Logged() {
		p.AuthorID = author.ID
	}
	s.fire(ctx, "document_added", p, r, err)
}

// AttachmentsAdded は files#create / documents#add_attachment の Mailer.deliver_attachments_added
// （Setting.notified_events に file_added があれば。attachmentIDs は同じコンテナに追加した添付）。
func (s *Service) AttachmentsAdded(ctx context.Context, attachmentIDs []int64) {
	if s == nil || len(attachmentIDs) == 0 || !s.eventEnabled(EventFileAdded) {
		return
	}
	r, err := s.AttachmentsRecipients(ctx, attachmentIDs)
	s.fire(ctx, "attachments_added", Payload{Kind: KindAttachmentsAdded, Event: EventFileAdded, AttachmentIDs: attachmentIDs}, r, err)
}

// MessagePosted は Message#send_notification（message_posted）。
func (s *Service) MessagePosted(ctx context.Context, messageID int64) {
	if s == nil || !s.eventEnabled(EventMessagePosted) {
		return
	}
	r, err := s.MessageRecipients(ctx, messageID)
	s.fire(ctx, "message_posted", Payload{Kind: KindMessagePosted, Event: EventMessagePosted, MessageID: messageID}, r, err)
}

// WikiContentAdded は WikiContent#send_notification_create（wiki_content_added）。
// author は User.current（メンションの可視性判定）。
func (s *Service) WikiContentAdded(ctx context.Context, pageID int64, version int, author *domain.User) {
	if s == nil || !s.eventEnabled(EventWikiContentAdded) {
		return
	}
	r, err := s.WikiRecipients(ctx, pageID, version, false, author, nil)
	s.fire(ctx, "wiki_content_added", Payload{Kind: KindWikiContentAdded, Event: EventWikiContentAdded, WikiPageID: pageID, WikiVersion: version}, r, err)
}

// WikiContentUpdated は WikiContent#send_notification_update（wiki_content_updated。本文が変わった場合のみ呼ぶ）。
// oldText は更新前の本文（新たなメンションの判定に使う）。
func (s *Service) WikiContentUpdated(ctx context.Context, pageID int64, version int, author *domain.User, oldText string) {
	if s == nil || !s.eventEnabled(EventWikiContentUpdated) {
		return
	}
	r, err := s.WikiRecipients(ctx, pageID, version, true, author, &oldText)
	s.fire(ctx, "wiki_content_updated", Payload{Kind: KindWikiContentUpdated, Event: EventWikiContentUpdated, WikiPageID: pageID, WikiVersion: version}, r, err)
}
