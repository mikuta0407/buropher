# LDAP 認証（OpenLDAP / Active Directory）の設定

buropher の LDAP 認証は Redmine の「LDAP 認証」（AuthSourceLdap）と互換で、Redmine から移行した認証方式・
ユーザーはそのまま使える。加えて、buropher 独自の拡張として次の機能がある（管理画面では Redmine の項目の下に
別の欄として表示される）。

| 機能 | 内容 |
|---|---|
| STARTTLS | 平文の LDAP（389）接続を STARTTLS で暗号化する（Redmine は LDAPS のみ） |
| CA 証明書 | 社内 CA などの証明書（PEM の本文またはファイルのパス）を検証に追加する |
| フェイルオーバ | 予備のホストを複数指定し、接続できないときに順に試す |
| Active Directory プリセット | `sAMAccountName` / `userPrincipalName` の属性一式、ネストしたグループ（`LDAP_MATCHING_RULE_IN_CHAIN`）、無効アカウントの判定（`userAccountControl`） |
| グループ同期 | LDAP グループ（`memberOf` 属性 / グループ検索）を buropher のグループに対応付け、ログインのたびに所属を追加・削除する |
| 定期同期 | ディレクトリに見つからない / 無効なユーザーのロック、氏名・メールアドレスの更新、グループ同期をジョブで定期実行する。「今すぐ同期」ボタンと結果のログ |

実装: `internal/auth/ldap`（`ldap.go` が Redmine 互換部分、`ldap_ext.go` が拡張）、
`internal/handler/auth_sources.go`（管理画面・Redmine 互換）、`internal/handler/auth_sources_ldap_ext.go`（拡張の設定・
グループ同期・定期同期）、テスト用の LDAP サーバ `internal/auth/ldap/ldaptest`。

## 1. 認証の流れ（Redmine と同じ）

1. 「アカウント」「パスワード」でサーバに bind する（両方空なら匿名。アカウントに `$login` を含めると、ログイン名を
   埋め込んだ DN とユーザー自身のパスワードで bind する）。
2. ベース DN 以下を `(&(&(objectClass=*)<LDAP フィルタ>)(<ログイン名属性>=<ログイン名>))` で検索してユーザーの DN を得る。
3. その DN と入力されたパスワードで bind できれば認証成功。
4. 未登録のユーザーは「あわせてユーザーを作成」（オンザフライ登録）が有効な認証方式で順に認証し、成功すれば
   名・姓・メールアドレスの属性からユーザーを作成する（メールアドレスが無いなど保存できなければログインできない）。

既存ユーザーを LDAP 認証にするには、管理 → ユーザー → 編集 の「認証方式」で認証方式を選ぶ。

## 2. 管理画面の項目

管理 → LDAP 認証 → 新しい認証方式。上半分は Redmine と同じ項目。

| 項目 | 説明 |
|---|---|
| 名称 | 一覧・ユーザー編集で表示する名前 |
| ホスト / ポート | 389（LDAP / STARTTLS）または 636（LDAPS） |
| 接続方式 | LDAP / LDAPS（証明書の検証なし）/ LDAPS（証明書の検証あり）。STARTTLS は「LDAP」を選んだうえで下の追加項目で有効にする |
| アカウント / パスワード | 検索に使うサービスアカウント（例: `cn=buropher,ou=Services,dc=example,dc=com`、AD は `buropher@example.com` も可） |
| ベース DN | ユーザーを検索する起点 |
| LDAP フィルタ | 対象ユーザーを絞り込む（例: `(memberOf=cn=buropher-users,ou=Groups,dc=example,dc=com)`） |
| タイムアウト | 秒（既定 20） |
| 属性 | ログイン名・名・姓・メールアドレスの属性名 |

### buropher の追加項目

**接続（buropher）**

- **ディレクトリの種類**: 汎用 LDAP / OpenLDAP / Active Directory。Active Directory にすると、ネストしたグループの
  取得に `LDAP_MATCHING_RULE_IN_CHAIN` を使い、定期同期で `userAccountControl` の ACCOUNTDISABLE（0x2）を見る。
- **プリセット**: ボタンで属性・フィルタ・グループの設定を入力する（保存前に確認・修正できる）。
  - OpenLDAP: `uid` / `givenName` / `sn` / `mail`、グループは `(objectClass=groupOfNames)` の `member`
  - Active Directory (sAMAccountName): ログイン名 `sAMAccountName`、フィルタ `(objectClass=user)`、グループは `(objectClass=group)` の `member`
  - Active Directory (userPrincipalName): ログイン名 `userPrincipalName`（`user@example.com` 形式でログインする）
