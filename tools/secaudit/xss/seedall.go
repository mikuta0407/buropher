// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package main

import (
	"fmt"
	"net/url"
)

// seedAll は 1 サーバのあらゆるユーザー入力欄にペイロードを格納する。
// 両サーバで同じ順に呼ぶので reg の採番は一致する。失敗は s.errf に記録して継続する。
func seedAll(s *server, reg *registry) {
	seedUsersGroups(s, reg)
	seedProject(s, reg)
	seedMasters(s, reg)
	seedCustomFields(s, reg)
	seedIssues(s, reg)
	seedWikiNews(s, reg)
	seedBoardsDocs(s, reg)
	seedQueriesVersions(s, reg)
	seedSettings(s, reg)
	seedAttachments(s, reg)
}

func seedUsersGroups(s *server, reg *registry) {
	// ユーザー（firstname/lastname はそのまま表示される）。login/mail は検証が厳しいので無害な値。
	s.apiJSON("POST", "/users.json", map[string]any{
		"user": map[string]any{
			"login":     "xssuser",
			"firstname": reg.short("user.firstname"),
			"lastname":  reg.short("user.lastname"),
			"mail":      "xssuser@example.com",
			"password":  "Xsspass123!",
		},
	}, "user", "user")
	// グループ名
	s.apiJSON("POST", "/groups.json", map[string]any{
		"group": map[string]any{"name": reg.short("group.name")},
	}, "group", "group")
}

func seedProject(s *server, reg *registry) {
	v := s.apiJSON("POST", "/projects.json", map[string]any{
		"project": map[string]any{
			"name":        reg.short("project.name"),
			"identifier":  "xssproj",
			"description": reg.text("project.description"),
			"homepage":    reg.url("project.homepage"),
		},
	}, "project", "project")
	if v == nil {
		// 既存なら ID を拾う
		s.ids["project"] = "xssproj"
	} else {
		s.ids["project"] = "xssproj"
	}
}

func seedMasters(s *server, reg *registry) {
	pid := "xssproj"
	// トラッカー（default_status_id=1 が fixtures にある）
	s.form("/trackers", url.Values{
		"tracker[name]":              {reg.short("tracker.name")},
		"tracker[default_status_id]": {"1"},
		"tracker[description]":       {reg.short("tracker.description")},
	}, "")
	// ステータス
	s.form("/issue_statuses", url.Values{"issue_status[name]": {reg.short("status.name")}}, "")
	// 優先度（enumeration、type 指定）
	s.form("/enumerations", url.Values{
		"enumeration[name]": {reg.short("priority.name")},
		"type":              {"IssuePriority"},
	}, "")
	// 作業分類（活動）
	s.form("/enumerations", url.Values{
		"enumeration[name]": {reg.short("activity.name")},
		"type":              {"TimeEntryActivity"},
	}, "")
	// 文書カテゴリ
	s.form("/enumerations", url.Values{
		"enumeration[name]": {reg.short("doccategory.name")},
		"type":              {"DocumentCategory"},
	}, "")
	// ロール
	s.form("/roles", url.Values{"role[name]": {reg.short("role.name")}}, "")
	// 課題カテゴリ（プロジェクト配下）
	s.apiJSON("POST", "/projects/"+pid+"/issue_categories.json", map[string]any{
		"issue_category": map[string]any{"name": reg.short("category.name")},
	}, "issue_category", "category")
}

func seedCustomFields(s *server, reg *registry) {
	// 各フォーマットのカスタムフィールドを作る。name/description/default_value/possible_values/url_pattern が出力される。
	// list 形式: possible_values に危険値、link 形式: url_pattern に %value%。
	formats := []struct {
		format, kind string
		extra        url.Values
	}{
		{"string", "IssueCustomField", nil},
		{"text", "IssueCustomField", nil},
		{"link", "IssueCustomField", url.Values{"custom_field[url_pattern]": {fmt.Sprintf("javascript:xss(%d)//%%value%%", reg.id("cf.link.url_pattern"))}}},
		{"list", "IssueCustomField", url.Values{"custom_field[possible_values]": {reg.short("cf.list.values")}}},
	}
	for _, f := range formats {
		vals := url.Values{
			"type":                       {f.kind},
			"custom_field[field_format]": {f.format},
			"custom_field[name]":         {reg.short("cf." + f.format + ".name")},
			"custom_field[description]":  {reg.text("cf." + f.format + ".description")},
			"custom_field[is_for_all]":   {"1"},
			"custom_field[is_filter]":    {"1"},
			"custom_field[visible]":      {"1"},
		}
		if f.format == "string" || f.format == "text" {
			vals.Set("custom_field[default_value]", reg.short("cf."+f.format+".default"))
		}
		for k, v := range f.extra {
			vals[k] = v
		}
		s.form("/custom_fields", vals, "cf."+f.format)
	}
}

func seedIssues(s *server, reg *registry) {
	pid := s.ids["project"]
	if pid == "" {
		pid = "xssproj"
	}
	// 課題（subject/description）。tracker_id=1, status 既定, priority 既定は fixtures 由来。
	v := s.apiJSON("POST", "/issues.json", map[string]any{
		"issue": map[string]any{
			"project_id":  pid,
			"tracker_id":  1,
			"subject":     reg.short("issue.subject"),
			"description": reg.text("issue.description"),
			"priority_id": 4,
		},
	}, "issue", "issue")
	if v == nil {
		s.ids["issue"] = s.maxIDFrom("/projects/"+pid+"/issues?sort=id:desc", `/issues/(\d+)`)
	}
	iid := s.ids["issue"]
	if iid == "" {
		return
	}
	// ジャーナル注記（notes）
	s.apiJSON("PUT", "/issues/"+iid+".json", map[string]any{
		"issue": map[string]any{"notes": reg.text("journal.notes")},
	}, "", "")
	// 時間記録コメント
	s.apiJSON("POST", "/time_entries.json", map[string]any{
		"time_entry": map[string]any{"issue_id": iid, "hours": 1, "activity_id": 9, "comments": reg.short("timeentry.comments")},
	}, "", "")
}

