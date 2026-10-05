// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

// Redmine 7.0 の Webhook（test/functional/webhooks_controller_test.rb・test/unit/webhook_test.rb・
// test/unit/webhook_payload_test.rb の移植）。

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/handler"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/jobs"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/server"
	"github.com/mikuta0407/buropher/internal/webhook"
)

// webhookEnv は Webhook のテスト環境（Developer ロールに use_webhooks を付け、Webhook を有効にする）。
type webhookEnv struct {
	t   *testing.T
	ctx context.Context
	srv *server.Server
	ts  *httptest.Server
	d   *db.DB
	app *handler.App
}

func newWebhookEnv(t *testing.T) *webhookEnv {
	t.Helper()
	srv, ts, d := newFixtureServerFull(t)
	e := &webhookEnv{t: t, ctx: context.Background(), srv: srv, ts: ts, d: d, app: srv.App()}
	// @role = Role.find_by_name 'Developer'; @role.permissions << :use_webhooks
	e.exec(`INSERT INTO role_permissions (role_id, permission, position) VALUES (2, 'use_webhooks', 100)`)
	if err := e.app.Settings.Set(e.ctx, "webhooks_enabled", "1"); err != nil {
		t.Fatal(err)
	}
	// テストでは名前解決しない（example.com は公開アドレスとして扱う）
	v := webhook.NewValidator(nil)
	v.Resolver = func(_ context.Context, host string) ([]netip.Addr, error) {
		switch strings.ToLower(host) {
		case "example.com", "www.example.com":
			return []netip.Addr{netip.MustParseAddr("93.184.215.14")}, nil
		}
		return nil, &net404{}
	}
	e.app.WebhookValidator = v
	return e
}

type net404 struct{}

func (*net404) Error() string { return "no such host" }

func (e *webhookEnv) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.d.Exec(e.ctx, q, args...); err != nil {
		e.t.Fatal(err)
	}
}

// createHook は create_hook（Webhook.create!）。
func (e *webhookEnv) createHook(userID int64, u string, active bool, events []string, projectIDs ...int64) *repository.Webhook {
	e.t.Helper()
	w := &repository.Webhook{URL: u, UserID: userID, Active: active, Events: db.NewJSON(events), ProjectIDs: projectIDs}
	if err := repository.SaveWebhook(e.ctx, e.d, w, db.NewTime(frozenTime)); err != nil {
		e.t.Fatal(err)
	}
	return w
}

func (e *webhookEnv) jobPayloads() []map[string]any {
	e.t.Helper()
	var rows []string
	if err := e.d.Select(e.ctx, &rows, `SELECT payload FROM jobs WHERE kind = ? ORDER BY id`, handler.JobWebhook); err != nil {
		e.t.Fatal(err)
	}
	var out []map[string]any
	for _, r := range rows {
		var p struct {
			HookID  int64  `json:"hook_id"`
			Payload string `json:"payload"`
		}
		if err := json.Unmarshal([]byte(r), &p); err != nil {
			e.t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(p.Payload), &m); err != nil {
			e.t.Fatal(err)
		}
		m["_hook_id"] = float64(p.HookID)
		out = append(out, m)
	}
	return out
}

