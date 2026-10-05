// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

// WebhooksController（app/controllers/webhooks_controller.rb。Redmine 7.0 Feature #29664）。
// 個人設定から開く「Webhook」画面（自分のフックの一覧・作成・編集・削除）。

import (
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/webhook"
)

// WebhooksController（self.main_menu = false）。
var WebhooksController = &Controller{Name: "webhooks", MainMenu: false}

type webhookCtxKey struct{}

// routesWebhooks は resources :webhooks, only: [:index, :new, :create, :edit, :update, :destroy]。
func (a *App) routesWebhooks(r Router) {
	ctrl := WebhooksController
	// before_action :require_login、
	// :check_enabled_or_admin（edit / update / destroy）/ :check_enabled（それ以外）、:authorize、
	// :find_webhook（edit / update / destroy）、require_sudo_mode :create, :update, :destroy
	base := []ActionOption{RequireLogin(), Before(a.webhooksCheckEnabled), Before(a.webhooksAuthorize)}
	member := []ActionOption{RequireLogin(), Before(a.webhooksCheckEnabledOrAdmin), Before(a.webhooksAuthorize), Before(a.findWebhook)}
	sudo := RequireSudoMode()
	a.Handle(r, http.MethodGet, "/webhooks", ctrl, "index", a.WebhooksIndex, base...)
	a.Handle(r, http.MethodPost, "/webhooks", ctrl, "create", a.WebhooksCreate, append(slices.Clone(base), sudo)...)
	a.Handle(r, http.MethodGet, "/webhooks/new", ctrl, "new", a.WebhooksNew, base...)
	a.Handle(r, http.MethodGet, "/webhooks/{id}/edit", ctrl, "edit", a.WebhooksEdit, member...)
	a.Handle(r, http.MethodPatch, "/webhooks/{id}", ctrl, "update", a.WebhooksUpdate, append(slices.Clone(member), sudo)...)
	a.Handle(r, http.MethodPut, "/webhooks/{id}", ctrl, "update", a.WebhooksUpdate, append(slices.Clone(member), sudo)...)
	a.Handle(r, http.MethodDelete, "/webhooks/{id}", ctrl, "destroy", a.WebhooksDestroy, append(slices.Clone(member), sudo)...)
}

// webhooksCheckEnabled は check_enabled（render_403 unless Webhook.enabled?）。
func (a *App) webhooksCheckEnabled(c *Req) {
	if !a.webhooksEnabled() {
		c.Render403("")
	}
}

// webhooksCheckEnabledOrAdmin は check_enabled_or_admin（Redmine 7.0.2: 無効でも管理者は既存のフックを
// 編集・削除できる。render_403 unless Webhook.enabled? || User.current.admin?）。
func (a *App) webhooksCheckEnabledOrAdmin(c *Req) {
	if !a.webhooksEnabled() && !c.User.IsAdmin() {
		c.Render403("")
	}
}

// webhooksAuthorize は authorize（deny_access unless User.current.allowed_to?(:use_webhooks, nil, global: true)）。
func (a *App) webhooksAuthorize(c *Req) {
	if !c.AllowedToGlobally(domain.Perm("use_webhooks")) {
		c.DenyAccess()
	}
}

// findWebhook は find_webhook（Webhook.editable.find(params[:id])。管理者は全員のフック、
// それ以外は自分のフックだけ。無ければ 404）。
func (a *App) findWebhook(c *Req) {
	id, ok := c.Params().IntStrict("id")
	if !ok {
		c.Render404("")
		return
	}
	w, err := repository.GetWebhook(c.Ctx(), a.DB, a.Secrets, id)
	if err == nil && !w.Editable(c.User.ID, c.User.IsAdmin()) {
		err = repository.ErrNotFound
	}
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			c.Render404("")
		} else {
			a.internalError(c, "find webhook", err)
		}
		return
	}
	c.setValue(webhookCtxKey{}, w)
}

// webhookForm は @webhook（labelled_form_for のモデル）。
type webhookForm struct {
	formModel
	W *repository.Webhook
	// url / secret は代入されていなければ nil（text_field に value 属性を出さない）。
	url, secret any
	// Owner は @webhook.user（フォームの「ユーザー」欄と setable_projects に使う）。
	Owner *domain.User
	// OtherOwner は @webhook.persisted? && @webhook.user != User.current（管理者が他人のフックを
	// 編集している。所有者を表示し、secret は表示しない。Redmine 7.0.2）。
	OtherOwner bool
	// currentUserID は safe_attributes= の user（User.current）。
	currentUserID int64
}

