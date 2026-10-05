// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package export は Redmine 6.1.x / 7.0.x の DB を読み取り専用で読み、中立形式のアーカイブ
// (internal/redmineimport/archive)へ書き出す。Ruby 環境は不要。
//
// 処理の流れ:
//  1. 読み取り専用・一貫スナップショットのトランザクションを開始
//  2. 受け入れ判定(付録 A §7): schema_migrations のコア集合が既知のスキーマ版
//     (6.1: 6.1.0〜6.1.5 の 322 件 / 7.0: 7.0.0〜7.0.1 の 327 件)のいずれかと完全一致、
//     プラグイン版は警告、その版のコアテーブル(6.1: 56 個 / 7.0: 58 個)・列の存在確認。
//     一致した版はマニフェストの redmine_schema に記録する
//  3. コアテーブル(imports/import_items を除く)を主キー順に ndjson へ書き出し
//  4. attachments が参照する添付ファイルを収集(欠損はマニフェストに記録)
package export

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	// 書き出し元 DB ドライバ(すべて pure Go)
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
	_ "modernc.org/sqlite"

	// --source-timezone の検証を OS の tzdata に依存させない
	_ "time/tzdata"

	"github.com/mikuta0407/buropher/internal/redmineimport/archive"
	"github.com/mikuta0407/buropher/internal/redmineimport/rediscipher"
)

// ToolVersion はマニフェストに記録するツール版。main から設定する想定。
var ToolVersion = "dev"

// Options はエクスポートの設定。
type Options struct {
	// DSN は書き出し元 DB。mysql:// postgres:// sqlite:// sqlserver:// 形式
	// (Driver 指定時はドライバ固有のネイティブ DSN も可)。
	DSN string
	// Driver は mysql/postgres/sqlite/sqlserver。空なら DSN のスキームから判定。
	Driver string
	// FilesDir は Redmine の添付ファイルディレクトリ(attachments_storage_path)。
	// 空で RedmineRoot 指定があればそこから推定する。
	FilesDir string
	// SourceTimezone は Redmine サーバの IANA タイムゾーン名(必須)。
	SourceTimezone string
	// Output は出力アーカイブのパス(.tar.zst)。空なら redmine-export-YYYYMMDD-HHMMSS.tar.zst。
	Output string
	// IncludeFiles は添付ファイル実体をアーカイブに含めるか。
	IncludeFiles bool
	// RedmineRoot は Redmine のインストールディレクトリ(任意)。
	RedmineRoot string
	// RedmineEnv は configuration.yml のセクション名(既定 production)。
	RedmineEnv string
	// Force は受け入れ判定の不合格を無視して続行する(開発用)。
	Force bool
	// Overwrite は既存の出力ファイルを上書きする。
	Overwrite bool
	// TempDir は一時ファイル置き場(既定は出力ファイルと同じディレクトリ)。
	TempDir string
	// ToolVersion はマニフェストに記録する版(空ならパッケージ変数 ToolVersion)。
	ToolVersion string
	// Logger は進捗ログ(nil なら出力しない)。
	Logger *slog.Logger
}

// AcceptanceError は受け入れ判定の不合格。
type AcceptanceError struct {
	Problems []string
}

func (e *AcceptanceError) Error() string {
	return "redmine export: source database is not acceptable:\n  - " + strings.Join(e.Problems, "\n  - ")
}

