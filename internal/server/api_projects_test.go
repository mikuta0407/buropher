package server_test

// Redmine の test/integration/api_test/projects_test.rb の移植。

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestAPIProjects(t *testing.T) {
	t.Run("GET /projects.xml should return projects", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		mastersExec(t, d, `UPDATE projects SET inherit_members = 1 WHERE id = 1`)
		r := apiGet(t, ts, "/projects.xml")
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/xml")
		doc := r.XML(t)
		const first = "projects > project:first-child > "
		assertXMLText(t, doc, first+"id", "1")
		assertXMLText(t, doc, first+"status", "1")
		assertXMLText(t, doc, first+"is_public", "true")
		assertXMLText(t, doc, first+"inherit_members", "true")
		assertXMLText(t, doc, first+"homepage", "http://ecookbook.somenet.foo/")
	})

	ts, d := newFixtureServer(t)
	t.Run("GET /projects.json should return projects", func(t *testing.T) {
		r := apiGet(t, ts, "/projects.json")
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/json")
		m := r.JSON(t)
		ps, ok := m["projects"].([]any)
		if !ok || len(ps) == 0 {
			t.Fatalf("projects: %s", r.Body)
		}
		first := ps[0].(map[string]any)
		for _, k := range []string{"id", "inherit_members", "homepage"} {
			if _, ok := first[k]; !ok {
				t.Errorf("missing key %s", k)
			}
		}
	})
	t.Run("GET /projects.xml with include=issue_categories should return categories", func(t *testing.T) {
		r := apiGet(t, ts, "/projects.xml?include=issue_categories")
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/xml")
		if len(xmlSelect(t, r.XML(t), `issue_categories[type=array] issue_category[id="2"][name=Recipes]`)) == 0 {
			t.Error("category 2 missing")
		}
	})
	t.Run("GET /projects.xml with include=trackers should return trackers", func(t *testing.T) {
		r := apiGet(t, ts, "/projects.xml?include=trackers")
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/xml")
		if len(xmlSelect(t, r.XML(t), `trackers[type=array] tracker[id="2"][name="Feature request"]`)) == 0 {
			t.Error("tracker 2 missing")
		}
	})
	t.Run("GET /projects.xml with include=enabled_modules should return enabled modules", func(t *testing.T) {
		r := apiGet(t, ts, "/projects.xml?include=enabled_modules")
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/xml")
		if len(xmlSelect(t, r.XML(t), `enabled_modules[type=array] enabled_module[name=issue_tracking]`)) == 0 {
			t.Error("issue_tracking missing")
		}
	})
	t.Run("GET /projects/:id.json should return the project", func(t *testing.T) {
		r := apiGet(t, ts, "/projects/1.json")
		r.expectStatus(t, 200)
		m := r.JSON(t)
		assertJSON(t, m, "project.id", "1")
		assertJSON(t, m, "project.inherit_members", "false")
		assertJSON(t, m, "project.homepage", "http://ecookbook.somenet.foo/")
		p := m["project"].(map[string]any)
		for _, k := range []string{"default_version", "default_assignee"} {
			if _, ok := p[k]; ok {
				t.Errorf("unexpected key %s", k)
			}
		}
	})
	t.Run("GET /projects/:id.xml with include=issue_categories should return categories", func(t *testing.T) {
		r := apiGet(t, ts, "/projects/1.xml?include=issue_categories")
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/xml")
		if len(xmlSelect(t, r.XML(t), `issue_categories[type=array] issue_category[id="2"][name=Recipes]`)) == 0 {
			t.Error("category 2 missing")
		}
	})
	t.Run("GET /projects/:id.xml with include=time_entry_activities should return activities", func(t *testing.T) {
		r := apiGet(t, ts, "/projects/1.xml?include=time_entry_activities")
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/xml")
		if len(xmlSelect(t, r.XML(t), `time_entry_activities[type=array] time_entry_activity[id="10"][name=Development]`)) == 0 {
			t.Error("activity 10 missing")
		}
	})
	t.Run("GET /projects/:id.xml with include=trackers should return trackers", func(t *testing.T) {
		r := apiGet(t, ts, "/projects/1.xml?include=trackers")
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/xml")
		if len(xmlSelect(t, r.XML(t), `trackers[type=array] tracker[id="2"][name="Feature request"]`)) == 0 {
			t.Error("tracker 2 missing")
		}
	})
	t.Run("GET /projects/:id.xml with include=enabled_modules should return enabled modules", func(t *testing.T) {
		r := apiGet(t, ts, "/projects/1.xml?include=enabled_modules")
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/xml")
		if len(xmlSelect(t, r.XML(t), `enabled_modules[type=array] enabled_module[name=issue_tracking]`)) == 0 {
			t.Error("issue_tracking missing")
		}
	})
	_ = d

	t.Run("GET /projects.xml with include=issue_custom_fields should return custom fields", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		mastersExec(t, d, `UPDATE custom_fields SET is_for_all = 1 WHERE id = 6`)
		mastersExec(t, d, `UPDATE custom_fields SET is_for_all = 0 WHERE id = 8`)
		r := apiGet(t, ts, "/projects.xml?include=issue_custom_fields")
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/xml")
		doc := r.XML(t)
		if len(xmlSelect(t, doc, `issue_custom_fields[type=array] custom_field[name="Project 1 cf"]`)) == 0 {
			t.Error("Project 1 cf missing")
		}
		// 全プロジェクト向けのカスタムフィールド
		if len(xmlSelect(t, doc, `issue_custom_fields[type=array] custom_field[id="6"]`)) == 0 {
			t.Error("custom field 6 missing")
		}
		assertXMLCount(t, doc, `issue_custom_fields[type=array] custom_field[id="8"]`, 0)
	})
	t.Run("GET /projects/:id.xml should return the project", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		mastersExec(t, d, `UPDATE projects SET inherit_members = 1 WHERE id = 1`)
		r := apiGet(t, ts, "/projects/1.xml")
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/xml")
		doc := r.XML(t)
		assertXMLText(t, doc, "project > id", "1")
		assertXMLText(t, doc, "project > status", "1")
		assertXMLText(t, doc, "project > is_public", "true")
		assertXMLText(t, doc, "project > inherit_members", "true")
		assertXMLText(t, doc, "project > homepage", "http://ecookbook.somenet.foo/")
		assertXMLText(t, doc, `custom_field[name="Development status"]`, "Stable")
		assertXMLCount(t, doc, "trackers", 0)
		assertXMLCount(t, doc, "issue_categories", 0)
	})
	t.Run("GET /projects/:id.xml with hidden custom fields should not display hidden custom fields", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		mastersExec(t, d, `UPDATE custom_fields SET visible = 0 WHERE owner_kind = 'project' AND name = 'Development status'`)
		r := apiGet(t, ts, "/projects/1.xml")
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/xml")
		assertXMLCount(t, r.XML(t), `custom_field[name="Development status"]`, 0)
	})
	t.Run("GET /projects/:id.xml with include=trackers should return trackers based on role-based permissioning", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		// role.permissions_all_trackers = {'view_issues' => '0'}; permissions_tracker_ids = {'view_issues' => ['1']}
		if mastersExec(t, d, `UPDATE role_permissions SET all_trackers = 0 WHERE role_id = 3 AND permission = 'view_issues'`) != 1 {
			t.Fatal("role 3 has no view_issues")
		}
		mastersExec(t, d, `INSERT INTO role_permission_trackers (role_id, permission, tracker_id) VALUES (3, 'view_issues', 1)`)
		// jsmith のプロジェクト 1 のロールを Reporter のみにする
		mid := mastersInt(t, d, `SELECT id FROM members WHERE project_id = 1 AND principal_id = 2`)
		mastersExec(t, d, `DELETE FROM member_roles WHERE member_id = ?`, mid)
		mastersExec(t, d, `INSERT INTO member_roles (member_id, role_id) VALUES (?, 3)`, mid)
		r := apiGet(t, ts, "/projects/1.xml?include=trackers", apiCreds("jsmith"))
		r.expectStatus(t, 200)
		mastersMedia(t, r, "application/xml")
		doc := r.XML(t)
		assertXMLCount(t, doc, `trackers[type=array] tracker[id="1"]`, 1)
		assertXMLCount(t, doc, `trackers[type=array] tracker[id="2"]`, 0)
		assertXMLCount(t, doc, `trackers[type=array] tracker[id="3"]`, 0)
	})
	t.Run("test_get_project_with_default_version_and_assignee", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		mastersExec(t, d, `UPDATE projects SET default_assigned_to_id = 3, default_version_id = 1 WHERE id = 1`)
		r := apiGet(t, ts, "/projects/1.json")
		r.expectStatus(t, 200)
		m := r.JSON(t)
		assertJSON(t, m, "project.id", "1")
		p := m["project"].(map[string]any)
		da, _ := p["default_assignee"].(map[string]any)
		dv, _ := p["default_version"].(map[string]any)
		if len(da) != 2 || len(dv) != 2 {
			t.Fatalf("default_assignee=%v default_version=%v", p["default_assignee"], p["default_version"])
		}
		assertJSON(t, m, "project.default_assignee.id", "3")
		assertJSON(t, m, "project.default_assignee.name", "Dave Lopper")
		assertJSON(t, m, "project.default_version.id", "1")
		assertJSON(t, m, "project.default_version.name", "0.1")
	})
	t.Run("test_get_project_should_not_load_default_query", func(t *testing.T) {
		srv, ts, _ := newFixtureServerFull(t)
		// ProjectQuery.stubs(:default).returns ProjectQuery.find(11)
		if err := srv.App().Settings.Set(context.Background(), "default_project_query", "11"); err != nil {
			t.Fatal(err)
		}
		r := apiGet(t, ts, "/projects.json")
		r.expectStatus(t, 200)
		var v struct {
			Projects []struct {
				Name string `json:"name"`
			} `json:"projects"`
		}
		if err := json.Unmarshal([]byte(r.Body), &v); err != nil {
			t.Fatal(err)
		}
		if len(v.Projects) != 4 {
			t.Errorf("projects = %d, want 4", len(v.Projects))
		}
		if !slices.ContainsFunc(v.Projects, func(p struct {
			Name string `json:"name"`
		}) bool {
			return p.Name == "eCookbook"
		}) {
			t.Error("eCookbook missing")
		}
	})

	t.Run("POST /projects.xml with valid parameters should create the project", func(t *testing.T) {
		srv, ts, d := newFixtureServerFull(t)
		if err := srv.App().Settings.Set(context.Background(), "default_projects_modules", []string{"issue_tracking", "repository"}); err != nil {
			t.Fatal(err)
		}
		before := mastersInt(t, d, `SELECT COUNT(*) FROM projects`)
		r := mastersForm(t, ts, "POST", "/projects.xml", "project[name]=API+test&project[identifier]=api-test", apiCreds("admin"))
		r.expectStatus(t, 201)
		mastersMedia(t, r, "application/xml")
		if n := mastersInt(t, d, `SELECT COUNT(*) FROM projects`); n != before+1 {
			t.Fatalf("projects %d -> %d", before, n)
		}
		id := mastersInt(t, d, `SELECT MAX(id) FROM projects`)
		if s := mastersStr(t, d, `SELECT name || '/' || identifier FROM projects WHERE id = ?`, id); s != "API test/api-test" {
			t.Errorf("project = %s", s)
		}
		if s := mastersStr(t, d, `SELECT group_concat(name, ',') FROM (SELECT name FROM project_modules WHERE project_id = ? ORDER BY name)`, id); s != "issue_tracking,repository" {
			t.Errorf("modules = %s", s)
		}
		if n, all := mastersInt(t, d, `SELECT COUNT(*) FROM project_trackers WHERE project_id = ?`, id), mastersInt(t, d, `SELECT COUNT(*) FROM trackers`); n != all {
			t.Errorf("trackers = %d, want %d", n, all)
		}
		assertXMLText(t, r.XML(t), "project id", itoa64(id))
		// :location => url_for(:controller => 'projects', :action => 'show', :id => @project.id)
		if loc := r.Header.Get("Location"); !strings.HasSuffix(loc, "/projects/"+itoa64(id)) {
			t.Errorf("Location = %q", loc)
		}
	})
	t.Run("POST /projects.xml should accept enabled_module_names attribute", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		r := mastersForm(t, ts, "POST", "/projects.xml",
			"project[name]=API+test&project[identifier]=api-test&project[enabled_module_names][]=issue_tracking&project[enabled_module_names][]=news&project[enabled_module_names][]=time_tracking",
			apiCreds("admin"))
		r.expectStatus(t, 201)
		id := mastersInt(t, d, `SELECT MAX(id) FROM projects`)
		if s := mastersStr(t, d, `SELECT group_concat(name, ',') FROM (SELECT name FROM project_modules WHERE project_id = ? ORDER BY name)`, id); s != "issue_tracking,news,time_tracking" {
			t.Errorf("modules = %s", s)
		}
	})
	t.Run("POST /projects.xml should accept tracker_ids attribute", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		r := mastersForm(t, ts, "POST", "/projects.xml",
			"project[name]=API+test&project[identifier]=api-test&project[tracker_ids][]=1&project[tracker_ids][]=3", apiCreds("admin"))
		r.expectStatus(t, 201)
		id := mastersInt(t, d, `SELECT MAX(id) FROM projects`)
		if s := mastersStr(t, d, `SELECT group_concat(tracker_id, ',') FROM (SELECT tracker_id FROM project_trackers WHERE project_id = ? ORDER BY tracker_id)`, id); s != "1,3" {
			t.Errorf("trackers = %s", s)
		}
	})
	t.Run("POST /projects.json with custom_fields and enabled_module_names", func(t *testing.T) {
		// 互換シナリオで見つかった差分: acts_as_customizable#custom_fields= と、enabled_modules の作成順
		// （Project.new の既定モジュール（Setting.default_projects_modules の順）に対する has_many の replace）
		ts, d := newFixtureServer(t)
		r := apiCall(t, ts, "POST", "/projects.json", "",
			`{"project":{"name":"API cf","identifier":"api-cf","enabled_module_names":["issue_tracking","wiki","news"],"custom_fields":[{"id":3,"value":"Beta"}]}}`,
			apiCreds("admin"))
		r.expectStatus(t, 201)
		m := r.JSON(t)
		assertJSON(t, m, "project.custom_fields.0.id", "3")
		assertJSON(t, m, "project.custom_fields.0.value", "Beta")
		id := mastersInt(t, d, `SELECT MAX(id) FROM projects`)
		if s := mastersStr(t, d, `SELECT group_concat(name, ',') FROM (SELECT name FROM project_modules WHERE project_id = ? ORDER BY id)`, id); s != "issue_tracking,news,wiki" {
			t.Errorf("modules (id order) = %s, want issue_tracking,news,wiki", s)
		}
		r = apiCall(t, ts, "PUT", "/projects/api-cf.json", "", `{"project":{"custom_fields":[{"id":3,"value":"Alpha"}]}}`, apiCreds("admin"))
		mastersNoContent(t, r)
		r = apiGet(t, ts, "/projects/api-cf.json", apiCreds("admin"))
		assertJSON(t, r.JSON(t), "project.custom_fields.0.value", "Alpha")
	})
	t.Run("POST /projects.xml with invalid parameters should return errors", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		before := mastersInt(t, d, `SELECT COUNT(*) FROM projects`)
		r := mastersForm(t, ts, "POST", "/projects.xml", "project[name]=API+test", apiCreds("admin"))
		r.expectStatus(t, 422)
		mastersMedia(t, r, "application/xml")
		if len(xmlSelect(t, r.XML(t), `errors error:contains("Identifier cannot be blank")`)) == 0 {
			t.Errorf("errors: %s", r.Body)
		}
		if n := mastersInt(t, d, `SELECT COUNT(*) FROM projects`); n != before {
			t.Error("project created")
		}
	})
	t.Run("PUT /projects/:id.xml with valid parameters should update the project", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		r := mastersForm(t, ts, "PUT", "/projects/2.xml", "project[name]=API+update", apiCreds("jsmith"))
		mastersNoContent(t, r)
		if ct := r.Header.Get("Content-Type"); ct != "" {
			t.Errorf("Content-Type = %q, want none", ct)
		}
		if s := mastersStr(t, d, `SELECT name FROM projects WHERE id = 2`); s != "API update" {
			t.Errorf("name = %s", s)
		}
	})
	t.Run("PUT /projects/:id.xml should accept enabled_module_names attribute", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		r := mastersForm(t, ts, "PUT", "/projects/2.xml",
			"project[name]=API+update&project[enabled_module_names][]=issue_tracking&project[enabled_module_names][]=news&project[enabled_module_names][]=time_tracking",
			apiCreds("admin"))
		mastersNoContent(t, r)
		if s := mastersStr(t, d, `SELECT group_concat(name, ',') FROM (SELECT name FROM project_modules WHERE project_id = 2 ORDER BY name)`); s != "issue_tracking,news,time_tracking" {
			t.Errorf("modules = %s", s)
		}
	})
	t.Run("PUT /projects/:id.xml should accept tracker_ids attribute", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		r := mastersForm(t, ts, "PUT", "/projects/2.xml",
			"project[name]=API+update&project[tracker_ids][]=1&project[tracker_ids][]=3", apiCreds("admin"))
		mastersNoContent(t, r)
		if s := mastersStr(t, d, `SELECT group_concat(tracker_id, ',') FROM (SELECT tracker_id FROM project_trackers WHERE project_id = 2 ORDER BY tracker_id)`); s != "1,3" {
			t.Errorf("trackers = %s", s)
		}
	})
	t.Run("PUT /projects/:id.xml with invalid parameters should return errors", func(t *testing.T) {
		ts, _ := newFixtureServer(t)
		r := mastersForm(t, ts, "PUT", "/projects/2.xml", "project[name]=", apiCreds("admin"))
		r.expectStatus(t, 422)
		mastersMedia(t, r, "application/xml")
		if len(xmlSelect(t, r.XML(t), `errors error:contains("Name cannot be blank")`)) == 0 {
			t.Errorf("errors: %s", r.Body)
		}
	})
	t.Run("DELETE /projects/:id.xml should schedule deletion of the project", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		r := apiCall(t, ts, "DELETE", "/projects/2.xml", "", "", apiCreds("admin"))
		mastersNoContent(t, r)
		// Redmine は DestroyProjectJob で非同期に削除し、ジョブの実行前は削除予約（STATUS_SCHEDULED_FOR_DELETION = 10）で
		// 残る。buropher は削除予約にしたあと同じリクエスト内で削除する（projects_admin.go の destroyProjects）ため、消えていてもよい。
		if st := mastersInt(t, d, `SELECT COALESCE((SELECT status FROM projects WHERE id = 2), 0)`); st != 10 && st != 0 {
			t.Errorf("status = %d, want 10 (scheduled for deletion)", st)
		}
	})
	t.Run("PUT /projects/:id/archive.xml should archive project", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		mastersNoContent(t, apiCall(t, ts, "PUT", "/projects/1/archive.xml", "", "", apiCreds("admin")))
		if st := mastersInt(t, d, `SELECT status FROM projects WHERE id = 1`); st == 1 {
			t.Error("project still active")
		}
	})
	t.Run("PUT /projects/:id/unarchive.xml should unarchive project", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		mastersExec(t, d, `UPDATE projects SET status = 9 WHERE id = 1`)
		mastersNoContent(t, apiCall(t, ts, "PUT", "/projects/1/unarchive.xml", "", "", apiCreds("admin")))
		if st := mastersInt(t, d, `SELECT status FROM projects WHERE id = 1`); st != 1 {
			t.Errorf("status = %d, want 1", st)
		}
	})
	t.Run("PUT /projects/:id/close.xml should close project", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		mastersNoContent(t, apiCall(t, ts, "PUT", "/projects/1/close.xml", "", "", apiCreds("admin")))
		if st := mastersInt(t, d, `SELECT status FROM projects WHERE id = 1`); st != 5 {
			t.Errorf("status = %d, want 5", st)
		}
	})
	t.Run("PUT /projects/:id/reopen.xml should reopen project", func(t *testing.T) {
		ts, d := newFixtureServer(t)
		mastersExec(t, d, `UPDATE projects SET status = 5 WHERE id = 1`)
		mastersNoContent(t, apiCall(t, ts, "PUT", "/projects/1/reopen.xml", "", "", apiCreds("admin")))
		if st := mastersInt(t, d, `SELECT status FROM projects WHERE id = 1`); st != 1 {
			t.Errorf("status = %d, want 1", st)
		}
	})
}
