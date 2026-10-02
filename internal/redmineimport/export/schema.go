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

// CoreMigrations は受け入れ対象の Redmine 6.1.2 コア schema_migrations(322 件)のコピーを返す。
func CoreMigrations() []string {
	return append([]string(nil), coreMigrations...)
}

// CoreTableNames はコア 56 テーブル名を返す。
func CoreTableNames() []string {
	out := make([]string, len(coreTables))
	for i, t := range coreTables {
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
	Missing []string // 6.1.2 にあって DB にない
	Extra   []string // DB にあって 6.1.2 にない(コア形式)
}

func (c *migrationCheck) OK() bool { return len(c.Missing) == 0 && len(c.Extra) == 0 }

// checkMigrations は付録 A §7 の 1〜3 を行う。
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
	want := map[string]bool{}
	for _, v := range coreMigrations {
		want[v] = true
		if !have[v] {
			c.Missing = append(c.Missing, v)
		}
	}
	for _, v := range c.Core {
		if !want[v] {
			c.Extra = append(c.Extra, v)
		}
	}
	for _, vs := range c.Plugins {
		sort.Strings(vs)
	}
	return c
}
