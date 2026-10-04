// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"crypto/rand"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mikuta0407/buropher/internal/auth/password"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/validation"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// このファイルは User モデル（app/models/user.rb, principal.rb）のうち、ユーザー管理画面・API の
// 作成・更新で使う部分（safe_attributes=、検証、before_save / after_save コールバック）の移植。
// フォームビルダ（labelled_form_for @user）にはこの userModel を渡す。

// userModel は編集中の User（ActiveRecord のインスタンス相当）。
type userModel struct {
	*domain.User
	newRecord bool
	// mail は User#mail（email_address.address）。
	mail    string
	mailWas string
	mailSet bool
	// password / passwordConfirmation は attr_accessor（nil = 未設定）。
	password             *string
	passwordConfirmation *string
	generatePassword     bool

	notification *domain.UserNotification
	pref         *domain.UserPreferenceDetail

	notifiedProjectIDs []int64
	notifiedChanged    bool
	groupIDs           []int64
	groupIDsChanged    bool

	// customValues は custom_field_values。
	customValues []principalCustomValue

	// 変更前の値（status_change / saved_change_to_* の判定用）
	orig domain.User

	errors *validation.Errors
	loc    *i18n.Localizer
}

// ParamKey は model_name.param_key。
func (m *userModel) ParamKey() string { return "user" }

// Persisted は persisted?。
func (m *userModel) Persisted() bool { return !m.newRecord }

// ToParam は to_param。
func (m *userModel) ToParam() string { return strconv.FormatInt(m.ID, 10) }

// ValidationErrors は errors（error_messages_for 用）。
func (m *userModel) ValidationErrors() *validation.Errors { return m.errors }

// ErrorsOn は errors[attr]（ラベルの class="error" 用）。
func (m *userModel) ErrorsOn(attr string) []string { return m.errors.Messages(m.loc, attr) }

// HumanAttributeName は User.human_attribute_name。
func (m *userModel) HumanAttributeName(attr string) string {
	return validation.HumanAttributeName(m.loc, "user", attr)
}

// Send はフォームビルダの属性参照（public_send）。
func (m *userModel) Send(method string) (any, bool) {
	switch method {
	case "login":
		return m.Login, true
	case "firstname":
		return m.Firstname, true
	case "lastname":
		return m.Lastname, true
	case "mail":
		if m.mail == "" && !m.mailSet {
			// 新規ユーザーは email_address が無い（nil）
			return nil, true
		}
		return m.mail, true
	case "language":
		return m.Language, true
	case "admin":
		return m.AdminFlag, true
	case "auth_source_id":
		if m.AuthSourceID == nil {
			return nil, true
		}
		return *m.AuthSourceID, true
	case "password", "password_confirmation":
		return nil, true
	case "generate_password":
		return m.generatePassword, true
	case "must_change_passwd":
		return m.MustChangePassword, true
	case "status":
		return m.Status, true
	case "mail_notification":
		return m.notification.MailNotification, true
	}
	return nil, false
}

// CustomFieldTags は users/_form の custom_field_tag_with_label :user, value の列。
func (m *userModel) CustomFieldTags() []rails.HTML {
	var out []rails.HTML
	for _, cv := range m.customValues {
		out = append(out, customFieldTagWithLabel("user", cv, m.errors.Include(cv.Field.Name)))
	}
	return out
}

// Mail は User#mail。
func (m *userModel) Mail() string { return m.mail }

// NewRecord は new_record?。
func (m *userModel) NewRecord() bool { return m.newRecord }

// Pref はフォームビルダ用の User#pref。
func (m *userModel) Pref() *prefModel { return &prefModel{m.pref, m.notification, m.loc} }

// MailNotification は User#mail_notification。
func (m *userModel) MailNotification() string { return m.notification.MailNotification }

// NotifiedProjectIDs は User#notified_projects_ids。
func (m *userModel) NotifiedProjectIDs() []int64 { return m.notifiedProjectIDs }

