package query

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/testfixtures"
)

// frozenNow は参照環境の Redmine (COMPAT_FROZEN_TIME) と同じ固定時刻。
var frozenNow = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

// testDB はフィクスチャ全件を frozenNow 基準で投入した DB。
type testDB struct {
	t  *testing.T
	d  *db.DB
	st *settings.Settings
}

func newTestDB(t *testing.T, d *db.DB) *testDB {
	t.Helper()
	testfixtures.LoadAt(t, d, frozenNow, testfixtures.All()...)
	st, err := settings.New(context.Background(), repository.SettingsStore{DB: d})
	if err != nil {
		t.Fatal(err)
	}
	return &testDB{t: t, d: d, st: st}
}

// forEachDB は両 dialect でフィクスチャ投入済み DB を用意してサブテストを実行する。
func forEachDB(t *testing.T, fn func(t *testing.T, tdb *testDB)) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) { fn(t, newTestDB(t, d)) })
}

func (tdb *testDB) setting(name string, v any) {
	tdb.t.Helper()
	if err := tdb.st.Set(context.Background(), name, v); err != nil {
		tdb.t.Fatal(err)
	}
}

// user は id のユーザ (0 なら匿名ユーザ) を返す。
func (tdb *testDB) user(id int64) *domain.User {
	tdb.t.Helper()
	ctx := context.Background()
	var u *domain.User
	var err error
	if id == 0 {
		u, err = repository.AnonymousUser(ctx, tdb.d)
	} else {
		u, err = repository.GetUser(ctx, tdb.d, id)
	}
	if err != nil {
		tdb.t.Fatalf("user %d: %v", id, err)
	}
	return u
}

// env は User.current = id の評価環境 (時刻は frozenNow、サーバのタイムゾーンは UTC)。
func (tdb *testDB) env(id int64) *Env {
	tdb.t.Helper()
	e, err := NewEnv(context.Background(), tdb.d, tdb.user(id), tdb.st)
	if err != nil {
		tdb.t.Fatal(err)
	}
	e.Now = func() time.Time { return frozenNow }
	e.ServerLocation = time.UTC
	// フィクスチャの lft (projects.yml)。buropher の既定のツリー順 (名前のバイト順) では
	// ルートの OnlineStore と eCookbook の順が入れ替わるため、比較用にフィクスチャの値を使う。
	e.ProjectNestedSet = func(context.Context) (map[int64]repository.NestedSetValue, error) {
		return map[int64]repository.NestedSetValue{1: {Lft: 1, Rgt: 10}, 2: {Lft: 11, Rgt: 12}, 3: {Lft: 6, Rgt: 7},
			4: {Lft: 8, Rgt: 9}, 5: {Lft: 2, Rgt: 5}, 6: {Lft: 3, Rgt: 4}}, nil
	}
	return e
}

func (tdb *testDB) project(id int64) *domain.Project {
	tdb.t.Helper()
	p, err := repository.GetProject(context.Background(), tdb.d, id)
	if err != nil {
		tdb.t.Fatalf("project %d: %v", id, err)
	}
	return p
}

// newQuery は User.current = userID のクエリ (project が 0 なら全プロジェクト)。
func (tdb *testDB) newQuery(userID int64, kind Kind, projectID int64) *Query {
	tdb.t.Helper()
	var p *domain.Project
	if projectID != 0 {
		p = tdb.project(projectID)
	}
	q, err := New(context.Background(), tdb.env(userID), kind, p)
	if err != nil {
		tdb.t.Fatal(err)
	}
	return q
}

func (tdb *testDB) exec(sql string, args ...any) {
	tdb.t.Helper()
	if _, err := tdb.d.Exec(context.Background(), sql, args...); err != nil {
		tdb.t.Fatalf("%s: %v", sql, err)
	}
}

func (tdb *testDB) ints(sql string, args ...any) []int64 {
	tdb.t.Helper()
	var out []int64
	if err := tdb.d.Select(context.Background(), &out, sql, args...); err != nil {
		tdb.t.Fatalf("%s: %v", sql, err)
	}
	return out
}

func mustFilter(t *testing.T, q *Query, field, op string, values ...string) {
	t.Helper()
	if values == nil {
		values = []string{""}
	}
	if err := q.AddFilter(context.Background(), field, op, values); err != nil {
		t.Fatal(err)
	}
	if !q.HasFilter(field) {
		t.Fatalf("filter %s is not available", field)
	}
}

// issueIDs は find_issues_with_query 相当 (statement を満たすチケット id。可視性は見ない)。
func findIssueIDs(t *testing.T, q *Query) []int64 {
	t.Helper()
	ctx := context.Background()
	st, args, err := q.Statement(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s := `SELECT issues.id FROM issues JOIN issue_statuses ON issue_statuses.id = issues.status_id
JOIN trackers ON trackers.id = issues.tracker_id JOIN projects ON projects.id = issues.project_id
JOIN issue_priorities ON issue_priorities.id = issues.priority_id`
	if st != "" {
		s += " WHERE " + st
	}
	s += " ORDER BY issues.id"
	var ids []int64
	if err := q.env.Q.Select(ctx, &ids, s, args...); err != nil {
		t.Fatalf("%v\n%s", err, s)
	}
	return ids
}

func queryIDs(t *testing.T, q *Query) []int64 {
	t.Helper()
	ids, err := q.IDs(context.Background(), ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

func sorted(ids []int64) []int64 {
	out := slices.Clone(ids)
	slices.Sort(out)
	return out
}

func assertIDs(t *testing.T, got, want []int64) {
	t.Helper()
	if !slices.Equal(sorted(got), sorted(want)) {
		t.Errorf("ids = %v, want %v", sorted(got), sorted(want))
	}
}
