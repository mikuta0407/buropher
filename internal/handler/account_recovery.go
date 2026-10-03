// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"errors"
	"html"
	"net/http"
	"net/url"
	"strings"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

// このファイルは AccountController のうちパスワード再発行（lost_password）と
// 自己登録（register / activate / activation_email）の移植。

// routesAccountRecovery は lost_password / register / activate / activation_email のルート（routesAccount から呼ぶ）。
func (a *App) routesAccountRecovery(r Router) {
	skip := Skip(FilterLoginRequired, FilterPasswordChange)
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		// match 'account/register', :to => 'account#register', :via => [:get, :post], :as => 'register'
		a.Handle(r, m, "/account/register", AccountController, "register", a.AccountRegister, skip)
		// match 'account/lost_password', :to => 'account#lost_password', :via => [:get, :post], :as => 'lost_password'
		a.Handle(r, m, "/account/lost_password", AccountController, "lost_password", a.AccountLostPassword, skip)
	}
	// match 'account/activate', :to => 'account#activate', :via => :get
	a.Handle(r, http.MethodGet, "/account/activate", AccountController, "activate", a.AccountActivate, skip)
	// get 'account/activation_email', :to => 'account#activation_email', :as => 'activation_email'
	a.Handle(r, http.MethodGet, "/account/activation_email", AccountController, "activation_email", a.AccountActivationEmail, skip)
}

// settingURL は Mailer の url_for（Setting.protocol と Setting.host_name を使った絶対 URL）。
func (a *App) settingURL(path string) string {
	proto := a.Settings.String("protocol")
	if proto == "" {
		proto = "http"
	}
	return proto + "://" + strings.TrimRight(a.Settings.String("host_name"), "/") + path
}

// recoveryTokenExpired は Token#expired?（recovery / register は作成から 1 日）。
func (a *App) tokenExpired(t *domain.Token) bool {
	v := repository.TokenValidity(t.Action, a.Settings.Int("autologin"))
	if v == 0 {
		return false
	}
	return !t.CreatedAt.After(a.now().Add(-v))
}

// passwordPolicyData はパスワード欄の注記（text_caracters_minimum / text_characters_must_contain）のデータ。
func (a *App) passwordPolicyData(c *Req, data map[string]any) {
	data["PasswordMinLength"] = a.Settings.String("password_min_length")
	var classes []string
	for _, k := range a.Settings.Strings("password_required_char_classes") {
		classes = append(classes, c.L("label_password_char_class_"+k))
	}
	data["PasswordCharClasses"] = strings.Join(classes, ", ")
}

// ---------------------------------------------------------------- lost_password

// AccountLostPassword は account#lost_password（GET / POST /account/lost_password）。
func (a *App) AccountLostPassword(c *Req) {
	if !a.Settings.Bool("lost_password") {
		c.Redirect("/")
		return
	}
	p := c.Params()
	s := c.Session()
	prt := p.String("token")
	if prt == "" && s != nil {
		prt = s.GetString("password_recovery_token")
	}
	if prt != "" {
		a.passwordRecovery(c, prt)
		return
	}
	if c.R.Method == http.MethodPost {
		email := strings.TrimSpace(p.String("mail"))
		user, err := repository.FindUserByMail(c.Ctx(), a.DB, email)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			a.serverError(c, err)
			return
		}
		if user == nil {
			// 存在しないメールアドレスでも同じメッセージ（アドレスの収集を防ぐ）
			c.Flash().SetNotice(c.L("notice_account_lost_email_sent"))
			c.Render("account/lost_password", nil)
			return
		}
		if !user.Active() {
			a.handleInactiveUser(c, user, "/account/lost_password")
			return
		}
		if !a.changePasswordAllowed(c, user) {
			c.Flash().Now("error", c.L("notice_can_t_change_password"))
			c.Render("account/lost_password", nil)
			return
		}
		token, err := repository.CreateToken(c.Ctx(), a.DB, user.ID, repository.TokenRecovery)
		if err != nil {
			a.serverError(c, err)
			return
		}
		// 送信先は入力されたアドレスと一致するユーザーのアドレス（パラメータそのものは使わない）
		recipient := user.Mail
		if addrs, err := repository.UserEmailAddresses(c.Ctx(), a.DB, user.ID, false); err == nil {
			for _, ad := range addrs {
				if strings.EqualFold(ad.Address, email) {
					recipient = ad.Address
					break
				}
			}
		}
		a.deliver("lost_password", a.accountMailer().LostPassword(c.Ctx(), user, recipient,
			a.settingURL("/account/lost_password?token="+url.QueryEscape(token.Value))))
		c.Flash().SetNotice(c.L("notice_account_lost_email_sent"))
		c.Redirect("/login")
		return
	}
	c.Render("account/lost_password", nil)
}

