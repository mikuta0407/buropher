// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package menu

import (
	"html/template"
	"strings"
	"testing"
)

type fakeEnv struct {
	loggedIn, admin bool
	current         string
	settings        map[string]string
	labels          map[string]string
}

func (f *fakeEnv) L(k string) string {
	if v, ok := f.labels[k]; ok {
		return v
	}
	return k
}
func (f *fakeEnv) LOrHumanize(name, prefix string) string {
	if v, ok := f.labels[prefix+name]; ok {
		return v
	}
	return strings.ToUpper(name[:1]) + strings.ReplaceAll(name[1:], "_", " ")
}
func (f *fakeEnv) LoggedIn() bool                              { return f.loggedIn }
func (f *fakeEnv) Admin() bool                                 { return f.admin }
func (f *fakeEnv) AllowedTo(string, Project) bool              { return true }
func (f *fakeEnv) AllowedToAction(c, a string, p Project) bool { return c != "repositories" }
func (f *fakeEnv) AllowedToGlobally(string) bool               { return true }
func (f *fakeEnv) ModuleEnabledInVisibleProject(string) bool   { return true }
func (f *fakeEnv) Setting(n string) string                     { return f.settings[n] }
func (f *fakeEnv) CurrentMenuItem() string                     { return f.current }
func (f *fakeEnv) SharedVersionsAny(Project) bool              { return true }
func (f *fakeEnv) RolledUpVersionsAny(Project) bool            { return false }
func (f *fakeEnv) AllowedTargetTrackersAny(Project) bool       { return true }
func (f *fakeEnv) HasWiki(Project) bool                        { return true }
func (f *fakeEnv) BoardsAny(Project) bool                      { return false }
func (f *fakeEnv) RepositoriesExist(Project) bool              { return false }
func (f *fakeEnv) SpriteIcon(icon string, l template.HTML) template.HTML {
	return template.HTML(`<svg data-icon="`+icon+`"></svg><span class="icon-label">`) + l + "</span>"
}

type proj string

func (p proj) Identifier() string { return string(p) }

var labels = map[string]string{
	"label_login": "Sign in", "label_register": "Register", "label_project_plural": "Projects",
	"label_activity": "Activity", "label_issue_plural": "Issues", "label_spent_time": "Spent time",
	"label_gantt": "Gantt", "label_calendar": "Calendar", "label_news_plural": "News",
	"label_my_account": "My account", "label_profile": "Profile", "label_logout": "Sign out", "label_overview": "Overview",
	"label_roadmap": "Roadmap", "label_issue_new": "New issue", "label_wiki": "Wiki",
	"label_file_plural": "Files", "label_settings": "Settings", "label_document_plural": "Documents",
}

// 期待値は Redmine 7.0.1 の実出力（匿名ユーザー）。
func TestAccountMenuAnonymous(t *testing.T) {
	e := &fakeEnv{settings: map[string]string{"self_registration": "2"}, labels: labels}
	want := `<ul><li><a class="login" href="/login">Sign in</a></li><li><a class="register" href="/account/register">Register</a></li></ul>`
	if got := string(AccountMenu().Render(e, nil)); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestAccountMenuLoggedIn(t *testing.T) {
	e := &fakeEnv{loggedIn: true, labels: labels}
	// Redmine 7.0.1 で :my_profile（先頭）が追加された。
	want := `<ul><li><a class="my-profile" href="/users/current">Profile</a></li><li><a class="my-account" href="/my/account">My account</a></li><li><a class="logout" rel="nofollow" data-method="post" href="/logout">Sign out</a></li></ul>`
	if got := string(AccountMenu().Render(e, nil)); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestApplicationMenu(t *testing.T) {
	e := &fakeEnv{current: CurrentMenuItem("projects", "index"), labels: labels}
	want := `<ul><li><a class="projects selected" href="/projects">Projects</a></li><li><a class="activity" href="/activity">Activity</a></li><li><a class="issues" href="/issues">Issues</a></li><li><a class="time-entries" href="/time_entries">Spent time</a></li><li><a class="gantt" href="/issues/gantt">Gantt</a></li><li><a class="calendar" href="/issues/calendar">Calendar</a></li><li><a class="news" href="/news">News</a></li></ul>`
	if got := string(ApplicationMenu().Render(e, nil)); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestProjectMenuNewObject(t *testing.T) {
	e := &fakeEnv{current: "issues", settings: map[string]string{"new_item_menu_tab": "2"}, labels: labels}
	got := string(ProjectMenu().Render(e, proj("ecookbook")))
	if !strings.HasPrefix(got, "<ul><li>\n"+`<a onclick="toggleNewObjectDropdown(); return false;" id="new-object" class="new-object" href="#"> + </a>`+"\n"+`<ul class="menu-children"><li><a accesskey="7" class="new-issue-sub" href="/projects/ecookbook/issues/new">New issue</a></li>`) {
		t.Errorf("unexpected: %s", got)
	}
	if !strings.Contains(got, `<a class="issues selected" href="/projects/ecookbook/issues">Issues</a>`) {
		t.Error("issues not selected")
	}
	if strings.Contains(got, "repository") || strings.Contains(got, "boards") {
		t.Error("hidden items rendered")
	}
}

func TestAdminMenuIcons(t *testing.T) {
	e := &fakeEnv{current: "users", settings: map[string]string{"rest_api_enabled": "0"}, labels: map[string]string{"label_user_plural": "Users"}}
	got := string(AdminMenu().Render(e, nil))
	if !strings.Contains(got, `<li><a class="icon icon-user users selected" href="/users"><svg data-icon="user"></svg><span class="icon-label">Users</span></a></li>`) {
		t.Errorf("unexpected: %s", got)
	}
	if strings.Contains(got, "applications") {
		t.Error("applications should be hidden when REST API disabled")
	}
	// :last 指定の plugins, info が末尾
	if !strings.HasSuffix(got, `href="/admin/info"><svg data-icon="help"></svg><span class="icon-label">label_information_plural</span></a></li></ul>`) {
		t.Errorf("info not last: %s", got)
	}
}