func newWebhookForm(c *Req, w *repository.Webhook, owner *domain.User) *webhookForm {
	f := &webhookForm{formModel: newFormModel(c, "webhook", w.ID), W: w, Owner: owner, currentUserID: c.User.ID}
	f.OtherOwner = w.ID != 0 && w.UserID != c.User.ID
	if w.ID != 0 {
		f.url = w.URL
		if w.Secret != "" {
			f.secret = w.Secret
		}
	}
	return f
}

// SecretValue は f.text_field :secret, :value => (他人のフックなら '' / それ以外は @webhook.secret)。
func (f *webhookForm) SecretValue() any {
	if f.OtherOwner {
		return ""
	}
	return f.secret
}

// Send はフォームの属性（url / secret / active）。
func (f *webhookForm) Send(method string) (any, bool) {
	switch method {
	case "url":
		return f.url, true
	case "secret":
		return f.secret, true
	case "active":
		return f.W.Active, true
	}
	return nil, false
}

// HasEvent は @webhook.events.include?(name)。
func (f *webhookForm) HasEvent(name string) bool { return slices.Contains(f.W.Events.V, name) }

// assign は @webhook.safe_attributes = params[:webhook]（url / secret / active / events / project_ids）。
// 保存済みの他人のフックで secret が空なら secret は変えない（Redmine 7.0.2）。
func (f *webhookForm) assign(p *httpx.Params) {
	w := f.W
	if v, ok := p.StringOK("url"); ok {
		w.URL, f.url = v, v
	}
	if v, ok := p.StringOK("secret"); ok {
		if !(strings.TrimSpace(v) == "" && w.ID != 0 && w.UserID != f.currentUserID) {
			w.Secret, f.secret = v, v
		}
	}
	if v, ok := p.StringOK("active"); ok {
		w.Active = castBool(v)
	}
	// events: [] / project_ids: [] は配列のときだけ許可される（スカラーは unpermitted として無視）
	if raw, ok := p.Get("events"); ok {
		if _, isArr := raw.([]any); isArr {
			w.Events = db.NewJSON(p.Strings("events"))
		}
	}
	if raw, ok := p.Get("project_ids"); ok {
		if _, isArr := raw.([]any); isArr {
			w.ProjectIDs = paramIDs(p.Strings("project_ids"))
		}
	}
}

// webhookEventGroup は _form の 1 種別（fieldset）。
type webhookEventGroup struct {
	Type   string
	Label  string
	Events []webhookEventItem
}

type webhookEventItem struct {
	Name  string
	Label string
}

// webhookEventGroups は @webhook.setable_events.keys.sort の各種別の表示。
func webhookEventGroups(c *Req) []webhookEventGroup {
	var out []webhookEventGroup
	for _, e := range webhook.SortedEvents() {
		humanized := humanizeType(e.Type)
		// l(:"label_#{type}_plural", :default => type.to_s.humanize.pluralize)
		g := webhookEventGroup{Type: e.Type, Label: lDefault(c, "label_"+e.Type+"_plural", humanized+"s", nil)}
		for _, action := range e.Actions {
			name := e.Type + "." + action
			// l(:"webhook_event_#{action}", :object_name => type.to_s.humanize, ...).capitalize
			label := lDefault(c, "webhook_event_"+action, humanizeType(e.Type+"_"+action), i18n.Vars{"object_name": humanized})
			g.Events = append(g.Events, webhookEventItem{Name: name, Label: RubyCapitalize(label)})
		}
		out = append(out, g)
	}
	return out
}

// lDefault は l(key, :default => def)。
func lDefault(c *Req, key, def string, vars i18n.Vars) string {
	return i18n.RubyToS(c.Loc.Bundle.TranslateDefault(c.Loc.Lang, key, vars, def))
}

// humanizeType は String#humanize（"time_entry" → "Time entry"）。
func humanizeType(s string) string {
	b := []rune(s)
	for i, r := range b {
		if r == '_' {
			b[i] = ' '
		}
	}
	return RubyCapitalize(string(b))
}

