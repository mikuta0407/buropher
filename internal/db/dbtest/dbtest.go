// Package dbtest はテスト用に、マイグレーション済みの新しい DB を 1 テストごとに用意する。
//
//   - SQLite: t.TempDir() 配下のファイル DB (テスト終了時に削除)。
//   - PostgreSQL: 環境変数 BUROPHER_TEST_PG_DSN が設定されている場合のみ。
//     テストごとにランダムなスキーマを作成し search_path をそこに向け、終了時に DROP する。
//     未設定ならテストを Skip する。
//
// 例:
//
//	func TestX(t *testing.T) {
//		dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) { ... })
//	}
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
)

// EnvPGDSN は PostgreSQL テスト用 DSN を与える環境変数名。
const EnvPGDSN = "BUROPHER_TEST_PG_DSN"

// PGDSN は PostgreSQL テスト用 DSN を返す (未設定なら "")。
func PGDSN() string { return os.Getenv(EnvPGDSN) }

// New は既定 dialect (SQLite) のマイグレーション済み DB を返す。
func New(t testing.TB) *db.DB { return NewSQLite(t) }

// NewSQLite はマイグレーション済みの新しい SQLite DB を返す。
func NewSQLite(t testing.TB) *db.DB {
	t.Helper()
	d := OpenSQLite(t)
	migrate(t, d)
	return d
}

// OpenSQLite はマイグレーション前の空の SQLite DB を返す。
func OpenSQLite(t testing.TB) *db.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(context.Background(), string(db.SQLite), path)
	if err != nil {
		t.Fatalf("dbtest: open sqlite: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// NewPostgres はマイグレーション済みの新しい PostgreSQL DB (専用スキーマ) を返す。
// BUROPHER_TEST_PG_DSN が未設定ならテストを Skip する。
func NewPostgres(t testing.TB) *db.DB {
	t.Helper()
	d := OpenPostgres(t)
	migrate(t, d)
	return d
}

// OpenPostgres はマイグレーション前の空スキーマに接続した PostgreSQL DB を返す。
func OpenPostgres(t testing.TB) *db.DB {
	t.Helper()
	base := PGDSN()
	if base == "" {
		t.Skipf("dbtest: %s is not set; skipping PostgreSQL test", EnvPGDSN)
	}
	ctx := context.Background()
	admin, err := db.Open(ctx, string(db.Postgres), base)
	if err != nil {
		t.Fatalf("dbtest: open postgres: %v", err)
	}
	schema := "test_" + randomHex(8)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("dbtest: create schema: %v", err)
	}
	dsn, err := withSearchPath(base, schema)
	if err != nil {
		admin.Close()
		t.Fatalf("dbtest: %v", err)
	}
	d, err := db.Open(ctx, string(db.Postgres), dsn)
	if err != nil {
		admin.Close()
		t.Fatalf("dbtest: open postgres schema: %v", err)
	}
	t.Cleanup(func() {
		d.Close()
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := admin.Exec(cctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("dbtest: drop schema %s: %v", schema, err)
		}
		admin.Close()
	})
	return d
}

// ForEachDialect は SQLite と (DSN があれば) PostgreSQL のそれぞれについて、
// マイグレーション済みの新しい DB でサブテストを実行する。
func ForEachDialect(t *testing.T, fn func(t *testing.T, d *db.DB)) {
	t.Helper()
	t.Run(string(db.SQLite), func(t *testing.T) { fn(t, NewSQLite(t)) })
	t.Run(string(db.Postgres), func(t *testing.T) { fn(t, NewPostgres(t)) })
}

// ForEachDialectEmpty は ForEachDialect と同じだがマイグレーションを行わない。
func ForEachDialectEmpty(t *testing.T, fn func(t *testing.T, d *db.DB)) {
	t.Helper()
	t.Run(string(db.SQLite), func(t *testing.T) { fn(t, OpenSQLite(t)) })
	t.Run(string(db.Postgres), func(t *testing.T) { fn(t, OpenPostgres(t)) })
}

func migrate(t testing.TB, d *db.DB) {
	t.Helper()
	if _, err := db.Migrate(context.Background(), d, db.Up); err != nil {
		t.Fatalf("dbtest: migrate: %v", err)
	}
}

// withSearchPath は DSN (URL / key=value 形式) に search_path を付与する。
// pgx は未知のパラメータをセッションの実行時パラメータとして送る。
func withSearchPath(dsn, schema string) (string, error) {
	if strings.Contains(dsn, "://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return "", err
		}
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		return u.String(), nil
	}
	return dsn + " search_path=" + schema, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
