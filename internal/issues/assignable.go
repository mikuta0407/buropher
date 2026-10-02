package issues

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

// PrincipalRef はユーザまたはグループ (担当者候補など)。
type PrincipalRef struct {
	domain.Principal
	Login string
}

// IsUser はユーザ (User / AnonymousUser) か。
func (p *PrincipalRef) IsUser() bool { return p.Kind.IsUser() }

// DisplayName は to_s (ユーザは Setting.user_format の書式、グループは名前)。
func (p *PrincipalRef) DisplayName(format string) string {
	if p.Kind.IsUser() {
		u := &domain.User{Principal: p.Principal, Login: p.Login}
		return u.Name(format)
	}
	return p.Name
}

func (e *Env) userFormat() string {
	if e.Settings == nil {
		return "firstname_lastname"
	}
	f := e.Settings.String("user_format")
	if f == "" {
		return "firstname_lastname"
	}
	return f
}

// principalRefs は id のプリンシパルを読み込む (id 順)。
func (e *Env) principalRefs(ctx context.Context, where string, args ...any) ([]*PrincipalRef, error) {
	var rows []struct {
		ID        int64   `db:"id"`
		Kind      string  `db:"kind"`
		Status    int     `db:"status"`
		Firstname string  `db:"firstname"`
		Lastname  string  `db:"lastname"`
		Name      string  `db:"name"`
		Login     *string `db:"login"`
	}
	if err := e.Q.Select(ctx, &rows, `SELECT p.id, p.kind, p.status, p.firstname, p.lastname, p.name, ua.login
FROM principals p LEFT JOIN user_accounts ua ON ua.principal_id = p.id WHERE `+where+` ORDER BY p.id`, args...); err != nil {
		return nil, err
	}
	out := make([]*PrincipalRef, len(rows))
	for i, r := range rows {
		out[i] = &PrincipalRef{Principal: domain.Principal{ID: r.ID, Kind: domain.PrincipalKind(r.Kind), Status: r.Status,
			Firstname: r.Firstname, Lastname: r.Lastname, Name: r.Name}}
		if r.Login != nil {
			out[i].Login = *r.Login
		}
	}
	return out, nil
}

// SortPrincipals は Principal#<=> の順 (ユーザが先、同種は表示名の大文字小文字無視比較) に並べる。
func (e *Env) SortPrincipals(ps []*PrincipalRef) {
	f := e.userFormat()
	slices.SortStableFunc(ps, func(a, b *PrincipalRef) int {
		ca, cb := principalClass(a), principalClass(b)
		if ca == cb {
			return casecmp(a.DisplayName(f), b.DisplayName(f))
		}
		// Ruby: principal.class.name <=> self.class.name (グループはユーザの後)
		return strings.Compare(cb, ca)
	})
}

func principalClass(p *PrincipalRef) string {
	return p.Kind.RedmineType()
}

// casecmp は String#casecmp (ASCII のみ大文字小文字を無視)。
func casecmp(a, b string) int {
	lower := func(s string) string {
		bs := []byte(s)
		for i, c := range bs {
			if c >= 'A' && c <= 'Z' {
				bs[i] = c + 32
			}
		}
		return string(bs)
	}
	return strings.Compare(lower(a), lower(b))
}

func containsPrincipal(ps []*PrincipalRef, id int64) bool {
	return slices.ContainsFunc(ps, func(p *PrincipalRef) bool { return p.ID == id })
}

