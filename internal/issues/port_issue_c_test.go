// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package issues

// test/unit/issue_test.rb の移植 (1353-2632 行: コピー・移動・重複クローズ・バージョン・移動先・通知先・削除・
// ブロック・再スケジュール・親の検証)。
//
// 移植済み:
//   test_copy, test_copy_to_another_project_should_clear_assignee_if_not_valid,
//   test_copy_with_keep_status_should_copy_status, test_copy_should_add_relation_with_copied_issue,
//   test_copy_should_copy_subtasks, test_copy_as_a_child_of_copied_issue_should_not_copy_itself,
//   test_copy_as_a_descendant_of_copied_issue_should_not_copy_itself, test_copy_should_copy_subtasks_to_target_project,
//   test_copy_should_not_copy_subtasks_twice_when_saving_twice, test_copy_should_clear_closed_on,
//   test_copy_should_not_copy_locked_watchers, test_copy_should_not_copy_watchers_without_permission,
//   test_copy_should_clear_subtasks_target_version_if_locked_or_closed, test_copy_should_clear_subtasks_assignee_if_is_locked,
//   test_copy_should_not_add_attachments_to_journal (アップロードの代わりに既存の添付ファイルを AttachSaved で紐付け),
//   test_copy_should_only_copy_editable_custom_fields,
//   test_should_not_call_after_project_change_on_creation / _on_update / test_should_call_after_project_change_on_project_change
//     (モックの代わりに time_entries.project_id の更新有無で確認),
//   test_adding_journal_should_update_timestamp, test_adding_journal_with_notes_and_details_empty_should_not_update_timestamp,
//   test_should_close_duplicates, test_should_not_close_duplicate_when_disabled, test_should_close_duplicates_with_private_notes,
//   test_should_not_close_duplicated_issue, test_assignable_versions, test_should_not_be_able_to_set_an_invalid_version_id,
//   test_should_not_be_able_to_assign_a_new_issue_to_a_closed_version / _locked_version,
//   test_should_be_able_to_assign_a_new_issue_to_an_open_version, test_should_be_able_to_update_an_issue_assigned_to_a_closed_version,
//   test_should_not_be_able_to_reopen_an_issue_assigned_to_a_closed_version,
//   test_should_be_able_to_reopen_and_reassign_an_issue_assigned_to_a_closed_version,
//   test_should_be_able_to_reopen_an_issue_assigned_to_a_locked_version,
//   test_should_not_be_able_to_keep_unshared_version_when_changing_project, test_should_keep_shared_version_when_changing_project,
//   test_should_not_be_able_to_set_an_invalid_category_id,
//   test_allowed_target_projects_should_include_projects_with_issue_tracking_enabled / _disabled / _without_trackers,
//   test_allowed_target_projects_for_subtask_should_not_include_invalid_projects,
//   test_allowed_target_trackers_* (7 件), test_move_to_another_project_* (9 件),
//   test_copy_to_the_same_project, test_copy_to_another_project_and_tracker, "#copy ..." (7 件),
//   test_valid_parent_project, test_recipients_should_include_previous_assignee,
//   test_recipients_should_not_include_users_that_cannot_view_the_issue, test_recipients_should_include_the_assigned_group_members,
//   test_watcher_recipients_should_not_include_users_that_cannot_view_the_issue,
//   test_issue_destroy, test_destroy_should_delete_time_entries_custom_values,
//   test_destroying_a_deleted_issue_should_not_raise_an_error, test_destroying_a_stale_issue_should_not_raise_an_error,
//   test_blocked, test_blocked_should_not_raise_exception_when_blocking_issue_id_is_invalid (FK のため関連を削除して代用),
//   test_blocked_issues_dont_allow_closed_statuses, test_unblocked_issues_allow_closed_statuses,
//   test_parent_issues_with_open_subtask_dont_allow_closed_statuses, test_parent_issues_with_closed_subtask_allow_closed_statuses,
//   test_reschedule_an_issue_without_dates / _with_start_date / _with_start_and_due_dates,
//   test_rescheduling_* (5 件), test_child_issue_should_consider_parent_soonest_start_on_create,
//   test_setting_parent_to_a_an_issue_that_precedes / _follows / _precedes_through_hierarchy_should_not_validate,
//   test_issue_and_following / preceding / blocked / blocking_issue_should_be_able_to_be_moved_to_the_same_parent,
//   test_issue_copy_should_be_able_to_be_moved_to_the_same_parent_as_copied_issue
//
// 省略:
//   test_destroy_should_delete_attachments_on_custom_values (attachment 形式のカスタムフィールドの値設定は未対応)

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/testfixtures"
)

func TestIssueCCopy(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.copyFromC(e, c.issue(1), CopyOptions{})
	if !iss.IsCopy() {
		t.Fatal("copy?")
	}
	c.saveOK(e, iss)
	c.reload(e, iss)
	orig := c.issue(1)
	if iss.Subject != orig.Subject || iss.TrackerID != orig.TrackerID {
		t.Error("subject/tracker")
	}
	if v := cfVal(c, e, iss, 2); v.String() != "125" {
		t.Errorf("cf2 = %q", v.String())
	}
}

func TestIssueCCopyToAnotherProjectShouldClearAssigneeIfNotValid(t *testing.T) {
	c := setup(t)
	e := c.env()
	c.generateSaved(e, Params{"project_id": 1, "assigned_to_id": 2})
	p := c.generateProjectC(nil)
	iss := c.copyFromC(e, c.issue(1), CopyOptions{})
	c.must(e.SetProject(c.ctx, iss, p, false))
	if iss.AssignedToID != nil {
		t.Errorf("assigned_to = %v", *iss.AssignedToID)
	}
}

func TestIssueCCopyWithKeepStatusShouldCopyStatus(t *testing.T) {
	c := setup(t)
	e := c.env()
	orig := c.issue(8)
	ds, _ := e.DefaultStatus(c.ctx, orig)
	if orig.StatusID == ds.ID {
		t.Fatal("precondition")
	}
	iss := c.copyFromC(e, orig, CopyOptions{KeepStatus: true})
	c.saveOK(e, iss)
	c.reload(e, iss)
	if iss.StatusID != orig.StatusID {
		t.Errorf("status = %d", iss.StatusID)
	}
}

func TestIssueCCopyShouldAddRelationWithCopiedIssue(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.copyFromC(e, c.issue(1), CopyOptions{})
	c.saveOK(e, iss)
	rs, err := e.Relations(c.ctx, iss)
	c.must(err)
	if len(rs) != 1 || rs[0].RelationType != "copied_to" || rs[0].IssueFromID != 1 || rs[0].IssueToID != iss.ID {
		t.Errorf("relations = %+v", rs)
	}
}

func childSubjectsC(c *tc, e *Env, iss *Issue) []string {
	c.t.Helper()
	ch, err := e.Children(c.ctx, iss)
	c.must(err)
	var out []string
	for _, x := range ch {
		out = append(out, x.Subject)
	}
	slices.Sort(out)
	return out
}

func TestIssueCCopyShouldCopySubtasks(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.generateWithDescendants(e, Params{})
	desc, _ := e.DescendantIDs(c.ctx, iss)
	cp := c.copyC(e, c.reload(e, iss), nil, CopyOptions{})
	cp.AuthorID = 7
	before := c.count(`SELECT COUNT(*) FROM issues`)
	c.saveOK(e, cp)
	if n := c.count(`SELECT COUNT(*) FROM issues`) - before; n != 1+len(desc) {
		t.Errorf("created %d", n)
	}
	c.reload(e, cp)
	if got := childSubjectsC(c, e, cp); !slices.Equal(got, []string{"Child1", "Child2"}) {
		t.Errorf("children = %v", got)
	}
	ch, _ := e.Children(c.ctx, cp)
	var child1 *Issue
	for _, x := range ch {
		if x.Subject == "Child1" {
			child1 = x
		}
	}
	if got := childSubjectsC(c, e, child1); !slices.Equal(got, []string{"Child11"}) {
		t.Errorf("grandchildren = %v", got)
	}
	if child1.AuthorID != cp.AuthorID {
		t.Errorf("author = %d", child1.AuthorID)
	}
}

