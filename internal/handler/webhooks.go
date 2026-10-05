// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package handler

// WebhooksController（app/controllers/webhooks_controller.rb。Redmine 7.0 Feature #29664）。
// 個人設定から開く「Webhook」画面（自分のフックの一覧・作成・編集・削除）。

import (
	"errors"
	"net/http"
	"slices"
	"strconv"

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
	// before_action :require_login / :check_enabled / :authorize、:find_webhook（edit / update / destroy）、
	// require_sudo_mode :create, :update, :destroy
	base := []ActionOption{RequireLogin(), Before(a.webhooksCheckEnabled), Before(a.webhooksAuthorize)}
	find := Before(a.findWebhook)
	sudo := RequireSudoMode()
	a.Handle(r, http.MethodGet, "/webhooks", ctrl, "index", a.WebhooksIndex, base...)
	a.Handle(r, http.MethodPost, "/webhooks", ctrl, "create", a.WebhooksCreate, append(slices.Clone(base), sudo)...)
	a.Handle(r, http.MethodGet, "/webhooks/new", ctrl, "new", a.WebhooksNew, base...)
	a.Handle(r, http.MethodGet, "/webhooks/{id}/edit", ctrl, "edit", a.WebhooksEdit, append(slices.Clone(base), find)...)
	a.Handle(r, http.MethodPatch, "/webhooks/{id}", ctrl, "update", a.WebhooksUpdate, append(slices.Clone(base), find, sudo)...)
	a.Handle(r, http.MethodPut, "/webhooks/{id}", ctrl, "update", a.WebhooksUpdate, append(slices.Clone(base), find, sudo)...)
	a.Handle(r, http.MethodDelete, "/webhooks/{id}", ctrl, "destroy", a.WebhooksDestroy, append(slices.Clone(base), find, sudo)...)
}

// webhooksCheckEnabled は check_enabled（render_403 unless Webhook.enabled?）。
func (a *App) webhooksCheckEnabled(c *Req) {
	if !a.webhooksEnabled() {
		c.Render403("")
	}
}

// webhooksAuthorize は authorize（deny_access unless User.current.allowed_to?(:use_webhooks, nil, global: true)）。
func (a *App) webhooksAuthorize(c *Req) {
	if !c.AllowedToGlobally(domain.Perm("use_webhooks")) {
		c.DenyAccess()
	}
}

// findWebhook は find_webhook（User.current.webhooks.find(params[:id])。無ければ 404）。
func (a *App) findWebhook(c *Req) {
	id, ok := c.Params().IntStrict("id")
	if !ok {
		c.Render404("")
		return
	}
	w, err := repository.GetUserWebhook(c.Ctx(), a.DB, a.Secrets, c.User.ID, id)
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
}

func newWebhookForm(c *Req, w *repository.Webhook) *webhookForm {
	f := &webhookForm{formModel: newFormModel(c, "webhook", w.ID), W: w}
	if w.ID != 0 {
		f.url = w.URL
		if w.Secret != "" {
			f.secret = w.Secret
		}
	}
	return f
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

// assign は @webhook.attributes = webhook_params（url / secret / active / events: [] / project_ids: []）。
func (f *webhookForm) assign(p *httpx.Params) {
	w := f.W
	if v, ok := p.StringOK("url"); ok {
		w.URL, f.url = v, v
	}
	if v, ok := p.StringOK("secret"); ok {
		w.Secret, f.secret = v, v
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
	setable, err := a.setableWebhookProjects(c, c.User)
	if err != nil {
		return false, err
	}
	// hook.projects = hook.projects.to_a & hook.setable_projects
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
		return repository.SaveWebhook(c.Ctx(), tx, a.Secrets, w, db.NewTime(a.now()))
	}); err != nil {
		return false, err
	}
	return true, nil
}

// webhookRow は index の 1 行。
type webhookRow struct {
	*repository.Webhook
	Projects []*domain.Project
}

