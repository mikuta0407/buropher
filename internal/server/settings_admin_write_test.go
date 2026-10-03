// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/bootstrap"
	"github.com/mikuta0407/buropher/internal/config"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/server"
	"github.com/mikuta0407/buropher/internal/settings"
)

// このファイルは設定画面・管理画面の更新系アクションの振る舞いを
// test/functional/settings_controller_test.rb / admin_controller_test.rb に倣って確認する。

func settingsPost(t *testing.T, c *http.Client, ts *httptest.Server, path string, form url.Values) (*http.Response, string) {
	t.Helper()
	_, page := get(t, c, ts.URL+"/settings")
	form.Set("authenticity_token", csrfToken(t, page))
	return post(t, c, ts.URL+path, form)
}

func TestSettingsUpdate(t *testing.T) {
	ts, d := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")
	ctx := context.Background()
	reload := func() *settings.Settings {
		st, err := settings.New(ctx, repository.SettingsStore{DB: d})
		if err != nil {
			t.Fatal(err)
		}
		return st
	}

	t.Run("notifications", func(t *testing.T) {
		// test_post_edit_notifications
		res, _ := settingsPost(t, c, ts, "/settings/edit?tab=notifications", url.Values{
			"settings[mail_from]":         {"functional@test.foo"},
			"settings[notified_events][]": {"", "issue_added", "issue_updated", "news_added"},
			"settings[emails_footer]":     {"Test footer"},
			"tab":                         {"notifications"},
		})
		if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/settings?tab=notifications") {
			t.Fatalf("status %d location %s", res.StatusCode, res.Header.Get("Location"))
		}
		st := reload()
		if st.String("mail_from") != "functional@test.foo" || st.String("emails_footer") != "Test footer" {
			t.Errorf("mail_from=%q footer=%q", st.String("mail_from"), st.String("emails_footer"))
		}
		if got := strings.Join(st.Strings("notified_events"), ","); got != "issue_added,issue_updated,news_added" {
			t.Errorf("notified_events = %s", got)
		}
		// アプリ側のキャッシュにも反映される
		_, body := get(t, c, ts.URL+"/settings?tab=notifications")
		if !strings.Contains(body, `value="functional@test.foo"`) || !strings.Contains(body, "Successful update.") {
			t.Error("updated value / flash not shown")
		}
	})

	t.Run("commit update keywords", func(t *testing.T) {
		// test_post_edit_commit_update_keywords
		res, _ := settingsPost(t, c, ts, "/settings/edit", url.Values{
			"settings[commit_update_keywords][keywords][]":      {"resolves", "closes"},
			"settings[commit_update_keywords][status_id][]":     {"3", "5"},
			"settings[commit_update_keywords][done_ratio][]":    {"", "100"},
			"settings[commit_update_keywords][if_tracker_id][]": {"", "2"},
		})
		if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/settings") {
			t.Fatalf("status %d location %s", res.StatusCode, res.Header.Get("Location"))
		}
		var v string
		if err := d.Get(ctx, &v, `SELECT value FROM settings WHERE name = 'commit_update_keywords'`); err != nil {
			t.Fatal(err)
		}
		want := `[{"keywords":"resolves","status_id":"3"},{"done_ratio":"100","if_tracker_id":"2","keywords":"closes","status_id":"5"}]`
		if v != want {
			t.Errorf("commit_update_keywords = %s", v)
		}
		// test_edit_commit_update_keywords: 2 行が表示され、選択状態が復元される
		_, body := get(t, c, ts.URL+"/settings?tab=repositories")
		if n := strings.Count(body, `<tr class="commit-keywords">`); n != 2 {
			t.Errorf("rows = %d", n)
		}
		for _, w := range []string{`value="closes"`, `<option selected="selected" value="100">100 %</option>`,
			`<option selected="selected" value="2">Feature request</option>`, `<option selected="selected" value="5">Closed</option>`} {
			if !strings.Contains(body, w) {
				t.Errorf("missing %s", w)
			}
		}
	})

	t.Run("invalid setting name", func(t *testing.T) {
		// test_post_edit_with_invalid_setting_should_not_error
		res, _ := settingsPost(t, c, ts, "/settings/edit", url.Values{"settings[invalid_setting]": {"1"}})
		if res.StatusCode != 302 {
			t.Errorf("status %d", res.StatusCode)
		}
	})

	t.Run("invalid regexp", func(t *testing.T) {
		// test_post_mail_handler_delimiters_should_not_save_invalid_regex_delimiters
		res, body := settingsPost(t, c, ts, "/settings/edit?tab=mail_handler", url.Values{
			"settings[mail_handler_enable_regex_delimiters]": {"1"},
			"settings[mail_handler_body_delimiters]":         {"Abc["},
		})
		if res.StatusCode != 200 {
			t.Fatalf("status %d", res.StatusCode)
		}
		st := reload()
		if st.String("mail_handler_enable_regex_delimiters") != "0" || st.String("mail_handler_body_delimiters") != "" {
			t.Error("invalid settings were saved")
		}
		if !strings.Contains(body, `<div id="errorExplanation">`) || !strings.Contains(body, "<b>Truncate emails after one of these lines</b> is not a valid regular expression (") {
			t.Errorf("error message not shown:\n%s", extract(body, `<div id="errorExplanation">`, `</div>`))
		}
		// 再表示では送信した値を使う（setting_value）
		if !strings.Contains(body, ">\nAbc[</textarea>") {
			t.Error("submitted value not redisplayed")
		}
		// valid
		res, _ = settingsPost(t, c, ts, "/settings/edit", url.Values{
			"settings[mail_handler_enable_regex_delimiters]": {"1"},
			"settings[mail_handler_body_delimiters]":         {"On .*, .* at .*, .* <.*<mailto:.*>> wrote:"},
		})
		if res.StatusCode != 302 {
			t.Errorf("valid regexp: status %d", res.StatusCode)
		}
	})

	t.Run("invalid mail_from", func(t *testing.T) {
		res, body := settingsPost(t, c, ts, "/settings/edit?tab=notifications", url.Values{
			"settings[mail_from]": {"not an address"}, "tab": {"notifications"},
		})
		if res.StatusCode != 200 || !strings.Contains(body, "<li><b>Emission email address</b> is invalid</li>") {
			t.Errorf("status %d\n%s", res.StatusCode, extract(body, `<div id="errorExplanation">`, `</div>`))
		}
	})

	t.Run("int format is silently ignored", func(t *testing.T) {
		res, _ := settingsPost(t, c, ts, "/settings/edit", url.Values{
			"settings[password_min_length]": {"abc"},
			"settings[app_title]":           {"My Redmine"},
		})
		if res.StatusCode != 302 {
			t.Fatalf("status %d", res.StatusCode)
		}
		st := reload()
		if st.String("password_min_length") != "8" || st.String("app_title") != "My Redmine" {
			t.Errorf("password_min_length=%q app_title=%q", st.String("password_min_length"), st.String("app_title"))
		}
	})

	t.Run("twofa disabled unpairs all", func(t *testing.T) {
		if _, err := d.Exec(ctx, `UPDATE user_accounts SET twofa_scheme = 'totp', twofa_totp_key = 'x' WHERE login = 'jsmith'`); err != nil {
			t.Fatal(err)
		}
		settingsPost(t, c, ts, "/settings/edit?tab=authentication", url.Values{"settings[twofa]": {"0"}})
		if n := count(t, d, `SELECT COUNT(*) FROM user_accounts WHERE twofa_scheme IS NOT NULL`); n != 0 {
			t.Errorf("paired users = %d", n)
		}
	})

	t.Run("test email", func(t *testing.T) {
		res, _ := settingsPost(t, c, ts, "/admin/test_email", url.Values{})
		if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/settings?tab=notifications") {
			t.Fatalf("status %d location %s", res.StatusCode, res.Header.Get("Location"))
		}
		_, body := get(t, c, ts.URL+"/settings?tab=notifications")
		if !strings.Contains(body, "An error occurred while sending mail (email delivery is not configured)") {
			t.Error("error flash not shown")
		}
	})

	t.Run("default configuration already loaded", func(t *testing.T) {
		// test_load_default_configuration_data_should_rescue_error 相当（データがあるので DataAlreadyLoaded）
		res, _ := settingsPost(t, c, ts, "/admin/default_configuration", url.Values{"lang": {"fr"}})
		if res.StatusCode != 302 || !strings.HasSuffix(res.Header.Get("Location"), "/admin") {
			t.Fatalf("status %d location %s", res.StatusCode, res.Header.Get("Location"))
		}
		_, body := get(t, c, ts.URL+"/admin")
		if !strings.Contains(body, "Some configuration data is already loaded.") {
			t.Error("error flash not shown")
		}
		if strings.Contains(body, `<div class="nodata">`) {
			t.Error("no data prompt shown")
		}
	})
}