func TestIssueCCopyAsAChildOfCopiedIssueShouldNotCopyItself(t *testing.T) {
	c := setup(t)
	e := c.env()
	parent := c.generateSaved(e, Params{})
	c.generateSaved(e, Params{"parent_issue_id": parent.ID, "subject": "Child 1"})
	c.generateSaved(e, Params{"parent_issue_id": parent.ID, "subject": "Child 2"})
	cp := c.copyC(e, c.reload(e, parent), nil, CopyOptions{})
	c.must(e.SetParentIssueID(c.ctx, cp, itoa(parent.ID)))
	cp.AuthorID = 7
	before := c.count(`SELECT COUNT(*) FROM issues`)
	c.saveOK(e, cp)
	if n := c.count(`SELECT COUNT(*) FROM issues`) - before; n != 3 {
		t.Errorf("created %d", n)
	}
	c.reload(e, parent)
	c.reload(e, cp)
	if cp.ParentID == nil || *cp.ParentID != parent.ID {
		t.Error("parent")
	}
	if n := c.count(`SELECT COUNT(*) FROM issues WHERE parent_id = ?`, parent.ID); n != 3 {
		t.Errorf("parent children = %d", n)
	}
	d, _ := e.DescendantIDs(c.ctx, parent)
	if len(d) != 5 {
		t.Errorf("parent descendants = %d", len(d))
	}
	d, _ = e.DescendantIDs(c.ctx, cp)
	if n := c.count(`SELECT COUNT(*) FROM issues WHERE parent_id = ?`, cp.ID); n != 2 || len(d) != 2 {
		t.Errorf("copy children = %d, descendants %d", n, len(d))
	}
}

func TestIssueCCopyAsADescendantOfCopiedIssueShouldNotCopyItself(t *testing.T) {
	c := setup(t)
	e := c.env()
	parent := c.generateSaved(e, Params{})
	child1 := c.generateSaved(e, Params{"parent_issue_id": parent.ID, "subject": "Child 1"})
	c.generateSaved(e, Params{"parent_issue_id": parent.ID, "subject": "Child 2"})
	cp := c.copyC(e, c.reload(e, parent), nil, CopyOptions{})
	c.must(e.SetParentIssueID(c.ctx, cp, itoa(child1.ID)))
	cp.AuthorID = 7
	before := c.count(`SELECT COUNT(*) FROM issues`)
	c.saveOK(e, cp)
	if n := c.count(`SELECT COUNT(*) FROM issues`) - before; n != 3 {
		t.Errorf("created %d", n)
	}
	if cp.ParentID == nil || *cp.ParentID != child1.ID {
		t.Error("parent")
	}
	cnt := func(id int64) (int, int) {
		d, _ := e.DescendantIDs(c.ctx, &Issue{Issue: domain.Issue{ID: id}, orig: &domain.Issue{}})
		return c.count(`SELECT COUNT(*) FROM issues WHERE parent_id = ?`, id), len(d)
	}
	if a, b := cnt(parent.ID); a != 2 || b != 5 {
		t.Errorf("parent %d %d", a, b)
	}
	if a, b := cnt(child1.ID); a != 1 || b != 3 {
		t.Errorf("child1 %d %d", a, b)
	}
	if a, b := cnt(cp.ID); a != 2 || b != 2 {
		t.Errorf("copy %d %d", a, b)
	}
}

func TestIssueCCopyShouldCopySubtasksToTargetProject(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.generateWithDescendants(e, Params{})
	cp := c.copyC(e, iss, Params{"project_id": 3}, CopyOptions{})
	before := c.count(`SELECT COUNT(*) FROM issues`)
	c.saveOK(e, cp)
	if n := c.count(`SELECT COUNT(*) FROM issues`) - before; n != 4 {
		t.Errorf("created %d", n)
	}
	d, _ := e.Descendants(c.ctx, cp)
	for _, x := range d {
		if x.ProjectID != 3 {
			t.Errorf("descendant project %d", x.ProjectID)
		}
	}
}

func TestIssueCCopyShouldNotCopySubtasksTwiceWhenSavingTwice(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.generateWithDescendants(e, Params{})
	cp := c.copyC(e, c.reload(e, iss), nil, CopyOptions{})
	before := c.count(`SELECT COUNT(*) FROM issues`)
	c.saveOK(e, cp)
	c.saveOK(e, c.reload(e, cp))
	if n := c.count(`SELECT COUNT(*) FROM issues`) - before; n != 4 {
		t.Errorf("created %d", n)
	}
}

func TestIssueCCopyShouldClearClosedOn(t *testing.T) {
	c := setup(t)
	e := c.env()
	open := c.copyC(e, c.issue(8), Params{"status_id": 1}, CopyOptions{})
	c.saveOK(e, open)
	if open.ClosedAt != nil {
		t.Error("closed_on should be nil")
	}
	closed := c.copyC(e, c.issue(8), Params{}, CopyOptions{KeepStatus: true})
	c.saveOK(e, closed)
	if closed.ClosedAt == nil {
		t.Error("closed_on should be set")
	}
}

func TestIssueCCopyShouldNotCopyLockedWatchers(t *testing.T) {
	c := setup(t)
	c.as(2)
	c.addWatcherC(8, 2)
	c.addWatcherC(8, 3)
	c.exec(`UPDATE principals SET status = 3 WHERE id = 3`)
	e := c.env()
	iss := c.copyFromC(e, c.issue(8), CopyOptions{})
	c.saveOK(e, iss)
	if w, _ := e.WatchedBy(c.ctx, iss, c.user(2)); !w {
		t.Error("user 2 should watch")
	}
	if w, _ := e.WatchedBy(c.ctx, iss, c.user(3)); w {
		t.Error("user 3 should not watch")
	}
}

func TestIssueCCopyShouldNotCopyWatchersWithoutPermission(t *testing.T) {
	c := setup(t)
	c.removePermission(1, "view_issue_watchers")
	c.as(2)
	c.addWatcherC(8, 2)
	c.addWatcherC(8, 3)
	e := c.env()
	iss := c.copyFromC(e, c.issue(8), CopyOptions{})
	c.saveOK(e, iss)
	if w, _ := e.WatchedBy(c.ctx, iss, c.user(2)); !w {
		t.Error("user 2 should watch")
	}
	if w, _ := e.WatchedBy(c.ctx, iss, c.user(3)); w {
		t.Error("user 3 should not watch")
	}
}

func TestIssueCCopyShouldClearSubtasksTargetVersionIfLockedOrClosed(t *testing.T) {
	c := setup(t)
	e := c.env()
	vid, err := c.d.InsertReturningID(c.ctx, `INSERT INTO versions (project_id, name, status, sharing, created_at, updated_at) VALUES (1, '2.1', 'open', 'none', ?, ?)`,
		frozenNowT(), frozenNowT())
	c.must(err)
	parent := c.generateSaved(e, Params{})
	c.generateSaved(e, Params{"parent_issue_id": parent.ID, "subject": "Child 1", "fixed_version_id": 3})
	c.generateSaved(e, Params{"parent_issue_id": parent.ID, "subject": "Child 2", "fixed_version_id": vid})
	c.exec(`UPDATE versions SET status = 'locked' WHERE id = ?`, vid)
	cp := c.copyC(e, c.reload(e, parent), nil, CopyOptions{})
	before := c.count(`SELECT COUNT(*) FROM issues`)
	c.saveOK(e, cp)
	if n := c.count(`SELECT COUNT(*) FROM issues`) - before; n != 3 {
		t.Errorf("created %d", n)
	}
	ch, _ := e.Children(c.ctx, cp)
	if len(ch) != 2 || ch[0].FixedVersionID == nil || *ch[0].FixedVersionID != 3 || ch[1].FixedVersionID != nil {
		t.Errorf("children versions")
	}
}

