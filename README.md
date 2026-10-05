# Buropher

Buropher は Redmine 7.0.2 互換（Redmine-compatible）のプロジェクト管理・チケットシステムを Go で再実装したものです。
シングルバイナリ（`buropher`）で動作します。

Buropher is a Redmine-compatible project management web application written in Go
(a derivative work of [Redmine](https://www.redmine.org/), GPL-2.0-or-later).

- 見た目・標準機能・使い勝手は Redmine 7.0.2 と互換
- DB は専用スキーマ（SQLite / PostgreSQL）。既存 Redmine からは export → import で移行
- 追加機能: LDAP 認証の強化、OIDC SSO（Microsoft Entra ID など）、Discord DM 通知
- Redmine プラグインには非対応

> 開発中です。

## ドキュメント / Documentation

- [Installation](docs/install.md) — binary, Docker, systemd, reverse proxy, HTTPS, backups
- [Configuration](docs/configuration.md) — config keys, environment variables, relevant settings
  (example: [config.example.toml](config.example.toml))
- [Migrating from Redmine](docs/migration-from-redmine.md) — export / import / verify, cutover checklist
- [Compatibility](docs/compatibility.md) — what matches Redmine 7.0.2, features added in Redmine 7.0 and known deviations
- [Development](docs/development.md) — architecture, package map, compat harness, upstream sync, releases
- Extensions: [OIDC SSO / Entra ID](docs/sso-entra-id.md), [Discord DM notifications](docs/discord.md), [PDF](docs/pdf.md)
- Internals (Japanese): [schema](docs/schema.md), [export format](docs/export-format.md), [import rules](docs/import.md)

## ビルド

```sh
make build        # bin/buropher
./bin/buropher init -admin-password 'change-me'
./bin/buropher serve
```

## upstream アセットの同期

`web/assets` と `web/locales/redmine` は Redmine 7.0.2 から取り込んだものです（同期した版は `web/UPSTREAM_VERSION`）。
同梱ライブラリ・アイコン・フォントのライセンス文は Redmine の `doc/licenses` から `docs/licenses/` に取り込んでいます。

```sh
tools/sync-upstream.sh <redmine-src> <gems-dir>
```

## ライセンス / License

Copyright (C) 2026 mikuta0407 and Buropher contributors

GNU General Public License v2 またはそれ以降（GPL-2.0-or-later、[LICENSE](LICENSE)）。
Buropher は Redmine（Copyright (C) 2006- Jean-Philippe Lang）の派生物です。Redmine から取り込んだ部分
（テンプレート・CSS/JS/画像・ロケール・移植したロジック・テスト用フィクスチャ）は [NOTICE](NOTICE)、
同梱のサードパーティ製コンポーネント（JavaScript ライブラリ・フォント・Go モジュール等）とそのライセンス、
GPL との互換性の考え方は [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) を参照してください。
Go のソースファイルには SPDX ライセンス識別子を付けています。

「Redmine」は派生元プロジェクトの名称で、Buropher は Redmine プロジェクトとは無関係の独立したプロジェクトです。
互換性のため、X-Redmine-* ヘッダなど機械向けの識別子は Redmine の名称のまま維持しています（[docs/compatibility.md](docs/compatibility.md)）。
