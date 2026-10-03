// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package notify

// チャネルのジョブ処理（notify.email / notify.discord）。

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/mikuta0407/buropher/internal/discord"
	"github.com/mikuta0407/buropher/internal/jobs"
	"github.com/mikuta0407/buropher/internal/repository"
)

func (s *Service) markDelivery(ctx context.Context, id int64, status, recipient, msg string) {
	if id == 0 {
		return
	}
	if len(msg) > 2000 {
		msg = msg[:2000]
	}
	s.logErr("delivery log", repository.UpdateNotificationDelivery(ctx, s.DB, id, status, recipient, msg, s.now()))
}

// deliveryError は再試行するエラーを記録する（最後の試行なら failed）。
func (s *Service) deliveryError(ctx context.Context, j *jobs.Job, p *Payload, err error) {
	status := "pending"
	if j.Attempts >= j.MaxAttempts || jobs.IsPermanent(err) {
		status = "failed"
	}
	s.markDelivery(ctx, p.DeliveryID, status, "", err.Error())
}

// handleEmail は notify.email（描画して SMTP 等で送る）。
func (s *Service) handleEmail(ctx context.Context, j *jobs.Job) error {
	var p Payload
	if err := j.Decode(&p); err != nil {
		return jobs.Permanent(err)
	}
	if !s.MailEnabled() {
		s.markDelivery(ctx, p.DeliveryID, "skipped", "", ErrMailDisabled.Error())
		return nil
	}
	m, err := s.Renderer.RenderMail(ctx, &p)
	if err != nil {
		s.deliveryError(ctx, j, &p, err)
		return err
	}
	if m == nil {
		s.markDelivery(ctx, p.DeliveryID, "skipped", "", "")
		return nil
	}
	if err := s.Mail.Send(ctx, m); err != nil {
		s.deliveryError(ctx, j, &p, err)
		return err
	}
	s.markDelivery(ctx, p.DeliveryID, "sent", strings.Join(m.Recipients(), ", "), "")
	return nil
}

// handleDiscord は notify.discord（DM を送る。レート制限は延期、恒久エラーはメールに切り替える）。
func (s *Service) handleDiscord(ctx context.Context, j *jobs.Job) error {
	var p Payload
	if err := j.Decode(&p); err != nil {
		return jobs.Permanent(err)
	}
	cfg := s.DiscordConfig()
	if !cfg.Usable() {
		s.markDelivery(ctx, p.DeliveryID, "skipped", "", "discord is disabled")
		s.fallbackToEmail(ctx, p)
		return nil
	}
	ident, err := repository.GetDiscordIdentity(ctx, s.DB, p.UserID)
	if err != nil {
		return err
	}
	if ident == nil {
		s.markDelivery(ctx, p.DeliveryID, "skipped", "", "discord account is not linked")
		s.fallbackToEmail(ctx, p)
		return nil
	}
	msg, err := s.Renderer.RenderDiscord(ctx, &p)
	if err != nil {
		s.deliveryError(ctx, j, &p, err)
		return err
	}
	if msg == nil {
		s.markDelivery(ctx, p.DeliveryID, "skipped", ident.Subject, "")
		return nil
	}
	err = s.SendDM(ctx, cfg, p.UserID, ident.Subject, msg)
	var rl *discord.RateLimitError
	switch {
	case err == nil:
		s.logErr("discord", repository.MarkDiscordDMSuccess(ctx, s.DB, p.UserID, s.now()))
		s.markDelivery(ctx, p.DeliveryID, "sent", ident.Subject, "")
		return nil
	case errors.As(err, &rl):
		s.markDelivery(ctx, p.DeliveryID, "pending", ident.Subject, err.Error())
		return jobs.Retry(rl.RetryAfter, err)
	case discord.IsUnauthorized(err):
		// Bot トークンが無効（設定の誤り）。ユーザーの失敗としては数えず、メールで届ける
		s.markDelivery(ctx, p.DeliveryID, "failed", ident.Subject, err.Error())
		s.fallbackToEmail(ctx, p)
		return nil
	case discord.IsPermanent(err):
		n, ferr := repository.MarkDiscordDMFailure(ctx, s.DB, p.UserID, ident.Subject, err.Error(), s.now())
		s.logErr("discord", ferr)
		s.markDelivery(ctx, p.DeliveryID, "failed", ident.Subject, err.Error())
		s.fallbackToEmail(ctx, p)
		if n == s.failureThreshold() {
			// 以後の通知はメールに切り替わる（ChannelsFor）。本人に知らせる
			s.logErr("discord_fallback", s.enqueue(ctx, ChannelEmail, Payload{Kind: KindDiscordFallback, UserID: p.UserID, Error: err.Error()}))
		}
		return nil
	default:
		s.deliveryError(ctx, j, &p, err)
		return err
	}
}

