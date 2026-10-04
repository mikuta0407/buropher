// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package db は buropher の DB アクセス基盤 (接続, dialect 抽象化, トランザクション,
// 時刻型, マイグレーション) を提供する。
//
// 方針:
//   - SQL は手書き (静的クエリ) または squirrel (動的クエリ, SQ 参照) で組み立て、
//     プレースホルダは常に `?` で書く。DB/Tx のメソッドが dialect に合わせて書き換える。
//   - 日時は UTC。列値は Time / NullTime / Date / NullDate 型で読み書きする
//     (SQLite は固定長 TEXT、PostgreSQL は timestamptz / date)。
//   - SQLite は接続ごとに foreign_keys=ON, journal_mode=WAL, busy_timeout, synchronous=NORMAL。
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite" // database/sql ドライバ "sqlite" を登録
)

// Result は Exec の結果。
type Result = sql.Result

// ErrNoRows は行が見つからないときのエラー (sql.ErrNoRows と同一)。
var ErrNoRows = sql.ErrNoRows

// SQ は動的 SQL 用の squirrel ビルダ。プレースホルダは `?` のまま生成し、
// ExecSQ / GetSQ / SelectSQ などで dialect に合わせて書き換える。
//
// squirrel を採用した理由: IssueQuery 等のフィルタ・ソート・集計で条件を動的に積み上げる
// 必要があり、実績のあるビルダ (純 Go, 依存が小さい) を使う方が自作より安全。
// 静的なクエリは手書き SQL を推奨する。
var SQ = sq.StatementBuilder.PlaceholderFormat(sq.Question)

// Sqlizer は squirrel のビルダが満たすインタフェース。
type Sqlizer = sq.Sqlizer

// SQLite の既定 busy_timeout (ミリ秒)。
const sqliteBusyTimeoutMS = 10000

// SQLite の接続ごとのページキャッシュ (KiB) と mmap の上限 (バイト)。
const (
	sqliteCacheSizeKiB = 32 * 1024
	sqliteMmapSize     = 256 << 20
)

// DefaultMaxIdleConns は接続プールに保持する待機接続の数 (CPU 数の 2 倍、最低 4)。
// database/sql の既定 (2 本) では並行リクエストのたびに接続を作り直すことになり、
// SQLite では接続ごとにスキーマの解析とプラグマの設定が、PostgreSQL では接続の確立が毎回かかる。
//
// 同時接続数の上限 (SetMaxOpenConns) は既定では設けない。1 リクエストがトランザクションを
// 保持したまま別の接続で読み取ることがあり、上限を設けるとプール枯渇で詰まるおそれがあるため。
// 必要なら設定 database.max_open_conns で指定する (SetMaxConns)。
func DefaultMaxIdleConns() int { return max(4, 2*runtime.NumCPU()) }

// SetMaxConns は同時接続数の上限を n にする (待機接続の数も n 以下に抑える)。n <= 0 なら何もしない。
// インメモリ SQLite は 1 接続に固定されたままにする。
func (d *DB) SetMaxConns(n int) {
	if n <= 0 || d.memory {
		return
	}
	d.x.SetMaxOpenConns(n)
	d.x.SetMaxIdleConns(min(n, DefaultMaxIdleConns()))
}

// DB は sqlx.DB と Dialect をまとめたもの。
type DB struct {
	x       *sqlx.DB
	dialect Dialect
	// memory はインメモリ SQLite (接続を 1 本に固定している)。
	memory bool
}

// Queryer は *DB と *Tx の共通インタフェース。リポジトリ層はこれを受け取る。
type Queryer interface {
	Dialect() Dialect
	Exec(ctx context.Context, query string, args ...any) (Result, error)
	Get(ctx context.Context, dest any, query string, args ...any) error
	Select(ctx context.Context, dest any, query string, args ...any) error
	Query(ctx context.Context, query string, args ...any) (*sqlx.Rows, error)
	QueryRow(ctx context.Context, query string, args ...any) *sqlx.Row
	// InsertReturningID は INSERT 文に " RETURNING id" を付けて実行し、採番された id を返す。
	InsertReturningID(ctx context.Context, query string, args ...any) (int64, error)
	ExecSQ(ctx context.Context, s Sqlizer) (Result, error)
	GetSQ(ctx context.Context, dest any, s Sqlizer) error
	SelectSQ(ctx context.Context, dest any, s Sqlizer) error
}

