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

// このファイルはカレンダー（CalendarsController）とレポート（ReportsController）の参照 Redmine との比較テスト。
//
// testdata/calendar_reports/* は参照 Redmine 6.1.2（http://127.0.0.1:3998）の出力を正規化し、
// HTML は <title>・ページ固有の head・#main だけを抜き出したもの（asNormalize）。
// 取り直すときは BUROPHER_CALREP_GOLDEN_REF=http://127.0.0.1:3998 go test -run TestCalendarReportsMatchRedmine ./internal/server
// （参照側は GET のみ）。

var calendarReportCases = []asCase{
	// カレンダー（全体）
	{"anonymous", "/issues/calendar", "calendar_anonymous.html"},
	{"admin", "/issues/calendar", "calendar_admin.html"},
	{"jsmith", "/issues/calendar", "calendar_jsmith.html"},
	{"dlopper", "/issues/calendar", "calendar_dlopper.html"},
	{"admin", "/issues/calendar?month=2&year=2026", "calendar_2026_02_admin.html"},
	{"admin", "/issues/calendar?month=7&year=2006&set_filter=1&f[]=", "calendar_2006_07_admin.html"},
	{"admin", "/issues/calendar?month=12&year=2025", "calendar_2025_12_admin.html"},
	{"admin", "/issues/calendar?year=1800&month=13", "calendar_invalid_year_admin.html"},
	{"jsmith", "/issues/calendar?set_filter=1&f[]=tracker_id&op[tracker_id]==&v[tracker_id][]=1", "calendar_filter_tracker_jsmith.html"},
	{"admin", "/issues/calendar?query_id=5", "calendar_query_5_admin.html"},
	{"admin", "/issues/calendar?set_filter=1&f[]=start_date&op[start_date]=%3E%3C&v[start_date][]=foo", "calendar_invalid_filter_admin.html"},
	// カレンダー（プロジェクト）
	{"jsmith", "/projects/ecookbook/issues/calendar", "project_calendar_jsmith.html"},
	{"anonymous", "/projects/ecookbook/issues/calendar", "project_calendar_anonymous.html"},
	{"admin", "/projects/onlinestore/issues/calendar", "project_calendar_onlinestore_admin.html"},
	{"admin", "/projects/ecookbook/issues/calendar?month=2&year=2026", "project_calendar_2026_02_admin.html"},
	{"jsmith", "/projects/ecookbook/issues/calendar?query_id=1", "project_calendar_query_1_jsmith.html"},
	// レポート
	{"jsmith", "/projects/ecookbook/issues/report", "report_jsmith.html"},
	{"admin", "/projects/ecookbook/issues/report", "report_admin.html"},
	{"anonymous", "/projects/ecookbook/issues/report", "report_anonymous.html"},
	{"admin", "/projects/onlinestore/issues/report", "report_onlinestore_admin.html"},
	{"dlopper", "/projects/ecookbook/issues/report", "report_dlopper.html"},
	{"jsmith", "/projects/ecookbook/issues/report/tracker", "report_tracker_jsmith.html"},
	{"jsmith", "/projects/ecookbook/issues/report/version", "report_version_jsmith.html"},
	{"jsmith", "/projects/ecookbook/issues/report/priority", "report_priority_jsmith.html"},
	{"jsmith", "/projects/ecookbook/issues/report/category", "report_category_jsmith.html"},
	{"jsmith", "/projects/ecookbook/issues/report/assigned_to", "report_assigned_to_jsmith.html"},
	{"jsmith", "/projects/ecookbook/issues/report/author", "report_author_jsmith.html"},
	{"jsmith", "/projects/ecookbook/issues/report/subproject", "report_subproject_jsmith.html"},
	{"admin", "/projects/ecookbook/issues/report/subproject", "report_subproject_admin.html"},
	{"admin", "/projects/ecookbook/issues/report/assigned_to.csv", "report_assigned_to_admin.csv"},
}

// TestCalendarReportsMatchRedmine はカレンダー・レポートの画面が参照 Redmine と一致することを確認する。
func TestCalendarReportsMatchRedmine(t *testing.T) {
	dir := filepath.Join("testdata", "calendar_reports")
	if ref := os.Getenv("BUROPHER_CALREP_GOLDEN_REF"); ref != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		clients := map[string]*http.Client{}
		for _, tc := range calendarReportCases {
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
	for _, tc := range calendarReportCases {
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

// TestCalendarReportsBehavior は 404・権限などの振る舞いを確認する。
func TestCalendarReportsBehavior(t *testing.T) {
	ts, _ := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	for path, want := range map[string]int{
		"/projects/unknown/issues/calendar":         404,
		"/projects/ecookbook/issues/report/foo":     404,
		"/projects/unknown/issues/report":           404,
		"/issues/calendar?query_id=999":             404,
		"/projects/ecookbook/issues/report/foo.csv": 404,
	} {
		if res, _ := get(t, admin, ts.URL+path); res.StatusCode != want {
			t.Errorf("%s: %d, want %d", path, res.StatusCode, want)
		}
	}
	// 巨大な年・ページ番号で前後の年・ページを列挙するループが終わらなくならない
	for _, path := range []string{
		"/projects/ecookbook/issues/calendar?year=9223372036854775802&month=1",
		"/projects/ecookbook/issues/gantt?year=9223372036854775802",
		"/news?page=9223372036854775805",
		"/issues?page=9223372036854775805&per_page=25",
	} {
		if res, _ := get(t, admin, ts.URL+path); res.StatusCode != 200 {
			t.Errorf("%s: %d", path, res.StatusCode)
		}
	}
	// 見つからない query_id は rescue されない RecordNotFound（public/404.html）
	if _, body := get(t, admin, ts.URL+"/issues/calendar?query_id=999"); !strings.Contains(body, "<title>Redmine 404 error</title>") {
		t.Error("query_id=999: not public 404")
	}
	anon := newClient(t)
	// 非公開プロジェクトは匿名ではログインを要求する
	for _, path := range []string{"/projects/onlinestore/issues/calendar", "/projects/onlinestore/issues/report"} {
		if res, _ := get(t, anon, ts.URL+path); res.StatusCode != 302 {
			t.Errorf("anonymous %s: %d", path, res.StatusCode)
		}
	}
	// dlopper は onlinestore のメンバーではない
	dl := login(t, ts, "dlopper", "foo")
	if res, _ := get(t, dl, ts.URL+"/projects/onlinestore/issues/report"); res.StatusCode != 403 {
		t.Errorf("dlopper onlinestore report: %d", res.StatusCode)
	}
	// 非公開クエリ（dlopper のもの）は他人には 403、本人には見える
	js := login(t, ts, "jsmith", "jsmith")
	if res, _ := get(t, js, ts.URL+"/projects/ecookbook/issues/calendar?query_id=2"); res.StatusCode != 403 {
		t.Errorf("private query: %d", res.StatusCode)
	}
	if res, _ := get(t, dl, ts.URL+"/projects/ecookbook/issues/calendar?query_id=2"); res.StatusCode != 200 {
		t.Errorf("private query: %d", res.StatusCode)
	}
}
