// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package domain

import (
	"regexp"
	"strings"
	"time"
)

// このファイルはユーザー管理（UsersController / GroupsController / EmailAddressesController）で使う
// User / EmailAddress / UserPreference / AuthSource の属性と、DB を要しない判定の移植。

// MailNotificationOption は User::MAIL_NOTIFICATION_OPTIONS の 1 項目。
type MailNotificationOption struct {
	Value string
	Label string // i18n キー
}

// MailNotificationOptions は User::MAIL_NOTIFICATION_OPTIONS。
var MailNotificationOptions = []MailNotificationOption{
	{"all", "label_user_mail_option_all"},
	{"selected", "label_user_mail_option_selected"},
	{"only_my_events", "label_user_mail_option_only_my_events"},
	{"only_assigned", "label_user_mail_option_only_assigned"},
	{"only_owner", "label_user_mail_option_only_owner"},
	{"only_my_watches", "label_user_mail_option_only_my_watches"}, // Redmine 7.0 (#37978)
	{"none", "label_user_mail_option_none"},
}

// ValidMailNotification は値が MAIL_NOTIFICATION_OPTIONS に含まれれば true。
func ValidMailNotification(v string) bool {
	for _, o := range MailNotificationOptions {
		if o.Value == v {
			return true
		}
	}
	return false
}

// ValidNotificationOptions は User#valid_notification_options（メンバーシップが無ければ selected を除く）。
func ValidNotificationOptions(hasMemberships bool) []MailNotificationOption {
	if hasMemberships {
		return MailNotificationOptions
	}
	var out []MailNotificationOption
	for _, o := range MailNotificationOptions {
		if o.Value != "selected" {
			out = append(out, o)
		}
	}
	return out
}

// User の長さ制限（User::LOGIN_LENGTH_LIMIT / MAIL_LENGTH_LIMIT）。
const (
	LoginLengthLimit = 60
	MailLengthLimit  = 254
)

// LoginFormat は validates_format_of :login（/\A[a-z0-9_\-@\.]*\z/i）。
var LoginFormat = regexp.MustCompile(`(?i)\A[a-z0-9_\-@.]*\z`)

// PasswordCharClassNames は Setting::PASSWORD_CHAR_CLASSES のキー（順序どおり）。
var PasswordCharClassNames = []string{"uppercase", "lowercase", "digits", "special_chars"}

// IsSpecialChar は Setting::PASSWORD_CHAR_CLASSES['special_chars'] に一致する文字なら true
// （ASCII の記号: !"#$%&'()*+,-./:;<=>?@[\]^_`{|}~）。
func IsSpecialChar(r rune) bool {
	return r > 0x20 && r < 0x7f && !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z')
}

// PasswordHasCharClass は password が文字クラス name の文字を含めば true。
func PasswordHasCharClass(password, name string) bool {
	switch name {
	case "uppercase":
		return strings.IndexFunc(password, func(r rune) bool { return r >= 'A' && r <= 'Z' }) >= 0
	case "lowercase":
		return strings.IndexFunc(password, func(r rune) bool { return r >= 'a' && r <= 'z' }) >= 0
	case "digits":
		return strings.IndexFunc(password, func(r rune) bool { return r >= '0' && r <= '9' }) >= 0
	case "special_chars":
		return strings.IndexFunc(password, IsSpecialChar) >= 0
	}
	return true
}

