package issues

// test/unit/issue_test.rb の移植 (後半: test_overdue 以降)。
//
// 移植済み:
//   test_overdue, #behind_schedule? (5 件), #assignable_users should be Users / include the issue author,
//   test_assignable_users_should_not_include_anonymous_user / _locked_user / include_the_current_assignee,
//   #assignable_users should not show the issue author twice / with(out) issue_group_assignment,
//   test_assignable_users_should_not_include_builtin_groups / _users_that_cannot_view_the_tracker,
//   test_create_should_send_email_notification / _one_email_notification_with_both_settings /
//   _not_send_email_notification_with_no_setting, test_update_should_notify_previous_assignee,
//   test_stale_issue_should_not_send_email_notification, test_journalized_description,
//   test_blank_descriptions_should_not_be_journalized, test_journalized_multi_custom_field,
//   test_custom_value_cleared_on_tracker_change_should_be_journalized, test_description_eol_should_be_normalized,
//   test_saving_twice_should_not_duplicate_journal_details, #done_ratio, #update_done_ratio_from_issue_status,
//   Issue#recipients (8 件), Authors who don't want to be self-notified ...,
//   test_last_journal_id_*, test_journals_after_*, test_css_classes_* (4 件), test_closed_on_* (6 件),
//   test_prior_assigned_to (prior_assigned_to はジャーナル詳細から直接確認), test_status_was_* (4 件),
//   test_closing_* (5 件), test_reopening_* (4 件), test_default_status_*, test_initializing_with_tracker*,
//   test_setting_tracker_should_set_default_status, test_changing_tracker_* (3 件), test_previous_assignee_with_a_group,
//   test_closable, test_reopenable, test_filter_projects_scope, test_create_should_add_watcher /
//   _add_author_watcher_only_once / _not_add_watcher, test_create_should_not_add_anonymous_as_watcher,
//   test_attachments_editable_should_check_issue_visibility / _deletable_
//
// 未移植 (理由):
//   #by_tracker / by_version / by_priority / by_category / by_assigned_to / by_author / by_subproject
//     (count_and_group_by はクエリ層の集計でチケットサービスの範囲外)
//   test_recently_updated_scope, test_on_active_projects_scope, test_like_* (スコープ = クエリ層)
//   test_save_attachments_with_hash_* / _with_array_* (アップロード処理は添付ファイル層。ここは保存済み添付の紐付けのみ)
//   test_issue_overdue_should_respect_user_timezone (タイムゾーンは UTC 固定。ユーザ別 today は未対応)
//   test_change_of_project_parent_should_update_shared_versions_with_derived_priorities
//     (Issue.update_versions_from_hierarchy_change はプロジェクト移動側の処理で未実装)

import (
	"slices"
	"testing"

	"github.com/mikuta0407/buropher/internal/testfixtures"
)

func TestIssueDOverdue(t *testing.T) {
	c := setup(t)
	e := c.env()
	check := func(due *string, status int64, want bool) {
		t.Helper()
		iss := c.newIssue(e, Params{})
		if due != nil {
			iss.SetDueDateString(*due)
		}
		iss.StatusID = status
		got, err := e.Overdue(c.ctx, iss)
		c.must(err)
		if got != want {
			t.Errorf("due=%v status=%d: overdue=%v", due, status, got)
		}
	}
	f := func(n int) *string { s := daysFromNow(n).Format("2006-01-02"); return &s }
	check(f(-1), 0, true)
	check(f(0), 0, false)
	check(f(1), 0, false)
	check(nil, 0, false)
	closed := c.ids(`SELECT id FROM issue_statuses WHERE is_closed = ? ORDER BY position LIMIT 1`, true)[0]
	check(f(-1), closed, false)
}

func TestIssueDBehindSchedule(t *testing.T) {
	c := setup(t)
	e := c.env()
	f := func(n int) string { return daysFromNow(n).Format("2006-01-02") }
	cases := []struct {
		start, due string
		ratio      int
		want       bool
	}{
		{"", f(1), 0, false},
		{f(1), "", 0, false},
		{f(-50), f(50), 90, false},
		{f(-1), f(1), 0, true},
		{f(-100), f(0), 90, true},
	}
	for _, x := range cases {
		iss := c.newIssue(e, Params{"start_date": x.start, "due_date": x.due, "done_ratio": x.ratio})
		got, err := e.BehindSchedule(c.ctx, iss)
		c.must(err)
		if got != x.want {
			t.Errorf("%+v: got %v", x, got)
		}
	}
}

func assignableIDs(c *tc, e *Env, iss *Issue) []int64 {
	c.t.Helper()
	us, err := e.AssignableUsers(c.ctx, iss)
	c.must(err)
	return principalIDs(us)
}

