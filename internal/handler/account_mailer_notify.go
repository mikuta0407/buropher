// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

// AccountMailer を通知の配送層（internal/notify）で実装する。アカウント系のメールは notify 側で常に
// メールとして積まれ、本文は mailer_account.go（Redmine の Mailer と同じテンプレート）で作る。

import (
	"context"
	"net/url"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/notify"
)

// NotifyAccountMailer は notify.Service へ委譲する AccountMailer（server がメール配送の設定時に App.Mailer に設定する）。
type NotifyAccountMailer struct {
	Service *notify.Service
}

// tokenOf は再設定・有効化の URL から token パラメータを取り出す（メール本文の URL は notify 側で
// Setting.protocol / host_name とトークンから組み立てる。Redmine の url_for と同じ）。
func tokenOf(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return p.Query().Get("token")
}

// LostPassword は Mailer.deliver_lost_password(user, token, recipient)。
func (m NotifyAccountMailer) LostPassword(ctx context.Context, user *domain.User, recipient, u string) error {
	m.Service.LostPassword(ctx, user, tokenOf(u), recipient)
	return nil
}

// Register は Mailer.deliver_register(user, token)。
func (m NotifyAccountMailer) Register(ctx context.Context, user *domain.User, u string) error {
	m.Service.Register(ctx, user, tokenOf(u))
	return nil
}

// AccountActivationRequest は Mailer.deliver_account_activation_request(user)（全有効管理者へ）。
func (m NotifyAccountMailer) AccountActivationRequest(ctx context.Context, user *domain.User, _ string) error {
	if user != nil {
		m.Service.AccountActivationRequest(ctx, user.ID)
	}
	return nil
}

// AccountActivated は Mailer.deliver_account_activated(user)。
func (m NotifyAccountMailer) AccountActivated(ctx context.Context, user *domain.User, _ string) error {
	m.Service.AccountActivated(ctx, user)
	return nil
}

// PasswordUpdated は Mailer.deliver_password_updated(user, sender)。
func (m NotifyAccountMailer) PasswordUpdated(ctx context.Context, user, sender *domain.User) error {
	m.Service.PasswordUpdated(ctx, user, loggedOrNil(sender), httpx.RemoteIPFromContext(ctx))
	return nil
}

// SecurityNotification は Mailer.deliver_security_notification(user, sender, options)（2 要素認証の通知など）。
func (m NotifyAccountMailer) SecurityNotification(ctx context.Context, user, sender *domain.User, n SecurityNotice) error {
	if user == nil {
		return nil
	}
	m.Service.SecurityNotification(ctx, []int64{user.ID}, loggedOrNil(sender), httpx.RemoteIPFromContext(ctx),
		notify.SecurityOptions{Message: n.Message, Title: n.Title, Field: n.Field, URL: n.URL})
	return nil
}

// loggedOrNil は匿名ユーザーなら nil（未ログインの操作では X-Redmine-Sender が空になる）。
func loggedOrNil(u *domain.User) *domain.User {
	if u == nil || !u.Logged() {
		return nil
	}
	return u
}
