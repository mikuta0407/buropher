// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository_test

// test/unit/token_test.rb の test_find_active_user_should_bump_updated_on_* / test_used_should_*
// （Redmine 7.0 #43938）の移植。

import (
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

func TestTokenFindActiveUserBumpsUpdatedOn(t *testing.T) {
	withFixtures(t, func(e *env) {
		now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
		for _, action := range []string{repository.TokenAPI, repository.TokenFeeds} {
			tok, err := repository.CreateToken(e.ctx, e.d, 1, action)
			e.must(err)
			updatedAt := func() time.Time {
				var ts db.Time
				e.must(e.d.Get(e.ctx, &ts, `SELECT updated_at FROM tokens WHERE id = ?`, tok.ID))
				return ts.Time
			}
			setUpdated := func(at time.Time) {
				_, err := e.d.Exec(e.ctx, `UPDATE tokens SET updated_at = ? WHERE id = ?`, db.NewTime(at), tok.ID)
				e.must(err)
			}
			// 2 分前に使ったものは更新する
			setUpdated(now.Add(-2 * time.Minute))
			_, err = repository.FindActiveTokenUser(e.ctx, e.d, action, tok.Value, 0, now)
			e.must(err)
			if got := updatedAt(); !got.Equal(now) {
				t.Errorf("%s: updated_at = %v, want %v", action, got, now)
			}
			// 1 分以内なら更新しない
			recent := now.Add(-time.Second)
			setUpdated(recent)
			_, err = repository.FindActiveTokenUser(e.ctx, e.d, action, tok.Value, 0, now)
			e.must(err)
			if got := updatedAt(); !got.Equal(recent) {
				t.Errorf("%s: updated_at = %v, want %v (not bumped)", action, got, recent)
			}
		}

		at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		if repository.TokenUsed(&domain.Token{CreatedAt: at}) {
			t.Error("used? with nil updated_on")
		}
		if repository.TokenUsed(&domain.Token{CreatedAt: at, UpdatedAt: at}) {
			t.Error("used? with updated_on == created_on")
		}
		if !repository.TokenUsed(&domain.Token{CreatedAt: at, UpdatedAt: at.Add(24 * time.Hour)}) {
			t.Error("used? with updated_on > created_on")
		}
	})
}
