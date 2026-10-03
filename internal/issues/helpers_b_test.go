// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package issues

import (
	"fmt"
	"slices"
	"sync/atomic"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

// Issue.visible(user, options) の id (昇順)。where は追加条件 (issues を参照)。
func (c *tc) visibleIDs(u *domain.User, opts authz.ConditionOptions, where string, args ...any) []int64 {
	c.t.Helper()
	cond, err := authz.New(c.d, u).IssueVisibleCondition(c.ctx, opts)
	c.must(err)
	if where == "" {
		where = "1=1"
	}
	return c.ids(`SELECT issues.id FROM issues JOIN projects ON projects.id = issues.project_id WHERE (`+cond+`) AND (`+where+`) ORDER BY issues.id`, args...)
}

// assertVisibilityMatch は assert_visibility_match (全チケットの visible? と SQL スコープが一致)。
func (c *tc) assertVisibilityMatch(u *domain.User, ids []int64) {
	c.t.Helper()
	e := c.env()
	var got []int64
	for _, id := range c.ids(`SELECT id FROM issues ORDER BY id`) {
		ok, err := e.Visible(c.ctx, c.issue(id), u)
		c.must(err)
		if ok {
			got = append(got, id)
		}
	}
	if !slices.Equal(ids, got) {
		c.t.Errorf("visibility mismatch: scope %v, visible? %v", ids, got)
	}
}

func (c *tc) builtinRoleID(builtin int) int64 {
	c.t.Helper()
	r, err := repository.BuiltinRole(c.ctx, c.d, builtin)
	c.must(err)
	return r.ID
}

func (c *tc) builtinGroupID(kind domain.PrincipalKind) int64 {
	c.t.Helper()
	id, err := repository.BuiltinGroupID(c.ctx, c.d, kind)
	c.must(err)
	return id
}

var roleSeqB, projSeqB atomic.Int64

// generateRole は Role.generate!(permissions: perms)。
func (c *tc) generateRole(perms ...string) int64 {
	c.t.Helper()
	r := &domain.Role{Name: fmt.Sprintf("RoleB %d", roleSeqB.Add(1)), Assignable: true, Permissions: perms, AllRolesManaged: true}
	c.must(repository.SaveRole(c.ctx, c.d, r))
	return r.ID
}

// generateProject は Project.generate!(tracker_ids: trackers)。
func (c *tc) generateProject(trackers ...int64) int64 {
	c.t.Helper()
	n := projSeqB.Add(1)
	p := &domain.Project{Name: fmt.Sprintf("project-b%04d", n), Identifier: fmt.Sprintf("project-b%04d", n), IsPublic: true,
		Status: domain.ProjectStatusActive}
	c.must(repository.CreateProject(c.ctx, c.d, p, repository.CreateProjectOptions{
		EnabledModules: []string{"issue_tracking", "time_tracking"}, TrackerIDs: trackers}))
	return p.ID
}

func (c *tc) memberID(project, principal int64) int64 {
	c.t.Helper()
	var id int64
	c.must(c.d.Get(c.ctx, &id, `SELECT id FROM members WHERE project_id = ? AND principal_id = ?`, project, principal))
	return id
}

func (c *tc) cfIDByName(name string) int64 {
	c.t.Helper()
	var id int64
	c.must(c.d.Get(c.ctx, &id, `SELECT id FROM custom_fields WHERE name = ?`, name))
	return id
}
