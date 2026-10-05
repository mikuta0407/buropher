// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package export

import (
	"regexp"
	"sort"
	"strings"
)

// tableDef はコアテーブルの定義。
type tableDef struct {
	Name    string
	PK      string // 単一主キー列名(結合テーブルは空)
	Columns []columnDef
}

// columnDef は列名と ActiveRecord の論理型。
type columnDef struct {
	Name string
	Type string
}

// excludedTables は書き出し対象外のコアテーブル(CSV インポート履歴)。
// schema_migrations はマニフェストにのみ記録し、ar_internal_metadata は書き出さない。
var excludedTables = map[string]bool{
	"imports":      true,
	"import_items": true,
}

// railsTables は Rails 管理テーブル(構造チェックの対象外)。
var railsTables = map[string]bool{
	"schema_migrations":    true,
	"ar_internal_metadata": true,
}

// encryptedColumns は Redmine::Ciphering で暗号化されうる列。
var encryptedColumns = map[string]map[string]bool{
	"users":        {"twofa_totp_key": true},
	"repositories": {"password": true},
	"auth_sources": {"account_password": true},
}

// SchemaVersion は受け入れ対象の Redmine スキーマ版(db/migrate の集合)の定義。
type SchemaVersion struct {
	// Name はマニフェストの redmine_schema に記録する版名("6.1" / "7.0")。
	Name string
	// Releases は同一の db/migrate を持つ Redmine リリース(表示用)。
	Releases string
	// Tables はコア業務テーブルと列。
	Tables []tableDef
	// Migrations はコア schema_migrations。
	Migrations []string
}

// schemaVersions は受け入れる Redmine スキーマ版(古い順)。
//   - 6.1: 6.1.0〜6.1.5 の db/migrate はファイル名・内容とも同一(2026-10 に各タグで確認)。
//   - 7.0: 7.0.0 / 7.0.1 の db/migrate は同一。6.1 に webhooks / projects_webhooks、
//     trackers.private_by_default、users.login の索引、
//     default_issue_start_date_to_creation_date の設定行保存の 5 件を加えたもの。
var schemaVersions = []*SchemaVersion{
	{Name: "6.1", Releases: "6.1.0-6.1.5", Tables: coreTables61, Migrations: coreMigrations61},
	{Name: "7.0", Releases: "7.0.0-7.0.1", Tables: coreTables70, Migrations: coreMigrations70},
}

// LatestSchema は受け入れる最新のスキーマ版。
func LatestSchema() *SchemaVersion { return schemaVersions[len(schemaVersions)-1] }

// LookupSchema は版名でスキーマ版を探す。
func LookupSchema(name string) *SchemaVersion {
	for _, v := range schemaVersions {
		if v.Name == name {
			return v
		}
	}
	return nil
}

// CoreMigrations はスキーマ版のコア schema_migrations のコピーを返す。
func (v *SchemaVersion) CoreMigrations() []string {
	return append([]string(nil), v.Migrations...)
}

// CoreTableNames はスキーマ版のコアテーブル名を返す。
func (v *SchemaVersion) CoreTableNames() []string {
	out := make([]string, len(v.Tables))
	for i, t := range v.Tables {
		out[i] = t.Name
	}
	return out
}

var (
	reCoreVersion   = regexp.MustCompile(`^\d+$`)
	rePluginVersion = regexp.MustCompile(`^\d+-(.+)$`)
)

// migrationCheck は schema_migrations の受け入れ判定結果。
type migrationCheck struct {
	Core    []string
	Plugins map[string][]string
	Unknown []string // どちらの形式でもない版
	// Schema は判定に使ったスキーマ版(一致した版、なければ最も近い版)。
	Schema  *SchemaVersion
	Missing []string // Schema にあって DB にない
	Extra   []string // DB にあって Schema にない(コア形式)
}

func (c *migrationCheck) OK() bool { return len(c.Missing) == 0 && len(c.Extra) == 0 }

// checkMigrations は付録 A §7 の 1〜3 を行う。コア版の集合がいずれかのスキーマ版と完全一致すれば合格。
// 一致しない場合は差分が最小の版(同数なら新しい版)を基準に不足・余剰を報告する。
func checkMigrations(versions []string) *migrationCheck {
	c := &migrationCheck{Plugins: map[string][]string{}}
	have := map[string]bool{}
	for _, v := range versions {
		v = strings.TrimSpace(v)
		switch {
		case reCoreVersion.MatchString(v):
			c.Core = append(c.Core, v)
			have[v] = true
		case rePluginVersion.MatchString(v):
			p := rePluginVersion.FindStringSubmatch(v)[1]
			c.Plugins[p] = append(c.Plugins[p], v)
		default:
			c.Unknown = append(c.Unknown, v)
		}
	}
	best := -1
	for _, sv := range schemaVersions {
		var missing, extra []string
		want := map[string]bool{}
		for _, v := range sv.Migrations {
			want[v] = true
			if !have[v] {
				missing = append(missing, v)
			}
		}
		for _, v := range c.Core {
			if !want[v] {
				extra = append(extra, v)
			}
		}
		if d := len(missing) + len(extra); best < 0 || d <= best {
			best = d
			c.Schema, c.Missing, c.Extra = sv, missing, extra
		}
	}
	for _, vs := range c.Plugins {
		sort.Strings(vs)
	}
	return c
}