func TestIssueDAssignableUsers(t *testing.T) {
	c := setup(t)
	e := c.env()
	// should be Users
	us, err := e.AssignableUsers(c.ctx, c.issue(1))
	c.must(err)
	if len(us) == 0 || !us[0].IsUser() {
		t.Error("first should be a user")
	}
	// should include the issue author
	nm := c.generateUser(testfixtures.UserAttrs{})
	iss := c.generateSaved(e, Params{"author_id": nm})
	if !slices.Contains(assignableIDs(c, e, iss), nm) {
		t.Error("author not included")
	}
	// should not include anonymous
	anon := c.ids(`SELECT id FROM principals WHERE kind = 'anonymous_user'`)[0]
	iss = c.generateSaved(e, Params{"author_id": anon})
	if slices.Contains(assignableIDs(c, e, iss), anon) {
		t.Error("anonymous included")
	}
	// should not include locked user
	u := c.generateUser(testfixtures.UserAttrs{})
	iss = c.generateSaved(e, Params{"author_id": u})
	c.lockUser(u)
	e = c.env()
	if slices.Contains(assignableIDs(c, e, iss), u) {
		t.Error("locked author included")
	}
	// should include the current assignee
	u2 := c.generateUser(testfixtures.UserAttrs{})
	c.addMember(u2, 1, 1)
	e = c.env()
	iss = c.generateSaved(e, Params{"assigned_to_id": u2})
	c.lockUser(u2)
	e = c.env()
	if !slices.Contains(assignableIDs(c, e, c.issue(iss.ID)), u2) {
		t.Error("current assignee not included")
	}
}

func TestIssueDAssignableUsersShouldNotShowAuthorTwice(t *testing.T) {
	c := setup(t)
	ids := assignableIDs(c, c.env(), c.issue(1))
	if len(ids) != 2 {
		t.Errorf("ids = %v", ids)
	}
}

func TestIssueDAssignableUsersGroupAssignment(t *testing.T) {
	c := setup(t)
	kinds := func() ([]string, []int64) {
		e := c.env()
		iss := c.newIssue(e, Params{"project_id": 2})
		us, err := e.AssignableUsers(c.ctx, iss)
		c.must(err)
		var ks []string
		for _, u := range us {
			k := principalClass(u)
			if !slices.Contains(ks, k) {
				ks = append(ks, k)
			}
		}
		slices.Sort(ks)
		return ks, principalIDs(us)
	}
	c.setting("issue_group_assignment", "1")
	ks, ids := kinds()
	if !slices.Equal(ks, []string{"Group", "User"}) || !slices.Contains(ids, 11) {
		t.Errorf("with: %v %v", ks, ids)
	}
	c.setting("issue_group_assignment", "0")
	ks, ids = kinds()
	if !slices.Equal(ks, []string{"User"}) || slices.Contains(ids, 11) {
		t.Errorf("without: %v %v", ks, ids)
	}
}

func TestIssueDAssignableUsersShouldNotIncludeBuiltinGroups(t *testing.T) {
	c := setup(t)
	for _, k := range []string{"group_non_member", "group_anonymous"} {
		c.addMember(c.ids(`SELECT id FROM principals WHERE kind = ?`, k)[0], 1, 1)
	}
	c.setting("issue_group_assignment", "1")
	e := c.env()
	us, err := e.AssignableUsers(c.ctx, c.newIssue(e, Params{"project_id": 1}))
	c.must(err)
	for _, u := range us {
		if u.Kind.IsBuiltinGroup() {
			t.Errorf("builtin group %d included", u.ID)
		}
	}
}

func TestIssueDAssignableUsersShouldNotIncludeUsersThatCannotViewTheTracker(t *testing.T) {
	c := setup(t)
	c.setPermissionTrackers(2, "view_issues", []int64{1, 3})
	e := c.env()
	if !slices.Contains(assignableIDs(c, e, c.newIssue(e, Params{"project_id": 1, "tracker_id": 1})), 3) {
		t.Error("issue1 should include user 3")
	}
	if slices.Contains(assignableIDs(c, e, c.newIssue(e, Params{"project_id": 1, "tracker_id": 2})), 3) {
		t.Error("issue2 should not include user 3")
	}
}

func TestIssueDCreateEmailNotification(t *testing.T) {
	for _, x := range []struct {
		events []any
		want   int
	}{{[]any{"issue_added"}, 2}, {[]any{"issue_added", "issue_updated"}, 2}, {[]any{}, 0}} {
		c := setup(t)
		c.setting("notified_events", x.events)
		e := c.env()
		ps, _ := e.Priorities(c.ctx)
		iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "author_id": 3, "status_id": 1, "priority_id": ps[0].ID,
			"subject": "test_create", "estimated_hours": "1:30"})
		res := c.saveOK(e, iss)
		if n := deliveries(res); n != x.want {
			t.Errorf("%v: deliveries = %d (%v)", x.events, n, res.Notifications)
		}
	}
}

func TestIssueDUpdateShouldNotifyPreviousAssignee(t *testing.T) {
	c := setup(t)
	c.exec(`DELETE FROM user_notified_projects WHERE user_id = 3`)
	c.setMailNotification(3, "only_assigned")
	c.setting("notified_events", []any{"issue_updated"})
	e := c.env()
	iss := c.issue(2)
	_, err := e.InitJournal(c.ctx, iss, c.user(1), "")
	c.must(err)
	iss.AssignedToID = nil
	res := c.saveOK(e, iss)
	if !deliveredTo(res, 3) {
		t.Errorf("notifications = %v", res.Notifications)
	}
}

