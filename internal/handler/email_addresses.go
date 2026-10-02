package handler

import (
	"net/http"
	"strconv"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/validation"
)

// EmailAddressesController（app/controllers/email_addresses_controller.rb）。
// 追加のメールアドレスの管理（/users/:user_id/email_addresses）。layout は base。
var EmailAddressesController = &Controller{Name: "email_addresses", MainMenu: false}

// routesEmailAddresses は email_addresses コントローラのルートを登録する。
func (a *App) routesEmailAddresses(r Router) {
	opts := []ActionOption{a.findEmailUser(), a.requireAdminOrCurrentUser()}
	a.Handle(r, http.MethodGet, "/users/{user_id}/email_addresses", EmailAddressesController, "index", a.EmailAddressesIndex, opts...)
	a.Handle(r, http.MethodPost, "/users/{user_id}/email_addresses", EmailAddressesController, "create", a.EmailAddressesCreate, opts...)
	for _, m := range []string{http.MethodPatch, http.MethodPut} {
		a.Handle(r, m, "/users/{user_id}/email_addresses/{id}", EmailAddressesController, "update", a.EmailAddressesUpdate, append(opts, a.findEmailAddress())...)
	}
	a.Handle(r, http.MethodDelete, "/users/{user_id}/email_addresses/{id}", EmailAddressesController, "destroy", a.EmailAddressesDestroy, append(opts, a.findEmailAddress())...)
}

type emailAddressCtxKey struct{}

// findEmailUser は EmailAddressesController#find_user（User.find。見つからなければ 404）。
func (a *App) findEmailUser() ActionOption {
	return Before(func(c *Req) {
		id, err := strconv.ParseInt(c.Params().String("user_id"), 10, 64)
		if err != nil {
			c.Render404("")
			return
		}
		u, err := repository.GetUser(c.Ctx(), a.DB, id)
		if err != nil {
			c.Render404("")
			return
		}
		c.setValue(userCtxKey{}, u)
	})
}

// requireAdminOrCurrentUser は require_admin_or_current_user。
func (a *App) requireAdminOrCurrentUser() ActionOption {
	return Before(func(c *Req) {
		u := c.value(userCtxKey{}).(*domain.User)
		if u.ID != c.User.ID || !c.User.Logged() {
			c.RequireAdmin()
		}
	})
}

// findEmailAddress は find_email_address（既定でないアドレスのみ）。
func (a *App) findEmailAddress() ActionOption {
	return Before(func(c *Req) {
		u := c.value(userCtxKey{}).(*domain.User)
		id, err := strconv.ParseInt(c.Params().String("id"), 10, 64)
		if err != nil {
			c.Render404("")
			return
		}
		addr, err := repository.GetAdditionalEmailAddress(c.Ctx(), a.DB, u.ID, id)
		if err != nil {
			c.Render404("")
			return
		}
		c.setValue(emailAddressCtxKey{}, addr)
	})
}

// emailAddressForm は form_for @address のモデル。
type emailAddressForm struct {
	Address string
	errors  *validation.Errors
	c       *Req
}

func (m *emailAddressForm) ParamKey() string                     { return "email_address" }
func (m *emailAddressForm) Persisted() bool                      { return false }
func (m *emailAddressForm) ValidationErrors() *validation.Errors { return m.errors }
func (m *emailAddressForm) ErrorsOn(attr string) []string        { return m.errors.Messages(m.c.Loc, attr) }
func (m *emailAddressForm) HumanAttributeName(attr string) string {
	return validation.HumanAttributeName(m.c.Loc, "email_address", attr)
}
func (m *emailAddressForm) Send(method string) (any, bool) {
	if method == "address" {
		return m.Address, true
	}
	return nil, false
}

// renderEmailIndex は index（html: index.html、js: index.js）。
func (a *App) renderEmailIndex(c *Req, form *emailAddressForm) {
	u := c.value(userCtxKey{}).(*domain.User)
	addrs, err := repository.UserEmailAddresses(c.Ctx(), a.DB, u.ID, true)
	if err != nil {
		a.serverError(c, err)
		return
	}
	if form == nil {
		form = &emailAddressForm{errors: validation.New("email_address"), c: c}
	}
	data := map[string]any{"User": u, "Addresses": addrs, "Address": form, "MaxAdditional": a.Settings.Int("max_additional_emails")}
	if httpx.Format(c.R) == "js" {
		c.Render("email_addresses/index", data, RenderOptions{Format: "js"})
		return
	}
	c.Render("email_addresses/index", data)
}

// EmailAddressesIndex は email_addresses#index。
func (a *App) EmailAddressesIndex(c *Req) { a.renderEmailIndex(c, nil) }

// EmailAddressesCreate は email_addresses#create（Setting.max_additional_emails まで）。
func (a *App) EmailAddressesCreate(c *Req) {
	u := c.value(userCtxKey{}).(*domain.User)
	form := &emailAddressForm{errors: validation.New("email_address"), c: c}
	saved := false
	err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
		n, err := repository.CountUserEmailAddresses(c.Ctx(), tx, u.ID)
		if err != nil {
			return err
		}
		if n > a.Settings.Int("max_additional_emails") {
			return nil
		}
		if p := c.Params().Map("email_address"); p != nil {
			form.Address = domain.NormalizeEmail(p.String("address"))
		}
		if err := a.validateEmailAddress(c, form.errors, "address", form.Address, true, 0, false); err != nil {
			return err
		}
		if form.errors.Any() {
			return nil
		}
		if _, err := repository.CreateEmailAddress(c.Ctx(), tx, u.ID, form.Address, a.now()); err != nil {
			return err
		}
		saved = true
		return nil
	})
	if err != nil {
		a.serverError(c, err)
		return
	}
	if httpx.Format(c.R) == "js" {
		if saved {
			form = nil
		}
		a.renderEmailIndex(c, form)
		return
	}
	if saved {
		c.Redirect("/users/" + itoa(u.ID) + "/email_addresses")
		return
	}
	a.renderEmailIndex(c, form)
}

// EmailAddressesUpdate は email_addresses#update（notify の切り替え）。
func (a *App) EmailAddressesUpdate(c *Req) {
	u := c.value(userCtxKey{}).(*domain.User)
	addr := c.value(emailAddressCtxKey{}).(*domain.EmailAddress)
	if v := c.Params().String("notify"); v != "" {
		if err := repository.UpdateEmailNotify(c.Ctx(), a.DB, addr.ID, castBool(v), a.now()); err != nil {
			a.serverError(c, err)
			return
		}
	}
	if httpx.Format(c.R) == "js" {
		a.renderEmailIndex(c, nil)
		return
	}
	c.Redirect("/users/" + itoa(u.ID) + "/email_addresses")
}

// EmailAddressesDestroy は email_addresses#destroy。
func (a *App) EmailAddressesDestroy(c *Req) {
	u := c.value(userCtxKey{}).(*domain.User)
	addr := c.value(emailAddressCtxKey{}).(*domain.EmailAddress)
	err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
		if err := repository.DestroyEmailAddress(c.Ctx(), tx, addr.ID); err != nil {
			return err
		}
		// EmailAddress#destroy_tokens
		return repository.DeleteUserTokensByActions(c.Ctx(), tx, u.ID, "recovery")
	})
	if err != nil {
		a.serverError(c, err)
		return
	}
	if httpx.Format(c.R) == "js" {
		a.renderEmailIndex(c, nil)
		return
	}
	c.Redirect("/users/" + itoa(u.ID) + "/email_addresses")
}