// passwordRecovery は lost_password のトークンがある場合（パスワード再設定フォーム）。
func (a *App) passwordRecovery(c *Req, prt string) {
	s := c.Session()
	token, err := repository.FindToken(c.Ctx(), a.DB, repository.TokenRecovery, prt, 0, a.now())
	if err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			a.serverError(c, err)
			return
		}
		c.Redirect("/")
		return
	}
	if a.tokenExpired(token) {
		// 期限切れのトークンをセッションから消してやり直させる
		if s != nil {
			s.Delete("password_recovery_token")
		}
		c.Flash().SetError(c.L("error_token_expired"))
		c.Redirect("/account/lost_password")
		return
	}
	// トークンを URL から取り除くため、セッションに移してリダイレクトする
	if c.R.URL.Query().Get("token") != "" {
		if s != nil {
			s.Set("password_recovery_token", token.Value)
		}
		c.Redirect("/account/lost_password")
		return
	}
	user, err := repository.GetUser(c.Ctx(), a.DB, token.UserID)
	if err != nil || !user.Active() {
		c.Redirect("/")
		return
	}
	m, err := a.loadUserModel(c, user)
	if err != nil {
		a.serverError(c, err)
		return
	}
	if c.R.Method == http.MethodPost {
		p := c.Params()
		newPw := p.String("new_password")
		ok := false
		if user.MustChangePassword {
			ok, _ = a.checkPassword(c, user, newPw)
		}
		if ok {
			c.Flash().Now("error", c.L("notice_new_password_must_be_different"))
		} else {
			conf := p.String("new_password_confirmation")
			m.password, m.passwordConfirmation = &newPw, &conf
			m.MustChangePassword = false
			saved, err := a.saveUser(c, m)
			if err != nil {
				a.serverError(c, err)
				return
			}
			if saved {
				if err := repository.DeleteToken(c.Ctx(), a.DB, user.ID, repository.TokenRecovery, token.Value); err != nil {
					a.logger().Error("delete recovery token", "err", err)
				}
				a.deliver("password_updated", a.accountMailer().PasswordUpdated(c.Ctx(), user, c.User))
				c.Flash().SetNotice(c.L("notice_account_password_updated"))
				c.Redirect("/login")
				return
			}
		}
	}
	c.NoStore()
	data := map[string]any{"User": m, "Token": token.Value}
	a.passwordPolicyData(c, data)
	c.Render("account/password_recovery", data)
}

// ---------------------------------------------------------------- register

// selfRegistration は Setting.self_registration?（"0" 以外）。
func (a *App) selfRegistration() bool {
	v := a.Settings.String("self_registration")
	return v != "" && v != "0"
}

// authSourceRegistration は session[:auth_source_registration]（オンザフライ作成に失敗したときの login と auth_source_id）。
func authSourceRegistration(c *Req) (login string, sourceID int64, ok bool) {
	s := c.Session()
	if s == nil {
		return "", 0, false
	}
	v, _ := s.Get("auth_source_registration").(map[string]any)
	if v == nil {
		return "", 0, false
	}
	login, _ = v["login"].(string)
	switch n := v["auth_source_id"].(type) {
	case float64:
		sourceID = int64(n)
	case int64:
		sourceID = n
	case int:
		sourceID = int64(n)
	}
	return login, sourceID, true
}

// AccountRegister は account#register（GET / POST /account/register）。
func (a *App) AccountRegister(c *Req) {
	login, sourceID, fromAuthSource := authSourceRegistration(c)
	if !a.selfRegistration() && !fromAuthSource {
		c.Redirect("/")
		return
	}
	var m *userModel
	if c.R.Method != http.MethodPost {
		if s := c.Session(); s != nil {
			s.Delete("auth_source_registration")
		}
		m = a.newUserModel(c)
		m.Language = c.Loc.Lang
	} else {
		p := c.Params()
		m = a.newUserModel(c)
		// User.new（language は Setting.default_language ではなく params から）
		m.Language = ""
		userParams := p.Map("user")
		m.assignSafeAttributes(userParams, c.User)
		m.assignPref(p.Map("pref"))
		m.AdminFlag = false
		m.Status = domain.StatusRegistered
		if fromAuthSource {
			m.Status = domain.StatusActive
			m.Login = login
			id := sourceID
			m.AuthSourceID = &id
			ok, err := a.saveUser(c, m)
			if err != nil {
				a.serverError(c, err)
				return
			}
			if ok {
				c.Session().Delete("auth_source_registration")
				u, err := repository.GetUser(c.Ctx(), a.DB, m.ID)
				if err != nil {
					a.serverError(c, err)
					return
				}
				a.setLoggedUser(c, u)
				c.Flash().SetNotice(c.L("notice_account_activated"))
				c.Redirect("/my/account")
				return
			}
		} else {
			var pw, conf string
			if userParams != nil {
				pw, conf = userParams.String("password"), userParams.String("password_confirmation")
			}
			if strings.TrimSpace(pw) != "" || strings.TrimSpace(conf) != "" {
				m.password, m.passwordConfirmation = &pw, &conf
			}
			var done bool
			switch a.Settings.String("self_registration") {
			case "1":
				done = a.registerByEmailActivation(c, m)
			case "3":
				done = a.registerAutomatically(c, m)
			default:
				done = a.registerManuallyByAdministrator(c, m)
			}
			if done || c.Halted() {
				return
			}
		}
	}
	c.NoStore()
	a.renderRegister(c, m)
}