func TestIssueDStaleIssueShouldNotSendEmailNotification(t *testing.T) {
	c := setup(t)
	c.setting("notified_events", []any{"issue_updated"})
	e := c.env()
	iss := c.issue(1)
	stale := c.issue(1)
	_, err := e.InitJournal(c.ctx, iss, c.user(1), "")
	c.must(err)
	iss.Subject = "Subjet update"
	res := c.saveOK(e, iss)
	if n := deliveries(res); n != 2 {
		t.Errorf("deliveries = %d (%v)", n, res.Notifications)
	}
	_, err = e.InitJournal(c.ctx, stale, c.user(1), "")
	c.must(err)
	stale.Subject = "Another subjet update"
	_, res2, err := e.Save(c.ctx, stale)
	if err != ErrStale {
		t.Fatalf("err = %v", err)
	}
	if res2 != nil && deliveries(res2) != 0 {
		t.Error("stale should not notify")
	}
}

func TestIssueDJournalizedDescription(t *testing.T) {
	c := setup(t)
	c.exec(`DELETE FROM custom_fields WHERE owner_kind = 'issue'`)
	e := c.env()
	i := c.issue(1)
	old := i.Description
	_, err := e.InitJournal(c.ctx, i, c.user(2), "")
	c.must(err)
	i.SetDescription(sp("This is the new description"))
	nj, nd := c.count(`SELECT COUNT(*) FROM issue_journals`), c.count(`SELECT COUNT(*) FROM issue_journal_details`)
	c.saveOK(e, i)
	if c.count(`SELECT COUNT(*) FROM issue_journals`) != nj+1 || c.count(`SELECT COUNT(*) FROM issue_journal_details`) != nd+1 {
		t.Fatal("expected 1 journal / 1 detail")
	}
	j := lastJournal(c, 1)
	d := j.Details[len(j.Details)-1]
	if d.Property != "attr" || d.PropKey != "description" || !eqPtr(d.OldValue, old) || *d.Value != "This is the new description" {
		t.Errorf("detail = %+v", d)
	}
}

func TestIssueDBlankDescriptionsShouldNotBeJournalized(t *testing.T) {
	c := setup(t)
	c.exec(`DELETE FROM custom_fields WHERE owner_kind = 'issue'`)
	c.exec(`UPDATE issues SET description = NULL WHERE id = 1`)
	e := c.env()
	i := c.issue(1)
	_, err := e.InitJournal(c.ctx, i, c.user(2), "")
	c.must(err)
	i.Subject = "blank description"
	i.SetDescription(sp("\r\n"))
	nj, nd := c.count(`SELECT COUNT(*) FROM issue_journals`), c.count(`SELECT COUNT(*) FROM issue_journal_details`)
	c.saveOK(e, i)
	if c.count(`SELECT COUNT(*) FROM issue_journals`) != nj+1 || c.count(`SELECT COUNT(*) FROM issue_journal_details`) != nd+1 {
		t.Error("expected 1 journal / 1 detail")
	}
}

func TestIssueDJournalizedMultiCustomField(t *testing.T) {
	c := setup(t)
	fid := c.createCF(cfAttrs{Name: "filter", Format: "list", IsForAll: true, Trackers: []int64{1},
		PossibleValues: []string{"value1", "value2", "value3"}, Multiple: true})
	e := c.env()
	iss := c.generateSaved(e, Params{"project_id": 1, "tracker_id": 1, "subject": "Test", "author_id": 1})
	nj := c.count(`SELECT COUNT(*) FROM issue_journals`)
	step := func(v any, want int) {
		t.Helper()
		nd := c.count(`SELECT COUNT(*) FROM issue_journal_details`)
		_, err := e.InitJournal(c.ctx, iss, c.user(1), "")
		c.must(err)
		c.must(e.SetCustomFieldValues(c.ctx, iss, map[string]any{itoa(fid): v}))
		c.saveOK(e, iss)
		if got := c.count(`SELECT COUNT(*) FROM issue_journal_details`) - nd; got != want {
			t.Errorf("%v: details +%d, want %d", v, got, want)
		}
	}
	step([]string{"value1"}, 1)
	step([]string{"value1", "value2"}, 1)
	step([]string{"value3", "value2"}, 2)
	step(nil, 2)
	if got := c.count(`SELECT COUNT(*) FROM issue_journals`) - nj; got != 1 {
		t.Errorf("journals +%d, want 1", got)
	}
}