// webhooks_controller_test.rb
func TestWebhooksController(t *testing.T) {
	e := newWebhookEnv(t)
	hook := e.createHook(3, "https://example.com/some/hook", false, []string{"issue.created", "issue.updated"}, 1)
	other := e.createHook(1, "https://example.com/other/hook", false, []string{"issue.created", "issue.updated"}, 1)

	t.Run("should require login", func(t *testing.T) {
		res, _ := get(t, newClient(t), e.ts.URL+"/webhooks")
		if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/login?back_url="+url.QueryEscape(e.ts.URL+"/webhooks")) {
			t.Errorf("status %d location %s", res.StatusCode, res.Header.Get("Location"))
		}
	})
	c := login(t, e.ts, "dlopper", "foo")
	t.Run("should get index", func(t *testing.T) {
		res, body := get(t, c, e.ts.URL+"/webhooks")
		if res.StatusCode != 200 {
			t.Fatalf("status %d", res.StatusCode)
		}
		if !strings.Contains(body, `<td title="`+hook.URL+`">`+hook.URL+"</td>") || strings.Contains(body, other.URL) {
			t.Errorf("index body:\n%s", body)
		}
		if !strings.Contains(body, `<code>issue.created</code>, <code>issue.updated</code>`) ||
			!strings.Contains(body, `<a href="/projects/ecookbook">eCookbook</a>`) {
			t.Errorf("events / projects not rendered:\n%s", body)
		}
	})
	t.Run("should return forbidden when disabled", func(t *testing.T) {
		_ = e.app.Settings.Set(e.ctx, "webhooks_enabled", "0")
		defer func() { _ = e.app.Settings.Set(e.ctx, "webhooks_enabled", "1") }()
		for _, p := range []string{"/webhooks", "/webhooks/new"} {
			if res, _ := get(t, c, e.ts.URL+p); res.StatusCode != 403 {
				t.Errorf("%s: status %d", p, res.StatusCode)
			}
		}
		// 個人設定のリンクも出さない（my_controller_test.rb test_my_account_should_toggle_webhook_link_with_setting）
		if _, body := get(t, c, e.ts.URL+"/my/account"); strings.Contains(body, "icon-webhook") {
			t.Error("webhook link should be hidden")
		}
	})
	t.Run("my account link", func(t *testing.T) {
		_, body := get(t, c, e.ts.URL+"/my/account")
		if strings.Count(body, `class="icon icon-webhook"`) != 1 {
			t.Error("webhook link should be shown")
		}
		// 権限の無いユーザーには出さない
		_, body = get(t, login(t, e.ts, "rhill", "foo"), e.ts.URL+"/my/account")
		if strings.Contains(body, "icon-webhook") {
			t.Error("webhook link should be hidden for rhill")
		}
	})
	t.Run("should deny without permission", func(t *testing.T) {
		if res, _ := get(t, login(t, e.ts, "rhill", "foo"), e.ts.URL+"/webhooks"); res.StatusCode != 403 {
			t.Errorf("status %d", res.StatusCode)
		}
	})
	t.Run("should get new", func(t *testing.T) {
		res, body := get(t, c, e.ts.URL+"/webhooks/new")
		if res.StatusCode != 200 {
			t.Fatalf("status %d", res.StatusCode)
		}
		for _, want := range []string{
			`<label for="webhook_url">URL<span class="required"> *</span></label><input size="60" type="text" name="webhook[url]" id="webhook_url" />`,
			`id="webhook_events_issue.created"`, `<fieldset id="wiki_page_events">`,
			`Time entry created`, `name="webhook[project_ids][]" value="1"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("missing %q", want)
			}
		}
		// dlopper が use_webhooks を持たないプロジェクト（onlinestore 等）は出さない
		if strings.Contains(body, `name="webhook[project_ids][]" value="2"`) {
			t.Error("project 2 should not be setable")
		}
	})
	t.Run("should create webhook", func(t *testing.T) {
		_, body := get(t, c, e.ts.URL+"/webhooks/new")
		res, _ := post(t, c, e.ts.URL+"/webhooks", url.Values{
			"authenticity_token":     {csrfToken(t, body)},
			"webhook[url]":           {"https://example.com/new/hook"},
			"webhook[secret]":        {"s3cret"},
			"webhook[events][]":      {"issue.created", ""},
			"webhook[project_ids][]": {"1", "2", ""},
		})
		if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/webhooks") {
			t.Fatalf("status %d", res.StatusCode)
		}
		ws, err := repository.ListUserWebhooks(e.ctx, e.d, e.app.Secrets, 3)
		if err != nil || len(ws) != 2 {
			t.Fatalf("webhooks %v %v", ws, err)
		}
		w := ws[0] // order(:url): new/hook < some/hook
		if w.URL != "https://example.com/new/hook" || w.Secret != "s3cret" || w.Active ||
			strings.Join(w.Events.V, ",") != "issue.created" || len(w.ProjectIDs) != 1 || w.ProjectIDs[0] != 1 {
			t.Errorf("created %+v", w)
		}
		// secret は Redmine と同じく平文で保存する
		var stored string
		if err := e.d.Get(e.ctx, &stored, `SELECT secret FROM webhooks WHERE id = ?`, w.ID); err != nil || stored != "s3cret" {
			t.Errorf("stored secret %q %v", stored, err)
		}
	})
	t.Run("should not create invalid webhook", func(t *testing.T) {
		_, body := get(t, c, e.ts.URL+"/webhooks/new")
		res, body := post(t, c, e.ts.URL+"/webhooks", url.Values{
			"authenticity_token": {csrfToken(t, body)},
			"webhook[url]":       {"http://127.0.0.1/hook"},
			"webhook[events][]":  {"issue.created", "invalid.event"},
		})
		if res.StatusCode != 200 || !strings.Contains(body, "errorExplanation") ||
			!strings.Contains(body, "<li>URL is invalid</li>") || !strings.Contains(body, "<li>Events is invalid</li>") {
			t.Errorf("status %d body:\n%s", res.StatusCode, body)
		}
	})
	t.Run("should get edit", func(t *testing.T) {
		res, body := get(t, c, e.ts.URL+"/webhooks/"+itoa(hook.ID)+"/edit")
		if res.StatusCode != 200 || !strings.Contains(body, `value="https://example.com/some/hook"`) {
			t.Errorf("status %d", res.StatusCode)
		}
	})
	t.Run("should update webhook", func(t *testing.T) {
		_, body := get(t, c, e.ts.URL+"/webhooks/"+itoa(hook.ID)+"/edit")
		res, _ := post(t, c, e.ts.URL+"/webhooks/"+itoa(hook.ID), url.Values{
			"authenticity_token": {csrfToken(t, body)}, "_method": {"patch"},
			"webhook[url]": {"https://example.com/updated/hook"},
		})
		if res.StatusCode != 302 {
			t.Fatalf("status %d", res.StatusCode)
		}
		w, err := repository.GetWebhook(e.ctx, e.d, e.app.Secrets, hook.ID)
		if err != nil || w.URL != "https://example.com/updated/hook" || strings.Join(w.Events.V, ",") != "issue.created,issue.updated" {
			t.Errorf("updated %+v %v", w, err)
		}
	})
	t.Run("edit should not find hook of other user", func(t *testing.T) {
		if res, _ := get(t, c, e.ts.URL+"/webhooks/"+itoa(other.ID)+"/edit"); res.StatusCode != 404 {
			t.Errorf("status %d", res.StatusCode)
		}
	})
	t.Run("should destroy webhook", func(t *testing.T) {
		_, body := get(t, c, e.ts.URL+"/webhooks/new")
		res, _ := post(t, c, e.ts.URL+"/webhooks/"+itoa(hook.ID), url.Values{
			"authenticity_token": {csrfToken(t, body)}, "_method": {"delete"},
		})
		if res.StatusCode != 302 {
			t.Fatalf("status %d", res.StatusCode)
		}
		if _, err := repository.GetWebhook(e.ctx, e.d, e.app.Secrets, hook.ID); err != repository.ErrNotFound {
			t.Errorf("not deleted: %v", err)
		}
	})
}

// webhooks_controller_test.rb のうち Redmine 7.0.2（Feature #44337）で追加された、管理者による他人のフックの
// 編集・削除、back_url、secret の扱い、flash のテスト。
func TestWebhooksControllerAdmin(t *testing.T) {
	e := newWebhookEnv(t)
	hook := e.createHook(3, "https://example.com/some/hook", false, []string{"issue.created", "issue.updated"}, 1)
	other := e.createHook(1, "https://example.com/other/hook", false, []string{"issue.created", "issue.updated"}, 1)
	setSecret := func(s string) { e.exec(`UPDATE webhooks SET secret = ? WHERE id = ?`, s, hook.ID) }
	reload := func() *repository.Webhook {
		t.Helper()
		w, err := repository.GetWebhook(e.ctx, e.d, e.app.Secrets, hook.ID)
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	editPath := e.ts.URL + "/webhooks/" + itoa(hook.ID) + "/edit"
	hookPath := e.ts.URL + "/webhooks/" + itoa(hook.ID)
	admin := login(t, e.ts, "admin", "admin")
	dlopper := login(t, e.ts, "dlopper", "foo")
	patch := func(c *http.Client, vals url.Values) *http.Response {
		t.Helper()
		_, body := get(t, c, e.ts.URL+"/my/account")
		vals.Set("authenticity_token", csrfToken(t, body))
		vals.Set("_method", "patch")
		res, _ := post(t, c, hookPath, vals)
		return res
	}

	t.Run("index should not list hooks of other users to admins", func(t *testing.T) {
		_, body := get(t, admin, e.ts.URL+"/webhooks")
		if !strings.Contains(body, ">"+other.URL+"</td>") || strings.Contains(body, ">"+hook.URL+"</td>") {
			t.Errorf("body:\n%s", body)
		}
	})
	t.Run("admin should edit hook of other user", func(t *testing.T) {
		res, body := get(t, admin, editPath)
		if res.StatusCode != 200 || !strings.Contains(body, `<input type="text" name="webhook_user" id="webhook_user" value="Dave Lopper" disabled="disabled" />`) {
			t.Errorf("status %d body:\n%s", res.StatusCode, body)
		}
	})
	t.Run("edit should not show the owner of ones own hook", func(t *testing.T) {
		_, body := get(t, dlopper, editPath)
		if strings.Contains(body, `id="webhook_user"`) {
			t.Error("owner field should not be shown")
		}
	})
	t.Run("edit should not disclose secret of other user", func(t *testing.T) {
		setSecret("v3rys3cret")
		_, body := get(t, admin, editPath)
		if !strings.Contains(body, `id="webhook_secret"`) || strings.Contains(body, "v3rys3cret") {
			t.Errorf("body:\n%s", body)
		}
		if !strings.Contains(body, "The secret of another user is not displayed.") {
			t.Error("missing webhook_secret_keep_info")
		}
	})
	t.Run("update should keep secret of other user when submitted blank", func(t *testing.T) {
		setSecret("v3rys3cret")
		patch(admin, url.Values{"webhook[url]": {hook.URL}, "webhook[secret]": {""}})
		if s := reload().Secret; s != "v3rys3cret" {
			t.Errorf("secret %q", s)
		}
	})
	t.Run("update should replace secret of other user when a new one is submitted", func(t *testing.T) {
		setSecret("v3rys3cret")
		patch(admin, url.Values{"webhook[url]": {hook.URL}, "webhook[secret]": {"newsecret"}})
		if s := reload().Secret; s != "newsecret" {
			t.Errorf("secret %q", s)
		}
	})
	t.Run("owner should see and be able to clear their own secret", func(t *testing.T) {
		setSecret("v3rys3cret")
		_, body := get(t, dlopper, editPath)
		if !strings.Contains(body, `value="v3rys3cret"`) {
			t.Error("owner should see the secret")
		}
		patch(dlopper, url.Values{"webhook[url]": {hook.URL}, "webhook[secret]": {""}})
		if s := reload().Secret; s != "" {
			t.Errorf("secret %q", s)
		}
	})
	t.Run("admin should update hook of other user without becoming its owner", func(t *testing.T) {
		res := patch(admin, url.Values{"webhook[url]": {"https://example.com/fixed/hook"}})
		if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/webhooks") {
			t.Fatalf("status %d location %s", res.StatusCode, res.Header.Get("Location"))
		}
		w := reload()
		if w.URL != "https://example.com/fixed/hook" || w.UserID != 3 {
			t.Errorf("updated %+v", w)
		}
		// flash[:notice] = l(:notice_successful_update)
		if _, body := get(t, admin, e.ts.URL+"/webhooks"); !strings.Contains(body, "Successful update.") {
			t.Error("missing flash notice")
		}
		// setable_projects は所有者（dlopper）を基準にする: admin でも dlopper が use_webhooks を持たない
		// プロジェクト 2 は付けられない
		patch(admin, url.Values{"webhook[project_ids][]": {"1", "2", ""}})
		if ids := reload().ProjectIDs; len(ids) != 1 || ids[0] != 1 {
			t.Errorf("project ids %v", ids)
		}
	})
	t.Run("admin should deactivate hook of other user", func(t *testing.T) {
		e.exec(`UPDATE webhooks SET active = ? WHERE id = ?`, true, hook.ID)
		patch(admin, url.Values{"webhook[active]": {"0"}})
		if reload().Active {
			t.Error("still active")
		}
	})
	t.Run("update should redirect to back_url", func(t *testing.T) {
		res := patch(admin, url.Values{"webhook[url]": {"https://example.com/fixed/hook"}, "back_url": {"/admin/webhooks"}})
		if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/admin/webhooks") {
			t.Errorf("status %d location %s", res.StatusCode, res.Header.Get("Location"))
		}
	})
	t.Run("create should redirect to back_url", func(t *testing.T) {
		_, body := get(t, dlopper, e.ts.URL+"/webhooks/new?back_url=%2Fadmin%2Fwebhooks")
		if !strings.Contains(body, `<input type="hidden" name="back_url" value="/admin/webhooks" autocomplete="off" />`) ||
			!strings.Contains(body, `<a href="/admin/webhooks">Cancel</a>`) {
			t.Errorf("new body:\n%s", body)
		}
		res, _ := post(t, dlopper, e.ts.URL+"/webhooks", url.Values{
			"authenticity_token":     {csrfToken(t, body)},
			"webhook[url]":           {"https://example.com/new/hook"},
			"webhook[events][]":      {"issue.created"},
			"webhook[project_ids][]": {"1"},
			"back_url":               {"/admin/webhooks"},
		})
		if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/admin/webhooks") {
			t.Errorf("status %d location %s", res.StatusCode, res.Header.Get("Location"))
		}
		if _, body := get(t, dlopper, e.ts.URL+"/webhooks"); !strings.Contains(body, "Successful creation.") {
			t.Error("missing flash notice")
		}
	})
	t.Run("new should prefill attributes from params", func(t *testing.T) {
		_, body := get(t, dlopper, e.ts.URL+"/webhooks/new?webhook%5Burl%5D=https%3A%2F%2Fexample.com%2Fprefilled")
		if !strings.Contains(body, `value="https://example.com/prefilled"`) {
			t.Error("url should be prefilled")
		}
	})
	t.Run("create without webhook params should render the form", func(t *testing.T) {
		_, body := get(t, dlopper, e.ts.URL+"/webhooks/new")
		res, body := post(t, dlopper, e.ts.URL+"/webhooks", url.Values{"authenticity_token": {csrfToken(t, body)}})
		if res.StatusCode != 200 || !strings.Contains(body, "errorExplanation") {
			t.Errorf("status %d", res.StatusCode)
		}
	})
	t.Run("admin should keep access to existing hooks when disabled", func(t *testing.T) {
		_ = e.app.Settings.Set(e.ctx, "webhooks_enabled", "0")
		defer func() { _ = e.app.Settings.Set(e.ctx, "webhooks_enabled", "1") }()
		for _, p := range []string{"/webhooks", "/webhooks/new"} {
			if res, _ := get(t, admin, e.ts.URL+p); res.StatusCode != 403 {
				t.Errorf("%s: status %d", p, res.StatusCode)
			}
		}
		// 管理者でないユーザーは自分のフックでも編集できない
		if res, _ := get(t, dlopper, editPath); res.StatusCode != 403 {
			t.Errorf("dlopper edit: status %d", res.StatusCode)
		}
		if res, _ := get(t, admin, editPath); res.StatusCode != 200 {
			t.Errorf("edit: status %d", res.StatusCode)
		}
		if res := patch(admin, url.Values{"webhook[url]": {"https://example.com/fixed/hook"}}); res.StatusCode != 302 {
			t.Errorf("update: status %d", res.StatusCode)
		}
		_, body := get(t, admin, e.ts.URL+"/my/account")
		res, _ := post(t, admin, hookPath, url.Values{"authenticity_token": {csrfToken(t, body)}, "_method": {"delete"}})
		if res.StatusCode != 302 {
			t.Fatalf("destroy: status %d", res.StatusCode)
		}
		if _, err := repository.GetWebhook(e.ctx, e.d, e.app.Secrets, hook.ID); err != repository.ErrNotFound {
			t.Errorf("not deleted: %v", err)
		}
		// flash[:notice] = l(:notice_successful_delete)
		if _, body := get(t, admin, e.ts.URL+"/admin/webhooks"); !strings.Contains(body, "Successful deletion.") {
			t.Error("missing flash notice")
		}
	})
}

// admin_controller_test.rb（Redmine 7.0.2 Feature #44337: 管理画面の Webhook 一覧）
func TestAdminWebhooks(t *testing.T) {
	e := newWebhookEnv(t)
	hook := e.createHook(3, "https://example.com/dlopper/hook", false, []string{"issue.created"}, 1)
	other := e.createHook(1, "https://example.com/admin/hook", false, []string{"issue.updated"}, 1)
	admin := login(t, e.ts, "admin", "admin")

	t.Run("index should link to webhooks", func(t *testing.T) {
		_, body := get(t, admin, e.ts.URL+"/admin")
		if !regexp.MustCompile(`<div id="admin-menu">[\s\S]*<a class="icon icon-webhook webhooks" href="/admin/webhooks">`).MatchString(body) {
			t.Error("admin menu should link to /admin/webhooks")
		}
	})
	t.Run("webhooks", func(t *testing.T) {
		res, body := get(t, admin, e.ts.URL+"/admin/webhooks")
		if res.StatusCode != 200 {
			t.Fatalf("status %d", res.StatusCode)
		}
		for _, want := range []string{
			`<tr id="webhook_` + itoa(hook.ID) + `"`,
			`<td><a class="user active" href="/users/3">Dave Lopper</a></td>`,
			`<td title="https://example.com/dlopper/hook">https://example.com/dlopper/hook</td>`,
			`href="/webhooks/` + itoa(hook.ID) + `/edit?back_url=%2Fadmin%2Fwebhooks"`,
			`<tr id="webhook_` + itoa(other.ID) + `"`,
			`<a class="icon icon-add" href="/webhooks/new?back_url=%2Fadmin%2Fwebhooks">`,
			`<li><a class="icon icon-webhook webhooks selected" href="/admin/webhooks">`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("missing %q", want)
			}
		}
		// order(*User.fields_for_order_statement, :url): Dave Lopper < Redmine Admin
		if strings.Index(body, `id="webhook_`+itoa(hook.ID)+`"`) > strings.Index(body, `id="webhook_`+itoa(other.ID)+`"`) {
			t.Error("webhooks should be ordered by user name")
		}
		if strings.Contains(body, `class="warning"`) {
			t.Error("warning should not be shown when enabled")
		}
	})
	t.Run("webhooks should remain accessible without creation link when disabled", func(t *testing.T) {
		_ = e.app.Settings.Set(e.ctx, "webhooks_enabled", "0")
		defer func() { _ = e.app.Settings.Set(e.ctx, "webhooks_enabled", "1") }()
		res, body := get(t, admin, e.ts.URL+"/admin/webhooks")
		if res.StatusCode != 200 || !strings.Contains(body, `<table class="list webhooks">`) ||
			strings.Contains(body, `<div class="contextual">`) ||
			!regexp.MustCompile(`<p class="warning">[^<]*<a href="/settings\?tab=integrations">Integrations</a>`).MatchString(body) {
			t.Errorf("status %d body:\n%s", res.StatusCode, body)
		}
	})
	t.Run("webhooks should be denied to non admin users", func(t *testing.T) {
		if res, _ := get(t, login(t, e.ts, "jsmith", "jsmith"), e.ts.URL+"/admin/webhooks"); res.StatusCode != 403 {
			t.Errorf("status %d", res.StatusCode)
		}
	})
	t.Run("webhooks without hooks", func(t *testing.T) {
		e.exec(`DELETE FROM webhook_projects`)
		e.exec(`DELETE FROM webhooks`)
		_, body := get(t, admin, e.ts.URL+"/admin/webhooks")
		if !strings.Contains(body, `<p class="nodata">No data to display</p>`) {
			t.Error("nodata should be shown")
		}
	})
}