// GroupIDs は User#group_ids。
func (m *userModel) GroupIDs() []int64 { return m.groupIDs }

// prefModel は labelled_fields_for :pref, @user.pref のモデル。
type prefModel struct {
	p   *domain.UserPreferenceDetail
	n   *domain.UserNotification
	loc *i18n.Localizer
}

func (m *prefModel) ParamKey() string { return "pref" }

func (m *prefModel) HumanAttributeName(attr string) string {
	return validation.HumanAttributeName(m.loc, "user_preference", attr)
}

// Send は UserPreference の属性参照。
func (m *prefModel) Send(method string) (any, bool) {
	p := m.p
	switch method {
	case "hide_mail":
		return p.HideMail, true
	case "time_zone":
		return p.TimeZone, true
	case "comments_sorting":
		// user_preferences.comments_sorting は NOT NULL で、未設定（Redmine の nil）と "asc" を区別できない。
		// インポートしたデータの大半は未設定なので "asc" は nil として扱う（どちらも先頭の選択肢が表示される）。
		if p.CommentsSorting == "asc" || p.CommentsSorting == "" {
			return nil, true
		}
		return p.CommentsSorting, true
	case "warn_on_leaving_unsaved":
		return p.WarnOnLeavingUnsaved, true
	case "textarea_font":
		return p.TextareaFont, true
	case "recently_used_projects":
		return p.RecentlyUsedProjects, true
	case "history_default_tab":
		return p.HistoryDefaultTab, true
	case "toolbar_language_options":
		return p.ToolbarLanguageOptionsOrDefault(), true
	case "default_issue_query":
		if p.DefaultIssueQueryID == nil {
			return nil, true
		}
		return strconv.FormatInt(*p.DefaultIssueQueryID, 10), true
	case "default_project_query":
		if p.DefaultProjectQueryID == nil {
			return nil, true
		}
		return strconv.FormatInt(*p.DefaultProjectQueryID, 10), true
	case "auto_watch_on":
		return p.AutoWatchOn, true
	case "no_self_notified":
		return m.n.NoSelfNotified, true
	case "notify_about_high_priority_issues":
		return m.n.NotifyAboutHighPriorityIssues, true
	}
	return nil, false
}

// AutoWatchOn は pref.auto_watch_on。
func (m *prefModel) AutoWatchOn() []string { return m.p.AutoWatchOn }

// newPreference は UserPreference.new の既定値（Setting.default_users_*）。
func (a *App) newPreference(userID int64) *domain.UserPreferenceDetail {
	p := &domain.UserPreferenceDetail{UserPreference: *domain.DefaultUserPreference(userID)}
	// comments_sorting は UserPreference.new では未設定（nil）
	p.CommentsSorting = ""
	p.HideMail = a.Settings.Bool("default_users_hide_mail")
	p.TimeZone = a.Settings.String("default_users_time_zone")
	if v := a.Settings.Get("default_users_auto_watch_on"); v == nil {
		p.AutoWatchOn = slices.Clone(domain.AutoWatchOnOptions)
	} else {
		p.AutoWatchOn = a.Settings.Strings("default_users_auto_watch_on")
	}
	return p
}

// newUserModel は User.new(language: Setting.default_language, mail_notification: Setting.default_notification_option)。
func (a *App) newUserModel(c *Req) *userModel {
	u := &domain.User{Principal: domain.Principal{Kind: domain.KindUser, Status: domain.StatusActive}}
	u.Language = a.Settings.String("default_language")
	m := &userModel{User: u, newRecord: true, errors: validation.New("user"), loc: c.Loc}
	m.notification = &domain.UserNotification{MailNotification: a.Settings.String("default_notification_option"),
		NoSelfNotified: a.Settings.Bool("default_users_no_self_notified")}
	m.pref = a.newPreference(0)
	m.orig = *u
	cvs, err := a.newPrincipalCustomValues(c, "user")
	if err != nil {
		a.logger().Error("custom fields", "err", err)
	}
	m.customValues = cvs
	return m
}