func TestIssueDCustomValueClearedOnTrackerChangeShouldBeJournalized(t *testing.T) {
	c := setup(t)
	a := c.createCF(cfAttrs{IsForAll: true, Trackers: []int64{1}})
	e := c.env()
	iss := c.generateSaved(e, Params{"project_id": 1, "tracker_id": 1, "custom_field_values": map[string]any{itoa(a): "foo"}})
	if v := cfVal(c, e, iss, a); v.String() != "foo" {
		t.Fatalf("value = %q", v.String())
	}
	_, err := e.InitJournal(c.ctx, iss, c.user(1), "")
	c.must(err)
	c.must(e.SetTrackerID(c.ctx, iss, 2))
	c.saveOK(e, iss)
	j := lastJournal(c, iss.ID)
	var ds []string
	for _, d := range j.Details {
		if d.Property == "cf" && d.PropKey == itoa(a) {
			if d.OldValue == nil || *d.OldValue != "foo" || d.Value != nil {
				t.Errorf("detail = %+v", d)
			}
			ds = append(ds, d.PropKey)
		}
	}
	if len(ds) != 1 {
		t.Errorf("cf details = %d", len(ds))
	}
}

func TestIssueDDescriptionEOLShouldBeNormalized(t *testing.T) {
	c := setup(t)
	i := c.newIssue(c.env(), Params{"description": "CR \r LF \n CRLF \r\n"})
	if *i.Description != "CR \r\n LF \r\n CRLF \r\n" {
		t.Errorf("%q", *i.Description)
	}
}

func TestIssueDSavingTwiceShouldNotDuplicateJournalDetails(t *testing.T) {
	c := setup(t)
	e := c.env()
	i := c.issue(1)
	_, err := e.InitJournal(c.ctx, i, c.user(2), "Some notes")
	c.must(err)
	i.Subject = "New subject"
	i.DoneRatio += 10
	nj := c.count(`SELECT COUNT(*) FROM issue_journals`)
	c.saveOK(e, i)
	if c.count(`SELECT COUNT(*) FROM issue_journals`) != nj+1 {
		t.Fatal("journal not created")
	}
	i.PriorityID = c.ids(`SELECT id FROM issue_priorities WHERE id <> ? ORDER BY position, id LIMIT 1`, i.PriorityID)[0]
	nd := c.count(`SELECT COUNT(*) FROM issue_journal_details`)
	c.saveOK(e, i)
	if c.count(`SELECT COUNT(*) FROM issue_journals`) != nj+1 || c.count(`SELECT COUNT(*) FROM issue_journal_details`) != nd+1 {
		t.Error("second save should add 1 detail to the same journal")
	}
	c.saveOK(e, i)
	if c.count(`SELECT COUNT(*) FROM issue_journals`) != nj+1 || c.count(`SELECT COUNT(*) FROM issue_journal_details`) != nd+1 {
		t.Error("third save should not add anything")
	}
}

func TestIssueDDoneRatio(t *testing.T) {
	c := setup(t)
	c.exec(`UPDATE issue_statuses SET default_done_ratio = 50 WHERE id = 1`)
	c.exec(`UPDATE issue_statuses SET default_done_ratio = 0 WHERE id = 2`)
	e := c.env()
	i1, i2 := c.issue(1), c.issue(2)
	dr := func(i *Issue) int { v, err := e.DoneRatio(c.ctx, i); c.must(err); return v }
	c.setting("issue_done_ratio", "issue_field")
	if dr(i1) != 0 || dr(i2) != 30 {
		t.Errorf("field: %d %d", dr(i1), dr(i2))
	}
	c.setting("issue_done_ratio", "issue_status")
	if dr(i1) != 50 || dr(i2) != 0 {
		t.Errorf("status: %d %d", dr(i1), dr(i2))
	}
}

func TestIssueDUpdateDoneRatioFromIssueStatus(t *testing.T) {
	c := setup(t)
	c.exec(`UPDATE issue_statuses SET default_done_ratio = 50 WHERE id = 1`)
	c.exec(`UPDATE issue_statuses SET default_done_ratio = 0 WHERE id = 2`)
	e := c.env()
	// update_done_ratio_from_issue_status は保存時 (before_save) にのみ走るため、保存して列の値を確認する
	c.setting("issue_done_ratio", "issue_field")
	for _, id := range []int64{1, 2} {
		i := c.issue(id)
		i.Subject += "!"
		c.saveOK(e, i)
	}
	if c.issue(1).DoneRatio != 0 || c.issue(2).DoneRatio != 30 {
		t.Errorf("field: %d %d", c.issue(1).DoneRatio, c.issue(2).DoneRatio)
	}
	c.setting("issue_done_ratio", "issue_status")
	for _, id := range []int64{1, 2} {
		i := c.issue(id)
		i.Subject += "!"
		c.saveOK(e, i)
	}
	if c.issue(1).DoneRatio != 50 || c.issue(2).DoneRatio != 0 {
		t.Errorf("status: %d %d", c.issue(1).DoneRatio, c.issue(2).DoneRatio)
	}
}

