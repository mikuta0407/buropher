// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package menu

// lib/redmine/preparation.rb の MenuManager.map 定義の移植。

// HelpURL は Redmine::Info.help_url。
const HelpURL = "https://www.redmine.org/guide"

func path(s string) func(Env, Project) string { return func(Env, Project) string { return s } }

// recalledTypeURL は params[:type] を引き継ぐ URL（Env が RecalledType を実装していれば s + "/" + type）。
func recalledTypeURL(s string) func(Env, Project) string {
	return func(e Env, _ Project) string {
		if r, ok := e.(interface{ RecalledType() string }); ok {
			if t := r.RecalledType(); t != "" {
				return s + "/" + t
			}
		}
		return s
	}
}

// projectPath はプロジェクト識別子を埋め込んだパスを返す（:param => :project_id / :id）。
func projectPath(prefix, suffix string) func(Env, Project) string {
	return func(_ Env, p Project) string {
		if p == nil {
			return prefix + suffix
		}
		return prefix + "/" + p.Identifier() + suffix
	}
}

// recallable はリクエストの :project_id を引き継ぐ URL（Env が RecalledProjectID を実装していれば
// /projects/:project_id を前に付ける。Rails の url_for の recall）。
func recallable(s string) func(Env, Project) string {
	return func(e Env, _ Project) string {
		if r, ok := e.(interface{ RecalledProjectID() string }); ok {
			if id := r.RecalledProjectID(); id != "" {
				return "/projects/" + id + s
			}
		}
		return s
	}
}

// roadmapURL は {:controller => 'versions', :action => 'index'} の URL。ルートで :format が固定されている
// リクエスト（attachments/:id/:filename など）では get 'versions.:format' のルートが format を引き継ぐ
// （Env が RecalledFormat を実装していれば "/projects/:id/versions.html" になる）。
func roadmapURL(e Env, p Project) string {
	if r, ok := e.(interface{ RecalledFormat() string }); ok && p != nil {
		if f := r.RecalledFormat(); f != "" {
			return "/projects/" + p.Identifier() + "/versions." + f
		}
	}
	return projectPath("/projects", "/roadmap")(e, p)
}

func globalModule(perm, module string) func(Env, Project) bool {
	return func(e Env, _ Project) bool {
		return e.AllowedToGlobally(perm) && e.ModuleEnabledInVisibleProject(module)
	}
}

// TopMenu は :top_menu。
func TopMenu() *Menu {
	m := &Menu{Name: "top_menu"}
	m.Push(&Item{Name: "home", URL: path("/"), PermMode: PermNone}, "")
	m.Push(&Item{Name: "my_page", Controller: "my", Action: "page", URL: path("/my/page"),
		Cond: func(e Env, _ Project) bool { return e.LoggedIn() }}, "")
	m.Push(&Item{Name: "projects", Controller: "projects", Action: "index", URL: path("/projects"),
		Caption: "label_project_plural"}, "")
	m.Push(&Item{Name: "administration", Controller: "admin", Action: "index", URL: path("/admin"),
		Cond: func(e Env, _ Project) bool { return e.Admin() }, Last: true}, "")
	m.Push(&Item{Name: "help", URL: path(HelpURL), PermMode: PermNone,
		HTML: []Attr{{"target", "_blank"}, {"rel", "noopener"}}, Last: true}, "")
	return m
}

// AccountMenu は :account_menu。
func AccountMenu() *Menu {
	m := &Menu{Name: "account_menu"}
	m.Push(&Item{Name: "login", URL: path("/login"), PermMode: PermNone,
		Cond: func(e Env, _ Project) bool { return !e.LoggedIn() }}, "")
	m.Push(&Item{Name: "register", URL: path("/account/register"), PermMode: PermNone,
		Cond: func(e Env, _ Project) bool {
			return !e.LoggedIn() && e.Setting("self_registration") != "" && e.Setting("self_registration") != "0"
		}}, "")
	m.Push(&Item{Name: "my_account", Controller: "my", Action: "account", URL: path("/my/account"),
		Cond: func(e Env, _ Project) bool { return e.LoggedIn() }}, "")
	m.Push(&Item{Name: "logout", URL: path("/logout"), PermMode: PermNone,
		HTML: []Attr{{"method", "post"}},
		Cond: func(e Env, _ Project) bool { return e.LoggedIn() }}, "")
	return m
}