// setableProjects は Webhook#setable_projects（user が閲覧でき use_webhooks 権限のあるプロジェクト）。
func (a *App) setableWebhookProjects(c *Req, user *domain.User) ([]*domain.Project, error) {
	ctx := c.Ctx()
	az := c.Authz()
	if user.ID != c.User.ID {
		az = authz.New(a.DB, user)
	}
	ids, err := az.VisibleProjectIDs(ctx)
	if err != nil {
		return nil, err
	}
	all, err := repository.ListProjects(ctx, a.DB)
	if err != nil {
		return nil, err
	}
	var out []*domain.Project
	for _, p := range all {
		if !slices.Contains(ids, p.ID) {
			continue
		}
		ok, err := az.AllowedTo(ctx, domain.Perm("use_webhooks"), p)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, p)
		}
	}
	return out, nil
}

// validateAndSaveWebhook は before_validation（projects を setable_projects に絞る）・検証・保存。
// 検証エラーなら false。
func (a *App) validateAndSaveWebhook(c *Req, f *webhookForm) (bool, error) {
	w := f.W
	// setable_projects は self.user（所有者）を基準にする
	setable, err := a.setableWebhookProjects(c, f.Owner)
	if err != nil {
		return false, err
	}
	// hook.projects = hook.projects.select {|p| setable_projects の id に含まれる }
	var keep []int64
	for _, id := range w.ProjectIDs {
		if slices.ContainsFunc(setable, func(p *domain.Project) bool { return p.ID == id }) && !slices.Contains(keep, id) {
			keep = append(keep, id)
		}
	}
	w.ProjectIDs = keep
	errs, events := webhook.Validate(c.Ctx(), a.webhookValidator(), w.URL, w.Secret, w.Events.V)
	w.Events = db.NewJSON(events)
	for _, e := range errs {
		f.errs.Add(e.Attr, e.Key, e.Opts)
	}
	if f.errs.Any() {
		return false, nil
	}
	if err := a.DB.WithTx(c.Ctx(), func(tx *db.Tx) error {
		return repository.SaveWebhook(c.Ctx(), tx, w, db.NewTime(a.now()))
	}); err != nil {
		return false, err
	}
	return true, nil
}

// webhookRow は webhooks/_list の 1 行。
type webhookRow struct {
	*repository.Webhook
	// User は webhook.user（show_author のときだけ読み込む）。
	User     *domain.User
	Projects []*domain.Project
}

