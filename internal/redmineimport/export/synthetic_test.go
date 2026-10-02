package export

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/redmineimport/archive"
)

// ---- 合成スキーマ: coreTables から各方言の DDL を作り、Redmine 6.1.2 相当の空 DB を作る ----

func ddlType(kind string, c columnDef, table string) string {
	switch c.Type {
	case "integer":
		if c.Name == "filesize" || strings.HasPrefix(table, "oauth_") && (c.Name == "id" || strings.HasSuffix(c.Name, "_id")) {
			if kind == KindSQLite {
				return "integer"
			}
			return "bigint"
		}
		return "integer"
	case "string":
		switch kind {
		case KindSQLite:
			return "varchar"
		case KindSQLServer:
			return "nvarchar(255)"
		}
		return "varchar(255)"
	case "text":
		switch kind {
		case KindMySQL:
			return "longtext"
		case KindSQLServer:
			return "nvarchar(max)"
		}
		return "text"
	case "boolean":
		switch kind {
		case KindMySQL:
			return "tinyint(1)"
		case KindSQLServer:
			return "bit"
		}
		return "boolean"
	case "datetime":
		switch kind {
		case KindMySQL:
			return "datetime(6)"
		case KindPostgres:
			return "timestamp(6) without time zone"
		case KindSQLServer:
			return "datetime2(6)"
		}
		return "datetime(6)"
	case "date":
		return "date"
	case "float":
		switch kind {
		case KindPostgres:
			return "double precision"
		case KindMySQL:
			return "float" // Rails の t.float は MySQL では単精度 FLOAT
		}
		return "float"
	case "binary":
		switch kind {
		case KindPostgres:
			return "bytea"
		case KindMySQL:
			return "longblob"
		case KindSQLServer:
			return "varbinary(max)"
		}
		return "blob"
	}
	panic("unknown type " + c.Type)
}

// createSchema は合成 Redmine 6.1.2 スキーマを作る。
func createSchema(t *testing.T, db *sql.DB, kind string) {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, td := range coreTables {
		var cols []string
		for _, c := range td.Columns {
			def := quoteIdent(kind, c.Name) + " " + ddlType(kind, c, td.Name)
			if c.Name == td.PK {
				def += " PRIMARY KEY"
			}
			cols = append(cols, def)
		}
		exec("CREATE TABLE " + quoteIdent(kind, td.Name) + " (" + strings.Join(cols, ", ") + ")")
	}
	exec("CREATE TABLE schema_migrations (version " + ddlType(kind, columnDef{Name: "version", Type: "string"}, "") + " PRIMARY KEY)")
	exec("CREATE TABLE ar_internal_metadata (" + quoteIdent(kind, "key") + " varchar(255) PRIMARY KEY, value varchar(255))")
	for _, v := range coreMigrations {
		exec("INSERT INTO schema_migrations (version) VALUES ("+ph(kind, 1)+")", v)
	}
}

func ph(kind string, i int) string {
	switch kind {
	case KindPostgres:
		return fmt.Sprintf("$%d", i)
	case KindSQLServer:
		return fmt.Sprintf("@p%d", i)
	}
	return "?"
}