// renderRegister は account/register を描画する。
func (a *App) renderRegister(c *Req, m *userModel) {
	showAll := a.Settings.Bool("show_custom_fields_on_registration")
	var tags []any
	for _, cv := range m.customValues {
		if (showAll && cv.Field.Editable) || cv.Field.IsRequired {
			tags = append(tags, customFieldTagWithLabel("user", cv, m.errors.Include(cv.Field.Name)))
		}
	}
	data := map[string]any{
		"User":                 m,
		"CustomFieldTags":      tags,
		"ForceDefaultLanguage": a.Settings.Bool("force_default_language_for_loggedin"),
	}
	a.passwordPolicyData(c, data)
	c.Render("account/register", data)
}

// registerByEmailActivation は AccountController#register_by_email_activation。保存できれば true（リダイレクト済み）。
func (a *App) registerByEmailActivation(c *Req, m *userModel) bool {
	ok, err := a.saveUser(c, m)
	if err != nil {
		a.serverError(c, err)
		return true
	}
	if !ok {
		return false
	}
	a.sendActivationEmail(c, m.User, m.mail)
	return true
}

// sendActivationEmail は register トークンを作り Mailer.deliver_register を呼んで、ログイン画面へリダイレクトする。
func (a *App) sendActivationEmail(c *Req, u *domain.User, mail string) {
	token, err := repository.CreateToken(c.Ctx(), a.DB, u.ID, repository.TokenRegister)
	if err != nil {
		a.serverError(c, err)
		return
	}
	u.Mail = mail
	a.deliver("register", a.accountMailer().Register(c.Ctx(), u, a.settingURL("/account/activate?token="+url.QueryEscape(token.Value))))
	c.Flash().SetNotice(c.L("notice_account_register_done", map[string]any{"email": html.EscapeString(mail)}))
	c.Redirect("/login")
}

// registerAutomatically は AccountController#register_automatically。
func (a *App) registerAutomatically(c *Req, m *userModel) bool {
	m.Status = domain.StatusActive
	ok, err := a.saveUser(c, m)
	if err != nil {
		a.serverError(c, err)
		return true
	}
	if !ok {
		return false
	}
	if err := repository.UpdateLastLogin(c.Ctx(), a.DB, m.ID, a.now()); err != nil {
		a.logger().Error("update last login", "err", err)
	}
	u, err := repository.GetUser(c.Ctx(), a.DB, m.ID)
	if err != nil {
		a.serverError(c, err)
		return true
	}
	a.setLoggedUser(c, u)
	c.Flash().SetNotice(c.L("notice_account_activated"))
	c.Redirect("/my/account")
	return true
}

// registerManuallyByAdministrator は AccountController#register_manually_by_administrator。
func (a *App) registerManuallyByAdministrator(c *Req, m *userModel) bool {
	ok, err := a.saveUser(c, m)
	if err != nil {
		a.serverError(c, err)
		return true
	}
	if !ok {
		return false
	}
	// 管理者へ有効化依頼のメール
	a.deliver("account_activation_request", a.accountMailer().AccountActivationRequest(c.Ctx(), m.User,
		a.settingURL("/users?sort_key=created_on&sort_order=desc&status=2")))
	a.accountPending(c, m.User, "/login")
	return true
}

// AccountActivate は account#activate（GET /account/activate?token=）。
func (a *App) AccountActivate(c *Req) {
	tok := c.Params().String("token")
	if !a.selfRegistration() || strings.TrimSpace(tok) == "" {
		c.Redirect("/")
		return
	}
	token, err := repository.FindToken(c.Ctx(), a.DB, repository.TokenRegister, tok, 0, a.now())
	if err != nil || a.tokenExpired(token) {
		c.Redirect("/")
		return
	}
	user, err := repository.GetUser(c.Ctx(), a.DB, token.UserID)
	if err != nil || !user.Registered() {
		c.Redirect("/")
		return
	}
	if err := repository.UpdateUserStatus(c.Ctx(), a.DB, user.ID, domain.StatusActive, a.now()); err != nil {
		a.serverError(c, err)
		return
	}
	if err := repository.DeleteToken(c.Ctx(), a.DB, user.ID, repository.TokenRegister, token.Value); err != nil {
		a.logger().Error("delete register token", "err", err)
	}
	c.Flash().SetNotice(c.L("notice_account_activated"))
	c.Redirect("/login")
}

// AccountActivationEmail は account#activation_email（アクティベーションメールの再送）。
func (a *App) AccountActivationEmail(c *Req) {
	s := c.Session()
	if s != nil && s.Has("registered_user_id") && a.Settings.String("self_registration") == "1" {
		id := s.GetInt("registered_user_id")
		s.Delete("registered_user_id")
		user, err := repository.GetUser(c.Ctx(), a.DB, id)
		if err == nil && user.Registered() {
			a.sendActivationEmail(c, user, user.Mail)
			return
		}
	}
	c.Redirect("/")
}