// webhook_test.rb（Redmine 7.0.2: Webhook.editable / editable?）
func TestWebhookEditable(t *testing.T) {
	w := &repository.Webhook{UserID: 3}
	for _, tc := range []struct {
		userID int64
		admin  bool
		want   bool
	}{{1, true, true}, {3, false, true}, {2, false, false}, {0, false, false}} {
		if got := w.Editable(tc.userID, tc.admin); got != tc.want {
			t.Errorf("Editable(%d, %v) = %v", tc.userID, tc.admin, got)
		}
	}
}

// settings の統合タブ（Redmine 7.0 で api から integrations に改名し webhooks_enabled を追加）
func TestWebhooksSettingsTab(t *testing.T) {
	ts, _ := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")
	_, body := get(t, c, ts.URL+"/settings?tab=integrations")
	for _, want := range []string{
		`id="tab-integrations"`, `action="/settings/edit?tab=integrations"`,
		`<input type="checkbox" name="settings[webhooks_enabled]" id="settings_webhooks_enabled" value="1" />`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
}

// webhook_test.rb の hooks_for / trigger
func TestWebhookTrigger(t *testing.T) {
	e := newWebhookEnv(t)
	issue1 := webhookIssueEvent(1)

	t.Run("should find hooks for issue", func(t *testing.T) {
		e.exec(`DELETE FROM jobs`)
		hook := e.createHook(3, "https://example.com/some/hook", true, []string{"issue.created"}, 1)
		defer e.exec(`DELETE FROM webhooks`)
		e.app.TriggerIssueWebhooks(e.ctx, issue1("created"))
		e.app.TriggerIssueWebhooks(e.ctx, issue1("updated"))
		ps := e.jobPayloads()
		if len(ps) != 1 || ps[0]["type"] != "issue.created" || ps[0]["_hook_id"] != float64(hook.ID) {
			t.Errorf("payloads %v", ps)
		}
	})
	t.Run("should check permission when looking for hooks", func(t *testing.T) {
		e.exec(`DELETE FROM jobs`)
		e.createHook(3, "https://example.com/some/hook", true, []string{"issue.created"}, 1)
		defer e.exec(`DELETE FROM webhooks`)
		e.exec(`DELETE FROM role_permissions WHERE role_id = 2 AND permission = 'use_webhooks'`)
		defer e.exec(`INSERT INTO role_permissions (role_id, permission, position) VALUES (2, 'use_webhooks', 100)`)
		e.app.TriggerIssueWebhooks(e.ctx, issue1("created"))
		if ps := e.jobPayloads(); len(ps) != 0 {
			t.Errorf("payloads %v", ps)
		}
	})
	t.Run("should not find inactive hook", func(t *testing.T) {
		e.exec(`DELETE FROM jobs`)
		e.createHook(3, "https://example.com/some/hook", false, []string{"issue.created"}, 1)
		defer e.exec(`DELETE FROM webhooks`)
		e.app.TriggerIssueWebhooks(e.ctx, issue1("created"))
		if ps := e.jobPayloads(); len(ps) != 0 {
			t.Errorf("payloads %v", ps)
		}
	})
	t.Run("should not find hook of inactive user", func(t *testing.T) {
		e.exec(`DELETE FROM jobs`)
		e.createHook(1, "https://example.com/some/hook", true, []string{"issue.created"}, 1)
		defer e.exec(`DELETE FROM webhooks`)
		e.app.TriggerIssueWebhooks(e.ctx, issue1("created"))
		if ps := e.jobPayloads(); len(ps) != 1 {
			t.Fatalf("payloads %v", ps)
		}
		e.exec(`DELETE FROM jobs`)
		e.exec(`UPDATE principals SET status = 3 WHERE id = 1`)
		defer e.exec(`UPDATE principals SET status = 1 WHERE id = 1`)
		e.app.TriggerIssueWebhooks(e.ctx, issue1("created"))
		if ps := e.jobPayloads(); len(ps) != 0 {
			t.Errorf("payloads %v", ps)
		}
	})
	t.Run("trigger should not enqueue jobs when disabled", func(t *testing.T) {
		e.exec(`DELETE FROM jobs`)
		e.createHook(3, "https://example.com/some/hook", true, []string{"issue.created"}, 1)
		defer e.exec(`DELETE FROM webhooks`)
		_ = e.app.Settings.Set(e.ctx, "webhooks_enabled", "0")
		defer func() { _ = e.app.Settings.Set(e.ctx, "webhooks_enabled", "1") }()
		e.app.TriggerIssueWebhooks(e.ctx, issue1("created"))
		if ps := e.jobPayloads(); len(ps) != 0 {
			t.Errorf("payloads %v", ps)
		}
	})
	t.Run("should not find hook for invisible issue", func(t *testing.T) {
		e.exec(`DELETE FROM jobs`)
		// issue 6 は非公開の子プロジェクト（dlopper は非メンバー）。フックの対象に入っていても見えないので送らない
		e.createHook(3, "https://example.com/some/hook", true, []string{"issue.created"}, 5)
		defer e.exec(`DELETE FROM webhooks`)
		e.app.TriggerIssueWebhooks(e.ctx, webhookIssueEventP(6, 5)("created"))
		if ps := e.jobPayloads(); len(ps) != 0 {
			t.Errorf("payloads %v", ps)
		}
	})
}

// webhookIssueEvent は project 1 のチケット id の WebhookEvent を作る関数。
func webhookIssueEvent(id int64) func(string) []issues.WebhookEvent { return webhookIssueEventP(id, 1) }

func webhookIssueEventP(id, projectID int64) func(string) []issues.WebhookEvent {
	return func(action string) []issues.WebhookEvent {
		return []issues.WebhookEvent{{Action: action, IssueID: id, ProjectID: projectID}}
	}
}

func issue1Updated() []issues.WebhookEvent { return webhookIssueEvent(1)("updated") }

// TestWebhookPayloads は参照 Redmine 7.0.1 で計算したペイロード（testdata/webhook_payloads.json。
// tools/gen/dump-webhook-payloads.rb）と一致することを確認する（webhook_payload_test.rb）。
func TestWebhookPayloads(t *testing.T) {
	e := newWebhookEnv(t)
	b, err := os.ReadFile("testdata/webhook_payloads.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name      string          `json:"name"`
		Event     string          `json:"event"`
		ID        int64           `json:"id"`
		User      string          `json:"user"`
		JournalID *int64          `json:"journal_id"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	tsRe := regexp.MustCompile(`"timestamp":"[^"]*"`)
	// フィクスチャの DB の作り方の違いで日時がずれる（参照の DB は 9 時間前）ため、日時は形式だけ比べる
	timeRe := regexp.MustCompile(`"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ"`)
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			u, err := repository.FindUserByLogin(e.ctx, e.d, tc.User)
			if err != nil {
				t.Fatal(err)
			}
			var jid int64
			if tc.JournalID != nil {
				jid = *tc.JournalID
			}
			got, ok, err := e.app.WebhookPayload(e.ctx, u, tc.Event, tc.ID, jid)
			if err != nil || !ok {
				t.Fatalf("payload: ok=%v err=%v", ok, err)
			}
			if strings.HasSuffix(tc.Event, ".deleted") || tc.Event == "news.updated" {
				got = tsRe.ReplaceAll(got, []byte(`"timestamp":null`))
			}
			var want, have bytes.Buffer
			if err := json.Compact(&want, tc.Payload); err != nil {
				t.Fatal(err)
			}
			if err := json.Compact(&have, got); err != nil {
				t.Fatalf("invalid json %s: %v", got, err)
			}
			w := timeRe.ReplaceAllString(strings.NewReplacer(`\u003c`, "<", `\u003e`, ">", `\u0026`, "&").Replace(want.String()), `"TIME"`)
			h := timeRe.ReplaceAllString(strings.NewReplacer(`\u003c`, "<", `\u003e`, ">", `\u0026`, "&").Replace(have.String()), `"TIME"`)
			if w != h {
				t.Errorf("payload mismatch\nwant %s\n got %s", w, h)
			}
		})
	}
}

