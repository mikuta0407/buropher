// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package archive は Redmine エクスポートアーカイブ(tar + zstd)の形式定義と
// ストリーミング読み書きを提供する。形式の仕様は docs/export-format.md を参照。
//
// アーカイブ内のエントリ順は固定:
//
//	manifest.json                 … 先頭(読み手はまずこれだけ読めば検証できる)
//	tables/<table>.ndjson         … 1 行 1 JSON オブジェクト(テーブル名順)
//	files/<disk_directory>/<disk_filename> … 添付ファイル実体(任意)
package archive

import "time"

const (
	// FormatName は manifest.json の format 値。
	FormatName = "buropher-redmine-export"
	// FormatVersion はアーカイブ形式の版。互換性のない変更で上げる。
	FormatVersion = 1

	// ManifestPath はマニフェストのエントリ名。
	ManifestPath = "manifest.json"
	// TablesDir はテーブルデータのディレクトリ。
	TablesDir = "tables/"
	// FilesDir は添付ファイル実体のディレクトリ。
	FilesDir = "files/"
)

// Manifest は manifest.json の内容。
type Manifest struct {
	Format        string    `json:"format"`
	FormatVersion int       `json:"format_version"`
	CreatedAt     time.Time `json:"created_at"`
	Tool          Tool      `json:"tool"`
	Source        Source    `json:"source"`

	// SchemaMigrations は書き出し元の schema_migrations.version 全件(コア+プラグイン, ソート済み)。
	SchemaMigrations []string `json:"schema_migrations"`
	// RedmineSchema は書き出し元のスキーマ版("6.1" / "7.0")。schema_migrations のコア集合から判定する。
	// 空(この項目より前のアーカイブ)は "6.1" とみなす。
	RedmineSchema string `json:"redmine_schema,omitempty"`
	// PluginMigrations はプラグイン由来のマイグレーション(<version>-<plugin_id>)をプラグイン別にまとめたもの。
	PluginMigrations []PluginMigrations `json:"plugin_migrations"`

	// Tables は書き出したテーブル(エントリ順)。
	Tables []Table `json:"tables"`
	// IncludeFiles は添付ファイル実体をアーカイブに含めたか。
	IncludeFiles bool `json:"include_files"`
	// Files はアーカイブに含めた添付ファイル(エントリ順)。
	Files []File `json:"files"`
	// MissingFiles は attachments から参照されているがディスクに存在しなかったファイル。
	MissingFiles []MissingFile `json:"missing_files"`

	// EncryptedValues は暗号化形式(aes-256-cbc:...)の値の件数("table.column" → 件数)。
	EncryptedValues map[string]int64 `json:"encrypted_values,omitempty"`
	// Warnings は書き出し時の警告(プラグイン、余分な列/テーブル、値の異常など)。
	Warnings []string `json:"warnings"`
}

// Tool は書き出しツールの情報。
type Tool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Source は書き出し元の情報。
type Source struct {
	// DBKind は mysql / postgres / sqlite / sqlserver。
	DBKind string `json:"db_kind"`
	// DBVersion はサーバ版(取得できた場合)。
	DBVersion string `json:"db_version,omitempty"`
	// Timezone は Redmine サーバの IANA タイムゾーン名。datetime 値はこの TZ の naive 時刻。
	Timezone string `json:"timezone"`
	// RedmineVersion は lib/redmine/version.rb から読んだ版(--redmine-root 指定時)。
	RedmineVersion string `json:"redmine_version,omitempty"`
	// CipherKeyConfigured は configuration.yml に database_cipher_key が設定されていたか(--redmine-root 指定時)。
	CipherKeyConfigured *bool `json:"cipher_key_configured,omitempty"`
	// AttachmentsDir は添付ファイルの読み取り元ディレクトリ。
	AttachmentsDir string `json:"attachments_dir,omitempty"`
	// AcceptanceForced は受け入れ判定に失敗したが --force で続行したことを示す。
	AcceptanceForced bool `json:"acceptance_forced,omitempty"`
}

// PluginMigrations は 1 プラグイン分のマイグレーション版。
type PluginMigrations struct {
	Plugin   string   `json:"plugin"`
	Versions []string `json:"versions"`
}

// Column はテーブルの列(Redmine/ActiveRecord の論理型付き)。
type Column struct {
	Name string `json:"name"`
	// Type は integer/string/text/boolean/datetime/date/float/decimal/binary。
	Type string `json:"type"`
}

// Table はテーブルエントリの情報。
type Table struct {
	Name    string   `json:"name"`
	Path    string   `json:"path"`
	Rows    int64    `json:"rows"`
	Size    int64    `json:"size"`
	SHA256  string   `json:"sha256"`
	Columns []Column `json:"columns"`
}

// File は添付ファイルエントリの情報。Path は files/ からの相対パス(disk_directory/disk_filename)。
type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// MissingFile は欠損した添付ファイル。
type MissingFile struct {
	Path          string  `json:"path"`
	AttachmentIDs []int64 `json:"attachment_ids"`
}

// Table は名前でテーブル情報を探す。
func (m *Manifest) Table(name string) (*Table, bool) {
	for i := range m.Tables {
		if m.Tables[i].Name == name {
			return &m.Tables[i], true
		}
	}
	return nil, false
}
