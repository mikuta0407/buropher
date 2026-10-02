package domain

import (
	"regexp"
	"strings"
	"time"
)

// ---------------------------------------------------------------- 表示名 (User::USER_FORMATS)

// userFormat は User::USER_FORMATS の 1 エントリ。
type userFormat struct {
	name, initials func(u *User) string
}

// firstRunes は String#first(n) (文字単位)。
func firstRunes(s string, n int) string {
	i := 0
	for j := range s {
		if i == n {
			return s[:j]
		}
		i++
	}
	return s
}

func firstRune(s string) string { return firstRunes(s, 1) }

var initialRe = regexp.MustCompile(`(([\p{L}])[\p{L}]*\.?)`)

// firstInitials は firstname.gsub(/(([[:alpha:]])[[:alpha:]]*\.?)/, '\2.')。
func firstInitials(s string) string { return initialRe.ReplaceAllString(s, "$2.") }

var userFormats = map[string]userFormat{
	"firstname_lastname": {
		func(u *User) string { return u.Firstname + " " + u.Lastname },
		func(u *User) string { return firstRune(u.Firstname) + firstRune(u.Lastname) }},
	"firstname_lastinitial": {
		func(u *User) string { return u.Firstname + " " + firstRune(u.Lastname) + "." },
		func(u *User) string { return firstRune(u.Firstname) + firstRune(u.Lastname) }},
	"firstinitial_lastname": {
		func(u *User) string { return firstInitials(u.Firstname) + " " + u.Lastname },
		func(u *User) string { return firstRune(firstInitials(u.Firstname)) + firstRune(u.Lastname) }},
	"firstname": {
		func(u *User) string { return u.Firstname },
		func(u *User) string { return firstRunes(u.Firstname, 2) }},
	"lastname_firstname": {
		func(u *User) string { return u.Lastname + " " + u.Firstname },
		func(u *User) string { return firstRune(u.Lastname) + firstRune(u.Firstname) }},
	"lastnamefirstname": {
		func(u *User) string { return u.Lastname + u.Firstname },
		func(u *User) string { return firstRune(u.Lastname) + firstRune(u.Firstname) }},
	"lastname_comma_firstname": {
		func(u *User) string { return u.Lastname + ", " + u.Firstname },
		func(u *User) string { return firstRune(u.Lastname) + firstRune(u.Firstname) }},
	"lastname": {
		func(u *User) string { return u.Lastname },
		func(u *User) string { return firstRunes(u.Lastname, 2) }},
	"username": {
		func(u *User) string { return u.Login },
		func(u *User) string { return firstRunes(u.Login, 2) }},
}

// ValidUserFormat は format が User::USER_FORMATS のキーなら true。
func ValidUserFormat(format string) bool {
	_, ok := userFormats[format]
	return ok
}

// formatter は User.name_formatter (未知の書式は firstname_lastname)。
func formatter(format string) userFormat {
	if f, ok := userFormats[format]; ok {
		return f
	}
	return userFormats["firstname_lastname"]
}

// Name は User#name(formatter)。format は USER_FORMATS のキー
// (呼び出し側が Setting.user_format を渡す)。匿名ユーザの表示名
// (l(:label_user_anonymous)) は i18n を要するため呼び出し側で扱う。
func (u *User) Name(format string) string { return formatter(format).name(u) }

// Initials は User#initials(formatter) (大文字化する)。
func (u *User) Initials(format string) string {
	return strings.ToUpper(formatter(format).initials(u))
}

// labelByStatus は User::LABEL_BY_STATUS。
var labelByStatus = map[int]string{
	StatusAnonymous: "anon", StatusActive: "active", StatusRegistered: "registered", StatusLocked: "locked",
}

// CSSClasses は User#css_classes。
func (u *User) CSSClasses() string { return "user " + labelByStatus[u.Status] }

// ---------------------------------------------------------------- パスワード・2FA

// PasswordExpired は User#password_expired?。maxAgeDays は Setting.password_max_age。
func (u *User) PasswordExpired(now time.Time, maxAgeDays int) bool {
	if maxAgeDays == 0 {
		return false
	}
	changed := time.Unix(0, 0)
	if u.PasswordChangedAt != nil {
		changed = *u.PasswordChangedAt
	}
	return changed.Before(now.AddDate(0, 0, -maxAgeDays))
}

// ChangePasswordAllowed は User#change_password_allowed?。
// 外部認証 (LDAP 等) のパスワード変更は未対応のため、認証元があれば false とする。
// TODO(auth): AuthSource#allow_password_changes?。
func (u *User) ChangePasswordAllowed() bool { return u.AuthSourceID == nil }

// MustChangePasswordNow は User#must_change_password?。
func (u *User) MustChangePasswordNow(now time.Time, maxAgeDays int) bool {
	return (u.MustChangePassword || u.PasswordExpired(now, maxAgeDays)) && u.ChangePasswordAllowed()
}

// TwofaActive は User#twofa_active?。
func (u *User) TwofaActive() bool { return u.TwofaScheme != "" }

// TwofaPolicy は User#must_activate_twofa? が参照する設定 (Setting.twofa)。
type TwofaPolicy struct {
	Required                  bool // Setting.twofa_required?
	RequiredForAdministrators bool // Setting.twofa_required_for_administrators?
	Optional                  bool // Setting.twofa_optional?
}

// MustActivateTwofa は User#must_activate_twofa?。
// inTwofaRequiredGroup は groups.any?(&:twofa_required?) の結果。
func (u *User) MustActivateTwofa(p TwofaPolicy, inTwofaRequiredGroup bool) bool {
	if u.TwofaActive() {
		return false
	}
	switch {
	case p.Required:
		return true
	case p.RequiredForAdministrators && u.IsAdmin():
		return true
	case p.Optional && inTwofaRequiredGroup:
		return true
	}
	return false
}

// ---------------------------------------------------------------- 個人設定

// UserPreference は user_preferences 行 (Redmine UserPreference / User#pref)。
// 行が無いユーザも既定値 (DefaultUserPreference) で扱う。
type UserPreference struct {
	UserID               int64
	HideMail             bool
	TimeZone             string // "" = サーバ既定
	CommentsSorting      string // "asc" / "desc"
	WarnOnLeavingUnsaved bool
	TextareaFont         string // "" / "monospace" / "proportional"
	RecentlyUsedProjects int
	HistoryDefaultTab    string
}

// DefaultUserPreference は UserPreference.new の既定値。
func DefaultUserPreference(userID int64) *UserPreference {
	return &UserPreference{
		UserID: userID, HideMail: true, CommentsSorting: "asc", WarnOnLeavingUnsaved: true,
		RecentlyUsedProjects: 3,
	}
}

// ---------------------------------------------------------------- トークン

// Token は tokens 行 (Redmine Token)。
type Token struct {
	ID        int64
	UserID    int64
	Action    string
	Value     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ---------------------------------------------------------------- ニュース

// News は news 行 (Redmine News)。Project / Author は読み込み時に preload される。
type News struct {
	ID            int64
	ProjectID     int64
	Title         string
	Summary       string
	Description   string
	AuthorID      int64
	CommentsCount int
	CreatedAt     time.Time

	Project *Project
	Author  *User
}
