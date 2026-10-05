// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository_test

// test/unit/lib/redmine/twofa_test.rb（#44368）の移植。

import (
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/repository"
)

func TestConsumeTwofaBackupCodeIsScopedToUser(t *testing.T) {
	withFixtures(t, func(e *env) {
		now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
		e.must(repository.ReplaceTwofaBackupCodes(e.ctx, e.d, 2, []string{"digest-jsmith"}, now))
		e.must(repository.ReplaceTwofaBackupCodes(e.ctx, e.d, 3, []string{"digest-dlopper"}, now))
		// 他のユーザーのバックアップコードは使えず、消しもしない
		ok, err := repository.ConsumeTwofaBackupCode(e.ctx, e.d, 2, "digest-dlopper")
		e.must(err)
		if ok {
			t.Error("another user's backup code accepted")
		}
		if n := e.count(`SELECT COUNT(*) FROM twofa_backup_codes WHERE user_id = 3`); n != 1 {
			t.Errorf("another user's backup code deleted (left %d)", n)
		}
		// 自分のコードは使え、使ったものは無効になる
		ok, err = repository.ConsumeTwofaBackupCode(e.ctx, e.d, 2, "digest-jsmith")
		e.must(err)
		if !ok {
			t.Error("own backup code rejected")
		}
		if n := e.count(`SELECT COUNT(*) FROM twofa_backup_codes WHERE user_id = 2`); n != 0 {
			t.Errorf("used backup code not invalidated (left %d)", n)
		}
	})
}
