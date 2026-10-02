# Redmine エクスポートアーカイブ形式(format_version 1)

`buropher redmine export` が出力し、`buropher redmine import` が読み込む中立形式。
Redmine 6.1.x の DB を**型変換せずに**保全することを目的とし、変換(TZ → UTC、真偽値の既定値補完、
YAML → JSON 等)はすべてインポート側で行う。

実装: `internal/redmineimport/archive`(読み書き)、`internal/redmineimport/export`(書き出し)。

## 1. コンテナ

- **tar(POSIX pax)を zstd で圧縮**した単一ファイル。拡張子は `.tar.zst`。
- エントリ順は固定:
  1. `manifest.json`(必ず先頭)
  2. `tables/<table>.ndjson`(テーブル名の昇順)
  3. `files/<disk_directory>/<disk_filename>`(添付ファイル実体、任意。パスの昇順)
- 読み手は先頭の `manifest.json` だけで内容を検証できる。テーブルは添付より前にあるため、
  テーブルだけ読む場合は添付部分を伸長しなくてよい。
- すべてのエントリは manifest に SHA256 とサイズが記録され、読み手はエントリごとに照合する
  (`archive.Reader` は不一致・欠落・未記載エントリをエラーにする)。
- tar の mtime は `created_at`(秒切り捨て)、mode は 0644。

## 2. manifest.json

```jsonc
{
  "format": "buropher-redmine-export",
  "format_version": 1,
  "created_at": "2026-10-03T00:18:30Z",          // UTC
  "tool": { "name": "buropher", "version": "v0.1.0" },
  "source": {
    "db_kind": "sqlite",                          // mysql / postgres / sqlite / sqlserver
    "db_version": "3.50.4",
    "timezone": "Asia/Tokyo",                     // --source-timezone(IANA 名、必須)
    "redmine_version": "6.1.2.stable",            // --redmine-root 指定時のみ
    "cipher_key_configured": false,               // --redmine-root 指定時のみ(鍵そのものは書かない)
    "attachments_dir": "/srv/redmine/files",      // 添付の読み取り元
    "acceptance_forced": false                    // --force で受け入れ判定を無視した場合 true
  },
  "schema_migrations": ["1", "2", "...", "20250611092227", "1-redmine_agile"],
  "plugin_migrations": [ { "plugin": "redmine_agile", "versions": ["1-redmine_agile"] } ],
  "tables": [
    {
      "name": "attachments",
      "path": "tables/attachments.ndjson",
      "rows": 4,
      "size": 1834,
      "sha256": "…",
      "columns": [ { "name": "id", "type": "integer" }, { "name": "container_id", "type": "integer" }, "…" ]
    }
  ],
  "include_files": true,
  "files": [ { "path": "2026/10/261003001804_copy.txt", "size": 17, "sha256": "…" } ],
  "missing_files": [ { "path": "2026/10/261003001804_gone.txt", "attachment_ids": [3] } ],
  "encrypted_values": { "users.twofa_totp_key": 1, "repositories.password": 1 },
  "warnings": [ "plugin \"redmine_agile\" has 1 migrations; plugin data is not exported" ]
}
```

| キー | 説明 |
|---|---|
| `schema_migrations` | 書き出し元の `schema_migrations.version` 全件(コア+プラグイン)。コアは数値順、他は辞書順。新スキーマには取り込まない |
| `plugin_migrations` | `<version>-<plugin_id>` 形式の版をプラグイン別に集計 |
| `tables[].columns` | 書き出した列と ActiveRecord の論理型(`integer` `string` `text` `boolean` `datetime` `date` `float` `binary`)。列順は ndjson のキー順と同じ |
| `files` | アーカイブに含めた添付(`include_files=false` なら空) |
| `missing_files` | `attachments` が参照しているが添付ディレクトリに存在しないファイル(パスごとに参照元 attachment id) |
| `encrypted_values` | `aes-256-cbc:` 形式の値の件数(対象: `users.twofa_totp_key`, `repositories.password`, `auth_sources.account_password`)。インポートで `--cipher-key` が要るかの判断用 |
| `warnings` | プラグイン、非コアテーブル/余分な列、解釈できなかった値、欠損ファイル等 |

配列キーは空でも `[]` を出力する(`encrypted_values` と任意の `source` 項目は省略されうる)。

## 3. tables/&lt;table&gt;.ndjson

- 1 行 = 1 レコード = 1 JSON オブジェクト(UTF-8、改行 `\n` 区切り、最終行も `\n` で終わる)。
- キーは `columns` の列名すべて(順序も同じ)。値が NULL なら `null`。
- 行順: 単一主キー(`id`)のあるテーブルは主キー昇順、結合テーブル(`groups_users` 等)は全列の昇順。

### 値の表現