// fallbackToEmail は Discord で送れなかった通知をメールで送る（メールも受け取る設定なら送らない）。
func (s *Service) fallbackToEmail(ctx context.Context, p Payload) {
	if IsAccountKind(p.Kind) || p.UserID == 0 {
		return
	}
	ns, err := repository.GetNotificationSetting(ctx, s.DB, p.UserID, s.Settings.String("default_notification_option"), s.Settings.Bool("default_users_no_self_notified"))
	if err != nil {
		s.logErr("fallback", err)
		return
	}
	pref := ns.Channels
	if !ns.Explicit {
		pref = s.Settings.String("buropher_default_notification_channel")
	}
	if pref == "both" {
		return
	}
	p.Fallback = true
	p.DeliveryID = 0
	s.logErr("fallback", s.enqueue(ctx, ChannelEmail, p))
}

// truncate は Discord の文字数制限（文字数）に合わせて切り詰める。
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// toDiscordMessage は描画結果を API の要求にする（各フィールドの文字数制限を守る）。
func toDiscordMessage(m *DiscordMessage) discord.Message {
	e := discord.Embed{
		Title:       truncate(m.Title, 256),
		URL:         m.URL,
		Description: truncate(m.Description, 4096),
		Color:       m.Color,
		Timestamp:   m.Timestamp,
	}
	for i, f := range m.Fields {
		if i >= 25 {
			break
		}
		name, value := truncate(f.Name, 256), truncate(f.Value, 1024)
		if strings.TrimSpace(name) == "" {
			name = "​"
		}
		if strings.TrimSpace(value) == "" {
			value = "​"
		}
		e.Fields = append(e.Fields, discord.EmbedField{Name: name, Value: value, Inline: f.Inline})
	}
	if m.Footer != "" {
		e.Footer = &discord.EmbedFooter{Text: truncate(m.Footer, 2048)}
	}
	if m.Author != "" {
		e.Author = &discord.EmbedAuthor{Name: truncate(m.Author, 256)}
	}
	return discord.Message{Content: truncate(m.Content, 2000), Embeds: []discord.Embed{e}}
}

// SendDM はユーザーに DM を送る（DM チャンネルは discord_dm_channels にキャッシュする。
// キャッシュしたチャンネルが無効なら作り直す）。
func (s *Service) SendDM(ctx context.Context, cfg DiscordConfig, userID int64, discordUserID string, m *DiscordMessage) error {
	if s.Discord == nil {
		return errors.New("discord client is not configured")
	}
	ch, err := repository.GetDiscordDMChannel(ctx, s.DB, userID)
	if err != nil {
		return err
	}
	channelID := ""
	if ch != nil && ch.DiscordUserID == discordUserID {
		channelID = ch.ChannelID
	}
	create := func() error {
		id, err := s.Discord.CreateDM(ctx, cfg.BotToken, discordUserID)
		if err != nil {
			return err
		}
		channelID = id
		return repository.SaveDiscordDMChannel(ctx, s.DB, userID, discordUserID, id, s.now())
	}
	if channelID == "" {
		if err := create(); err != nil {
			return err
		}
	}
	req := toDiscordMessage(m)
	_, err = s.Discord.SendMessage(ctx, cfg.BotToken, channelID, req)
	var ae *discord.APIError
	if errors.As(err, &ae) && ae.Code == discord.CodeUnknownChannel {
		if err := create(); err != nil {
			return err
		}
		_, err = s.Discord.SendMessage(ctx, cfg.BotToken, channelID, req)
	}
	return err
}
