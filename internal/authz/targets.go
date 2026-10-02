package authz

import (
	"context"
	"slices"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

// ModuleEnabledInVisibleProject は EnabledModule.exists?(project: Project.visible, name: module)
// (アプリケーションメニューの表示条件) の移植。
func (a *Authorizer) ModuleEnabledInVisibleProject(ctx context.Context, module string) (bool, error) {
	cond, err := a.VisibleCondition(ctx, ConditionOptions{})
	if err != nil {
		return false, err
	}
	var n int
	if err := a.q.Get(ctx, &n, `SELECT COUNT(*) FROM project_modules em JOIN projects ON projects.id = em.project_id
WHERE em.name = ? AND `+cond, module); err != nil {
		return false, err
	}
	return n > 0, nil
}

// AllowedTargetTrackerIDs は Issue.allowed_target_trackers(project, user, current_tracker) の移植。
// プロジェクトのトラッカー (position 順) のうち、add_issues を持つロールで許可されたものを返す。
// currentTrackerID が 0 以外なら (トラッカー制限があっても) 候補に含める。
func (a *Authorizer) AllowedTargetTrackerIDs(ctx context.Context, p *domain.Project, currentTrackerID int64) ([]int64, error) {
	if p == nil {
		return nil, nil
	}
	ids, err := repository.ProjectTrackerIDs(ctx, a.q, p.ID)
	if err != nil {
		return nil, err
	}
	if a.user.IsAdmin() {
		return ids, nil
	}
	roles, err := a.RolesForProject(ctx, p)
	if err != nil {
		return nil, err
	}
	var allowed []int64
	for _, r := range roles {
		if !r.HasPermission("add_issues") {
			continue
		}
		if r.PermissionsAllTrackers("add_issues") {
			return ids, nil
		}
		allowed = append(allowed, r.PermissionsTrackerIDs("add_issues")...)
	}
	if currentTrackerID != 0 {
		allowed = append(allowed, currentTrackerID)
	}
	return slices.DeleteFunc(ids, func(id int64) bool { return !slices.Contains(allowed, id) }), nil
}
