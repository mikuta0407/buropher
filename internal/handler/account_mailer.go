// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"context"
	"log/slog"

	"github.com/mikuta0407/buropher/internal/domain"
)

// AccountMailer は認証・アカウント関連のメール（Mailer のうち account_controller / twofa が送るもの）。
// メール配信基盤（internal/notify 等）は別途接続する。未接続の間は NopAccountMailer がログに記録するだけ。
//
// 各メソッドの引数は Redmine の Mailer.deliver_* に対応する（url は絶対 URL）。
type AccountMailer interface {
	// LostPassword は Mailer.deliver_lost_password(user, token, recipient)。url はパスワード再設定画面の URL。
	LostPassword(ctx context.Context, user *domain.User, recipient, url string) error
	// Register は Mailer.deliver_register(user, token)。url はアカウント有効化の URL。
	Register(ctx context.Context, user *domain.User, url string) error
	// AccountActivationRequest は Mailer.deliver_account_activation_request(user)（管理者宛て）。url はユーザー一覧の URL。
	AccountActivationRequest(ctx context.Context, user *domain.User, url string) error
	// AccountActivated は Mailer.deliver_account_activated(user)（管理者が有効化したとき）。url はログイン画面の URL。
	AccountActivated(ctx context.Context, user *domain.User, url string) error
	// PasswordUpdated は Mailer.deliver_password_updated(user, sender)。
	PasswordUpdated(ctx context.Context, user, sender *domain.User) error
	// SecurityNotification は Mailer.deliver_security_notification(user, sender, options)。
	SecurityNotification(ctx context.Context, user, sender *domain.User, n SecurityNotice) error
}

// SecurityNotice は deliver_security_notification の options（message / title / field / url / originator）。
type SecurityNotice struct {
	// Message は本文の訳文キー（例: twofa_mail_body_security_notification_paired）。
	Message string
	// Title はリンクの文言の訳文キー（例: label_my_account）。
	Title string
	// Field は message の %{field} に入る訳文キー（例: twofa__totp__name）。
	Field string
	// URL はリンク先（絶対 URL）。
	URL string
}

// NopAccountMailer はメールを送らずログに残す AccountMailer。
type NopAccountMailer struct{ Logger *slog.Logger }

func (m NopAccountMailer) log() *slog.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return slog.Default()
}

func (m NopAccountMailer) LostPassword(_ context.Context, u *domain.User, recipient, url string) error {
	m.log().Info("mail not delivered (no mailer): lost_password", "user", u.Login, "to", recipient, "url", url)
	return nil
}

func (m NopAccountMailer) Register(_ context.Context, u *domain.User, url string) error {
	m.log().Info("mail not delivered (no mailer): register", "user", u.Login, "to", u.Mail, "url", url)
	return nil
}

func (m NopAccountMailer) AccountActivationRequest(_ context.Context, u *domain.User, url string) error {
	m.log().Info("mail not delivered (no mailer): account_activation_request", "user", u.Login, "url", url)
	return nil
}

func (m NopAccountMailer) AccountActivated(_ context.Context, u *domain.User, url string) error {
	m.log().Info("mail not delivered (no mailer): account_activated", "user", u.Login, "url", url)
	return nil
}

func (m NopAccountMailer) PasswordUpdated(_ context.Context, u, _ *domain.User) error {
	m.log().Info("mail not delivered (no mailer): password_updated", "user", u.Login)
	return nil
}

func (m NopAccountMailer) SecurityNotification(_ context.Context, u, _ *domain.User, n SecurityNotice) error {
	m.log().Info("mail not delivered (no mailer): security_notification", "user", u.Login, "message", n.Message)
	return nil
}

// accountMailer は a.Mailer（nil なら NopAccountMailer）。
func (a *App) accountMailer() AccountMailer {
	if a.Mailer != nil {
		return a.Mailer
	}
	return NopAccountMailer{Logger: a.Logger}
}

// deliver はメール送信のエラーを記録する（Redmine の deliver_later は失敗しても画面の処理を続ける）。
func (a *App) deliver(kind string, err error) {
	if err != nil {
		a.logger().Error("mail delivery failed", "kind", kind, "err", err)
	}
}
