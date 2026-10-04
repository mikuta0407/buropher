// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package db

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
)

// DialectName は対応 DB の識別子。config.Database.Driver の値と一致する。
type DialectName string

const (
	SQLite   DialectName = "sqlite"
	Postgres DialectName = "postgres"
)

// Execer は Dialect のヘルパーが SQL を発行する対象 (*DB / *Tx の両方が満たす)。
type Execer interface {
	Exec(ctx context.Context, query string, args ...any) (Result, error)
}

// Dialect は SQLite / PostgreSQL の SQL 方言差を吸収する。
//
// アプリ側の SQL は原則として `?` プレースホルダで書き、DB/Tx のメソッドが
// 実行直前に dialect に合わせて書き換える (PG では $1, $2 ...)。
// このため PostgreSQL の jsonb `?` 演算子は使えない (jsonb_exists() 等を使う)。
type Dialect interface {
	Name() DialectName
	// BindType は sqlx の Rebind 用のプレースホルダ種別。
	BindType() int
	// Rebind は `?` プレースホルダを dialect 用に書き換える。
	Rebind(query string) string

	// ILike は大文字小文字を無視した LIKE 条件 (右辺はプレースホルダ 1 個) を返す。
	// パターンは EscapeLike でエスケープし、% を付けて渡す。エスケープ文字は '\'。
	ILike(col string) string
	// BoolLiteral は SQL に直接埋め込む真偽リテラル。
	BoolLiteral(b bool) string
	// NowExpr は現在時刻 (UTC) を列と同じ表現で返す SQL 式。
	// 通常は Go 側で db.Now() を引数に渡すこと。DEFAULT 句やバッチ UPDATE 用。
	NowExpr() string
	// LimitOffset は "LIMIT n OFFSET m" 句 (limit<0 なら無制限)。
	LimitOffset(limit, offset int) string
	// Upsert は INSERT ... ON CONFLICT (conflict) DO UPDATE SET ... 文を返す。
	// update が空なら DO NOTHING。
	Upsert(table string, cols, conflict, update []string) string
	// JSONExtractText は JSON 列 col のトップレベルキー key の値をテキストとして取り出す式。
	JSONExtractText(col, key string) string
	// DeferConstraints は現在のトランザクション内で遅延可能な外部キー検査をコミット時まで遅延させる。
	DeferConstraints(ctx context.Context, e Execer) error
	// ResetSequence は明示 ID 挿入後に table の id 採番を max(id)+1 以降に進める。
	ResetSequence(ctx context.Context, e Execer, table string) error
	// AdvanceSequence は table の次の採番を max(atLeast, max(id)) + 1 以降に進める
	// (移行で破棄した行の ID を再利用しないため)。
	AdvanceSequence(ctx context.Context, e Execer, table string, atLeast int64) error
}

// DialectFor は名前から Dialect を返す。
func DialectFor(name DialectName) (Dialect, error) {
	switch name {
	case SQLite:
		return sqliteDialect{}, nil
	case Postgres:
		return postgresDialect{}, nil
	}
	return nil, fmt.Errorf("db: unknown dialect %q", name)
}

// ForUpdate は行ロックの句を返す: PostgreSQL では " FOR UPDATE"、SQLite では ""。
// PostgreSQL の既定（READ COMMITTED）では、トランザクション内で読んだ値に基づく検査と更新の間に
// 他のトランザクションが同じ行を更新できるため、検査の前に対象の行をロックするのに使う。
// SQLite の書き込みトランザクションは BEGIN IMMEDIATE で直列化されるのでロックは不要。
// 複数プロセスが同じ DB を使うことがあるため、プロセス内のミューテックスではなく DB のロックで直列化する。
func ForUpdate(q interface{ Dialect() Dialect }) string {
	if q.Dialect().Name() == Postgres {
		return " FOR UPDATE"
	}
	return ""
}

// EscapeLike は LIKE パターン中の特殊文字 (\ % _) をエスケープする。
func EscapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// limitOffset は LIMIT/OFFSET 句を作る。unlimited は無制限を表す LIMIT 値
// (SQLite は OFFSET 単独を許さないため "-1"、PG は "ALL")。
func limitOffset(limit, offset int, unlimited string) string {
	var b strings.Builder
	b.WriteString("LIMIT ")
	if limit >= 0 {
		b.WriteString(strconv.Itoa(limit))
	} else {
		b.WriteString(unlimited)
	}
	if offset > 0 {
		b.WriteString(" OFFSET ")
		b.WriteString(strconv.Itoa(offset))
	}
	return b.String()
}

func upsert(table string, cols, conflict, update []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s) ",
		table, strings.Join(cols, ", "), strings.TrimSuffix(strings.Repeat("?, ", len(cols)), ", "),
		strings.Join(conflict, ", "))
	if len(update) == 0 {
		b.WriteString("DO NOTHING")
		return b.String()
	}
	b.WriteString("DO UPDATE SET ")
	for i, c := range update {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s = excluded.%s", c, c)
	}
	return b.String()
}

