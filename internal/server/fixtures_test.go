// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/config"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/handler"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/server"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/testfixtures"
)

// frozenTime は参照 Redmine（tools/compat/redmine-ref.sh）の固定時刻 COMPAT_FROZEN_TIME。
// フィクスチャの相対日時とサーバの現在時刻（distance_of_time_in_words 等）を揃える。
var frozenTime = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

// newFixtureServer は Redmine の公式フィクスチャを投入した DB（REST API 有効・時刻固定）でサーバを起動する。
// extra はテスト用の追加ルート（nil 可）。
func newFixtureServer(t *testing.T, extra ...func(a *handler.App, r chi.Router)) (*httptest.Server, *db.DB) {
	t.Helper()
	_, ts, d := newFixtureServerFull(t, extra...)
	return ts, d
}

// newFixtureServerFull は newFixtureServer と同じ。*server.Server（ルータ・App）も返す。
func newFixtureServerFull(t *testing.T, extra ...func(a *handler.App, r chi.Router)) (*server.Server, *httptest.Server, *db.DB) {
	t.Helper()
	// 参照環境は TZ=UTC（タイムゾーン未設定ユーザーの時刻はサーバのローカル時刻で表示される）
	saved := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = saved })
	ctx := context.Background()
	d := dbtest.New(t)
	if err := testfixtures.LoadContext(ctx, d, frozenTime, testfixtures.All()...); err != nil {
		t.Fatal(err)
	}
	st, err := settings.New(ctx, repository.SettingsStore{DB: d})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Set(ctx, "rest_api_enabled", "1"); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Server.SecretKey = "test-secret"
	cfg.Storage.AttachmentsPath = t.TempDir()
	opts := server.Options{TempDir: t.TempDir(), Now: func() time.Time { return frozenTime }}
	if len(extra) > 0 {
		opts.ExtraRoutes = extra[0]
	}
	srv, err := server.New(cfg, d, opts)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts, d
}

var feedKeyRe = regexp.MustCompile(`key=[0-9a-f]{40}`)

// normalizeFixture は normalize に加えてベース URL と atom キーを伏せる
// （testdata/*_fixtures.html は参照 Redmine の `compat fetch -raw` の出力に同じ置換をしたもの）。
func normalizeFixture(s, base string) string {
	s = strings.ReplaceAll(s, base, "{{BASE}}")
	return feedKeyRe.ReplaceAllString(s, "key=KEY")
}

// login はログイン済みのクライアントを返す。
func login(t *testing.T, ts *httptest.Server, user, pw string) *http.Client {
	t.Helper()
	c := newClient(t)
	_, body := get(t, c, ts.URL+"/login")
	res, _ := post(t, c, ts.URL+"/login", url.Values{
		"authenticity_token": {csrfToken(t, body)}, "username": {user}, "password": {pw},
	})
	if res.StatusCode != 302 {
		t.Fatalf("login %s: status %d", user, res.StatusCode)
	}
	return c
}

// TestWelcomeWithFixtures はフィクスチャ投入済みのホーム画面（メニュー・ジャンプボックス・最新ニュース）が
// 参照 Redmine とバイト単位で一致することを確認する。
func TestWelcomeWithFixtures(t *testing.T) {
	ts, _ := newFixtureServer(t)

	t.Run("anonymous", func(t *testing.T) {
		res, body := get(t, newClient(t), ts.URL+"/")
		if res.StatusCode != 200 {
			t.Fatalf("status %d", res.StatusCode)
		}
		compareGolden(t, "welcome_anonymous_fixtures.html", normalizeFixture(body, ts.URL))
	})

	t.Run("admin", func(t *testing.T) {
		c := login(t, ts, "admin", "admin")
		res, body := get(t, c, ts.URL+"/")
		if res.StatusCode != 200 {
			t.Fatalf("status %d", res.StatusCode)
		}
		// ブックマーク [eCookbook, Private child]（ツリー表示）・所属プロジェクト [Private child]・
		// 非公開プロジェクトのニュースを含む最新ニュース
		compareGolden(t, "welcome_admin_fixtures.html", normalizeFixture(body, ts.URL))
	})

	t.Run("jsmith", func(t *testing.T) {
		c := login(t, ts, "jsmith", "jsmith")
		_, body := get(t, c, ts.URL+"/")
		// 兄弟順は fixtures の lft を保持する（projects.position）ので参照環境と同じく eCookbook が先。
		want := `<div class="drdn-items projects selection"><strong>All Projects</strong>` +
			`<a title="eCookbook" href="/projects/ecookbook?jump=welcome"><span style="padding-inline-start:0px;">eCookbook</span></a>` +
			`<a title="Private child of eCookbook" href="/projects/private-child?jump=welcome"><span style="padding-inline-start:16px;">Private child of eCookbook</span></a>` +
			`<a title="OnlineStore" href="/projects/onlinestore?jump=welcome"><span style="padding-inline-start:0px;">OnlineStore</span></a>` +
			`</div>`
		if !strings.Contains(body, want) {
			t.Errorf("jump box mismatch:\n%s", extract(body, `<div class="drdn-items projects selection">`, `</div>`))
		}
		// jsmith は OnlineStore（非公開）のメンバーなので非公開プロジェクトのニュースも見える
		if !strings.Contains(body, `<a href="/news/3">News on a private project</a>`) {
			t.Error("private news not shown to member")
		}
	})
}

// extract は s のうち start から最初の end までを返す（失敗時の表示用）。
func extract(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return "(not found)"
	}
	j := strings.Index(s[i:], end)
	if j < 0 {
		return s[i:]
	}
	return s[i : i+j+len(end)]
}