// webhookRows は webhooks/_list の行（webhook.projects.to_a.select(&:visible?)）。
// withUsers なら所有者も読み込む。
func (a *App) webhookRows(c *Req, ws []*repository.Webhook, withUsers bool) ([]webhookRow, error) {
	ctx := c.Ctx()
	visible, err := c.Authz().VisibleProjectIDs(ctx)
	if err != nil {
		return nil, err
	}
	all, err := repository.ListProjects(ctx, a.DB)
	if err != nil {
		return nil, err
	}
	users := map[int64]*domain.User{}
	var rows []webhookRow
	for _, w := range ws {
		row := webhookRow{Webhook: w}
		if withUsers {
			u, ok := users[w.UserID]
			if !ok {
				if u, err = repository.GetUser(ctx, a.DB, w.UserID); err != nil && !errors.Is(err, repository.ErrNotFound) {
					return nil, err
				}
				users[w.UserID] = u
			}
			row.User = u
		}
		// projects_webhooks の行順 = 保存時の project_id 順
		for _, id := range w.ProjectIDs {
			for _, p := range all {
				if p.ID == id && slices.Contains(visible, p.ID) {
					row.Projects = append(row.Projects, p)
				}
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// WebhooksIndex は index（@webhooks = webhooks.preload(:projects).order(:url)）。
func (a *App) WebhooksIndex(c *Req) {
	ws, err := repository.ListUserWebhooks(c.Ctx(), a.DB, a.Secrets, c.User.ID)
	if err != nil {
		a.internalError(c, "list webhooks", err)
		return
	}
	rows, err := a.webhookRows(c, ws, false)
	if err != nil {
		a.internalError(c, "webhook rows", err)
		return
	}
	c.Render("webhooks/index", map[string]any{"Webhooks": rows})
}

func (a *App) renderWebhookForm(c *Req, tmpl string, f *webhookForm) {
	projects, err := a.setableWebhookProjects(c, f.Owner)
	if err != nil {
		a.internalError(c, "webhook projects", err)
		return
	}
	c.Render(tmpl, map[string]any{
		"Webhook":     f,
		"EventGroups": webhookEventGroups(c),
		"Projects":    projects,
	})
}

// assignWebhookParams は @webhook.safe_attributes = params[:webhook]（ハッシュでなければ何もしない）。
func assignWebhookParams(c *Req, f *webhookForm) {
	if mp := c.Params().Map("webhook"); mp != nil {
		f.assign(mp)
	}
}

// WebhooksNew は new（@webhook = Webhook.new; @webhook.safe_attributes = params[:webhook]）。
func (a *App) WebhooksNew(c *Req) {
	f := newWebhookForm(c, &repository.Webhook{UserID: c.User.ID}, c.User)
	assignWebhookParams(c, f)
	a.renderWebhookForm(c, "webhooks/new", f)
}

// WebhooksCreate は create（webhooks.build; @webhook.safe_attributes = params[:webhook]）。
func (a *App) WebhooksCreate(c *Req) {
	f := newWebhookForm(c, &repository.Webhook{UserID: c.User.ID}, c.User)
	assignWebhookParams(c, f)
	ok, err := a.validateAndSaveWebhook(c, f)
	if err != nil {
		a.internalError(c, "save webhook", err)
		return
	}
	if !ok {
		a.renderWebhookForm(c, "webhooks/new", f)
		return
	}
	c.Flash().SetNotice(c.L("notice_successful_create"))
	c.RedirectBackOrDefault("/webhooks", false)
}

// editWebhookForm は edit / update の @webhook（所有者 @webhook.user を読み込む）。
func (a *App) editWebhookForm(c *Req) (*webhookForm, bool) {
	w := c.value(webhookCtxKey{}).(*repository.Webhook)
	owner := c.User
	if w.UserID != c.User.ID {
		u, err := repository.GetUser(c.Ctx(), a.DB, w.UserID)
		if err != nil {
			a.internalError(c, "webhook owner", err)
			return nil, false
		}
		owner = u
	}
	return newWebhookForm(c, w, owner), true
}

// WebhooksEdit は edit。
func (a *App) WebhooksEdit(c *Req) {
	f, ok := a.editWebhookForm(c)
	if !ok {
		return
	}
	a.renderWebhookForm(c, "webhooks/edit", f)
}

// WebhooksUpdate は update（@webhook.safe_attributes = params[:webhook]; @webhook.save）。
func (a *App) WebhooksUpdate(c *Req) {
	f, ok := a.editWebhookForm(c)
	if !ok {
		return
	}
	assignWebhookParams(c, f)
	saved, err := a.validateAndSaveWebhook(c, f)
	if err != nil {
		a.internalError(c, "save webhook", err)
		return
	}
	if !saved {
		a.renderWebhookForm(c, "webhooks/edit", f)
		return
	}
	c.Flash().SetNotice(c.L("notice_successful_update"))
	c.RedirectBackOrDefault("/webhooks", false)
}

// WebhooksDestroy は destroy。
func (a *App) WebhooksDestroy(c *Req) {
	w := c.value(webhookCtxKey{}).(*repository.Webhook)
	if err := repository.DeleteWebhook(c.Ctx(), a.DB, w.ID); err != nil {
		a.internalError(c, "delete webhook", err)
		return
	}
	c.Flash().SetNotice(c.L("notice_successful_delete"))
	c.RedirectBackOrDefault("/webhooks", false)
}

// AdminWebhooks は admin#webhooks（Redmine 7.0.2 Feature #44337。全ユーザーのフックの一覧）:
// @webhooks = Webhook.eager_load(:user).preload(:projects).order(*User.fields_for_order_statement, :url)。
func (a *App) AdminWebhooks(c *Req) {
	ws, err := repository.ListAllWebhooks(c.Ctx(), a.DB, a.Secrets, a.Settings.String("user_format"))
	if err != nil {
		a.internalError(c, "list webhooks", err)
		return
	}
	rows, err := a.webhookRows(c, ws, true)
	if err != nil {
		a.internalError(c, "webhook rows", err)
		return
	}
	c.renderAdmin("admin/webhooks", map[string]any{"Webhooks": rows, "WebhooksEnabled": a.webhooksEnabled()}, false)
}
