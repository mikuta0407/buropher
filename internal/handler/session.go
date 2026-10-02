package handler

import (
	"errors"
	"github.com/mikuta0407/buropher/internal/auth/ldap"
	"net/http"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/auth/password"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
)

// このファイルは ApplicationController のうち「現在のユーザー」に関わる部分
// （session_expiration, user_setup / find_current_user, autologin, logged_user=, logout_user,
// check_if_login_required, check_password_change, check_twofa_activation）と
// User のうち設定・DB を要する判定（must_change_password?, must_activate_twofa?, try_to_login）の移植。

// policy は Setting.session_lifetime / session_timeout。
func (a *App) policy() httpx.ExpiryPolicy {
	return httpx.PolicyFromMinutes(a.Settings.Int("session_lifetime"), a.Settings.Int("session_timeout"))
}

// SessionPolicy は SessionManager.Policy に渡す関数。
func (a *App) SessionPolicy() httpx.ExpiryPolicy { return a.policy() }

// sessionExpiration は ApplicationController#session_expiration。
func (a *App) sessionExpiration(c *Req) {
	s := c.Session()
	if s == nil {
		return
	}
	expired := (s.UserID() != 0 && s.Expired(a.policy(), a.now())) || s.Revoked()
	if !expired {
		return
	}
	if u := a.tryToAutologin(c); u != nil {
		return
	}
	var user *domain.User
	if s.UserID() != 0 {
		user = a.findActiveUser(c, s.UserID())
	}
	if user == nil {
		user = a.anonymous(c.Ctx())
	}
	a.setLocalization(c, user)
	a.setLoggedUser(c, nil)
	c.Flash().SetError(c.L("error_session_expired"))
	a.requireLogin(c)
}

// findActiveUser は User.active.find_by_id(id)（無ければ nil）。
func (a *App) findActiveUser(c *Req, id int64) *domain.User {
	u, err := repository.FindActiveUser(c.Ctx(), a.DB, id)
	if err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			a.logger().Error("find active user", "id", id, "err", err)
		}
		return nil
	}
	return u
}

// userSetup は ApplicationController#user_setup（User.current = find_current_user）。
func (a *App) userSetup(c *Req) {
	user := a.findCurrentUser(c)
	if c.halted {
		return
	}
	if user == nil {
		user = a.anonymous(c.Ctx())
	}
	c.SetUser(user)
}

// findCurrentUser は ApplicationController#find_current_user。
// セッション → autologin → atom キー（accept_atom_auth のアクションのみ）→
// API キー / HTTP Basic（REST API 有効かつ accept_api_auth のアクションのみ）の順に探す。
// エラー応答を返した場合は c.Halted() が true になる。
// TODO(oauth): Doorkeeper（OAuth2 アクセストークン）による認証。
func (a *App) findCurrentUser(c *Req) *domain.User {
	var user *domain.User
	p := c.Params()
	if !httpx.IsAPIRequest(c.R) {
		if s := c.Session(); s != nil && s.UserID() != 0 {
			user = a.findActiveUser(c, s.UserID())
		} else if u := a.tryToAutologin(c); u != nil {
			user = u
		} else if httpx.Format(c.R) == "atom" && p.Present("key") && c.R.Method == http.MethodGet && c.cfg.acceptAtomAuth {
			// atom キーの認証はセッションを開始しない
			user = a.findTokenUser(c, repository.TokenFeeds, p.String("key"), 0)
		}
	}
	if user == nil && a.Settings.Bool("rest_api_enabled") && c.cfg.acceptAPIAuth {
		if key := apiKeyFromRequest(c); key != "" {
			user = a.findTokenUser(c, repository.TokenAPI, key, 0)
		} else if username, pw, ok := c.R.BasicAuth(); ok {
			// HTTP Basic（ログイン名とパスワード、または API キーと任意の文字列）
			u, err := a.tryToLogin(c, username, pw, true)
			if err != nil {
				a.logger().Error("basic authentication", "err", err)
			}
			if u != nil && u.TwofaActive() {
				c.RenderError(http.StatusUnauthorized, "HTTP Basic authentication is not allowed. Use API key instead")
				return nil
			}
			if u == nil {
				u = a.findTokenUser(c, repository.TokenAPI, username, 0)
			}
			if u != nil && a.mustChangePassword(u) {
				c.RenderError(http.StatusForbidden, "You must change your password")
				return nil
			}
			user = u
		}
		// 管理者による X-Redmine-Switch-User
		if user != nil && user.IsAdmin() {
			if login := strings.TrimSpace(c.R.Header.Get("X-Redmine-Switch-User")); login != "" {
				su, err := repository.FindUserByLogin(c.Ctx(), a.DB, login)
				if err == nil && su.Active() {
					a.logger().Info("User switched", "by", user.Login, "id", user.ID)
					user = su
				} else {
					c.RenderError(http.StatusPreconditionFailed, "Invalid X-Redmine-Switch-User header")
					return nil
				}
			}
		}
	}
	return user
}

