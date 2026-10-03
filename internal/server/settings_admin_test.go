// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

// 管理画面の設定（settings#edit）と admin#index / plugins / info の互換テスト。
// testdata/settings_admin/ の期待値は共用の参照 Redmine（3998）から
// `go run ./tools/compat fetch -base http://127.0.0.1:3998 -raw -user admin <path>` で取得し、
// ベース URL を {{BASE}} に置換したもの。

var reRecentlyUsed = regexp.MustCompile(`<strong>Recently used</strong>.*?<strong>All Projects</strong>`)

// reSCMRow は settings/_repositories の SCM の 1 行。
var reSCMRow = regexp.MustCompile(`(?s)\n    <tr>\n      <td class="scm_name">.*?\n    </tr>`)

// gitOnlySCM は期待値の SCM 表から Git 以外の行を除き、Git のバージョン表示を伏せる。
// 意図的な差異: buropher は Git のみ対応するため（docs: D-15）、Redmine の表のうち Git の行だけを出す。
func gitOnlySCM(s string) string {
	s = reSCMRow.ReplaceAllStringFunc(s, func(row string) string {
		if strings.Contains(row, `value="Git"`) {
			return row
		}
		return ""
	})
	return regexp.MustCompile(`(<span class="icon icon-(?:ok|error)"></span>\n\s*git\n\s*</td>\n\s*<td>\n\s*)[^\n]*`).
		ReplaceAllString(s, "${1}GITVERSION")
}

func compareSettingsGolden(t *testing.T, name, got, base string, edit ...func(string) string) {
	t.Helper()
	host := strings.TrimPrefix(base, "http://")
	edits := append([]func(string) string{func(s string) string {
		s = strings.ReplaceAll(s, "127.0.0.1:3998", "{{HOST}}")
		// 共用の参照環境の「最近使ったプロジェクト」は他のテストの GET で変わるため比較しない
		s = reRecentlyUsed.ReplaceAllString(s, "<strong>All Projects</strong>")
		return strings.ReplaceAll(s, host, "{{HOST}}")
	}}, edit...)
	compareGoldenDir(t, "testdata/settings_admin/", name, got, base, edits...)
}

// compareGoldenDir は got と dir/name を正規化して比較する（compareAdminGolden のディレクトリ指定版）。
func compareGoldenDir(t *testing.T, dir, name, got, base string, edit ...func(string) string) {
	t.Helper()
	raw, err := os.ReadFile(dir + name)
	if err != nil {
		t.Fatal(err)
	}
	want := normalizeAdmin(string(raw), "{{BASE}}")
	g := normalizeAdmin(got, base)
	for _, e := range edit {
		want, g = e(want), e(g)
	}
	if g == want {
		return
	}
	if d := os.Getenv("BUROPHER_GOLDEN_DUMP"); d != "" {
		_ = os.WriteFile(d+"/"+name+".got", []byte(g), 0o644)
		_ = os.WriteFile(d+"/"+name+".want", []byte(want), 0o644)
	}
	gl, wl := strings.Split(g, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(gl) || i < len(wl); i++ {
		var a, b string
		if i < len(gl) {
			a = gl[i]
		}
		if i < len(wl) {
			b = wl[i]
		}
		if a != b {
			t.Fatalf("%s: line %d differs\n got: %q\nwant: %q", name, i+1, a, b)
		}
	}
}

func TestSettingsPagesMatchRedmine(t *testing.T) {
	ts, _ := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")
	t.Run("settings", func(t *testing.T) {
		compareSettingsGolden(t, "settings.html", adminGet(t, c, ts.URL+"/settings"), ts.URL, gitOnlySCM)
	})
	t.Run("admin", func(t *testing.T) {
		compareSettingsGolden(t, "admin.html", adminGet(t, c, ts.URL+"/admin"), ts.URL)
	})
	t.Run("plugins", func(t *testing.T) {
		compareSettingsGolden(t, "admin_plugins.html", adminGet(t, c, ts.URL+"/admin/plugins"), ts.URL)
	})
	t.Run("info", func(t *testing.T) {
		// 意図的な差異: 環境情報は buropher のものを表示する（レイアウトと CSS クラスのみ比較する）
		_, body := getRaw(t, c, ts.URL+"/admin/info")
		for _, want := range []string{
			"<h2>Information</h2>",
			"<p><strong>Buropher dev</strong></p>",
			`Licensed under <a href="https://www.gnu.org/licenses/old-licenses/gpl-2.0.html" class="external">GPL-2.0-or-later</a>.`,
			`Source code: <a href="https://github.com/mikuta0407/buropher" class="external">https://github.com/mikuta0407/buropher</a>`,
			`<td class="name">Default administrator account changed</td>`,
			`<td class="name">Attachments directory writable</td>`,
			`<span class="icon-only icon-ok">`,
			`<div class="box autoscroll">` + "\n<pre>Environment:\n  Buropher version",
			"Database adapter               SQLite",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("admin/info: missing %q", want)
			}
		}
	})
	t.Run("plugin settings 404", func(t *testing.T) {
		res, _ := get(t, c, ts.URL+"/settings/plugin/foo")
		if res.StatusCode != 404 {
			t.Errorf("status %d", res.StatusCode)
		}
	})
	t.Run("non admin 403", func(t *testing.T) {
		jc := login(t, ts, "jsmith", "jsmith")
		for _, p := range []string{"/settings", "/admin", "/admin/plugins", "/admin/info"} {
			res, _ := get(t, jc, ts.URL+p)
			if res.StatusCode != 403 {
				t.Errorf("%s: status %d", p, res.StatusCode)
			}
		}
		res, _ := post(t, jc, ts.URL+"/settings/edit", url.Values{})
		if res.StatusCode != 422 && res.StatusCode != 403 {
			t.Errorf("POST /settings/edit: status %d", res.StatusCode)
		}
	})
}