// ApplicationMenu は :application_menu。
func ApplicationMenu() *Menu {
	m := &Menu{Name: "application_menu"}
	m.Push(&Item{Name: "projects", Controller: "projects", Action: "index", URL: path("/projects"),
		PermMode: PermNone, Caption: "label_project_plural"}, "")
	m.Push(&Item{Name: "activity", Controller: "activities", Action: "index", URL: path("/activity")}, "")
	m.Push(&Item{Name: "issues", Controller: "issues", Action: "index", URL: recallable("/issues"),
		Cond: globalModule("view_issues", "issue_tracking"), Caption: "label_issue_plural"}, "")
	m.Push(&Item{Name: "time_entries", Controller: "timelog", Action: "index", URL: recallable("/time_entries"),
		Cond: globalModule("view_time_entries", "time_tracking"), Caption: "label_spent_time"}, "")
	m.Push(&Item{Name: "gantt", Controller: "gantts", Action: "show", URL: recallable("/issues/gantt"),
		Caption: "label_gantt", Cond: globalModule("view_gantt", "gantt")}, "")
	m.Push(&Item{Name: "calendar", Controller: "calendars", Action: "show", URL: recallable("/issues/calendar"),
		Caption: "label_calendar", Cond: globalModule("view_calendar", "calendar")}, "")
	m.Push(&Item{Name: "news", Controller: "news", Action: "index", URL: recallable("/news"),
		Cond: globalModule("view_news", "news"), Caption: "label_news_plural"}, "")
	return m
}

// AdminMenu は :admin_menu。
func AdminMenu() *Menu {
	m := &Menu{Name: "admin_menu"}
	add := func(name, controller, action, url, caption, icon, class string, last bool, cond func(Env, Project) bool) {
		m.Push(&Item{Name: name, Controller: controller, Action: action, URL: path(url), Caption: caption,
			Icon: icon, HTML: []Attr{{"class", "icon " + class}}, Last: last, Cond: cond}, "")
	}
	add("projects", "admin", "projects", "/admin/projects", "label_project_plural", "projects", "icon-projects", false, nil)
	add("users", "users", "index", "/users", "label_user_plural", "user", "icon-user", false, nil)
	add("groups", "groups", "index", "/groups", "label_group_plural", "group", "icon-group", false, nil)
	add("roles", "roles", "index", "/roles", "label_role_and_permissions", "roles", "icon-roles", false, nil)
	add("trackers", "trackers", "index", "/trackers", "label_tracker_plural", "issue", "icon-issue", false, nil)
	add("issue_statuses", "issue_statuses", "index", "/issue_statuses", "label_issue_status_plural", "issue-edit", "icon-issue-edit", false, nil)
	add("workflows", "workflows", "edit", "/workflows/edit", "label_workflow", "workflows", "icon-workflows", false, nil)
	add("custom_fields", "custom_fields", "index", "/custom_fields", "label_custom_field_plural", "custom-fields", "icon-custom-fields", false, nil)
	add("enumerations", "enumerations", "index", "/enumerations", "", "list", "icon-list", false, nil)
	// :type をルートの既定値に持つリクエスト（imports#new）では url_for の recall で /enumerations/:type になる
	m.Find("enumerations").URL = recalledTypeURL("/enumerations")
	add("settings", "settings", "index", "/settings", "", "settings", "icon-settings", false, nil)
	add("ldap_authentication", "auth_sources", "index", "/auth_sources", "", "server-authentication", "icon-server-authentication", false, nil)
	add("applications", "oauth2_applications", "index", "/oauth/applications", "doorkeeper.layouts.admin.nav.applications", "apps", "icon-applications", false,
		func(e Env, _ Project) bool { return e.Setting("rest_api_enabled") == "1" })
	add("plugins", "admin", "plugins", "/admin/plugins", "", "plugins", "icon-plugins", true, nil)
	add("info", "admin", "info", "/admin/info", "label_information_plural", "help", "icon-help", true, nil)
	return m
}