func TestIssueDRecipients(t *testing.T) {
	c := setup(t)
	e := c.env()
	// should include project recipients
	iss := c.generateSaved(e, Params{})
	proj := c.ids(`SELECT m.principal_id FROM members m JOIN principals p ON p.id = m.principal_id
LEFT JOIN user_notification_settings ns ON ns.user_id = p.id
WHERE m.project_id = 1 AND p.kind = 'user' AND p.status = 1 AND (ns.mail_notification = 'all'
 OR EXISTS (SELECT 1 FROM user_notified_projects u WHERE u.user_id = p.id AND u.project_id = 1))`)
	if len(proj) == 0 {
		t.Fatal("no project recipients")
	}
	got := notifiedIDs(c, e, iss)
	for _, id := range proj {
		if !slices.Contains(got, id) {
			t.Errorf("project recipient %d missing", id)
		}
	}
	// author active
	u := c.generateUser(testfixtures.UserAttrs{})
	c.setMailNotification(u, "only_my_events")
	iss = c.generateSaved(e, Params{"author_id": u})
	if !slices.Contains(notifiedIDs(c, e, iss), u) {
		t.Error("author missing")
	}
	// assignee active
	u = c.generateUser(testfixtures.UserAttrs{})
	c.addMember(u, 1, 1)
	c.setMailNotification(u, "only_my_events")
	e = c.env()
	iss = c.generateSaved(e, Params{"assigned_to_id": u})
	if !slices.Contains(notifiedIDs(c, e, iss), u) {
		t.Error("assignee missing")
	}
	// opt out of all email
	u = c.generateUser(testfixtures.UserAttrs{})
	iss = c.generateSaved(e, Params{"author_id": u})
	c.setMailNotification(u, "none")
	if slices.Contains(notifiedIDs(c, e, iss), u) {
		t.Error("none included")
	}
	// only_assigned author
	c.setMailNotification(u, "only_assigned")
	if slices.Contains(notifiedIDs(c, e, iss), u) {
		t.Error("only_assigned author included")
	}
	// only_owner assignee
	u = c.generateUser(testfixtures.UserAttrs{})
	c.addMember(u, 1, 1)
	e = c.env()
	iss = c.generateSaved(e, Params{"assigned_to_id": u})
	c.setMailNotification(u, "only_owner")
	if slices.Contains(notifiedIDs(c, e, iss), u) {
		t.Error("only_owner assignee included")
	}
}

func TestIssueDRecipientsHighPriority(t *testing.T) {
	c := setup(t)
	u := c.generateUser(testfixtures.UserAttrs{})
	c.setNotifyFlag(u, "notify_about_high_priority_issues", true)
	c.addMember(u, 1, 1)
	e := c.env()
	iss := c.generateSaved(e, Params{"priority_id": 6})
	if !slices.Contains(notifiedIDs(c, e, iss), u) {
		t.Error("high: missing")
	}
	iss = c.generateSaved(e, Params{})
	if slices.Contains(notifiedIDs(c, e, iss), u) {
		t.Error("default: included")
	}
	iss.PriorityID = 6
	c.saveOK(e, iss)
	if !slices.Contains(notifiedIDs(c, e, iss), u) {
		t.Error("update high: missing")
	}
	iss.PriorityID = 4
	c.saveOK(e, iss)
	if slices.Contains(notifiedIDs(c, e, iss), u) {
		t.Error("update low: included")
	}
}

func TestIssueDAuthorsNoSelfNotifiedHighPriority(t *testing.T) {
	c := setup(t)
	u := c.generateUser(testfixtures.UserAttrs{})
	c.setNotifyFlag(u, "notify_about_high_priority_issues", true)
	c.setNotifyFlag(u, "no_self_notified", true)
	c.exec(`DELETE FROM members WHERE project_id = 1`)
	c.addMember(u, 1, 1)
	e := c.env()
	iss := c.newIssue(e, Params{"author_id": u, "priority_id": 6, "subject": "test create", "project_id": 1, "tracker_id": 1, "status_id": 1})
	res := c.saveOK(e, iss)
	if deliveries(res) != 0 {
		t.Errorf("notifications = %v", res.Notifications)
	}
}

func TestIssueDLastJournalIDAndJournalsAfter(t *testing.T) {
	c := setup(t)
	e := c.env()
	if id, _ := e.LastJournalID(c.ctx, c.issue(1)); id != 2 {
		t.Errorf("last_journal_id(1) = %d", id)
	}
	if id, _ := e.LastJournalID(c.ctx, c.issue(3)); id != 0 {
		t.Errorf("last_journal_id(3) = %d", id)
	}
	ids := func(js []*Journal) []int64 {
		var out []int64
		for _, j := range js {
			out = append(out, j.ID)
		}
		return out
	}
	js, _ := e.JournalsAfter(c.ctx, c.issue(1), 1)
	eqIDs(t, []int64{2}, ids(js), "after 1")
	js, _ = e.JournalsAfter(c.ctx, c.issue(1), 2)
	eqIDs(t, nil, ids(js), "after 2")
	js, _ = e.JournalsAfter(c.ctx, c.issue(1), 0)
	eqIDs(t, []int64{1, 2}, ids(js), "after blank")
}

