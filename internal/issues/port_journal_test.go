// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package issues

// test/unit/journal_test.rb の移植。
//
// 移植済み:
//   test_journalized_is_an_issue, test_new_status, test_create_should_send_email_notification,
//   test_should_not_save_journal_with_blank_notes_and_no_details, test_create_should_not_split_non_private_notes,
//   test_create_should_split_private_notes, test_create_should_add_wacher, test_create_should_not_add_watcher,
//   test_create_should_not_add_anonymous_as_watcher, test_visible_scope_for_anonymous / _for_user / _for_admin
//   (Issue.visible_condition + Journal.visible_notes_condition の SQL で確認),
//   test_details_should_normalize_dates / _true_values / _false_values (journalValue の正規化で確認),
//   test_custom_field_should_return_custom_field_for_cf_detail,
//   test_visible_details_should_include_relations_to_visible_issues_only,
//   test_notified_mentions_should_not_include_users_who_cannot_view_private_notes
//
// 未移植 (理由):
//   test_preload_journals_details_custom_fields_* (ActiveRecord の preload 最適化。Go では不要)
//   test_custom_field_should_return_nil_for_non_cf_detail (JournalDetail#custom_field 相当の API なし。cf 以外は参照しない)
//   test_attachments (Journal#attachments は添付ファイル層の API で未実装)

import (
	"slices"
	"testing"

	"github.com/mikuta0407/buropher/internal/authz"
	"github.com/mikuta0407/buropher/internal/domain"
)

func TestJournalJournalizedAndNewStatus(t *testing.T) {
	c := setup(t)
	e := c.env()
	j, err := e.FindJournal(c.ctx, 1)
	c.must(err)
	if j.IssueID != 1 {
		t.Errorf("issue = %d", j.IssueID)
	}
	v := j.NewValueFor("status_id")
	if v == nil || *v != "2" {
		t.Errorf("new_status = %v", v)
	}
}

func TestJournalCreateShouldSendEmailNotification(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.issue(1)
	// init_journal(user, issue): notes は issue.to_s
	j, err := e.InitJournal(c.ctx, iss, c.user(1), e.String(c.ctx, iss))
	c.must(err)
	ok, res := c.saveJournal(e, iss, j)
	if !ok || deliveries(res) != 2 {
		t.Errorf("ok=%v notifications=%v", ok, res.Notifications)
	}
}

func TestJournalShouldNotSaveWithBlankNotesAndNoDetails(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.issue(1)
	j, err := e.InitJournal(c.ctx, iss, c.user(1), "")
	c.must(err)
	n := c.count(`SELECT COUNT(*) FROM issue_journals`)
	if ok, _ := c.saveJournal(e, iss, j); ok || c.count(`SELECT COUNT(*) FROM issue_journals`) != n {
		t.Error("blank journal saved")
	}
}

func newDetail() []*domain.JournalDetail {
	return []*domain.JournalDetail{{Property: "attr", PropKey: "subject"}}
}

func TestJournalCreateShouldNotSplitNonPrivateNotes(t *testing.T) {
	c := setup(t)
	e := c.env()
	for _, x := range []struct {
		notes   string
		details []*domain.JournalDetail
		nd      int
	}{{"Notes", nil, 0}, {"Notes", newDetail(), 1}, {"", newDetail(), 1}} {
		nj, nd := c.count(`SELECT COUNT(*) FROM issue_journals`), c.count(`SELECT COUNT(*) FROM issue_journal_details`)
		c.generateJournal(e, 1, 1, x.notes, false, x.details)
		if c.count(`SELECT COUNT(*) FROM issue_journals`)-nj != 1 || c.count(`SELECT COUNT(*) FROM issue_journal_details`)-nd != x.nd {
			t.Errorf("%+v", x)
		}
	}
}

