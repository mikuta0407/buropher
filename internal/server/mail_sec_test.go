// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/notify"
)

// 通知メールの「親チケット」に、受信者が見られない親チケットの件名を載せない。
func TestMailParentIssueNotLeaked(t *testing.T) {
	a, d, _ := newMailApp(t)
	ctx := context.Background()
	// #2 を dlopper（Developer: issues_visibility=default）が見られない非公開チケットにして #1 の親にする
	if _, err := d.Exec(ctx, `UPDATE issues SET is_private = ?, author_id = 1, assigned_to_id = NULL WHERE id = 2`, true); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(ctx, `UPDATE issues SET parent_id = 2 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	render := func(addr string) string {
		m, err := a.RenderMail(ctx, &notify.Payload{Kind: notify.KindIssueAdd, IssueID: 1, UserID: userIDByMail(t, d, addr)})
		if err != nil || m == nil {
			t.Fatalf("render: %v", err)
		}
		return m.Text + m.HTML
	}
	if body := render("dlopper@somenet.foo"); strings.Contains(body, "Add ingredients categories") || !strings.Contains(body, "#2") {
		t.Errorf("invisible parent subject leaked or missing id:\n%s", body)
	}
	if body := render("jsmith@somenet.foo"); !strings.Contains(body, "Add ingredients categories") {
		t.Errorf("visible parent subject missing:\n%s", body)
	}
}

// 非公開の注記は view_private_notes の無い受信者（説明文で新たにメンションされたユーザーを含む）へのメールに
// 載せない。見える変更も無ければメール自体を送らない。
func TestMailPrivateNotesNotLeaked(t *testing.T) {
	a, d, _ := newMailApp(t)
	ctx := context.Background()
	if _, err := d.Exec(ctx, `UPDATE issue_journals SET private_notes = ? WHERE id IN (1, 2)`, true); err != nil {
		t.Fatal(err)
	}
	render := func(addr string, journalID int64) string {
		m, err := a.RenderMail(ctx, &notify.Payload{Kind: notify.KindIssueEdit, IssueID: 1, JournalID: journalID,
			UserID: userIDByMail(t, d, addr)})
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if m == nil {
			return ""
		}
		return m.Text + m.HTML
	}
	// journal 1: 注記 + 状態の変更。dlopper（Developer）には変更だけ
	if body := render("dlopper@somenet.foo", 1); body == "" || strings.Contains(body, "Journal notes") {
		t.Errorf("private notes leaked or mail missing:\n%s", body)
	}
	// journal 2: 注記のみ。dlopper には送らない
	if body := render("dlopper@somenet.foo", 2); body != "" {
		t.Errorf("notes-only private journal sent:\n%s", body)
	}
	if body := render("jsmith@somenet.foo", 1); !strings.Contains(body, "Journal notes") {
		t.Errorf("private notes missing for allowed user:\n%s", body)
	}
}
