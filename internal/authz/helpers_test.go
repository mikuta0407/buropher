package authz_test

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"testing"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/testfixtures"
)

// env は Redmine の公式フィクスチャを投入した DB 上でのテスト環境。
type env struct {
	t   *testing.T
	ctx context.Context
	d   *db.DB
}

// withFixtures は各 dialect で全フィクスチャを投入した DB を用意して fn を実行する。
func withFixtures(t *testing.T, fn func(e *env)) {
	t.Helper()
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		testfixtures.Load(t, d, testfixtures.All()...)
		fn(&env{t: t, ctx: context.Background(), d: d})
	})
}

func (e *env) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) user(id int64) *domain.User {
	e.t.Helper()
	u, err := repository.GetUser(e.ctx, e.d, id)
	e.must(err)
	return u
}

func (e *env) anonymous() *domain.User {
	e.t.Helper()
	u, err := repository.AnonymousUser(e.ctx, e.d)
	e.must(err)
	return u
}

func (e *env) az(u *domain.User) *authz.Authorizer { return authz.New(e.d, u) }

func (e *env) project(id int64) *domain.Project {
	e.t.Helper()
	p, err := repository.GetProject(e.ctx, e.d, id)
	e.must(err)
	return p
}

func (e *env) projects(ids ...int64) []*domain.Project {
	var out []*domain.Project
	for _, id := range ids {
		out = append(out, e.project(id))
	}
	return out
}

func (e *env) allowed(u *domain.User, perm string, p *domain.Project) bool {
	e.t.Helper()
	ok, err := e.az(u).AllowedTo(e.ctx, domain.Perm(perm), p)
	e.must(err)
	return ok
}

func (e *env) global(u *domain.User, perm string) bool {
	e.t.Helper()
	ok, err := e.az(u).AllowedToGlobally(e.ctx, domain.Perm(perm), nil)
	e.must(err)
	return ok
}

func (e *env) rolesForProject(u *domain.User, pid int64) []int64 {
	e.t.Helper()
	rs, err := e.az(u).RolesForProject(e.ctx, e.project(pid))
	e.must(err)
	return sortedRoleIDs(rs)
}

func sortedRoleIDs(rs []*domain.Role) []int64 {
	out := []int64{}
	for _, r := range rs {
		out = append(out, r.ID)
	}
	slices.Sort(out)
	return out
}

func (e *env) updateProject(id int64, f func(p *domain.Project)) {
	e.t.Helper()
	p := e.project(id)
	f(p)
	e.must(repository.UpdateProject(e.ctx, e.d, p))
}

func (e *env) updateRole(id int64, f func(r *domain.Role)) {
	e.t.Helper()
	r, err := repository.GetRole(e.ctx, e.d, id)
	e.must(err)
	f(r)
	e.must(repository.SaveRole(e.ctx, e.d, r))
}

func (e *env) builtinRoleID(builtin int) int64 {
	e.t.Helper()
	r, err := repository.BuiltinRole(e.ctx, e.d, builtin)
	e.must(err)
	return r.ID
}

func (e *env) removePermission(roleID int64, perm string) {
	e.updateRole(roleID, func(r *domain.Role) {
		r.Permissions = slices.DeleteFunc(r.Permissions, func(p string) bool { return p == perm })
	})
}

func (e *env) addPermission(roleID int64, perm string) {
	e.updateRole(roleID, func(r *domain.Role) {
		if !slices.Contains(r.Permissions, perm) {
			r.Permissions = append(r.Permissions, perm)
		}
	})
}

// generateRole は Role.generate! 相当 (名前 Role<N>, 指定権限)。
func (e *env) generateRole(perms ...string) int64 {
	e.t.Helper()
	var n int
	e.must(e.d.Get(e.ctx, &n, `SELECT COUNT(*) FROM roles`))
	r := &domain.Role{Name: fmt.Sprintf("Generated role %d", n+1), Assignable: true, AllRolesManaged: true, Permissions: perms}
	e.must(repository.SaveRole(e.ctx, e.d, r))
	return r.ID
}

func (e *env) createMember(projectID, principalID int64, roleIDs ...int64) int64 {
	e.t.Helper()
	id, err := repository.CreateMember(e.ctx, e.d, projectID, principalID, roleIDs)
	e.must(err)
	return id
}

func (e *env) builtinGroup(kind domain.PrincipalKind) int64 {
	e.t.Helper()
	id, err := repository.BuiltinGroupID(e.ctx, e.d, kind)
	e.must(err)
	return id
}

// visibleIssues は Issue.visible(user, opts) の id を返す。
func (e *env) visibleIssues(u *domain.User, opts authz.ConditionOptions, extraWhere string) []int64 {
	e.t.Helper()
	cond, err := e.az(u).IssueVisibleCondition(e.ctx, opts)
	e.must(err)
	q := `SELECT issues.id FROM issues JOIN projects ON projects.id = issues.project_id WHERE (` + cond + `)`
	if extraWhere != "" {
		q += ` AND ` + extraWhere
	}
	var ids []int64
	e.must(e.d.Select(e.ctx, &ids, q+` ORDER BY issues.id`))
	return ids
}

func (e *env) issues(where string) []domain.Issue {
	e.t.Helper()
	if where == "" {
		where = "1=1"
	}
	var is []domain.Issue
	e.must(e.d.Select(e.ctx, &is, `SELECT id, project_id AS projectid, tracker_id AS trackerid, status_id AS statusid,
  author_id AS authorid, assigned_to_id AS assignedtoid, is_private AS isprivate FROM issues WHERE `+where+` ORDER BY id`))
	return is
}

func (e *env) issueVisible(u *domain.User, issueID int64) bool {
	e.t.Helper()
	is := e.issues("id = " + itoa(issueID))
	if len(is) != 1 {
		e.t.Fatalf("issue %d not found", issueID)
	}
	ok, err := e.az(u).IssueVisible(e.ctx, &is[0], e.project(is[0].ProjectID))
	e.must(err)
	return ok
}

// assertVisibilityMatch は Redmine の assert_visibility_match: SQL スコープの結果が
// Issue#visible? の結果と一致することを確認する。
func (e *env) assertVisibilityMatch(u *domain.User, visible []int64) {
	e.t.Helper()
	var want []int64
	for _, is := range e.issues("") {
		if e.issueVisible(u, is.ID) {
			want = append(want, is.ID)
		}
	}
	if !slices.Equal(visible, want) {
		e.t.Errorf("visible scope %v does not match Issue#visible? %v", visible, want)
	}
}

func (e *env) issueProjects(ids []int64) []int64 {
	var out []int64
	for _, is := range e.issues("") {
		if slices.Contains(ids, is.ID) && !slices.Contains(out, is.ProjectID) {
			out = append(out, is.ProjectID)
		}
	}
	slices.Sort(out)
	return out
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
