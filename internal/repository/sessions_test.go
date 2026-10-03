// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/bootstrap"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/repository"
)

func TestSessionStore(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		if err := bootstrap.Init(ctx, d, bootstrap.Options{AdminPassword: "x", SkipDefaultData: true}); err != nil {
			t.Fatal(err)
		}
		var adminID int64
		if err := d.Get(ctx, &adminID, `SELECT principal_id FROM user_accounts WHERE login = 'admin'`); err != nil {
			t.Fatal(err)
		}
		st := repository.SessionStore{DB: d}
		now := time.Now().UTC().Truncate(time.Microsecond)
		rec := &httpx.Record{ID: "abc", UserID: adminID, Data: map[string]any{"_csrf_token": "t"}, CreatedAt: now, UpdatedAt: now,
			ExpiresAt: now.Add(time.Hour), SudoAt: now, IP: "127.0.0.1"}
		if err := st.Save(ctx, rec); err != nil {
			t.Fatal(err)
		}
		rec.Data["x"] = "y"
		if err := st.Save(ctx, rec); err != nil {
			t.Fatal(err)
		}
		got, err := st.Get(ctx, "abc")
		if err != nil || got == nil {
			t.Fatal(got, err)
		}
		if got.UserID != adminID || got.Data["x"] != "y" || !got.SudoAt.Equal(now) || !got.ExpiresAt.Equal(now.Add(time.Hour)) {
			t.Errorf("got %+v", got)
		}
		if err := st.Save(ctx, &httpx.Record{ID: "other", UserID: adminID, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := st.DestroyAllForUser(ctx, adminID, "abc"); err != nil {
			t.Fatal(err)
		}
		if g, _ := st.Get(ctx, "other"); g != nil {
			t.Error("other session should be destroyed")
		}
		if g, _ := st.Get(ctx, "abc"); g == nil {
			t.Error("current session should remain")
		}
		if n, err := st.DeleteExpiredSessions(ctx, now.Add(2*time.Hour)); err != nil || n != 1 {
			t.Errorf("expired deleted = %d, %v", n, err)
		}
		if g, _ := st.Get(ctx, "missing"); g != nil {
			t.Error("missing should be nil")
		}
	})
}
