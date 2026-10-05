// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository_test

// test/unit/changeset_test.rb（test_previous_uses_same_order_as_changeset_list /
// test_next_uses_same_order_as_changeset_list、Redmine 7.0 #43965）の移植。

import (
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
)

func TestChangesetPreviousNextUseListOrder(t *testing.T) {
	withFixtures(t, func(e *env) {
		repoID, err := e.d.InsertReturningID(e.ctx, `INSERT INTO repositories (project_id, scm, url, identifier, is_default, created_at) VALUES (3, 'git', '/tmp/previous-order', 'order', 0, '2026-01-15T12:00:00.000000Z')`)
		e.must(err)
		mk := func(rev string, at time.Time) *domain.Changeset {
			c := &domain.Changeset{RepositoryID: repoID, Revision: rev, Scmid: rev, CommittedOn: at, Comments: rev}
			e.must(repository.InsertChangeset(e.ctx, e.d, c))
			return c
		}
		// id の小さい方が新しいコミット
		newer := mk("order-newer", time.Date(2025, 4, 8, 10, 0, 0, 0, time.UTC))
		older := mk("order-older", time.Date(2025, 4, 7, 10, 0, 0, 0, time.UTC))
		// 同じ日時は id の順
		same := mk("order-same", time.Date(2025, 4, 8, 10, 0, 0, 0, time.UTC))

		check := func(name string, got *domain.Changeset, err error, want *domain.Changeset) {
			t.Helper()
			e.must(err)
			switch {
			case want == nil && got != nil:
				t.Errorf("%s = %s, want nil", name, got.Revision)
			case want != nil && (got == nil || got.ID != want.ID):
				t.Errorf("%s = %+v, want %s", name, got, want.Revision)
			}
		}
		p, err := repository.PreviousChangeset(e.ctx, e.d, newer)
		check("newer.previous", p, err, older)
		n, err := repository.NextChangeset(e.ctx, e.d, older)
		check("older.next", n, err, newer)
		n, err = repository.NextChangeset(e.ctx, e.d, newer)
		check("newer.next", n, err, same)
		p, err = repository.PreviousChangeset(e.ctx, e.d, same)
		check("same.previous", p, err, newer)
		p, err = repository.PreviousChangeset(e.ctx, e.d, older)
		check("older.previous", p, err, nil)
		n, err = repository.NextChangeset(e.ctx, e.d, same)
		check("same.next", n, err, nil)
	})
}
