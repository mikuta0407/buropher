package bootstrap

import (
	"context"
	"errors"
	"testing"

	"github.com/mikuta0407/buropher/internal/auth/password"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
)

func TestInit(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		if err := Init(ctx, d, Options{Language: "ja", AdminPassword: "secret123"}); err != nil {
			t.Fatal(err)
		}
		counts := map[string]int{
			"principals": 4, "roles": 5, "issue_statuses": 6, "trackers": 3,
			"workflow_transitions": 144, "issue_priorities": 5, "queries": 7,
			"document_categories": 2, "time_entry_activities": 2,
		}
		for table, want := range counts {
			var n int
			if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM `+table); err != nil {
				t.Fatal(err)
			}
			if n != want {
				t.Errorf("%s = %d, want %d", table, n, want)
			}
		}
		var name string
		if err := d.Get(ctx, &name, `SELECT name FROM trackers WHERE position = 1`); err != nil || name != "バグ" {
			t.Errorf("tracker name = %q, %v", name, err)
		}
		var hash string
		if err := d.Get(ctx, &hash, `SELECT password_hash FROM user_accounts WHERE login = 'admin'`); err != nil {
			t.Fatal(err)
		}
		if ok, _ := password.Verify(hash, "secret123"); !ok {
			t.Error("admin password mismatch")
		}
		var pos []string
		if err := d.Select(ctx, &pos, `SELECT position_name FROM issue_priorities ORDER BY position`); err != nil {
			t.Fatal(err)
		}
		want := []string{"lowest", "default", "high3", "high2", "highest"}
		for i := range want {
			if pos[i] != want[i] {
				t.Errorf("position names = %v, want %v", pos, want)
				break
			}
		}
		if err := Init(ctx, d, Options{AdminPassword: "x"}); !errors.Is(err, ErrAlreadyInitialized) {
			t.Errorf("second init err = %v", err)
		}
	})
}