// Run はエクスポートを実行し、書き出したアーカイブのマニフェストを返す。
func Run(ctx context.Context, opt Options) (*archive.Manifest, error) {
	log := opt.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opt.DSN == "" {
		return nil, errors.New("redmine export: DSN is required")
	}
	if err := validateTimezone(opt.SourceTimezone); err != nil {
		return nil, err
	}
	ci, err := resolveConn(opt.DSN, opt.Driver)
	if err != nil {
		return nil, fmt.Errorf("redmine export: %w", err)
	}

	m := &archive.Manifest{
		Tool:            archive.Tool{Name: "buropher", Version: firstNonEmpty(opt.ToolVersion, ToolVersion)},
		Source:          archive.Source{DBKind: ci.Kind, Timezone: opt.SourceTimezone},
		IncludeFiles:    opt.IncludeFiles,
		EncryptedValues: map[string]int64{},
		CreatedAt:       time.Now().UTC().Truncate(time.Second),
	}
	warn := func(format string, args ...any) {
		s := fmt.Sprintf(format, args...)
		m.Warnings = append(m.Warnings, s)
		log.Warn(s)
	}

	if opt.RedmineRoot != "" {
		info, ws, err := readRedmineRoot(opt.RedmineRoot, opt.RedmineEnv)
		if err != nil {
			return nil, fmt.Errorf("redmine export: %w", err)
		}
		for _, w := range ws {
			warn("%s", w)
		}
		m.Source.RedmineVersion = info.Version
		configured := info.CipherKeyConfigured
		m.Source.CipherKeyConfigured = &configured
		if opt.FilesDir == "" {
			opt.FilesDir = info.AttachmentsPath
		}
	}
	if opt.FilesDir != "" {
		abs, err := filepath.Abs(opt.FilesDir)
		if err != nil {
			return nil, err
		}
		st, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("redmine export: files dir: %w", err)
		}
		if !st.IsDir() {
			return nil, fmt.Errorf("redmine export: files dir %s is not a directory", abs)
		}
		opt.FilesDir = abs
		m.Source.AttachmentsDir = abs
	} else if opt.IncludeFiles {
		return nil, errors.New("redmine export: files dir is required to include attachment files (or disable files)")
	}

	// 出力先
	if opt.Output == "" {
		opt.Output = "redmine-export-" + time.Now().Format("20060102-150405") + ".tar.zst"
	}
	outAbs, err := filepath.Abs(opt.Output)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(outAbs); err == nil && !opt.Overwrite {
		return nil, fmt.Errorf("redmine export: output %s already exists", outAbs)
	}
	tmpDir := opt.TempDir
	if tmpDir == "" {
		tmpDir = filepath.Dir(outAbs)
	}

	// DB 接続
	db, err := sql.Open(ci.DriverName, ci.DSN)
	if err != nil {
		return nil, fmt.Errorf("redmine export: open: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("redmine export: connect: %w", err)
	}
	tx, err := beginSnapshot(ctx, db, ci.Kind, warn)
	if err != nil {
		return nil, fmt.Errorf("redmine export: begin transaction: %w", err)
	}
	defer tx.Rollback()

	m.Source.DBVersion = dbVersion(ctx, tx, ci.Kind)
	log.Info("connected", "kind", ci.Kind, "version", m.Source.DBVersion)

	// 受け入れ判定
	plan, err := accept(ctx, tx, ci.Kind, m, warn)
	if err != nil {
		var ae *AcceptanceError
		if errors.As(err, &ae) && opt.Force {
			for _, p := range ae.Problems {
				warn("acceptance check failed (forced): %s", p)
			}
			m.Source.AcceptanceForced = true
		} else {
			return nil, err
		}
	}

	if v := m.Source.RedmineVersion; v != "" && m.RedmineSchema != "" && !strings.HasPrefix(v, m.RedmineSchema+".") {
		warn("redmine root reports version %s, but the database schema is Redmine %s", v, m.RedmineSchema)
	}

	// 書き出し
	partial := outAbs + ".partial"
	f, err := os.OpenFile(partial, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			f.Close()
			os.Remove(partial)
		}
	}()
	aw, err := archive.NewWriter(f, tmpDir)
	if err != nil {
		return nil, err
	}
	defer aw.Abort()

	refs := map[string][]int64{} // 添付の相対パス → attachment id
	for _, tp := range plan {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := exportTable(ctx, tx, ci.Kind, tp, aw, m, refs, warn)
		if err != nil {
			return nil, fmt.Errorf("redmine export: table %s: %w", tp.def.Name, err)
		}
		log.Info("table exported", "table", tp.def.Name, "rows", n)
	}
	// スナップショットはここまでで十分
	_ = tx.Rollback()

	if err := collectFiles(ctx, opt, refs, aw, m, warn); err != nil {
		return nil, err
	}
	if len(m.EncryptedValues) == 0 {
		m.EncryptedValues = nil
	}
	if err := aw.Close(m); err != nil {
		return nil, fmt.Errorf("redmine export: write archive: %w", err)
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(partial, outAbs); err != nil {
		return nil, err
	}
	success = true
	log.Info("export finished", "output", outAbs, "tables", len(m.Tables), "files", len(m.Files), "missing_files", len(m.MissingFiles), "warnings", len(m.Warnings))
	return m, nil
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// validateTimezone は IANA タイムゾーン名を検証する(既定値は設けない)。
func validateTimezone(tz string) error {
	if tz == "" {
		return errors.New("redmine export: source timezone is required (IANA name such as Asia/Tokyo)")
	}
	if tz == "Local" {
		return errors.New("redmine export: source timezone must be an explicit IANA name, not Local")
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return fmt.Errorf("redmine export: invalid source timezone %q: %w", tz, err)
	}
	return nil
}

// beginSnapshot は読み取り専用の一貫スナップショットトランザクションを開始する。
func beginSnapshot(ctx context.Context, db *sql.DB, kind string, warn func(string, ...any)) (*sql.Tx, error) {
	switch kind {
	case KindSQLite:
		// deferred トランザクション: 最初の読み取りで共有ロック/WAL スナップショットを取る
		return db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	case KindPostgres, KindMySQL:
		return db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	case KindSQLServer:
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSnapshot})
		if err == nil {
			// スナップショット分離が DB で許可されていない場合は最初のアクセスでエラーになる
			var n int
			if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&n); err == nil {
				return tx, nil
			}
			tx.Rollback()
		}
		warn("SQL Server snapshot isolation is not available (%v); falling back to READ COMMITTED — stop Redmine during export for a consistent result", err)
		return db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	}
	return nil, fmt.Errorf("unsupported kind %s", kind)
}