func TestIssueCCopyShouldClearSubtasksAssigneeIfIsLocked(t *testing.T) {
	c := setup(t)
	e := c.env()
	parent := c.generateSaved(e, Params{})
	c.generateSaved(e, Params{"parent_issue_id": parent.ID, "subject": "Child 1", "assigned_to_id": 3})
	c.generateSaved(e, Params{"parent_issue_id": parent.ID, "subject": "Child 2", "assigned_to_id": 2})
	c.exec(`UPDATE principals SET status = 3 WHERE id = 2`)
	e = c.env()
	cp := c.copyC(e, c.reload(e, parent), nil, CopyOptions{})
	before := c.count(`SELECT COUNT(*) FROM issues`)
	c.saveOK(e, cp)
	if n := c.count(`SELECT COUNT(*) FROM issues`) - before; n != 3 {
		t.Errorf("created %d", n)
	}
	ch, _ := e.Children(c.ctx, cp)
	if len(ch) != 2 || ch[0].AssignedToID == nil || *ch[0].AssignedToID != 3 || ch[1].AssignedToID != nil {
		t.Errorf("children assignees")
	}
}

func TestIssueCCopyShouldNotAddAttachmentsToJournal(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.generateSaved(e, Params{})
	cp, _ := e.NewBlank(c.ctx)
	_, err := e.InitJournal(c.ctx, cp, c.user(1), "")
	c.must(err)
	c.must(e.CopyFrom(c.ctx, cp, iss, CopyOptions{}))
	p, _ := e.ProjectOf(c.ctx, iss)
	c.must(e.SetProject(c.ctx, cp, p, false))
	aid, err := c.d.InsertReturningID(c.ctx, `INSERT INTO attachments (filename, disk_filename, filesize, author_id, created_at) VALUES ('upload', 'x_upload', 4, 1, ?)`, frozenNowT())
	c.must(err)
	cp.AttachSaved(aid)
	c.saveOK(e, cp)
	j := lastJournal(c, cp.ID)
	if j == nil || len(j.Details) != 1 || j.Details[0].Property != "relation" {
		t.Errorf("journal = %+v", j)
	}
}

func TestIssueCCopyShouldOnlyCopyEditableCustomFields(t *testing.T) {
	c := setup(t)
	c.exec(`INSERT INTO custom_fields_roles (custom_field_id, role_id) VALUES (1, 1)`)
	c.exec(`UPDATE custom_fields SET visible = ? WHERE id = 1`, false)
	c.as(3)
	e := c.env()
	iss := c.copyFromC(e, c.issue(3), CopyOptions{})
	c.saveOK(e, iss)
	if v := cfVal(c, e, iss, 1); v.String() != "" {
		t.Errorf("cf1 = %q", v.String())
	}
}

func timeEntryProjectC(c *tc, issueID int64) int64 {
	var p int64
	c.must(c.d.Get(c.ctx, &p, `SELECT project_id FROM time_entries WHERE issue_id = ? ORDER BY id LIMIT 1`, issueID))
	return p
}

func TestIssueCAfterProjectChange(t *testing.T) {
	c := setup(t)
	e := c.env()
	// on creation: 新規作成では after_project_change を呼ばない (作成は問題なく完了する)
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "status_id": 1, "subject": "Test", "author_id": 1})
	c.saveOK(e, iss)
	if iss.SavedChangeTo("project_id") && !iss.savedNew {
		t.Error("project change on creation")
	}
	// on update without project change: 工数のプロジェクトを書き換えない
	c.exec(`UPDATE time_entries SET project_id = 3 WHERE issue_id = 1`)
	iss = c.issue(1)
	c.must(e.SetProject(c.ctx, iss, c.project(1), false))
	iss.Subject = "No project change"
	c.saveOK(e, iss)
	if timeEntryProjectC(c, 1) != 3 {
		t.Error("after_project_change should not be called")
	}
	// project change
	iss = c.issue(1)
	c.must(e.SetProject(c.ctx, iss, c.project(2), false))
	c.saveOK(e, iss)
	if timeEntryProjectC(c, 1) != 2 {
		t.Error("after_project_change should be called")
	}
}

func TestIssueCAddingJournalShouldUpdateTimestamp(t *testing.T) {
	c := setup(t)
	c.now = frozenNow.Add(3600e9)
	e := c.env()
	iss := c.issue(1)
	was := iss.UpdatedAt
	_, err := e.InitJournal(c.ctx, iss, c.user(1), "Adding notes")
	c.must(err)
	before := c.count(`SELECT COUNT(*) FROM issue_journals`)
	c.saveOK(e, iss)
	if c.count(`SELECT COUNT(*) FROM issue_journals`) != before+1 {
		t.Error("journal count")
	}
	if c.issue(1).UpdatedAt.Equal(was) {
		t.Error("updated_on should change")
	}
}

func TestIssueCAddingJournalWithNotesAndDetailsEmptyShouldNotUpdateTimestamp(t *testing.T) {
	c := setup(t)
	c.now = frozenNow.Add(3600e9)
	e := c.env()
	iss := c.issue(1)
	was := iss.UpdatedAt
	_, err := e.InitJournal(c.ctx, iss, c.user(1), "")
	c.must(err)
	before := c.count(`SELECT COUNT(*) FROM issue_journals`)
	c.saveOK(e, iss)
	if c.count(`SELECT COUNT(*) FROM issue_journals`) != before {
		t.Error("journal count")
	}
	if !c.issue(1).UpdatedAt.Equal(was) {
		t.Error("updated_on should not change")
	}
}

func closedStatusIDC(c *tc) int64 {
	return c.ids(`SELECT id FROM issue_statuses WHERE is_closed = ? ORDER BY id LIMIT 1`, true)[0]
}

func isClosedC(c *tc, e *Env, id int64) bool {
	ok, err := e.Closed(c.ctx, c.issue(id))
	c.must(err)
	return ok
}

func TestIssueCShouldCloseDuplicates(t *testing.T) {
	c := setup(t)
	e := c.env()
	i1 := c.generateSaved(e, Params{})
	i2 := c.generateSaved(e, Params{})
	i3 := c.generateSaved(e, Params{})
	c.addRelation(e, i2, i1, "duplicates", nil)
	c.addRelation(e, i3, i2, "duplicates", nil)
	c.addRelation(e, i3, i1, "duplicates", nil)
	dups, _ := e.Duplicates(c.ctx, c.reload(e, i1))
	if !slices.ContainsFunc(dups, func(x *Issue) bool { return x.ID == i2.ID }) {
		t.Fatal("duplicates")
	}
	_, err := e.InitJournal(c.ctx, i1, c.user(1), "Closing issue1")
	c.must(err)
	i1.StatusID = closedStatusIDC(c)
	c.saveOK(e, i1)
	if !isClosedC(c, e, i2.ID) || !isClosedC(c, e, i3.ID) {
		t.Error("2 and 3 should be closed")
	}
}

func TestIssueCShouldNotCloseDuplicateWhenDisabled(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.generateSaved(e, Params{})
	dup := c.generateSaved(e, Params{})
	c.addRelation(e, dup, iss, "duplicates", nil)
	c.setting("close_duplicate_issues", "0")
	c.reload(e, iss)
	_, err := e.InitJournal(c.ctx, iss, c.user(1), "Closing issue")
	c.must(err)
	iss.StatusID = closedStatusIDC(c)
	c.save(e, iss)
	if isClosedC(c, e, dup.ID) {
		t.Error("duplicate should not be closed")
	}
}

