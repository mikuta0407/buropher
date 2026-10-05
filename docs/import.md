# Redmine インポート / 検証

`buropher redmine import` は `buropher redmine export` のアーカイブ(形式は [export-format.md](export-format.md))を
buropher の新スキーマ([schema.md](schema.md))へ取り込み、`buropher redmine verify` が結果をアーカイブと突き合わせる。

実装: `internal/redmineimport/importer`(`Run` / `Main`)、`internal/redmineimport/verify`(`Verify` / `Main`)、
秘密値の再暗号化に `internal/crypto/secretbox`。

## 1. 使い方

```sh
buropher migrate                                   # 空 DB をマイグレーション(buropher init は実行しない)
buropher redmine import --dsn data/buropher.db --files-dir data/files \
    --cipher-key "$REDMINE_DATABASE_CIPHER_KEY" --secret-key "$BUROPHER_SECRET_KEY" \
    --report-json import-report.json redmine-export.tar.zst
buropher redmine verify --dsn data/buropher.db --files-dir data/files --password admin:xxxx redmine-export.tar.zst
```

| フラグ(import) | 説明 |
|---|---|
| `--driver` / `--dsn` | 書き込み先(既定 `sqlite` / `data/buropher.db`、環境変数 `BUROPHER_DB_DRIVER` / `BUROPHER_DB_DSN`) |
| `--archive` または引数 | エクスポートアーカイブ |
| `--cipher-key` | Redmine の `database_cipher_key`(環境変数 `REDMINE_CIPHER_KEY`)。暗号化列の復号に使う |
| `--secret-key` | buropher の `server.secret_key`(環境変数 `BUROPHER_SECRET_KEY`)。秘密値を secretbox 形式で再暗号化する |
| `--files-dir` | 添付の保存先(既定 `data/files`、`BUROPHER_ATTACHMENTS_PATH`)。空文字ならファイルをコピーしない |
| `--source-files-dir` | アーカイブを `--no-files` で作った場合の添付の読み元(Redmine の `files/`) |
| `--temp-dir` | テーブル展開用の一時ディレクトリ(既定: アーカイブと同じディレクトリ) |
| `--dry-run` | 変換・検査をすべて行ってからロールバック(ファイルもコピーしない) |
| `--report-json` | レポートを JSON で出力(`-` で標準出力)。テキストのレポートは常に標準出力 |
| `--migrate` | 先に未適用のマイグレーションを適用する |

`verify` のフラグ: `--driver` `--dsn` `--files-dir`(ファイルの存在・サイズ)`--digests`(digest も照合)
`--password login:pw`(複数可)`--strict`(インポート時に破棄した行も失敗扱い)`--report-json`。

Go API:

```go
rep, err := importer.Run(ctx, db, "export.tar.zst", importer.Options{
    CipherKey: "...", NewCipherKey: cfg.Server.SecretKey, FilesDir: cfg.Storage.AttachmentsPath,
    SourceFilesDir: "", DryRun: false, Logger: slog.Default(),
})
rep.WriteText(os.Stdout); rep.WriteJSON(f)       // エラー時もそれまでのレポートを返す
vr, err := verify.Verify(ctx, db, "export.tar.zst", verify.Options{FilesDir: "...", Passwords: map[string]string{"admin": "..."}})
```

## 2. 処理の流れ

1. **前提確認**: マイグレーション済み(未適用なし)かつ空(`principals` `projects` `settings` `issues` `roles` `trackers` が 0 行)。
   空でなければ `ErrNotEmpty`。
2. **アーカイブを 1 回だけ読む**: マニフェストとハッシュを検証しつつ、テーブルを一時ディレクトリに展開
   (変換は FK 依存順でアーカイブはテーブル名順のため)。添付実体は `FilesDir/.buropher-import-*/` に staging する。