func TestJournalCreateShouldSplitPrivateNotes(t *testing.T) {
	c := setup(t)
	e := c.env()
	counts := func() (int, int) {
		return c.count(`SELECT COUNT(*) FROM issue_journals`), c.count(`SELECT COUNT(*) FROM issue_journal_details`)
	}
	// ノートのみ
	nj, nd := counts()
	j, _ := c.generateJournal(e, 1, 1, "Notes", true, nil)
	j, _ = e.FindJournal(c.ctx, j.ID)
	if a, b := counts(); a-nj != 1 || b-nd != 0 || !j.PrivateNotes || j.Notes != "Notes" {
		t.Error("notes only")
	}
	// ノート + 詳細 → 分割
	nj, nd = counts()
	j, _ = c.generateJournal(e, 1, 1, "Notes", true, newDetail())
	j, _ = e.FindJournal(c.ctx, j.ID)
	if a, b := counts(); a-nj != 2 || b-nd != 1 || !j.PrivateNotes || j.Notes != "Notes" || len(j.Details) != 0 {
		t.Errorf("split: %+v", j)
	}
	other, _ := e.FindJournal(c.ctx, c.ids(`SELECT id FROM issue_journals ORDER BY id DESC LIMIT 1 OFFSET 1`)[0])
	if other.PrivateNotes || other.Notes != "" || len(other.Details) != 1 || !other.CreatedAt.Equal(j.CreatedAt) {
		t.Errorf("changes journal: %+v", other)
	}
	// 空ノート + 詳細 → 非公開にしない
	nj, nd = counts()
	j, _ = c.generateJournal(e, 1, 1, "", true, newDetail())
	j, _ = e.FindJournal(c.ctx, j.ID)
	if a, b := counts(); a-nj != 1 || b-nd != 1 || j.PrivateNotes || j.Notes != "" || len(j.Details) != 1 {
		t.Errorf("blank notes: %+v", j)
	}
}

func TestJournalCreateAutoWatcher(t *testing.T) {
	for _, x := range []struct {
		aw   string
		want int
	}{{`["issue_contributed_to"]`, 1}, {`[]`, 0}} {
		c := setup(t)
		c.setAutoWatchOn(1, x.aw)
		e := c.env()
		n := c.count(`SELECT COUNT(*) FROM watchers`)
		c.generateJournal(e, 1, 1, "notes", false, nil)
		if got := c.count(`SELECT COUNT(*) FROM watchers`) - n; got != x.want {
			t.Errorf("%s: +%d", x.aw, got)
		}
	}
}

func TestJournalCreateShouldNotAddAnonymousAsWatcher(t *testing.T) {
	c := setup(t)
	c.addPermission(c.ids(`SELECT id FROM roles WHERE builtin = 2`)[0], "add_issue_watchers")
	e := c.env()
	anon := c.ids(`SELECT id FROM principals WHERE kind = 'anonymous_user'`)[0]
	n := c.count(`SELECT COUNT(*) FROM watchers`)
	c.generateJournal(e, 1, anon, "notes", false, nil)
	if c.count(`SELECT COUNT(*) FROM watchers`) != n {
		t.Error("anonymous added as watcher")
	}
}

// visibleJournals は Journal.visible(user) の課題 (issue_id, project.is_public, project_id)。
func visibleJournals(c *tc, uid int64) [][3]int64 {
	c.t.Helper()
	e := c.env()
	u := c.user(uid)
	cond, err := authz.New(c.d, u).IssueVisibleCondition(c.ctx, authz.ConditionOptions{})
	c.must(err)
	notes, err := e.JournalVisibleNotesCondition(c.ctx, u)
	c.must(err)
	var rows []struct {
		ID      int64 `db:"id"`
		Public  bool  `db:"is_public"`
		Project int64 `db:"project_id"`
	}
	c.must(c.d.Select(c.ctx, &rows, `SELECT issue_journals.id, projects.is_public, issues.project_id FROM issue_journals
JOIN issues ON issues.id = issue_journals.issue_id JOIN projects ON projects.id = issues.project_id WHERE `+cond+` AND `+notes))
	var out [][3]int64
	for _, r := range rows {
		p := int64(0)
		if r.Public {
			p = 1
		}
		out = append(out, [3]int64{r.ID, p, r.Project})
	}
	return out
}

func TestJournalVisibleScopeForAnonymous(t *testing.T) {
	c := setup(t)
	anon := c.ids(`SELECT id FROM principals WHERE kind = 'anonymous_user'`)[0]
	js := visibleJournals(c, anon)
	if len(js) == 0 || slices.ContainsFunc(js, func(r [3]int64) bool { return r[1] == 0 }) {
		t.Errorf("anonymous: %v", js)
	}
	c.removePermission(c.ids(`SELECT id FROM roles WHERE builtin = 2`)[0], "view_issues")
	if js := visibleJournals(c, anon); len(js) != 0 {
		t.Errorf("without view_issues: %v", js)
	}
}

