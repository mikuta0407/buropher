// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/config"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/server"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/testfixtures"
)

// newThemeServer は config の web.themes_dir に themesDir を指定し、ui_theme を theme にしたサーバを起動する。
func newThemeServer(t *testing.T, themesDir, theme string) *httptest.Server {
	t.Helper()
	ctx := context.Background()
	d := dbtest.New(t)
	if err := testfixtures.LoadContext(ctx, d, frozenTime, testfixtures.All()...); err != nil {
		t.Fatal(err)
	}
	st, err := settings.New(ctx, repository.SettingsStore{DB: d})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Set(ctx, "ui_theme", theme); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Server.SecretKey = "test-secret"
	cfg.Storage.AttachmentsPath = t.TempDir()
	cfg.Web.ThemesDir = themesDir
	srv, err := server.New(cfg, d, server.Options{TempDir: t.TempDir(), Now: func() time.Time { return frozenTime }})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func writeThemeFile(t *testing.T, root, name, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestExternalTheme は外部テーマ（web.themes_dir）の CSS・theme.js・favicon・画像・アイコンスプライトの差し替えと、
// 表示設定のテーマ一覧を確認する（Redmine の themes/ ディレクトリに置いたテーマと同じ振る舞い）。
func TestExternalTheme(t *testing.T) {
	dir := t.TempDir()
	writeThemeFile(t, dir, "my_corp/stylesheets/application.css", `@import url(/application.css); body { background: url(../images/bg.png); }`)
	writeThemeFile(t, dir, "my_corp/javascripts/theme.js", `console.log("theme");`)
	writeThemeFile(t, dir, "my_corp/favicon/favicon.png", "PNG")
	writeThemeFile(t, dir, "my_corp/images/bg.png", "PNG")
	writeThemeFile(t, dir, "my_corp/images/icons.svg", `<svg xmlns="http://www.w3.org/2000/svg"><symbol id="icon--edit"></symbol></svg>`)
	// application.css の無いディレクトリはテーマとして扱わない
	writeThemeFile(t, dir, "broken/stylesheets/other.css", "")

	ts := newThemeServer(t, dir, "my_corp")
	c := login(t, ts, "admin", "admin")
	_, body := get(t, c, ts.URL+"/issues/1")
	for _, re := range []string{
		`<link rel="stylesheet" href="/assets/themes/my_corp/application-[0-9a-f]{8}\.css" media="all" />`,
		`<script src="/assets/themes/my_corp/theme-[0-9a-f]{8}\.js"></script>`,
		`<link rel="shortcut icon" type="image/x-icon" href="/assets/themes/my_corp/favicon-[0-9a-f]{8}\.png" />`,
		`<body class="theme-My_corp `,
		// テーマのスプライトにあるアイコンはテーマ側、無いアイコンは標準のスプライト
		`<use href="/assets/themes/my_corp/icons-[0-9a-f]{8}\.svg#icon--edit">`,
		`<use href="/assets/icons-[0-9a-f]{8}\.svg#icon--copy">`,
	} {
		if !regexp.MustCompile(re).MatchString(body) {
			t.Errorf("missing %s", re)
		}
	}
	css := regexp.MustCompile(`/assets/themes/my_corp/application-[0-9a-f]{8}\.css`).FindString(body)
	res, cssBody := get(t, c, ts.URL+css)
	if res.StatusCode != 200 || !regexp.MustCompile(`url\("?/assets/themes/my_corp/bg-[0-9a-f]{8}\.png"?\)`).MatchString(cssBody) ||
		!regexp.MustCompile(`@import url\("/assets/application-[0-9a-f]{8}\.css"\)`).MatchString(cssBody) {
		t.Errorf("theme css %d:\n%s", res.StatusCode, cssBody)
	}

	_, body = get(t, c, ts.URL+"/settings?tab=display")
	sel := extract(body, `<select name="settings[ui_theme]"`, `</select>`)
	for _, o := range []string{`<option value="alternate">Alternate</option>`, `<option value="classic">Classic</option>`,
		`<option selected="selected" value="my_corp">My corp</option>`} {
		if !strings.Contains(sel, o) {
			t.Errorf("theme options lack %s:\n%s", o, sel)
		}
	}
	if strings.Contains(sel, "broken") {
		t.Error("theme without application.css listed")
	}
}

// TestBuiltinThemes は同梱テーマ（classic / alternate）の CSS の差し替えと body のクラス（参照 Redmine と同じ）。
func TestBuiltinThemes(t *testing.T) {
	for _, tc := range []struct{ id, class string }{{"classic", "theme-Classic"}, {"alternate", "theme-Alternate"}} {
		ts := newThemeServer(t, "", tc.id)
		_, body := get(t, newClient(t), ts.URL+"/projects")
		if !regexp.MustCompile(`<link rel="stylesheet" href="/assets/themes/` + tc.id + `/application-[0-9a-f]{8}\.css" media="all" />`).MatchString(body) {
			t.Errorf("%s: stylesheet", tc.id)
		}
		if !strings.Contains(body, `<body class="`+tc.class+` `) {
			t.Errorf("%s: body class", tc.id)
		}
		// 同梱テーマは favicon・theme.js を持たない
		if !regexp.MustCompile(`href="/assets/favicon-[0-9a-f]{8}\.ico"`).MatchString(body) || strings.Contains(body, "/theme-") {
			t.Errorf("%s: favicon / theme.js", tc.id)
		}
	}
}
