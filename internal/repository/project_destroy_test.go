// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository_test

// test/unit/jobs/destroy_project_job_test.rb（Redmine 7.0.2 Defect #44375）の移植。
// Redmine は lft/rgt の範囲が古いまま update_all すると無関係なプロジェクトまで削除予約にしていた。
// buropher は閉包テーブルで子孫を ID で引いて 1 文で更新するため起きないことを確かめる。

import (
	"testing"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

func TestScheduleProjectDeletionMarksOnlyProjectAndDescendants(t *testing.T) {
	withFixtures(t, func(e *env) {
		// 無関係なルート（onlinestore）を eCookbook より前に並ぶ名前にする
		if _, err := e.d.Exec(e.ctx, `UPDATE projects SET name = ? WHERE id = 2`, "AAA renamed earlier than ecookbook"); err != nil {
			t.Fatal(err)
		}
		var want []int64
		e.must(e.d.Select(e.ctx, &want, `SELECT descendant_id FROM project_closure WHERE ancestor_id = 1 ORDER BY descendant_id`))
		e.must(repository.ScheduleProjectDeletion(e.ctx, e.d, 1))
		var got []int64
		e.must(e.d.Select(e.ctx, &got, `SELECT id FROM projects WHERE status = ? ORDER BY id`, domain.ProjectStatusScheduledForDeletion))
		if len(got) != len(want) || len(got) < 2 {
			t.Fatalf("scheduled %v, want %v", got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("scheduled %v, want %v", got, want)
			}
		}
	})
}