var (
	_ Queryer = (*DB)(nil)
	_ Queryer = (*Tx)(nil)
)

// Open は driver ("sqlite" / "postgres") と DSN で DB を開き、疎通確認する。
//
// SQLite の DSN はファイルパス (例 "data/buropher.db") または "file:" URI。
// 接続ごとのプラグマはここで付与するので DSN に含める必要はない。
// PostgreSQL の DSN は pgx 形式 (URL または key=value)。セッションの timezone は UTC に固定する。
func Open(ctx context.Context, driver, dsn string) (*DB, error) {
	d, err := DialectFor(DialectName(driver))
	if err != nil {
		return nil, err
	}
	var x *sqlx.DB
	var memory bool
	switch d.Name() {
	case SQLite:
		full, mem, err := sqliteDSN(dsn)
		memory = mem
		if err != nil {
			return nil, err
		}
		if !mem {
			if err := createSQLiteFile(dsn); err != nil {
				return nil, err
			}
		}
		sdb, err := sql.Open("sqlite", full)
		if err != nil {
			return nil, fmt.Errorf("db: open sqlite: %w", err)
		}
		if mem {
			// インメモリ DB は接続ごとに別 DB になるため 1 接続に固定する。
			sdb.SetMaxOpenConns(1)
		} else {
			// WAL なので読み取りは複数接続で並行できる。書き込みは BEGIN IMMEDIATE と busy_timeout により
			// SQLite のロックで直列化される。
			sdb.SetMaxIdleConns(DefaultMaxIdleConns())
		}
		sdb.SetConnMaxIdleTime(5 * time.Minute)
		x = sqlx.NewDb(sdb, "sqlite")
	case Postgres:
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil {
			return nil, fmt.Errorf("db: parse postgres dsn: %w", err)
		}
		if cfg.RuntimeParams == nil {
			cfg.RuntimeParams = map[string]string{}
		}
		cfg.RuntimeParams["timezone"] = "UTC"
		sdb := stdlib.OpenDB(*cfg)
		sdb.SetMaxIdleConns(DefaultMaxIdleConns())
		sdb.SetConnMaxIdleTime(5 * time.Minute)
		x = sqlx.NewDb(sdb, "pgx")
	}
	if err := x.PingContext(ctx); err != nil {
		x.Close()
		return nil, fmt.Errorf("db: ping %s: %w", driver, err)
	}
	return &DB{x: x, dialect: d, memory: memory}, nil
}

// createSQLiteFile は SQLite の DB ファイルが無ければ所有者だけが読み書きできるモード（0600）で作る。
// SQLite 自身は 0644 で作るため、パスワードのハッシュ・API キー・セッションを含む DB が同じホストの
// 他の利用者から読めてしまう。-wal / -shm は SQLite が DB ファイルと同じモードで作る。
// 既存のファイルのモードは変えない（運用者の設定を尊重する）。
func createSQLiteFile(dsn string) error {
	path := dsn
	if strings.HasPrefix(path, "file:") {
		path = strings.TrimPrefix(path, "file:")
		if i := strings.IndexByte(path, '?'); i >= 0 {
			if q, err := url.ParseQuery(path[i+1:]); err != nil || q.Get("mode") != "" {
				// mode=ro などファイルの作成を伴わない指定は SQLite に任せる
				return nil
			}
			path = path[:i]
		}
		if strings.HasPrefix(path, "//") {
			// file://host/path 形式などは SQLite に任せる
			return nil
		}
		if p, err := url.PathUnescape(path); err == nil {
			path = p
		}
	}
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil
		}
		return fmt.Errorf("db: create sqlite file: %w", err)
	}
	return f.Close()
}

