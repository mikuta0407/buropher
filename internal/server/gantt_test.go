// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// このファイルはガントチャート（GanttsController#show）の参照 Redmine との比較テスト。
//
// testdata/gantt/* は参照 Redmine 7.0.1（http://127.0.0.1:3998）の出力を正規化し、
// <title>・ページ固有の head・#main だけを抜き出したもの（asNormalize）。
// 取り直すときは BUROPHER_GANTT_GOLDEN_REF=http://127.0.0.1:3998 go test -run TestGanttMatchRedmine ./internal/server
// （参照側は GET のみ）。
//
// gantts#show は zoom / months を個人設定（gantt_zoom / gantt_months）に保存し、パラメータが無いときに使う。
// 参照環境は共有でリセットしないため、各ユーザーの最初と最後の要求で zoom=2&months=6（既定値）を明示して
// 設定を既定値に戻す（未設定の候補側と同じ表示になる）。

var ganttCases = []asCase{
	// 全体
	{"admin", "/issues/gantt?zoom=2&months=6", "global_admin.html"},
	{"admin", "/issues/gantt", "global_pref_admin.html"},
	{"admin", "/issues/gantt?zoom=1&months=3", "global_z1_m3_admin.html"},
	{"admin", "/issues/gantt?year=2006&month=1&months=24&zoom=1&set_filter=1&f[]=status_id&op[status_id]=*", "global_2006_admin.html"},
	{"admin", "/issues/gantt?query_id=5", "global_query_5_admin.html"},
	{"admin", "/issues/gantt?set_filter=1&f[]=start_date&op[start_date]=%3E%3C&v[start_date][]=foo", "global_invalid_filter_admin.html"},
	{"admin", "/issues/gantt?months=30&month=13&year=abc", "global_invalid_params_admin.html"},
	{"admin", "/issues/gantt?months=24&zoom=9&year=2025&month=0", "global_m24_admin.html"},
	// プロジェクト
	{"admin", "/projects/ecookbook/issues/gantt?zoom=2&months=6", "ecookbook_admin.html"},
	{"admin", "/projects/ecookbook/issues/gantt?zoom=3&months=2&month=12&year=2025", "ecookbook_z3_admin.html"},
	{"admin", "/projects/ecookbook/issues/gantt?zoom=4&months=1", "ecookbook_z4_admin.html"},
	{"admin", "/projects/ecookbook/issues/gantt", "ecookbook_pref_admin.html"},
	{"admin", "/projects/ecookbook/issues/gantt?year=2006&month=7&months=12&zoom=2&set_filter=1&f[]=status_id&op[status_id]=*", "ecookbook_2006_admin.html"},
	{"admin", "/projects/ecookbook/issues/gantt?set_filter=1&f[]=status_id&op[status_id]=*&query[draw_selected_columns]=1&query[draw_progress_line]=1&query[draw_relations]=0&c[]=project&c[]=status&c[]=done_ratio&c[]=fixed_version&c[]=parent&c[]=parent.subject&c[]=cf_2&c[]=estimated_hours&c[]=spent_hours&c[]=relations&c[]=subject", "ecookbook_options_admin.html"},
	{"admin", "/projects/ecookbook/issues/gantt?query_id=1", "ecookbook_query_1_admin.html"},
	{"admin", "/projects/onlinestore/issues/gantt", "onlinestore_admin.html"},
	{"admin", "/projects/subproject1/issues/gantt?year=2006&month=1&months=24&set_filter=1&f[]=status_id&op[status_id]=*", "subproject1_admin.html"},
	{"admin", "/issues/gantt?zoom=2&months=6", "global_reset_admin.html"},
	// ほかのユーザー
	{"jsmith", "/issues/gantt?zoom=2&months=6", "global_jsmith.html"},
	{"jsmith", "/projects/ecookbook/issues/gantt?zoom=3", "ecookbook_z3_jsmith.html"},
	{"jsmith", "/projects/onlinestore/issues/gantt?query_id=7", "onlinestore_query_7_jsmith.html"},
	{"jsmith", "/issues/gantt?zoom=2&months=6", "global_reset_jsmith.html"},
	{"dlopper", "/issues/gantt?zoom=2&months=6", "global_dlopper.html"},
	{"dlopper", "/projects/ecookbook/issues/gantt?set_filter=1&f[]=status_id&op[status_id]=*&year=2006&month=6&months=12", "ecookbook_2006_dlopper.html"},
	{"dlopper", "/projects/ecookbook/issues/gantt?query_id=2", "ecookbook_query_2_dlopper.html"},
	{"dlopper", "/issues/gantt?zoom=2&months=6", "global_reset_dlopper.html"},
	{"anonymous", "/issues/gantt", "global_anonymous.html"},
	{"anonymous", "/projects/ecookbook/issues/gantt?zoom=4&months=2", "ecookbook_anonymous.html"},
}

// TestGanttMatchRedmine はガントチャートの画面が参照 Redmine と一致することを確認する。
func TestGanttMatchRedmine(t *testing.T) {
	dir := filepath.Join("testdata", "gantt")
	if ref := os.Getenv("BUROPHER_GANTT_GOLDEN_REF"); ref != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		clients := map[string]*http.Client{}
		for _, tc := range ganttCases {
			status, body := asFetch(t, ref, clients, tc.user, tc.path)
			if status != 200 {
				t.Fatalf("ref %s %s: status %d", tc.user, tc.path, status)
			}
			if err := os.WriteFile(filepath.Join(dir, tc.golden), []byte(asNormalize(body, ref)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	ts, _ := newFixtureServer(t)
	clients := map[string]*http.Client{}
	for _, tc := range ganttCases {
		t.Run(tc.golden, func(t *testing.T) {
			status, body := asFetch(t, ts.URL, clients, tc.user, tc.path)
			if status != 200 {
				t.Fatalf("status %d", status)
			}
			want, err := os.ReadFile(filepath.Join(dir, tc.golden))
			if err != nil {
				t.Fatal(err)
			}
			got := asNormalize(body, ts.URL)
			if got != string(want) {
				gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
				for i := 0; i < len(gl) || i < len(wl); i++ {
					var a, b string
					if i < len(gl) {
						a = gl[i]
					}
					if i < len(wl) {
						b = wl[i]
					}
					if a != b {
						t.Fatalf("line %d differs\n got: %q\nwant: %q", i+1, a, b)
					}
				}
			}
		})
	}
}

// TestGanttBehavior は 404・権限・形式などの振る舞いを確認する。
func TestGanttBehavior(t *testing.T) {
	ts, _ := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	for path, want := range map[string]int{
		"/projects/unknown/issues/gantt":        404,
		"/issues/gantt?query_id=999":            404,
		"/issues/gantt.pdf":                     200,
		"/issues/gantt.png":                     406,
		"/projects/ecookbook/issues/gantt":      200,
		"/projects/ecookbook/issues/gantt.json": 406,
	} {
		if res, _ := get(t, admin, ts.URL+path); res.StatusCode != want {
			t.Errorf("%s: %d, want %d", path, res.StatusCode, want)
		}
	}
	// 非公開プロジェクトは匿名ではログイン画面へ
	anon := newClient(t)
	if res, _ := get(t, anon, ts.URL+"/projects/private-child/issues/gantt"); res.StatusCode != 302 {
		t.Errorf("anonymous private project: %d, want 302", res.StatusCode)
	}
}
