// Package authz は Redmine の権限判定 (User#allowed_to?, User#roles_for_project,
// Project.allowed_to_condition, 各種 visible スコープ) を移植する。
//
// Authorizer は 1 ユーザ・1 リクエスト用のオブジェクトで、Redmine がインスタンス変数で
// メモ化している値 (メンバーシップ, project_ids_by_role 等) をキャッシュする。
// 並行利用は想定しない。データを変更した後は新しい Authorizer を作ること。
package authz

import (
	"context"
	"slices"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

// RoleFilter は allowed_to? のブロックに相当する、ロール単位の追加条件。
type RoleFilter func(role *domain.Role, user *domain.User) bool

// Authorizer は User を主体とした権限判定器。
type Authorizer struct {
	q    db.Queryer
	user *domain.User

	builtinRole       *domain.Role
	builtinGroupID    int64
	rolesByID         map[int64]*domain.Role
	memberships       map[int64]*domain.Member // project_id -> membership (nil = 非メンバー)
	overrideMembers   map[int64]map[domain.PrincipalKind]*domain.Member
	roles             []*domain.Role
	rolesLoaded       bool
	projectIDs        []int64
	projectIDsLoaded  bool
	projectIDsByRole  []RoleProjectIDs
	pibrLoaded        bool
	groupIDs          []int64
	groupIDsLoaded    bool
	visibleProjectIDs []int64
	vpLoaded          bool
}

// New は user を主体とする Authorizer を返す。
func New(q db.Queryer, user *domain.User) *Authorizer {
	return &Authorizer{
		q: q, user: user,
		rolesByID:       map[int64]*domain.Role{},
		memberships:     map[int64]*domain.Member{},
		overrideMembers: map[int64]map[domain.PrincipalKind]*domain.Member{},
	}
}

// User は主体のユーザを返す。
func (a *Authorizer) User() *domain.User { return a.user }

// Queryer は判定に使う DB ハンドルを返す。
func (a *Authorizer) Queryer() db.Queryer { return a.q }

// Dialect は DB の dialect を返す (SQL 断片を組み立てる呼び出し側向け)。
func (a *Authorizer) Dialect() db.Dialect { return a.q.Dialect() }

// BuiltinRole は User#builtin_role (匿名なら Anonymous ロール、それ以外は Non member ロール)。
func (a *Authorizer) BuiltinRole(ctx context.Context) (*domain.Role, error) {
	if a.builtinRole == nil {
		r, err := repository.BuiltinRole(ctx, a.q, a.user.BuiltinRoleBuiltin())
		if err != nil {
			return nil, err
		}
		a.builtinRole = r
		a.rolesByID[r.ID] = r
	}
	return a.builtinRole, nil
}

// builtinGroup は主体に対応する組込グループ (GroupAnonymous / GroupNonMember) の id。
func (a *Authorizer) builtinGroup(ctx context.Context) (int64, error) {
	if a.builtinGroupID == 0 {
		id, err := repository.BuiltinGroupID(ctx, a.q, a.user.BuiltinGroupKind())
		if err != nil {
			return 0, err
		}
		a.builtinGroupID = id
	}
	return a.builtinGroupID, nil
}

// rolesFor は id のロールをキャッシュ経由で返す (入力順を保つ)。
func (a *Authorizer) rolesFor(ctx context.Context, ids []int64) ([]*domain.Role, error) {
	var missing []int64
	for _, id := range ids {
		if _, ok := a.rolesByID[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		rs, err := repository.RolesByIDs(ctx, a.q, missing)
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			a.rolesByID[r.ID] = r
		}
	}
	out := make([]*domain.Role, 0, len(ids))
	for _, id := range ids {
		if r := a.rolesByID[id]; r != nil {
			out = append(out, r)
		}
	}
	return out, nil
}

// Membership は User#membership(project): アーカイブされていないプロジェクトのメンバーシップ。
// 匿名ユーザは常に nil。
func (a *Authorizer) Membership(ctx context.Context, projectID int64) (*domain.Member, error) {
	if a.user.Anonymous() || a.user.ID == 0 {
		return nil, nil
	}
	if m, ok := a.memberships[projectID]; ok {
		return m, nil
	}
	m, err := repository.Membership(ctx, a.q, a.user.ID, projectID)
	if err != nil {
		return nil, err
	}
	a.memberships[projectID] = m
	return m, nil
}

// OverrideRoles は Project#override_roles(role): 公開プロジェクトに組込グループ
// (GroupAnonymous / GroupNonMember) のメンバーシップがあればそのロール、無ければ [role]。
func (a *Authorizer) OverrideRoles(ctx context.Context, p *domain.Project, role *domain.Role) ([]*domain.Role, error) {
	ms, ok := a.overrideMembers[p.ID]
	if !ok {
		var err error
		ms, err = repository.BuiltinGroupMemberships(ctx, a.q, p.ID)
		if err != nil {
			return nil, err
		}
		a.overrideMembers[p.ID] = ms
	}
	kind := domain.KindGroupNonMember
	if role.IsAnonymous() {
		kind = domain.KindGroupAnonymous
	}
	if m := ms[kind]; m != nil {
		return a.rolesFor(ctx, m.RoleIDs())
	}
	return []*domain.Role{role}, nil
}

// RolesForProject は User#roles_for_project(project)。
func (a *Authorizer) RolesForProject(ctx context.Context, p *domain.Project) ([]*domain.Role, error) {
	if p == nil || p.Archived() {
		return nil, nil
	}
	m, err := a.Membership(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	if m != nil {
		return a.rolesFor(ctx, m.RoleIDs())
	}
	if p.IsPublic {
		br, err := a.BuiltinRole(ctx)
		if err != nil {
			return nil, err
		}
		return a.OverrideRoles(ctx, p, br)
	}
	return nil, nil
}

// AllowedTo は User#allowed_to?(action, project)。
func (a *Authorizer) AllowedTo(ctx context.Context, action domain.Action, p *domain.Project) (bool, error) {
	return a.AllowedToWith(ctx, action, p, nil)
}

// AllowedToWith はブロック付きの User#allowed_to?(action, project) { |role, user| ... }。
// p が nil の場合は (global なしの呼び出しと同じく) false。
func (a *Authorizer) AllowedToWith(ctx context.Context, action domain.Action, p *domain.Project, filter RoleFilter) (bool, error) {
	if p == nil {
		return false, nil
	}
	if !p.AllowsTo(action) {
		return false, nil
	}
	if a.user.IsAdmin() {
		return true, nil
	}
	roles, err := a.RolesForProject(ctx, p)
	if err != nil {
		return false, err
	}
	for _, r := range roles {
		if (p.IsPublic || r.IsMember()) && r.AllowedTo(action, a.user.OAuthScope) && (filter == nil || filter(r, a.user)) {
			return true, nil
		}
	}
	return false, nil
}

// AllowedToProjects は配列コンテキストの User#allowed_to?: 全プロジェクトで許可されていれば true。
// 空なら false。
func (a *Authorizer) AllowedToProjects(ctx context.Context, action domain.Action, ps []*domain.Project, filter RoleFilter) (bool, error) {
	if len(ps) == 0 {
		return false, nil
	}
	ok := true
	for _, p := range ps {
		r, err := a.AllowedToWith(ctx, action, p, filter)
		if err != nil {
			return false, err
		}
		// Redmine は reduce(:&) で全要素を評価する
		ok = ok && r
	}
	return ok, nil
}

// AllowedToGlobally は User#allowed_to?(action, nil, global: true) / #allowed_to_globally?。
func (a *Authorizer) AllowedToGlobally(ctx context.Context, action domain.Action, filter RoleFilter) (bool, error) {
	if a.user.IsAdmin() {
		return true, nil
	}
	roles, err := a.Roles(ctx)
	if err != nil {
		return false, err
	}
	br, err := a.BuiltinRole(ctx)
	if err != nil {
		return false, err
	}
	all := slices.Clone(roles)
	if !slices.ContainsFunc(all, func(r *domain.Role) bool { return r.ID == br.ID }) {
		all = append(all, br)
	}
	for _, r := range all {
		if r.AllowedTo(action, a.user.OAuthScope) && (filter == nil || filter(r, a.user)) {
			return true, nil
		}
	}
	return false, nil
}

// AllowedToViewAllTimeEntries は User#allowed_to_view_all_time_entries?(project)。
func (a *Authorizer) AllowedToViewAllTimeEntries(ctx context.Context, p *domain.Project) (bool, error) {
	return a.AllowedToWith(ctx, domain.Perm("view_time_entries"), p, func(r *domain.Role, _ *domain.User) bool {
		return r.TimeEntriesVisibility == domain.TimeEntriesVisibilityAll
	})
}

// Roles は User#roles: アーカイブされていないプロジェクトでのメンバーシップの全ロール。
// 無ければ公開プロジェクトでの組込グループのロール。
func (a *Authorizer) Roles(ctx context.Context) ([]*domain.Role, error) {
	if a.rolesLoaded {
		return a.roles, nil
	}
	var ids []int64
	if a.user.ID != 0 {
		if err := a.q.Select(ctx, &ids, `SELECT DISTINCT mr.role_id FROM member_roles mr
JOIN members m ON m.id = mr.member_id JOIN projects ON projects.id = m.project_id
WHERE projects.status <> ? AND m.principal_id = ?`, domain.ProjectStatusArchived, a.user.ID); err != nil {
			return nil, err
		}
	}
	if len(ids) == 0 {
		gid, err := a.builtinGroup(ctx)
		if err != nil {
			return nil, err
		}
		if err := a.q.Select(ctx, &ids, `SELECT DISTINCT mr.role_id FROM member_roles mr
JOIN members m ON m.id = mr.member_id JOIN projects ON projects.id = m.project_id
WHERE projects.status <> ? AND projects.is_public = ? AND m.principal_id = ?`, domain.ProjectStatusArchived, true, gid); err != nil {
			return nil, err
		}
	}
	rs, err := a.rolesFor(ctx, ids)
	if err != nil {
		return nil, err
	}
	domain.SortRoles(rs)
	a.roles, a.rolesLoaded = rs, true
	return rs, nil
}

// ProjectIDs は Principal#project_ids (アーカイブされていないメンバーシップのプロジェクト)。
func (a *Authorizer) ProjectIDs(ctx context.Context) ([]int64, error) {
	if !a.projectIDsLoaded {
		if a.user.ID != 0 {
			ids, err := repository.MemberProjectIDs(ctx, a.q, a.user.ID)
			if err != nil {
				return nil, err
			}
			a.projectIDs = ids
		}
		a.projectIDsLoaded = true
	}
	return a.projectIDs, nil
}

// RoleProjectIDs はロールとそのロールを持つプロジェクト id の組。
type RoleProjectIDs struct {
	Role       *domain.Role
	ProjectIDs []int64
}

// ProjectIDsByRole は User#project_ids_by_role の移植。
// 自分がメンバーのプロジェクトと、組込グループにロールが与えられた公開プロジェクトを
// ロール別に返す (自分がメンバーのプロジェクトでは組込グループのロールを無視する)。
// 並びは Role.sorted の順。
func (a *Authorizer) ProjectIDsByRole(ctx context.Context) ([]RoleProjectIDs, error) {
	if a.pibrLoaded {
		return a.projectIDsByRole, nil
	}
	gid, err := a.builtinGroup(ctx)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		PrincipalID int64 `db:"principal_id"`
		RoleID      int64 `db:"role_id"`
		ProjectID   int64 `db:"project_id"`
	}
	if err := a.q.Select(ctx, &rows, `SELECT m.principal_id, mr.role_id, m.project_id FROM members m
JOIN projects ON projects.id = m.project_id JOIN member_roles mr ON mr.member_id = m.id
WHERE projects.status <> 9 AND (m.principal_id = ? OR (projects.is_public = ? AND m.principal_id = ?))
ORDER BY m.id, mr.id`, a.user.ID, true, gid); err != nil {
		return nil, err
	}
	myProjects, err := a.ProjectIDs(ctx)
	if err != nil {
		return nil, err
	}
	byRole := map[int64][]int64{}
	var roleIDs []int64
	for _, r := range rows {
		if r.PrincipalID != a.user.ID && slices.Contains(myProjects, r.ProjectID) {
			continue
		}
		if _, ok := byRole[r.RoleID]; !ok {
			roleIDs = append(roleIDs, r.RoleID)
		}
		if !slices.Contains(byRole[r.RoleID], r.ProjectID) {
			byRole[r.RoleID] = append(byRole[r.RoleID], r.ProjectID)
		}
	}
	roles, err := a.rolesFor(ctx, roleIDs)
	if err != nil {
		return nil, err
	}
	domain.SortRoles(roles)
	out := make([]RoleProjectIDs, 0, len(roles))
	for _, r := range roles {
		out = append(out, RoleProjectIDs{Role: r, ProjectIDs: byRole[r.ID]})
	}
	a.projectIDsByRole, a.pibrLoaded = out, true
	return out, nil
}

// GroupIDs は user.groups の id。
func (a *Authorizer) GroupIDs(ctx context.Context) ([]int64, error) {
	if !a.groupIDsLoaded {
		if a.user.ID != 0 {
			ids, err := repository.UserGroupIDs(ctx, a.q, a.user.ID)
			if err != nil {
				return nil, err
			}
			a.groupIDs = ids
		}
		a.groupIDsLoaded = true
	}
	return a.groupIDs, nil
}

// IsOrBelongsTo は User#is_or_belongs_to?(principal): 自身か、所属するグループなら true。
func (a *Authorizer) IsOrBelongsTo(ctx context.Context, principalID int64) (bool, error) {
	if principalID == 0 {
		return false, nil
	}
	if principalID == a.user.ID {
		return true, nil
	}
	gids, err := a.GroupIDs(ctx)
	if err != nil {
		return false, err
	}
	return slices.Contains(gids, principalID), nil
}

// VisibleProjectIDs は User#visible_project_ids (Project.visible(user) の id)。
func (a *Authorizer) VisibleProjectIDs(ctx context.Context) ([]int64, error) {
	if a.vpLoaded {
		return a.visibleProjectIDs, nil
	}
	cond, err := a.VisibleCondition(ctx, ConditionOptions{})
	if err != nil {
		return nil, err
	}
	var ids []int64
	if err := a.q.Select(ctx, &ids, `SELECT projects.id FROM projects WHERE `+cond+` ORDER BY projects.id`); err != nil {
		return nil, err
	}
	a.visibleProjectIDs, a.vpLoaded = ids, true
	return ids, nil
}

// ProjectVisible は Project#visible?(user) (= allowed_to?(:view_project, project))。
func (a *Authorizer) ProjectVisible(ctx context.Context, p *domain.Project) (bool, error) {
	return a.AllowedTo(ctx, domain.Perm("view_project"), p)
}

// ManagedRoles は User#managed_roles(project): 管理者は付与可能な全ロール、
// それ以外はメンバーシップの Member#managed_roles。
func (a *Authorizer) ManagedRoles(ctx context.Context, projectID int64) ([]*domain.Role, error) {
	if a.user.IsAdmin() {
		return repository.GivableRoles(ctx, a.q)
	}
	m, err := a.Membership(ctx, projectID)
	if err != nil || m == nil {
		return nil, err
	}
	return MemberManagedRoles(ctx, a.q, m)
}

// MemberManagedRoles は Member#managed_roles の移植。
func MemberManagedRoles(ctx context.Context, q db.Queryer, m *domain.Member) ([]*domain.Role, error) {
	pr, err := repository.GetPrincipal(ctx, q, m.PrincipalID)
	if err != nil {
		return nil, err
	}
	if pr.Kind == domain.KindUser {
		u, err := repository.GetUser(ctx, q, m.PrincipalID)
		if err != nil {
			return nil, err
		}
		if u.IsAdmin() {
			return repository.GivableRoles(ctx, q)
		}
	}
	roles, err := repository.RolesByIDs(ctx, q, m.RoleIDs())
	if err != nil {
		return nil, err
	}
	var mgmt []*domain.Role
	for _, r := range roles {
		if r.HasPermission("manage_members") {
			mgmt = append(mgmt, r)
		}
	}
	if len(mgmt) == 0 {
		return nil, nil
	}
	for _, r := range mgmt {
		if r.AllRolesManaged {
			return repository.GivableRoles(ctx, q)
		}
	}
	var ids []int64
	for _, r := range mgmt {
		for _, id := range r.ManagedRoleIDs {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	return repository.RolesByIDs(ctx, q, ids)
}
