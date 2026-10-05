// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package mailhandler

import (
	"regexp"
	"testing"
)

// メールの返信で、見えないチケット（非公開で作成者・担当者でない）には注記を追加できない
// （7.0.1 #44118 で本家も issue.visible?(user) && issue.notes_addable? を見るようになった）。
func TestReplyToInvisibleIssueIsRejected(t *testing.T) {
	c := setup(t)
	// #2 を dlopper（Developer: add_issue_notes あり、issues_visibility=default）が見られない非公開チケットにする
	c.exec(`UPDATE issues SET is_private = ?, author_id = 1, assigned_to_id = NULL WHERE id = 2`, true)
	fromDlopper := func(s string) string {
		return regexp.MustCompile(`(?m)^From:.*$`).ReplaceAllLiteralString(s, `From: "Dave Lopper" <dlopper@somenet.foo>`)
	}
	journals := c.count(`SELECT COUNT(*) FROM issue_journals WHERE issue_id = 2`)
	if obj := c.submit("ticket_reply.eml", Options{}, fromDlopper); obj != nil {
		t.Errorf("reply to invisible issue accepted: %T", obj)
	}
	if n := c.count(`SELECT COUNT(*) FROM issue_journals WHERE issue_id = 2`); n != journals {
		t.Errorf("journals %d -> %d", journals, n)
	}
	// 見えるようになれば受け付ける
	c.exec(`UPDATE issues SET is_private = ? WHERE id = 2`, false)
	c.assertJournal(c.submit("ticket_reply.eml", Options{}, fromDlopper))
}

// Redmine 7.0.1 の test_reply_to_a_non_visible_issue (#44118)。
func TestReplyToANonVisibleIssue(t *testing.T) {
	c := setup(t)
	// Role.find_by_name('Developer').remove_permission! :view_issues
	c.exec(`DELETE FROM role_permissions WHERE permission = 'view_issues' AND role_id = (SELECT id FROM roles WHERE name = 'Developer')`)
	issues := c.count(`SELECT COUNT(*) FROM issues`)
	journals := c.count(`SELECT COUNT(*) FROM issue_journals`)
	obj := c.submit("ticket_reply.eml", Options{}, func(s string) string {
		s = regexp.MustCompile(`(?m)^From:.*$`).ReplaceAllLiteralString(s, "From: <jsmith@somenet.foo>")
		return regexp.MustCompile(`(?m)^In-Reply-To:.*$`).ReplaceAllLiteralString(s, "In-Reply-To: <redmine.issue-4.20060719210421@osiris>")
	})
	if obj != nil {
		t.Errorf("reply accepted: %T", obj)
	}
	if c.count(`SELECT COUNT(*) FROM issues`) != issues || c.count(`SELECT COUNT(*) FROM issue_journals`) != journals {
		t.Error("issue or journal created")
	}
}