3. **1 トランザクションで変換**(SQLite は `PRAGMA defer_foreign_keys`、PG は `SET CONSTRAINTS ALL DEFERRED`)。
   順序は付録 A §6.1 に従う: settings → auth_sources → users → email/groups/tokens → statuses/trackers/enumerations/roles →
   projects(+closure)/modules/project_trackers/time_entry_activities → custom fields → members/member_roles → versions/categories →
   projects.default_version_id → workflows → issues/relations → journals → time_entries → documents/news/comments/boards/messages →
   wiki → repositories/changesets → queries → projects.default_issue_query_id → user_preferences → 通知設定 →
   custom_values/attachments/watchers/reactions → oauth → 導出値 → シーケンス → 整合性チェック。
   挿入は複数行 INSERT(最大 500 行 / 30000 パラメータ)。
4. **整合性チェック**: FK(SQLite `PRAGMA foreign_key_check`、PG `SET CONSTRAINTS ALL IMMEDIATE`)、ポリモーフィック孤児
   (attachments / custom_values / watchers / reactions の種別ごとに `NOT EXISTS`)、階層(issues の root_id / hier_path、
   project_closure の自己行・親行)。1 つでも失敗すればロールバックしてエラー。member_roles の継承行の差分は警告のみ。
5. **コミット**(DryRun はロールバック)後、staging の添付を本来の位置 `FilesDir/<disk_directory>/<disk_filename>` へ移す。
   既に同じ内容のファイルがあればそのまま、内容が異なれば上書きせず警告。

## 3. レポート

ソーステーブルごとに `source_rows`(アーカイブの行数)、`imported`(書き込み先テーブル別の行数)、
`dropped` / `repaired`(理由ごとの件数と最大 10 件のサンプル ID。結合テーブルは `a-b` 形式)を持つ。
ほかに添付ファイルの結果(コピー数・既存・欠損とサンプル)、チェック結果、警告(DST の曖昧/欠落時刻、
プラグイン、member_roles 継承差分など)。テキストと JSON(`Report.WriteText` / `WriteJSON`)で出力できる。

`dropped` は取り込まなかった行、`repaired` は値を補完・修正して取り込んだ行(1 行に複数の補完があれば重複して数える)。

## 4. 共通の変換規則

| 項目 | 規則 |
|---|---|
| ID | Redmine の ID を保持する。新規に作る行(欠けていた組込プリンシパル、wiki_contents にしかない最新版)は max(id)+1 から採番。最後に全テーブルの採番を `ResetSequence` で max(id) の次へ |
| 日時 | naive 値をマニフェストの `source.timezone` で解釈して UTC へ。DST の曖昧な時刻は**早い方**、存在しない時刻は欠落前のオフセットで解釈(= 時計を進める、Ruby の `Time.local` と同じ)。件数とサンプルを警告に出す。日付のみ(`2026-01-13`)は 0 時とみなす |
| 日時の欠損 | NOT NULL 列が NULL/解釈不能なら関連列(created ↔ updated)→ インポート時刻の順で補完して `repaired` に記録。NULL 許容列の不正値は NULL |
| 真偽値 | `1/0`、`t/f`、`true/false`、`yes/no` を受理。NULL は列の既定値 |
| 空文字列 | **Redmine の値をそのまま保持**(`''` は `''`、NULL は NULL。docs/schema.md 決定 10 / D-17)。例外はドメイン列への正規化で `''` が値として成り立たない列のみ: `twofa_scheme`(CHECK)、`mail_notification`(`''`/未知 → NULL = 既定)、`repositories.identifier`(既定リポジトリの空 → NULL。一意索引が `IS NOT NULL` 部分索引のため)、リポジトリ・チェンジセットの任意文字列(`root_url` `login` `path_encoding` `log_encoding` `scmid` `from_path` `from_revision` `revision` `branch`)、OAuth の `code_challenge(_method)` `refresh_token`、digest(長さ不正は NULL) |
| 不正 UTF-8 | Latin-1 とみなして UTF-8 へ変換 |
| 必須参照の欠損 | 作成者系(author_id, user_id, journals.user_id 等)→ 匿名ユーザー。マスタ(tracker/status/priority/activity/document category)→ 既定値。所有者(project, issue, board, wiki …)の欠損 → 行を破棄 |
| 任意参照の欠損 | NULL(assigned_to, category, fixed_version, updated_by, changesets.user_id …) |
| 名前の一意性 | DB で一意にした名前(status/tracker/role/priority/document category/activity/custom field/version/category)が重複したら 2 行目以降を `"<name> (<id>)"` に改名。wiki ページ名(大文字小文字無視)は `<title>_<id>`、ログイン名(大文字小文字無視)は `<login>-<id>`、プロジェクト識別子は `<identifier>-<id>`、リポジトリ識別子は `<identifier>-<id>` |
| 重複行 | 結合テーブル・メールアドレス(大文字小文字無視)・トークン値・関係・ウォッチャ等の重複は最初の行(ID 順)を残して破棄 |
| 階層 | projects / issues / boards / wiki_pages / messages の親が存在しない(または自分自身)なら NULL。循環は循環内の最小 ID の親を NULL にして切る |
| YAML | `rubyyaml` で読み、Hash の挿入順を保ったまま JSON 化(シンボルは文字列、`!binary` は復号)。読めない YAML は無視して記録 |
| 秘密値 | §6 |

