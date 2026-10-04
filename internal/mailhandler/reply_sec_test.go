// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package mailhandler

import (
	"regexp"
	"testing"
)

// メールの返信で、見えないチケット（非公開で作成者・担当者でない）には注記を追加できない
// （本家は notes_addable? だけを見るので追加できてしまう）。
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