func insert(t *testing.T, db *sql.DB, kind, table string, cols []string, vals ...any) {
	t.Helper()
	var qs, qc []string
	for i, c := range cols {
		qc = append(qc, quoteIdent(kind, c))
		qs = append(qs, ph(kind, i+1))
	}
	q := "INSERT INTO " + quoteIdent(kind, table) + " (" + strings.Join(qc, ", ") + ") VALUES (" + strings.Join(qs, ", ") + ")"
	if _, err := db.Exec(q, vals...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// exportForTest はアーカイブを書き出して全行を読み戻す。
func exportForTest(t *testing.T, opt Options) (*archive.Manifest, map[string][]archive.Row, map[string][]byte) {
	t.Helper()
	m, err := Run(context.Background(), opt)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	r, err := archive.Open(opt.Output)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if !reflect.DeepEqual(r.Manifest().Tables, m.Tables) {
		t.Errorf("manifest read back differs from returned manifest")
	}
	rows := map[string][]archive.Row{}
	files := map[string][]byte{}
	for {
		e, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch e.Kind {
		case archive.KindTable:
			rs := e.Rows()
			for rs.Next() {
				rows[e.Name] = append(rows[e.Name], rs.Row())
			}
			if rs.Err() != nil {
				t.Fatal(rs.Err())
			}
			if int64(len(rows[e.Name])) != e.Table.Rows {
				t.Errorf("%s: manifest rows %d, read %d", e.Name, e.Table.Rows, len(rows[e.Name]))
			}
		case archive.KindFile:
			b, _ := io.ReadAll(e.Reader())
			files[e.Name] = b
		}
	}
	return m, rows, files
}

// populateSynthetic は方言差が出やすい値を入れる。
func populateSynthetic(t *testing.T, db *sql.DB, kind string, boolT, boolF any) {
	insert(t, db, kind, "users", []string{"id", "login", "hashed_password", "firstname", "lastname", "admin", "status", "last_login_on", "created_on", "updated_on", "type", "salt", "must_change_passwd", "twofa_totp_key"},
		1, "admin", "abc", "管理者", "😀", boolT, 1, "2026-10-02 23:28:40.788013", "2026-01-02 03:04:05", "2026-01-02 03:04:05.000000", "User", "s", boolF, "aes-256-cbc:pqpo49Hczr46erH7RDC94Q==--DuJKv+942Aj5ecoapoPf+g==")
	insert(t, db, kind, "users", []string{"id", "login", "hashed_password", "firstname", "lastname", "admin", "status", "type", "must_change_passwd"},
		2, "", "", "", "Anonymous", boolF, 0, "AnonymousUser", boolF)
	insert(t, db, kind, "time_entries", []string{"id", "project_id", "user_id", "issue_id", "hours", "comments", "activity_id", "spent_on", "tyear", "tmonth", "tweek", "created_on", "updated_on", "author_id"},
		1, 1, 1, nil, 0.30000000000000004, "x", 9, "2026-10-02", 2026, 10, 40, "2026-10-02 10:00:00", "2026-10-02 10:00:00", 1)
	insert(t, db, kind, "time_entries", []string{"id", "project_id", "user_id", "hours", "activity_id", "spent_on", "tyear", "tmonth", "tweek", "created_on", "updated_on"},
		2, 1, 1, 2.0, 9, "2026-10-03", 2026, 10, 40, "2026-10-03 10:00:00", "2026-10-03 10:00:00")
	insert(t, db, kind, "wiki_content_versions", []string{"id", "wiki_content_id", "page_id", "data", "compression", "comments", "updated_on", "version"},
		1, 1, 1, []byte{0x78, 0x9c, 0x00, 0xff}, "gzip", "", "2026-10-02 00:00:00", 1)
	insert(t, db, kind, "attachments", []string{"id", "container_id", "container_type", "filename", "disk_filename", "filesize", "content_type", "digest", "downloads", "author_id", "created_on", "disk_directory"},
		1, 1, "Issue", "a.txt", "261003_a.txt", int64(1)<<33, "text/plain", "d", 0, 1, "2026-10-03 00:00:00", "2026/10")
	insert(t, db, kind, "attachments", []string{"id", "container_id", "container_type", "filename", "disk_filename", "filesize", "digest", "downloads", "author_id", "created_on", "disk_directory"},
		2, 1, "Issue", "b.txt", "261003_b.txt", 1, "d", 0, 1, "2026-10-03 00:00:00", "2026/10")
	insert(t, db, kind, "attachments", []string{"id", "container_id", "container_type", "filename", "disk_filename", "filesize", "digest", "downloads", "author_id", "created_on", "disk_directory"},
		3, 1, "Issue", "a-copy.txt", "261003_a.txt", 1, "d", 0, 1, "2026-10-03 00:00:00", "2026/10")
	insert(t, db, kind, "attachments", []string{"id", "container_id", "container_type", "filename", "disk_filename", "filesize", "digest", "downloads", "author_id", "created_on", "disk_directory"},
		4, 1, "Issue", "old.txt", "old.txt", 1, "d", 0, 1, "2008-01-01 00:00:00", nil)
	insert(t, db, kind, "settings", []string{"id", "name", "value", "updated_on"},
		1, "notified_events", "---\n- issue_added\n", "2026-10-03 00:00:00.5")
	insert(t, db, kind, "groups_users", []string{"group_id", "user_id"}, 5, 1)
	insert(t, db, kind, "groups_users", []string{"group_id", "user_id"}, 3, 1)
}

func makeFilesDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "2026", "10"), 0o755)
	os.WriteFile(filepath.Join(dir, "2026", "10", "261003_a.txt"), []byte("A-content"), 0o644)
	os.WriteFile(filepath.Join(dir, "old.txt"), []byte("old"), 0o644)
	return dir
}

