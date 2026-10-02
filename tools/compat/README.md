# 互換テストハーネス (tools/compat)

本物の Redmine 6.1.2（参照）と buropher（候補）に同じリクエスト列を流し、
レスポンスを正規化して unified diff で比較する。設計は `_planning/08_testing.md`。

```
tools/compat/
  redmine-ref.sh          参照 Redmine（公式 test/fixtures 投入済み）の setup/start/stop/reset
  ruby/                   fixtures 投入ランナー・時刻固定イニシャライザ
  main.go                 CLI（diff / check / snapshot / fetch）
  internal/normalize      HTML/JSON/XML 正規化
  internal/udiff          Myers 差分 + unified diff
  internal/scenario       シナリオ（YAML/TXT）読み込み
  internal/client         ログイン・取得
  internal/report         レポート（Markdown/HTML）・許容差分リスト
testdata/compat/
  scenarios/*.yml         シナリオ
  golden/<scenario>/      正規化済み参照出力（snapshot で生成、CI で check に使う）
  allowlist.yml           許容差分リスト
```

## 1. 参照 Redmine（fixtures 版）

```sh
tools/compat/redmine-ref.sh start    # 初回は setup も実行（約 20 秒）→ http://127.0.0.1:3998
tools/compat/redmine-ref.sh status
tools/compat/redmine-ref.sh reset    # 停止 → 投入直後の DB を復元 → 起動
tools/compat/redmine-ref.sh stop
tools/compat/redmine-ref.sh setup --force   # 作り直し
```

- `_reference/redmine-migrated`（バンドル設定・プリコンパイル済みアセット・secret_token 込み）を
  `_reference/redmine-fixtures` にコピーし、新規 sqlite DB を `db:migrate`、
  `test/fixtures/*.yml` 全 43 セットを `ActiveRecord::FixtureSet.create_fixtures` で投入する。
  投入直後の DB は `db/redmine.pristine.sqlite3` に保存され、`reset` で書き戻す。
- **時刻固定**: fixtures 内の ERB（`1.day.ago` 等）は `COMPAT_FROZEN_TIME`（既定 `2026-01-15 12:00:00 UTC`）で評価し、
  サーバプロセスも同時刻に固定する（`config/initializers/zz_compat_frozen_time.rb`、`travel_to`）。
  相対時間・期日・`last_login_on`・フッターの年などが決定的になる。`COMPAT_FROZEN_TIME=` で固定解除。
