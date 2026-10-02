package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

// このファイルは AccountController のうち 2 要素認証のログイン 2 段目
// （twofa_confirm / twofa / twofa_resend と setup_twofa_session 等の private メソッド）の移植。

// twofaSessionValidity は twofa_setup の有効期間（0.0014 日 ≒ 2 分強）。
const twofaSessionValidity = time.Duration(0.0014 * 24 * float64(time.Hour))

// routesAccountTwofa は 2 要素認証のログイン 2 段目のルート（routesAccount から呼ぶ）。
func (a *App) routesAccountTwofa(r Router) {
	skip := Skip(FilterLoginRequired, FilterPasswordChange)
	// before_action :require_active_twofa, :twofa_setup, only: [:twofa_resend, :twofa_confirm, :twofa]
	// before_action :prevent_twofa_session_replay, only: [:twofa_resend, :twofa]
	setup := []ActionOption{skip, Before(a.requireActiveTwofa), Before(a.accountTwofaSetup)}
	replay := Before(a.preventTwofaSessionReplay)
	// match 'account/twofa/confirm', :to => 'account#twofa_confirm', :via => :get
	a.Handle(r, http.MethodGet, "/account/twofa/confirm", AccountController, "twofa_confirm", a.AccountTwofaConfirm, setup...)
	// match 'account/twofa/resend', :to => 'account#twofa_resend', :via => :post
	a.Handle(r, http.MethodPost, "/account/twofa/resend", AccountController, "twofa_resend", a.AccountTwofaResend, append(setup, replay)...)
	// match 'account/twofa', :to => 'account#twofa', :via => [:get, :post]
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		a.Handle(r, m, "/account/twofa", AccountController, "twofa", a.AccountTwofa, append(setup, replay)...)
	}
}

// requireActiveTwofa は TwofaHelper#require_active_twofa（Setting.twofa? でなければ deny_access）。
func (a *App) requireActiveTwofa(c *Req) {
	if a.Settings.String("twofa") == "0" {
		c.DenyAccess()
	}
}

const (
	ctxTwofaUser = "twofa_user"
	ctxTwofa     = "twofa"
)

// accountTwofaSetup は AccountController#twofa_setup。
func (a *App) accountTwofaSetup(c *Req) {
	s := c.Session()
	var user *domain.User
	if s != nil {
		tok, err := repository.FindToken(c.Ctx(), a.DB, repository.TokenTwofaSession, s.GetString("twofa_session_token"), 0, a.now())
		if err == nil && tok.CreatedAt.After(a.now().Add(-twofaSessionValidity)) {
			user = a.findActiveUser(c, tok.UserID)
		}
	}
	if user == nil {
		a.destroyTwofaSession(c)
		c.Redirect("/")
		return
	}
	// back_url と autologin をパラメータに戻す
	p := c.Params()
	if !p.Has("back_url") && s.Has("twofa_back_url") {
		p.Set("back_url", s.Get("twofa_back_url"))
	}
	if !p.Has("autologin") && s.Has("twofa_autologin") {
		p.Set("autologin", s.Get("twofa_autologin"))
	}
	a.setLocalization(c, user)
	t, err := a.twofaFor(c, user)
	if err != nil {
		a.serverError(c, err)
		return
	}
	c.setLocal(ctxTwofaUser, user)
	c.setLocal(ctxTwofa, t)
}

func (c *Req) twofaUser() *domain.User {
	u, _ := c.local(ctxTwofaUser).(*domain.User)
	return u
}

func (c *Req) twofa() *twofaTotp {
	t, _ := c.local(ctxTwofa).(*twofaTotp)
	return t
}

// setupTwofaSession は AccountController#setup_twofa_session。
func (a *App) setupTwofaSession(c *Req, user *domain.User, previousTries int64) error {
	token, err := repository.CreateToken(c.Ctx(), a.DB, user.ID, repository.TokenTwofaSession)
	if err != nil {
		return err
	}
	s := c.Session()
	p := c.Params()
	s.Set("twofa_session_token", token.Value)
	s.Set("twofa_tries_counter", previousTries)
	if v, ok := p.Get("back_url"); ok {
		s.Set("twofa_back_url", v)
	} else {
		s.Delete("twofa_back_url")
	}
	if v, ok := p.Get("autologin"); ok {
		s.Set("twofa_autologin", v)
	} else {
		s.Delete("twofa_autologin")
	}
	return nil
}

// preventTwofaSessionReplay は prevent_twofa_session_replay（twofa_session トークンを 1 リクエストで使い捨てにする）。
func (a *App) preventTwofaSessionReplay(c *Req) {
	tries := c.Session().GetInt("twofa_tries_counter") + 1
	a.destroyTwofaSession(c)
	if err := a.setupTwofaSession(c, c.twofaUser(), tries); err != nil {
		a.serverError(c, err)
	}
}

// destroyTwofaSession は AccountController#destroy_twofa_session。
func (a *App) destroyTwofaSession(c *Req) {
	s := c.Session()
	if s == nil {
		return
	}
	if tok, err := repository.FindToken(c.Ctx(), a.DB, repository.TokenTwofaSession, s.GetString("twofa_session_token"), 0, a.now()); err == nil {
		if err := repository.DeleteToken(c.Ctx(), a.DB, tok.UserID, tok.Action, tok.Value); err != nil {
			a.logger().Error("delete twofa session token", "err", err)
		}
	} else if !errors.Is(err, repository.ErrNotFound) {
		a.logger().Error("find twofa session token", "err", err)
	}
	for _, k := range []string{"twofa_session_token", "twofa_tries_counter", "twofa_back_url", "twofa_autologin"} {
		s.Delete(k)
	}
}

// startTwofaLogin は password_authentication のうち 2 要素認証が有効なユーザーの分岐。
func (a *App) startTwofaLogin(c *Req, user *domain.User) {
	if err := a.setupTwofaSession(c, user, 1); err != nil {
		a.serverError(c, err)
		return
	}
	// TOTP はコードを送信しない（send_code は false）
	c.Redirect("/account/twofa/confirm")
}

// AccountTwofaResend は account#twofa_resend（TOTP では送信しないため確認画面へ戻るだけ）。
func (a *App) AccountTwofaResend(c *Req) {
	if c.Session().GetInt("twofa_tries_counter") > 3 {
		a.destroyTwofaSession(c)
		c.Flash().SetError(c.L("twofa_too_many_tries"))
		c.Redirect("/")
		return
	}
	c.Redirect("/account/twofa/confirm")
}

// AccountTwofaConfirm は account#twofa_confirm。
func (a *App) AccountTwofaConfirm(c *Req) {
	c.NoStore()
	c.Render("account/twofa_confirm", map[string]any{"TwofaView": c.twofa().otpConfirmView()})
}

// AccountTwofa は account#twofa（コードの検証）。
func (a *App) AccountTwofa(c *Req) {
	user := c.twofaUser()
	ok, err := c.twofa().verify(c.Params().String("twofa_code"))
	if err != nil {
		a.serverError(c, err)
		return
	}
	switch {
	case ok:
		a.destroyTwofaSession(c)
		a.handleActiveUser(c, user)
	case c.Session().GetInt("twofa_tries_counter") > 3:
		// パスワード 1 回の入力につき OTP の入力は 3 回まで
		a.destroyTwofaSession(c)
		c.Flash().SetError(c.L("twofa_too_many_tries"))
		c.Redirect("/")
	default:
		c.Flash().SetError(c.L("twofa_invalid_code"))
		c.Redirect("/account/twofa/confirm")
	}
}