func dbVersion(ctx context.Context, tx *sql.Tx, kind string) string {
	var q string
	switch kind {
	case KindSQLite:
		q = "SELECT sqlite_version()"
	case KindMySQL:
		q = "SELECT VERSION()"
	case KindPostgres:
		q = "SHOW server_version"
	case KindSQLServer:
		q = "SELECT CAST(SERVERPROPERTY('ProductVersion') AS NVARCHAR(128))"
	}
	var v string
	if err := tx.QueryRowContext(ctx, q).Scan(&v); err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

// tablePlan は書き出すテーブルと、実在する列の情報。
type tablePlan struct {
	def     tableDef
	present map[string]bool // 実在する列(Force で欠落列を許した場合に使う)
}

// accept は受け入れ判定(付録 A §7)を行い、書き出し計画を返す。
func accept(ctx context.Context, tx *sql.Tx, kind string, m *archive.Manifest, warn func(string, ...any)) ([]tablePlan, error) {
	var problems []string

	tables, columns, err := introspect(ctx, tx, kind)
	if err != nil {
		return nil, fmt.Errorf("redmine export: introspect schema: %w", err)
	}
	if !tables["schema_migrations"] {
		return nil, &AcceptanceError{Problems: []string{"schema_migrations table not found (not a Redmine database?)"}}
	}

	// 1-3. schema_migrations
	versions, err := queryStrings(ctx, tx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("redmine export: read schema_migrations: %w", err)
	}
	// コア版は数値順、それ以外(プラグイン)は後ろに辞書順
	sort.Slice(versions, func(i, j int) bool {
		a, b := versions[i], versions[j]
		ca, cb := reCoreVersion.MatchString(a), reCoreVersion.MatchString(b)
		if ca != cb {
			return ca
		}
		if ca && len(a) != len(b) {
			return len(a) < len(b)
		}
		return a < b
	})
	m.SchemaMigrations = versions
	mc := checkMigrations(versions)
	sv := mc.Schema
	m.RedmineSchema = sv.Name
	if len(mc.Missing) > 0 {
		problems = append(problems, fmt.Sprintf("%d core migrations of Redmine %s are missing (e.g. %s): upgrade Redmine to a supported version (%s) and run `rake db:migrate` before exporting",
			len(mc.Missing), sv.Name, strings.Join(head(mc.Missing, 5), ", "), supportedReleases()))
	}
	if len(mc.Extra) > 0 {
		problems = append(problems, fmt.Sprintf("%d core migrations unknown to Redmine %s were found (e.g. %s): unsupported Redmine version (supported: %s)",
			len(mc.Extra), sv.Name, strings.Join(head(mc.Extra, 5), ", "), supportedReleases()))
	}
	plugins := make([]string, 0, len(mc.Plugins))
	for p := range mc.Plugins {
		plugins = append(plugins, p)
	}
	sort.Strings(plugins)
	for _, p := range plugins {
		m.PluginMigrations = append(m.PluginMigrations, archive.PluginMigrations{Plugin: p, Versions: mc.Plugins[p]})
		warn("plugin %q has %d migrations; plugin data is not exported", p, len(mc.Plugins[p]))
	}
	for _, v := range mc.Unknown {
		warn("unrecognized schema_migrations version %q", v)
	}

	// 4. 構造チェック
	core := map[string]bool{}
	var plan []tablePlan
	for _, td := range sv.Tables {
		core[td.Name] = true
		if !tables[td.Name] {
			problems = append(problems, fmt.Sprintf("core table %s is missing", td.Name))
			continue
		}
		cols := columns[td.Name]
		present := map[string]bool{}
		known := map[string]bool{}
		for _, c := range td.Columns {
			known[c.Name] = true
			if cols[c.Name] {
				present[c.Name] = true
			} else {
				problems = append(problems, fmt.Sprintf("column %s.%s is missing", td.Name, c.Name))
			}
		}
		var extra []string
		for c := range cols {
			if !known[c] {
				extra = append(extra, c)
			}
		}
		sort.Strings(extra)
		if len(extra) > 0 {
			warn("table %s has extra columns not exported (plugin?): %s", td.Name, strings.Join(extra, ", "))
		}
		if !excludedTables[td.Name] {
			plan = append(plan, tablePlan{def: td, present: present})
		}
	}
	var extraTables []string
	for t := range tables {
		if !core[t] && !railsTables[t] && !isSystemTable(kind, t) {
			extraTables = append(extraTables, t)
		}
	}
	sort.Strings(extraTables)
	if len(extraTables) > 0 {
		warn("non-core tables are not exported (plugin?): %s", strings.Join(extraTables, ", "))
	}
	if len(problems) > 0 {
		return plan, &AcceptanceError{Problems: problems}
	}
	return plan, nil
}

// supportedReleases は受け入れる Redmine リリースの一覧(表示用)。
func supportedReleases() string {
	rs := make([]string, len(schemaVersions))
	for i, v := range schemaVersions {
		rs[i] = v.Releases
	}
	return strings.Join(rs, ", ")
}

func head(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func isSystemTable(kind, t string) bool {
	switch kind {
	case KindSQLite:
		return strings.HasPrefix(t, "sqlite_")
	case KindSQLServer:
		return t == "sysdiagrams"
	}
	return false
}

// introspect はテーブル一覧と列一覧を取得する(名前は小文字化)。
func introspect(ctx context.Context, tx *sql.Tx, kind string) (map[string]bool, map[string]map[string]bool, error) {
	tables := map[string]bool{}
	columns := map[string]map[string]bool{}
	addCol := func(t, c string) {
		t, c = strings.ToLower(t), strings.ToLower(c)
		if columns[t] == nil {
			columns[t] = map[string]bool{}
		}
		columns[t][c] = true
	}
	switch kind {
	case KindSQLite:
		names, err := queryStrings(ctx, tx, "SELECT name FROM sqlite_master WHERE type = 'table'")
		if err != nil {
			return nil, nil, err
		}
		for _, n := range names {
			tables[strings.ToLower(n)] = true
			if strings.HasPrefix(n, "sqlite_") {
				continue
			}
			cols, err := queryStrings(ctx, tx, "SELECT name FROM pragma_table_info(?)", n)
			if err != nil {
				return nil, nil, err
			}
			for _, c := range cols {
				addCol(n, c)
			}
		}
		return tables, columns, nil
	}
	var schemaExpr string
	switch kind {
	case KindMySQL:
		schemaExpr = "DATABASE()"
	case KindPostgres:
		schemaExpr = "current_schema()"
	case KindSQLServer:
		schemaExpr = "SCHEMA_NAME()"
	}
	names, err := queryStrings(ctx, tx, "SELECT table_name FROM information_schema.tables WHERE table_schema = "+schemaExpr+" AND table_type = 'BASE TABLE'")
	if err != nil {
		return nil, nil, err
	}
	for _, n := range names {
		tables[strings.ToLower(n)] = true
	}
	rows, err := tx.QueryContext(ctx, "SELECT table_name, column_name FROM information_schema.columns WHERE table_schema = "+schemaExpr)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t, c string
		if err := rows.Scan(&t, &c); err != nil {
			return nil, nil, err
		}
		addCol(t, c)
	}
	return tables, columns, rows.Err()
}

func queryStrings(ctx context.Context, tx *sql.Tx, q string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		switch x := v.(type) {
		case []byte:
			out = append(out, string(x))
		case string:
			out = append(out, x)
		case nil:
		default:
			out = append(out, fmt.Sprint(x))
		}
	}
	return out, rows.Err()
}

