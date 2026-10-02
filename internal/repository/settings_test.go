package repository

import (
	"context"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/settings"
)

func TestSettingsStore(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		s, err := settings.New(ctx, SettingsStore{DB: d})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Set(ctx, "app_title", "Tracker"); err != nil {
			t.Fatal(err)
		}
		if err := s.Set(ctx, "app_title", "Tracker2"); err != nil {
			t.Fatal(err)
		}
		if err := s.Set(ctx, "issue_list_default_columns", []any{"status", "subject"}); err != nil {
			t.Fatal(err)
		}
		s2, err := settings.New(ctx, SettingsStore{DB: d})
		if err != nil {
			t.Fatal(err)
		}
		if s2.String("app_title") != "Tracker2" || len(s2.Strings("issue_list_default_columns")) != 2 {
			t.Errorf("reload mismatch: %q %v", s2.String("app_title"), s2.Strings("issue_list_default_columns"))
		}
	})
}
