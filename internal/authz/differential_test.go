// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package authz_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/testfixtures"
)

// ground_truth.json は testdata/gen/dump_ground_truth.rb を Redmine 7.0.1 (公式フィクスチャ) で
// 実行して生成した正解データ。
//
//go:embed testdata/ground_truth.json
var groundTruthJSON []byte

type gtUser struct {
	Allowed                 map[string]string  `json:"allowed"`
	Actions                 map[string]string  `json:"actions"`
	RolesForProject         map[string][]int64 `json:"roles_for_project"`
	ManagedRoles            map[string][]int64 `json:"managed_roles"`
	ViewAllTimeEntries      map[string]bool    `json:"view_all_time_entries"`
	AllowedAllProjects      string             `json:"allowed_all_projects"`
	Global                  string             `json:"global"`
	AllowedProjects         map[string][]int64 `json:"allowed_projects"`
	AllowedProjectsMember   map[string][]int64 `json:"allowed_projects_member"`
	VisibleIssues           []int64            `json:"visible_issues"`
	VisibleIssuesProject1   []int64            `json:"visible_issues_project1"`
	VisibleIssuesProject1Sb []int64            `json:"visible_issues_project1_sub"`
	IssueVisible            string             `json:"issue_visible"`
	VisiblePrincipals       []int64            `json:"visible_principals"`
	VisibleProjectIDs       []int64            `json:"visible_project_ids"`
	Roles                   []int64            `json:"roles"`
	ProjectIDsByRole        map[string][]int64 `json:"project_ids_by_role"`
}

type gtScenario struct {
	ProjectStatus map[string]int     `json:"project_status"`
	ProjectTree   []int64            `json:"project_tree"`
	Members       []json.RawMessage  `json:"members"`
	Users         map[string]*gtUser `json:"users"`
}

type groundTruth struct {
	Permissions []string               `json:"permissions"`
	Actions     []string               `json:"actions"`
	Scenarios   map[string]*gtScenario `json:"scenarios"`
}

// mutation は Ruby 側のシナリオを repository の書き込み操作で再現する。
type mutation func(ctx context.Context, t *testing.T, q db.Queryer)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustProject(ctx context.Context, t *testing.T, q db.Queryer, id int64) *domain.Project {
	t.Helper()
	p, err := repository.GetProject(ctx, q, id)
	must(t, err)
	return p
}

func ptr(v int64) *int64 { return &v }

func updateProject(ctx context.Context, t *testing.T, q db.Queryer, id int64, f func(p *domain.Project)) {
	t.Helper()
	p := mustProject(ctx, t, q, id)
	f(p)
	must(t, repository.UpdateProject(ctx, q, p, nil))
}

func updateRole(ctx context.Context, t *testing.T, q db.Queryer, id int64, f func(r *domain.Role)) {
	t.Helper()
	r, err := repository.GetRole(ctx, q, id)
	must(t, err)
	f(r)
	must(t, repository.SaveRole(ctx, q, r))
}

// setPermissionTrackers は Role#set_permission_trackers 相当。
func setPermissionTrackers(r *domain.Role, perm string, trackerIDs []int64) {
	if r.RestrictedTrackers == nil {
		r.RestrictedTrackers = map[string][]int64{}
	}
	r.RestrictedTrackers[perm] = trackerIDs
}

