package authz_test

// test/unit/user_test.rb の権限関連テストの移植。

import (
	"reflect"
	"slices"
	"testing"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/testfixtures"
)

func TestRolesForProject(t *testing.T) {
	const (
		nonMember = 4
		anonymous = 5
	)
	cases := []struct {
		name    string
		user    int64 // 0 = 匿名
		private bool
		// override は組込グループ (匿名ユーザなら GroupAnonymous) に roles [1, 2] を与える
		override bool
		want     []int64
	}{
		{"member on public project", 2, false, false, []int64{1}},
		{"member on private project", 2, true, false, []int64{1}},
		{"non member with public project", 8, false, false, []int64{nonMember}},
		{"non member with public project and override", 8, false, true, []int64{1, 2}},
		{"non member with private project", 8, true, false, []int64{}},
		{"non member with private project and override", 8, true, true, []int64{}},
		{"anonymous with public project", 0, false, false, []int64{anonymous}},
		{"anonymous with public project and override", 0, false, true, []int64{1, 2}},
		{"anonymous with private project", 0, true, false, []int64{}},
		{"anonymous with private project and override", 0, true, true, []int64{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withFixtures(t, func(e *env) {
				if c.private {
					e.updateProject(1, func(p *domain.Project) { p.IsPublic = false })
				}
				u := e.anonymous()
				if c.user != 0 {
					u = e.user(c.user)
				}
				if c.override {
					e.createMember(1, e.builtinGroup(u.BuiltinGroupKind()), 1, 2)
				}
				if got := e.rolesForProject(u, 1); !slices.Equal(got, c.want) {
					t.Errorf("roles = %v, want %v", got, c.want)
				}
			})
		})
	}
}

func TestRolesForProjectShouldBeUnique(t *testing.T) {
	withFixtures(t, func(e *env) {
		e.createMember(1, 1, 1, 1)
		if got := e.rolesForProject(e.user(1), 1); !slices.Equal(got, []int64{1}) {
			t.Errorf("roles = %v", got)
		}
	})
}

func TestProjectIDsByRole(t *testing.T) {
	withFixtures(t, func(e *env) {
		// test_projects_by_role_for_user_with_role
		pibr, err := e.az(e.user(2)).ProjectIDsByRole(e.ctx)
		e.must(err)
		got := map[int64][]int64{}
		var all []int64
		for _, rp := range pibr {
			ids := slices.Sorted(slices.Values(rp.ProjectIDs))
			got[rp.Role.ID] = ids
			all = append(all, ids...)
		}
		if want := map[int64][]int64{1: {1, 5}, 2: {2}}; !reflect.DeepEqual(got, want) {
			t.Errorf("project_ids_by_role = %v, want %v", got, want)
		}
		slices.Sort(all)
		if !slices.Equal(all, []int64{1, 2, 5}) {
			t.Errorf("flattened = %v", all)
		}
		// test_projects_by_role_for_user_with_no_role
		uid := testfixtures.GenerateUser(t, e.d)
		pibr, err = e.az(e.user(uid)).ProjectIDsByRole(e.ctx)
		e.must(err)
		if len(pibr) != 0 {
			t.Errorf("new user project_ids_by_role = %v", pibr)
		}
	})
}

func TestAllowedTo(t *testing.T) {
	t.Run("archived project should return false", func(t *testing.T) {
		withFixtures(t, func(e *env) {
			ok, err := repository.ArchiveProject(e.ctx, e.d, 1)
			e.must(err)
			if !ok {
				t.Fatal("archive failed")
			}
			if e.allowed(e.user(1), "view_issues", e.project(1)) {
				t.Error("admin allowed on archived project")
			}
		})
	})
	t.Run("closed project should return true for read actions", func(t *testing.T) {
		withFixtures(t, func(e *env) {
			e.must(repository.CloseProject(e.ctx, e.d, 1))
			p, admin := e.project(1), e.user(1)
			if e.allowed(admin, "edit_project", p) {
				t.Error("edit_project allowed on closed project")
			}
			if !e.allowed(admin, "view_project", p) {
				t.Error("view_project denied on closed project")
			}
		})
	})
	t.Run("project with module disabled should return false", func(t *testing.T) {
		withFixtures(t, func(e *env) {
			e.must(repository.SetEnabledModules(e.ctx, e.d, 1, []string{"issue_tracking"}))
			p, admin := e.project(1), e.user(1)
			if !e.allowed(admin, "add_issues", p) || e.allowed(admin, "view_wiki_pages", p) {
				t.Error("module restriction mismatch")
			}
		})
	})
	withFixtures(t, func(e *env) {
		p := e.project(1)
		admin, jsmith, dlopper := e.user(1), e.user(2), e.user(3)
		// admin users should return true
		m, err := repository.Membership(e.ctx, e.d, 1, 1)
		e.must(err)
		if m != nil {
			t.Fatal("admin should not be a member of project 1")
		}
		for _, perm := range []string{"edit_issues", "delete_issues", "manage_news", "add_documents", "manage_wiki"} {
			if !e.allowed(admin, perm, p) {
				t.Errorf("admin denied %s", perm)
			}
		}
		// normal users
		if !e.allowed(jsmith, "delete_messages", p) {
			t.Error("Manager should delete_messages")
		}
		if e.allowed(dlopper, "delete_messages", p) {
			t.Error("Developer should not delete_messages")
		}
		// empty array should return false
		ok, err := e.az(admin).AllowedToProjects(e.ctx, domain.Perm("view_project"), nil, nil)
		e.must(err)
		if ok {
			t.Error("empty array allowed")
		}
		// multiple projects
		all, err := repository.ListProjects(e.ctx, e.d)
		e.must(err)
		check := func(u *domain.User, perm string, ps []*domain.Project, want bool) {
			t.Helper()
			ok, err := e.az(u).AllowedToProjects(e.ctx, domain.Perm(perm), ps, nil)
			e.must(err)
			if ok != want {
				t.Errorf("user %d allowed_to?(%s, %d projects) = %v, want %v", u.ID, perm, len(ps), ok, want)
			}
		}
		check(admin, "view_project", all, true)
		check(dlopper, "view_project", all, false)
		jsmithProjects, err := repository.MemberProjectIDs(e.ctx, e.d, 2)
		e.must(err)
		check(jsmith, "edit_issues", e.projects(jsmithProjects...), true)
		check(jsmith, "delete_issue_watchers", e.projects(jsmithProjects...), false)
		// options[:global]
		dlopper2, anonymous := e.user(5), e.user(6)
		for _, c := range []struct {
			u    *domain.User
			perm string
			want bool
		}{
			{jsmith, "delete_issue_watchers", true},
			{dlopper2, "delete_issue_watchers", false},
			{dlopper2, "add_issues", true},
			{anonymous, "add_issues", false},
			{anonymous, "view_issues", true},
		} {
			if got := e.global(c.u, c.perm); got != c.want {
				t.Errorf("user %d allowed_to_globally?(%s) = %v, want %v", c.u.ID, c.perm, got, c.want)
			}
		}
	})
}