func TestJournalVisibleScopeForUser(t *testing.T) {
	c := setup(t)
	js := visibleJournals(c, 9)
	if len(js) == 0 || slices.ContainsFunc(js, func(r [3]int64) bool { return r[1] == 0 }) {
		t.Errorf("non member: %v", js)
	}
	c.removePermission(c.ids(`SELECT id FROM roles WHERE builtin = 1`)[0], "view_issues")
	if js := visibleJournals(c, 9); len(js) != 0 {
		t.Errorf("without view_issues: %v", js)
	}
	c.addMember(9, 1, 1)
	js = visibleJournals(c, 9)
	if len(js) == 0 || slices.ContainsFunc(js, func(r [3]int64) bool { return r[2] != 1 }) {
		t.Errorf("member: %v", js)
	}
}

func TestJournalVisibleScopeForAdmin(t *testing.T) {
	c := setup(t)
	c.exec(`DELETE FROM members WHERE principal_id = 1`)
	js := visibleJournals(c, 1)
	if len(js) == 0 || !slices.ContainsFunc(js, func(r [3]int64) bool { return r[1] == 0 }) {
		t.Errorf("admin: %v", js)
	}
}

func TestJournalDetailsShouldNormalizeValues(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.newIssue(e, Params{"start_date": "2012-11-03", "is_private": true})
	if v, _ := e.journalValue(c.ctx, iss, "start_date"); v == nil || *v != "2012-11-03" {
		t.Errorf("date = %v", v)
	}
	if v, _ := e.journalValue(c.ctx, iss, "is_private"); v == nil || *v != "1" {
		t.Errorf("true = %v", v)
	}
	iss.IsPrivate = false
	if v, _ := e.journalValue(c.ctx, iss, "is_private"); v == nil || *v != "0" {
		t.Errorf("false = %v", v)
	}
}

func TestJournalCustomFieldForCFDetail(t *testing.T) {
	c := setup(t)
	cf, err := c.env().CustomField(c.ctx, 2)
	c.must(err)
	if cf == nil || cf.ID != 2 {
		t.Error("custom field 2 not found")
	}
}

func TestJournalVisibleDetailsShouldIncludeRelationsToVisibleIssuesOnly(t *testing.T) {
	c := setup(t)
	e := c.env()
	iss := c.generateSaved(e, Params{})
	vis := c.generateSaved(e, Params{})
	hid := c.generateSaved(e, Params{"is_private": true})
	j := &Journal{}
	j.Details = []*domain.JournalDetail{
		{Property: "relation", PropKey: "relates", Value: sp(itoa(vis.ID))},
		{Property: "relation", PropKey: "relates", Value: sp(itoa(hid.ID))},
	}
	anon := c.user(c.ids(`SELECT id FROM principals WHERE kind = 'anonymous_user'`)[0])
	vd, err := e.VisibleDetails(c.ctx, j, iss, anon)
	c.must(err)
	if len(vd) != 1 || *vd[0].Value != itoa(vis.ID) {
		t.Errorf("anonymous: %v", vd)
	}
	vd, err = e.VisibleDetails(c.ctx, j, iss, c.user(2))
	c.must(err)
	if len(vd) != 2 {
		t.Errorf("jsmith: %d", len(vd))
	}
}

func TestJournalNotifiedMentionsShouldNotIncludeUsersWhoCannotViewPrivateNotes(t *testing.T) {
	c := setup(t)
	e := c.env()
	j, _ := c.generateJournal(e, 2, 1, "Hello @dlopper, @jsmith and @admin.", true, nil)
	iss := c.issue(2)
	ms, err := e.filterMailable(c.ctx, iss, e.journalMentions(c.ctx, j))
	c.must(err)
	ms, err = e.selectJournalVisibleUsers(c.ctx, j, iss, ms)
	c.must(err)
	ids := userIDs(ms)
	slices.Sort(ids)
	eqIDs(t, []int64{1, 2}, ids, "mentions")
}
