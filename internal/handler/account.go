package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/auth/password"
	"github.com/mikuta0407/buropher/internal/httpx"
)

// AccountController（app/controllers/account_controller.rb）。
var AccountController = &Controller{Name: "account", MainMenu: false}

// AccountLogin は account#login（GET / POST /login）。
func (a *App) AccountLogin(c *Req) {
	// no_store（ヘッダはレスポンス送出前に設定する必要があるため先に設定する）
	c.NoStore()
	if c.R.Method == http.MethodPost {
		a.authenticateUser(c)
	} else if c.User.Logged() {
		c.RedirectBackOrDefault("/", true)
	}
	if !c.Halted() {
		c.Render("account/login", nil)
	}
}

// AccountLogout は account#logout（GET / POST /logout）。
func (a *App) AccountLogout(c *Req) {
	if c.User.Anonymous() {
		c.Redirect("/")
		return
	}
	if c.R.Method == http.MethodPost {
		a.logoutUser(c)
		c.Redirect("/")
		return
	}
	// ログアウトのフォームを表示する
	c.Render("account/logout", nil)
}

func (a *App) authenticateUser(c *Req) { a.passwordAuthentication(c) }

// tryToLogin は User.try_to_login!(login, password, active_only)。
// TODO(auth): LDAP 等の外部認証（AuthSource.authenticate とオンザフライ登録）。
func (a *App) tryToLogin(c *Req, login, pw string, activeOnly bool) (*User, error) {
	login = strings.TrimSpace(login)
	if login == "" || pw == "" {
		return nil, nil
	}
	u, err := a.Users.FindByLogin(c.Ctx(), login)
	if err != nil || u == nil {
		return nil, err
	}
	if u.AuthSourceID != 0 {
		// 外部認証は未対応
		return nil, nil
	}
	ok, err := password.Verify(u.PasswordHash, pw)
	if err != nil {
		a.logger().Warn("password verify", "login", login, "err", err)
		return nil, nil
	}
	if !ok {
		return nil, nil
	}
	if !u.Active() && activeOnly {
		return nil, nil
	}
	// Redmine 形式（SHA1）のハッシュはログイン成功時に argon2id へ移行する
	if password.NeedsRehash(u.PasswordHash) {
		if h, err := password.Hash(pw); err == nil {
			if err := a.Users.UpdatePasswordHash(c.Ctx(), u.ID(), h); err != nil {
				a.logger().Error("password rehash", "err", err)
			} else {
				u.PasswordHash = h
			}
		}
	}
	if u.Active() {
		if err := a.Users.UpdateLastLogin(c.Ctx(), u.ID(), a.now()); err != nil {
			return nil, err
		}
	}
	return u, nil
}

// passwordAuthentication は AccountController#password_authentication。
func (a *App) passwordAuthentication(c *Req) {
	p := c.Params()
	user, err := a.tryToLogin(c, p.String("username"), p.String("password"), false)
	if err != nil {
		a.logger().Error("authentication", "err", err)
		c.RenderError(http.StatusInternalServerError, err.Error())
		return
	}
	switch {
	case user == nil:
		a.invalidCredentials(c)
	case user.Active():
		if user.TwofaActive() {
			// TODO(twofa): setup_twofa_session と account/twofa 画面。未実装の間は 2FA を迂回させない。
			c.Flash().SetError(c.L("notice_account_invalid_credentials"))
			c.Redirect("/login")
			return
		}
		a.handleActiveUser(c, user)
	default:
		a.handleInactiveUser(c, user, "/login")
	}
}

// handleActiveUser は AccountController#handle_active_user。
func (a *App) handleActiveUser(c *Req, user *User) {
	a.successfulAuthentication(c, user)
	// update_sudo_timestamp!
	if s := c.Session(); s != nil {
		s.SetSudoAt(a.now())
	}
}

// successfulAuthentication は AccountController#successful_authentication。
func (a *App) successfulAuthentication(c *Req, user *User) {
	a.logger().Info("Successful authentication", "login", user.Login(), "ip", httpx.RemoteIP(c.R))
	a.setLoggedUser(c, user)
	if c.Params().Present("autologin") && a.Settings.Bool("autologin") {
		a.setAutologinCookie(c, user)
	}
	c.RedirectBackOrDefault("/my/page", false)
}

// setAutologinCookie は AccountController#set_autologin_cookie。
func (a *App) setAutologinCookie(c *Req, user *User) {
	token, err := a.Users.CreateToken(c.Ctx(), user.ID(), "autologin")
	if err != nil {
		a.logger().Error("autologin token", "err", err)
		return
	}
	secure := httpx.RequestScheme(c.R) == "https"
	if a.AutologinCookieSecure != nil {
		secure = *a.AutologinCookieSecure
	}
	http.SetCookie(c.W, &http.Cookie{
		Name: a.autologinCookieName(), Value: token, Path: a.autologinCookiePath(),
		Expires: a.now().AddDate(1, 0, 0), SameSite: http.SameSiteLaxMode, Secure: secure, HttpOnly: true,
	})
}

// invalidCredentials は AccountController#invalid_credentials。
func (a *App) invalidCredentials(c *Req) {
	a.logger().Warn("Failed login", "login", c.Params().String("username"), "ip", httpx.RemoteIP(c.R), "at", time.Now().UTC())
	c.Flash().Now("error", c.L("notice_account_invalid_credentials"))
}

// handleInactiveUser は AccountController#handle_inactive_user。
func (a *App) handleInactiveUser(c *Req, user *User, redirectPath string) {
	if user.Registered() {
		a.accountPending(c, user, redirectPath)
	} else {
		a.accountLocked(c, redirectPath)
	}
}

// accountPending は AccountController#account_pending。
func (a *App) accountPending(c *Req, user *User, redirectPath string) {
	if a.Settings.String("self_registration") == "1" {
		c.Flash().SetError(c.L("notice_account_not_activated_yet", map[string]any{"url": "/account/activation_email"}))
		c.Session().Set("registered_user_id", user.ID())
	} else {
		c.Flash().SetError(c.L("notice_account_pending"))
	}
	c.Redirect(redirectPath)
}

// accountLocked は AccountController#account_locked。
func (a *App) accountLocked(c *Req, redirectPath string) {
	c.Flash().SetError(c.L("notice_account_locked"))
	c.Redirect(redirectPath)
}