func seedWikiNews(s *server, reg *registry) {
	pid := s.ids["project"]
	if pid == "" {
		pid = "xssproj"
	}
	// Wiki ページ（タイトル・本文）
	s.admin.api("PUT", "/projects/"+pid+"/wiki/XssWiki.json", map[string]any{
		"wiki_page": map[string]any{"text": reg.text("wiki.text"), "comments": reg.short("wiki.comments")},
	})
	// ニュース（タイトル・要約・説明）
	s.form("/projects/"+pid+"/news", url.Values{
		"news[title]":       {reg.short("news.title")},
		"news[summary]":     {reg.short("news.summary")},
		"news[description]": {reg.text("news.description")},
	}, "news")
}

func seedBoardsDocs(s *server, reg *registry) {
	pid := s.ids["project"]
	if pid == "" {
		pid = "xssproj"
	}
	// フォーラム（名前・説明）
	s.form("/projects/"+pid+"/boards", url.Values{
		"board[name]":        {reg.short("board.name")},
		"board[description]": {reg.short("board.description")},
	}, "board")
	s.ids["board"] = s.maxIDFrom("/projects/"+pid+"/boards", `/boards/(\d+)`)
	if bid := s.ids["board"]; bid != "" {
		// メッセージ（件名・本文）。作成は new_board_message（messages#new、POST）。
		s.form(fmt.Sprintf("/boards/%s/topics/new", bid), url.Values{
			"message[subject]": {reg.short("message.subject")},
			"message[content]": {reg.text("message.content")},
		}, "message")
		s.ids["message"] = s.maxIDFrom("/projects/"+pid+"/boards/"+bid, `/topics/(\d+)`)
	}
	// 文書（タイトル・説明）。category_id=1 は fixtures 由来。
	s.form("/projects/"+pid+"/documents", url.Values{
		"document[title]":       {reg.short("document.title")},
		"document[description]": {reg.text("document.description")},
		"document[category_id]": {"1"},
	}, "document")
}

func seedQueriesVersions(s *server, reg *registry) {
	pid := s.ids["project"]
	if pid == "" {
		pid = "xssproj"
	}
	// バージョン（名前・説明）
	s.apiJSON("POST", "/projects/"+pid+"/versions.json", map[string]any{
		"version": map[string]any{"name": reg.short("version.name"), "description": reg.short("version.description")},
	}, "version", "version")
	// カスタムクエリ（名前）
	s.form("/queries", url.Values{
		"query[name]":       {reg.short("query.name")},
		"query[project_id]": {pid},
		"query[visibility]": {"2"},
		"f[]":               {"status_id"},
		"op[status_id]":     {"o"},
		"c[]":               {"subject"},
	}, "query")
}

func seedSettings(s *server, reg *registry) {
	// アプリタイトル・ウェルカムテキスト・メールヘッダ/フッタ
	s.form("/settings/edit", url.Values{
		"settings[app_title]":     {reg.short("settings.app_title")},
		"settings[welcome_text]":  {reg.text("settings.welcome_text")},
		"settings[emails_header]": {reg.text("settings.emails_header")},
		"settings[emails_footer]": {reg.text("settings.emails_footer")},
	}, "")
}

func seedAttachments(s *server, reg *registry) {
	// HTML/SVG ファイルをアップロードし、添付ファイルとして課題に付ける。
	// ファイル名・説明がリンクに出る。content-type（inline 実行）も検査対象。
	iid := s.ids["issue"]
	if iid == "" {
		return
	}
	n := reg.id("attachment.filename")
	fname := fmt.Sprintf(`xss%d"><img src=x onerror=xss(%d)>.svg`, n, n)
	svg := []byte(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg"><script>xss(%d)</script></svg>`, reg.id("attachment.svgbody")))
	tok, err := s.admin.upload(fname, svg)
	if err != nil {
		s.errf("upload svg: %v", err)
		return
	}
	s.apiJSON("PUT", "/issues/"+iid+".json", map[string]any{
		"issue": map[string]any{
			"notes": "attach",
			"uploads": []map[string]any{
				{"token": tok, "filename": fname, "description": reg.short("attachment.description"), "content_type": "image/svg+xml"},
			},
		},
	}, "", "")
	// 添付 ID を拾う
	s.ids["attachment"] = s.maxIDFrom("/issues/"+iid, `/attachments/(?:download/)?(\d+)`)
	// HTML ファイルも別途（inline 配信の危険性確認）
	hn := reg.id("attachment.html")
	hname := fmt.Sprintf("xss%d.html", hn)
	tok2, err := s.admin.upload(hname, []byte(fmt.Sprintf(`<html><body><script>xss(%d)</script></body></html>`, hn)))
	if err == nil {
		s.apiJSON("PUT", "/issues/"+iid+".json", map[string]any{
			"issue": map[string]any{"notes": "attach2", "uploads": []map[string]any{{"token": tok2, "filename": hname, "content_type": "text/html"}}},
		}, "", "")
	}
	s.ids["attachment_html"] = s.maxIDFrom("/issues/"+iid, `/attachments/(?:download/)?(\d+)`)
}
