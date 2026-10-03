// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package issues

import (
	"fmt"
	"sync/atomic"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

var genSeqC atomic.Int64

// copyFromC は Issue.new.copy_from(src, opts)。
func (c *tc) copyFromC(e *Env, src *Issue, opts CopyOptions) *Issue {
	c.t.Helper()
	iss, err := e.NewBlank(c.ctx)
	c.must(err)
	c.must(e.CopyFrom(c.ctx, iss, src, opts))
	return iss
}

// copyC は issue.copy(attrs, opts)。
func (c *tc) copyC(e *Env, src *Issue, attrs Params, opts CopyOptions) *Issue {
	c.t.Helper()
	iss, err := e.Copy(c.ctx, src, attrs, opts)
	c.must(err)
	return iss
}

// generateProjectC は Project.generate! (trackers が nil なら全トラッカー)。
func (c *tc) generateProjectC(trackers []int64) *domain.Project {
	c.t.Helper()
	n := genSeqC.Add(1)
	if trackers == nil {
		trackers = c.ids(`SELECT id FROM trackers ORDER BY position`)
	}
	p := &domain.Project{Name: fmt.Sprintf("project-c%04d", n), Identifier: fmt.Sprintf("project-c%04d", n), IsPublic: true}
	c.must(repository.CreateProject(c.ctx, c.d, p, repository.CreateProjectOptions{
		EnabledModules: c.st.Strings("default_projects_modules"), TrackerIDs: trackers}))
	return c.project(p.ID)
}

// generateRoleC は Role.generate! (権限なし)。
func (c *tc) generateRoleC() int64 {
	c.t.Helper()
	n := genSeqC.Add(1)
	var pos int
	c.must(c.d.Get(c.ctx, &pos, `SELECT COALESCE(MAX(position), 0) + 1 FROM roles`))
	id, err := c.d.InsertReturningID(c.ctx, `INSERT INTO roles (name, position, builtin) VALUES (?, ?, 0)`, fmt.Sprintf("Role c%d", n), pos)
	c.must(err)
	return id
}

// addWatcherC は Watcher.create!(user, issue)。
func (c *tc) addWatcherC(issueID, userID int64) {
	c.t.Helper()
	c.exec(`INSERT INTO watchers (watchable_kind, watchable_id, principal_id) VALUES ('issue', ?, ?)`, issueID, userID)
}

// generateTimeEntryC は TimeEntry.generate!(issue)。
func (c *tc) generateTimeEntryC(issue *Issue) int64 {
	c.t.Helper()
	var act int64
	c.must(c.d.Get(c.ctx, &act, `SELECT id FROM time_entry_activities WHERE project_id IS NULL ORDER BY position, id LIMIT 1`))
	d := today()
	y, w := d.ISOWeek()
	id, err := c.d.InsertReturningID(c.ctx, `INSERT INTO time_entries (project_id, user_id, author_id, issue_id, hours, activity_id, spent_on,
  tyear, tmonth, tweek, created_at, updated_at) VALUES (?, 2, 2, ?, 1.0, ?, ?, ?, ?, ?, ?, ?)`,
		issue.ProjectID, issue.ID, act, db.NewDate(d.Date()), y, int(d.Month()), w, db.NewTime(frozenNow), db.NewTime(frozenNow))
	c.must(err)
	return id
}

// recipientsC は issue.recipients (通知ユーザの id)。
func (c *tc) recipientsC(e *Env, iss *Issue) []int64 {
	c.t.Helper()
	us, err := e.NotifiedUsers(c.ctx, iss)
	c.must(err)
	return userIDs(us)
}

func frozenNowT() db.Time { return db.NewTime(frozenNow) }