## 5. テーブル別の変換

| Redmine | buropher | 主な規則・判断 |
|---|---|---|
| settings | settings / legacy_settings | `settings.yml` にある名前のみ settings。serialized は YAML → JSON、それ以外は **JSON 文字列**(int 項目も文字列のまま: `internal/settings` が非シリアライズ値を文字列で扱うため。付録 A の「int は number」から変更)。未知・廃止・プラグインの設定と YAML が読めない値は legacy_settings へ原文退避。同名の重複は ID 最小を採用。updated_on NULL はインポート時刻。`default_issue_start_date_to_creation_date` の行がない(6.1 の)DB は `'1'` を保存(Redmine 7.0 のマイグレーション 20260320090000 と同じく従来の挙動を保つ。新規インストールの既定は 0) |
| auth_sources | auth_sources | `AuthSourceLdap` のみ(他は破棄、参照ユーザーの auth_source_id は NULL)。kind `ldap`、`config` JSON(`host` `port` `account` `base_dn` `filter` `timeout` `tls` `verify_peer` `attr_login` `attr_firstname` `attr_lastname` `attr_mail`、値のないキーは省略)、`account_password` → `secret`(§6)。enabled=true、position は行順、created/updated はインポート時刻 |
| users | principals / user_accounts | type → kind(未知の type は破棄、NULL はログインがあれば User)。組込(匿名ユーザー・組込グループ)の 2 行目以降は user / group に変換。グループ系は lastname → `name`(firstname/lastname は空)。status 0〜3 以外は既定値。user_accounts は User / AnonymousUser のみ: `password_hash` = `redmine-sha1$<salt>$<hash>`(salt 空なら `redmine-sha1-nosalt$$<hash>`、空なら NULL、匿名ユーザーは常に NULL)、language はそのまま、存在しない auth_source → NULL、twofa_scheme `totp` 以外は 2FA リセット、TOTP 鍵は §6(復元できなければ 2FA リセット)。匿名ユーザー・組込グループがなければ作成 |
| email_addresses | email_addresses | ユーザー不在・空・重複(大文字小文字無視)を破棄。既定アドレスが複数なら 2 つ目以降を解除、0 件なら最古を既定に |
| groups_users | group_users | group 側は kind=group(組込グループ不可)、user 側は kind=user のみ |
| tokens | tokens / twofa_backup_codes | updated_on → updated_at(Redmine 7.0 では最終使用日時)。`api` `feeds` は常に、`autologin` は設定 `autologin`(日数)が正で期限内のみ、`recovery` `register` は 1 日以内のみ(基準は `Options.Now`)。`session` `twofa_session` は破棄(全員再ログイン)。`twofa_backup_code` は値の SHA-256 hex を twofa_backup_codes へ |
| user_preferences | user_preferences / user_project_bookmarks / user_recent_projects | others の既知キーを列へ(comments_sorting asc/desc、warn_on_leaving_unsaved 未設定=true、textarea_font、recently_used_projects 既定 3、history_default_tab、toolbar_language_options、default_issue_query / default_project_query → 種別の合うクエリ ID、auto_watch_on、my_page_layout、my_page_settings)。`bookmarked_project_ids` / `recently_used_project_ids` は存在するプロジェクトだけ子テーブルへ(順序 = position)。その他のキーは `extra`。同一ユーザーの 2 行目は破棄 |
| (users + user_preferences) | user_notification_settings | 全ユーザー(匿名含む)に 1 行: mail_notification(7.0 の `only_my_watches` を含む。`''`/未知 → NULL)、no_self_notified / notify_about_high_priority_issues(others の `true`/`'1'`。設定行のないユーザーは `default_users_no_self_notified`)、channels=`email` |
| members.mail_notification | user_notified_projects | 真のメンバーのうちプリンシパルがユーザーのもの |
| issue_statuses / trackers | 同名 | position NULL は末尾に採番。default_done_ratio 範囲外 → NULL。trackers.default_status_id 不在 → 先頭ステータス。fields_bits → `disabled_core_fields`(CORE_FIELDS 順、bit 10 以上は無視して記録)。trackers.private_by_default(7.0)はそのまま、6.1 は false |
| enumerations | issue_priorities / document_categories / time_entry_activities | type で振り分け(`Enumeration` 等の異常な type は破棄)。優先度・文書カテゴリの project_id/parent_id は無視。活動のプロジェクト上書きは: プロジェクト不在 → 破棄(工数は親活動へ付け替え)、親が不正 → 無効なシステム活動として残す。position_name は再計算 |
| roles | roles / role_permissions / role_permission_trackers | name 空は `Role <id>`。builtin の重複・不正値は 0。visibility 不正値は既定値。permissions は Redmine と同じ正規表現 `:([a-z0-9_]+)` で抽出し、`internal/permission` にない権限は破棄して記録。settings の `permissions_all_trackers[perm] == '0'` → all_trackers=false、`permissions_tracker_ids[perm]` の存在するトラッカーを role_permission_trackers へ。default_time_entry_activity_id は活動の取り込み後に設定 |
| projects | projects / project_closure | lft/rgt は使わず parent_id から閉包(自己行 depth 0 を含む)。identifier NULL → `project-<id>`、status 不正 → 1。homepage/description はそのまま(`''` と NULL を区別)。default_version_id / default_issue_query_id は後段で UPDATE(不在なら NULL) |
| enabled_modules | project_modules | project_id NULL・未知のモジュール名・重複を破棄。ID 保持(ニュースのウォッチャ用) |
| custom_fields | custom_fields (+ enumerations, 3 結合表) | 未知の type・field_format は破棄。possible_values → JSON 文字列配列(空要素除去)。format_store → `format_settings` JSON。**値は Redmine の表現(文字列)のまま**で、`user_role` / `version_status` の空要素だけ除去(付録 A の型正規化は行わない: 利用側がフォーム値と同じ表現を期待できるように) |
| members / member_roles | 同名 | プリンシパル・プロジェクト不在、重複を破棄。member_roles の inherited_from が存在しない行は(連鎖的に)破棄 |
| versions / issue_categories | 同名 | status/sharing 不正値は open/none、名前はプロジェクト内で一意化 |
| workflows | workflow_transitions / workflow_field_rules | old_status_id 0 → NULL(新規)。field_name が数字 → custom_field_id(不在なら破棄)、コアフィールド名以外は破棄。rule は readonly/required のみ |
| issues | issues | 2 パス: 1 周目で親子関係(不在・循環を修正)、2 周目で root_id / hier_path(`%010d/` の連結、自身を含む)を計算して挿入。プロジェクト不在 → 破棄。done_ratio は 0〜100 に丸める |
| issue_relations | 同名 | 逆向き型(duplicated / blocked / follows / copied_from)は from/to を入れ替えて正方向へ、relates は from < to に。自己関係・正規化後の重複は破棄 |
| journals / journal_details | issue_journals / issue_journal_details | journalized_type=Issue のみ。notes はそのまま。details の値は変換しない。cf は prop_key を custom_field_id に(数字でなければ破棄)、未知の property は破棄 |
| time_entries | 同名 | author_id NULL・不在 → user_id。活動不在 → 親活動または既定活動。tyear/tmonth/tweek(ISO 週)は spent_on から再計算 |
| documents / news / comments | documents / news / news_comments | category_id 0/不在 → 既定カテゴリ(is_default、なければ先頭)。news.project_id NULL は破棄。comments は commented_type=News のみ。comments_count は再計算 |
| boards / messages | 同名 | board の親は同一プロジェクトのみ。メッセージは 2 階層に正規化(返信への返信はトピックへ付け替え)。sticky(整数)/ locked(NULL)→ bool。カウンタ・last_message_id / last_reply_id は再計算 |
| wikis / wiki_pages | 同名 | 1 プロジェクト 1 wiki(2 つ目は破棄)。current_version = wiki_contents.version(なければ版の最大値、版がなければ 0) |
| wiki_content_versions + wiki_contents | wiki_page_versions | 版を伸長(`gzip` は zlib deflate、gzip ヘッダ付きも可、`''` は生バイト)。wiki_contents は最新版: 同じ版があれば **wiki_contents を正**として上書き(差異を記録)、なければ追加 |
| wiki_redirects | 同名 | redirects_to_wiki_id 不在 → wiki_id |
| repositories | 同名 | type → scm、extra_info YAML → JSON、password は §6、identifier `''`→NULL・重複は改名、既定リポジトリはプロジェクトに 1 つ |
| changesets / changes / changeset_parents / changesets_issues | changesets / changeset_files / … | (repository, revision) の重複・孤児を破棄。commit_date はそのまま(ローカル日付) |
| queries / queries_roles | 同名 | type → kind。user_id 0 → NULL(システムクエリ)、存在しないユーザー → 匿名ユーザー(Redmine のユーザー削除と同じ)、存在しないプロジェクトのクエリは破棄。filters は挿入順を保った JSON オブジェクト、column_names は文字列配列、sort_criteria は `[[列, 向き], ...]`、options の `totalable_names` / `display_type` は専用列、残りは `options` |
| custom_values | 同名 | customized_type → kind(`User`/`Group` は principal、`IssuePriority` 等は enumeration も受理)。カスタムフィールドの所有種別と整合しない行・参照先不在は破棄。値の `''` と NULL はそのまま、bool の `t/f/true/false` → `1/0`、int/float/progressbar の前後空白除去 |
| attachments | 同名 | container_type → container_kind(未知は破棄、コンテナ不在は破棄、type/id の片方だけ空なら未紐付け扱い)。digest は長さで sha256(64)/ md5(32)、それ以外は NULL |
| watchers / reactions | 同名 | 種別変換(`EnabledModule` → project_module)、参照先不在・user NULL・重複を破棄。reactions はユーザー(user_accounts)のみ |
| oauth_* | 同名 | アプリは全件(uid 重複は破棄)。grant は未失効かつ期限内のみ。access token は未失効で、期限切れでも refresh_token があれば残す |
| webhooks / projects_webhooks(7.0) | webhooks / webhook_projects | ユーザー不在・URL 空の Webhook は破棄。events は YAML 配列 → JSON 文字列配列(空要素除去、配列でなければ空)。**secret は平文のまま**(Redmine も平文で保存し編集フォームに表示する。HMAC 署名に平文が必要で、buropher へのログイン資格情報でもないため §6 の再暗号化はしない。空文字は NULL)。projects_webhooks は Webhook・プロジェクト不在と重複を破棄。6.1 のアーカイブには無い |
| imports / import_items | — | 移行しない(アーカイブにも含まれない) |