func checkSynthetic(t *testing.T, kind string, m *archive.Manifest, rows map[string][]archive.Row, files map[string][]byte) {
	t.Helper()
	if m.Source.DBKind != kind || m.Source.Timezone != "Asia/Tokyo" {
		t.Errorf("source: %+v", m.Source)
	}
	if len(m.SchemaMigrations) != 322 || len(m.PluginMigrations) != 0 {
		t.Errorf("migrations: %d plugins %v", len(m.SchemaMigrations), m.PluginMigrations)
	}
	if len(m.Tables) != 54 {
		t.Errorf("tables = %d, want 54", len(m.Tables))
	}
	for _, x := range []string{"imports", "import_items", "schema_migrations", "ar_internal_metadata"} {
		if _, ok := m.Table(x); ok {
			t.Errorf("%s must not be exported", x)
		}
	}
	u := rows["users"]
	if len(u) != 2 {
		t.Fatalf("users rows %d", len(u))
	}
	want := map[string]any{"id": int64(1), "login": "admin", "admin": true, "must_change_passwd": false, "firstname": "管理者", "lastname": "😀",
		"last_login_on": "2026-10-02 23:28:40.788013", "created_on": "2026-01-02 03:04:05", "updated_on": "2026-01-02 03:04:05", "mail_notification": nil}
	for k, v := range want {
		if !reflect.DeepEqual(u[0][k], v) {
			t.Errorf("users[0].%s = %#v, want %#v", k, u[0][k], v)
		}
	}
	if u[1]["admin"] != false || u[1]["last_login_on"] != nil {
		t.Errorf("users[1] = %#v", u[1])
	}
	te := rows["time_entries"]
	wantHours := 0.30000000000000004
	if kind == KindMySQL {
		wantHours = 0.3 // 単精度 FLOAT に丸められ、最短 10 進表現で読み直される
	}
	if te[0]["hours"] != wantHours || te[1]["hours"] != 2.0 || te[0]["spent_on"] != "2026-10-02" || te[0]["issue_id"] != nil {
		t.Errorf("time_entries = %#v", te)
	}
	if b, ok := rows["wiki_content_versions"][0]["data"].([]byte); !ok || !reflect.DeepEqual(b, []byte{0x78, 0x9c, 0x00, 0xff}) {
		t.Errorf("binary = %#v", rows["wiki_content_versions"][0]["data"])
	}
	if rows["attachments"][0]["filesize"] != int64(1)<<33 {
		t.Errorf("filesize = %#v", rows["attachments"][0]["filesize"])
	}
	if rows["settings"][0]["updated_on"] != "2026-10-03 00:00:00.500000" {
		t.Errorf("settings.updated_on = %#v", rows["settings"][0]["updated_on"])
	}
	// 結合テーブルは全列順
	g := rows["groups_users"]
	if g[0]["group_id"] != int64(3) || g[1]["group_id"] != int64(5) {
		t.Errorf("groups_users order = %#v", g)
	}
	if m.EncryptedValues["users.twofa_totp_key"] != 1 {
		t.Errorf("encrypted = %v", m.EncryptedValues)
	}
	// 添付: a.txt(2 行で共有)と old.txt(ディレクトリなし)は存在、b.txt は欠損
	if len(files) != 2 || string(files["2026/10/261003_a.txt"]) != "A-content" || string(files["old.txt"]) != "old" {
		t.Errorf("files = %v", files)
	}
	if len(m.MissingFiles) != 1 || m.MissingFiles[0].Path != "2026/10/261003_b.txt" || !reflect.DeepEqual(m.MissingFiles[0].AttachmentIDs, []int64{2}) {
		t.Errorf("missing = %+v", m.MissingFiles)
	}
}

