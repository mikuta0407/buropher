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