## 6. 秘密値(TOTP 鍵、LDAP バインドパスワード、リポジトリのパスワード)

| ソースの値 | `--cipher-key` | `--secret-key` | 結果 |
|---|---|---|---|
| `aes-256-cbc:...`(Redmine で暗号化) | あり | あり | 復号して secretbox 形式で再暗号化 |
| 同上 | あり | なし | **エラー**(平文で保存しない) |
| 同上 | なし / 復号失敗 | — | 値を破棄。TOTP は 2FA リセット(要再登録)、LDAP/リポジトリのパスワードは NULL。`repaired` に記録 |
| 平文(Redmine に鍵がなかった) | — | あり | secretbox 形式で暗号化 |
| 同上 | — | なし | 平文のまま保存して `repaired` に記録 |

secretbox 形式(`internal/crypto/secretbox`): `"sb1:" + base64url_nopad(nonce(12) || AES-256-GCM 暗号文+タグ)`、
鍵は `HMAC-SHA256(server.secret_key, "buropher.secretbox.v1")`。API キー・フィードキーは「キーを表示」機能のため平文のまま
(schema.md の決定)。パスワードハッシュは `redmine-sha1$...` のまま保存し、初回ログイン時に argon2id へ再ハッシュする
(`internal/auth/password`)。

## 7. 添付ファイル