// apiKeyFromRequest は ApplicationController#api_key_from_request。
func apiKeyFromRequest(c *Req) string {
	if p := c.Params(); p.Present("key") {
		return p.String("key")
	}
	return strings.TrimSpace(c.R.Header.Get("X-Redmine-API-Key"))
}

// findTokenUser は Token.find_active_user(action, key, validity_days)（無ければ nil）。
func (a *App) findTokenUser(c *Req, action, key string, validityDays int) *domain.User {
	u, err := repository.FindActiveTokenUser(c.Ctx(), a.DB, action, key, validityDays, a.now())
	if err != nil {
		if !errors.Is(err, repository.ErrNotFound) {
			a.logger().Error("token user", "action", action, "err", err)
		}
		return nil
	}
	return u
}

// tryToAutologin は ApplicationController#try_to_autologin。
func (a *App) tryToAutologin(c *Req) *domain.User {
	ck, err := c.R.Cookie(a.autologinCookieName())
	if err != nil || ck.Value == "" || !a.Settings.Bool("autologin") {
		return nil
	}
	// User.try_to_autologin: 有効なトークンのユーザーの last_login_on を更新する
	u := a.findTokenUser(c, repository.TokenAutologin, ck.Value, a.Settings.Int("autologin"))
	if u == nil {
		return nil
	}
	if err := repository.UpdateLastLogin(c.Ctx(), a.DB, u.ID, a.now()); err != nil {
		a.logger().Error("update last login", "err", err)
	}
	if s := c.Session(); s != nil {
		s.Reset()
		a.startUserSession(c, u)
	}
	c.SetUser(u)
	return u
}

// startUserSession は ApplicationController#start_user_session。
func (a *App) startUserSession(c *Req, u *domain.User) {
	s := c.Session()
	s.SetUserID(u.ID)
	if a.mustChangePassword(u) {
		s.Set("pwd", "1")
	}
	if a.mustActivateTwofa(c, u) {
		s.Set("must_activate_twofa", "1")
	}
}

// setLoggedUser は ApplicationController#logged_user=（nil ならログアウト状態にする）。
func (a *App) setLoggedUser(c *Req, u *domain.User) {
	if s := c.Session(); s != nil {
		s.Reset()
	}
	if u != nil {
		c.SetUser(u)
		if c.Session() != nil {
			a.startUserSession(c, u)
		}
	} else {
		c.SetUser(a.anonymous(c.Ctx()))
	}
}

// logoutUser は ApplicationController#logout_user。
func (a *App) logoutUser(c *Req) {
	if !c.User.Logged() {
		return
	}
	if ck, err := c.R.Cookie(a.autologinCookieName()); err == nil {
		a.deleteAutologinCookie(c)
		if ck.Value != "" {
			if err := repository.DeleteToken(c.Ctx(), a.DB, c.User.ID, repository.TokenAutologin, ck.Value); err != nil {
				a.logger().Error("delete autologin token", "err", err)
			}
		}
	}
	a.setLoggedUser(c, nil)
}