func TestIssueCShouldCloseDuplicatesWithPrivateNotes(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.generateSaved(e, Params{})
	dup := c.generateSaved(e, Params{})
	c.addRelation(e, dup, iss, "duplicates", nil)
	c.reload(e, iss)
	j, err := e.InitJournal(c.ctx, iss, c.user(1), "Private notes")
	c.must(err)
	j.PrivateNotes = true
	iss.StatusID = closedStatusIDC(c)
	c.saveOK(e, iss)
	found := false
	for _, j := range journals(c, dup.ID) {
		if j.Notes == "Private notes" {
			found = true
			if !j.PrivateNotes {
				t.Error("should be private")
			}
		}
	}
	if !found {
		t.Error("journal not found")
	}
}

func TestIssueCShouldNotCloseDuplicatedIssue(t *testing.T) {
	c := setup(t)
	e := c.env()
	i1 := c.generateSaved(e, Params{})
	i2 := c.generateSaved(e, Params{})
	c.addRelation(e, i2, i1, "duplicates", nil)
	dups, _ := e.Duplicates(c.ctx, c.reload(e, i2))
	if len(dups) != 0 {
		t.Fatal("2 has no duplicates")
	}
	_, err := e.InitJournal(c.ctx, i2, c.user(1), "Closing issue2")
	c.must(err)
	i2.StatusID = closedStatusIDC(c)
	c.saveOK(e, i2)
	if isClosedC(c, e, i1.ID) {
		t.Error("1 should not be closed")
	}
}

func TestIssueCAssignableVersions(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "author_id": 1, "status_id": 1, "fixed_version_id": 1, "subject": "New issue"})
	vs, err := e.AssignableVersions(c.ctx, iss)
	c.must(err)
	for _, v := range vs {
		if v.Status != "open" {
			t.Errorf("version %d status %s", v.ID, v.Status)
		}
	}
}

func TestIssueCNewIssueVersionAssignment(t *testing.T) {
	c := setup(t)
	e := c.env()
	for _, tt := range []struct {
		vid int64
		ok  bool
	}{{424242, false}, {1, false}, {2, false}, {3, true}} {
		iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "author_id": 1, "status_id": 1, "fixed_version_id": tt.vid, "subject": "New issue"})
		ok := c.save(e, iss)
		if ok != tt.ok || (!ok && len(errorsOn(iss, "fixed_version_id")) == 0) {
			t.Errorf("version %d: ok=%v errors=%v", tt.vid, ok, iss.Errors.List)
		}
	}
}

func TestIssueCClosedVersionUpdates(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.issue(11)
	v, _ := e.Version(c.ctx, iss.FixedVersionID)
	if v.Status != "closed" {
		t.Fatal("precondition")
	}
	iss.Subject = "Subject changed"
	if !c.save(e, iss) {
		t.Errorf("update closed version: %v", iss.Errors.List)
	}
	iss = c.issue(11)
	c.must(e.SetStatusID(c.ctx, iss, 1))
	if c.save(e, iss) || len(errorsOn(iss, "base")) == 0 {
		t.Errorf("reopen on closed version: %v", iss.Errors.List)
	}
	iss = c.issue(11)
	c.must(e.SetStatusID(c.ctx, iss, 1))
	iss.FixedVersionID = ptrInt64(3)
	if !c.save(e, iss) {
		t.Errorf("reopen and reassign: %v", iss.Errors.List)
	}
	iss = c.issue(12)
	v, _ = e.Version(c.ctx, iss.FixedVersionID)
	if v.Status != "locked" {
		t.Fatal("precondition locked")
	}
	c.must(e.SetStatusID(c.ctx, iss, 1))
	if !c.save(e, iss) {
		t.Errorf("reopen on locked: %v", iss.Errors.List)
	}
}

func TestIssueCShouldNotBeAbleToKeepUnsharedVersionWhenChangingProject(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.issue(2)
	if iss.FixedVersionID == nil || *iss.FixedVersionID != 2 {
		t.Fatal("precondition")
	}
	c.must(e.SetProjectID(c.ctx, iss, 3))
	if iss.FixedVersionID != nil {
		t.Error("fixed_version should be cleared")
	}
	iss.FixedVersionID = ptrInt64(2)
	if c.save(e, iss) || !hasError(iss, "fixed_version_id", "inclusion") {
		t.Errorf("errors = %v", iss.Errors.List)
	}
}

func TestIssueCShouldKeepSharedVersionWhenChangingProject(t *testing.T) {
	c := setup(t)
	c.exec(`UPDATE versions SET sharing = 'tree' WHERE id = 2`)
	e := c.env()
	iss := c.issue(2)
	c.must(e.SetProjectID(c.ctx, iss, 3))
	if iss.FixedVersionID == nil || *iss.FixedVersionID != 2 {
		t.Error("fixed_version should be kept")
	}
	if !c.save(e, iss) {
		t.Errorf("errors = %v", iss.Errors.List)
	}
}

func TestIssueCShouldNotBeAbleToSetAnInvalidCategoryID(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "author_id": 1, "status_id": 1, "category_id": 3, "subject": "New issue"})
	if c.save(e, iss) || len(errorsOn(iss, "category_id")) == 0 {
		t.Errorf("errors = %v", iss.Errors.List)
	}
}

func projectIDsOfC(ps []*domain.Project) []int64 {
	var out []int64
	for _, p := range ps {
		out = append(out, p.ID)
	}
	return out
}

func TestIssueCAllowedTargetProjects(t *testing.T) {
	c := setup(t)
	e := c.env()
	blank, _ := e.NewBlank(c.ctx)
	ps, err := e.AllowedTargetProjects(c.ctx, blank, c.user(2), "*")
	c.must(err)
	if !slices.Contains(projectIDsOfC(ps), 2) {
		t.Error("should include project 2")
	}
	c.exec(`DELETE FROM project_modules WHERE project_id = 2 AND name = 'issue_tracking'`)
	e = c.env()
	ps, err = e.AllowedTargetProjects(c.ctx, blank, c.user(2), "*")
	c.must(err)
	if slices.Contains(projectIDsOfC(ps), 2) {
		t.Error("should not include project 2 without issue tracking")
	}
	p := c.generateProjectC([]int64{})
	ps, err = e.AllowedTargetProjects(c.ctx, blank, c.user(1), "*")
	c.must(err)
	if slices.Contains(projectIDsOfC(ps), p.ID) {
		t.Error("should not include project without trackers")
	}
}

func TestIssueCAllowedTargetProjectsForSubtaskShouldNotIncludeInvalidProjects(t *testing.T) {
	c := setup(t)
	c.as(1)
	c.setting("cross_project_subtasks", "tree")
	e := c.env()
	iss := c.issue(1)
	iss.ParentID = ptrInt64(3)
	ps, err := e.AllowedTargetProjectsForSubtask(c.ctx, iss, nil)
	c.must(err)
	got := projectIDsOfC(ps)
	slices.Sort(got)
	eqIDs(t, []int64{1, 3, 5}, got)
}

func trackerIDsOfC(ts []*domain.Tracker) []int64 {
	out := []int64{}
	for _, t := range ts {
		out = append(out, t.ID)
	}
	slices.Sort(out)
	return out
}

func TestIssueCAllowedTargetTrackers(t *testing.T) {
	for _, tt := range []struct {
		name  string
		roles [][]int64 // nil = 全トラッカー, ロールごとの add_issues トラッカー
		noAdd []bool
		want  []int64
	}{
		{"one role all trackers", [][]int64{nil}, []bool{false}, []int64{1, 2, 3}},
		{"one role some trackers", [][]int64{{1, 3}}, []bool{false}, []int64{1, 3}},
		{"two roles some trackers", [][]int64{{1}, {3}}, []bool{false, false}, []int64{1, 3}},
		{"two roles all and some", [][]int64{nil, {1, 3}}, []bool{false, false}, []int64{1, 2, 3}},
		{"role without add_issues", [][]int64{nil, {1, 3}}, []bool{true, false}, []int64{1, 3}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := setup(t)
			uid := c.generateUser(testfixtures.UserAttrs{})
			var roles []int64
			for i, trs := range tt.roles {
				r := c.generateRoleC()
				if !tt.noAdd[i] {
					c.addPermission(r, "add_issues")
					c.setPermissionTrackers(r, "add_issues", trs)
				}
				roles = append(roles, r)
			}
			c.addMember(uid, 1, roles...)
			e := c.env()
			iss := c.newIssue(e, Params{"project_id": 1})
			ts, err := e.AllowedTargetTrackers(c.ctx, iss, c.user(uid))
			c.must(err)
			eqIDs(t, tt.want, trackerIDsOfC(ts))
		})
	}
}