- アーカイブに実体があればそれを、なければ `--source-files-dir` から、取り込んだ attachments が参照するパス
  (`disk_directory/disk_filename`、重複排除で共有されたものは 1 回)を staging にコピーし、コミット後に `--files-dir` へ移す。
- 見つからないファイルは「missing」として件数とサンプル(パスと添付 ID)を報告する(行は取り込む)。
- MD5 digest の SHA-256 への再計算は行わない(`digest_algo='md5'` として保持)。

## 8. 検証(verify)

- **件数と ID**: ID を保持する各テーブルについて、アーカイブにない ID が新 DB にあれば失敗(生成されうる principals /
  user_accounts / wiki_page_versions は除く)。アーカイブにあって新 DB にない ID は件数とサンプルを表示(`--strict` で失敗)。
  結合テーブルは件数のみ。
- **階層**: 旧 lft/rgt が parent_id と整合している場合のみ、projects は「lft/rgt の祖先関係 == project_closure」、
  issues は「旧 `ORDER BY root_id, lft` == 新 `ORDER BY root_id, hier_path`」を照合(整合していなければ警告してスキップ)。
  hier_path が親の hier_path + 自身であることは常に確認。
- **添付**: `--files-dir` 指定時、ファイルの存在(マニフェストの missing_files にあるものは警告のみ)、サイズ不一致(警告)、
  `--digests` で digest 不一致(警告)。