var mutations = map[string]mutation{
	"base": func(context.Context, *testing.T, db.Queryer) {},
	"closed_project1": func(ctx context.Context, t *testing.T, q db.Queryer) {
		must(t, repository.CloseProject(ctx, q, 1))
	},
	"archived_project5": func(ctx context.Context, t *testing.T, q db.Queryer) {
		ok, err := repository.ArchiveProject(ctx, q, 5)
		must(t, err)
		if !ok {
			t.Fatal("archive failed")
		}
	},
	"archived_project1": func(ctx context.Context, t *testing.T, q db.Queryer) {
		ok, err := repository.ArchiveProject(ctx, q, 1)
		must(t, err)
		if !ok {
			t.Fatal("archive failed")
		}
	},
	"reopen_after_close": func(ctx context.Context, t *testing.T, q db.Queryer) {
		must(t, repository.CloseProject(ctx, q, 1))
		must(t, repository.ReopenProject(ctx, q, 5))
	},
	"builtin_group_overrides": func(ctx context.Context, t *testing.T, q db.Queryer) {
		nm, err := repository.BuiltinGroupID(ctx, q, domain.KindGroupNonMember)
		must(t, err)
		an, err := repository.BuiltinGroupID(ctx, q, domain.KindGroupAnonymous)
		must(t, err)
		for _, c := range []struct {
			project, principal int64
			roles              []int64
		}{{1, nm, []int64{1, 2}}, {2, an, []int64{2}}, {3, an, []int64{3}}, {4, nm, []int64{3}}} {
			_, err := repository.CreateMember(ctx, q, c.project, c.principal, c.roles)
			must(t, err)
		}
	},
	"role_settings": func(ctx context.Context, t *testing.T, q db.Queryer) {
		updateRole(ctx, t, q, 2, func(r *domain.Role) {
			r.IssuesVisibility = "own"
			setPermissionTrackers(r, "view_issues", []int64{2})
		})
		updateRole(ctx, t, q, 1, func(r *domain.Role) {
			r.UsersVisibility = "members_of_visible_projects"
			setPermissionTrackers(r, "view_issues", nil)
		})
		updateRole(ctx, t, q, 4, func(r *domain.Role) {
			r.UsersVisibility = "members_of_visible_projects"
			r.IssuesVisibility = "own"
		})
		updateRole(ctx, t, q, 5, func(r *domain.Role) {
			r.Permissions = slices.DeleteFunc(r.Permissions, func(p string) bool { return p == "view_issues" })
		})
		updateRole(ctx, t, q, 3, func(r *domain.Role) {
			r.IssuesVisibility = "all"
			setPermissionTrackers(r, "view_issues", []int64{1, 3})
		})
	},
	"group_membership_changes": func(ctx context.Context, t *testing.T, q db.Queryer) {
		must(t, repository.AddUserToGroup(ctx, q, 10, 7))
		must(t, repository.RemoveUserFromGroup(ctx, q, 11, 8))
		must(t, repository.AddUserToGroup(ctx, q, 11, 4))
		_, err := repository.CreateMember(ctx, q, 1, 11, []int64{3})
		must(t, err)
	},
	"inherit_members": func(ctx context.Context, t *testing.T, q db.Queryer) {
		updateProject(ctx, t, q, 3, func(p *domain.Project) { p.InheritMembers = true })
		updateProject(ctx, t, q, 6, func(p *domain.Project) { p.InheritMembers = true })
		_, err := repository.CreateMember(ctx, q, 1, 4, []int64{2})
		must(t, err)
		_, err = repository.CreateMember(ctx, q, 5, 7, []int64{3})
		must(t, err)
	},
	"inherit_members_off": func(ctx context.Context, t *testing.T, q db.Queryer) {
		updateProject(ctx, t, q, 3, func(p *domain.Project) { p.InheritMembers = true })
		updateProject(ctx, t, q, 3, func(p *domain.Project) { p.InheritMembers = false })
		updateProject(ctx, t, q, 4, func(p *domain.Project) { p.InheritMembers = true })
	},
	"move_project": func(ctx context.Context, t *testing.T, q db.Queryer) {
		updateProject(ctx, t, q, 4, func(p *domain.Project) { p.InheritMembers = true })
		updateProject(ctx, t, q, 4, func(p *domain.Project) { p.ParentID = ptr(2) })
		updateProject(ctx, t, q, 3, func(p *domain.Project) { p.ParentID = nil })
		updateProject(ctx, t, q, 6, func(p *domain.Project) { p.InheritMembers = true })
		updateProject(ctx, t, q, 6, func(p *domain.Project) { p.ParentID = ptr(4) })
	},
	"member_role_updates": func(ctx context.Context, t *testing.T, q db.Queryer) {
		must(t, repository.SetMemberRoles(ctx, q, 1, []int64{2, 3}))
		must(t, repository.SetMemberRoles(ctx, q, 6, []int64{2}))
		must(t, repository.DestroyMember(ctx, q, 9))
		must(t, repository.SetMemberRoles(ctx, q, 7, []int64{3}))
	},
	"public_private": func(ctx context.Context, t *testing.T, q db.Queryer) {
		updateProject(ctx, t, q, 1, func(p *domain.Project) { p.IsPublic = false })
		updateProject(ctx, t, q, 2, func(p *domain.Project) { p.IsPublic = true })
	},
	"modules": func(ctx context.Context, t *testing.T, q db.Queryer) {
		must(t, repository.SetEnabledModules(ctx, q, 1, []string{"issue_tracking"}))
		must(t, repository.EnableModule(ctx, q, 2, "wiki"))
		must(t, repository.DisableModule(ctx, q, 5, "issue_tracking"))
	},
	"create_projects": func(ctx context.Context, t *testing.T, q db.Queryer) {
		p := &domain.Project{Name: "New child", Identifier: "new-child", IsPublic: false, InheritMembers: true, ParentID: ptr(5)}
		must(t, repository.CreateProject(ctx, q, p, repository.CreateProjectOptions{EnabledModules: []string{"issue_tracking", "wiki"}}))
		p2 := &domain.Project{Name: "Another", Identifier: "another", IsPublic: true, ParentID: ptr(1)}
		must(t, repository.CreateProject(ctx, q, p2, repository.CreateProjectOptions{EnabledModules: []string{"issue_tracking"}}))
		_, err := repository.CreateMember(ctx, q, p2.ID, 7, []int64{1})
		must(t, err)
	},
	"destroy_group": func(ctx context.Context, t *testing.T, q db.Queryer) {
		must(t, repository.DestroyGroup(ctx, q, 10))
	},
	"private_issues": privateIssues,
	"private_issues_own": func(ctx context.Context, t *testing.T, q db.Queryer) {
		privateIssues(ctx, t, q)
		for _, id := range []int64{1, 2, 4} {
			updateRole(ctx, t, q, id, func(r *domain.Role) { r.IssuesVisibility = "own" })
		}
	},
}