// loadUserModel は保存済みのユーザーを編集用に読み込む。
func (a *App) loadUserModel(c *Req, u *domain.User) (*userModel, error) {
	ctx := c.Ctx()
	m := &userModel{User: u, mail: u.Mail, mailWas: u.Mail, mailSet: true, errors: validation.New("user"), loc: c.Loc, orig: *u}
	n, err := repository.GetUserNotification(ctx, a.DB, u.ID)
	if err != nil {
		return nil, err
	}
	m.notification = n
	p, err := repository.GetUserPreferenceDetail(ctx, a.DB, u.ID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		// 行が無い（User#pref が UserPreference.new を返す）場合は既定値
		p = a.newPreference(u.ID)
		n.NoSelfNotified = a.Settings.Bool("default_users_no_self_notified")
		n.NotifyAboutHighPriorityIssues = false
	}
	m.pref = p
	if m.notifiedProjectIDs, err = repository.NotifiedProjectIDs(ctx, a.DB, u.ID); err != nil {
		return nil, err
	}
	if m.groupIDs, err = repository.UserGroupIDs(ctx, a.DB, u.ID); err != nil {
		return nil, err
	}
	cvs, err := a.principalCustomValuesByID(c, "user", []int64{u.ID}, false)
	if err != nil {
		return nil, err
	}
	m.customValues = cvs[u.ID]
	return m, nil
}

// castBoolAny は ActiveModel::Type::Boolean の変換（"0" / "false" / "f" / "off" / "" は false）。
func castBoolAny(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "", "0", "f", "false", "off":
			return false
		}
		return true
	}
	return httpx.ValueBool(v)
}

// idsFromParam は配列パラメータを正の整数 ID の列にする（空文字は除く）。
func idsFromParam(v any) []int64 {
	var out []int64
	add := func(s string) {
		if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil && n > 0 && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			add(httpx.ValueString(e))
		}
	case nil:
	default:
		add(httpx.ValueString(x))
	}
	return out
}

// assignSafeAttributes は User#safe_attributes=(params[:user])（current は User.current）。
func (m *userModel) assignSafeAttributes(p *httpx.Params, current *domain.User) {
	if p == nil {
		return
	}
	str := func(k string) (string, bool) {
		v, ok := p.Get(k)
		if !ok {
			return "", false
		}
		return httpx.ValueString(v), true
	}
	if v, ok := str("firstname"); ok {
		m.Firstname = v
	}
	if v, ok := str("lastname"); ok {
		m.Lastname = v
	}
	if v, ok := str("mail"); ok {
		m.mail = domain.NormalizeEmail(v)
		m.mailSet = true
	}
	if v, ok := str("mail_notification"); ok {
		m.notification.MailNotification = v
	}
	if v, ok := p.Get("notified_project_ids"); ok {
		m.notifiedProjectIDs = idsFromParam(v)
		m.notifiedChanged = true
	}
	if v, ok := str("language"); ok {
		m.Language = v
	}
	admin := current != nil && current.IsAdmin()
	if m.newRecord || admin {
		if v, ok := str("login"); ok {
			m.Login = v
		}
	}
	if admin {
		if v, ok := str("status"); ok {
			m.Status = int(httpx.RubyToI(v))
		}
		if v, ok := p.Get("auth_source_id"); ok {
			s := httpx.ValueString(v)
			if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
				m.AuthSourceID = &n
			} else {
				m.AuthSourceID = nil
			}
		}
		if v, ok := p.Get("generate_password"); ok {
			m.generatePassword = castBoolAny(v)
		}
		if v, ok := p.Get("must_change_passwd"); ok {
			m.MustChangePassword = castBoolAny(v)
		}
		if v, ok := p.Get("admin"); ok {
			m.AdminFlag = castBoolAny(v)
		}
		if !m.newRecord {
			if v, ok := p.Get("group_ids"); ok {
				m.groupIDs = idsFromParam(v)
				m.groupIDsChanged = true
			}
		}
	}
	m.customValues = assignCustomFieldValues(m.customValues, p)
}