func TestOAuthScope(t *testing.T) {
	withFixtures(t, func(e *env) {
		// test_should_recognize_authorized_by_oauth
		u := e.user(2)
		if u.AuthorizedByOAuth() {
			t.Error("not authorized by oauth")
		}
		u.OAuthScope = []string{"add_issues", "view_issues"}
		if !u.AuthorizedByOAuth() {
			t.Error("authorized by oauth")
		}
		// test_admin_should_be_limited_by_oauth_scope
		admin := e.user(1)
		if !admin.IsAdmin() {
			t.Fatal("admin")
		}
		admin.OAuthScope = []string{"add_issues", "view_issues"}
		if admin.IsAdmin() {
			t.Error("admin without admin scope")
		}
		admin.OAuthScope = append(admin.OAuthScope, "admin")
		if !admin.IsAdmin() {
			t.Error("admin with admin scope")
		}
		user := e.user(2)
		user.OAuthScope = []string{"add_issues", "view_issues", "admin"}
		if user.IsAdmin() {
			t.Error("non admin with admin scope")
		}
		// test_oauth_scope_should_limit_global_user_permissions
		for _, id := range []int64{1, 2} {
			u := e.user(id)
			if !e.global(u, "add_issues") || !e.global(u, "view_issues") {
				t.Errorf("user %d global without scope", id)
			}
			u.OAuthScope = []string{"view_issues"}
			if e.global(u, "add_issues") || !e.global(u, "view_issues") {
				t.Errorf("user %d global with scope", id)
			}
		}
		// test_oauth_scope_should_limit_project_user_permissions
		admin = e.user(1)
		p5 := e.project(5)
		if !e.allowed(admin, "add_issues", p5) || !e.allowed(admin, "view_issues", p5) {
			t.Error("admin project 5")
		}
		admin.OAuthScope = []string{"view_issues"}
		if e.allowed(admin, "add_issues", p5) || !e.allowed(admin, "view_issues", p5) {
			t.Error("admin project 5 scoped")
		}
		admin.OAuthScope = []string{"view_issues", "admin"}
		if !e.allowed(admin, "add_issues", p5) || !e.allowed(admin, "view_issues", p5) {
			t.Error("admin project 5 admin scope")
		}
		user = e.user(2)
		p1 := e.project(1)
		if !e.allowed(user, "add_issues", p1) || !e.allowed(user, "view_issues", p1) {
			t.Error("user project 1")
		}
		user.OAuthScope = []string{"view_issues"}
		if e.allowed(user, "add_issues", p1) || !e.allowed(user, "view_issues", p1) {
			t.Error("user project 1 scoped")
		}
		user.OAuthScope = []string{"view_issues", "admin"}
		if e.allowed(user, "add_issues", p1) || !e.allowed(user, "view_issues", p1) {
			t.Error("user project 1 admin scope")
		}
	})
}

func TestAnonymousUserIsCreatedIfMissing(t *testing.T) {
	withFixtures(t, func(e *env) {
		_, err := e.d.Exec(e.ctx, `DELETE FROM principals WHERE kind = 'anonymous_user'`)
		e.must(err)
		u := e.anonymous()
		if u.Kind != domain.KindAnonymousUser || u.Lastname != "Anonymous" || u.Status != domain.StatusAnonymous || u.Logged() {
			t.Errorf("anonymous = %+v", u)
		}
		if u2 := e.anonymous(); u2.ID != u.ID {
			t.Error("anonymous user created twice")
		}
	})
}

func TestFindUserByLogin(t *testing.T) {
	withFixtures(t, func(e *env) {
		// test .try_to_login should fall-back to case-insensitive
		u, err := repository.FindUserByLogin(e.ctx, e.d, "JSmith")
		e.must(err)
		if u.ID != 2 {
			t.Errorf("FindUserByLogin(JSmith) = %d", u.ID)
		}
		if _, err := repository.FindUserByLogin(e.ctx, e.d, ""); err != repository.ErrNotFound {
			t.Errorf("blank login: %v", err)
		}
		if _, err := repository.FindUserByLogin(e.ctx, e.d, "nobody"); err != repository.ErrNotFound {
			t.Errorf("unknown login: %v", err)
		}
	})
}