func TestIssueCAllowedTargetTrackersWithoutProjectShouldBeEmpty(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss, _ := e.NewBlank(c.ctx)
	ts, err := e.AllowedTargetTrackers(c.ctx, iss, c.user(2))
	c.must(err)
	if len(ts) != 0 {
		t.Errorf("trackers = %v", trackerIDsOfC(ts))
	}
}

func TestIssueCAllowedTargetTrackersShouldIncludeCurrentTracker(t *testing.T) {
	c := setup(t)
	uid := c.generateUser(testfixtures.UserAttrs{})
	r := c.generateRoleC()
	c.addPermission(r, "add_issues")
	c.setPermissionTrackers(r, "add_issues", []int64{3})
	c.addMember(uid, 1, r)
	e := c.env()
	iss := c.generateSaved(e, Params{"project_id": 1, "tracker_id": 1})
	ts, err := e.AllowedTargetTrackers(c.ctx, iss, c.user(uid))
	c.must(err)
	eqIDs(t, []int64{1, 3}, trackerIDsOfC(ts))
}

func moveToC(c *tc, e *Env, iss *Issue, pid int64) *Issue {
	c.t.Helper()
	c.must(e.SetProject(c.ctx, iss, c.project(pid), false))
	if !c.save(e, iss) {
		c.t.Fatalf("move: %v", iss.Errors.List)
	}
	return c.reload(e, iss)
}

func updateOKC(c *tc, e *Env, iss *Issue, attrs Params) {
	c.t.Helper()
	c.must(e.AssignAttributes(c.ctx, iss, attrs))
	c.saveOK(e, iss)
}

func TestIssueCMoveToAnotherProjectWithSameCategory(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := moveToC(c, e, c.issue(1), 2)
	if iss.ProjectID != 2 || iss.CategoryID == nil || *iss.CategoryID != 4 {
		t.Errorf("project %d category %v", iss.ProjectID, iss.CategoryID)
	}
	if timeEntryProjectC(c, 1) != 2 {
		t.Error("time entries should be moved")
	}
}

func TestIssueCMoveToAnotherProjectWithoutSameCategory(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := moveToC(c, e, c.issue(2), 2)
	if iss.ProjectID != 2 || iss.CategoryID != nil {
		t.Errorf("project %d category %v", iss.ProjectID, iss.CategoryID)
	}
}

func TestIssueCMoveToAnotherProjectFixedVersion(t *testing.T) {
	for _, tt := range []struct {
		vid, pid int64
		keep     bool
	}{{3, 2, false}, {4, 5, true}, {3, 5, false}, {7, 2, true}} {
		c := setup(t)
		e := c.env()
		iss := c.issue(1)
		updateOKC(c, e, iss, Params{"fixed_version_id": tt.vid})
		iss = moveToC(c, e, iss, tt.pid)
		if iss.ProjectID != tt.pid {
			t.Errorf("project")
		}
		if tt.keep && (iss.FixedVersionID == nil || *iss.FixedVersionID != tt.vid) || !tt.keep && iss.FixedVersionID != nil {
			t.Errorf("version %d -> project %d: fixed_version = %v", tt.vid, tt.pid, iss.FixedVersionID)
		}
	}
}

func TestIssueCMoveToAnotherProjectParent(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.issue(1)
	updateOKC(c, e, iss, Params{"parent_issue_id": 2})
	iss = moveToC(c, e, iss, 3)
	if iss.ParentID == nil || *iss.ParentID != 2 {
		t.Errorf("parent should be kept: %v", iss.ParentID)
	}
	c = setup(t)
	e = c.env()
	iss = c.issue(1)
	updateOKC(c, e, iss, Params{"parent_issue_id": 2})
	iss = moveToC(c, e, iss, 2)
	if iss.ParentID != nil {
		t.Errorf("parent should be cleared: %v", *iss.ParentID)
	}
}

func TestIssueCMoveToAnotherProjectWithDisabledTracker(t *testing.T) {
	c := setup(t)
	c.exec(`DELETE FROM project_trackers WHERE project_id = 2`)
	c.exec(`INSERT INTO project_trackers (project_id, tracker_id) VALUES (2, 3)`)
	e := c.env()
	iss := moveToC(c, e, c.issue(1), 2)
	if iss.ProjectID != 2 || iss.TrackerID != 3 {
		t.Errorf("project %d tracker %d", iss.ProjectID, iss.TrackerID)
	}
}

func TestIssueCMoveToAnotherProjectShouldSetErrorMessageOnChildFailure(t *testing.T) {
	c := setup(t)
	e := c.env()
	parent := c.generateSaved(e, Params{})
	child := c.generateSaved(e, Params{"parent_issue_id": parent.ID, "tracker_id": 2})
	p := c.generateProjectC([]int64{1})
	e = c.env()
	c.reload(e, parent)
	c.must(e.SetProjectID(c.ctx, parent, p.ID))
	if c.save(e, parent) {
		t.Fatal("should not save")
	}
	msgs := errorsOn(parent, "base")
	want := "#" + itoa(child.ID)
	if len(msgs) != 1 || !strings.Contains(msgs[0], "error_move_of_child_not_possible") || !strings.Contains(msgs[0], want) ||
		!strings.Contains(msgs[0], "tracker") {
		t.Errorf("base errors = %v", msgs)
	}
	if c.issue(parent.ID).ProjectID == p.ID {
		t.Error("move should be rolled back")
	}
}

func TestIssueCCopyToTheSameProject(t *testing.T) {
	c := setup(t)
	e := c.env()
	cp := c.copyC(e, c.issue(1), nil, CopyOptions{})
	before := c.count(`SELECT COUNT(*) FROM issues`)
	c.saveOK(e, cp)
	if c.count(`SELECT COUNT(*) FROM issues`) != before+1 {
		t.Error("count")
	}
	if cp.ProjectID != 1 || cfVal(c, e, cp, 2).String() != "125" {
		t.Errorf("project %d cf %q", cp.ProjectID, cfVal(c, e, cp, 2).String())
	}
}

func TestIssueCCopyToAnotherProjectAndTracker(t *testing.T) {
	c := setup(t)
	e := c.env()
	cp := c.copyC(e, c.issue(1), Params{"project_id": 3, "tracker_id": 2}, CopyOptions{})
	c.saveOK(e, cp)
	c.reload(e, cp)
	if cp.ProjectID != 3 || cp.TrackerID != 2 {
		t.Error("project/tracker")
	}
	if c.count(`SELECT COUNT(*) FROM custom_values WHERE customized_kind = 'issue' AND customized_id = ? AND custom_field_id = 2`, cp.ID) != 0 {
		t.Error("custom value 2 should not exist")
	}
}

func TestIssueCCopyShouldNotCreateAJournal(t *testing.T) {
	c := setup(t)
	e := c.env()
	cp := c.copyC(e, c.issue(1), Params{"project_id": 3, "tracker_id": 2}, CopyOptions{NoLink: true})
	c.saveOK(e, cp)
	if len(journals(c, cp.ID)) != 0 {
		t.Error("no journals expected")
	}
}