// privateIssues は Ruby の private_issues! と同じ UPDATE を行う。
func privateIssues(ctx context.Context, t *testing.T, q db.Queryer) {
	for _, s := range []string{
		`UPDATE issues SET is_private = ?, assigned_to_id = 10 WHERE id = 1`,
		`UPDATE issues SET is_private = ?, assigned_to_id = 11 WHERE id = 4`,
		`UPDATE issues SET is_private = ? WHERE id = 2`,
		`UPDATE issues SET is_private = ?, author_id = 8 WHERE id = 6`,
	} {
		_, err := q.Exec(ctx, s, true)
		must(t, err)
	}
	_, err := q.Exec(ctx, `UPDATE issues SET assigned_to_id = 10 WHERE id = 7`)
	must(t, err)
}

func ids(xs []int64) []int64 {
	out := slices.Clone(xs)
	slices.Sort(out)
	if out == nil {
		out = []int64{}
	}
	return out
}

func roleIDs(rs []*domain.Role) []int64 {
	out := []int64{}
	for _, r := range rs {
		out = append(out, r.ID)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func selectIDs(ctx context.Context, t *testing.T, q db.Queryer, query string) []int64 {
	t.Helper()
	var out []int64
	if err := q.Select(ctx, &out, query); err != nil {
		t.Fatalf("%v\nquery: %s", err, query)
	}
	return ids(out)
}

func bits(n int, f func(i int) bool) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		if f(i) {
			b.WriteByte('1')
		} else {
			b.WriteByte('0')
		}
	}
	return b.String()
}

// diffBits は '0'/'1' 列の差分を名前付きで返す。
func diffBits(names []string, got, want string) string {
	var ds []string
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			g := byte('?')
			if i < len(got) {
				g = got[i]
			}
			ds = append(ds, fmt.Sprintf("%s(go=%c ruby=%c)", names[i], g, want[i]))
		}
	}
	return strings.Join(ds, ", ")
}