// 送信ジョブ（WebhookJob）: 署名付きで POST する。
func TestWebhookDelivery(t *testing.T) {
	e := newWebhookEnv(t)
	var mu sync.Mutex
	var got []*http.Request
	var bodies []string
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, r)
		bodies = append(bodies, string(b))
		mu.Unlock()
	}))
	defer recv.Close()
	port := recv.URL[strings.LastIndex(recv.URL, ":"):]
	e.app.WebhookExecutor = &webhook.Executor{ValidIPs: func(context.Context, string) []netip.Addr {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	}}
	hook := e.createHook(3, "https://example.com"+port+"/hook", true, []string{"issue.updated"}, 1)
	// https の代わりに http で受ける（ValidIPs で接続先を固定しているので URL は http にする）
	e.exec(`UPDATE webhooks SET url = ?, secret = ? WHERE id = ?`, "http://user:pass@example.com"+port+"/hook", "topsecret", hook.ID)
	e.app.TriggerIssueWebhooks(e.ctx, issue1Updated())
	n, err := e.srv.Jobs().RunPending(e.ctx)
	if err != nil || n == 0 {
		t.Fatalf("RunPending = %d, %v", n, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("requests %d", len(got))
	}
	r := got[0]
	if r.Method != "POST" || r.URL.Path != "/hook" || r.Header.Get("Content-Type") != "application/json" ||
		r.Header.Get(webhook.SignatureHeader) != webhook.Signature("topsecret", []byte(bodies[0])) {
		t.Errorf("request %s %s %v", r.Method, r.URL, r.Header)
	}
	if u, p, ok := r.BasicAuth(); !ok || u != "user" || p != "pass" {
		t.Errorf("basic auth %q %q", u, p)
	}
	if !strings.Contains(bodies[0], `"type":"issue.updated"`) {
		t.Errorf("body %s", bodies[0])
	}
	var state string
	if err := e.d.Get(e.ctx, &state, `SELECT state FROM jobs WHERE kind = ?`, handler.JobWebhook); err != nil || state != jobs.StateSucceeded {
		t.Errorf("job state %q %v", state, err)
	}
}