// assignPref は user.pref.safe_attributes = params[:pref]。
func (m *userModel) assignPref(p *httpx.Params) {
	if p == nil {
		return
	}
	pr := m.pref
	get := func(k string) (any, bool) { return p.Get(k) }
	if v, ok := get("hide_mail"); ok {
		pr.HideMail = castBoolAny(v)
	}
	if v, ok := get("time_zone"); ok {
		pr.TimeZone, pr.TimeZoneNull = httpx.ValueString(v), false
	}
	if v, ok := get("comments_sorting"); ok {
		pr.CommentsSorting = httpx.ValueString(v)
	}
	if v, ok := get("warn_on_leaving_unsaved"); ok {
		pr.WarnOnLeavingUnsaved = httpx.ValueString(v) != "0"
	}
	if v, ok := get("no_self_notified"); ok {
		s := httpx.ValueString(v)
		m.notification.NoSelfNotified = s == "1" || s == "true"
	}
	if v, ok := get("notify_about_high_priority_issues"); ok {
		s := httpx.ValueString(v)
		m.notification.NotifyAboutHighPriorityIssues = s == "1" || s == "true"
	}
	if v, ok := get("textarea_font"); ok {
		pr.TextareaFont = httpx.ValueString(v)
	}
	if v, ok := get("recently_used_projects"); ok {
		pr.RecentlyUsedProjects = int(httpx.RubyToI(httpx.ValueString(v)))
	}
	if v, ok := get("history_default_tab"); ok {
		pr.HistoryDefaultTab = httpx.ValueString(v)
	}
	if v, ok := get("default_issue_query"); ok {
		pr.DefaultIssueQueryID = firstID(v)
	}
	if v, ok := get("default_project_query"); ok {
		pr.DefaultProjectQueryID = firstID(v)
	}
	if v, ok := get("toolbar_language_options"); ok {
		pr.ToolbarLanguageOptions = normalizeToolbarLanguages(httpx.ValueString(v))
	}
	if v, ok := get("auto_watch_on"); ok {
		var vals []string
		switch x := v.(type) {
		case []any:
			for _, e := range x {
				if s := httpx.ValueString(e); s != "" {
					vals = append(vals, s)
				}
			}
		default:
			if s := httpx.ValueString(x); s != "" {
				vals = append(vals, s)
			}
		}
		pr.AutoWatchOn = vals
	}
}

func firstID(v any) *int64 {
	ids := idsFromParam(v)
	if len(ids) == 0 {
		return nil
	}
	return &ids[0]
}

// normalizeToolbarLanguages は UserPreference#toolbar_language_options=（空白を除き、カンマで分割）。
// TODO(textformat): Redmine::SyntaxHighlighting.language_supported? による絞り込み
// （ここでは言語名として妥当な文字列だけを残す）。
func normalizeToolbarLanguages(v string) string {
	v = strings.ReplaceAll(v, " ", "")
	var out []string
	for _, l := range strings.Split(v, ",") {
		if l == "" {
			continue
		}
		ok := true
		for _, r := range l {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("+#_-.", r)) {
				ok = false
			}
		}
		if ok {
			out = append(out, l)
		}
	}
	return strings.Join(out, ",")
}

