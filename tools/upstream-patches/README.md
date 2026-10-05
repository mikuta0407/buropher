# upstream-patches

`web/UPSTREAM_VERSION` より新しい Redmine のセキュリティ修正をアセットへ当てるためのパッチ置き場。
`tools/sync-upstream.sh` が同期後に `*.patch` を当て直す。上流の新版に取り込まれたら削除する
（例: 7.0.2 #44429 は 7.0.2 への追従で削除済み）。
