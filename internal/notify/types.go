// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package notify は通知（Redmine の Mailer の deliver_* と Redmine::Notifiable）の配送層。
//
// 流れ（_planning/07_notifications.md）:
//
//	ドメインのイベント（チケット追加・更新、ニュース、文書、ファイル、フォーラム、Wiki、アカウント系）
//	  → 受信者の計算（Redmine 互換。チケットは internal/issues、それ以外は recipients.go）
//	  → 受信者ごとにチャネル（email / discord）を決定（user_notification_settings.channels、
//	    Discord は有効化・連携済み・恒久エラーで無効化されていない場合のみ。アカウント・セキュリティ系は常に email）
//	  → notification_deliveries に記録し、チャネルごとに jobs へ積む（notify.email / notify.discord）
//	  → ワーカーが描画（Renderer。受信者の言語・タイムゾーン・権限で internal/handler が描画）して送る。
//
// ハンドラからの呼び出し方は internal/handler/doc.go の「規約: 通知」を参照。
package notify

import (
	"context"

	"github.com/mikuta0407/buropher/internal/mail"
)

// メールの種類（Mailer のアクション名）。
const (
	KindIssueAdd                 = "issue_add"
	KindIssueEdit                = "issue_edit"
	KindDocumentAdded            = "document_added"
	KindAttachmentsAdded         = "attachments_added"
	KindNewsAdded                = "news_added"
	KindNewsCommentAdded         = "news_comment_added"
	KindMessagePosted            = "message_posted"
	KindWikiContentAdded         = "wiki_content_added"
	KindWikiContentUpdated       = "wiki_content_updated"
	KindAccountInformation       = "account_information"
	KindAccountActivationRequest = "account_activation_request"
	KindAccountActivated         = "account_activated"
	KindLostPassword             = "lost_password"
	KindRegister                 = "register"
	KindSecurityNotification     = "security_notification"
	KindSettingsUpdated          = "settings_updated"
	KindTestEmail                = "test_email"
	KindReminder                 = "reminder"
	// KindDiscordFallback は Discord の DM が恒久的に失敗してメールに切り替えたことの通知（buropher 独自）。
	KindDiscordFallback = "discord_fallback"
)

// Notifiable のイベント名（Setting.notified_events の値）。
const (
	EventIssueAdded         = "issue_added"
	EventIssueUpdated       = "issue_updated"
	EventNewsAdded          = "news_added"
	EventNewsCommentAdded   = "news_comment_added"
	EventDocumentAdded      = "document_added"
	EventFileAdded          = "file_added"
	EventMessagePosted      = "message_posted"
	EventWikiContentAdded   = "wiki_content_added"
	EventWikiContentUpdated = "wiki_content_updated"
)

// チャネル名。
const (
	ChannelEmail   = "email"
	ChannelDiscord = "discord"
)

// accountKinds はアカウント・セキュリティ系（常にメール。Discord には送らない）。
var accountKinds = map[string]bool{
	KindAccountInformation: true, KindAccountActivationRequest: true, KindAccountActivated: true,
	KindLostPassword: true, KindRegister: true, KindSecurityNotification: true, KindSettingsUpdated: true,
	KindTestEmail: true, KindDiscordFallback: true,
}

// IsAccountKind はアカウント・セキュリティ系のメールか（常にメールで送る）。
func IsAccountKind(kind string) bool { return accountKinds[kind] }

// Payload は 1 受信者・1 チャネル分の通知（ジョブの payload。Mailer のアクション引数に相当）。
// 描画は配送時に行う（Redmine の deliver_later と同じく、配送時点の DB の内容で描画する）。
type Payload struct {
	Kind string `json:"kind"`
	// Event は Notifiable のイベント名（記録用。アカウント系は Kind と同じ）。
	Event string `json:"event,omitempty"`
	// UserID は受信者（Mailer の第 1 引数の User）。
	UserID int64 `json:"user_id,omitempty"`
	// Addresses は User ではなくアドレスで宛先を指定する場合（lost_password の recipient、
	// account_information / account_activated / register は user.mail）。
	Addresses []string `json:"addresses,omitempty"`
	// ExtraRecipients は security_notification の options[:recipients]。
	ExtraRecipients []string `json:"extra_recipients,omitempty"`
	// DeliveryID は notification_deliveries.id。
	DeliveryID int64 `json:"delivery_id,omitempty"`

	IssueID       int64   `json:"issue_id,omitempty"`
	JournalID     int64   `json:"journal_id,omitempty"`
	NewsID        int64   `json:"news_id,omitempty"`
	CommentID     int64   `json:"comment_id,omitempty"`
	DocumentID    int64   `json:"document_id,omitempty"`
	MessageID     int64   `json:"message_id,omitempty"`
	WikiPageID    int64   `json:"wiki_page_id,omitempty"`
	WikiVersion   int     `json:"wiki_version,omitempty"`
	AttachmentIDs []int64 `json:"attachment_ids,omitempty"`
	// AuthorID は document_added の author（User.current）。
	AuthorID int64 `json:"author_id,omitempty"`
	// SenderID / RemoteIP は security_notification・settings_updated の sender と remote_ip。
	SenderID int64  `json:"sender_id,omitempty"`
	RemoteIP string `json:"remote_ip,omitempty"`
	// Password は account_information のパスワード（secretbox で暗号化して保存する）。
	Password string `json:"password,omitempty"`
	// Token は lost_password / register のトークン値。
	Token string `json:"token,omitempty"`
	// security_notification の options（Message・Field・Title は i18n キー、URL は絶対 URL かパス）。
	Message string `json:"message,omitempty"`
	Field   string `json:"field,omitempty"`
	Value   string `json:"value,omitempty"`
	Title   string `json:"title,omitempty"`
	URL     string `json:"url,omitempty"`
	// Changes は settings_updated の設定名。
	Changes []string `json:"changes,omitempty"`
	// Days / IssueIDs は reminder。
	Days     int     `json:"days,omitempty"`
	IssueIDs []int64 `json:"issue_ids,omitempty"`
	// Fallback は Discord から切り替えたメール（記録用）。
	Fallback bool `json:"fallback,omitempty"`
	// Error は discord_fallback の失敗理由。
	Error string `json:"error,omitempty"`
}

// DiscordField は埋め込みのフィールド。
type DiscordField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

// DiscordMessage は DM 1 通（埋め込み 1 つ）。
type DiscordMessage struct {
	Content     string
	Title       string
	URL         string
	Description string
	Color       int
	Fields      []DiscordField
	Footer      string
	Author      string
	Timestamp   string // ISO 8601
}

// Renderer は通知を受信者の言語・タイムゾーン・権限で描画する（internal/handler が実装する）。
type Renderer interface {
	// RenderMail はメールを描画する。送るべきでない場合（宛先が空・対象が削除済み）は nil を返す。
	RenderMail(ctx context.Context, p *Payload) (*mail.Message, error)
	// RenderDiscord は Discord の DM を描画する。送るべきでない場合は nil を返す。
	RenderDiscord(ctx context.Context, p *Payload) (*DiscordMessage, error)
}
