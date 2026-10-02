# buropher

Redmine 6.1.2 互換のチケットシステムを Go で再実装したものです。シングルバイナリで動作します。

- 見た目・標準機能・使い勝手は Redmine 6.1.2 と互換
- DB は専用スキーマ（SQLite / PostgreSQL）。既存 Redmine からは export → import で移行
- 追加機能: LDAP 認証の強化、OIDC SSO（Microsoft Entra ID など）、Discord DM 通知
- Redmine プラグインには非対応

> 開発中です。

## ビルド

```sh
make build        # bin/buropher
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
