// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package main

// crawlPaths は格納済み ID を使って、1 サーバの巡回対象 URL を列挙する。
func crawlPaths(s *server) []pathSpec {
	id := func(k string) string {
		if v := s.ids[k]; v != "" {
			return v
		}
		return "0"
	}
	pid := "xssproj"
	iid := id("issue")
	var ps []pathSpec
	add := func(p string) { ps = append(ps, pathSpec{path: p}) }
	addX := func(p string) { ps = append(ps, pathSpec{path: p, xhr: true}) }
	addAPI := func(p string) { ps = append(ps, pathSpec{path: p, api: true}) }

	// --- プロジェクト・概要 ---
	add("/projects")
	add("/projects.atom")
	addAPI("/projects.json")
	addAPI("/projects.xml")
	add("/projects/" + pid)
	add("/projects/" + pid + "/settings")
	add("/projects/" + pid + "/activity")
	add("/projects/" + pid + "/activity.atom")

	// --- 課題 ---
	add("/issues")
	add("/issues.atom")
	add("/issues.csv")
	addAPI("/issues.json?include=attachments,journals,relations,children,watchers")
	addAPI("/issues.xml?include=attachments,journals,relations,children,watchers")
	add("/projects/" + pid + "/issues")
	add("/projects/" + pid + "/issues.csv")
	add("/projects/" + pid + "/issues.atom")
	if iid != "0" {
		add("/issues/" + iid)
		add("/issues/" + iid + ".atom")
		add("/issues/" + iid + ".pdf")
		addAPI("/issues/" + iid + ".json?include=attachments,journals,relations,children,watchers,changesets")
		addAPI("/issues/" + iid + ".xml?include=attachments,journals,relations,children,watchers")
		addX("/issues/" + iid + "/edit")
		addX("/issues/" + iid + "/quoted")
		add("/issues/" + iid + "/time_entries")
	}
	// コンテキストメニュー・一括編集・プレビュー（XHR）
	addX("/issues/context_menu?ids[]=" + iid)
	addX("/issues/bulk_edit?ids[]=" + iid)
	add("/issues/gantt")
	add("/projects/" + pid + "/issues/gantt")
	add("/projects/" + pid + "/issues/calendar")
	add("/issues/calendar")

	// --- Wiki ---
	add("/projects/" + pid + "/wiki/XssWiki")
	add("/projects/" + pid + "/wiki/XssWiki.pdf")
	add("/projects/" + pid + "/wiki/XssWiki/history")
	addX("/projects/" + pid + "/wiki/XssWiki/edit")
	add("/projects/" + pid + "/wiki/index")
	add("/projects/" + pid + "/wiki/date_index")
	add("/projects/" + pid + "/activity.atom?show_wiki_edits=1")

	// --- ニュース・文書・フォーラム ---
	add("/projects/" + pid + "/news")
	add("/news")
	add("/news.atom")
	addAPI("/news.json")
	if nid := id("news"); nid != "0" {
		add("/news/" + nid)
	}
	add("/projects/" + pid + "/documents")
	if did := id("document"); did != "0" {
		add("/documents/" + did)
	}
	add("/projects/" + pid + "/boards")
	if bid := id("board"); bid != "0" {
		add("/projects/" + pid + "/boards/" + bid)
		if mid := id("message"); mid != "0" {
			add("/boards/" + bid + "/topics/" + mid)
		}
	}

	// --- バージョン・ロードマップ ---
	add("/projects/" + pid + "/roadmap")
	if vid := id("version"); vid != "0" {
		add("/versions/" + vid)
		addAPI("/versions/" + vid + ".json")
	}

	// --- 管理（マスタ・カスタムフィールド・ユーザー・グループ） ---
	add("/trackers")
	add("/issue_statuses")
	add("/enumerations")
	add("/roles")
	add("/custom_fields")
	for _, k := range []string{"cf.string", "cf.text", "cf.link", "cf.list"} {
		if cid := id(k); cid != "0" {
			addX("/custom_fields/" + cid + "/edit")
		}
	}
	add("/users")
	addAPI("/users.json")
	if uid := id("user"); uid != "0" {
		add("/users/" + uid)
		addAPI("/users/" + uid + ".json")
		add("/users/" + uid + "/edit")
	}
	add("/groups")
	addAPI("/groups.json")
	if gid := id("group"); gid != "0" {
		add("/groups/" + gid)
		addAPI("/groups/" + gid + ".json")
	}
	add("/settings")
	add("/settings?tab=display")
	add("/time_entries")
	add("/time_entries.csv")
	add("/time_entries.atom")

	// --- 検索・自動補完・XHR ---
	add("/search?q=xss&all_words=1")
	add("/search?q=xss&scope=all")
	addX("/issues/auto_complete?q=xss")
	addX("/projects/" + pid + "/search?q=xss")
	add("/activity")
	add("/activity.atom")

	// --- 添付（inline 配信・ファイル名ヘッダ） ---
	if aid := id("attachment"); aid != "0" {
		add("/attachments/" + aid)
		add("/attachments/download/" + aid)
		add("/attachments/" + aid + "/xss.svg")
	}
	if aid := id("attachment_html"); aid != "0" {
		add("/attachments/download/" + aid)
	}

	// --- マイページ ---
	add("/my/page")
	add("/my/account")

	return ps
}