// generatePasswordValue は User#random_password(length)。
func (a *App) generatePasswordValue(length int) string {
	sets := []string{"ABCDEFGHIJKLMNOPQRSTUVWXYZ", "abcdefghijklmnopqrstuvwxyz", "0123456789"}
	if slices.Contains(a.Settings.Strings("password_required_char_classes"), "special_chars") {
		var sp strings.Builder
		for c := rune(0x20); c <= 0x7e; c++ {
			if domain.IsSpecialChar(c) {
				sp.WriteRune(c)
			}
		}
		sets = append(sets, sp.String())
	}
	for i, s := range sets {
		sets[i] = strings.Map(func(r rune) rune {
			if strings.ContainsRune(`0O1l|'"`+"`*", r) {
				return -1
			}
			return r
		}, s)
	}
	pick := func(s string) byte {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(s))))
		return s[n.Int64()]
	}
	var buf []byte
	for _, s := range sets {
		buf = append(buf, pick(s))
		length--
	}
	all := strings.Join(sets, "")
	for i := 0; i < length; i++ {
		buf = append(buf, pick(all))
	}
	for i := len(buf) - 1; i > 0; i-- {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		j := int(n.Int64())
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}

// validateUser は User の検証（宣言順: email_address の autosave 検証 → validate_status →
// login/firstname/lastname の presence → login の uniqueness / format / length → firstname / lastname の length →
// mail_notification の inclusion → パスワードの文字種 → validate_password_length → validate_password_complexity →
// パスワード確認）。
func (a *App) validateUser(c *Req, m *userModel) error {
	ctx := c.Ctx()
	e := m.errors
	e.Clear()
	// has_one :email_address, autosave: true（エラーは email_address.address に付く）
	if err := a.validateEmailAddress(c, e, "email_address.address", m.mail, m.mail != m.mailWas || m.newRecord, m.ID, true); err != nil {
		return err
	}
	// Principal#validate_status
	if m.Status != m.orig.Status || m.newRecord {
		if m.Status != domain.StatusActive && m.Status != domain.StatusRegistered && m.Status != domain.StatusLocked {
			e.Add("status", "invalid")
		}
	}
	// acts_as_customizable: validate_custom_field_values
	validateCustomFieldValues(e, m.customValues)
	for _, f := range []struct{ attr, v string }{{"login", m.Login}, {"firstname", m.Firstname}, {"lastname", m.Lastname}} {
		if strings.TrimSpace(f.v) == "" {
			e.Add(f.attr, "blank")
		}
	}
	if (m.newRecord || m.Login != m.orig.Login) && m.Login != "" {
		taken, err := repository.LoginTaken(ctx, a.DB, m.Login, m.ID)
		if err != nil {
			return err
		}
		if taken {
			e.Add("login", "taken")
		}
	}
	if !domain.LoginFormat.MatchString(m.Login) {
		e.Add("login", "invalid")
	}
	if utf8.RuneCountInString(m.Login) > domain.LoginLengthLimit {
		e.Add("login", "too_long", "count", domain.LoginLengthLimit)
	}
	if utf8.RuneCountInString(m.Firstname) > 30 {
		e.Add("firstname", "too_long", "count", 30)
	}
	if utf8.RuneCountInString(m.Lastname) > 255 {
		e.Add("lastname", "too_long", "count", 255)
	}
	if mn := m.notification.MailNotification; mn != "" && !domain.ValidMailNotification(mn) {
		e.Add("mail_notification", "inclusion")
	}
	required := a.Settings.Strings("password_required_char_classes")
	if m.password != nil && *m.password != "" {
		for _, k := range domain.PasswordCharClassNames {
			if slices.Contains(required, k) && !domain.PasswordHasCharClass(*m.password, k) {
				e.Add("password", "must_contain_"+k)
			}
		}
	}
	skip := (m.password == nil || *m.password == "") && m.generatePassword
	if !skip && m.password != nil {
		minLen := a.Settings.Int("password_min_length")
		if utf8.RuneCountInString(*m.password) < minLen {
			e.Add("password", "too_short", "count", minLen)
		}
		bad := []string{m.Login, m.Firstname, m.Lastname, m.mail}
		if !m.newRecord {
			addrs, err := repository.UserEmailAddresses(ctx, a.DB, m.ID, false)
			if err != nil {
				return err
			}
			for _, ad := range addrs {
				bad = append(bad, ad.Address)
			}
		}
		for _, b := range bad {
			if strings.EqualFold(*m.password, b) {
				e.Add("password", "too_simple")
				break
			}
		}
	}
	if m.passwordConfirmation != nil && (m.password == nil || *m.password != *m.passwordConfirmation) {
		e.Add("password", "confirmation")
	}
	return nil
}

