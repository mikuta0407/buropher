package export

import (
	"bytes"
	"compress/zlib"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/redmineimport/archive"
	"github.com/mikuta0407/buropher/internal/redmineimport/rediscipher"
	"github.com/mikuta0407/buropher/internal/redmineimport/rubyyaml"
)

// 実際の Redmine 6.1.2 が作った DB での結合テスト。
// _reference/export-test/ 以下(リポジトリ外)に置いた DB コピーを使い、無ければスキップする。
//   sample.sqlite3 … redmine:load_default_data + サンプルデータ
//   rich.sqlite3 + rich-files/ … sample に testdata/populate.rb を適用したもの
//     (POPULATE_CIPHER_KEY=export-test-key)
// 別の場所は BUROPHER_TEST_EXPORT_DIR で指定できる。

func findExportTestDir(t *testing.T) string {
	t.Helper()
	if d := os.Getenv("BUROPHER_TEST_EXPORT_DIR"); d != "" {
		return d
	}
	wd, _ := os.Getwd()
	for d := wd; ; d = filepath.Dir(d) {
		c := filepath.Join(d, "_reference", "export-test")
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			return c
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	t.Skip("_reference/export-test not found")
	return ""
}

var (
	reNaiveDatetime = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(\.\d{6})?$`)
	reNaiveDate     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// yamlColumns は Redmine が YAML シリアライズする列(settings.value は一部キーのみ)。
var yamlColumns = map[string][]string{
	"settings":         {"value"},
	"user_preferences": {"others"},
	"queries":          {"filters", "column_names", "sort_criteria", "options"},
	"roles":            {"permissions", "settings"},
	"custom_fields":    {"possible_values", "format_store"},
	"repositories":     {"extra_info"},
}

// checkRealExport は書き出し元と件数・型・YAML 可読性を照合する。
func checkRealExport(t *testing.T, dbPath string, m *archive.Manifest, rows map[string][]archive.Row) {
	t.Helper()
	src, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if len(m.SchemaMigrations) != 322 || len(m.PluginMigrations) != 0 || m.Source.AcceptanceForced {
		t.Errorf("migrations %d plugins %v forced %v", len(m.SchemaMigrations), m.PluginMigrations, m.Source.AcceptanceForced)
	}
	if len(m.Tables) != 54 {
		t.Errorf("tables %d", len(m.Tables))
	}
	types := map[string]map[string]string{}
	for _, td := range coreTables {
		types[td.Name] = map[string]string{}
		for _, c := range td.Columns {
			types[td.Name][c.Name] = c.Type
		}
	}
	for _, tb := range m.Tables {
		var n int64
		if err := src.QueryRow("SELECT COUNT(*) FROM " + quoteIdent(KindSQLite, tb.Name)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != tb.Rows || int64(len(rows[tb.Name])) != n {
			t.Errorf("%s: source %d, manifest %d, archive %d", tb.Name, n, tb.Rows, len(rows[tb.Name]))
		}
		for _, r := range rows[tb.Name] {
			if len(r) != len(tb.Columns) {
				t.Fatalf("%s: row has %d columns, want %d", tb.Name, len(r), len(tb.Columns))
			}
			for col, v := range r {
				if v == nil {
					continue
				}
				ok := true
				switch types[tb.Name][col] {
				case "integer":
					_, ok = v.(int64)
				case "float":
					_, ok = v.(float64)
				case "boolean":
					_, ok = v.(bool)
				case "datetime":
					s, isStr := v.(string)
					ok = isStr && reNaiveDatetime.MatchString(s)
				case "date":
					s, isStr := v.(string)
					ok = isStr && reNaiveDate.MatchString(s)
				case "binary":
					_, ok = v.([]byte)
				default:
					_, ok = v.(string)
				}
				if !ok {
					t.Errorf("%s.%s: unexpected value %#v for type %s", tb.Name, col, v, types[tb.Name][col])
				}
			}
		}
	}
	// YAML 列は全て rubyyaml で読めること
	nYAML := 0
	for table, cols := range yamlColumns {
		for _, r := range rows[table] {
			for _, c := range cols {
				s, _ := r[c].(string)
				if !strings.HasPrefix(s, "---") {
					continue
				}
				if _, err := rubyyaml.Decode(s); err != nil {
					t.Errorf("%s.%s id=%v: %v\n%s", table, c, r["id"], err, s)
				}
				nYAML++
			}
		}
	}
	if nYAML < 20 {
		t.Errorf("only %d YAML values decoded", nYAML)
	}
}

func TestRealSampleSQLite(t *testing.T) {
	dir := findExportTestDir(t)
	p := filepath.Join(dir, "sample.sqlite3")
	if _, err := os.Stat(p); err != nil {
		t.Skip("sample.sqlite3 not found")
	}
	out := filepath.Join(t.TempDir(), "sample.tar.zst")
	opt := Options{DSN: "sqlite://" + p, SourceTimezone: "Asia/Tokyo", Output: out}
	// Redmine ソースがあれば --redmine-root も検証
	if root := filepath.Join(filepath.Dir(dir), "redmine"); fileExists(filepath.Join(root, "lib", "redmine", "version.rb")) {
		opt.RedmineRoot = root
	}
	m, rows, _ := exportForTest(t, opt)
	checkRealExport(t, p, m, rows)
	if opt.RedmineRoot != "" {
		if m.Source.RedmineVersion != "6.1.2.stable" || m.Source.CipherKeyConfigured == nil {
			t.Errorf("redmine root info: %+v", m.Source)
		}
	}
	// サンプルデータ: 既定データ(トラッカー 3, ステータス 6, ロール 5, ワークフロー 144)
	for table, want := range map[string]int{"trackers": 3, "issue_statuses": 6, "roles": 5, "workflows": 144} {
		if len(rows[table]) != want {
			t.Errorf("%s rows %d, want %d", table, len(rows[table]), want)
		}
	}
	t.Logf("sample: %d tables, warnings %v", len(m.Tables), m.Warnings)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func TestRealRichSQLite(t *testing.T) {
	dir := findExportTestDir(t)
	p := filepath.Join(dir, "rich.sqlite3")
	files := filepath.Join(dir, "rich-files")
	if !fileExists(p) || !fileExists(files) {
		t.Skip("rich.sqlite3 / rich-files not found")
	}
	out := filepath.Join(t.TempDir(), "rich.tar.zst")
	m, rows, gotFiles := exportForTest(t, Options{DSN: "sqlite://" + p, SourceTimezone: "Asia/Tokyo", Output: out, FilesDir: files, IncludeFiles: true})
	checkRealExport(t, p, m, rows)

	byID := func(table string, id int64) archive.Row {
		for _, r := range rows[table] {
			if r["id"] == id {
				return r
			}
		}
		return nil
	}
	find := func(table, col, val string) archive.Row {
		for _, r := range rows[table] {
			if r[col] == val {
				return r
			}
		}
		t.Fatalf("%s.%s=%s not found", table, col, val)
		return nil
	}
	// 暗号化列: 値はそのまま書き出され、既知の鍵で復号できる
	const key = "export-test-key"
	for _, c := range []struct{ table, findCol, findVal, col, plain string }{
		{"users", "login", "taro", "twofa_totp_key", "JBSWY3DPEHPK3PXP"},
		{"repositories", "login", "svnuser", "password", "svn-secret"},
		{"auth_sources", "name", "LDAP", "account_password", "ldap-secret"},
	} {
		v, _ := find(c.table, c.findCol, c.findVal)[c.col].(string)
		got, err := rediscipher.Decrypt(key, v)
		if err != nil || got != c.plain {
			t.Errorf("%s.%s: decrypt(%q) = %q, %v", c.table, c.col, v, got, err)
		}
		if m.EncryptedValues[c.table+"."+c.col] != 1 {
			t.Errorf("encrypted count %s.%s = %d", c.table, c.col, m.EncryptedValues[c.table+"."+c.col])
		}
	}
	// Wiki 旧版: compression=gzip は zlib deflate、'' は生テキスト(BLOB)
	for _, r := range rows["wiki_content_versions"] {
		data := r["data"].([]byte)
		text := string(data)
		if r["compression"] == "gzip" {
			zr, err := zlib.NewReader(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("version %v: %v", r["version"], err)
			}
			b, _ := io.ReadAll(zr)
			text = string(b)
		}
		if !strings.Contains(text, "本文 v") {
			t.Errorf("wiki version %v text = %q", r["version"], text)
		}
	}
	// user_preferences.others(シンボルキー)
	taro := find("users", "login", "taro")
	for _, r := range rows["user_preferences"] {
		if r["user_id"] == taro["id"] {
			v, err := rubyyaml.Decode(r["others"].(string))
			if err != nil {
				t.Fatal(err)
			}
			mm := v.(map[string]any)
			if mm["no_self_notified"] != "1" || mm["gantt_zoom"] != int64(2) {
				t.Errorf("others = %#v", mm)
			}
		}
	}
	// クエリ: column_names はシンボル配列
	q := find("queries", "name", "自分のチケット")
	cn, _ := rubyyaml.Decode(q["column_names"].(string))
	if arr, ok := cn.([]any); !ok || len(arr) != 4 || arr[0] != rubyyaml.Symbol("tracker") {
		t.Errorf("column_names = %#v", cn)
	}
	// 真偽値と小数
	if find("users", "login", "admin")["admin"] != true || taro["admin"] != false {
		t.Errorf("admin flags wrong")
	}
	if rows["time_entries"][0]["hours"] != 0.30000000000000004 {
		t.Errorf("hours = %#v", rows["time_entries"][0]["hours"])
	}
	// 添付: 重複排除で共有されたファイルは 1 回だけ、gone.txt は欠損として記録
	if len(gotFiles) != len(m.Files) || len(m.Files) != 2 {
		t.Errorf("files: %v", m.Files)
	}
	for _, f := range m.Files {
		want, _ := os.ReadFile(filepath.Join(files, f.Path))
		if !bytes.Equal(gotFiles[f.Path], want) {
			t.Errorf("file %s content mismatch", f.Path)
		}
	}
	if len(m.MissingFiles) != 1 || !strings.HasSuffix(m.MissingFiles[0].Path, "_gone.txt") || len(m.MissingFiles[0].AttachmentIDs) != 1 {
		t.Errorf("missing: %+v", m.MissingFiles)
	} else if a := byID("attachments", m.MissingFiles[0].AttachmentIDs[0]); a == nil || a["filename"] != "gone.txt" {
		t.Errorf("missing attachment row = %#v", a)
	}
	t.Logf("rich: warnings %v", m.Warnings)
}

// 環境変数で指定した任意の Redmine DB(MySQL/PostgreSQL 等)を書き出し、件数を照合する。
//
//	BUROPHER_TEST_REDMINE_DSN=postgres://...   (Redmine 6.1.2 をマイグレーション済みの DB)
func TestRealDSN(t *testing.T) {
	dsn := os.Getenv("BUROPHER_TEST_REDMINE_DSN")
	if dsn == "" {
		t.Skip("BUROPHER_TEST_REDMINE_DSN not set")
	}
	out := filepath.Join(t.TempDir(), "real.tar.zst")
	opt := Options{DSN: dsn, SourceTimezone: "Asia/Tokyo", Output: out}
	if d := os.Getenv("BUROPHER_TEST_REDMINE_FILES"); d != "" {
		opt.FilesDir, opt.IncludeFiles = d, true
	}
	m, rows, files := exportForTest(t, opt)
	ci, _ := resolveConn(dsn, "")
	db, err := sql.Open(ci.DriverName, ci.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, tb := range m.Tables {
		var n int64
		if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+quoteIdent(ci.Kind, tb.Name)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != tb.Rows || int64(len(rows[tb.Name])) != n {
			t.Errorf("%s: source %d manifest %d archive %d", tb.Name, n, tb.Rows, len(rows[tb.Name]))
		}
	}
	nYAML := 0
	for table, cols := range yamlColumns {
		for _, r := range rows[table] {
			for _, c := range cols {
				if s, _ := r[c].(string); strings.HasPrefix(s, "---") {
					if _, err := rubyyaml.Decode(s); err != nil {
						t.Errorf("%s.%s: %v", table, c, err)
					}
					nYAML++
				}
			}
		}
	}
	// 書き出し結果の要約(別 DB 種別との比較用)
	t.Logf("%s: tables=%d files=%d missing=%d yaml=%d encrypted=%v warnings=%v", m.Source.DBKind, len(m.Tables), len(files), len(m.MissingFiles), nYAML, m.EncryptedValues, m.Warnings)
	if os.Getenv("BUROPHER_TEST_DUMP") != "" {
		dumpRows(t, os.Getenv("BUROPHER_TEST_DUMP"), rows)
	}
}

// dumpRows は比較用にアーカイブの行を正規化 JSON で書き出す(異なる DB 種別の書き出し結果の diff 用)。
func dumpRows(t *testing.T, path string, rows map[string][]archive.Row) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	names := CoreTableNames()
	for _, n := range names {
		for _, r := range rows[n] {
			b, _ := json.Marshal(r) // map のキーはソートされる
			f.WriteString(n + "\t" + string(b) + "\n")
		}
	}
}