// sqliteDSN はパスまたは file: URI に接続ごとのプラグマを付与する。
func sqliteDSN(dsn string) (full string, memory bool, err error) {
	if dsn == "" {
		return "", false, errors.New("db: empty sqlite dsn")
	}
	memory = dsn == ":memory:" || strings.Contains(dsn, "mode=memory")
	base, query := dsn, ""
	if strings.HasPrefix(dsn, "file:") {
		if i := strings.IndexByte(dsn, '?'); i >= 0 {
			base, query = dsn[:i], dsn[i+1:]
		}
	} else if dsn != ":memory:" {
		base = "file:" + dsn
	}
	v, err := url.ParseQuery(query)
	if err != nil {
		return "", false, fmt.Errorf("db: sqlite dsn query: %w", err)
	}
	pragmas := []string{
		"foreign_keys(1)",
		fmt.Sprintf("busy_timeout(%d)", sqliteBusyTimeoutMS),
		"synchronous(NORMAL)",
		// 一時 B-tree (ORDER BY / GROUP BY / DISTINCT の作業領域) をメモリに置く
		"temp_store(MEMORY)",
		// ページキャッシュ (負値は KiB 単位)。既定の 2 MiB では大きな DB で再読み込みが多い
		fmt.Sprintf("cache_size(%d)", -sqliteCacheSizeKiB),
	}
	if !memory {
		pragmas = append(pragmas, "journal_mode(WAL)",
			// 読み取りを mmap で行い、read システムコールとページのコピーを減らす
			fmt.Sprintf("mmap_size(%d)", sqliteMmapSize))
	}
	for _, p := range pragmas {
		v.Add("_pragma", p)
	}
	if v.Get("_txlock") == "" {
		// 書き込みトランザクションの昇格時デッドロック (SQLITE_BUSY) を避けるため BEGIN IMMEDIATE。
		v.Set("_txlock", "immediate")
	}
	return base + "?" + v.Encode(), memory, nil
}

// Close は接続プールを閉じる。
func (d *DB) Close() error { return d.x.Close() }

// X は内部の *sqlx.DB を返す (goose 等、生のハンドルが必要な場合のみ使う)。
func (d *DB) X() *sqlx.DB { return d.x }

// Dialect は dialect を返す。
func (d *DB) Dialect() Dialect { return d.dialect }

// Ping は疎通確認する。
func (d *DB) Ping(ctx context.Context) error { return d.x.PingContext(ctx) }

func (d *DB) Exec(ctx context.Context, q string, args ...any) (Result, error) {
	if statsEnabled.Load() {
		defer traceQuery(time.Now(), q)
	}
	return d.x.ExecContext(ctx, d.dialect.Rebind(q), args...)
}

func (d *DB) Get(ctx context.Context, dest any, q string, args ...any) error {
	if statsEnabled.Load() {
		defer traceQuery(time.Now(), q)
	}
	return d.x.GetContext(ctx, dest, d.dialect.Rebind(q), args...)
}

func (d *DB) Select(ctx context.Context, dest any, q string, args ...any) error {
	if statsEnabled.Load() {
		defer traceQuery(time.Now(), q)
	}
	return d.x.SelectContext(ctx, dest, d.dialect.Rebind(q), args...)
}

func (d *DB) Query(ctx context.Context, q string, args ...any) (*sqlx.Rows, error) {
	if statsEnabled.Load() {
		defer traceQuery(time.Now(), q)
	}
	return d.x.QueryxContext(ctx, d.dialect.Rebind(q), args...) //nolint:sqlclosecheck // 呼び出し側で Close する
}

func (d *DB) QueryRow(ctx context.Context, q string, args ...any) *sqlx.Row {
	if statsEnabled.Load() {
		defer traceQuery(time.Now(), q)
	}
	return d.x.QueryRowxContext(ctx, d.dialect.Rebind(q), args...)
}

func (d *DB) InsertReturningID(ctx context.Context, q string, args ...any) (int64, error) {
	return insertReturningID(ctx, d, q, args)
}

func (d *DB) ExecSQ(ctx context.Context, s Sqlizer) (Result, error) { return execSQ(ctx, d, s) }
func (d *DB) GetSQ(ctx context.Context, dest any, s Sqlizer) error  { return getSQ(ctx, d, dest, s) }
func (d *DB) SelectSQ(ctx context.Context, dest any, s Sqlizer) error {
	return selectSQ(ctx, d, dest, s)
}

