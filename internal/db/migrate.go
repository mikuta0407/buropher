// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package db

//go:generate go run ./internal/migrationgen/cmd -src migrations/src -out migrations

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/pressly/goose/v3"
)

// migrationsFS は dialect 別の生成済みマイグレーション。
// migrations/src (テンプレート) は埋め込まない。
//
//go:embed migrations/sqlite/*.sql migrations/postgres/*.sql
var migrationsFS embed.FS

// MigrationTable はマイグレーション履歴テーブル名。
const MigrationTable = "schema_migrations"

// Direction はマイグレーションの方向。
type Direction int

const (
	// Up は未適用のマイグレーションを全て適用する。
	Up Direction = iota
	// UpByOne は次のマイグレーションを 1 つだけ適用する。
	UpByOne
	// Down は最新のマイグレーションを 1 つだけ戻す。
	Down
	// DownAll は全てのマイグレーションを戻す (バージョン 0)。
	DownAll
)

func (d Direction) String() string {
	switch d {
	case Up:
		return "up"
	case UpByOne:
		return "up-by-one"
	case Down:
		return "down"
	case DownAll:
		return "down-all"
	}
	return fmt.Sprintf("Direction(%d)", int(d))
}

// MigrationResult は 1 マイグレーションの適用結果。
type MigrationResult struct {
	Version   int64
	Path      string
	Direction string // "up" / "down"
	Duration  time.Duration
	Empty     bool
}

func (r MigrationResult) String() string {
	return fmt.Sprintf("%-4s %s (%s)", r.Direction, r.Path, r.Duration.Round(time.Microsecond))
}

// MigrationStatus は 1 マイグレーションの状態。
type MigrationStatus struct {
	Version   int64
	Path      string
	Applied   bool
	AppliedAt time.Time // 未適用ならゼロ値
}

// MigrationFS は dialect のマイグレーションファイル群を返す。
func MigrationFS(name DialectName) (fs.FS, error) {
	switch name {
	case SQLite, Postgres:
		return fs.Sub(migrationsFS, "migrations/"+string(name))
	}
	return nil, fmt.Errorf("db: unknown dialect %q", name)
}

func newProvider(d *DB) (*goose.Provider, error) {
	fsys, err := MigrationFS(d.dialect.Name())
	if err != nil {
		return nil, err
	}
	gd := goose.DialectSQLite3
	if d.dialect.Name() == Postgres {
		gd = goose.DialectPostgres
	}
	return goose.NewProvider(gd, d.x.DB, fsys,
		goose.WithTableName(MigrationTable),
		goose.WithDisableGlobalRegistry(true),
	)
}

// Migrate はマイグレーションを direction の方向に実行し、適用したものを返す。
// 適用済み・未適用がなく何もしなかった場合は空スライスを返す (エラーではない)。
func Migrate(ctx context.Context, d *DB, direction Direction) ([]MigrationResult, error) {
	p, err := newProvider(d)
	if err != nil {
		return nil, err
	}
	var rs []*goose.MigrationResult
	switch direction {
	case Up:
		rs, err = p.Up(ctx)
	case UpByOne:
		var r *goose.MigrationResult
		r, err = p.UpByOne(ctx)
		if errors.Is(err, goose.ErrNoNextVersion) {
			err = nil
		}
		if r != nil {
			rs = append(rs, r)
		}
	case Down:
		var r *goose.MigrationResult
		r, err = p.Down(ctx)
		if errors.Is(err, goose.ErrNoNextVersion) {
			err = nil
		}
		if r != nil {
			rs = append(rs, r)
		}
	case DownAll:
		rs, err = p.DownTo(ctx, 0)
	default:
		return nil, fmt.Errorf("db: unknown migration direction %v", direction)
	}
	out := convertResults(rs)
	if err != nil {
		return out, fmt.Errorf("db: migrate %s: %w", direction, err)
	}
	return out, nil
}

// MigrateTo は指定バージョンまで (必要に応じて up / down で) 移行する。
func MigrateTo(ctx context.Context, d *DB, version int64) ([]MigrationResult, error) {
	p, err := newProvider(d)
	if err != nil {
		return nil, err
	}
	cur, err := p.GetDBVersion(ctx)
	if err != nil {
		return nil, err
	}
	var rs []*goose.MigrationResult
	if version >= cur {
		rs, err = p.UpTo(ctx, version)
	} else {
		rs, err = p.DownTo(ctx, version)
	}
	out := convertResults(rs)
	if err != nil {
		return out, fmt.Errorf("db: migrate to %d: %w", version, err)
	}
	return out, nil
}

// Status は全マイグレーションの適用状態を返す。
func Status(ctx context.Context, d *DB) ([]MigrationStatus, error) {
	p, err := newProvider(d)
	if err != nil {
		return nil, err
	}
	ss, err := p.Status(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]MigrationStatus, 0, len(ss))
	for _, s := range ss {
		out = append(out, MigrationStatus{
			Version:   s.Source.Version,
			Path:      s.Source.Path,
			Applied:   s.State == goose.StateApplied,
			AppliedAt: s.AppliedAt,
		})
	}
	return out, nil
}

// Version は DB に適用済みの最新バージョンを返す (未適用なら 0)。
func Version(ctx context.Context, d *DB) (int64, error) {
	p, err := newProvider(d)
	if err != nil {
		return 0, err
	}
	return p.GetDBVersion(ctx)
}

// HasPending は未適用のマイグレーションがあるかを返す。
func HasPending(ctx context.Context, d *DB) (bool, error) {
	p, err := newProvider(d)
	if err != nil {
		return false, err
	}
	return p.HasPending(ctx)
}

func convertResults(rs []*goose.MigrationResult) []MigrationResult {
	out := make([]MigrationResult, 0, len(rs))
	for _, r := range rs {
		if r == nil || r.Source == nil {
			continue
		}
		out = append(out, MigrationResult{
			Version:   r.Source.Version,
			Path:      r.Source.Path,
			Direction: r.Direction,
			Duration:  r.Duration,
			Empty:     r.Empty,
		})
	}
	return out
}
