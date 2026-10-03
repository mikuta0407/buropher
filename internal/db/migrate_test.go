// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package db_test

import (
	"context"
	"slices"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
)

// 初期スキーマで作られるべきテーブル (docs/schema.md と対応)。
var expectedTables = []string{
	"auth_sources", "principals", "user_accounts", "email_addresses", "group_users",
	"auth_source_group_mappings", "user_identities", "tokens", "twofa_backup_codes", "sessions",
	"issue_statuses", "trackers", "projects", "project_closure", "project_modules", "project_trackers",
	"issue_priorities", "document_categories", "time_entry_activities",
	"roles", "role_permissions", "role_permission_trackers", "roles_managed_roles",
	"members", "member_roles", "user_notified_projects",
	"custom_fields", "custom_field_enumerations", "custom_fields_projects", "custom_fields_roles",
	"custom_fields_trackers", "custom_values",
	"workflow_transitions", "workflow_field_rules",
	"versions", "issue_categories", "issues", "issue_relations", "issue_journals", "issue_journal_details",
	"watchers", "time_entries",
	"wikis", "wiki_pages", "wiki_page_versions", "wiki_redirects",
	"boards", "messages", "news", "news_comments", "documents", "reactions", "attachments",
	"queries", "queries_roles",
	"user_preferences", "user_project_bookmarks", "user_recent_projects", "user_notification_settings",
	"settings", "legacy_settings",
	"repositories", "changesets", "changeset_files", "changeset_parents", "changesets_issues",
	"imports", "import_items",
	"oauth_applications", "oauth_access_grants", "oauth_access_tokens",
	"jobs", "notification_deliveries", "discord_dm_channels",
}

func listTables(t *testing.T, d *db.DB) []string {
	t.Helper()
	var q string
	switch d.Dialect().Name() {
	case db.SQLite:
		q = "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name <> ?"
	case db.Postgres:
		q = "SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_type = 'BASE TABLE' AND table_name <> ?"
	}
	var names []string
	if err := d.Select(context.Background(), &names, q, db.MigrationTable); err != nil {
		t.Fatalf("list tables: %v", err)
	}
	slices.Sort(names)
	return names
}

func TestMigrateUpDownUp(t *testing.T) {
	dbtest.ForEachDialectEmpty(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		want := slices.Clone(expectedTables)
		slices.Sort(want)

		pending, err := db.HasPending(ctx, d)
		if err != nil || !pending {
			t.Fatalf("HasPending = %v, %v; want true", pending, err)
		}

		res, err := db.Migrate(ctx, d, db.Up)
		if err != nil {
			t.Fatalf("up: %v", err)
		}
		if len(res) == 0 {
			t.Fatal("up applied nothing")
		}
		if got := listTables(t, d); !slices.Equal(got, want) {
			t.Fatalf("tables after up:\n got %v\nwant %v", got, want)
		}
		st, err := db.Status(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range st {
			if !s.Applied {
				t.Errorf("migration %d not applied", s.Version)
			}
		}
		// 2 回目の up は何もしない
		if res, err := db.Migrate(ctx, d, db.Up); err != nil || len(res) != 0 {
			t.Fatalf("second up = %v, %v", res, err)
		}

		if _, err := db.Migrate(ctx, d, db.DownAll); err != nil {
			t.Fatalf("down all: %v", err)
		}
		if got := listTables(t, d); len(got) != 0 {
			t.Fatalf("tables after down: %v", got)
		}
		v, err := db.Version(ctx, d)
		if err != nil || v != 0 {
			t.Fatalf("version after down = %d, %v", v, err)
		}

		if _, err := db.Migrate(ctx, d, db.Up); err != nil {
			t.Fatalf("re-up: %v", err)
		}
		if got := listTables(t, d); !slices.Equal(got, want) {
			t.Fatalf("tables after re-up: %v", got)
		}
	})
}

func TestMigrationVersionsMatch(t *testing.T) {
	// 両 dialect で同じバージョン番号・ファイル名のマイグレーションが揃っていること。
	names := map[db.DialectName][]string{}
	for _, n := range []db.DialectName{db.SQLite, db.Postgres} {
		fsys, err := db.MigrationFS(n)
		if err != nil {
			t.Fatal(err)
		}
		d, err := readDirNames(fsys)
		if err != nil {
			t.Fatal(err)
		}
		names[n] = d
	}
	if !slices.Equal(names[db.SQLite], names[db.Postgres]) {
		t.Fatalf("migration files differ: sqlite=%v postgres=%v", names[db.SQLite], names[db.Postgres])
	}
	if len(names[db.SQLite]) == 0 {
		t.Fatal("no migrations")
	}
}
