// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

// Mailer#account_information / account_activation_request / account_activated / lost_password / register /
// security_notification / settings_updated。

import (
	"net/url"
	"strings"

	"github.com/mikuta0407/buropher/internal/crypto/secretbox"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/mail"
	"github.com/mikuta0407/buropher/internal/repository"
)

// userMail は user.mail（宛先が User ではなくアドレスのメール。Payload.Addresses があればそれ）。
func (m *mailer) userMail() []string {
	if len(m.p.Addresses) > 0 {
		return m.p.Addresses
	}
	if m.user != nil && m.user.Mail != "" {
		return []string{m.user.Mail}
	}
	return nil
}

// decryptPassword は暗号化して保存したパスワードを戻す。
func (m *mailer) decryptPassword() string {
	pw := m.p.Password
	if secretbox.IsSealed(pw) {
		if m.a.Secrets != nil {
			if s, err := m.a.Secrets.Open(pw); err == nil {
				return s
			}
		}
		return ""
	}
	return pw
}

// accountInformation は Mailer#account_information(user, password)。
func (m *mailer) accountInformation() (*mail.Message, error) {
	if m.user == nil {
		return nil, nil
	}
	if m.user.AuthSourceID != nil {
		if src, err := repository.GetAuthSource(m.ctx, m.a.DB, *m.user.AuthSourceID); err == nil && src != nil {
			m.data["AuthSource"] = src.Name
		}
	}
	m.data["Login"] = m.user.Login
	m.data["Password"] = m.decryptPassword()
	m.data["LoginURL"] = m.url("/login")
	return m.finish(m.userMail(), m.l("mail_subject_register", m.a.Settings.String("app_title")), "account_information")
}

// accountActivationRequest は Mailer#account_activation_request(user, new_user)。
func (m *mailer) accountActivationRequest() (*mail.Message, error) {
	nu := m.getUser(m.p.SenderID)
	if nu == nil {
		return nil, nil
	}
	m.data["NewLogin"] = nu.Login
	m.data["URL"] = m.url("/users?sort_key=created_on&sort_order=desc&status=2")
	to, err := m.userMailTo()
	if err != nil {
		return nil, err
	}
	return m.finish(to, m.l("mail_subject_account_activation_request", m.a.Settings.String("app_title")), "account_activation_request")
}

// accountActivated は Mailer#account_activated(user)。
func (m *mailer) accountActivated() (*mail.Message, error) {
	m.data["LoginURL"] = m.url("/login")
	return m.finish(m.userMail(), m.l("mail_subject_register", m.a.Settings.String("app_title")), "account_activated")
}

// lostPassword は Mailer#lost_password(user, token, recipient)。
func (m *mailer) lostPassword() (*mail.Message, error) {
	if m.user == nil {
		return nil, nil
	}
	m.data["URL"] = m.url("/account/lost_password?token=" + url.QueryEscape(m.p.Token))
	m.data["Login"] = m.user.Login
	return m.finish(m.userMail(), m.l("mail_subject_lost_password", m.a.Settings.String("app_title")), "lost_password")
}

// register は Mailer#register(user, token)。
func (m *mailer) register() (*mail.Message, error) {
	m.data["URL"] = m.url("/account/activate?token=" + url.QueryEscape(m.p.Token))
	return m.finish(m.userMail(), m.l("mail_subject_register", m.a.Settings.String("app_title")), "register")
}

// securityURL は options[:url]（パスなら url_for の完全 URL）。
func (m *mailer) securityURL() string {
	u := m.p.URL
	if strings.HasPrefix(u, "/") {
		return m.url(u)
	}
	return u
}

// securityNotification は Mailer#security_notification(user, sender, options)。
func (m *mailer) securityNotification() (*mail.Message, error) {
	sender := m.getUser(m.p.SenderID)
	login := ""
	if sender != nil {
		login = sender.Login
	}
	m.redmineHeaders("Sender", login)
	vars := i18n.Vars{"value": m.p.Value}
	if m.p.Field != "" {
		vars["field"] = m.l(m.p.Field)
	} else {
		vars["field"] = nil
	}
	m.data["Message"] = m.l(m.p.Message, vars)
	title := ""
	if m.p.Title != "" {
		title = m.l(m.p.Title)
	}
	u := m.securityURL()
	m.redmineHeaders("Url", nilIfEmptyStr(u))
	m.data["Title"] = title
	m.data["URL"] = u
	if u != "" {
		m.data["URLOrTitle"] = u
	} else {
		m.data["URLOrTitle"] = title
	}
	m.data["SenderLogin"] = login
	m.data["RemoteIP"] = m.p.RemoteIP
	m.data["Date"] = m.c.Loc.FormatTime(m.a.now(), true)
	var users []*domain.User
	if m.user != nil {
		users = append(users, m.user)
	}
	var extra []string
	for _, r := range m.p.ExtraRecipients {
		dup := false
		for _, e := range extra {
			if e == r {
				dup = true
			}
		}
		if !dup {
			extra = append(extra, r)
		}
	}
	to, err := m.mailTo(users, extra)
	if err != nil {
		return nil, err
	}
	return m.finish(to, "["+m.a.Settings.String("app_title")+"] "+m.l("mail_subject_security_notification"), "security_notification")
}

func nilIfEmptyStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// settingsUpdated は Mailer#settings_updated(user, sender, changes, options)。
func (m *mailer) settingsUpdated() (*mail.Message, error) {
	sender := m.getUser(m.p.SenderID)
	login := ""
	if sender != nil {
		login = sender.Login
	}
	m.redmineHeaders("Sender", login)
	var changes []string
	for _, c := range m.p.Changes {
		changes = append(changes, m.l("setting_"+c))
	}
	m.data["Changes"] = changes
	m.data["URL"] = m.url("/settings")
	m.data["SenderLogin"] = login
	m.data["RemoteIP"] = m.p.RemoteIP
	m.data["Date"] = m.c.Loc.FormatTime(m.a.now(), true)
	to, err := m.userMailTo()
	if err != nil {
		return nil, err
	}
	return m.finish(to, "["+m.a.Settings.String("app_title")+"] "+m.l("mail_subject_security_notification"), "settings_updated")
}