| 論理型 | JSON 表現 | 備考 |
|---|---|---|
| `integer` | 数値(整数、`.` や指数部を含まない) | 64bit。`filesize` 等も同じ |
| `float` | 数値(**必ず `.` か指数部を含む**: `2.0`, `0.30000000000000004`, `1e+21`) | 読み手は「`.`/`e` を含む数値 = 浮動小数」で整数と区別できる。NaN/±Inf は `{"$float":"NaN"}` / `{"$float":"+Inf"}` / `{"$float":"-Inf"}` |
| `boolean` | `true` / `false` | ソースの `1/0`、`'t'/'f'`、`'true'/'false'`、MySQL `tinyint(1)`、SQL Server `bit` を正規化 |
| `datetime` | 文字列 `"YYYY-MM-DD HH:MM:SS"` または `"YYYY-MM-DD HH:MM:SS.ffffff"` | **TZ 変換なしの naive 値**(Redmine サーバの壁時計時刻。`source.timezone` で解釈する)。小数秒は 0 なら省略、あれば 6 桁 |
| `date` | 文字列 `"YYYY-MM-DD"` | |
| `string` / `text` | 文字列(加工なし) | YAML シリアライズ列も原文のまま(`rubyyaml` パッケージで読む) |
| `binary` | `{"$b64": "<標準 Base64>"}` | `wiki_content_versions.data` など |

例外:

- `string`/`text` 列に不正な UTF-8 があった場合は `{"$b64": ...}` で原文バイト列を保持し、警告を出す。
- 型として解釈できない値(例: MySQL の `0000-00-00 00:00:00`、解釈不能な真偽値)は**原文の文字列のまま**残し、
  警告(列名・件数・例)を出す。インポート側で扱いを決める。
- 日時値に明示的なオフセットが付いていた場合(通常の Redmine は書かない)は壁時計部分を残してオフセットを捨て、警告を出す。

`archive.Reader` は値を Go の `nil` / `bool` / `int64` / `float64` / `string` / `[]byte` として返す。

### 対象テーブル

Redmine 6.1.2 のコア 56 テーブルのうち、`imports` と `import_items`(CSV インポート履歴)を除く **54 テーブル**。
`schema_migrations` は manifest にのみ記録し、`ar_internal_metadata` は書き出さない。
プラグインのテーブルやコアテーブルに追加された列は書き出さない(警告に列挙)。

## 4. files/

- `files/` 以下のパスは `attachments.disk_directory` と `disk_filename` を `/` で結合したもの
  (`disk_directory` が NULL/空の古い添付は `files/<disk_filename>`)。Redmine の保存レイアウトと同じ。
- 重複排除により複数の attachments 行が同じファイルを指す場合も、実体は 1 回だけ格納する。
- `..` を含むパス・絶対パスは格納しない(警告)。
- `--no-files` では格納しないが、添付ディレクトリが指定されていれば存在確認は行い `missing_files` を記録する。

## 5. 受け入れ判定(エクスポート時)

付録 A §7 の通り。不合格ならアーカイブを作らずに終了する(`--force` で続行した場合は
`source.acceptance_forced=true` と警告を記録し、欠落列は `null` で出力)。

1. `schema_migrations` のコア版(`^\d+$`)集合が Redmine 6.1.2 の 322 件と完全一致。
   不足 → Redmine を 6.1.x に上げて `db:migrate` するよう案内、余剰 → 未対応バージョン。
   Redmine 6.1.0 / 6.1.1 / 6.1.2 の `db/migrate` は同一(ファイル名・内容とも)なので、6.1.0〜6.1.2 はすべて受け入れる。
2. プラグイン版(`^\d+-(.+)$`)は警告として記録。
3. コア 56 テーブルと全列の存在(名前で照合、型は DB 差を許容)。余分なテーブル・列は警告。

## 6. 一貫性

書き出しは 1 本の読み取り専用トランザクション内で行う。

| DB | 方式 |
|---|---|
| SQLite | `mode=ro` + `query_only` で開き、deferred トランザクション(WAL ならスナップショット) |
| PostgreSQL | `REPEATABLE READ READ ONLY` |
| MySQL/MariaDB | `REPEATABLE READ` + `START TRANSACTION READ ONLY`(InnoDB の一貫読み取り) |
| SQL Server | `SNAPSHOT`(DB で許可されていなければ `READ COMMITTED` に落として警告。Redmine を停止して実行すること) |

日時列は SQLite では `CAST(... AS TEXT)`、PostgreSQL では `::text` で文字列として取得し、
MySQL は `parseTime=false`(文字列)で取得するため、ドライバによる TZ 解釈は入らない。

## 7. 互換性

- 読み手は `format` と `format_version` を確認し、未知の版は拒否する。
- フィールドの追加は版を上げずに行うことがある(読み手は未知のキーを無視すること)。
