// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"testing"
)

// TestMessageBoardIDOfOtherProjectNeedsPermission は edit_messages を持つプロジェクトのトピックを、
// 権限の無い（非メンバーの非公開）プロジェクトのフォーラムへ移動したり、そこへ投稿したりできないことを確認する。
func TestMessageBoardIDOfOtherProjectNeedsPermission(t *testing.T) {
	ts, d := newFixtureServer(t)
	ctx := context.Background()
	// jsmith を onlinestore（非公開、フォーラム 3）のメンバーから外す
	if _, err := d.Exec(ctx, `DELETE FROM members WHERE project_id = 2 AND principal_id = 2`); err != nil {
		t.Fatal(err)
	}
	jsmith := login(t, ts, "jsmith", "jsmith")
	before := contentCount(t, d, `SELECT COUNT(*) FROM messages WHERE board_id = 3`)

	// 編集でのトピック移動
	res, _ := post(t, jsmith, ts.URL+"/boards/1/topics/1/edit", contentForm(t, jsmith, ts,
		"message[subject]", "moved", "message[content]", "moved", "message[board_id]", "3"))
	if res.StatusCode == 302 {
		t.Errorf("edit with board_id of another project: redirected")
	}
	if n := contentCount(t, d, `SELECT board_id FROM messages WHERE id = 1`); n != 1 {
		t.Fatalf("topic 1 moved to board %d", n)
	}
	// 返信・新規トピックでの別フォーラムへの投稿
	post(t, jsmith, ts.URL+"/boards/1/topics/1/replies", contentForm(t, jsmith, ts,
		"reply[subject]", "RE", "reply[content]", "x", "reply[board_id]", "3"))
	post(t, jsmith, ts.URL+"/boards/1/topics/new", contentForm(t, jsmith, ts,
		"message[subject]", "S", "message[content]", "x", "message[board_id]", "3"))
	if n := contentCount(t, d, `SELECT COUNT(*) FROM messages WHERE board_id = 3`); n != before {
		t.Fatalf("messages posted into board 3: %d -> %d", before, n)
	}

	// 同じプロジェクト内の移動は従来どおり
	res, _ = post(t, jsmith, ts.URL+"/boards/1/topics/1/edit", contentForm(t, jsmith, ts,
		"message[subject]", "moved", "message[content]", "moved", "message[board_id]", "2"))
	if res.StatusCode != 302 {
		t.Fatalf("move within the project: status %d", res.StatusCode)
	}
	if n := contentCount(t, d, `SELECT board_id FROM messages WHERE id = 1`); n != 2 {
		t.Errorf("topic 1 board = %d, want 2", n)
	}
	// 移動先でも権限があれば（管理者）他プロジェクトへの移動もできる（Redmine と同じ）
	admin := login(t, ts, "admin", "admin")
	res, _ = post(t, admin, ts.URL+"/boards/2/topics/1/edit", contentForm(t, admin, ts,
		"message[subject]", "moved", "message[content]", "moved", "message[board_id]", "3"))
	if res.StatusCode != 302 {
		t.Fatalf("admin move: status %d", res.StatusCode)
	}
	if n := contentCount(t, d, `SELECT board_id FROM messages WHERE id = 1`); n != 3 {
		t.Errorf("admin move: board = %d, want 3", n)
	}
}