// EmailAddress は email_addresses 行（Redmine EmailAddress）。
type EmailAddress struct {
	ID        int64
	UserID    int64
	Address   string
	IsDefault bool
	Notify    bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// EmailRegexp は URI::MailTo::EMAIL_REGEXP。
var EmailRegexp = regexp.MustCompile(`\A[a-zA-Z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*\z`)

// NormalizeEmail は EmailAddress の normalizes :address（前後の空白を除く。
// IDN ドメインの ASCII 化は未対応: ASCII 以外を含むドメインはそのまま）。
func NormalizeEmail(address string) string {
	return strings.TrimSpace(address)
}

// EmailDomainIn は EmailAddress.domain_in?(domain, domains)。
func EmailDomainIn(domain, domains string) bool {
	domain = strings.ToLower(domain)
	for _, s := range regexp.MustCompile(`[\s,]+`).Split(domains, -1) {
		if s == "" {
			continue
		}
		s = strings.ToLower(s)
		if strings.HasPrefix(s, ".") {
			if strings.HasSuffix(domain, s) {
				return true
			}
		} else if domain == s {
			return true
		}
	}
	return false
}

// ValidEmailDomain は EmailAddress.valid_domain?（Setting.email_domains_denied / allowed）。
func ValidEmailDomain(domainOrEmail, denied, allowed string) bool {
	domain := domainOrEmail
	if i := strings.LastIndex(domain, "@"); i >= 0 {
		domain = domain[i+1:]
	}
	if strings.TrimSpace(denied) != "" && EmailDomainIn(domain, denied) {
		return false
	}
	if strings.TrimSpace(allowed) != "" && !EmailDomainIn(domain, allowed) {
		return false
	}
	return true
}

// UserNotification は user_notification_settings 行（users.mail_notification と
// pref.others の no_self_notified / notify_about_high_priority_issues）。
type UserNotification struct {
	UserID int64
	// MailNotification は "" なら未設定。
	MailNotification              string
	NoSelfNotified                bool
	NotifyAboutHighPriorityIssues bool
}

// TextareaFontOptions は UserPreference::TEXTAREA_FONT_OPTIONS。
var TextareaFontOptions = []string{"monospace", "proportional"}

// DefaultToolbarLanguageOptions は UserPreference::DEFAULT_TOOLBAR_LANGUAGE_OPTIONS。
var DefaultToolbarLanguageOptions = []string{"c", "cpp", "csharp", "css", "diff", "go", "groovy", "html", "java", "javascript", "objc", "perl", "php", "python", "r", "ruby", "sass", "scala", "shell", "sql", "swift", "xml", "yaml"}

// AutoWatchOnOptions は UserPreference::AUTO_WATCH_ON_OPTIONS。
var AutoWatchOnOptions = []string{"issue_created", "issue_contributed_to", "issue_assigned_to_me"}

// UserPreferenceDetail はユーザー編集画面で扱う個人設定の全項目（user_preferences 行）。
type UserPreferenceDetail struct {
	UserPreference
	// Persisted は行が存在するか（無ければ UserPreference.new の既定値）。
	Persisted bool
	// TimeZoneNull は time_zone が NULL（Redmine の nil）。保存時に "" へ変えないために持つ (D-17)。
	TimeZoneNull bool
	// ToolbarLanguageOptions は "" なら既定（DefaultToolbarLanguageOptions）。
	ToolbarLanguageOptions string
	DefaultIssueQueryID    *int64
	DefaultProjectQueryID  *int64
	AutoWatchOn            []string
}

// ToolbarLanguageOptionsOrDefault は UserPreference#toolbar_language_options。
func (p *UserPreferenceDetail) ToolbarLanguageOptionsOrDefault() string {
	if p.ToolbarLanguageOptions != "" {
		return p.ToolbarLanguageOptions
	}
	return strings.Join(DefaultToolbarLanguageOptions, ",")
}

// AuthSource は auth_sources 行（認証方式の選択肢に使う列のみ）。
type AuthSource struct {
	ID   int64
	Kind string
	Name string
	// Searchable は AuthSourceLdap#searchable?。
	Searchable bool
}

// SavedQueryOption は個人設定の既定クエリの選択肢（queries の id と name）。
type SavedQueryOption struct {
	ID         int64
	Name       string
	UserID     *int64
	Visibility int
}

// Query の可視性（Query::VISIBILITY_*）。
const (
	QueryVisibilityPrivate = 0
	QueryVisibilityRoles   = 1
	QueryVisibilityPublic  = 2
)