// validateEmailAddress は EmailAddress の検証（presence → format → length → uniqueness → domain）。
func (a *App) validateEmailAddress(c *Req, e *validation.Errors, attr, address string, changed bool, userID int64, isDefault bool) error {
	if strings.TrimSpace(address) == "" {
		e.Add(attr, "blank")
		return nil
	}
	if !domain.EmailRegexp.MatchString(address) {
		e.Add(attr, "invalid")
	}
	if utf8.RuneCountInString(address) > domain.MailLengthLimit {
		e.Add(attr, "too_long", "count", domain.MailLengthLimit)
	}
	if changed {
		var exceptID int64
		if isDefault && userID != 0 {
			if addrs, err := repository.UserEmailAddresses(c.Ctx(), a.DB, userID, false); err == nil {
				for _, ad := range addrs {
					if ad.IsDefault {
						exceptID = ad.ID
					}
				}
			}
		}
		taken, err := repository.EmailTaken(c.Ctx(), a.DB, address, exceptID)
		if err != nil {
			return err
		}
		if taken {
			e.Add(attr, "taken")
		}
	}
	domain_ := address[strings.LastIndex(address, "@")+1:]
	if strings.Contains(address, "@") && domain_ != "" &&
		!domain.ValidEmailDomain(domain_, a.Settings.String("email_domains_denied"), a.Settings.String("email_domains_allowed")) {
		if c.User.Logged() {
			e.Add(attr, "domain_not_allowed", "domain", domain_)
		} else {
			e.Add(attr, "invalid")
		}
	}
	return nil
}