// Redmine 7.0: wiki/show.api.rsb に project を出す。
func TestWikiPageAPIProject(t *testing.T) {
	ts, _ := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")
	_, body := get(t, c, ts.URL+"/projects/ecookbook/wiki/CookBook_documentation.json")
	if !strings.Contains(body, `"comments":"Gzip compression activated","project":{"id":1,"name":"eCookbook"},"created_on"`) {
		t.Errorf("body %s", body)
	}
}

// 画面からの操作で発火する（issues#update / news#create / timelog#destroy）。
func TestWebhookTriggeredByActions(t *testing.T) {
	e := newWebhookEnv(t)
	e.createHook(3, "https://example.com/some/hook", true, webhook.EventNames(), 1)
	c := login(t, e.ts, "jsmith", "jsmith")

	_, body := get(t, c, e.ts.URL+"/issues/1")
	res, _ := post(t, c, e.ts.URL+"/issues/1", url.Values{
		"authenticity_token": {csrfToken(t, body)}, "_method": {"patch"},
		"issue[notes]": {"webhook note"},
	})
	if res.StatusCode != 302 {
		t.Fatalf("issue update status %d", res.StatusCode)
	}
	_, body = get(t, c, e.ts.URL+"/projects/ecookbook/news/new")
	res, _ = post(t, c, e.ts.URL+"/projects/ecookbook/news", url.Values{
		"authenticity_token": {csrfToken(t, body)}, "news[title]": {"Webhook title"}, "news[description]": {"desc"},
	})
	if res.StatusCode != 302 {
		t.Fatalf("news create status %d", res.StatusCode)
	}
	_, body = get(t, c, e.ts.URL+"/time_entries/1/edit")
	res, _ = post(t, c, e.ts.URL+"/time_entries/1", url.Values{"authenticity_token": {csrfToken(t, body)}, "_method": {"delete"}})
	if res.StatusCode != 302 {
		t.Fatalf("time entry destroy status %d", res.StatusCode)
	}
	ps := e.jobPayloads()
	var types []string
	for _, p := range ps {
		types = append(types, p["type"].(string))
	}
	if strings.Join(types, ",") != "issue.updated,news.created,time_entry.deleted" {
		t.Fatalf("types %v", types)
	}
	data := ps[0]["data"].(map[string]any)
	if j, ok := data["journal"].(map[string]any); !ok || j["notes"] != "webhook note" {
		t.Errorf("journal %v", data["journal"])
	}
	if te := ps[2]["data"].(map[string]any)["time_entry"].(map[string]any); te["hours"] != 4.25 {
		t.Errorf("time entry %v", te)
	}
}