- **STARTTLS**: 接続方式が「LDAP」のときだけ使える（LDAPS との併用は検証エラー）。
- **証明書を検証する（STARTTLS）**: STARTTLS のサーバ証明書を検証する（推奨。社内 CA なら「CA 証明書」も設定する）。
- **予備のホスト**: 1 行に 1 つ `host` または `host:port`（ポート省略時は上のポート）。上のホストに TCP で
  接続できないときに順に試す。bind の失敗（パスワード誤り）では切り替えない。
- **CA 証明書**: PEM の本文を貼り付けるか、サーバ上の PEM ファイルのパスを書く。システムの証明書に追加して
  検証する（LDAPS（証明書の検証あり）か「証明書を検証する（STARTTLS）」のときだけ使われる）。

**グループ同期**

- **グループの所属**:
  - 同期しない（既定）
  - ユーザーの memberOf 属性: ユーザーのエントリの `memberOf`（属性名は「memberOf 属性」で変更可）を使う。
    OpenLDAP では `memberof` オーバーレイが必要。「グループのベース DN」を指定すると、その下のグループだけを使う
    （対応表を CN で書く場合は、ユーザーがグループを作れる OU と区別するために指定することを推奨）。
  - グループを検索: 「グループのベース DN」（空ならベース DN）以下を `(&<グループのフィルタ>(<メンバー属性>=<値>))`
    で検索する。メンバー属性が `member` / `uniqueMember` なら値はユーザーの DN、`memberUid`（posixGroup）なら
    ログイン名。
- **ネストしたグループを含める**: Active Directory は `(member:1.2.840.113556.1.4.1941:=<ユーザー DN>)` で
  間接的な所属も 1 回の検索で得る（memberOf / 検索のどちらでも）。それ以外のディレクトリでは見つかったグループを
  メンバーに持つ親グループを再帰的に検索する（深さ 10 まで。`memberUid` では不可）。
- **対応表**: 「LDAP グループ」にグループの DN（例: `cn=devs,ou=Groups,dc=example,dc=com`）または CN（例: `devs`）、
  右に buropher のグループを選ぶ。大文字小文字・DN の空白は区別しない。

ログイン（LDAP 認証の成功）のたびに、**対応表に現れる buropher グループだけ**について、ユーザーの所属を
追加・削除する。対応表に無いグループの所属には触れない。グループのメンバーシップのロールはユーザーに継承
されるため、プロジェクトの権限を LDAP / AD のグループで管理できる。グループの取得に失敗した場合（権限不足など）は
同期せずにログインを続ける（サーバのログに記録）。

**定期同期**

- **ユーザーを定期的に同期する**: 有効にすると、ジョブ（1 時間ごとに起動）が「間隔（時間）」（既定 24）ごとに、
  この認証方式を使う有効なユーザーをサービスアカウントで 1 人ずつ検索する。アカウントに `$login` を含む設定では
  使えない（検証エラー）。ジョブのワーカーが動いている必要がある（`[jobs] workers` が負でないこと）。
- **見つからない・無効なユーザーをロックする**: ディレクトリに見つからない（LDAP フィルタで除外された場合を含む）
  ユーザーと、Active Directory で無効なユーザーをロックする。管理者はロックしない。再び見つかっても自動では
  ロック解除しない。
- **氏名とメールアドレスを更新する**: ディレクトリの値に合わせる（空の値では上書きしない。他のユーザーが使って
  いるメールアドレスには変更しない）。
- グループ同期が設定されていれば、ログイン時と同じ規則でグループの所属も同期する。

編集画面の下の「同期」欄の **今すぐ同期** で間隔に関係なく実行できる。最後の同期の時刻と結果（件数と、
ユーザーごとの変更・エラー）がこの欄に表示される。

## 3. OpenLDAP の設定例

ディレクトリの例:

```
dc=example,dc=com
├── ou=People     uid=alice (inetOrgPerson)  …
├── ou=Groups     cn=devs (groupOfNames, member: uid=alice,ou=People,dc=example,dc=com)
└── ou=Services   cn=buropher (simpleSecurityObject)
```

サービスアカウントにユーザーとグループの読み取り権限を与える（slapd の ACL の例）:

```
access to dn.subtree="dc=example,dc=com"
  by dn.exact="cn=buropher,ou=Services,dc=example,dc=com" read
  by * break
```