func (a *App) deleteAutologinCookie(c *Req) {
	http.SetCookie(c.W, &http.Cookie{Name: a.autologinCookieName(), Value: "", Path: a.autologinCookiePath(), MaxAge: -1, Expires: time.Unix(0, 0)})
}

// checkIfLoginRequired は ApplicationController#check_if_login_required。
func (a *App) checkIfLoginRequired(c *Req) {
	if c.User.Logged() {
		return
	}
	if a.Settings.Bool("login_required") {
		a.requireLogin(c)
	}
}

// checkPasswordChange は ApplicationController#check_password_change。
func (a *App) checkPasswordChange(c *Req) {
	s := c.Session()
	if s == nil || !s.Has("pwd") {
		return
	}
	if a.mustChangePassword(c.User) {
		c.Flash().SetError(c.L("error_password_expired"))
		c.Redirect("/my/password")
		return
	}
	s.Delete("pwd")
}

// checkTwofaActivation は ApplicationController#check_twofa_activation。
func (a *App) checkTwofaActivation(c *Req) {
	s := c.Session()
	if s == nil || !s.Has("must_activate_twofa") {
		return
	}
	if a.mustActivateTwofa(c, c.User) {
		c.Flash().SetWarning(c.L("twofa_warning_require"))
		// 利用可能な方式は totp のみ（available_schemes.length == 1 → init_twofa_pairing_and_send_code_for）
		t, err := a.twofaFor(c, c.User)
		if err != nil {
			a.serverError(c, err)
			return
		}
		a.initTwofaPairingAndSendCodeFor(c, t)
		return
	}
	s.Delete("must_activate_twofa")
}

// ---------------------------------------------------------------- User の設定依存の判定

// mustChangePassword は User#must_change_password?。
func (a *App) mustChangePassword(u *domain.User) bool {
	if u == nil || !u.Logged() {
		return false
	}
	return u.MustChangePasswordNow(a.now(), a.Settings.Int("password_max_age"))
}

// mustActivateTwofa は User#must_activate_twofa?。
func (a *App) mustActivateTwofa(c *Req, u *domain.User) bool {
	if u == nil || !u.Logged() {
		return false
	}
	policy := domain.TwofaPolicy{
		Required:                  a.Settings.TwofaRequired(),
		RequiredForAdministrators: a.Settings.TwofaRequiredForAdministrators(),
		Optional:                  a.Settings.TwofaOptional(),
	}
	inGroup := false
	if policy.Optional && !u.TwofaActive() {
		ok, err := repository.UserInTwofaRequiredGroup(c.Ctx(), a.DB, u.ID)
		if err != nil {
			a.logger().Error("twofa groups", "err", err)
		}
		inGroup = ok
	}
	return u.MustActivateTwofa(policy, inGroup)
}

// userLanguage は User#language（force_default_language_for_loggedin なら既定言語）。
func (a *App) userLanguage(u *domain.User) string {
	if a.Settings.Bool("force_default_language_for_loggedin") {
		return a.Settings.String("default_language")
	}
	return u.Language
}

// preference は user.pref（c.User なら Req にキャッシュする）。
func (a *App) preference(c *Req, u *domain.User) *domain.UserPreference {
	if c.User == u {
		return c.Pref()
	}
	p, err := repository.GetUserPreference(c.Ctx(), a.DB, u.ID)
	if err != nil {
		a.logger().Error("user preference", "err", err)
		return domain.DefaultUserPreference(u.ID)
	}
	return p
}

// tryToLogin は User.try_to_login(login, password, active_only)（AuthSourceException は記録して nil）。
// 該当なし・パスワード不一致は (nil, nil)。オンザフライ作成に失敗した未保存のユーザーも nil。
func (a *App) tryToLogin(c *Req, login, pw string, activeOnly bool) (*domain.User, error) {
	u, _, err := a.tryToLoginBang(c, login, pw, activeOnly)
	if err != nil && ldap.IsAuthSourceError(err) {
		a.logger().Error("An error occured when authenticating "+strings.TrimSpace(login), "err", err)
		return nil, nil
	}
	return u, err
}