// memberRows は Ruby の dump と同じ形式のメンバー一覧を作る。
func memberRows(ctx context.Context, t *testing.T, q db.Queryer) []string {
	t.Helper()
	var rows []struct {
		ProjectID   int64  `db:"project_id"`
		PrincipalID int64  `db:"principal_id"`
		RoleID      int64  `db:"role_id"`
		SrcProject  *int64 `db:"src_project"`
		SrcUser     *int64 `db:"src_user"`
		SrcRole     *int64 `db:"src_role"`
		Inherited   *int64 `db:"inherited_from"`
	}
	must(t, q.Select(ctx, &rows, `SELECT m.project_id, m.principal_id, mr.role_id, mr.inherited_from,
  sm.project_id AS src_project, sm.principal_id AS src_user, smr.role_id AS src_role
FROM members m JOIN member_roles mr ON mr.member_id = m.id
LEFT JOIN member_roles smr ON smr.id = mr.inherited_from LEFT JOIN members sm ON sm.id = smr.member_id`))
	var out []string
	for _, r := range rows {
		var src any
		if r.Inherited != nil {
			src = []int64{*r.SrcProject, *r.SrcUser, *r.SrcRole}
		}
		b, _ := json.Marshal([]any{r.ProjectID, r.PrincipalID, r.RoleID, src})
		out = append(out, string(b))
	}
	sort.Strings(out)
	return out
}

// checkPreorder は order の各プロジェクトについて、子孫がその直後に連続して並ぶことを確認する。
func checkPreorder(ctx context.Context, q db.Queryer, order []int64) error {
	pos := map[int64]int{}
	for i, id := range order {
		pos[id] = i
	}
	for i, id := range order {
		desc, err := repository.ProjectSelfAndDescendantIDs(ctx, q, id)
		if err != nil {
			return err
		}
		for _, dd := range desc {
			if p := pos[dd]; p < i || p >= i+len(desc) {
				return fmt.Errorf("descendant %d of %d is out of its subtree range", dd, id)
			}
		}
	}
	return nil
}

func normalizeJSON(raw []json.RawMessage) []string {
	var out []string
	for _, r := range raw {
		var v any
		_ = json.Unmarshal(r, &v)
		b, _ := json.Marshal(v)
		out = append(out, string(b))
	}
	sort.Strings(out)
	return out
}

func TestDifferentialAgainstRedmine(t *testing.T) {
	var gt groundTruth
	if err := json.Unmarshal(groundTruthJSON, &gt); err != nil {
		t.Fatal(err)
	}
	actions := make([]domain.Action, len(gt.Actions))
	for i, a := range gt.Actions {
		c, act, _ := strings.Cut(a, "/")
		actions[i] = domain.ControllerAction(c, act)
	}
	names := make([]string, 0, len(gt.Scenarios))
	for n := range gt.Scenarios {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		sc := gt.Scenarios[name]
		mut, ok := mutations[name]
		if !ok {
			t.Errorf("scenario %s has no Go mutation", name)
			continue
		}
		t.Run(name, func(t *testing.T) {
			dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
				ctx := context.Background()
				testfixtures.Load(t, d, testfixtures.All()...)
				must(t, d.WithTx(ctx, func(tx *db.Tx) error {
					mut(ctx, t, tx)
					return nil
				}))
				checkScenario(ctx, t, d, &gt, sc, actions)
			})
		})
	}
}