func cssHas(c *tc, e *Env, iss *Issue, uid int64, class string) bool {
	c.t.Helper()
	var u = c.user(1)
	if uid != 0 {
		u = c.user(uid)
	} else {
		u = nil
	}
	s, err := e.CSSClasses(c.ctx, iss, u)
	c.must(err)
	return slices.Contains(splitSpaces(s), class)
}

func splitSpaces(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ' ' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func TestIssueDCSSClasses(t *testing.T) {
	c := setup(t)
	e := c.env()
	if !cssHas(c, e, c.newIssue(e, Params{"tracker_id": 2}), 0, "tracker-2") {
		t.Error("tracker-2")
	}
	i := c.newIssue(e, Params{"priority_id": 8})
	if !cssHas(c, e, i, 0, "priority-8") || !cssHas(c, e, i, 0, "priority-highest") {
		t.Error("priority")
	}
	// user and group assignment
	u := c.generateUser(testfixtures.UserAttrs{})
	g := c.generateGroup()
	c.addMember(g, 1, 1, 2)
	c.must(repository_AddUserToGroup(c, g, u))
	e = c.env()
	i1 := c.generate(e, Params{"assigned_to_id": g})
	if !cssHas(c, e, i1, u, "assigned-to-my-group") || cssHas(c, e, i1, u, "assigned-to-me") {
		t.Error("group assignment")
	}
	i2 := c.generate(e, Params{"assigned_to_id": u})
	if cssHas(c, e, i2, u, "assigned-to-my-group") || !cssHas(c, e, i2, u, "assigned-to-me") {
		t.Error("user assignment")
	}
	// behind schedule
	if !cssHas(c, e, c.issue(1), 0, "behind-schedule") || cssHas(c, e, c.issue(2), 0, "behind-schedule") {
		t.Error("behind-schedule")
	}
}

func TestIssueDClosedOn(t *testing.T) {
	c := setup(t)
	e := c.env()
	// creating an open issue
	iss := c.generateSaved(e, Params{"status_id": 1})
	if cl, _ := e.Closed(c.ctx, iss); cl || iss.ClosedAt != nil {
		t.Error("open create")
	}
	// creating a closed issue
	iss = c.generateSaved(e, Params{"status_id": 5})
	if cl, _ := e.Closed(c.ctx, iss); !cl || iss.ClosedAt == nil || !iss.ClosedAt.Equal(iss.UpdatedAt) || !iss.ClosedAt.Equal(iss.CreatedAt) {
		t.Errorf("closed create: %v %v %v", iss.ClosedAt, iss.UpdatedAt, iss.CreatedAt)
	}
	// updating an open issue
	iss = c.issue(1)
	iss.Subject = "Not closed yet"
	c.saveOK(e, iss)
	if c.issue(1).ClosedAt != nil {
		t.Error("open update")
	}
	// closing an open issue
	iss = c.issue(1)
	iss.Subject = "Now closed"
	c.must(e.SetStatusID(c.ctx, iss, 5))
	c.saveOK(e, iss)
	iss = c.issue(1)
	if iss.ClosedAt == nil || !iss.ClosedAt.Equal(iss.UpdatedAt) {
		t.Error("closing")
	}
}

func TestIssueDClosedOnClosedIssue(t *testing.T) {
	c := setup(t)
	e := c.env()
	first := func() *Issue {
		return c.issue(c.ids(`SELECT issues.id FROM issues JOIN issue_statuses s ON s.id = issues.status_id WHERE s.is_closed = ? ORDER BY issues.id LIMIT 1`, true)[0])
	}
	iss := first()
	was := iss.ClosedAt
	if was == nil {
		t.Fatal("closed_on should be set")
	}
	iss.Subject = "Updating a closed issue"
	c.saveOK(e, iss)
	if got := c.issue(iss.ID).ClosedAt; got == nil || !got.Equal(*was) {
		t.Error("closed update changed closed_on")
	}
	iss = c.issue(iss.ID)
	iss.Subject = "Reopening a closed issue"
	c.must(e.SetStatusID(c.ctx, iss, 1))
	c.saveOK(e, iss)
	iss = c.issue(iss.ID)
	if cl, _ := e.Closed(c.ctx, iss); cl || iss.ClosedAt == nil || !iss.ClosedAt.Equal(*was) {
		t.Error("reopen should preserve closed_on")
	}
}

func TestIssueDPriorAssignedTo(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.generateSaved(e, Params{"assigned_to_id": 2})
	_, err := e.InitJournal(c.ctx, iss, c.user(2), "update")
	c.must(err)
	iss.AssignedToID = ptrInt64(3)
	c.saveOK(e, iss)
	// prior_assigned_to: 担当者変更の詳細のうち old_value が nil でない最新のもの
	var old string
	c.must(c.d.Get(c.ctx, &old, `SELECT d.old_value FROM issue_journal_details d JOIN issue_journals j ON j.id = d.journal_id
WHERE j.issue_id = ? AND d.prop_key = 'assigned_to_id' AND d.old_value IS NOT NULL ORDER BY j.id DESC LIMIT 1`, iss.ID))
	if old != "2" {
		t.Errorf("prior = %q", old)
	}
}

func TestIssueDStatusWas(t *testing.T) {
	c := setup(t)
	e := c.env()
	n, _ := e.NewBlank(c.ctx)
	if s, _ := e.StatusWas(c.ctx, n); s != nil {
		t.Error("new: status_was should be nil")
	}
	iss := c.issue(1)
	c.must(e.SetStatusID(c.ctx, iss, 2))
	if s, _ := e.StatusWas(c.ctx, iss); s == nil || s.ID != 1 {
		t.Error("status_was before change")
	}
	c.saveOK(e, iss)
	if s, _ := e.StatusWas(c.ctx, iss); s == nil || s.ID != 2 {
		t.Error("status_was should be reset on save")
	}
}

func TestIssueDClosing(t *testing.T) {
	c := setup(t)
	e := c.env()
	closing := func(i *Issue) bool { v, err := e.Closing(c.ctx, i); c.must(err); return v }
	iss := c.issue(1)
	c.must(e.SetStatusID(c.ctx, iss, 2))
	if closing(iss) {
		t.Error("2 is not closing")
	}
	c.must(e.SetStatusID(c.ctx, iss, 5))
	if !closing(iss) {
		t.Error("5 is closing")
	}
	n, _ := e.NewBlank(c.ctx)
	if closing(n) {
		t.Error("new open")
	}
	c.must(e.SetStatusID(c.ctx, n, 5))
	if !closing(n) {
		t.Error("new closed")
	}
	// reset after save
	iss = c.issue(1)
	c.must(e.SetStatusID(c.ctx, iss, 5))
	c.saveOK(e, iss)
	if closing(iss) {
		t.Error("closing should be reset after save")
	}
}

func TestIssueDReopening(t *testing.T) {
	c := setup(t)
	e := c.env()
	re := func(i *Issue) bool { v, err := e.Reopening(c.ctx, i); c.must(err); return v }
	iss := c.issue(8)
	c.must(e.SetStatusID(c.ctx, iss, 6))
	if re(iss) {
		t.Error("6 is closed")
	}
	c.must(e.SetStatusID(c.ctx, iss, 2))
	if !re(iss) {
		t.Error("2 reopens")
	}
	n, _ := e.NewBlank(c.ctx)
	c.must(e.SetStatusID(c.ctx, n, 1))
	if re(n) {
		t.Error("new open")
	}
	iss = c.issue(8)
	c.must(e.SetStatusID(c.ctx, iss, 2))
	c.saveOK(e, iss)
	if re(iss) {
		t.Error("reopening should be reset after save")
	}
}

func TestIssueDDefaultStatus(t *testing.T) {
	c := setup(t)
	e := c.env()
	n, _ := e.NewBlank(c.ctx)
	if s, _ := e.DefaultStatus(c.ctx, n); s != nil || n.TrackerID != 0 {
		t.Error("without tracker")
	}
	tr, _ := e.Tracker(c.ctx, 1)
	for name, iss := range map[string]*Issue{
		"tracker_id": c.newIssue(e, Params{"tracker_id": 1}),
		"tracker":    c.newIssue(e, Params{"tracker": tr}),
	} {
		ds, _ := e.DefaultStatus(c.ctx, iss)
		if iss.StatusID == 0 || ds == nil || ds.ID != tr.DefaultStatusID || iss.StatusID != ds.ID {
			t.Errorf("%s: status=%d", name, iss.StatusID)
		}
	}
	n, _ = e.NewBlank(c.ctx)
	c.must(e.SetTracker(c.ctx, n, tr))
	if n.StatusID != tr.DefaultStatusID {
		t.Error("setting tracker")
	}
}

func TestIssueDChangingTracker(t *testing.T) {
	cases := []struct {
		name       string
		transition bool
		newStatus  int64
		start      int64
		want       int64
	}{
		{"status was default", true, 1, 1, 2},
		{"status not used by tracker", false, 0, 3, 2},
		{"keep status", true, 3, 3, 3},
	}
	for _, x := range cases {
		c := setup(t)
		c.exec(`DELETE FROM workflow_transitions`)
		if x.transition {
			c.addTransition(2, 1, 2, x.newStatus, false, false)
		}
		c.exec(`UPDATE trackers SET default_status_id = 2 WHERE id = 2`)
		e := c.env()
		iss := c.newIssue(e, Params{"tracker_id": 1, "status_id": x.start})
		if iss.StatusID != x.start {
			t.Fatalf("%s: initial status %d", x.name, iss.StatusID)
		}
		tr, _ := e.Tracker(c.ctx, 2)
		c.must(e.SetTracker(c.ctx, iss, tr))
		if iss.StatusID != x.want {
			t.Errorf("%s: status = %d, want %d", x.name, iss.StatusID, x.want)
		}
	}
}

func TestIssueDPreviousAssigneeWithAGroup(t *testing.T) {
	c := setup(t)
	c.addMember(10, 1, 1)
	c.setting("issue_group_assignment", "1")
	e := c.env()
	iss := c.generateSaved(e, Params{"assigned_to_id": 10})
	iss = c.reload(e, iss)
	iss.AssignedToID = nil
	if p := iss.previousAssigneeID(); p == nil || *p != 10 {
		t.Errorf("previous_assignee = %v", p)
	}
}

func TestIssueDClosable(t *testing.T) {
	c := setup(t)
	e := c.env()
	i10 := c.issue(10)
	ok, warn, err := e.closable(c.ctx, i10)
	c.must(err)
	if !ok || warn != "" {
		t.Error("issue 10 should be closable")
	}
	ok, warn, err = e.closable(c.ctx, c.issue(9))
	c.must(err)
	if ok || warn != "notice_issue_not_closable_by_blocking_issue" {
		t.Errorf("issue 9: %v %q", ok, warn)
	}
}

func TestIssueDReopenable(t *testing.T) {
	c := setup(t)
	e := c.env()
	parent := c.generateSaved(e, Params{"status_id": 5})
	child := c.generateSaved(e, Params{"status_id": 5, "parent_issue_id": parent.ID, "project_id": parent.ProjectID, "tracker_id": parent.TrackerID})
	ok, warn, err := e.reopenable(c.ctx, child)
	c.must(err)
	if ok || warn != "notice_issue_not_reopenable_by_closed_parent_issue" {
		t.Errorf("%v %q", ok, warn)
	}
}

func TestIssueDFilterProjectsScope(t *testing.T) {
	c := setup(t)
	scope := func(issueID int64, s string) []int64 {
		p := c.project(c.issue(issueID).ProjectID)
		cond := filterProjectsScope(p, s)
		if cond == "" {
			return nil
		}
		return c.ids(`SELECT id FROM projects WHERE ` + cond + ` ORDER BY id`)
	}
	if scope(1, "system") != nil || scope(1, "xxx") != nil {
		t.Error("system / default should be unrestricted")
	}
	eqIDs(t, []int64{1, 3, 4, 5, 6}, scope(5, "tree"), "tree")
	eqIDs(t, []int64{1, 5, 6}, scope(9, "hierarchy"), "hierarchy")
	eqIDs(t, []int64{5, 6}, scope(9, "descendants"), "descendants")
	eqIDs(t, []int64{5}, scope(9, ""), "''")
}

func TestIssueDCreateAutoWatcher(t *testing.T) {
	for _, x := range []struct {
		aw       string
		watchers []int64
		want     int
	}{{`["issue_created"]`, nil, 1}, {`["issue_created"]`, []int64{1}, 1}, {`[]`, nil, 0}} {
		c := setup(t)
		c.setAutoWatchOn(1, x.aw)
		e := c.env()
		iss := c.newIssue(e, Params{"project_id": 1, "tracker_id": 1, "author_id": 1, "subject": "test_create_should_add_watcher"})
		iss.SetWatcherUserIDs(x.watchers)
		n := c.count(`SELECT COUNT(*) FROM watchers`)
		c.saveOK(e, iss)
		if got := c.count(`SELECT COUNT(*) FROM watchers`) - n; got != x.want {
			t.Errorf("%s %v: watchers +%d, want %d", x.aw, x.watchers, got, x.want)
		}
	}
}

func TestIssueDCreateShouldNotAddAnonymousAsWatcher(t *testing.T) {
	c := setup(t)
	c.addPermission(c.ids(`SELECT id FROM roles WHERE builtin = 2`)[0], "add_issue_watchers")
	e := c.env()
	anon := c.ids(`SELECT id FROM principals WHERE kind = 'anonymous_user'`)[0]
	if aw, _ := e.autoWatchOn(c.ctx, anon); !slices.Contains(aw, "issue_contributed_to") {
		t.Fatal("anonymous should auto watch issue_contributed_to")
	}
	n := c.count(`SELECT COUNT(*) FROM watchers`)
	if _, _ = c.generateJournal(e, 1, anon, "notes", false, nil); c.count(`SELECT COUNT(*) FROM watchers`) != n {
		t.Error("anonymous added as watcher")
	}
	if !c.valid(e, c.issue(1)) {
		t.Error("issue should be valid")
	}
}

func TestIssueDAttachmentsEditableAndDeletable(t *testing.T) {
	c := setup(t)
	e := c.env()
	i := c.issue(14)
	for name, f := range map[string]func() (bool, bool){
		"editable": func() (bool, bool) {
			a, _ := e.AttachmentsEditable(c.ctx, i, c.user(2))
			b, _ := e.AttachmentsEditable(c.ctx, i, c.user(3))
			return a, b
		},
		"deletable": func() (bool, bool) {
			a, _ := e.AttachmentsDeletable(c.ctx, i, c.user(2))
			b, _ := e.AttachmentsDeletable(c.ctx, i, c.user(3))
			return a, b
		},
	} {
		a, b := f()
		if !a || b {
			t.Errorf("%s: jsmith=%v dlopper=%v", name, a, b)
		}
	}
}