// saveUser は User#save（検証 → before_save → 保存 → after_save）。検証エラーなら false。
func (a *App) saveUser(c *Req, m *userModel) (bool, error) {
	if err := a.validateUser(c, m); err != nil {
		return false, err
	}
	if m.errors.Any() {
		return false, nil
	}
	now := a.now()
	// before_save: generate_password_if_needed, update_hashed_password
	if m.generatePassword && m.AuthSourceID == nil {
		length := max(a.Settings.Int("password_min_length")+2, 10)
		pw := a.generatePasswordValue(length)
		m.password, m.passwordConfirmation = &pw, &pw
	}
	passwordChanged := false
	if m.password != nil && m.AuthSourceID == nil {
		h, err := password.Hash(*m.password)
		if err != nil {
			return false, err
		}
		m.PasswordHash = h
		t := now.UTC().Truncate(time.Second)
		m.PasswordChangedAt = &t
		passwordChanged = true
	}
	// before_create: set_mail_notification
	if m.newRecord && m.notification.MailNotification == "" {
		m.notification.MailNotification = a.Settings.String("default_notification_option")
	}
	before := userSavedSnapshot{newRecord: m.newRecord, admin: m.orig.AdminFlag, status: m.orig.Status, mail: m.mailWas}
	err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
		if m.newRecord {
			m.Kind = domain.KindUser
			if _, err := repository.InsertUser(c.Ctx(), tx, m.User, m.mail, m.notification, now); err != nil {
				return err
			}
			m.CreatedAt, m.UpdatedAt = now, now
			m.pref.UserID = m.ID
			if err := repository.SaveUserPreferenceDetail(c.Ctx(), tx, m.pref); err != nil {
				return err
			}
		} else {
			changed := m.userChanged() || passwordChanged
			mailChanged, err := repository.SetDefaultEmail(c.Ctx(), tx, m.ID, m.mail, now)
			if err != nil {
				return err
			}
			if mailChanged {
				// EmailAddress#destroy_tokens（アドレスが変わったら recovery トークンを削除）
				if err := repository.DeleteUserTokensByActions(c.Ctx(), tx, m.ID, "recovery"); err != nil {
					return err
				}
			}
			if changed {
				m.UpdatedAt = now
			}
			if err := repository.UpdateUser(c.Ctx(), tx, m.User, &m.orig, changed, now); err != nil {
				return err
			}
			if err := repository.SaveUserNotification(c.Ctx(), tx, m.notification); err != nil {
				return err
			}
			if m.groupIDsChanged {
				if err := a.setUserGroups(c, tx, m.ID, m.groupIDs); err != nil {
					return err
				}
			}
		}
		// acts_as_customizable: save_custom_field_values
		for _, cv := range m.customValues {
			if cv.Field.Multiple {
				if _, err := repository.SetPrincipalCustomValues(c.Ctx(), tx, m.ID, cv.Field.ID, cv.Values); err != nil {
					return err
				}
				continue
			}
			if cv.Value == nil {
				continue
			}
			if _, err := repository.SetPrincipalCustomValue(c.Ctx(), tx, m.ID, cv.Field.ID, *cv.Value); err != nil {
				return err
			}
		}
		// after_save: update_notified_project_ids
		if m.notifiedChanged {
			ids := m.notifiedProjectIDs
			if m.notification.MailNotification != "selected" {
				ids = nil
			}
			if err := repository.SetNotifiedProjects(c.Ctx(), tx, m.ID, ids); err != nil {
				return err
			}
		}
		// after_save: destroy_tokens
		if !m.newRecord && (passwordChanged || (m.Status != m.orig.Status && m.Status != domain.StatusActive)) {
			if err := repository.DeleteUserTokensByActions(c.Ctx(), tx, m.ID, "recovery", "autologin", "session"); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	// after_save :deliver_security_notification（管理者の追加・削除）と既定アドレスの変更の通知
	a.notifyUserSaved(c, m.User, before, m.mail)
	m.newRecord = false
	m.orig = *m.User
	m.mailWas = m.mail
	m.User.Mail = m.mail
	return true, nil
}

// userChanged は principals / user_accounts の属性が変わったか（ActiveRecord の changed?）。
func (m *userModel) userChanged() bool {
	o, u := m.orig, m.User
	return o.Login != u.Login || o.Firstname != u.Firstname || o.Lastname != u.Lastname || o.Status != u.Status ||
		o.AdminFlag != u.AdminFlag || o.Language != u.Language || o.MustChangePassword != u.MustChangePassword ||
		!sameIDPtr(o.AuthSourceID, u.AuthSourceID)
}

func sameIDPtr(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// setUserGroups は User#group_ids=（追加は Group#user_added、削除は Group#user_removed）。
func (a *App) setUserGroups(c *Req, tx *db.Tx, userID int64, groupIDs []int64) error {
	cur, err := repository.UserGroupIDs(c.Ctx(), tx, userID)
	if err != nil {
		return err
	}
	for _, g := range cur {
		if !slices.Contains(groupIDs, g) {
			if err := repository.RemoveUserFromGroup(c.Ctx(), tx, g, userID); err != nil {
				return err
			}
		}
	}
	for _, g := range groupIDs {
		if slices.Contains(cur, g) {
			continue
		}
		grp, err := repository.GetGroup(c.Ctx(), tx, g)
		if err != nil || grp.Builtin() {
			// 存在しない・組込グループは無視する（Group.find が失敗すると Redmine は 404 になるが、
			// フォームからは givable なグループしか送られない）
			continue
		}
		if err := repository.AddUserToGroup(c.Ctx(), tx, g, userID); err != nil {
			return err
		}
	}
	return nil
}
