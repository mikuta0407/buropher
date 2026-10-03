// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/urlroot"
)

// TwofaBackupCodesController（app/controllers/twofa_backup_codes_controller.rb）。main_menu = false。
//
// Redmine はバックアップコードを tokens に平文で保存し、生成直後の表示（show）は flash に入れたトークン ID で
// 引き直す。buropher はコードを SHA-256 で保存するため、生成した平文のコードを生成時刻とともにセッションに入れ、
// show で取り出して消す（5 分を過ぎたものは表示しない点は Redmine と同じ）。
var TwofaBackupCodesController = &Controller{Name: "twofa_backup_codes", MainMenu: false}

// routesTwofaBackupCodes は twofa_backup_codes コントローラのルート（routesTwofa から呼ぶ）。
func (a *App) routesTwofaBackupCodes(r Router) {
	// before_action :require_login, :require_active_twofa / before_action :twofa_setup / require_sudo_mode
	opts := []ActionOption{RequireLogin(), Before(a.requireActiveTwofa), Before(a.backupCodesTwofaSetup), RequireSudoMode()}
	// match 'my/twofa/backup_codes/init', :via => :post
	a.Handle(r, http.MethodPost, "/my/twofa/backup_codes/init", TwofaBackupCodesController, "init", a.TwofaBackupCodesInit, opts...)
	// match 'my/twofa/backup_codes/confirm', :via => :get
	a.Handle(r, http.MethodGet, "/my/twofa/backup_codes/confirm", TwofaBackupCodesController, "confirm", a.TwofaBackupCodesConfirm, opts...)
	// match 'my/twofa/backup_codes/create', :via => [:get, :post]
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		a.Handle(r, m, "/my/twofa/backup_codes/create", TwofaBackupCodesController, "create", a.TwofaBackupCodesCreate, opts...)
	}
	// match 'my/twofa/backup_codes', :via => [:get]
	a.Handle(r, http.MethodGet, "/my/twofa/backup_codes", TwofaBackupCodesController, "show", a.TwofaBackupCodesShow, opts...)
}

// backupCodesTwofaSetup は TwofaBackupCodesController#twofa_setup。
// Redmine は 2 要素認証が無効なユーザーでは @twofa が nil になり 500 になるが、ここではマイアカウントへ戻す。
func (a *App) backupCodesTwofaSetup(c *Req) {
	if c.User.TwofaScheme == "" {
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

// TwofaBackupCodesInit は twofa_backup_codes#init。
func (a *App) TwofaBackupCodesInit(c *Req) { c.Redirect("/my/twofa/backup_codes/confirm") }

// TwofaBackupCodesConfirm は twofa_backup_codes#confirm。
func (a *App) TwofaBackupCodesConfirm(c *Req) {
	c.NoStore()
	data := map[string]any{"TwofaView": c.twofa().otpConfirmView()}
	if err := a.mySidebarData(c, c.User, data); err != nil {
		a.serverError(c, err)
		return
	}
	c.Render("twofa_backup_codes/confirm", data)
}

const sessionBackupCodes = "twofa_backup_codes"

// TwofaBackupCodesCreate は twofa_backup_codes#create。
func (a *App) TwofaBackupCodesCreate(c *Req) {
	t := c.twofa()
	ok, err := t.verify(c.Params().String("twofa_code"))
	if err != nil {
		a.serverError(c, err)
		return
	}
	if !ok {
		c.Flash().SetError(c.L("twofa_invalid_code"))
		c.Redirect("/my/twofa/backup_codes/confirm")
		return
	}
	prev, err := twofaBackupCodesCreatedAt(c)
	if err != nil {
		a.serverError(c, err)
		return
	}
	if prev != nil {
		c.Flash().SetWarning(c.L("twofa_warning_backup_codes_generated_invalidated", map[string]any{"time": c.Loc.FormatTime(*prev, true)}))
	} else {
		c.Flash().SetNotice(c.L("twofa_notice_backup_codes_generated"))
	}
	codes, err := t.initBackupCodes()
	if err != nil {
		a.serverError(c, err)
		return
	}
	c.Session().Set(sessionBackupCodes, map[string]any{"codes": strings.Join(codes, ","), "created_at": a.now().Unix()})
	c.Redirect("/my/twofa/backup_codes")
}

func twofaBackupCodesCreatedAt(c *Req) (*time.Time, error) {
	return repository.TwofaBackupCodesCreatedAt(c.Ctx(), c.App.DB, c.User.ID)
}

// TwofaBackupCodesShow は twofa_backup_codes#show。
func (a *App) TwofaBackupCodesShow(c *Req) {
	s := c.Session()
	v, _ := s.Get(sessionBackupCodes).(map[string]any)
	s.Delete(sessionBackupCodes)
	var codes []string
	var created time.Time
	if v != nil {
		if str, _ := v["codes"].(string); str != "" {
			codes = strings.Split(str, ",")
		}
		switch n := v["created_at"].(type) {
		case float64:
			created = time.Unix(int64(n), 0)
		case int64:
			created = time.Unix(n, 0)
		}
	}
	if len(codes) == 0 || !created.After(a.now().Add(-5*time.Minute)) {
		c.Flash().SetWarning(c.L("twofa_backup_codes_already_shown", map[string]any{"bc_path": urlroot.Path("/my/twofa/backup_codes/init")}))
		c.Redirect("/my/account")
		return
	}
	grouped := make([]string, len(codes))
	for i, code := range codes {
		grouped[i] = groupsOf4(code)
	}
	c.NoStore()
	data := map[string]any{"BackupCodes": grouped, "CreatedAt": created}
	if err := a.mySidebarData(c, c.User, data); err != nil {
		a.serverError(c, err)
		return
	}
	c.Render("twofa_backup_codes/show", data)
}
