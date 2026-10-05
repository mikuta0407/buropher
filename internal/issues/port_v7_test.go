// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package issues

// Redmine 7.0 で追加・変更されたチケットまわりの振る舞いのテスト。
//
//   - トラッカーの「プライベート」既定値 (#9432): issues_controller_test の
//     test_post_create_should_respect_private_by_default_per_tracker_setting /
//     test_post_create_should_not_apply_private_by_default_without_permission の SafeAssign 部分
//   - 担当者の自動ウォッチ (#2716): journal_test の test_create_should_add_assignee_as_watcher
//   - ウォッチしている対象だけ通知 (#37978): mailer_test の only_my_watches の 2 件
//   - 空の注記に編集したジャーナルを残す (#44258): journal_test の test_should_save_existing_journal_with_blank_notes_and_no_details
//   - 期日の既定値 (#31518): Issue.new の既定値

import (
	"slices"
	"testing"

	"github.com/mikuta0407/buropher/internal/domain"
)

func TestV7PrivateByDefaultPerTracker(t *testing.T) {
	c := setup(t)
	c.exec(`UPDATE trackers SET private_by_default = ? WHERE id = 1`, true)
	e := c.as(2).env()
	u := c.user(2)

	iss, err := e.NewBlank(c.ctx)
	c.must(err)
	c.must(e.SafeAssign(c.ctx, iss, Params{"project_id": "1", "tracker_id": "1", "subject": "private by default"}, u))
	if !iss.IsPrivate {
		t.Error("is_private should default to the tracker setting")
	}

	iss, err = e.NewBlank(c.ctx)
	c.must(err)
	c.must(e.SafeAssign(c.ctx, iss, Params{"project_id": "1", "tracker_id": "1", "subject": "public", "is_private": "0"}, u))
	if iss.IsPrivate {
		t.Error("explicit is_private=0 should win over the tracker default")
	}

	// 既存チケットには適用しない
	old := c.issue(1)
	c.must(e.SafeAssign(c.ctx, old, Params{"subject": "edited"}, u))
	if old.IsPrivate {
		t.Error("private_by_default should not apply to existing issues")
	}
}

func TestV7PrivateByDefaultWithoutPermission(t *testing.T) {
	c := setup(t)
	c.removePermission(1, "set_issues_private", "set_own_issues_private")
	c.exec(`UPDATE trackers SET private_by_default = ? WHERE id = 1`, true)
	e := c.as(2).env()
	iss, err := e.NewBlank(c.ctx)
	c.must(err)
	c.must(e.SafeAssign(c.ctx, iss, Params{"project_id": "1", "tracker_id": "1", "subject": "public"}, c.user(2)))
	if iss.IsPrivate {
		t.Error("private_by_default should not apply without set_issues_private permission")
	}
}

func TestV7JournalShouldAddAssigneeAsWatcher(t *testing.T) {
	c := setup(t)
	c.setAutoWatchOn(2, `["issue_assigned_to_me"]`)
	e := c.as(1).env()
	iss := c.issue(1)
	c.must(e.RemoveWatcher(c.ctx, iss, 2))
	_, err := e.InitJournal(c.ctx, iss, c.user(1), "")
	c.must(err)
	iss.AssignedToID = ptrInt64(2)
	n := c.count(`SELECT COUNT(*) FROM watchers`)
	c.saveOK(e, iss)
	if got := c.count(`SELECT COUNT(*) FROM watchers`) - n; got != 1 {
		t.Errorf("watchers +%d, want 1", got)
	}
	if w, err := e.WatchedBy(c.ctx, c.issue(1), c.user(2)); err != nil || !w {
		t.Errorf("assignee should watch the issue (err=%v)", err)
	}
}

func TestV7JournalShouldNotAddAssigneeWithoutOption(t *testing.T) {
	c := setup(t)
	c.setAutoWatchOn(2, `[]`)
	e := c.as(1).env()
	iss := c.issue(1)
	c.must(e.RemoveWatcher(c.ctx, iss, 2))
	_, err := e.InitJournal(c.ctx, iss, c.user(1), "")
	c.must(err)
	iss.AssignedToID = ptrInt64(2)
	n := c.count(`SELECT COUNT(*) FROM watchers`)
	c.saveOK(e, iss)
	if got := c.count(`SELECT COUNT(*) FROM watchers`) - n; got != 0 {
		t.Errorf("watchers +%d, want 0", got)
	}
}

func TestV7OnlyMyWatchesNotification(t *testing.T) {
	c := setup(t)
	// 作成者でもウォッチしていなければ通知しない
	c.setMailNotification(1, "only_my_watches")
	e := c.env()
	iss := c.generate(e, Params{"project_id": 1, "author_id": 1})
	c.saveOK(e, iss)
	users, err := e.NotifiedUsers(c.ctx, iss)
	c.must(err)
	if slices.ContainsFunc(users, func(u *domain.User) bool { return u.ID == 1 }) {
		t.Error("author with only_my_watches should not be notified unless watching")
	}
	// ウォッチしていれば通知する
	c.must(e.AddWatcher(c.ctx, iss, 1))
	users, err = e.NotifiedUsers(c.ctx, iss)
	c.must(err)
	watchers, err := e.NotifiedWatchers(c.ctx, iss)
	c.must(err)
	if !slices.ContainsFunc(append(users, watchers...), func(u *domain.User) bool { return u.ID == 1 }) {
		t.Error("watcher with only_my_watches should be notified")
	}
}

func TestV7UpdateJournalWithBlankNotesKeepsJournal(t *testing.T) {
	c := setup(t)
	e := c.as(1).env()
	j, err := e.FindJournal(c.ctx, 2)
	c.must(err)
	n := c.count(`SELECT COUNT(*) FROM issue_journals`)
	c.must(e.UpdateJournalNotes(c.ctx, j, "", nil, c.user(1)))
	if c.count(`SELECT COUNT(*) FROM issue_journals`) != n {
		t.Fatal("journal should not be deleted")
	}
	j, err = e.FindJournal(c.ctx, 2)
	c.must(err)
	if j == nil || j.Notes != "" {
		t.Errorf("notes = %+v", j)
	}
}

func TestV7NewIssueDefaultDueDate(t *testing.T) {
	c := setup(t)
	c.setting("default_issue_due_date_offset", "7")
	e := c.env()
	iss, err := e.New(c.ctx, c.project(1), 1, c.user(2))
	c.must(err)
	if iss.DueDate == nil || !iss.DueDate.Equal(daysFromNow(7)) {
		t.Errorf("due_date = %v, want %v", iss.DueDate, daysFromNow(7))
	}

	c.setting("default_issue_due_date_offset", "")
	iss, err = e.New(c.ctx, c.project(1), 1, c.user(2))
	c.must(err)
	if iss.DueDate != nil {
		t.Errorf("due_date = %v, want nil", iss.DueDate)
	}
}