func checkScenario(ctx context.Context, t *testing.T, d *db.DB, gt *groundTruth, sc *gtScenario, actions []domain.Action) {
	projects, err := repository.ListProjects(ctx, d)
	must(t, err)
	var tree []int64
	status := map[string]int{}
	for _, p := range projects {
		tree = append(tree, p.ID)
		status[strconv.FormatInt(p.ID, 10)] = p.Status
	}
	// 兄弟の並びは DB の照合順序と操作履歴に依存するため (docs 参照) 完全一致は求めず、
	// Go と Ruby の並びがともに Go の閉包テーブルに対する正しい前順 (部分木が連続) であることを確認する。
	if !slices.Equal(ids(tree), ids(sc.ProjectTree)) {
		t.Errorf("project set = %v, want %v", tree, sc.ProjectTree)
	}
	for _, order := range [][]int64{tree, sc.ProjectTree} {
		if err := checkPreorder(ctx, d, order); err != nil {
			t.Errorf("tree order %v: %v", order, err)
		}
	}
	if !reflect.DeepEqual(status, sc.ProjectStatus) {
		t.Errorf("project status = %v, want %v", status, sc.ProjectStatus)
	}
	if got, want := memberRows(ctx, t, d), normalizeJSON(sc.Members); !slices.Equal(got, want) {
		t.Errorf("members differ:\n go:   %v\n ruby: %v", got, want)
	}
	// Redmine ではロールの無いメンバーは残らない (remove_member_if_empty)
	var empty int
	must(t, d.Get(ctx, &empty, `SELECT COUNT(*) FROM members m WHERE NOT EXISTS (SELECT 1 FROM member_roles mr WHERE mr.member_id = m.id)`))
	if empty > 0 {
		t.Errorf("%d members without roles", empty)
	}
	byID := map[int64]*domain.Project{}
	var byIDOrder []*domain.Project
	for _, p := range projects {
		byID[p.ID] = p
	}
	for _, id := range ids(tree) {
		byIDOrder = append(byIDOrder, byID[id])
	}
	var issues []domain.Issue
	must(t, d.Select(ctx, &issues, `SELECT id, project_id AS projectid, tracker_id AS trackerid, status_id AS statusid,
  author_id AS authorid, assigned_to_id AS assignedtoid, is_private AS isprivate FROM issues ORDER BY id`))
	userIDs := make([]string, 0, len(sc.Users))
	for k := range sc.Users {
		userIDs = append(userIDs, k)
	}
	sort.Strings(userIDs)
	for _, uidStr := range userIDs {
		want := sc.Users[uidStr]
		uid, _ := strconv.ParseInt(uidStr, 10, 64)
		u, err := repository.GetUser(ctx, d, uid)
		must(t, err)
		a := authz.New(d, u)
		pfx := "user " + uidStr + ": "
		for _, p := range byIDOrder {
			pid := strconv.FormatInt(p.ID, 10)
			got := bits(len(gt.Permissions), func(i int) bool {
				ok, err := a.AllowedTo(ctx, domain.Perm(gt.Permissions[i]), p)
				must(t, err)
				return ok
			})
			if got != want.Allowed[pid] {
				t.Errorf("%sallowed_to? project %s: %s", pfx, pid, diffBits(gt.Permissions, got, want.Allowed[pid]))
			}
			got = bits(len(actions), func(i int) bool {
				ok, err := a.AllowedTo(ctx, actions[i], p)
				must(t, err)
				return ok
			})
			if got != want.Actions[pid] {
				t.Errorf("%sallowed_to?(action) project %s: %s", pfx, pid, diffBits(gt.Actions, got, want.Actions[pid]))
			}
			rs, err := a.RolesForProject(ctx, p)
			must(t, err)
			if g := roleIDs(rs); !slices.Equal(g, ids(want.RolesForProject[pid])) {
				t.Errorf("%sroles_for_project %s = %v, want %v", pfx, pid, g, want.RolesForProject[pid])
			}
			mr, err := a.ManagedRoles(ctx, p.ID)
			must(t, err)
			if g := roleIDs(mr); !slices.Equal(g, ids(want.ManagedRoles[pid])) {
				t.Errorf("%smanaged_roles %s = %v, want %v", pfx, pid, g, want.ManagedRoles[pid])
			}
			vt, err := a.AllowedToViewAllTimeEntries(ctx, p)
			must(t, err)
			if vt != want.ViewAllTimeEntries[pid] {
				t.Errorf("%sallowed_to_view_all_time_entries? %s = %v", pfx, pid, vt)
			}
		}
		got := bits(len(gt.Permissions), func(i int) bool {
			ok, err := a.AllowedToProjects(ctx, domain.Perm(gt.Permissions[i]), byIDOrder, nil)
			must(t, err)
			return ok
		})
		if got != want.AllowedAllProjects {
			t.Errorf("%sallowed_to?(all projects): %s", pfx, diffBits(gt.Permissions, got, want.AllowedAllProjects))
		}
		got = bits(len(gt.Permissions), func(i int) bool {
			ok, err := a.AllowedToGlobally(ctx, domain.Perm(gt.Permissions[i]), nil)
			must(t, err)
			return ok
		})
		if got != want.Global {
			t.Errorf("%sallowed_to?(global): %s", pfx, diffBits(gt.Permissions, got, want.Global))
		}
		for _, perm := range gt.Permissions {
			cond, err := a.AllowedToCondition(ctx, perm, authz.ConditionOptions{}, nil)
			must(t, err)
			if g := selectIDs(ctx, t, d, `SELECT projects.id FROM projects WHERE `+cond); !slices.Equal(g, ids(want.AllowedProjects[perm])) {
				t.Errorf("%sProject.allowed_to(%s) = %v, want %v\n  %s", pfx, perm, g, want.AllowedProjects[perm], cond)
			}
		}
		for perm, w := range want.AllowedProjectsMember {
			cond, err := a.AllowedToCondition(ctx, perm, authz.ConditionOptions{Member: true}, nil)
			must(t, err)
			if g := selectIDs(ctx, t, d, `SELECT projects.id FROM projects WHERE `+cond); !slices.Equal(g, ids(w)) {
				t.Errorf("%sProject.allowed_to(%s, member: true) = %v, want %v", pfx, perm, g, w)
			}
		}
		for _, c := range []struct {
			name string
			opts authz.ConditionOptions
			want []int64
		}{
			{"all", authz.ConditionOptions{}, want.VisibleIssues},
			{"project1", authz.ConditionOptions{Project: byID[1]}, want.VisibleIssuesProject1},
			{"project1+sub", authz.ConditionOptions{Project: byID[1], WithSubprojects: true}, want.VisibleIssuesProject1Sb},
		} {
			cond, err := a.IssueVisibleCondition(ctx, c.opts)
			must(t, err)
			g := selectIDs(ctx, t, d, `SELECT issues.id FROM issues JOIN projects ON projects.id = issues.project_id WHERE `+cond)
			if !slices.Equal(g, ids(c.want)) {
				t.Errorf("%sIssue.visible(%s) = %v, want %v\n  %s", pfx, c.name, g, c.want, cond)
			}
		}
		got = bits(len(issues), func(i int) bool {
			ok, err := a.IssueVisible(ctx, &issues[i], byID[issues[i].ProjectID])
			must(t, err)
			return ok
		})
		if got != want.IssueVisible {
			t.Errorf("%sIssue#visible? = %s, want %s", pfx, got, want.IssueVisible)
		}
		cond, err := a.PrincipalVisibleCondition(ctx)
		must(t, err)
		if g := selectIDs(ctx, t, d, `SELECT principals.id FROM principals WHERE `+cond); !slices.Equal(g, ids(want.VisiblePrincipals)) {
			t.Errorf("%sPrincipal.visible = %v, want %v\n  %s", pfx, g, want.VisiblePrincipals, cond)
		}
		vp, err := a.VisibleProjectIDs(ctx)
		must(t, err)
		if !slices.Equal(ids(vp), ids(want.VisibleProjectIDs)) {
			t.Errorf("%svisible_project_ids = %v, want %v", pfx, vp, want.VisibleProjectIDs)
		}
		roles, err := a.Roles(ctx)
		must(t, err)
		if g := roleIDs(roles); !slices.Equal(g, ids(want.Roles)) {
			t.Errorf("%sroles = %v, want %v", pfx, g, want.Roles)
		}
		pibr, err := a.ProjectIDsByRole(ctx)
		must(t, err)
		gotPIBR := map[string][]int64{}
		for _, rp := range pibr {
			gotPIBR[strconv.FormatInt(rp.Role.ID, 10)] = ids(rp.ProjectIDs)
		}
		wantPIBR := map[string][]int64{}
		for k, v := range want.ProjectIDsByRole {
			wantPIBR[k] = ids(v)
		}
		if !reflect.DeepEqual(gotPIBR, wantPIBR) {
			t.Errorf("%sproject_ids_by_role = %v, want %v", pfx, gotPIBR, wantPIBR)
		}
	}
}
