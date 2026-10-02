package handler

import (
	"errors"
	"net/http"
	"slices"

	"github.com/mikuta0407/buropher/internal/repository"
)

// TwofaController（app/controllers/twofa_controller.rb）。main_menu = false。
var TwofaController = &Controller{Name: "twofa", MainMenu: false}

// routesTwofa は twofa コントローラのルートを登録する。
func (a *App) routesTwofa(r Router) {
	login := RequireLogin()
	active := Before(a.requireActiveTwofa)
	sudo := RequireSudoMode()
	noTwofa := Before(a.ensureUserHasNoTwofa)
	activate := Before(a.twofaActivateSetup)
	deactivate := Before(a.twofaDeactivateSetup)
	skip := Skip(FilterTwofaActivation)
	actOpts := []ActionOption{skip, login, active, sudo, noTwofa, activate}
	deactOpts := []ActionOption{login, active, sudo, deactivate}
	// match 'my/twofa/activate/init' / 'my/twofa/:scheme/activate/init', :via => :post
	a.Handle(r, http.MethodPost, "/my/twofa/activate/init", TwofaController, "activate_init", a.TwofaActivateInit, actOpts...)
	a.Handle(r, http.MethodPost, "/my/twofa/{scheme}/activate/init", TwofaController, "activate_init", a.TwofaActivateInit, actOpts...)
	// match 'my/twofa/:scheme/activate/confirm', :via => :get
	a.Handle(r, http.MethodGet, "/my/twofa/{scheme}/activate/confirm", TwofaController, "activate_confirm", a.TwofaActivateConfirm, actOpts...)
	// match 'my/twofa/:scheme/activate', :via => [:get, :post]
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		a.Handle(r, m, "/my/twofa/{scheme}/activate", TwofaController, "activate", a.TwofaActivate, actOpts...)
		a.Handle(r, m, "/my/twofa/{scheme}/deactivate", TwofaController, "deactivate", a.TwofaDeactivate, deactOpts...)
	}
	// match 'my/twofa/:scheme/deactivate/init', :via => :post / 'my/twofa/:scheme/deactivate/confirm', :via => :get
	a.Handle(r, http.MethodPost, "/my/twofa/{scheme}/deactivate/init", TwofaController, "deactivate_init", a.TwofaDeactivateInit, deactOpts...)
	a.Handle(r, http.MethodGet, "/my/twofa/{scheme}/deactivate/confirm", TwofaController, "deactivate_confirm", a.TwofaDeactivateConfirm, deactOpts...)
	// match 'my/twofa/select_scheme', :via => :get
	a.Handle(r, http.MethodGet, "/my/twofa/select_scheme", TwofaController, "select_scheme", a.TwofaSelectScheme, skip, login, active, sudo, noTwofa)
	// match 'users/:user_id/twofa/deactivate', :controller => 'twofa', :action => 'admin_deactivate', :via => :post
	a.Handle(r, http.MethodPost, "/users/{user_id}/twofa/deactivate", TwofaController, "admin_deactivate", a.TwofaAdminDeactivate, login, RequireAdmin(), active)
	a.routesTwofaBackupCodes(r)
}

// ensureUserHasNoTwofa は TwofaController#ensure_user_has_no_twofa。
func (a *App) ensureUserHasNoTwofa(c *Req) {
	if c.User.TwofaScheme == "" {
		return
	}
	c.Flash().SetWarning(c.L("twofa_already_setup"))
	c.Redirect("/my/account")
}

// twofaActivateSetup は TwofaController#activate_setup。
func (a *App) twofaActivateSetup(c *Req) {
	if !slices.Contains(twofaSchemes, c.Params().String("scheme")) {
		c.Redirect("/my/account")
		return
	}
	t, err := a.twofaFor(c, c.User)
	if err != nil {
		a.serverError(c, err)
		return
	}
	c.setLocal(ctxTwofa, t)
}

// twofaDeactivateSetup は TwofaController#deactivate_setup。
func (a *App) twofaDeactivateSetup(c *Req) {
	if c.Params().String("scheme") != c.User.TwofaScheme || c.User.TwofaScheme == "" {
		c.Redirect("/my/account")
		return
	}
	t, err := a.twofaFor(c, c.User)
	if err != nil {
		a.serverError(c, err)
		return
	}
	c.setLocal(ctxTwofa, t)
}

