# buropher

Redmine 6.1.2 互換のチケットシステムを Go で再実装したものです。シングルバイナリで動作します。

- 見た目・標準機能・使い勝手は Redmine 6.1.2 と互換
- DB は専用スキーマ（SQLite / PostgreSQL）。既存 Redmine からは export → import で移行
- 追加機能: LDAP 認証の強化、OIDC SSO（Microsoft Entra ID など）、Discord DM 通知
- Redmine プラグインには非対応

> 開発中です。

## ドキュメント / Documentation

- [Installation](docs/install.md) — binary, Docker, systemd, reverse proxy, HTTPS, backups
- [Configuration](docs/configuration.md) — config keys, environment variables, relevant settings
  (example: [config.example.toml](config.example.toml))
- [Migrating from Redmine](docs/migration-from-redmine.md) — export / import / verify, cutover checklist
- [Compatibility](docs/compatibility.md) — what matches Redmine 6.1.2 and known deviations
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

`web/assets` と `web/locales/redmine` は Redmine 6.1.2 から取り込んだものです。

```sh
tools/sync-upstream.sh <redmine-src> <gems-dir>
```

## ライセンス

GNU General Public License v2 またはそれ以降（[LICENSE](LICENSE)）。
Redmine (Copyright (C) Jean-Philippe Lang) の派生物です。同梱の JavaScript ライブラリとフォントはそれぞれのライセンスに従います。