buropher の設定:

| 項目 | 値 |
|---|---|
| ホスト / ポート / 接続方式 | `ldap.example.com` / `389` / LDAP + **STARTTLS**（または `636` / LDAPS） |
| アカウント | `cn=buropher,ou=Services,dc=example,dc=com` |
| ベース DN | `ou=People,dc=example,dc=com` |
| LDAP フィルタ | `(objectClass=inetOrgPerson)` |
| 属性 | `uid` / `givenName` / `sn` / `mail`（プリセット「OpenLDAP」） |
| ディレクトリの種類 | OpenLDAP |
| グループの所属 | グループを検索（グループのベース DN `ou=Groups,dc=example,dc=com`、フィルタ `(objectClass=groupOfNames)`、メンバー属性 `member`） |
| 対応表 | `devs` → 開発者グループ など |

`memberof` オーバーレイを入れている場合は「ユーザーの memberOf 属性」でもよい（検索が 1 回で済む）。
posixGroup（`memberUid`）の場合はフィルタ `(objectClass=posixGroup)`、メンバー属性 `memberUid`。

STARTTLS の確認: `ldapsearch -ZZ -H ldap://ldap.example.com -D cn=buropher,... -W -b ou=People,dc=example,dc=com uid=alice`
が通ること。自己署名・社内 CA の場合は「CA 証明書」に CA の PEM を入れ、「証明書を検証する（STARTTLS）」を有効にする。

## 4. Active Directory の設定例

| 項目 | 値 |
|---|---|
| ホスト / ポート | `dc1.corp.example.com` / `389`（STARTTLS）または `636`（LDAPS） |
| 予備のホスト | `dc2.corp.example.com`（グローバルカタログを使うなら `dc2.corp.example.com:3268`） |
| アカウント | `svc-buropher@corp.example.com`（または DN） |
| ベース DN | `DC=corp,DC=example,DC=com` |
| LDAP フィルタ | `(&(objectClass=user)(objectCategory=person))`。無効なアカウントを最初から除くなら `(!(userAccountControl:1.2.840.113556.1.4.803:=2))` を加える |
| 属性 | プリセット「Active Directory (sAMAccountName)」: `sAMAccountName` / `givenName` / `sn` / `mail` |
| STARTTLS | 有効 +「証明書を検証する（STARTTLS）」（LDAPS なら接続方式「LDAPS（証明書の検証あり）」） |
| CA 証明書 | AD 証明書サービス（エンタープライズ CA）のルート証明書の PEM |
| ディレクトリの種類 | Active Directory |
| グループの所属 | ユーザーの memberOf 属性 +「ネストしたグループを含める」 |
| 対応表 | `CN=buropher-devs,OU=Groups,DC=corp,DC=example,DC=com` → 開発者グループ など |

注意:

- AD は既定で平文の simple bind を拒否することがある（LDAP 署名・チャネルバインディングの要求）。STARTTLS か LDAPS を使う。
- `userPrincipalName` をログイン名にする場合、ユーザーは `alice@corp.example.com` の形でログインし、buropher の
  ログイン名もその形になる。既存ユーザーと混在させないこと。
- ネストしたグループの `LDAP_MATCHING_RULE_IN_CHAIN` 検索は大きなディレクトリでは重いことがある。グループの
  ベース DN を対象の OU に絞るとよい。
- 定期同期で「見つからない・無効なユーザーをロックする」を有効にすると、AD で無効化・削除されたユーザーは
  次回の同期で buropher でもロックされる（無効なユーザーは AD 側でも bind できないため、ログイン自体は
  同期を待たずに失敗する）。

## 5. トラブルシューティング

- 一覧の「テスト」で接続とサービスアカウントの bind を確認できる。エラーは「接続できません (LDAP: …)」に表示される。
- 証明書エラー（`x509: certificate signed by unknown authority`）は「CA 証明書」を設定する。ホスト名と証明書の
  SAN が一致している必要がある（IP アドレスで接続する場合は SAN に IP が必要）。
- グループが同期されない: 「グループの所属」が「同期しない」になっていないか、対応表の LDAP グループの表記
  （DN または CN）、サービスアカウント（`$login` の場合はユーザー自身）にグループの読み取り権限があるかを確認する。
  サーバのログに `LDAP group synchronization` / `LDAP groups synchronized` が出る。
- 定期同期の結果は編集画面の「同期」欄と、サーバのログの `LDAP synchronization` に出る。
