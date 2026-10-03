// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package helper

import (
	"slices"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

// jumpBox は render_project_jump_box が使う一覧（projects_for_jump_box と
// Redmine::ProjectJumpBox#bookmarked_projects / recently_used_projects）を読み込む。
// 匿名ユーザー・DB 未設定・エラー時は空。
func (p *Page) jumpBox() *JumpBox {
	jb := &JumpBox{}
	a := p.authorizer()
	if !p.logged() || p.DB == nil || a == nil {
		return jb
	}
	j, err := LoadJumpBox(p, a)
	if err != nil {
		p.logError("jump box", err)
		return jb
	}
	return j
}

// LoadJumpBox はジャンプボックスの一覧を読み込む（projects#autocomplete からも使う）。
func LoadJumpBox(p *Page, a *authz.Authorizer) (*JumpBox, error) {
	ctx, q, uid := p.ctx(), p.DB, p.User.ID
	jb := &JumpBox{}
	ns, err := repository.ProjectNestedSet(ctx, q)
	if err != nil {
		return nil, err
	}
	jp := func(pr *domain.Project) JumpProject {
		v := ns[pr.ID]
		return JumpProject{ID: pr.ID, Name: pr.Name, Identifier: pr.Identifier, Lft: v.Lft, Rgt: v.Rgt}
	}
	// projects_for_jump_box: user.projects.active
	mine, err := repository.UserProjects(ctx, q, uid)
	if err != nil {
		return nil, err
	}
	for _, pr := range mine {
		jb.Projects = append(jb.Projects, jp(pr))
	}
	visible, err := a.VisibleCondition(ctx, authz.ConditionOptions{})
	if err != nil {
		return nil, err
	}
	// bookmarked_projects: Project.where(id: bookmarked_project_ids).visible（表示時に project_tree で並べる）
	bookmarks, err := repository.BookmarkedProjectIDs(ctx, q, uid)
	if err != nil {
		return nil, err
	}
	bps, err := visibleProjects(p, q, bookmarks, visible)
	if err != nil {
		return nil, err
	}
	for _, pr := range bps {
		jb.Bookmarked = append(jb.Bookmarked, jp(pr))
	}
	// recently_used_projects: 保存順、件数は pref.recently_used_projects
	recents, err := repository.RecentProjectIDs(ctx, q, uid)
	if err != nil {
		return nil, err
	}
	if n := p.pref().RecentlyUsedProjects; len(recents) > n {
		recents = recents[:max(n, 0)]
	}
	rps, err := visibleProjects(p, q, recents, visible)
	if err != nil {
		return nil, err
	}
	byID := map[int64]*domain.Project{}
	for _, pr := range rps {
		byID[pr.ID] = pr
	}
	for _, id := range recents {
		if pr := byID[id]; pr != nil {
			jb.Recents = append(jb.Recents, jp(pr))
		}
	}
	return jb, nil
}

// visibleProjects は Project.where(id: ids).visible。
func visibleProjects(p *Page, q db.Queryer, ids []int64, visible string) ([]*domain.Project, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	query, args, err := db.In(`projects.id IN (?) AND `+visible, slices.Clone(ids))
	if err != nil {
		return nil, err
	}
	return repository.LoadProjects(p.ctx(), q, query, args...)
}