func newSQLiteSynthetic(t *testing.T, boolT, boolF any) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "redmine.sqlite3")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	createSchema(t, db, KindSQLite)
	populateSynthetic(t, db, KindSQLite, boolT, boolF)
	return p
}

func TestSyntheticSQLite(t *testing.T) {
	// 新しい Rails は 1/0、古い SQLite DB は 't'/'f' で真偽値を保存する
	for name, b := range map[string][2]any{"int": {1, 0}, "tf": {"t", "f"}} {
		t.Run(name, func(t *testing.T) {
			p := newSQLiteSynthetic(t, b[0], b[1])
			out := filepath.Join(t.TempDir(), "out.tar.zst")
			m, rows, files := exportForTest(t, Options{DSN: "sqlite://" + p, SourceTimezone: "Asia/Tokyo", Output: out, FilesDir: makeFilesDir(t), IncludeFiles: true})
			checkSynthetic(t, KindSQLite, m, rows, files)
			if len(m.Warnings) != 1 || !containsStr(m.Warnings, "1 attachment files referenced by attachments are missing") {
				t.Errorf("warnings: %v", m.Warnings)
			}
		})
	}
}

func TestSyntheticSQLiteNoFiles(t *testing.T) {
	p := newSQLiteSynthetic(t, 1, 0)
	out := filepath.Join(t.TempDir(), "out.tar.zst")
	m, _, files := exportForTest(t, Options{DSN: "sqlite:" + p, SourceTimezone: "UTC", Output: out, FilesDir: makeFilesDir(t), IncludeFiles: false})
	if len(files) != 0 || len(m.Files) != 0 || m.IncludeFiles {
		t.Errorf("files should not be included: %v", m.Files)
	}
	if len(m.MissingFiles) != 1 {
		t.Errorf("missing files should still be checked: %+v", m.MissingFiles)
	}
	// 出力が既に存在する場合は Overwrite なしでは失敗
	if _, err := Run(context.Background(), Options{DSN: "sqlite:" + p, SourceTimezone: "UTC", Output: out}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected exists error, got %v", err)
	}
	// 添付を確認しない場合は警告
	m, err := Run(context.Background(), Options{DSN: "sqlite:" + p, SourceTimezone: "UTC", Output: out, Overwrite: true})
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(m.Warnings, "attachment files were not checked") {
		t.Errorf("warnings = %v", m.Warnings)
	}
}