// TestAdminLoadDefaultConfiguration は既定データの無い DB で admin#index に案内が出て、
// default_configuration で選んだ言語の既定データが投入されることを確認する（test_load_default_configuration_data）。
func TestAdminLoadDefaultConfiguration(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	if err := bootstrap.Init(ctx, d, bootstrap.Options{AdminPassword: "adminpass1", SkipDefaultData: true}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Server.SecretKey = "test-secret"
	cfg.Storage.AttachmentsPath = t.TempDir()
	srv, err := server.New(cfg, d, server.Options{TempDir: t.TempDir(), Now: func() time.Time { return frozenTime }})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	c := login(t, ts, "admin", "adminpass1")
	_, body := get(t, c, ts.URL+"/admin")
	for _, w := range []string{`<div class="nodata">`, `<form action="/admin/default_configuration"`, `<select name="lang" id="lang">`,
		`<option selected="selected" value="en">English</option>`, `value="Load the default configuration"`} {
		if !strings.Contains(body, w) {
			t.Errorf("missing %s", w)
		}
	}
	res, _ := post(t, c, ts.URL+"/admin/default_configuration", url.Values{"lang": {"fr"}, "authenticity_token": {csrfToken(t, body)}})
	if res.StatusCode != 302 {
		t.Fatalf("status %d", res.StatusCode)
	}
	if n := count(t, d, `SELECT COUNT(*) FROM issue_statuses WHERE name = 'Nouveau'`); n != 1 {
		t.Errorf("Nouveau = %d", n)
	}
	_, body = get(t, c, ts.URL+"/admin")
	if strings.Contains(body, `<div class="nodata">`) {
		t.Error("no data prompt still shown")
	}
	// 既定のトラッカーが新規プロジェクトの既定に設定され、設定のキャッシュにも反映される
	if !strings.Contains(adminGet(t, c, ts.URL+"/settings?tab=projects"), `checked="checked" />Anomalie</label>`) {
		t.Error("default_projects_tracker_ids not reflected")
	}
}