- `TZ=UTC`、Rails の time_zone は既定（UTC）、設定は既定値のまま。ただし **REST API のみ有効化**
  (`Setting.rest_api_enabled = 1`。テストの `with_settings` 相当）。
- 添付ファイルの保存先は `test/fixtures/files`（テストの `set_fixtures_attachments_directory` 相当）。
- アセットは public/assets（プリコンパイル済み）を Rails が配信（`RAILS_SERVE_STATIC_FILES=1`）。
- ログ: `_reference/redmine-fixtures/log/compat-server.out`, `production.log`。
- 3999 で動いている `redmine-migrated` インスタンスには触れない。

### fixtures のユーザー

| login | password | 備考 |
|---|---|---|
| admin | admin | 管理者 |
| jsmith | jsmith | eCookbook の Manager |
| dlopper | foo | Developer |
| rhill | foo | |
| someone / miscuser8 | foo | |
| dlopper2, miscuser9 | （ログイン不可） | hashed_password ダミー |

## 2. 比較の実行

```sh
# 参照 vs 候補（各サーバに同じ順序でリクエスト列を流す。参照は取得前に reset）
go run ./tools/compat diff -ref http://127.0.0.1:3998 -cand http://127.0.0.1:3000 \
  -reset-ref 'tools/compat/redmine-ref.sh reset'

# ゴールデンファイルの作成/更新（参照のみ。Ruby が必要）
go run ./tools/compat snapshot -ref http://127.0.0.1:3998 -reset-ref 'tools/compat/redmine-ref.sh reset'

# 候補 vs ゴールデン（CI 向け、Ruby 不要）
go run ./tools/compat check -cand http://127.0.0.1:3000

# 1 URL の正規化結果を見る（デバッグ）
go run ./tools/compat fetch -base http://127.0.0.1:3998 -user admin /issues/1
go run ./tools/compat fetch -user jsmith -raw /issues/1.json
```

- シナリオ省略時は `testdata/compat/scenarios/*.yml` をすべて実行。`-run 'smoke/issues_1__.*'` で絞り込み（`<scenario>/<case id>` の正規表現）。
- 出力: `compat-report/summary.md`, `summary.html`, `diffs/<scenario>/<id>.diff`,
  `normalized/{ref,cand}/...`（正規化後の全文）。fail/error があれば終了コード 1。
- `-reset-cand` も指定可能（候補の DB を初期化するコマンド）。環境変数 `COMPAT_REF`, `COMPAT_CAND`, `COMPAT_RESET_REF`, `COMPAT_RESET_CAND` でも指定できる。
- ケース ID は `<パス由来>__<ユーザー>`（例: `issues_new_project_id_1__jsmith`）。ゴールデンは `golden/<scenario>/<id>.<html|json|xml|txt>`。

比較対象の文書は次の形式（ヘッダ部 + 正規化済み本文）:

```
status: 302
content-type: text/html
location: {{BASE}}/login?back_url={{BASE}}%2Fmy%2Fpage

<本文>
```

リダイレクトは追わない。

## 3. シナリオ形式

```yaml
name: smoke                    # 省略時はファイル名
users:                         # 未定義ユーザーは login = password = ユーザー名
  dlopper: {login: dlopper, password: foo}
headers: {Accept-Language: en} # 全リクエスト共通ヘッダ
normalize:                     # シナリオ全体の正規化設定（下記）
  mask_text_selectors: ["#footer"]
requests:
  - path: /issues/1
    users: [anonymous, admin, jsmith]
  - path: /issues.json
    users: [admin]
    auth: basic                # session（ログインフォーム+Cookie）/ basic / none。API 形式は既定で basic
    format: json               # 省略時は Content-Type から判定
  - path: /issues/1
    id: issues_1_post          # ケース ID を明示
    method: PUT
    user: admin
    form: {"issue[subject]": "x"}   # session 認証時は authenticity_token を自動付与
  - path: /account/lost_password
    method: POST
    user: anonymous
    csrf: true                 # 未ログインのフォーム送信でも authenticity_token を付与する
    expect_status: 302         # 参照側のステータス確認
    normalize: {strip_selectors: ["#sidebar"]}
  - path: /gantt
    skip: "未対応"
```

ファイルのアップロードと動的な URL（`imports_write.yml` を参照）:

```yaml
  - id: issues_create
    path: /imports
    method: POST
    user: jsmith
    form: {type: "IssueImport"}
    files: {file: ../files/imports/import_issues.csv}   # multipart/form-data（相対パスはシナリオファイル基準）
    capture: {issues: '/imports/([0-9a-f]+)/settings'}  # Location から変数を取り出す（参照・候補で別々に保持）
  - id: issues_settings
    path: /imports/${issues}/settings                   # Path / Form / Body の ${name} を展開
    users: [jsmith]
```

簡易 TXT 形式（`.txt`）: `[METHOD] /path [user1,user2] [format=json] [auth=basic]`、`#` 以降コメント。

**注意**: Redmine は GET でも DB を更新する（最近使ったプロジェクト、RSS キー生成、`last_login_on` 等）。
そのためケースは記述順に逐次実行し、参照・候補それぞれ同じ順序で流してから比較する。
シナリオの順序を変えたらゴールデンを取り直すこと。

## 4. 正規化

常に適用:
- CSRF トークン（`meta[name=csrf-token]`, `input[name=authenticity_token]`、および同じ文字列の出現箇所すべて）→ `{{CSRF}}`
- Redmine の `form_tag_html` が付けるランダムなフォーム name（`form-1a2b3c4d`）→ `form-RANDOM`
- アセットダイジェスト `-[0-9a-f]{8}.(css|js|png|svg|…)` → `-DIGEST.ext`
- 対象サーバのベース URL（素の形と URL エンコード形）→ `{{BASE}}`、ホスト:ポート単体 → `{{HOST}}`
- フィード/API キー `key=<40桁hex>` → `key=KEY`
- HTML: 1 要素 1 行のインデント形式に整形、属性をソート、テキストの連続空白を圧縮して trim、空白のみのテキストとコメントを除去。
  `pre`/`textarea` の中身は保持、`script`/`style` は行末空白と前後の空行のみ除去。
- JSON: キー順を保ったまま 2 スペースインデント（数値は元の表記のまま、`200.0` と `200` は区別）。
- XML: 要素順を保ったまま整形、属性ソート、空要素は `<a/>`。

設定可能（`normalize:`。リストは上位設定に追記、bool は上書き）:

| キー | 内容 |
|---|---|
| `strip_selectors` | CSS セレクタに一致する要素を削除 |
| `mask_text_selectors` | 一致要素の中身を `MASKED` に |
| `mask_attrs` | `{selector, attr}` の属性値を `MASKED` に |
| `regex` | `{pattern, replace}` の正規表現置換（全テキスト・属性・JSON/XML 文字列） |
| `mask_keys` | JSON キー名 / XML 要素名・属性名の値を `MASKED` に |
| `mask_relative_times` | 英語の相対時間（`about 2 hours`, `3 days` 等）を `RELTIME` に |
| `mask_timestamps` | ISO8601 日時を `TIMESTAMP` に |
| `sort_keys` | JSON キーをソート |
| `sort_classes` | class 属性のトークンをソート |
| `keep_comments` | HTML コメントを残す |
| `preserve_edge_space` | テキスト前後の空白（1 個に圧縮）を残す（厳密モード） |

時刻固定しているので `mask_relative_times` / `mask_timestamps` は既定オフ。

## 5. 許容差分 (testdata/compat/allowlist.yml)

```yaml
entries:
  - case: "smoke/issues_1__*"     # "<scenario>/<case id>" の glob
    reason: "追加機能のボタン"      # 必須
    lines: ['buropher-extra']      # 差分行がすべていずれかに一致すれば許容
  - case: "smoke/settings__admin"
    reason: "未実装"
    ignore: true                   # ケース全体を許容
```

許容されたケースは `ALLOWED` として集計され、失敗扱いにならない。

## 6. buropher 側で check するときの前提

ゴールデンは「fixtures 投入済み DB + 時刻 2026-01-15 12:00:00 UTC + REST API 有効 + シナリオ順に逐次アクセス」の結果。
buropher も同じ DB（`_reference/redmine-fixtures/db/redmine.pristine.sqlite3`、または同等の import 結果）と
同じ固定時刻で起動し、実行前に DB を初期状態へ戻すこと（`-reset-cand`）。

## 7. 既知の制約

- `test/fixtures/repositories` のリポジトリ（tar 展開が必要）は用意していないため、リポジトリ画面は対象外。
- メール送信は `:test`（送信しない）。
- 相対時間マスクは英語ロケールのみ。
- インライン `<script>` 内の JSON は 1 行のまま比較されるため、差分行が長くなることがある。
- 参照 Redmine 同士（3998 vs 3998）の比較で差分 0 になることを確認済み（`-reset-ref`/`-reset-cand` に `redmine-ref.sh reset` を指定）。