// ProjectAssignableUsers は Project#assignable_users(tracker)。
func (e *Env) ProjectAssignableUsers(ctx context.Context, p *domain.Project, tracker *domain.Tracker) ([]*PrincipalRef, error) {
	kinds := "'user'"
	if e.Settings != nil && e.Settings.Bool("issue_group_assignment") {
		kinds = "'user', 'group'"
	}
	where := `p.status = 1 AND p.kind IN (` + kinds + `) AND p.id IN (SELECT m.principal_id FROM members m
JOIN member_roles mr ON mr.member_id = m.id JOIN roles r ON r.id = mr.role_id
WHERE m.project_id = ? AND r.assignable = ?`
	args := []any{p.ID, true}
	if tracker != nil {
		roles, err := repository.ListRoles(ctx, e.Q)
		if err != nil {
			return nil, err
		}
		var ids []string
		for _, r := range roles {
			if r.Assignable && r.PermissionsTracker("view_issues", tracker.ID) {
				ids = append(ids, strconv.FormatInt(r.ID, 10))
			}
		}
		if len(ids) == 0 {
			where += " AND 1=0"
		} else {
			where += " AND r.id IN (" + strings.Join(ids, ",") + ")"
		}
	}
	where += ")"
	ps, err := e.principalRefs(ctx, where, args...)
	if err != nil {
		return nil, err
	}
	e.SortPrincipals(ps)
	return ps, nil
}

// AssignableUsers は Issue#assignable_users (プロジェクトの担当可能ユーザ + 有効な作成者 + 変更前の担当者)。
func (e *Env) AssignableUsers(ctx context.Context, iss *Issue) ([]*PrincipalRef, error) {
	p, err := e.ProjectOf(ctx, iss)
	if err != nil || p == nil {
		return nil, err
	}
	t, err := e.TrackerOf(ctx, iss)
	if err != nil {
		return nil, err
	}
	users, err := e.ProjectAssignableUsers(ctx, p, t)
	if err != nil {
		return nil, err
	}
	if iss.AuthorID != 0 {
		as, err := e.principalRefs(ctx, `p.id = ? AND p.status = 1`, iss.AuthorID)
		if err != nil {
			return nil, err
		}
		users = append(users, as...)
	}
	if iss.orig != nil && iss.orig.AssignedToID != nil {
		as, err := e.principalRefs(ctx, `p.id = ?`, *iss.orig.AssignedToID)
		if err != nil {
			return nil, err
		}
		users = append(users, as...)
	}
	var out []*PrincipalRef
	for _, u := range users {
		if !containsPrincipal(out, u.ID) {
			out = append(out, u)
		}
	}
	e.SortPrincipals(out)
	return out, nil
}

// sharedVersionIDs は project.shared_versions の id。
func (e *Env) sharedVersionIDs(ctx context.Context, p *domain.Project) ([]int64, error) {
	return repository.SharedVersionIDs(ctx, e.Q, p)
}

// SharedVersions は project.shared_versions (status が空でなければその状態のもの)。
func (e *Env) SharedVersions(ctx context.Context, p *domain.Project, status string) ([]*domain.Version, error) {
	where := repository.SharedVersionsCondition(p)
	var args []any
	if status != "" {
		where += " AND versions.status = ?"
		args = append(args, status)
	}
	var rows []versionRow
	if err := e.Q.Select(ctx, &rows, `SELECT `+versionCols+` FROM versions JOIN projects ON projects.id = versions.project_id WHERE `+where+` ORDER BY versions.id`, args...); err != nil {
		return nil, err
	}
	out := make([]*domain.Version, len(rows))
	for i := range rows {
		out[i] = rows[i].version()
	}
	return out, nil
}

// AssignableVersions は assignable_versions (共有バージョンのうち open なもの + 現在のバージョン)。
func (e *Env) AssignableVersions(ctx context.Context, iss *Issue) ([]*domain.Version, error) {
	p, err := e.ProjectOf(ctx, iss)
	if err != nil || p == nil {
		return nil, err
	}
	versions, err := e.SharedVersions(ctx, p, domain.VersionStatusOpen)
	if err != nil {
		return nil, err
	}
	fv, err := e.Version(ctx, iss.FixedVersionID)
	if err != nil {
		return nil, err
	}
	if fv != nil {
		switch {
		case iss.AttrChanged("fixed_version_id"):
		case iss.AttrChanged("project_id"):
			ids, err := e.sharedVersionIDs(ctx, p)
			if err != nil {
				return nil, err
			}
			if containsID(ids, fv.ID) {
				versions = append(versions, fv)
			}
		default:
			versions = append(versions, fv)
		}
	}
	var out []*domain.Version
	for _, v := range versions {
		if !slices.ContainsFunc(out, func(o *domain.Version) bool { return o.ID == v.ID }) {
			out = append(out, v)
		}
	}
	slices.SortStableFunc(out, domain.CompareVersions)
	return out, nil
}