// ペイロードはフックの利用者に見える範囲だけを含む: 非公開の注記（view_private_notes が無い）と
// 見えないカスタムフィールドは出さない。管理者には出す。
func TestWebhookPayloadVisibilityForHookUser(t *testing.T) {
	e := newWebhookEnv(t)
	e.exec(`UPDATE issue_journals SET private_notes = ? WHERE id = 1`, true)
	e.exec(`UPDATE custom_fields SET visible = ? WHERE id = 2`, false)
	for _, x := range []struct {
		login   string
		visible bool
	}{{"dlopper", false}, {"admin", true}} {
		u, err := repository.FindUserByLogin(e.ctx, e.d, x.login)
		if err != nil {
			t.Fatal(err)
		}
		got, ok, err := e.app.WebhookPayload(e.ctx, u, "issue.updated", 1, 1)
		if err != nil || !ok {
			t.Fatalf("%s: ok=%v err=%v", x.login, ok, err)
		}
		s := string(got)
		if strings.Contains(s, "Journal notes") != x.visible || strings.Contains(s, `"journal"`) != x.visible {
			t.Errorf("%s: private journal visible=%v: %s", x.login, !x.visible, s)
		}
		if strings.Contains(s, "Searchable field") != x.visible {
			t.Errorf("%s: hidden custom field visible=%v: %s", x.login, !x.visible, s)
		}
	}
}