- **パスワード**: `--password login:pw` ごとに `password.Verify`。

## 9. 判断・付録 A からの逸脱(まとめ)

1. 非シリアライズ設定は int 項目も JSON 文字列で保存(`internal/settings` の表現に合わせた)。
2. `format_settings` の値は Redmine の文字列表現のまま(型正規化しない)。
3. `user_preferences` の no_self_notified / notify_about_high_priority_issues は `user_notification_settings` へ(schema.md の決定)。
4. tokens は schema の CHECK に合わせ `api` `feeds` `autologin`(期限内)`recovery` `register`(1 日以内)を移行。2FA バックアップコードは SHA-256 で twofa_backup_codes へ。
5. LDAP 設定は `auth_sources.config` JSON(キー名は Redmine の列名)+ `secret`。
6. 暗号化列は「暗号文のまま転記」ではなく復号して secretbox で再暗号化(鍵がなければ破棄・2FA リセット)。
7. 欠けている組込プリンシパル(匿名ユーザー・組込グループ)は作成する(`buropher init` を実行しないため)。
8. 存在しないユーザーのクエリは匿名ユーザーへ付け替え(システムクエリ化はしない)。
9. wiki_contents と最新版の内容が異なる場合は wiki_contents を正とする(付録 A のとおり)。
10. member_roles の継承行は再計算・修復せず、期待集合との差分を警告に出す。