func TestIssueCCopyShouldAllowChanges(t *testing.T) {
	c := setup(t)
	uid := c.generateUser(testfixtures.UserAttrs{})
	c.addMember(uid, 3, 1)
	e := c.env()
	cp := c.copyC(e, c.issue(1), Params{"project_id": 3, "tracker_id": 2, "assigned_to_id": uid}, CopyOptions{})
	if cp.AssignedToID == nil || *cp.AssignedToID != uid {
		t.Error("assigned_to")
	}
	cp = c.copyC(e, c.issue(1), Params{"project_id": 3, "tracker_id": 2, "status_id": 2}, CopyOptions{})
	if cp.StatusID != 2 {
		t.Error("status")
	}
	cp = c.copyC(e, c.issue(1), Params{"project_id": 3, "tracker_id": 2, "start_date": today()}, CopyOptions{})
	if cp.StartDate == nil || !cp.StartDate.Equal(today()) {
		t.Error("start_date")
	}
	cp = c.copyC(e, c.issue(1), Params{"project_id": 3, "tracker_id": 2, "due_date": today()}, CopyOptions{})
	if cp.DueDate == nil || !cp.DueDate.Equal(today()) {
		t.Error("due_date")
	}
	c.as(9)
	e = c.env()
	cp = c.copyC(e, c.issue(1), Params{"project_id": 3, "tracker_id": 2}, CopyOptions{})
	if cp.AuthorID != 9 {
		t.Error("author")
	}
}

func TestIssueCCopyShouldCreateAJournalWithNotes(t *testing.T) {
	c := setup(t)
	e := c.env()
	cp := c.copyC(e, c.issue(1), Params{"project_id": 3, "tracker_id": 2, "start_date": today()}, CopyOptions{NoLink: true})
	cur, _ := e.currentUser(c.ctx)
	_, err := e.InitJournal(c.ctx, cp, cur, "Notes added when copying")
	c.must(err)
	c.saveOK(e, cp)
	js := journals(c, cp.ID)
	if len(js) != 1 || len(js[0].Details) != 0 || js[0].Notes != "Notes added when copying" {
		t.Errorf("journals = %+v", js)
	}
}

func TestIssueCValidParentProject(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.issue(1)
	same := c.issue(2)
	child := c.issue(5)
	grand := c.generateSaved(e, Params{"project_id": 6, "tracker_id": 1})
	otherChild := c.issue(6)
	diff := c.issue(4)
	check := func(mode string, a, b *Issue, want bool) {
		t.Helper()
		c.setting("cross_project_subtasks", mode)
		got, err := e.ValidParentProject(c.ctx, a, b)
		c.must(err)
		if got != want {
			t.Errorf("%q: %d parent %d = %v", mode, a.ID, b.ID, got)
		}
	}
	check("", iss, same, true)
	check("", iss, child, false)
	check("", iss, grand, false)
	check("", iss, diff, false)
	check("system", iss, same, true)
	check("system", iss, child, true)
	check("system", iss, diff, true)
	check("tree", iss, same, true)
	check("tree", iss, child, true)
	check("tree", iss, grand, true)
	check("tree", iss, diff, false)
	check("tree", child, same, true)
	check("tree", child, otherChild, true)
	check("descendants", iss, same, true)
	check("descendants", iss, child, false)
	check("descendants", iss, grand, false)
	check("descendants", iss, diff, false)
	check("descendants", child, iss, true)
	check("descendants", child, otherChild, false)
}

func TestIssueCRecipientsShouldIncludePreviousAssignee(t *testing.T) {
	c := setup(t)
	c.exec(`DELETE FROM user_notified_projects WHERE user_id = 3`)
	c.exec(`UPDATE user_notification_settings SET mail_notification = 'only_assigned' WHERE user_id = 3`)
	e := c.env()
	iss := c.issue(2)
	iss.AssignedToID = nil
	if !slices.Contains(c.recipientsC(e, iss), 3) {
		t.Error("before save")
	}
	c.saveOK(e, iss)
	if !slices.Contains(c.recipientsC(e, iss), 3) {
		t.Error("after save")
	}
	iss.AssignedToID = ptrInt64(2)
	c.saveOK(e, iss)
	if slices.Contains(c.recipientsC(e, iss), 3) {
		t.Error("after reassign")
	}
}

func TestIssueCRecipientsShouldNotIncludeUsersThatCannotViewTheIssue(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.issue(12)
	if !slices.Contains(c.recipientsC(e, iss), iss.AuthorID) {
		t.Error("author should be included")
	}
	cp := c.copyC(e, iss, Params{"project_id": 5, "tracker_id": 2}, CopyOptions{})
	if slices.Contains(c.recipientsC(e, cp), cp.AuthorID) {
		t.Error("copy author should not be included")
	}
}

func TestIssueCRecipientsShouldIncludeTheAssignedGroupMembers(t *testing.T) {
	c := setup(t)
	uid := c.generateUser(testfixtures.UserAttrs{})
	gid := c.generateGroup()
	c.exec(`INSERT INTO group_users (group_id, user_id) VALUES (?, ?)`, gid, uid)
	e := c.env()
	iss := c.issue(12)
	iss.AssignedToID = ptrInt64(gid)
	if !slices.Contains(c.recipientsC(e, iss), uid) {
		t.Error("group member should be included")
	}
}

func TestIssueCWatcherRecipientsShouldNotIncludeUsersThatCannotViewTheIssue(t *testing.T) {
	c := setup(t)
	c.addWatcherC(9, 3)
	e := c.env()
	iss := c.issue(9)
	if w, _ := e.WatchedBy(c.ctx, iss, c.user(3)); !w {
		t.Fatal("watched_by")
	}
	ws, err := e.NotifiedWatchers(c.ctx, iss)
	c.must(err)
	if slices.Contains(userIDs(ws), 3) {
		t.Error("user 3 should not be notified")
	}
}

func TestIssueCIssueDestroy(t *testing.T) {
	c := setup(t)
	e := c.env()
	_, err := e.Destroy(c.ctx, c.issue(1))
	c.must(err)
	if c.count(`SELECT COUNT(*) FROM issues WHERE id = 1`) != 0 || c.count(`SELECT COUNT(*) FROM time_entries WHERE issue_id = 1`) != 0 {
		t.Error("not destroyed")
	}
}

func TestIssueCDestroyShouldDeleteTimeEntriesCustomValues(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.generateSaved(e, Params{})
	te := c.generateTimeEntryC(iss)
	c.exec(`INSERT INTO custom_values (customized_kind, customized_id, custom_field_id, value) VALUES ('time_entry', ?, 10, '1')`, te)
	before := c.count(`SELECT COUNT(*) FROM custom_values WHERE customized_kind = 'time_entry'`)
	_, err := e.Destroy(c.ctx, iss)
	c.must(err)
	if n := c.count(`SELECT COUNT(*) FROM custom_values WHERE customized_kind = 'time_entry'`); n != before-1 {
		t.Errorf("custom values %d -> %d", before, n)
	}
}

func TestIssueCDestroyingADeletedIssueShouldNotRaiseAnError(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.issue(1)
	_, err := e.Destroy(c.ctx, c.issue(1))
	c.must(err)
	before := c.count(`SELECT COUNT(*) FROM issues`)
	_, err = e.Destroy(c.ctx, iss)
	c.must(err)
	if c.count(`SELECT COUNT(*) FROM issues`) != before {
		t.Error("count changed")
	}
}

func TestIssueCDestroyingAStaleIssueShouldNotRaiseAnError(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.issue(1)
	fresh := c.issue(1)
	updateOKC(c, e, fresh, Params{"subject": "Updated"})
	before := c.count(`SELECT COUNT(*) FROM issues`)
	_, err := e.Destroy(c.ctx, iss)
	c.must(err)
	if c.count(`SELECT COUNT(*) FROM issues`) != before-1 {
		t.Error("should be destroyed")
	}
}

