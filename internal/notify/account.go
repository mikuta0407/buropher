// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package notify

// アカウント・セキュリティ系のメール（常にメールで送る）。s が nil なら何もしない。

import (
	"context"
	"errors"
	"fmt"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

func (s *Service) deliverAccount(ctx context.Context, what string, p Payload, userIDs []int64) {
	s.logErr(what, s.deliverToUsers(ctx, p, userIDs))
}

// AccountInformation は Mailer.deliver_account_information(user, password)（パスワードは暗号化して積む）。
func (s *Service) AccountInformation(ctx context.Context, user *domain.User, password string) {
	if s == nil || user == nil {
		return
	}
	p := Payload{Kind: KindAccountInformation, Addresses: mailOf(user)}
	if password != "" {
		p.Password = password
		if s.Secrets != nil {
			if sealed, err := s.Secrets.Seal(password); err == nil {
				p.Password = sealed
			}
		}
	}
	s.deliverAccount(ctx, "account_information", p, []int64{user.ID})
}

func mailOf(u *domain.User) []string {
	if u.Mail == "" {
		return nil
	}
	return []string{u.Mail}
}

// AccountActivationRequest は Mailer.deliver_account_activation_request(new_user)（全有効管理者へ）。
func (s *Service) AccountActivationRequest(ctx context.Context, newUserID int64) {
	if s == nil {
		return
	}
	admins, err := repository.ActiveAdminIDs(ctx, s.DB)
	if err != nil {
		s.logErr("account_activation_request", err)
		return
	}
	s.deliverAccount(ctx, "account_activation_request", Payload{Kind: KindAccountActivationRequest, SenderID: newUserID}, admins)
}

// AccountActivated は Mailer.deliver_account_activated(user)。
func (s *Service) AccountActivated(ctx context.Context, user *domain.User) {
	if s == nil || user == nil {
		return
	}
	s.deliverAccount(ctx, "account_activated", Payload{Kind: KindAccountActivated, Addresses: mailOf(user)}, []int64{user.ID})
}

// LostPassword は Mailer.deliver_lost_password(user, token, recipient)（recipient が空なら user.mail）。
func (s *Service) LostPassword(ctx context.Context, user *domain.User, token, recipient string) {
	if s == nil || user == nil {
		return
	}
	to := mailOf(user)
	if recipient != "" {
		to = []string{recipient}
	}
	s.deliverAccount(ctx, "lost_password", Payload{Kind: KindLostPassword, Addresses: to, Token: token}, []int64{user.ID})
}

// Register は Mailer.deliver_register(user, token)。
func (s *Service) Register(ctx context.Context, user *domain.User, token string) {
	if s == nil || user == nil {
		return
	}
	s.deliverAccount(ctx, "register", Payload{Kind: KindRegister, Addresses: mailOf(user), Token: token}, []int64{user.ID})
}

// SecurityOptions は Mailer.deliver_security_notification の options。
type SecurityOptions struct {
	// Message / Field / Title は i18n キー。
	Message, Field, Value, Title string
	// URL は options[:url]（"/my/account" のようなパスか完全 URL）。
	URL string
	// Recipients は追加の宛先（options[:recipients]）。
	Recipients []string
}

// SecurityNotification は Mailer.deliver_security_notification(users, sender, options)。
func (s *Service) SecurityNotification(ctx context.Context, userIDs []int64, sender *domain.User, remoteIP string, o SecurityOptions) {
	if s == nil || len(userIDs) == 0 {
		return
	}
	p := Payload{Kind: KindSecurityNotification, RemoteIP: remoteIP, Message: o.Message, Field: o.Field, Value: o.Value,
		Title: o.Title, URL: o.URL, ExtraRecipients: o.Recipients}
	if sender != nil {
		p.SenderID = sender.ID
	}
	s.deliverAccount(ctx, "security_notification", p, userIDs)
}

// PasswordUpdated は Mailer.deliver_password_updated(user, sender)。
func (s *Service) PasswordUpdated(ctx context.Context, user, sender *domain.User, remoteIP string) {
	if s == nil || user == nil {
		return
	}
	// 初回ログイン時に変更を強制される既定の admin アカウントのダミーアドレスには送らない
	if user.AdminFlag && user.Login == "admin" && user.Mail == "admin@example.net" {
		return
	}
	s.SecurityNotification(ctx, []int64{user.ID}, sender, remoteIP, SecurityOptions{
		Message: "mail_body_password_updated", Title: "button_change_password", URL: "/my/password"})
}

// AdminFlagChanged は User#deliver_security_notification（管理者の追加・削除。全有効管理者へ）。
// added は管理者になった（作成・権限付与・ロック解除）なら true、外れた（削除・権限剥奪・ロック）なら false。
func (s *Service) AdminFlagChanged(ctx context.Context, user, sender *domain.User, remoteIP string, added bool) {
	if s == nil || user == nil {
		return
	}
	admins, err := repository.ActiveAdminIDs(ctx, s.DB)
	if err != nil {
		s.logErr("security_notification", err)
		return
	}
	msg := "mail_body_security_notification_remove"
	if added {
		msg = "mail_body_security_notification_add"
	}
	s.SecurityNotification(ctx, admins, sender, remoteIP, SecurityOptions{
		Message: msg, Field: "field_admin", Value: user.Login, Title: "label_user_plural", URL: "/users"})
}

// EmailAddressAdded は EmailAddress#deliver_security_notification_create（唯一のアドレスなら送らない）。
func (s *Service) EmailAddressAdded(ctx context.Context, user, sender *domain.User, remoteIP, address string) {
	if s == nil || user == nil {
		return
	}
	all, err := repository.AllEmailAddresses(ctx, s.DB, user.ID)
	if err != nil || (len(all) == 1 && all[0] == address) {
		return
	}
	s.SecurityNotification(ctx, []int64{user.ID}, sender, remoteIP, SecurityOptions{
		Message: "mail_body_security_notification_add", Field: "field_mail", Value: address,
		Title: "label_my_account", URL: "/my/account"})
}

// EmailAddressChanged は EmailAddress#deliver_security_notification_update（アドレスの変更）。
func (s *Service) EmailAddressChanged(ctx context.Context, user, sender *domain.User, remoteIP, oldAddress, newAddress string) {
	if s == nil || user == nil || oldAddress == newAddress {
		return
	}
	s.SecurityNotification(ctx, []int64{user.ID}, sender, remoteIP, SecurityOptions{
		Message: "mail_body_security_notification_change_to", Field: "field_mail", Value: newAddress,
		Title: "label_my_account", URL: "/my/account", Recipients: []string{oldAddress}})
}

// EmailAddressNotifyChanged は EmailAddress#deliver_security_notification_update（通知の有効・無効の変更）。
func (s *Service) EmailAddressNotifyChanged(ctx context.Context, user, sender *domain.User, remoteIP, address string, notify bool) {
	if s == nil || user == nil {
		return
	}
	msg := "mail_body_security_notification_notify_disabled"
	if notify {
		msg = "mail_body_security_notification_notify_enabled"
	}
	s.SecurityNotification(ctx, []int64{user.ID}, sender, remoteIP, SecurityOptions{
		Message: msg, Value: address, Title: "label_my_account", URL: "/my/account", Recipients: []string{address}})
}

// EmailAddressRemoved は EmailAddress#deliver_security_notification_destroy。
func (s *Service) EmailAddressRemoved(ctx context.Context, user, sender *domain.User, remoteIP, address string) {
	if s == nil || user == nil {
		return
	}
	s.SecurityNotification(ctx, []int64{user.ID}, sender, remoteIP, SecurityOptions{
		Message: "mail_body_security_notification_remove", Field: "field_mail", Value: address,
		Title: "label_my_account", URL: "/my/account", Recipients: []string{address}})
}

// Twofa は Redmine::Twofa::Base の deliver_twofa_*（action: paired / unpaired / backup_codes_generated / backup_code_used。
// scheme は "totp"、sender は User.current）。
func (s *Service) Twofa(ctx context.Context, user, sender *domain.User, remoteIP, action, scheme string) {
	if s == nil || user == nil {
		return
	}
	o := SecurityOptions{Title: "label_my_account", URL: "/my/account"}
	switch action {
	case "paired":
		o.Message, o.Field = "twofa_mail_body_security_notification_paired", "twofa__"+scheme+"__name"
	case "unpaired":
		o.Message = "twofa_mail_body_security_notification_unpaired"
	case "backup_codes_generated":
		o.Message = "twofa_mail_body_backup_codes_generated"
	case "backup_code_used":
		o.Message = "twofa_mail_body_backup_code_used"
	default:
		return
	}
	s.SecurityNotification(ctx, []int64{user.ID}, sender, remoteIP, o)
}

// SettingsUpdated は Mailer.deliver_settings_updated(sender, changes)（全有効管理者へ）。
func (s *Service) SettingsUpdated(ctx context.Context, sender *domain.User, changes []string, remoteIP string) {
	if s == nil || len(changes) == 0 {
		return
	}
	admins, err := repository.ActiveAdminIDs(ctx, s.DB)
	if err != nil {
		s.logErr("settings_updated", err)
		return
	}
	p := Payload{Kind: KindSettingsUpdated, Changes: changes, RemoteIP: remoteIP}
	if sender != nil {
		p.SenderID = sender.ID
	}
	s.deliverAccount(ctx, "settings_updated", p, admins)
}

// ErrMailDisabled はメール配送が設定されていない。
var ErrMailDisabled = errors.New("email delivery is not configured")

// TestEmail は Mailer.deliver_test_email(user)（同期送信して配送エラーを返す）。
func (s *Service) TestEmail(ctx context.Context, user *domain.User) error {
	if s == nil || !s.MailEnabled() {
		return ErrMailDisabled
	}
	m, err := s.Renderer.RenderMail(ctx, &Payload{Kind: KindTestEmail, UserID: user.ID})
	if err != nil {
		return err
	}
	if m == nil {
		return fmt.Errorf("no recipient")
	}
	return s.Mail.Send(ctx, m)
}