// AllowedTargetTrackers は allowed_target_trackers(user) (現在のトラッカーは常に含む)。
func (e *Env) AllowedTargetTrackers(ctx context.Context, iss *Issue, u *domain.User) ([]*domain.Tracker, error) {
	var was int64
	if iss.orig != nil {
		was = iss.orig.TrackerID
	}
	p, err := e.ProjectOf(ctx, iss)
	if err != nil {
		return nil, err
	}
	return e.allowedTargetTrackersFor(ctx, p, u, was)
}

// AllowedTargetTrackersFor は Issue.allowed_target_trackers(project, user, current_tracker)。
func (e *Env) allowedTargetTrackersFor(ctx context.Context, p *domain.Project, u *domain.User, current int64) ([]*domain.Tracker, error) {
	if p == nil {
		return nil, nil
	}
	if u == nil {
		var err error
		if u, err = e.currentUser(ctx); err != nil {
			return nil, err
		}
	}
	ids, err := e.authz(u).AllowedTargetTrackerIDs(ctx, p, current)
	if err != nil {
		return nil, err
	}
	ts, err := e.ProjectTrackers(ctx, p)
	if err != nil {
		return nil, err
	}
	var out []*domain.Tracker
	for _, t := range ts {
		if containsID(ids, t.ID) {
			out = append(out, t)
		}
	}
	return out, nil
}

// AllowedTargetProjects は allowed_target_projects(user, scope) (scope は cross_project_subtasks と同じ値、"*" で制限なし)。
func (e *Env) AllowedTargetProjects(ctx context.Context, iss *Issue, u *domain.User, scope string) ([]*domain.Project, error) {
	var current *domain.Project
	if iss.Persisted() {
		p, err := e.ProjectOf(ctx, iss)
		if err != nil {
			return nil, err
		}
		current = p
	}
	scopeCond := ""
	if scope != "*" {
		p, err := e.ProjectOf(ctx, iss)
		if err != nil {
			return nil, err
		}
		scopeCond = filterProjectsScope(p, scope)
	}
	return e.allowedTargetProjects(ctx, u, current, scopeCond)
}

// AllowedTargetProjectsForSubtask は allowed_target_projects_for_subtask(user)。
func (e *Env) AllowedTargetProjectsForSubtask(ctx context.Context, iss *Issue, u *domain.User) ([]*domain.Project, error) {
	scopeCond := ""
	if iss.ParentIssueID() != "" {
		p, err := e.ProjectOf(ctx, iss)
		if err != nil {
			return nil, err
		}
		cps := "tree"
		if e.Settings != nil {
			cps = e.Settings.String("cross_project_subtasks")
		}
		scopeCond = filterProjectsScope(p, cps)
	}
	p, err := e.ProjectOf(ctx, iss)
	if err != nil {
		return nil, err
	}
	return e.allowedTargetProjects(ctx, u, p, scopeCond)
}