func TestIssueCBlocked(t *testing.T) {
	c := setup(t)
	e := c.env()
	if b, _ := e.Blocked(c.ctx, c.issue(9)); !b {
		t.Error("9 should be blocked")
	}
	if b, _ := e.Blocked(c.ctx, c.issue(10)); b {
		t.Error("10 should not be blocked")
	}
	// 不正な関連元 (FK があるため削除で代用)
	c.exec(`DELETE FROM issue_relations WHERE issue_from_id = 10 AND issue_to_id = 9 AND relation_type = 'blocks'`)
	if b, _ := e.Blocked(c.ctx, c.issue(9)); b {
		t.Error("9 should not be blocked")
	}
}

func hasClosedC(ss []*domain.IssueStatus) bool {
	return slices.ContainsFunc(ss, func(s *domain.IssueStatus) bool { return s.IsClosed })
}

func TestIssueCBlockedIssuesStatuses(t *testing.T) {
	c := setup(t)
	e := c.env()
	ss, err := e.NewStatusesAllowedTo(c.ctx, c.issue(9), c.user(2), false)
	c.must(err)
	if len(ss) == 0 || hasClosedC(ss) {
		t.Errorf("blocked: %v", statusIDs(ss))
	}
	ss, err = e.NewStatusesAllowedTo(c.ctx, c.issue(10), c.user(2), false)
	c.must(err)
	if len(ss) == 0 || !hasClosedC(ss) {
		t.Errorf("unblocked: %v", statusIDs(ss))
	}
}

func TestIssueCParentIssuesWithSubtaskStatuses(t *testing.T) {
	c := setup(t)
	e := c.env()
	parent := c.generateSaved(e, Params{})
	c.generateSaved(e, Params{"parent_issue_id": parent.ID})
	ss, err := e.NewStatusesAllowedTo(c.ctx, c.reload(e, parent), c.user(2), false)
	c.must(err)
	if ok, warn, _ := e.closable(c.ctx, parent); ok || warn != "notice_issue_not_closable_by_open_tasks" {
		t.Errorf("closable %v %q", ok, warn)
	}
	if parent.TransitionWarning != "notice_issue_not_closable_by_open_tasks" {
		t.Errorf("transition_warning = %q", parent.TransitionWarning)
	}
	if len(ss) == 0 || hasClosedC(ss) {
		t.Errorf("open subtask: %v", statusIDs(ss))
	}

	parent = c.generateSaved(e, Params{})
	c.generateSaved(e, Params{"parent_issue_id": parent.ID, "status_id": 5})
	ss, err = e.NewStatusesAllowedTo(c.ctx, c.reload(e, parent), c.user(2), false)
	c.must(err)
	if ok, _, _ := e.closable(c.ctx, parent); !ok || parent.TransitionWarning != "" {
		t.Error("should be closable")
	}
	if len(ss) == 0 || !hasClosedC(ss) {
		t.Errorf("closed subtask: %v", statusIDs(ss))
	}
}

func TestIssueCRescheduleOn(t *testing.T) {
	c := setup(t)
	for _, tt := range []struct {
		nw           []any
		start, due   string
		on           string
		wantS, wantD string
	}{
		{[]any{}, "", "", "2012-10-09", "2012-10-09", "2012-10-09"},
		{[]any{"6", "7"}, "", "", "2012-10-09", "2012-10-09", "2012-10-09"},
		{[]any{"6", "7"}, "", "", "2012-10-13", "2012-10-15", "2012-10-15"},
		{[]any{}, "2012-10-09", "", "2012-10-13", "2012-10-13", "2012-10-13"},
		{[]any{"6", "7"}, "2012-10-09", "", "2012-10-11", "2012-10-11", "2012-10-11"},
		{[]any{"6", "7"}, "2012-10-09", "", "2012-10-13", "2012-10-15", "2012-10-15"},
		{[]any{}, "2012-10-09", "2012-10-15", "2012-10-13", "2012-10-13", "2012-10-19"},
		{[]any{"6", "7"}, "2012-10-09", "2012-10-19", "2012-10-11", "2012-10-11", "2012-10-23"},
		{[]any{"6", "7"}, "2012-10-09", "2012-10-19", "2012-10-13", "2012-10-15", "2012-10-25"},
	} {
		c.setting("non_working_week_days", tt.nw)
		e := c.env()
		iss := c.newIssue(e, Params{"start_date": tt.start, "due_date": tt.due})
		e.RescheduleOn(iss, date(tt.on))
		if iss.StartDate == nil || !iss.StartDate.Equal(date(tt.wantS)) || iss.DueDate == nil || !iss.DueDate.Equal(date(tt.wantD)) {
			t.Errorf("%v %s-%s on %s: got %v %v", tt.nw, tt.start, tt.due, tt.on, iss.StartDate, iss.DueDate)
		}
	}
}

func eqDateC(t *testing.T, want string, got interface{ Format(string) string }, msg string) {
	t.Helper()
	if got.Format("2006-01-02") != want {
		t.Errorf("%s: want %s, got %s", msg, want, got.Format("2006-01-02"))
	}
}

func TestIssueCReschedulingToALaterDueDateShouldRescheduleFollowingIssue(t *testing.T) {
	c := setup(t)
	c.setting("non_working_week_days", []any{})
	e := c.env()
	i1 := c.generateSaved(e, Params{"start_date": "2012-10-15", "due_date": "2012-10-17"})
	i2 := c.generateSaved(e, Params{"start_date": "2012-10-15", "due_date": "2012-10-17"})
	c.addRelation(e, i1, i2, "precedes", nil)
	eqDateC(t, "2012-10-18", *c.reload(e, i2).StartDate, "i2 start")
	c.reload(e, i1)
	i1.SetDueDateString("2012-10-23")
	c.saveOK(e, i1)
	c.reload(e, i2)
	eqDateC(t, "2012-10-24", *i2.StartDate, "i2 start")
	eqDateC(t, "2012-10-26", *i2.DueDate, "i2 due")

	c.setting("non_working_week_days", []any{"6", "7"})
	e = c.env()
	i1 = c.generateSaved(e, Params{"start_date": "2014-03-10", "due_date": "2014-03-12"})
	i2 = c.generateSaved(e, Params{"start_date": "2014-03-10", "due_date": "2014-03-12"})
	c.addRelation(e, i1, i2, "precedes", intp(8))
	eqDateC(t, "2014-03-25", *c.reload(e, i2).StartDate, "delay")
}

func TestIssueCReschedulingToAnEarlierDueDateShouldRescheduleFollowingIssue(t *testing.T) {
	c := setup(t)
	e := c.env()
	i1 := c.generateSaved(e, Params{"start_date": "2012-10-15", "due_date": "2012-10-17"})
	i2 := c.generateSaved(e, Params{"start_date": "2012-10-15", "due_date": "2012-10-17"})
	c.addRelation(e, i1, i2, "precedes", nil)
	eqDateC(t, "2012-10-18", *c.reload(e, i2).StartDate, "i2 start")
	c.reload(e, i1)
	i1.SetStartDateString("2012-09-17")
	i1.SetDueDateString("2012-09-18")
	c.saveOK(e, i1)
	c.reload(e, i2)
	eqDateC(t, "2012-09-19", *i2.StartDate, "start")
	eqDateC(t, "2012-09-21", *i2.DueDate, "due")
}