func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// ---------------------------------------------------------------- SQLite

type sqliteDialect struct{}

func (sqliteDialect) Name() DialectName       { return SQLite }
func (sqliteDialect) BindType() int           { return sqlx.QUESTION }
func (sqliteDialect) Rebind(q string) string  { return q }
func (sqliteDialect) ILike(col string) string { return "LOWER(" + col + ") LIKE LOWER(?) ESCAPE '\\'" }
func (sqliteDialect) BoolLiteral(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// NowExpr は固定長 'YYYY-MM-DDTHH:MM:SS.ffffffZ' を返す (SQLite はミリ秒精度なので下 3 桁は 0)。
func (sqliteDialect) NowExpr() string {
	return "(strftime('%Y-%m-%dT%H:%M:%S', 'now') || substr(strftime('%f', 'now'), 3) || '000Z')"
}

func (sqliteDialect) LimitOffset(limit, offset int) string {
	return limitOffset(limit, offset, "-1")
}

func (sqliteDialect) Upsert(table string, cols, conflict, update []string) string {
	return upsert(table, cols, conflict, update)
}

func (sqliteDialect) JSONExtractText(col, key string) string {
	return "(" + col + " ->> " + quoteLiteral("$."+key) + ")"
}

func (sqliteDialect) DeferConstraints(ctx context.Context, e Execer) error {
	// コミット (またはロールバック) で自動的に OFF に戻る。
	_, err := e.Exec(ctx, "PRAGMA defer_foreign_keys = ON")
	return err
}

func (sqliteDialect) ResetSequence(ctx context.Context, e Execer, table string) error {
	// AUTOINCREMENT テーブルは明示 ID 挿入で sqlite_sequence が自動更新されるが、
	// 念のため max(id) 未満なら引き上げる。
	_, err := e.Exec(ctx,
		"UPDATE sqlite_sequence SET seq = (SELECT COALESCE(MAX(id), 0) FROM "+table+") "+
			"WHERE name = ? AND seq < (SELECT COALESCE(MAX(id), 0) FROM "+table+")", table)
	return err
}

func (sqliteDialect) AdvanceSequence(ctx context.Context, e Execer, table string, atLeast int64) error {
	if _, err := e.Exec(ctx, "INSERT INTO sqlite_sequence (name, seq) SELECT ?, 0 WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = ?)", table, table); err != nil {
		return err
	}
	_, err := e.Exec(ctx, "UPDATE sqlite_sequence SET seq = ? WHERE name = ? AND seq < ?", atLeast, table, atLeast)
	return err
}

// ---------------------------------------------------------------- PostgreSQL

type postgresDialect struct{}

func (postgresDialect) Name() DialectName       { return Postgres }
func (postgresDialect) BindType() int           { return sqlx.DOLLAR }
func (postgresDialect) Rebind(q string) string  { return sqlx.Rebind(sqlx.DOLLAR, q) }
func (postgresDialect) ILike(col string) string { return col + " ILIKE ? ESCAPE '\\'" }
func (postgresDialect) BoolLiteral(b bool) string {
	if b {
		return "TRUE"
	}
	return "FALSE"
}
func (postgresDialect) NowExpr() string { return "CURRENT_TIMESTAMP" }
func (postgresDialect) LimitOffset(limit, offset int) string {
	return limitOffset(limit, offset, "ALL")
}
func (postgresDialect) Upsert(table string, cols, conflict, update []string) string {
	return upsert(table, cols, conflict, update)
}
func (postgresDialect) JSONExtractText(col, key string) string {
	return "(" + col + " ->> " + quoteLiteral(key) + ")"
}
func (postgresDialect) DeferConstraints(ctx context.Context, e Execer) error {
	_, err := e.Exec(ctx, "SET CONSTRAINTS ALL DEFERRED")
	return err
}
func (postgresDialect) AdvanceSequence(ctx context.Context, e Execer, table string, atLeast int64) error {
	_, err := e.Exec(ctx,
		"SELECT setval(pg_get_serial_sequence(?, 'id'), GREATEST(?, COALESCE((SELECT MAX(id) FROM "+table+"), 0)) + 1, false)",
		table, atLeast)
	return err
}

func (postgresDialect) ResetSequence(ctx context.Context, e Execer, table string) error {
	// 空テーブルなら次の採番が 1 になるよう is_called=false で設定する。
	_, err := e.Exec(ctx,
		"SELECT setval(pg_get_serial_sequence(?, 'id'), COALESCE((SELECT MAX(id) FROM "+table+"), 0) + 1, false)",
		table)
	return err
}
