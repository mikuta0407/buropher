// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package query

import (
	"context"
	"slices"
	"testing"
)

func TestSmokeDefaultIssueQuery(t *testing.T) {
	forEachDB(t, func(t *testing.T, tdb *testDB) {
		q := tdb.newQuery(2, KindIssue, 0)
		ids := queryIDs(t, q)
		want := []int64{14, 13, 10, 9, 7, 6, 5, 4, 3, 2, 1}
		if !slices.Equal(ids, want) {
			t.Errorf("ids = %v, want %v", ids, want)
		}
		af, err := q.AvailableFilters(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		wantKeys := []string{"status_id", "project_id", "tracker_id", "priority_id", "author_id", "author.group", "author.role", "assigned_to_id", "member_of_group", "assigned_to_role", "fixed_version_id", "fixed_version.due_date", "fixed_version.status", "subject", "description", "notes", "created_on", "updated_on", "closed_on", "start_date", "due_date", "estimated_hours", "spent_time", "done_ratio", "is_private", "attachment", "attachment_description", "watcher_id", "updated_by", "last_updated_by", "project.status", "cf_2", "cf_1", "cf_9", "project.cf_3", "relates", "duplicates", "duplicated", "blocks", "blocked", "precedes", "follows", "copied_to", "copied_from", "parent_id", "child_id", "issue_id", "any_searchable"}
		if !slices.Equal(af.Keys(), wantKeys) {
			t.Errorf("filters = %v", af.Keys())
		}
		cols, err := q.AvailableColumns(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, c := range cols {
			names = append(names, c.Name)
		}
		wantCols := []string{"id", "project", "tracker", "parent", "parent.subject", "status", "priority", "subject", "author", "assigned_to", "watcher_users", "updated_on", "category", "fixed_version", "start_date", "due_date", "estimated_hours", "estimated_remaining_hours", "total_estimated_hours", "spent_hours", "total_spent_hours", "done_ratio", "created_on", "closed_on", "last_updated_by", "relations", "attachments", "description", "last_notes", "cf_2", "cf_1", "cf_6", "cf_8", "cf_9", "is_private"}
		if !slices.Equal(names, wantCols) {
			t.Errorf("columns = %v", names)
		}
	})
}
