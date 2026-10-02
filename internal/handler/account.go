package handler

import (
	"net/http"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
)

// AccountController（app/controllers/account_controller.rb）。
var AccountController = &Controller{Name: "account", MainMenu: false}

// routesAccount は account コントローラのルートを登録する。
func (a *App) routesAccount(r Router) {
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		// match 'login', :to => 'account#login', :as => 'signin', :via => [:get, :post]
		a.Handle(r, m, "/login", AccountController, "login", a.AccountLogin,
			Skip(FilterLoginRequired, FilterPasswordChange))
		// match 'logout', :to => 'account#logout', :as => 'signout', :via => [:get, :post]
		a.Handle(r, m, "/logout", AccountController, "logout", a.AccountLogout,
			Skip(FilterLoginRequired, FilterPasswordChange, FilterTwofaActivation))
	}
	a.routesAccountTwofa(r)
	a.routesAccountRecovery(r)
	a.routesOIDC(r)
}

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
		c.Render("account/login", a.ssoLoginData(c))
	}
}

// AccountLogout は account#logout（GET / POST /logout）。
func (a *App) AccountLogout(c *Req) {
	if c.User.Anonymous() {
		c.Redirect("/")
		return
	}
	if c.R.Method == http.MethodPost {
		// buropher 拡張: OIDC でログインしていれば IdP からもログアウトする（RP-Initiated Logout）
		dest := a.ssoLogoutURL(c)
		a.logoutUser(c)
		if dest != "" {
			c.Redirect(dest)
			return
		}
		c.Redirect("/")
		return
	}
	// ログアウトのフォームを表示する
	c.Render("account/logout", nil)
}

func (a *App) authenticateUser(c *Req) { a.passwordAuthentication(c) }

// passwordAuthentication は AccountController#password_authentication。
func (a *App) passwordAuthentication(c *Req) {
	p := c.Params()
	user, unsaved, err := a.tryToLoginBang(c, p.String("username"), p.String("password"), false)
	if err != nil {
		// rescue AuthSourceException => e（login アクション）: render_error :message => e.message
		a.logger().Error("An error occurred when authenticating "+p.String("username"), "err", err)
		c.RenderError(http.StatusInternalServerError, err.Error())
		return
	}
	switch {
	case unsaved != nil:
		// onthefly_creation_failed: session[:auth_source_registration] を設定して account/register を描画する
		a.ontheflyCreationFailed(c, unsaved)
	case user == nil:
		a.invalidCredentials(c)
	case user.Active():
		if !a.localLoginAllowed(c, user) {
			// buropher 拡張: SSO 必須モードでは管理者以外のパスワードログインを拒否する
			c.Flash().Now("error", c.L("buropher.sso.notice_password_login_disabled"))
			return
		}
		if user.TwofaActive() {
			a.startTwofaLogin(c, user)
			return
		}
		a.handleActiveUser(c, user)
	default:
		a.handleInactiveUser(c, user, "/login")
	}
}

// ontheflyCreationFailed は AccountController#onthefly_creation_failed（登録画面で不足している属性を入力させる）。
func (a *App) ontheflyCreationFailed(c *Req, m *userModel) {
	if s := c.Session(); s != nil && m.AuthSourceID != nil {
		s.Set("auth_source_registration", map[string]any{"login": m.Login, "auth_source_id": *m.AuthSourceID})
	}
	c.NoStore()
	a.renderRegister(c, m)
}

// handleActiveUser は AccountController#handle_active_user。
// afterLogin はログイン直後（セッション開始後・リダイレクト前）に行う処理（SSO のセッション情報の保存など）。
// セッションはレスポンスヘッダの送出時に保存されるため、リダイレクトより前に済ませる必要がある。
func (a *App) handleActiveUser(c *Req, user *domain.User, afterLogin ...func()) {
	a.successfulAuthentication(c, user, func() {
		// update_sudo_timestamp!（Redmine は successful_authentication の後に呼ぶが、
		// buropher のセッションはリダイレクトの送出時に保存されるため先に設定する）
		if s := c.Session(); s != nil {
			s.SetSudoAt(a.now())
		}
		for _, f := range afterLogin {
			f()
		}
	})
}

// successfulAuthentication は AccountController#successful_authentication。
func (a *App) successfulAuthentication(c *Req, user *domain.User, beforeRedirect func()) {
	a.logger().Info("Successful authentication", "login", user.Login, "ip", httpx.RemoteIP(c.R))
	a.setLoggedUser(c, user)
	if c.Params().Present("autologin") && a.Settings.Bool("autologin") {
		a.setAutologinCookie(c, user)
	}
	if beforeRedirect != nil {
		beforeRedirect()
	}
	c.RedirectBackOrDefault("/my/page", false)
}

// setAutologinCookie は AccountController#set_autologin_cookie。
func (a *App) setAutologinCookie(c *Req, user *domain.User) {
	token, err := repository.CreateToken(c.Ctx(), a.DB, user.ID, repository.TokenAutologin)
	if err != nil {
		a.logger().Error("autologin token", "err", err)
		return
	}
	secure := httpx.RequestScheme(c.R) == "https"
	if a.AutologinCookieSecure != nil {
		secure = *a.AutologinCookieSecure
	}
	http.SetCookie(c.W, &http.Cookie{
		Name: a.autologinCookieName(), Value: token.Value, Path: a.autologinCookiePath(),
		Expires: a.now().AddDate(1, 0, 0), SameSite: http.SameSiteLaxMode, Secure: secure, HttpOnly: true,
	})
}

// invalidCredentials は AccountController#invalid_credentials。
func (a *App) invalidCredentials(c *Req) {
	a.logger().Warn("Failed login", "login", c.Params().String("username"), "ip", httpx.RemoteIP(c.R), "at", a.now().UTC())
	c.Flash().Now("error", c.L("notice_account_invalid_credentials"))
}

// handleInactiveUser は AccountController#handle_inactive_user。
func (a *App) handleInactiveUser(c *Req, user *domain.User, redirectPath string) {
	if user.Registered() {
		a.accountPending(c, user, redirectPath)
	} else {
		a.accountLocked(c, redirectPath)
	}
}

// accountPending は AccountController#account_pending。
func (a *App) accountPending(c *Req, user *domain.User, redirectPath string) {
	if a.Settings.String("self_registration") == "1" {
		c.Flash().SetError(c.L("notice_account_not_activated_yet", map[string]any{"url": "/account/activation_email"}))
		c.Session().Set("registered_user_id", user.ID)
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