// filterProjectsScope は filter_projects_scope(scope) の SQL 条件 ("" は制限なし)。
func filterProjectsScope(p *domain.Project, scope string) string {
	if p == nil {
		return ""
	}
	id := strconv.FormatInt(p.ID, 10)
	switch scope {
	case "system":
		return ""
	case "tree":
		root := "(SELECT rc.ancestor_id FROM project_closure rc WHERE rc.descendant_id = " + id + " ORDER BY rc.depth DESC LIMIT 1)"
		return "projects.id IN (SELECT tc.descendant_id FROM project_closure tc WHERE tc.ancestor_id = " + root + ")"
	case "hierarchy":
		return "(projects.id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = " + id + ")" +
			" OR projects.id IN (SELECT ancestor_id FROM project_closure WHERE descendant_id = " + id + "))"
	case "descendants":
		return "projects.id IN (SELECT descendant_id FROM project_closure WHERE ancestor_id = " + id + ")"
	case "":
		return "projects.id = " + id
	}
	return ""
}

// allowedTargetProjects は Issue.allowed_target_projects(user, current_project, scope)。
func (e *Env) allowedTargetProjects(ctx context.Context, u *domain.User, current *domain.Project, scopeCond string) ([]*domain.Project, error) {
	if u == nil {
		var err error
		if u, err = e.currentUser(ctx); err != nil {
			return nil, err
		}
	}
	cond, err := e.authz(u).AllowedToCondition(ctx, "add_issues", authz.ConditionOptions{}, nil)
	if err != nil {
		return nil, err
	}
	if current != nil {
		cond = "(" + cond + ") OR projects.id = " + strconv.FormatInt(current.ID, 10)
	}
	where := "(" + cond + ") AND projects.id IN (SELECT DISTINCT project_id FROM project_trackers)"
	if scopeCond != "" {
		where = "(" + scopeCond + ") AND " + where
	}
	return repository.LoadProjects(ctx, e.Q, where)
}

// projectAllowedAsTarget は allowed_target_projects(user).where(id: pid).exists?。
func (e *Env) projectAllowedAsTarget(ctx context.Context, iss *Issue, u *domain.User, pid int64) (bool, error) {
	ps, err := e.AllowedTargetProjects(ctx, iss, u, "*")
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(ps, func(p *domain.Project) bool { return p.ID == pid }), nil
}

// ValidParentProject は valid_parent_project?(issue) (Setting.cross_project_subtasks)。
func (e *Env) ValidParentProject(ctx context.Context, iss *Issue, parent *Issue) (bool, error) {
	if parent == nil || parent.ProjectID == iss.ProjectID {
		return true, nil
	}
	mode := "tree"
	if e.Settings != nil {
		mode = e.Settings.String("cross_project_subtasks")
	}
	pp, cp := parent.ProjectID, iss.ProjectID
	switch mode {
	case "system":
		return true, nil
	case "tree":
		r1, err := e.projectRootID(ctx, pp)
		if err != nil {
			return false, err
		}
		r2, err := e.projectRootID(ctx, cp)
		return r1 == r2, err
	case "hierarchy":
		a, err := e.projectIsOrIsAncestorOf(ctx, pp, cp)
		if err != nil || a {
			return a, err
		}
		return e.projectIsOrIsAncestorOf(ctx, cp, pp)
	case "descendants":
		return e.projectIsOrIsAncestorOf(ctx, pp, cp)
	}
	return false, nil
}

func (e *Env) projectRootID(ctx context.Context, id int64) (int64, error) {
	var root int64
	err := e.Q.Get(ctx, &root, `SELECT ancestor_id FROM project_closure WHERE descendant_id = ? ORDER BY depth DESC LIMIT 1`, id)
	return root, err
}

func (e *Env) projectIsOrIsAncestorOf(ctx context.Context, anc, desc int64) (bool, error) {
	var n int
	err := e.Q.Get(ctx, &n, `SELECT COUNT(*) FROM project_closure WHERE ancestor_id = ? AND descendant_id = ?`, anc, desc)
	return n > 0, err
}

// inIDs は id の IN 句 (空なら "NULL")。
func inIDs(ids []int64) string {
	if len(ids) == 0 {
		return "NULL"
	}
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(s, ",")
}

var _ = db.In