// tryToLoginBang は User.try_to_login!(login, password, active_only)。
// 該当なし・パスワード不一致は (nil, nil, nil)。未登録ユーザーを認証方式（オンザフライ登録）で認証できたが
// 保存に失敗した場合は未保存のユーザー（new_record）を 2 番目に返す。認証方式の例外（*ldap.Error）はそのまま返す。
func (a *App) tryToLoginBang(c *Req, login, pw string, activeOnly bool) (*domain.User, *userModel, error) {
	login = strings.TrimSpace(login)
	if login == "" || pw == "" {
		return nil, nil, nil
	}
	u, err := repository.FindUserByLogin(c.Ctx(), a.DB, login)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		// 未登録: オンザフライ登録の認証方式で認証し、ユーザーを作成する
		attrs := a.authenticateWithAuthSources(c, login, pw)
		if attrs == nil {
			return nil, nil, nil
		}
		m := a.newUserModel(c)
		m.Firstname, m.Lastname = attrs.Firstname, attrs.Lastname
		m.mail, m.mailSet = attrs.Mail, true
		id := attrs.AuthSourceID
		m.AuthSourceID = &id
		m.Login = login
		m.Language = a.Settings.String("default_language")
		ok, err := a.saveUser(c, m)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return nil, m, nil
		}
		a.logger().Info("User created from external auth source", "login", m.Login, "auth_source_id", id)
		if u, err = repository.GetUser(c.Ctx(), a.DB, m.ID); err != nil {
			return nil, nil, err
		}
	case err != nil:
		return nil, nil, err
	default:
		ok, err := a.checkPassword(c, u, pw)
		if err != nil || !ok {
			return nil, nil, err
		}
		if !u.Active() && activeOnly {
			return nil, nil, nil
		}
	}
	if u.Active() {
		if err := repository.UpdateLastLogin(c.Ctx(), a.DB, u.ID, a.now()); err != nil {
			return nil, nil, err
		}
	}
	return u, nil, nil
}

// checkPassword は User#check_password?（認証方式があればその認証、なければローカルのパスワード）。
func (a *App) checkPassword(c *Req, u *domain.User, pw string) (bool, error) {
	if u.AuthSourceID != nil {
		rec, err := repository.GetAuthSource(c.Ctx(), a.DB, *u.AuthSourceID)
		if errors.Is(err, repository.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		src := a.ldapSource(rec)
		if src == nil || !rec.Enabled {
			return false, nil
		}
		attrs, err := src.Authenticate(u.Login, pw)
		return attrs != nil, err
	}
	ok, err := password.Verify(u.PasswordHash, pw)
	if err != nil {
		a.logger().Warn("password verify", "login", u.Login, "err", err)
		return false, nil
	}
	if !ok {
		return false, nil
	}
	// Redmine 形式（SHA1）のハッシュはログイン成功時に argon2id へ移行する
	if password.NeedsRehash(u.PasswordHash) {
		if h, err := password.Hash(pw); err == nil {
			if err := repository.UpdatePasswordHash(c.Ctx(), a.DB, u.ID, h); err != nil {
				a.logger().Error("password rehash", "err", err)
			} else {
				u.PasswordHash = h
			}
		}
	}
	return true, nil
}

// authenticateWithAuthSources は AuthSource.authenticate（オンザフライ登録の認証方式を順に試し、
// 例外は記録して次へ進む）。
func (a *App) authenticateWithAuthSources(c *Req, login, pw string) *ldap.Attrs {
	recs, err := repository.OntheflyAuthSources(c.Ctx(), a.DB)
	if err != nil {
		a.logger().Error("Error during authentication", "err", err)
		return nil
	}
	for _, rec := range recs {
		src := a.ldapSource(rec)
		if src == nil {
			continue
		}
		attrs, err := src.Authenticate(login, pw)
		if err != nil {
			a.logger().Error("Error during authentication", "err", err)
			continue
		}
		if attrs != nil {
			return attrs
		}
	}
	return nil
}