// ProjectMenu は :project_menu。
func ProjectMenu() *Menu {
	m := &Menu{Name: "project_menu"}
	m.Push(&Item{Name: "new_object", CaptionLiteral: " + ",
		Cond: func(e Env, _ Project) bool { return e.Setting("new_item_menu_tab") == "2" },
		HTML: []Attr{{"id", "new-object"}, {"onclick", "toggleNewObjectDropdown(); return false;"}}}, "")
	m.Push(&Item{Name: "new_issue_sub", Controller: "issues", Action: "new", URL: projectPath("/projects", "/issues/new"),
		Caption: "label_issue_new", HTML: []Attr{{"accesskey", "7"}},
		Cond:     func(e Env, p Project) bool { return e.AllowedTargetTrackersAny(p) },
		PermMode: PermNamed, Permission: "add_issues"}, "new_object")
	sub := func(name, controller, url, caption string) {
		m.Push(&Item{Name: name, Controller: controller, Action: "new", URL: projectPath("/projects", url), Caption: caption}, "new_object")
	}
	sub("new_issue_category", "issue_categories", "/issue_categories/new", "label_issue_category_new")
	sub("new_version", "versions", "/versions/new", "label_version_new")
	sub("new_timelog", "timelog", "/time_entries/new", "button_log_time")
	sub("new_news", "news", "/news/new", "label_news_new")
	sub("new_document", "documents", "/documents/new", "label_document_new")
	sub("new_wiki_page", "wiki", "/wiki/new", "label_wiki_page_new")
	sub("new_file", "files", "/files/new", "label_attachment_new")

	m.Push(&Item{Name: "overview", Controller: "projects", Action: "show", URL: projectPath("/projects", "")}, "")
	m.Push(&Item{Name: "activity", Controller: "activities", Action: "index", URL: projectPath("/projects", "/activity")}, "")
	m.Push(&Item{Name: "roadmap", Controller: "versions", Action: "index", URL: roadmapURL,
		Cond: func(e Env, p Project) bool {
			if e.SharedVersionsAny(p) {
				return true
			}
			return e.Setting("display_subprojects_issues") == "1" && e.RolledUpVersionsAny(p)
		}}, "")
	m.Push(&Item{Name: "issues", Controller: "issues", Action: "index", URL: projectPath("/projects", "/issues"), Caption: "label_issue_plural"}, "")
	m.Push(&Item{Name: "new_issue", Controller: "issues", Action: "new", URL: projectPath("/projects", "/issues/new"),
		Caption: "label_issue_new", HTML: []Attr{{"accesskey", "7"}},
		Cond: func(e Env, p Project) bool {
			return e.Setting("new_item_menu_tab") == "1" && e.AllowedTargetTrackersAny(p)
		},
		PermMode: PermNamed, Permission: "add_issues"}, "")
	m.Push(&Item{Name: "time_entries", Controller: "timelog", Action: "index", URL: projectPath("/projects", "/time_entries"), Caption: "label_spent_time"}, "")
	m.Push(&Item{Name: "gantt", Controller: "gantts", Action: "show", URL: projectPath("/projects", "/issues/gantt"), Caption: "label_gantt"}, "")
	m.Push(&Item{Name: "calendar", Controller: "calendars", Action: "show", URL: projectPath("/projects", "/issues/calendar"), Caption: "label_calendar"}, "")
	m.Push(&Item{Name: "news", Controller: "news", Action: "index", URL: projectPath("/projects", "/news"), Caption: "label_news_plural"}, "")
	m.Push(&Item{Name: "documents", Controller: "documents", Action: "index", URL: projectPath("/projects", "/documents"), Caption: "label_document_plural"}, "")
	m.Push(&Item{Name: "wiki", Controller: "wiki", Action: "show", URL: projectPath("/projects", "/wiki"),
		Cond: func(e Env, p Project) bool { return e.HasWiki(p) }}, "")
	m.Push(&Item{Name: "boards", Controller: "boards", Action: "index", URL: projectPath("/projects", "/boards"),
		Cond: func(e Env, p Project) bool { return e.BoardsAny(p) }, Caption: "label_board_plural"}, "")
	m.Push(&Item{Name: "files", Controller: "files", Action: "index", URL: projectPath("/projects", "/files"), Caption: "label_file_plural"}, "")
	m.Push(&Item{Name: "repository", Controller: "repositories", Action: "show", URL: projectPath("/projects", "/repository"),
		Cond: func(e Env, p Project) bool { return e.RepositoriesExist(p) }}, "")
	m.Push(&Item{Name: "settings", Controller: "projects", Action: "settings", URL: projectPath("/projects", "/settings"), Last: true}, "")
	return m
}

// controllerMenuItems はコントローラごとの menu_item 宣言
// （app/controllers/*: menu_item :x / menu_item :x, only: [...]）。
var controllerMenuItems = map[string]struct {
	def     string
	actions map[string]string
}{
	"settings":         {"settings", map[string]string{"plugin": "plugins"}},
	"journals":         {"issues", nil},
	"versions":         {"roadmap", nil},
	"issue_categories": {"settings", nil},
	"reports":          {"issues", nil},
	"auth_sources":     {"ldap_authentication", nil},
	"wikis":            {"wiki", nil},
	"repositories": {"repository", map[string]string{
		"new": "settings", "create": "settings", "edit": "settings", "update": "settings", "destroy": "settings", "committers": "settings"}},
	"calendars":  {"calendar", nil},
	"gantts":     {"gantt", nil},
	"activities": {"activity", nil},
	"queries":    {"issues", nil},
	"projects": {"overview", map[string]string{
		"settings": "settings", "index": "projects", "new": "projects", "copy": "projects", "create": "projects"}},
	"messages": {"boards", nil},
	"timelog":  {"time_entries", nil},
	"admin":    {"admin", map[string]string{"projects": "projects", "plugins": "plugins", "info": "info"}},
	"files":    {"files", nil},
}

// CurrentMenuItem は MenuController#current_menu_item の移植。
// menu_item 宣言のないコントローラはコントローラ名そのもの（例: issues, wiki, boards）。
// imports コントローラ等が動的に決める場合は呼び出し側で上書きすること。
func CurrentMenuItem(controller, action string) string {
	if c, ok := controllerMenuItems[controller]; ok {
		if v, ok := c.actions[action]; ok {
			return v
		}
		return c.def
	}
	return controller
}