// Begin はトランザクションを開始する。通常は WithTx を使う。
func (d *DB) Begin(ctx context.Context) (*Tx, error) {
	tx, err := d.x.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &Tx{x: tx, dialect: d.dialect}, nil
}

// WithTx は fn をトランザクション内で実行する。fn がエラーを返すか panic した場合は
// ロールバックし、それ以外はコミットする。
func (d *DB) WithTx(ctx context.Context, fn func(tx *Tx) error) (err error) {
	tx, err := d.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			if rerr := tx.Rollback(); rerr != nil && !errors.Is(rerr, sql.ErrTxDone) {
				err = errors.Join(err, fmt.Errorf("db: rollback: %w", rerr))
			}
			return
		}
		if cerr := tx.Commit(); cerr != nil {
			err = fmt.Errorf("db: commit: %w", cerr)
		}
	}()
	return fn(tx)
}

// Tx はトランザクション。
type Tx struct {
	x       *sqlx.Tx
	dialect Dialect
}

// X は内部の *sqlx.Tx を返す。
func (t *Tx) X() *sqlx.Tx { return t.x }

// Dialect は dialect を返す。
func (t *Tx) Dialect() Dialect { return t.dialect }

// Commit はコミットする。
func (t *Tx) Commit() error { return t.x.Commit() }

// Rollback はロールバックする。
func (t *Tx) Rollback() error { return t.x.Rollback() }

// DeferConstraints は以降の遅延可能な外部キー検査をコミット時まで遅延させる。
// (自己参照 FK は既定で INITIALLY DEFERRED。これは他の FK も含め一括で遅延させたい場合用。
// SQLite では DEFERRABLE でない FK も遅延される。)
func (t *Tx) DeferConstraints(ctx context.Context) error {
	return t.dialect.DeferConstraints(ctx, t)
}

func (t *Tx) Exec(ctx context.Context, q string, args ...any) (Result, error) {
	if statsEnabled.Load() {
		defer traceQuery(time.Now(), q)
	}
	return t.x.ExecContext(ctx, t.dialect.Rebind(q), args...)
}

func (t *Tx) Get(ctx context.Context, dest any, q string, args ...any) error {
	if statsEnabled.Load() {
		defer traceQuery(time.Now(), q)
	}
	return t.x.GetContext(ctx, dest, t.dialect.Rebind(q), args...)
}

func (t *Tx) Select(ctx context.Context, dest any, q string, args ...any) error {
	if statsEnabled.Load() {
		defer traceQuery(time.Now(), q)
	}
	return t.x.SelectContext(ctx, dest, t.dialect.Rebind(q), args...)
}

func (t *Tx) Query(ctx context.Context, q string, args ...any) (*sqlx.Rows, error) {
	if statsEnabled.Load() {
		defer traceQuery(time.Now(), q)
	}
	return t.x.QueryxContext(ctx, t.dialect.Rebind(q), args...) //nolint:sqlclosecheck // 呼び出し側で Close する
}

func (t *Tx) QueryRow(ctx context.Context, q string, args ...any) *sqlx.Row {
	if statsEnabled.Load() {
		defer traceQuery(time.Now(), q)
	}
	return t.x.QueryRowxContext(ctx, t.dialect.Rebind(q), args...)
}

func (t *Tx) InsertReturningID(ctx context.Context, q string, args ...any) (int64, error) {
	return insertReturningID(ctx, t, q, args)
}

func (t *Tx) ExecSQ(ctx context.Context, s Sqlizer) (Result, error) { return execSQ(ctx, t, s) }
func (t *Tx) GetSQ(ctx context.Context, dest any, s Sqlizer) error  { return getSQ(ctx, t, dest, s) }
func (t *Tx) SelectSQ(ctx context.Context, dest any, s Sqlizer) error {
	return selectSQ(ctx, t, dest, s)
}

// In は sqlx.In のラッパ。スライス引数を `?, ?, ...` に展開する (プレースホルダは `?` のまま)。
func In(query string, args ...any) (string, []any, error) {
	return sqlx.In(query, args...)
}

func insertReturningID(ctx context.Context, q Queryer, query string, args []any) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, strings.TrimRight(strings.TrimSpace(query), ";")+" RETURNING id", args...).Scan(&id)
	return id, err
}

