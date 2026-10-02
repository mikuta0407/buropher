# Discord DM 通知

buropher は Redmine と同じ通知（チケットの追加・更新、ニュース、文書、ファイル、フォーラム、Wiki、期日リマインダ）を、
メールに加えて **Discord のダイレクトメッセージ（DM）** で届けられます。DM は buropher に登録した Bot から送られます。

- 受信者の計算（通知設定・ウォッチャー・担当者・閲覧権限・「自分の変更は通知しない」）はメールと同じです。
- ユーザーはマイアカウントで「通知の送信先」を **メール / Discord DM / メールと Discord DM** から選べます。
- パスワード再発行・アカウント登録/有効化・アカウント情報・セキュリティ通知・設定変更の通知は、常にメールで送ります。
- Discord が使えない場合（未連携、無効化、DM 拒否など）はメールで届けます。

## 1. Discord 側の準備

1. [Discord Developer Portal](https://discord.com/developers/applications) で **New Application** を作成します。
2. **Bot** タブで Bot を追加し、**Reset Token** で Bot トークンを取得します（後で buropher に登録します）。
   - Privileged Gateway Intents は不要です（buropher は REST API だけを使います）。
3. **OAuth2** タブで次を控えます。
   - **Client ID** と **Client Secret**
   - **Redirects** に buropher のリダイレクト URL を追加します:
     `<プロトコル>://<ホスト名>/my/discord/callback`
     （管理 > 設定 > 全般 の「ホスト名とパス」「プロトコル」から作られます。設定画面に表示される URL をそのまま登録してください）
4. Bot を通知用のサーバー（Guild）に招待します。OAuth2 > URL Generator で scope `bot` を選び、生成された URL を開きます
   （権限は不要です）。
5. サーバー ID を控えます（Discord の設定 > 詳細設定 > 開発者モード を有効にし、サーバーを右クリック > 「サーバー ID をコピー」）。

> **Bot と DM 先のユーザーは同じサーバーに参加している必要があります**（Discord の仕様。共通のサーバーが無いと
> DM の送信は `50007 Cannot send messages to this user` で失敗します）。buropher はアカウント連携時に
> `guilds.join` で対象サーバーへ自動参加させることができます。ユーザーが「サーバーメンバーからのダイレクトメッセージを
> 許可する」を無効にしている場合も DM は届きません。

## 2. buropher の設定

### config.toml

```toml
[discord]
# 管理画面（管理 > プラグイン）に Discord 通知の設定画面を出す
enabled = true
# 既定は https://discord.com/api/v10 と https://discord.com/oauth2/authorize（通常は指定不要）
# api_base = "https://discord.com/api/v10"
# authorize_url = "https://discord.com/oauth2/authorize"
```

環境変数 `BUROPHER_DISCORD_API_BASE` でも API の接続先を変更できます。

### 管理画面

管理 > プラグイン > **Discord 通知** の「設定」（`/settings/plugin/buropher_discord`）で次を入力します。

| 項目 | 内容 |
|---|---|
| Discord 通知を有効にする | DM での通知を有効にする |
| Bot トークン | Bot のトークン（暗号化して保存。空欄のまま保存すると変更しない） |
| クライアント ID / クライアントシークレット | OAuth2 の Client ID / Secret（シークレットは暗号化して保存） |
| サーバー (Guild) ID | Bot が参加しているサーバー |
| アカウント連携時にサーバーへ参加させる | 連携時に `guilds.join` で上記サーバーへ参加させる |
| 通知の送信先の既定値 | 送信先を選んでいないユーザーの既定（メール / Discord DM / 両方） |
| 連続して失敗したらメールに切り替える回数 | DM の恒久エラー（DM 拒否など）がこの回数続くとメールに切り替える（既定 3） |

設定画面の下には直近の配送ログ（`notification_deliveries`）が表示されます。

## 3. ユーザーの操作

マイアカウントの「設定」欄に次が表示されます（Discord 通知が有効な場合のみ）。

- **通知の送信先**: メール / Discord DM / メールと Discord DM
- **Discord アカウント**: 「Discord アカウントと連携」から Discord の認可画面へ進み、許可すると連携されます
  （scope は `identify`、サーバーへ参加させる設定なら `guilds.join` も要求します）。連携直後にテスト DM を送り、
  届かない場合は理由を表示します。連携済みなら「テストメッセージを送信」「連携を解除」ができます。

## 4. 配送の仕組み

- 通知はコミット後にジョブ（`jobs` テーブル）として積まれ、`buropher serve` のワーカーが送ります。
- DM チャンネルは `POST /users/@me/channels` で作成し、`discord_dm_channels` にキャッシュします。
- メッセージは埋め込み（タイトル `[プロジェクト - トラッカー #番号] 題名`、チケットへのリンク、変更内容、注記の抜粋
  （4096 文字まで））で、受信者の言語で作られます。
- レート制限（429 / `retry_after`、`X-RateLimit-*`）はジョブを延期して再送します。
- 恒久エラー（`50007` DM 不可、ユーザー・チャンネル不明など）はその通知をメールで送り直し、連続回数が閾値に
  達すると本人にメールで知らせて以後の通知をメールに切り替えます。マイアカウントからテスト DM に成功すると再開します。
- Bot トークンが無効（401）の場合はメールで送り、配送ログに記録します。

## 5. トラブルシューティング

| 症状 | 確認すること |
|---|---|
| 連携ボタンが出ない | 管理画面で有効化し、クライアント ID / シークレットを登録したか |
| 認可後にエラー | Developer Portal の Redirects に設定画面のリダイレクト URL が登録されているか、ホスト名とプロトコルの設定 |
| テスト DM が届かない（50007） | Bot と同じサーバーに参加しているか、サーバーメンバーからの DM を許可しているか |
| 通知がメールで届く | 送信先の設定、配送ログの失敗理由、恒久エラーで切り替わっていないか |
