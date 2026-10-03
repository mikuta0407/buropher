// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

// Discord の DM（notify.Renderer#RenderDiscord）。メールと同じアクション（mailer の issueAdd 等）を
// Discord モードで実行して件名・本文データを作り、埋め込みメッセージにする。文言は受信者の言語
// （buropher.discord.* は web/locales/overlay）。

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/mail"
	"github.com/mikuta0407/buropher/internal/notify"
	"github.com/mikuta0407/buropher/internal/repository"
)

// 埋め込みの色（メールの未完了・完了バッジの色と、高優先度）。
const (
	discordColorOpen   = 0x205D86
	discordColorClosed = 0x1D781D
	discordColorHigh   = 0xC61A1A
	discordColorOther  = 0x169
)

// finishDiscord は Discord モードの Mailer#mail（自分の変更を通知しない設定なら送らない）。
func (m *mailer) finishDiscord(subject string) (*mail.Message, error) {
	if m.author != nil && m.author.Logged() && m.user != nil && m.author.ID == m.user.ID {
		ns, err := repository.GetNotificationSetting(m.ctx, m.a.DB, m.author.ID,
			m.a.Settings.String("default_notification_option"), m.a.Settings.Bool("default_users_no_self_notified"))
		if err != nil {
			return nil, err
		}
		if ns.NoSelfNotified {
			return nil, nil
		}
	}
	m.subject = subject
	return &mail.Message{Subject: subject}, nil
}

func dataString(d map[string]any, k string) string {
	s, _ := d[k].(string)
	return s
}

// RenderDiscord は notify.Renderer#RenderDiscord。
func (a *App) RenderDiscord(ctx context.Context, p *notify.Payload) (*notify.DiscordMessage, error) {
	if notify.IsAccountKind(p.Kind) {
		return nil, nil
	}
	m, err := a.newMailer(ctx, p)
	if m == nil || err != nil {
		return nil, err
	}
	m.discord = true
	msg, err := m.run()
	if msg == nil || err != nil {
		return nil, err
	}
	d := m.data
	dm := &notify.DiscordMessage{Title: m.subject, Color: discordColorOther}
	if m.author != nil {
		dm.Author = m.userName(m.author)
	}
	switch p.Kind {
	case notify.KindIssueAdd, notify.KindIssueEdit:
		ic := m.loadIssue(p.IssueID)
		if ic == nil {
			return nil, nil
		}
		dm.Title = m.issueSubjectPrefix(ic) + " " + ic.row.Subject
		dm.URL = dataString(d, "IssueURL")
		dm.Footer = ic.im.Project.Name
		dm.Color = discordColorOpen
		if ic.im.Status != nil && ic.im.Status.IsClosed {
			dm.Color = discordColorClosed
		} else if ic.im.Priority != nil && ic.im.Priority.PositionName != nil && strings.HasPrefix(*ic.im.Priority.PositionName, "high") {
			dm.Color = discordColorHigh
		}
		if p.Kind == notify.KindIssueAdd {
			dm.Content = dataString(d, "AddedText")
			dm.Description = ic.row.Description
			dm.Timestamp = ic.row.CreatedAt.UTC().Format(time.RFC3339)
			for _, item := range m.emailIssueAttributes(ic, false) {
				if i := strings.Index(item, ": "); i > 0 {
					dm.Fields = append(dm.Fields, notify.DiscordField{Name: item[:i], Value: item[i+2:], Inline: true})
				}
			}
		} else {
			dm.Content = dataString(d, "UpdatedText")
			if b, _ := d["PrivateNotes"].(bool); b {
				dm.Content = "(" + m.l("field_private_notes") + ") " + dm.Content
			}
			dm.Description = dataString(d, "Notes")
			if details, _ := d["DetailsText"].([]string); len(details) > 0 {
				dm.Fields = append(dm.Fields, notify.DiscordField{Name: m.l("buropher.discord.field_changes"),
					Value: "• " + strings.Join(details, "\n• ")})
			}
			if t, err := repository.JournalCreatedAt(ctx, a.DB, p.JournalID); err == nil {
				dm.Timestamp = t.UTC().Format(time.RFC3339)
			}
		}
	case notify.KindNewsAdded:
		dm.URL = dataString(d, "NewsURL")
		dm.Description = dataString(d, "Description")
	case notify.KindNewsCommentAdded:
		dm.URL = dataString(d, "NewsURL")
		dm.Content = dataString(d, "WroteText")
		dm.Description = dataString(d, "Comments")
	case notify.KindDocumentAdded:
		dm.URL = dataString(d, "DocumentURL")
		dm.Description = dataString(d, "Description")
	case notify.KindAttachmentsAdded:
		dm.URL = dataString(d, "AddedToURL")
		dm.Content = dataString(d, "AddedTo")
		if atts, _ := d["Attachments"].([]mailAttachment); len(atts) > 0 {
			var lines []string
			for _, at := range atts {
				lines = append(lines, "• "+at.Filename+" ("+at.Size+")")
			}
			dm.Description = strings.Join(lines, "\n")
		}
	case notify.KindMessagePosted:
		dm.URL = dataString(d, "MessageURL")
		dm.Description = dataString(d, "Content")
	case notify.KindWikiContentAdded, notify.KindWikiContentUpdated:
		dm.URL = dataString(d, "WikiURL")
		dm.Content = dataString(d, "BodyText")
		dm.Description = dataString(d, "Comments")
		if u := dataString(d, "DiffURL"); u != "" {
			dm.Fields = append(dm.Fields, notify.DiscordField{Name: m.l("label_view_diff"), Value: u})
		}
	case notify.KindReminder:
		dm.URL = dataString(d, "OpenIssuesURL")
		dm.Content = dataString(d, "BodyText")
		var lines []string
		if items, ok := d["ReminderLines"].([]string); ok {
			for _, s := range items {
				lines = append(lines, "• "+s)
			}
		}
		dm.Description = strings.Join(lines, "\n")
	default:
		return nil, nil
	}
	return dm, nil
}

// discordFallback は buropher 独自のメール: Discord の DM が恒久的に失敗し、通知をメールに切り替えたことを知らせる。
func (m *mailer) discordFallback() (*mail.Message, error) {
	m.data["Reason"] = m.p.Error
	m.data["AccountURL"] = m.url("/my/account")
	to, err := m.userMailTo()
	if err != nil {
		return nil, err
	}
	return m.finish(to, "["+m.a.Settings.String("app_title")+"] "+m.l("buropher.discord.mail_subject_fallback"), "discord_fallback")
}

// DiscordTestMessage はテスト DM の内容（受信者の言語）。
func (a *App) DiscordTestMessage(ctx context.Context, u *domain.User) *notify.DiscordMessage {
	c := a.newBackgroundReq(ctx, u, MailerController, "discord_test")
	return &notify.DiscordMessage{
		Title:       c.L("buropher.discord.test_message_title", a.Settings.String("app_title")),
		URL:         c.MailBaseURL + "/my/account",
		Description: c.L("buropher.discord.test_message_body", u.Login),
		Color:       discordColorOpen,
		Footer:      a.Settings.String("app_title"),
		Timestamp:   a.now().UTC().Format(time.RFC3339),
	}
}

var _ = strconv.Itoa
