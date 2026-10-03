// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository_test

import (
	"testing"

	"github.com/mikuta0407/buropher/internal/repository"
)

// 同じタイムステップの TOTP コードは一度しか通らない（並行するリクエストで二重に使われない）。
func TestSetTwofaTotpLastUsedRejectsReuse(t *testing.T) {
	withFixtures(t, func(e *env) {
		for _, c := range []struct {
			at   int64
			want bool
		}{{100, true}, {100, false}, {99, false}, {101, true}} {
			ok, err := repository.SetTwofaTotpLastUsed(e.ctx, e.d, 2, c.at)
			e.must(err)
			if ok != c.want {
				t.Errorf("at=%d: %v, want %v", c.at, ok, c.want)
			}
		}
	})
}
