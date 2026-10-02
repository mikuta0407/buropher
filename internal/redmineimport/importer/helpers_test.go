package importer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/redmineimport/export"
)

// 実データでの結合テストは _reference/(リポジトリ外)を使い、無ければスキップする。
//   redmine-fixtures/db/redmine.pristine.sqlite3 … Redmine 公式テストフィクスチャを読み込んだ DB(読み取り専用、コピーして使う)
//   redmine-fixtures/test/fixtures/files        … その添付
//   export-test/rich.sqlite3 + rich-files/      … export パッケージの結合テスト用 DB(POPULATE_CIPHER_KEY=export-test-key)
// 一時ファイルは _reference/import-test/ 以下に作る(/tmp は小さい tmpfs のため)。

func findReference(t *testing.T) string {
	t.Helper()
	if d := os.Getenv("BUROPHER_TEST_REFERENCE_DIR"); d != "" {
		return d
	}
	wd, _ := os.Getwd()
	for d := wd; ; d = filepath.Dir(d) {
		c := filepath.Join(d, "_reference")
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			return c
		}
		// worktree の場合はリポジトリ本体の _reference を探す
		if filepath.Dir(d) == d {
			break
		}
	}
	t.Skip("_reference not found")
	return ""
}

// workDir は _reference/import-test 以下にテスト用ディレクトリを作る。
func workDir(t *testing.T, ref string) string {
	t.Helper()
	b := make([]byte, 4)
	rand.Read(b)
	d := filepath.Join(ref, "import-test", "t-"+hex.EncodeToString(b))
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !t.Failed() || os.Getenv("BUROPHER_TEST_KEEP") == "" {
			os.RemoveAll(d)
		}
	})
	return d
}

func copyTo(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	out.Close()
}

// exportFixture は Redmine DB(のコピー)をアーカイブに書き出す。
func exportFixture(t *testing.T, dir, srcDB, filesDir, tz string, includeFiles bool) string {
	t.Helper()
	cp := filepath.Join(dir, "source.sqlite3")
	copyTo(t, srcDB, cp)
	out := filepath.Join(dir, "export.tar.zst")
	_, err := export.Run(context.Background(), export.Options{
		DSN: "sqlite://" + cp, SourceTimezone: tz, Output: out, FilesDir: filesDir, IncludeFiles: includeFiles, TempDir: dir,
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	return out
}

// newTarget はマイグレーション済みの空の SQLite DB を作る。
func newTarget(t *testing.T, dir string) *db.DB {
	t.Helper()
	d, err := db.Open(context.Background(), "sqlite", filepath.Join(dir, "target.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(context.Background(), d, db.Up); err != nil {
		t.Fatal(err)
	}
	return d
}

// targets は SQLite と(BUROPHER_TEST_PG_DSN があれば)PostgreSQL の空 DB でサブテストを実行する。
func targets(t *testing.T, dir string, fn func(t *testing.T, d *db.DB, filesDir string)) {
	t.Run("sqlite", func(t *testing.T) {
		sub := filepath.Join(dir, "sqlite")
		os.MkdirAll(sub, 0o755)
		fn(t, newTarget(t, sub), filepath.Join(sub, "files"))
	})
	t.Run("postgres", func(t *testing.T) {
		d := dbtest.NewPostgres(t)
		sub := filepath.Join(dir, "pg")
		os.MkdirAll(sub, 0o755)
		fn(t, d, filepath.Join(sub, "files"))
	})
}

// fixturesNow は Redmine フィクスチャ DB の凍結時刻。
var fixturesNow = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