func TestIssueCReschedulingShouldAddJournalToFollowingIssue(t *testing.T) {
	c := setup(t)
	c.setting("non_working_week_days", []any{})
	e := c.env()
	i1 := c.generateSaved(e, Params{"start_date": "2012-10-15", "due_date": "2012-10-17"})
	i2 := c.generateSaved(e, Params{"start_date": "2012-10-18", "due_date": "2012-10-20"})
	c.addRelation(e, i1, i2, "precedes", nil)
	before := len(journals(c, i2.ID))
	c.reload(e, i1)
	_, err := e.InitJournal(c.ctx, i1, c.user(3), "")
	c.must(err)
	i1.SetDueDateString("2012-10-23")
	c.saveOK(e, i1)
	js := journals(c, i2.ID)
	if len(js) != before+1 {
		t.Fatalf("journals %d -> %d", before, len(js))
	}
	j := js[len(js)-1]
	sd, dd := j.DetailForAttribute("start_date"), j.DetailForAttribute("due_date")
	if sd == nil || *sd.OldValue != "2012-10-18" || *sd.Value != "2012-10-24" {
		t.Errorf("start_date detail %+v", sd)
	}
	if dd == nil || *dd.OldValue != "2012-10-20" || *dd.Value != "2012-10-26" {
		t.Errorf("due_date detail %+v", dd)
	}
}

func TestIssueCReschedulingEarlierShouldConsiderOtherPrecedingIssues(t *testing.T) {
	c := setup(t)
	e := c.env()
	i1 := c.generateSaved(e, Params{"start_date": "2012-10-15", "due_date": "2012-10-17"})
	i2 := c.generateSaved(e, Params{"start_date": "2012-10-15", "due_date": "2012-10-17"})
	i3 := c.generateSaved(e, Params{"start_date": "2012-10-01", "due_date": "2012-10-02"})
	c.addRelation(e, i1, i2, "precedes", nil)
	c.addRelation(e, i3, i2, "precedes", nil)
	eqDateC(t, "2012-10-18", *c.reload(e, i2).StartDate, "i2 start")
	c.reload(e, i1)
	i1.SetStartDateString("2012-09-17")
	i1.SetDueDateString("2012-09-18")
	c.saveOK(e, i1)
	c.reload(e, i2)
	eqDateC(t, "2012-10-03", *i2.StartDate, "start")
	eqDateC(t, "2012-10-05", *i2.DueDate, "due")
}

func TestIssueCReschedulingAStaleIssueShouldNotRaiseAnError(t *testing.T) {
	c := setup(t)
	c.setting("non_working_week_days", []any{})
	e := c.env()
	stale := c.issue(1)
	iss := c.issue(1)
	iss.Subject = "Updated"
	c.saveOK(e, iss)
	d := daysFromNow(10)
	_, err := e.RescheduleOnAndSave(c.ctx, stale, d)
	c.must(err)
	if got := c.issue(1).StartDate; got == nil || !got.Equal(d) {
		t.Errorf("start_date = %v", got)
	}
}

func TestIssueCChildIssueShouldConsiderParentSoonestStartOnCreate(t *testing.T) {
	c := setup(t)
	e := c.env()
	i1 := c.generateSaved(e, Params{"start_date": "2012-10-15", "due_date": "2012-10-17"})
	i2 := c.generateSaved(e, Params{"start_date": "2012-10-18", "due_date": "2012-10-20"})
	c.addRelation(e, i1, i2, "precedes", nil)
	c.reload(e, i2)
	eqDateC(t, "2012-10-18", *i2.StartDate, "i2 start")
	e.DateFormat = func(t2 time.Time) string { return t2.Format("01/02/2006") }
	child := c.newIssue(e, Params{"parent_issue_id": i2.ID, "start_date": "2012-10-16", "project_id": 1, "tracker_id": 1,
		"status_id": 1, "subject": "Child", "author_id": 1})
	if c.valid(e, child) {
		t.Fatal("should not be valid")
	}
	if len(child.Errors.List) == 0 || child.Errors.On("start_date")[0] != "earlier_than_minimum_start_date" ||
		child.Errors.List[0].Vars["date"] != "10/18/2012" {
		t.Errorf("errors = %+v", child.Errors.List)
	}
	ss, err := e.SoonestStart(c.ctx, child)
	c.must(err)
	eqDateC(t, "2012-10-18", *ss, "soonest_start")
	child.SetStartDateString("2012-10-18")
	if !c.save(e, child) {
		t.Errorf("save: %v", child.Errors.List)
	}
}

func TestIssueCSettingParentToPrecedingOrFollowingShouldNotValidate(t *testing.T) {
	c := setup(t)
	e := c.env()
	i1 := c.generateSaved(e, Params{})
	i2 := c.generateSaved(e, Params{})
	i3 := c.generateSaved(e, Params{})
	c.addRelation(e, i1, i2, "precedes", nil)
	c.addRelation(e, i2, i3, "precedes", nil)
	c.reload(e, i3)
	c.must(e.SetParentIssueID(c.ctx, i3, itoa(i1.ID)))
	if c.valid(e, i3) || !hasError(i3, "parent_issue_id", "invalid") {
		t.Errorf("3 parent 1: %v", i3.Errors.List)
	}
	c.reload(e, i1)
	c.must(e.SetParentIssueID(c.ctx, i1, itoa(i3.ID)))
	if c.valid(e, i1) || !hasError(i1, "parent_issue_id", "invalid") {
		t.Errorf("1 parent 3: %v", i1.Errors.List)
	}
}

func TestIssueCSettingParentToPrecedingThroughHierarchyShouldNotValidate(t *testing.T) {
	c := setup(t)
	e := c.env()
	i1 := c.generateSaved(e, Params{})
	i2 := c.generateSaved(e, Params{})
	c.addRelation(e, i1, i2, "precedes", nil)
	i3 := c.generateSaved(e, Params{})
	c.reload(e, i2)
	c.must(e.SetParentIssueID(c.ctx, i2, itoa(i3.ID)))
	c.saveOK(e, i2)
	i4 := c.generateSaved(e, Params{})
	c.addRelation(e, c.reload(e, i3), i4, "precedes", nil)
	c.reload(e, i4)
	c.must(e.SetParentIssueID(c.ctx, i4, itoa(i1.ID)))
	if c.valid(e, i4) || !hasError(i4, "parent_issue_id", "invalid") {
		t.Errorf("errors = %v", i4.Errors.List)
	}
}

func TestIssueCRelatedIssuesShouldBeAbleToBeMovedToTheSameParent(t *testing.T) {
	for _, typ := range []string{"follows", "precedes", "blocked", "blocks"} {
		t.Run(typ, func(t *testing.T) {
			c := setup(t)
			e := c.env()
			i1 := c.generateSaved(e, Params{})
			i2 := c.generateSaved(e, Params{})
			r := c.addRelation(e, i2, i1, typ, nil)
			parent := c.generateSaved(e, Params{})
			c.reload(e, i1)
			c.must(e.SetParentIssueID(c.ctx, i1, itoa(parent.ID)))
			if !c.save(e, i1) {
				t.Fatalf("i1: %v", i1.Errors.List)
			}
			c.reload(e, i2)
			c.must(e.SetParentIssueID(c.ctx, i2, itoa(parent.ID)))
			if !c.save(e, i2) {
				t.Fatalf("i2: %v", i2.Errors.List)
			}
			if c.count(`SELECT COUNT(*) FROM issue_relations WHERE id = ?`, r.ID) != 1 {
				t.Error("relation should exist")
			}
		})
	}
}

func TestIssueCCopyShouldBeAbleToBeMovedToTheSameParentAsCopiedIssue(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.generateSaved(e, Params{})
	parent := c.generateSaved(e, Params{})
	c.must(e.SetParentIssueID(c.ctx, iss, itoa(parent.ID)))
	c.saveOK(e, iss)
	c.reload(e, iss)
	cp := c.copyFromC(e, iss, CopyOptions{})
	c.saveOK(e, cp)
	rid := c.ids(`SELECT id FROM issue_relations ORDER BY id DESC LIMIT 1`)[0]
	c.reload(e, cp)
	c.must(e.SetParentIssueID(c.ctx, cp, itoa(parent.ID)))
	if !c.save(e, cp) {
		t.Fatalf("errors = %v", cp.Errors.List)
	}
	if c.count(`SELECT COUNT(*) FROM issue_relations WHERE id = ?`, rid) != 1 {
		t.Error("relation should exist")
	}
}