// WebhooksIndex は index（@webhooks = webhooks.order(:url)）。
func (a *App) WebhooksIndex(c *Req) {
	ctx := c.Ctx()
	ws, err := repository.ListUserWebhooks(ctx, a.DB, a.Secrets, c.User.ID)
	if err != nil {
		a.internalError(c, "list webhooks", err)
		return
	}
	visible, err := c.Authz().VisibleProjectIDs(ctx)
	if err != nil {
		a.internalError(c, "visible projects", err)
		return
	}
	var rows []webhookRow
	cache := map[int64]*domain.Project{}
	for _, w := range ws {
		row := webhookRow{Webhook: w}
		// webhook.projects.visible
		for _, pid := range w.ProjectIDs {
			if !slices.Contains(visible, pid) {
				continue
			}
			p, ok := cache[pid]
			if !ok {
				if p, err = repository.GetProject(ctx, a.DB, pid); err != nil {
					a.internalError(c, "webhook project", err)
					return
				}
				cache[pid] = p
			}
			row.Projects = append(row.Projects, p)
		}
		rows = append(rows, row)
	}
	c.Render("webhooks/index", map[string]any{"Webhooks": rows})
}

func (a *App) renderWebhookForm(c *Req, tmpl string, f *webhookForm) {
	projects, err := a.setableWebhookProjects(c, c.User)
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

// WebhooksNew は new（@webhook = Webhook.new）。
func (a *App) WebhooksNew(c *Req) {
	a.renderWebhookForm(c, "webhooks/new", newWebhookForm(c, &repository.Webhook{UserID: c.User.ID}))
}

// WebhooksCreate は create（webhooks.build(webhook_params)）。
func (a *App) WebhooksCreate(c *Req) {
	mp := c.Params().Map("webhook")
	if mp == nil || !c.Params().Present("webhook") {
		// params.require(:webhook) → ActionController::ParameterMissing（400）
		httpx.BadRequest(c.W)
		c.Halt()
		return
	}
	f := newWebhookForm(c, &repository.Webhook{UserID: c.User.ID})
	f.assign(mp)
	ok, err := a.validateAndSaveWebhook(c, f)
	if err != nil {
		a.internalError(c, "save webhook", err)
		return
	}
	if !ok {
		a.renderWebhookForm(c, "webhooks/new", f)
		return
	}
	c.Redirect("/webhooks")
}

// WebhooksEdit は edit。
func (a *App) WebhooksEdit(c *Req) {
	w := c.value(webhookCtxKey{}).(*repository.Webhook)
	a.renderWebhookForm(c, "webhooks/edit", newWebhookForm(c, w))
}

// WebhooksUpdate は update（@webhook.update(webhook_params)）。
func (a *App) WebhooksUpdate(c *Req) {
	mp := c.Params().Map("webhook")
	if mp == nil || !c.Params().Present("webhook") {
		httpx.BadRequest(c.W)
		c.Halt()
		return
	}
	w := c.value(webhookCtxKey{}).(*repository.Webhook)
	f := newWebhookForm(c, w)
	f.assign(mp)
	ok, err := a.validateAndSaveWebhook(c, f)
	if err != nil {
		a.internalError(c, "save webhook", err)
		return
	}
	if !ok {
		a.renderWebhookForm(c, "webhooks/edit", f)
		return
	}
	c.Redirect("/webhooks")
}

// WebhooksDestroy は destroy。
func (a *App) WebhooksDestroy(c *Req) {
	w := c.value(webhookCtxKey{}).(*repository.Webhook)
	if err := repository.DeleteWebhook(c.Ctx(), a.DB, w.ID); err != nil {
		a.internalError(c, "delete webhook", err)
		return
	}
	c.Redirect("/webhooks")
}

// webhookPath は webhook_path(webhook)。
func webhookPath(id int64) string { return "/webhooks/" + strconv.FormatInt(id, 10) }