func execSQ(ctx context.Context, q Queryer, s Sqlizer) (Result, error) {
	query, args, err := s.ToSql()
	if err != nil {
		return nil, err
	}
	return q.Exec(ctx, query, args...)
}

func getSQ(ctx context.Context, q Queryer, dest any, s Sqlizer) error {
	query, args, err := s.ToSql()
	if err != nil {
		return err
	}
	return q.Get(ctx, dest, query, args...)
}

func selectSQ(ctx context.Context, q Queryer, dest any, s Sqlizer) error {
	query, args, err := s.ToSql()
	if err != nil {
		return err
	}
	return q.Select(ctx, dest, query, args...)
}

// sqliteAnalysisLimit は ANALYZE が各インデックスで調べる行数の上限 (PRAGMA analysis_limit)。
// 近似の統計でもプランの選択には十分で、大きなテーブルでも短時間で終わる。
const sqliteAnalysisLimit = 1000

// Optimize は SQLite の統計 (sqlite_stat1) を更新する。統計が無いとクエリプランナーが
// 大きなテーブルで不適切な結合順を選ぶことがある (例: IN リストの代わりに全チケットを走査する)。
//
// ANALYZE は書き込みロックを取るので、テーブルごとに別の文 (別トランザクション) で実行し、
// 他の書き込みを長く待たせないようにする。all が偽なら統計の無いテーブル (統計の無いインデックスを
// 持つテーブルを含む) だけを対象にする。
// PostgreSQL では autovacuum が統計を更新するので何もしない。
func (d *DB) Optimize(ctx context.Context, all bool) error {
	if d.dialect.Name() != SQLite || d.memory {
		return nil
	}
	conn, err := d.x.Connx(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var tables []string
	if err := conn.SelectContext(ctx, &tables, `SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite\_%' ESCAPE '\' ORDER BY name`); err != nil {
		return err
	}
	analyzed := map[string]bool{}
	if !all {
		// 統計のあるテーブルのうち、統計の無いインデックス (後から追加したもの) を持たないものは対象外
		var done []struct {
			Tbl string         `db:"tbl"`
			Idx sql.NullString `db:"idx"`
		}
		// sqlite_stat1 は一度も ANALYZE していなければ存在しない
		if err := conn.SelectContext(ctx, &done, `SELECT tbl, idx FROM sqlite_stat1`); err == nil {
			hasStat := map[string]bool{}
			for _, r := range done {
				analyzed[r.Tbl] = true
				hasStat[r.Tbl+"\x00"+r.Idx.String] = true
			}
			var idx []struct {
				Tbl  string `db:"tbl_name"`
				Name string `db:"name"`
			}
			if err := conn.SelectContext(ctx, &idx, `SELECT tbl_name, name FROM sqlite_schema WHERE type = 'index'`); err != nil {
				return err
			}
			for _, i := range idx {
				if analyzed[i.Tbl] && !hasStat[i.Tbl+"\x00"+i.Name] {
					analyzed[i.Tbl] = false
				}
			}
		}
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf(`PRAGMA analysis_limit=%d`, sqliteAnalysisLimit)); err != nil {
		return err
	}
	done := 0
	for _, t := range tables {
		if analyzed[t] {
			continue
		}
		if _, err := conn.ExecContext(ctx, `ANALYZE "`+strings.ReplaceAll(t, `"`, `""`)+`"`); err != nil {
			return fmt.Errorf("db: analyze %s: %w", t, err)
		}
		done++
	}
	if done == 0 {
		return nil
	}
	// ANALYZE が統計を読み直させるのは実行した接続だけで、プールの他の接続は古い統計
	// (初回は統計なし) のまま使い続ける。スキーマを変更して (一時的なテーブルの作成と削除。
	// トランザクション内なので他の接続からは見えない) スキーマのバージョンを進め、
	// 全接続が次の文でスキーマと統計を読み直すようにする。
	tx, err := conn.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	for _, q := range []string{`CREATE TABLE buropher_reload_stats (x INTEGER)`, `DROP TABLE buropher_reload_stats`} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("db: reload stats: %w", err)
		}
	}
	return tx.Commit()
}
