// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

// Redmine 7.0 で変わった管理画面（ユーザー・情報）とロードマップ・文書・ニュースの一覧の振る舞いの
// テスト。users_controller_test.rb / documents_controller_test.rb / versions_controller_test.rb /
// admin_controller_test.rb の該当テストの移植。

import (
	"regexp"
	"strings"
	"testing"
)

// test_new_should_show_lastname_before_firstname_when_user_format_requires_it /
// test_index_default_columns_should_show_lastname_before_firstname_when_user_format_requires_it（#4507）
// と、test_edit のパスワード欄の autocomplete=new-password（#44268）。
func TestUsersNameOrderAndPasswordAutocomplete(t *testing.T) {
	f := newContentFixture(t)
	admin := login(t, f.ts, "admin", "admin")

	_, body := get(t, admin, f.ts.URL+"/users/new")
	if strings.Index(body, `id="user_firstname"`) > strings.Index(body, `id="user_lastname"`) {
		t.Error("new: firstname should come first by default")
	}
	_, body = get(t, admin, f.ts.URL+"/users")
	thLast := regexp.MustCompile(`<th[^>]*class="[^"]*\blastname\b`).FindStringIndex(body)
	thFirst := regexp.MustCompile(`<th[^>]*class="[^"]*\bfirstname\b`).FindStringIndex(body)
	if thLast == nil || thFirst == nil || thFirst[0] > thLast[0] {
		t.Errorf("index: default column order (firstname, lastname) not kept: %v %v", thFirst, thLast)
	}

	f.setting(t, "user_format", "lastname_firstname")
	_, body = get(t, admin, f.ts.URL+"/users/new")
	if i, j := strings.Index(body, `id="user_lastname"`), strings.Index(body, `id="user_firstname"`); i < 0 || j < 0 || i > j {
		t.Error("new: lastname should come before firstname")
	}
	_, body = get(t, admin, f.ts.URL+"/users")
	thLast = regexp.MustCompile(`<th[^>]*class="[^"]*\blastname\b`).FindStringIndex(body)
	thFirst = regexp.MustCompile(`<th[^>]*class="[^"]*\bfirstname\b`).FindStringIndex(body)
	if thLast == nil || thFirst == nil || thLast[0] > thFirst[0] {
		t.Errorf("index: lastname column should come first: %v %v", thFirst, thLast)
	}

	_, body = get(t, admin, f.ts.URL+"/users/2/edit")
	for _, name := range []string{"user[password]", "user[password_confirmation]"} {
		re := regexp.MustCompile(`<input[^>]*autocomplete="new-password"[^>]*name="` + regexp.QuoteMeta(name) + `"|<input[^>]*name="` + regexp.QuoteMeta(name) + `"[^>]*autocomplete="new-password"`)
		if !re.MatchString(body) {
			t.Errorf("edit: %s lacks autocomplete=new-password", name)
		}
	}
}

// test_index_grouped_by_date（#44111: 日付のグループ見出しは format_activity_day）。
func TestDocumentsIndexGroupedByDate(t *testing.T) {
	f := newContentFixture(t)
	admin := login(t, f.ts, "admin", "admin")
	_, body := get(t, admin, f.ts.URL+"/projects/ecookbook/documents?sort_by=date")
	m := regexp.MustCompile(`<h3 class="group-name">([^<]*)</h3>`).FindAllStringSubmatch(body, -1)
	if len(m) < 2 || m[0][1] != "03/05/2007" || m[1][1] != "02/12/2007" {
		t.Errorf("date groups = %v", m)
	}
	if !strings.Contains(body, `<h4 class="title"><svg class="s18 icon-svg" aria-hidden="true"><use href="/assets/icons-`) {
		t.Error("document title lacks the document icon")
	}
}

// test_index（#39882: サイドバーのバージョンへのリンクは #version-<id>、見出しの header に id）。
func TestRoadmapVersionAnchors(t *testing.T) {
	f := newContentFixture(t)
	admin := login(t, f.ts, "admin", "admin")
	_, body := get(t, admin, f.ts.URL+"/projects/ecookbook/roadmap")
	for _, s := range []string{`<a href="#version-3">2.0</a>`, `<a href="#version-4">eCookbook Subproject 1 - 2.0</a>`,
		`<header id="version-3">`, `<a name="2.0" href="/versions/3">2.0</a>`} {
		if !strings.Contains(body, s) {
			t.Errorf("roadmap lacks %s", s)
		}
	}
}

// #44111: ニュースの一覧は div#news-list で囲む。#44062: 情報の画面に Environment の見出し。
func TestNewsListAndAdminInfoEnvironment(t *testing.T) {
	f := newContentFixture(t)
	admin := login(t, f.ts, "admin", "admin")
	_, body := get(t, admin, f.ts.URL+"/projects/ecookbook/news")
	if !regexp.MustCompile(`<div id="news-list">\s*<article class="news-article">`).MatchString(body) {
		t.Error("news-list wrapper missing")
	}
	_, body = get(t, admin, f.ts.URL+"/admin/info")
	if !regexp.MustCompile(`<h3>Environment</h3>\s*<div class="box autoscroll">`).MatchString(body) {
		t.Error("Environment heading missing")
	}
}