func containsStr(ss []string, sub string) bool {
	for _, s := range ss {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func TestAcceptance(t *testing.T) {
	mod := func(t *testing.T, stmts ...string) string {
		p := newSQLiteSynthetic(t, 1, 0)
		db, _ := sql.Open("sqlite", p)
		defer db.Close()
		for _, s := range stmts {
			if _, err := db.Exec(s); err != nil {
				t.Fatal(s, err)
			}
		}
		return p
	}
	run := func(p string, force bool) (*archive.Manifest, error) {
		return Run(context.Background(), Options{DSN: "sqlite://" + p, SourceTimezone: "Asia/Tokyo", Output: filepath.Join(filepath.Dir(p), fmt.Sprintf("o%d.tar.zst", rand.Int())), Force: force})
	}
	cases := []struct {
		name    string
		stmts   []string
		wantErr string // 空なら成功(警告を確認)
		warn    string
	}{
		{"missing migration", []string{"DELETE FROM schema_migrations WHERE version = '20250611092227'"}, "core migrations of Redmine 6.1.2 are missing", ""},
		{"extra core migration", []string{"INSERT INTO schema_migrations VALUES ('20260101000000')"}, "unknown to Redmine 6.1.2", ""},
		{"missing table", []string{"DROP TABLE reactions"}, "core table reactions is missing", ""},
		{"missing column", []string{"ALTER TABLE issues DROP COLUMN closed_on"}, "column issues.closed_on is missing", ""},
		{"plugin", []string{"INSERT INTO schema_migrations VALUES ('1-redmine_agile')", "INSERT INTO schema_migrations VALUES ('2-redmine_agile')",
			"CREATE TABLE agile_data (id integer)", "ALTER TABLE issues ADD COLUMN agile_points integer"}, "", "plugin \"redmine_agile\" has 2 migrations"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := mod(t, c.stmts...)
			m, err := run(p, false)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want %q", err, c.wantErr)
				}
				// --force では続行し、警告とフラグを残す
				m, err = run(p, true)
				if err != nil {
					t.Fatalf("forced: %v", err)
				}
				if !m.Source.AcceptanceForced || !containsStr(m.Warnings, c.wantErr) {
					t.Errorf("forced manifest: %+v %v", m.Source, m.Warnings)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !containsStr(m.Warnings, c.warn) || !containsStr(m.Warnings, "agile_data") || !containsStr(m.Warnings, "agile_points") {
				t.Errorf("warnings = %v", m.Warnings)
			}
			if len(m.PluginMigrations) != 1 || m.PluginMigrations[0].Plugin != "redmine_agile" || len(m.SchemaMigrations) != 324 {
				t.Errorf("plugin migrations = %+v", m.PluginMigrations)
			}
		})
	}
	// 欠落列を --force で書き出すと null になる
	p := mod(t, "ALTER TABLE users DROP COLUMN twofa_totp_key")
	out := filepath.Join(filepath.Dir(p), "forced.tar.zst")
	_, rows, _ := exportForTest(t, Options{DSN: "sqlite://" + p, SourceTimezone: "UTC", Output: out, Force: true})
	if v, ok := rows["users"][0]["twofa_totp_key"]; !ok || v != nil {
		t.Errorf("forced missing column = %#v", rows["users"][0])
	}
}

func TestOptionsValidation(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		opt  Options
		want string
	}{
		{Options{DSN: "sqlite:///x", SourceTimezone: ""}, "timezone is required"},
		{Options{DSN: "sqlite:///x", SourceTimezone: "Local"}, "explicit IANA"},
		{Options{DSN: "sqlite:///x", SourceTimezone: "Mars/Olympus"}, "invalid source timezone"},
		{Options{DSN: "foo", SourceTimezone: "UTC"}, "cannot detect"},
		{Options{DSN: "sqlite:///x", SourceTimezone: "UTC", IncludeFiles: true}, "files dir is required"},
	} {
		if _, err := Run(ctx, c.opt); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: err = %v, want %q", c.opt, err, c.want)
		}
	}
}