// TwofaSelectScheme は twofa#select_scheme。
func (a *App) TwofaSelectScheme(c *Req) {
	data := map[string]any{"Schemes": twofaSchemes, "MustActivateTwofa": a.mustActivateTwofa(c, c.User)}
	if err := a.mySidebarData(c, c.User, data); err != nil {
		a.serverError(c, err)
		return
	}
	c.Render("twofa/select_scheme", data)
}

// initTwofaPairingAndSendCodeFor は ApplicationController#init_twofa_pairing_and_send_code_for。
func (a *App) initTwofaPairingAndSendCodeFor(c *Req, t *twofaTotp) {
	if err := t.initPairing(); err != nil {
		a.serverError(c, err)
		return
	}
	// TOTP はコードを送信しない
	c.Redirect("/my/twofa/" + t.SchemeName() + "/activate/confirm")
}

// TwofaActivateInit は twofa#activate_init。
func (a *App) TwofaActivateInit(c *Req) { a.initTwofaPairingAndSendCodeFor(c, c.twofa()) }

// TwofaActivateConfirm は twofa#activate_confirm。
func (a *App) TwofaActivateConfirm(c *Req) {
	view, err := c.twofa().initPairingView()
	if err != nil {
		// 鍵が無い（activate_init を経ていない）・復号できない
		a.logger().Warn("twofa pairing view", "user", c.User.Login, "err", err)
		a.initTwofaPairingAndSendCodeFor(c, c.twofa())
		return
	}
	c.NoStore()
	data := map[string]any{"TwofaView": view, "MustActivateTwofa": a.mustActivateTwofa(c, c.User)}
	if err := a.mySidebarData(c, c.User, data); err != nil {
		a.serverError(c, err)
		return
	}
	c.Render("twofa/activate_confirm", data)
}

// TwofaActivate は twofa#activate。
func (a *App) TwofaActivate(c *Req) {
	t := c.twofa()
	ok, err := t.confirmPairing(c.Params().String("twofa_code"))
	if err != nil {
		a.serverError(c, err)
		return
	}
	if !ok {
		c.Flash().SetError(c.L("twofa_invalid_code"))
		c.Redirect("/my/twofa/" + t.SchemeName() + "/activate/confirm")
		return
	}
	// 2 要素認証の有効化でセッションが破棄されたので、新しいセッションで続ける
	if s := c.Session(); s != nil {
		s.Renew()
	}
	c.Flash().SetNotice(c.L("twofa_activated", map[string]any{"bc_path": "/my/twofa/backup_codes/init"}))
	c.Redirect("/my/account")
}

// TwofaDeactivateInit は twofa#deactivate_init。
func (a *App) TwofaDeactivateInit(c *Req) {
	c.Redirect("/my/twofa/" + c.twofa().SchemeName() + "/deactivate/confirm")
}

// TwofaDeactivateConfirm は twofa#deactivate_confirm。
func (a *App) TwofaDeactivateConfirm(c *Req) {
	data := map[string]any{"TwofaView": c.twofa().otpConfirmView()}
	if err := a.mySidebarData(c, c.User, data); err != nil {
		a.serverError(c, err)
		return
	}
	c.Render("twofa/deactivate_confirm", data)
}

// TwofaDeactivate は twofa#deactivate。
func (a *App) TwofaDeactivate(c *Req) {
	t := c.twofa()
	ok, err := t.destroyPairing(c.Params().String("twofa_code"))
	if err != nil {
		a.serverError(c, err)
		return
	}
	if !ok {
		c.Flash().SetError(c.L("twofa_invalid_code"))
		c.Redirect("/my/twofa/" + t.SchemeName() + "/deactivate/confirm")
		return
	}
	c.Flash().SetNotice(c.L("twofa_deactivated"))
	c.Redirect("/my/account")
}

// TwofaAdminDeactivate は twofa#admin_deactivate（管理者による他ユーザーの 2 要素認証の解除）。
func (a *App) TwofaAdminDeactivate(c *Req) {
	id, ok := c.Params().IntStrict("user_id")
	if !ok {
		c.Render404("")
		return
	}
	user, err := repository.GetUser(c.Ctx(), a.DB, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.Render404("")
		} else {
			a.serverError(c, err)
		}
		return
	}
	// 自分自身は確認なしに解除させない
	if user.ID == c.User.ID {
		c.Render403("")
		return
	}
	t, err := a.twofaFor(c, user)
	if err != nil {
		a.serverError(c, err)
		return
	}
	if err := t.destroyPairingWithoutVerify(); err != nil {
		a.serverError(c, err)
		return
	}
	c.Flash().SetNotice(c.L("twofa_deactivated"))
	c.Redirect("/users/" + itoa(user.ID) + "/edit")
}