func quoteIdent(kind, s string) string {
	switch kind {
	case KindMySQL:
		return "`" + strings.ReplaceAll(s, "`", "``") + "`"
	case KindSQLServer:
		return "[" + strings.ReplaceAll(s, "]", "]]") + "]"
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// selectExpr は列の取得式。日時は文字列として取得し、ドライバの TZ 解釈を避ける。
func selectExpr(kind string, c columnDef) string {
	q := quoteIdent(kind, c.Name)
	if c.Type == "datetime" || c.Type == "date" {
		switch kind {
		case KindSQLite:
			return "CAST(" + q + " AS TEXT)"
		case KindPostgres:
			return q + "::text"
		}
	}
	return q
}

// exportTable は 1 テーブルを主キー順に書き出す。
func exportTable(ctx context.Context, tx *sql.Tx, kind string, tp tablePlan, aw *archive.Writer, m *archive.Manifest, refs map[string][]int64, warn func(string, ...any)) (int64, error) {
	td := tp.def
	cols := make([]archive.Column, len(td.Columns))
	var exprs, order []string
	for i, c := range td.Columns {
		cols[i] = archive.Column{Name: c.Name, Type: c.Type}
		if tp.present[c.Name] {
			exprs = append(exprs, selectExpr(kind, c))
			order = append(order, quoteIdent(kind, c.Name))
		} else {
			exprs = append(exprs, "NULL")
		}
	}
	if td.PK != "" && tp.present[td.PK] {
		order = []string{quoteIdent(kind, td.PK)}
	}
	q := "SELECT " + strings.Join(exprs, ", ") + " FROM " + quoteIdent(kind, td.Name)
	if len(order) > 0 {
		q += " ORDER BY " + strings.Join(order, ", ")
	}
	tw, err := aw.BeginTable(td.Name, cols)
	if err != nil {
		return 0, err
	}
	rows, err := tx.QueryContext(ctx, q)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	raw := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	vals := make([]any, len(cols))
	warnCounts := map[string]int{}
	warnSample := map[string]string{}
	enc := encryptedColumns[td.Name]
	var attID, attDir, attFile = -1, -1, -1
	if td.Name == "attachments" {
		for i, c := range td.Columns {
			switch c.Name {
			case "id":
				attID = i
			case "disk_directory":
				attDir = i
			case "disk_filename":
				attFile = i
			}
		}
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return 0, err
		}
		for i, c := range td.Columns {
			v, w := normalizeValue(c.Type, raw[i])
			if w != "" {
				key := c.Name + ": " + w
				warnCounts[key]++
				if _, ok := warnSample[key]; !ok {
					warnSample[key] = fmt.Sprintf("%v", raw[i])
				}
			}
			vals[i] = v
			if enc[c.Name] {
				if s, ok := v.(string); ok && rediscipher.IsEncrypted(s) {
					m.EncryptedValues[td.Name+"."+c.Name]++
				}
			}
		}
		if attFile >= 0 {
			fn, _ := vals[attFile].(string)
			dir, _ := vals[attDir].(string)
			if strings.TrimSpace(fn) != "" {
				id, _ := vals[attID].(int64)
				rel := path.Join(dir, fn)
				refs[rel] = append(refs[rel], id)
			}
		}
		if err := tw.WriteRow(vals); err != nil {
			return 0, err
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	keys := make([]string, 0, len(warnCounts))
	for k := range warnCounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sample := warnSample[k]
		if len(sample) > 80 {
			sample = sample[:80] + "…"
		}
		warn("table %s column %s (%d rows, e.g. %q)", td.Name, k, warnCounts[k], sample)
	}
	return tw.Rows(), tw.Close()
}

// collectFiles は attachments が参照するファイルを収集し、欠損を記録する。
func collectFiles(ctx context.Context, opt Options, refs map[string][]int64, aw *archive.Writer, m *archive.Manifest, warn func(string, ...any)) error {
	if opt.FilesDir == "" {
		if len(refs) > 0 {
			warn("attachment files were not checked (no files directory given); %d distinct files are referenced", len(refs))
		}
		return nil
	}
	paths := make([]string, 0, len(refs))
	for p := range refs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		clean, err := archive.CleanFilePath(rel)
		if err != nil {
			warn("attachment path %q is unsafe; skipped (attachment ids %v)", rel, refs[rel])
			continue
		}
		src := filepath.Join(opt.FilesDir, filepath.FromSlash(clean))
		st, err := os.Stat(src)
		if err != nil || !st.Mode().IsRegular() {
			m.MissingFiles = append(m.MissingFiles, archive.MissingFile{Path: clean, AttachmentIDs: refs[rel]})
			continue
		}
		if opt.IncludeFiles {
			if _, err := aw.AddFile(clean, src); err != nil {
				return fmt.Errorf("redmine export: attachment %s: %w", clean, err)
			}
		}
	}
	if n := len(m.MissingFiles); n > 0 {
		warn("%d attachment files referenced by attachments are missing in %s", n, opt.FilesDir)
	}
	return nil
}