// 合成スキーマを PostgreSQL / MySQL でも検証する(DSN が環境変数にある場合のみ)。
//
//	BUROPHER_TEST_PG_DSN=postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable
//	BUROPHER_TEST_MYSQL_DSN=mysql://root:pass@127.0.0.1:3306/
func TestSyntheticPostgres(t *testing.T) {
	dsn := os.Getenv("BUROPHER_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("BUROPHER_TEST_PG_DSN not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := fmt.Sprintf("buropher_export_test_%d", rand.Int63())
	if _, err := db.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	defer db.Exec("DROP SCHEMA " + schema + " CASCADE")
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	sdsn := dsn + sep + "search_path=" + schema
	sdb, err := sql.Open("pgx", sdsn)
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	sdb.SetMaxOpenConns(1)
	createSchema(t, sdb, KindPostgres)
	populateSynthetic(t, sdb, KindPostgres, true, false)
	out := filepath.Join(t.TempDir(), "pg.tar.zst")
	m, rows, files := exportForTest(t, Options{DSN: sdsn, SourceTimezone: "Asia/Tokyo", Output: out, FilesDir: makeFilesDir(t), IncludeFiles: true})
	checkSynthetic(t, KindPostgres, m, rows, files)
}

func TestSyntheticMySQL(t *testing.T) {
	dsn := os.Getenv("BUROPHER_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("BUROPHER_TEST_MYSQL_DSN not set")
	}
	ci, err := resolveConn(dsn, "")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("mysql", ci.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	name := fmt.Sprintf("buropher_export_test_%d", rand.Int63())
	if _, err := db.Exec("CREATE DATABASE " + name + " CHARACTER SET utf8mb4"); err != nil {
		t.Fatal(err)
	}
	defer db.Exec("DROP DATABASE " + name)
	base := strings.TrimRight(strings.SplitN(dsn, "?", 2)[0], "/")
	if i := strings.LastIndex(base, "/"); i > len("mysql://") {
		base = base[:i]
	}
	sdsn := base + "/" + name
	sci, _ := resolveConn(sdsn, "")
	sdb, err := sql.Open("mysql", sci.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	createSchema(t, sdb, KindMySQL)
	populateSynthetic(t, sdb, KindMySQL, 1, 0)
	out := filepath.Join(t.TempDir(), "my.tar.zst")
	m, rows, files := exportForTest(t, Options{DSN: sdsn, SourceTimezone: "Asia/Tokyo", Output: out, FilesDir: makeFilesDir(t), IncludeFiles: true})
	checkSynthetic(t, KindMySQL, m, rows, files)
}

// SQL Server(BUROPHER_TEST_MSSQL_DSN=sqlserver://sa:pass@127.0.0.1:1433 の場合のみ)。
// スナップショット分離が無効な DB では READ COMMITTED へのフォールバック警告、有効化後は警告なしを確認する。
func TestSyntheticSQLServer(t *testing.T) {
	dsn := os.Getenv("BUROPHER_TEST_MSSQL_DSN")
	if dsn == "" {
		t.Skip("BUROPHER_TEST_MSSQL_DSN not set")
	}
	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	name := fmt.Sprintf("buropher_export_test_%d", rand.Int63())
	if _, err := db.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	defer db.Exec("ALTER DATABASE " + name + " SET SINGLE_USER WITH ROLLBACK IMMEDIATE; DROP DATABASE " + name)
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	sdsn := dsn + sep + "database=" + name
	sdb, err := sql.Open("sqlserver", sdsn)
	if err != nil {
		t.Fatal(err)
	}
	createSchema(t, sdb, KindSQLServer)
	populateSynthetic(t, sdb, KindSQLServer, true, false)
	sdb.Close()
	files := makeFilesDir(t)
	m, rows, gotFiles := exportForTest(t, Options{DSN: sdsn, SourceTimezone: "Asia/Tokyo", Output: filepath.Join(t.TempDir(), "ms1.tar.zst"), FilesDir: files, IncludeFiles: true})
	checkSynthetic(t, KindSQLServer, m, rows, gotFiles)
	if !containsStr(m.Warnings, "snapshot isolation is not available") {
		t.Errorf("expected snapshot fallback warning: %v", m.Warnings)
	}
	if _, err := db.Exec("ALTER DATABASE " + name + " SET ALLOW_SNAPSHOT_ISOLATION ON"); err != nil {
		t.Fatal(err)
	}
	m, rows, gotFiles = exportForTest(t, Options{DSN: sdsn, SourceTimezone: "Asia/Tokyo", Output: filepath.Join(t.TempDir(), "ms2.tar.zst"), FilesDir: files, IncludeFiles: true})
	checkSynthetic(t, KindSQLServer, m, rows, gotFiles)
	if containsStr(m.Warnings, "snapshot isolation") {
		t.Errorf("unexpected snapshot warning: %v", m.Warnings)
	}
}
