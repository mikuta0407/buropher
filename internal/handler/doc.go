// Package handler は Redmine のコントローラ（app/controllers）の移植。
//
// ApplicationController の before_action（session_expiration, user_setup,
// check_if_login_required, set_localization, check_password_change, check_twofa_activation）は
// App.Handle でルートごとに適用する。各アクションは *Req（コントローラのインスタンス相当）を受け取る。
//
// # CONVENTIONS
//
// コントローラを追加するときの規約。
//
// # 規約: ファイルとルート
//
//   - 1 コントローラ = 1 ファイル（issues_controller.rb → issues.go）。ファイルには
//     コントローラ変数（var IssuesController = &Controller{Name: "issues", MainMenu: true, ...}）、
//     ルート登録メソッド（func (a *App) routesIssues(r Router)）、アクション（func (a *App) IssuesIndex(c *Req)）を置く。
//   - routes.go の Routes には a.routesIssues(r) の 1 行だけを足す（並列開発での衝突を最小にするため、
//     ルート本体は各コントローラのファイルに書く）。
//   - ルートは config/routes.rb と同じパス・メソッドで a.Handle(r, method, pattern, ctrl, action, fn, opts...) により登録する。
//     パスパラメータは chi の {id} 形式。".json" 等の拡張子付きは httpx.Route が自動で登録し、
//     params[:format] は httpx.Format(c.R) で得る。パスパラメータ・クエリ・本文は c.Params() にまとまる（Rails の params）。
//   - Controller.Name は controller_name（権限判定 authorize と menu の選択に使う）、Action は action_name。
//     menu_item の宣言は Controller.MenuItem、default_search_scope は Controller.DefaultSearchScope。
//
// # 規約: フィルタ（before_action）
//
// コントローラの before_action は Handle の opts に宣言順に並べる（filters.go）:
//
//	// before_action :find_project, :authorize, :only => [:settings]
//	a.Handle(r, http.MethodGet, "/projects/{id}/settings", ProjectsController, "settings", a.ProjectsSettings,
//		FindProject("id"), Authorize())
//
// 用意しているフィルタ:
//
//   - FindProject(param) / FindProjectByProjectID() / FindOptionalProjectByID() / FindOptionalProject()
//     は Redmine の find_project 系（id か識別子、無ければ 404）。
//   - Authorize() / AuthorizeGlobal() は authorize / authorize_global（Controller.Name と action を
//     permission の AllowedActions で判定。拒否時はアーカイブ済み → notice_not_authorized_archived_project の 403、
//     モジュール無効 → 403、それ以外 → deny_access）。
//   - RequireLogin() / RequireAdmin() / CheckProjectPrivacy() はそれぞれ require_login / require_admin / check_project_privacy。
//   - Skip(FilterLoginRequired, ...) は skip_before_action。AcceptAPIAuth() / AcceptAtomAuth() は accept_api_auth / accept_atom_auth。
//   - 独自の before_action は Before(func(c *Req) { ... }) で書く。render / redirect したら以降は実行されない（c.Halted()）。
//     find_issue のようなモデル取得もこの形で書き、見つからなければ c.Render404("")、見えなければ c.DenyAccess()。
//   - 同じ判定をアクション内で使うときは c.Authorize(ctrl, action, global) / c.RequireAdmin() / c.FindProject(id) を呼ぶ。
//   - after_action の record_project_usage は Handle が自動で行う（c.Project を設定したアクションでは
//     最近使ったプロジェクトが更新される）。
//
// # 規約: 現在のユーザーと権限
//
//   - User.current は c.User（*domain.User。常に非 nil、匿名なら AnonymousUser）。HTTP ハンドラの外側からは
//     CurrentUser(r)。ユーザーを切り替えるときは c.SetUser(u)（ログイン処理は setLoggedUser）。
//   - 権限判定はすべて internal/authz の Authorizer を使う: c.Authz()（リクエスト内で 1 つ・遅延生成・キャッシュあり）、
//     外側からは Authz(r)。よく使う形は c.AllowedTo(domain.Perm("edit_issues"), c.Project) と
//     c.AllowedToGlobally(domain.Perm("add_project"))。可視性の SQL は c.Authz().VisibleCondition /
//     AllowedToCondition / IssueVisibleCondition / PrincipalVisibleCondition で作る。
//     メンバーシップ・ロールを変更した後は c.ResetAuthz()。
//   - 個人設定は c.Pref()（*domain.UserPreference、行が無ければ既定値）。
//   - 設定値は a.Settings（Setting.xxx）。現在時刻は a.now()（clock.Now。互換テストの固定時刻に従う）。time.Now() は使わない。
//
// # 規約: SQL の置き場所
//
//   - SQL は internal/repository に書く（ハンドラ・ヘルパーに SQL を書かない）。関数は
//     func Xxx(ctx context.Context, q db.Queryer, ...) の形で、見つからなければ repository.ErrNotFound を返す。
//   - repository は authz に依存できない（authz が repository に依存するため）。可視性で絞るクエリは
//     authz が返す SQL 断片を引数で受け取る（例: repository.LatestNews(ctx, q, visibleCond, 5)）。
//   - モデルの型は internal/domain、Redmine のモデルのうち DB を要しない判定（Project#allows_to?、User#name 等）も domain に置く。
//   - 書き込みを複数行で行うときは a.DB.WithTx で 1 トランザクションにまとめる。
//
// # 規約: 描画
//
//   - HTML は c.Render("issues/index", data)（web/templates/issues/index.html.tmpl を base レイアウトで描画）。
//     data はインスタンス変数に相当する map[string]any か構造体。レイアウトの変更は RenderOptions{Layout: ...}、
//     ステータスは RenderOptions{Status: ...}。テンプレートでは domain の型をそのまま使い、
//     パス・リンクは internal/helper のテンプレート関数（project_path, link_to_project, link_to_user, news_path ...）を使う。
//     必要な helper 関数が無ければ internal/helper に Redmine のヘルパー名で追加する。
//   - エラーは c.Render404("") / c.Render403("") / c.RenderError(status, message)（API 形式なら本文なし）。
//     c.Render403 は @project を nil にする（render_403 と同じ）。
//   - render / redirect 後は return する（Render・Redirect は c.Halt() 済み）。
//
// # 規約: flash とリダイレクト
//
//   - flash[:notice] = l(:notice_successful_update) は c.Flash().Set("notice", c.L("notice_successful_update"))、
//     flash.now は c.Flash().Now(key, msg)。ショートカット c.Flash().SetNotice / SetError / SetWarning もある。
//   - redirect_to は c.Redirect(path)（相対パスは絶対 URL に直る）、redirect_back_or_default は
//     c.RedirectBackOrDefault(def, referer)。back_url の検証は httpx.ValidateBackURL。
//
// # 規約: API（.json / .xml）
//
//   - API を受け付けるアクションは AcceptAPIAuth() を付ける（API キー: key パラメータ / X-Redmine-API-Key、
//     OAuth2 の Bearer トークン（Authorization: Bearer / access_token / bearer_token パラメータ）、
//     HTTP Basic、管理者の X-Redmine-Switch-User を find_current_user が処理する）。
//   - OAuth2 で認証したユーザーは c.User.OAuthScope（トークンのスコープ）を持ち、権限判定（authz）は
//     Role#allowed_to?(action, scope) と同じくスコープとの積になる。IsAdmin は admin スコープがある場合のみ真。
//     プロバイダ側（Doorkeeper 互換の /oauth/*）は oauth_*.go と internal/auth/doorkeeper。
//   - 形式の判定は httpx.Format(c.R) / httpx.IsAPIRequest(c.R) / httpx.Negotiate。API のエラーは
//     a.Errors.RenderAPIErrors（422）・c.RenderError（ステータスのみ）。
//   - JSON / XML の本文（.api.rsb テンプレート）は internal/apibuilder（Redmine::Views::Builders の移植）で組み立て、
//     c.RenderAPI(status, func(b apibuilder.Builder) { ... }) で返す（status 0 は 200。json / xml 以外は 406。
//     JSONP は Setting.jsonp_enabled と callback / jsonp パラメータで自動処理）。
//
// .api.rsb の DSL と apibuilder の対応（左が Redmine、右が Go）:
//
//	api.array :users, api_meta(total_count: n) do  →  b.Array("users", c.APIMeta(apibuilder.A("total_count", n)), func() {
//	api.user do                                    →  b.Object("user", func() {
//	api.id user.id                                 →  b.Value("id", u.ID)  // nil は null / <id/>、time.Time は xmlschema
//	api.project :id => 1, :name => "x"             →  b.Attrs("project", apibuilder.A("id", 1, "name", "x"))
//	api.custom_field attrs do api.value v end      →  b.ObjectAttrs("custom_field", attrs, func() { b.Value("value", v) })
//
// その他の API 用ヘルパー: c.RenderAPIOK()（render_api_ok = 204）、c.RenderAPIErrors(msgs...) /
// c.RenderValidationErrors(errs)（422）、c.IncludeInAPIResponse("memberships")（include_in_api_response?）、
// c.APIMeta（nometa / X-Redmine-Nometa）、c.APIOffsetAndLimit()（api_offset_and_limit）。
// API 形式ではセッションを使わない（find_current_user と同じ）。
//
// # 規約: ページネーション・検証エラー
//
//   - 一覧のページ分割は internal/pagination（Redmine::Pagination::Paginator）と c.PerPageOption()（per_page_option）を使い、
//     テンプレートでは {{pagination_links_full .Pages .Count}}（per_page_links を含む。リンクは現在のパス + クエリ）。
//   - モデルの検証エラーは internal/validation（ActiveModel::Errors 相当。full_messages は human_attribute_name の規則で
//     field_<model>_<attr> / field_<attr> を引く）。フォームのモデルが ValidationErrors() を実装すれば error_messages_for に渡せる。
//   - before_action で取得したモデル（@user など）は c.setValue(key, v) / c.value(key) で持たせる（req_values.go）。
//
// # 規約: 添付ファイル
//
// 添付ファイル（Attachment / acts_as_attachable）は internal/attachments の Store（a.AttachmentStore。
// 保存先は config の storage.attachments_path、ディスク上の配置は Redmine と同一）を使う。行の SQL は
// internal/repository/attachments.go、型は domain.Attachment（Token / IsImage / IsPDF ... は Attachment のメソッド）。
//
//   - 新規作成（params[:attachments] の file）とトークン（POST /uploads で作った未紐付けの添付）の両方を
//     受ける save_attachments は a.AttachmentStore.SaveAttachments(ctx, tx, c.Params().Get("attachments") の値, c.User, c.Loc)、
//     コンテナの保存と同じトランザクションで AttachSaved(ctx, tx, res, domain.AttachmentContainerWikiPage, id)。
//     保存済みのコンテナへの Attachment.attach_files は AttachFiles(ctx, q, kind, id, attachments, c.User, c.Loc)。
//   - res.FailedCount > 0（見つからないトークン）はコンテナの検証エラー res.FailedMessage(c.Loc)
//     （warn_about_failed_attachments）。render_attachment_warning_if_needed(obj) は c.AttachFilesWarning(res)。
//   - トランザクションがロールバックされたら a.AttachmentStore.DeleteFromDisk(ctx, a.DB, res.Created...)。
//   - 一覧は repository.ContainerAttachmentList(ctx, q, kind, id)（created_on, id 順・author 読み込み済み）。
//     コンテナ削除時は tx 内で repository.DeleteContainerAttachments(ctx, tx, kind, ids) し、コミット後に
//     a.AttachmentStore.DeleteFromDisk(ctx, a.DB, deleted...)（after_commit :delete_from_disk）。
//   - 可視性（Attachment#visible?）は c.AttachmentVisible(att)。ダウンロードの Content-Disposition は
//     httpx.ContentDisposition（send_file / send_data と同じ書式）。
//   - 添付フォーム（attachments/_form）の JS は POST /uploads.js（attachments#upload）を呼ぶ。
//   - Wiki は保存を AttachmentSaver（wiki_support.go。既定は Store.AttachFiles）経由で行い、表示は
//     repository.ContainerAttachmentList と attachments/_links（helper の link_to_attachments）だけを使う。
//     共通の添付基盤を差し替えるときは NewWikiAttachmentSaver を合わせる。
//   - AttachmentsController の残りのアクション（attachments_more.go）: show（テキストはシンタックスハイライト、
//     Markdown / Textile は書式変換、画像、.diff / .patch は internal/unifieddiff による inline / sbs 表示、その他は
//     common/_other）、thumbnail（a.AttachmentStore.Thumbnail(att, size)。純 Go の縮小で ThumbnailsRoot に
//     "#{digest}_#{filesize}_#{size}.thumb" を保存。PDF は生成せず 404）、update（API のみ）、destroy（チケットの
//     添付は issues.Env の InitJournal / JournalizeAttachment / SaveJournal でジャーナルに記録）、edit_all /
//     update_all / download_all（/attachments/<object_type>/<id>/...）。
//   - コンテナごとの表示・編集・削除の権限（attachments_visible? / attachments_editable? / attachments_deletable?）は
//     a.containerAttachmentsPermitted(c, ct, "edit" | "delete")、link_to_attachment_container は a.containerLink。
//     current_menu_item はコンテナの種類で決まり（c.setAttachmentMenuItem）、コンテナが無ければ nil
//     （helper.NoMenuItem。ジャンプボックスのリンクに jump を付けない）。
//   - 再表示するフォームに保存済み（トークン付き）の添付を出すときは attachments/_form に
//     (dict "saved_attachments" res.Files) を渡す（container.saved_attachments）。
//
// # 規約: ウォッチャー
//
//   - WatchersController（watchers.go）は 1 つで、ウォッチ対象の種類は registerWatchable で object_type ごとに登録する
//     （issue / wiki / wiki_page / news / board / message / enabled_module）。watchableType は Load（id → 対象と project）・
//     Visible（visible?(user)。nil なら visible? を持たないモデルとして project.visible? / valid_watcher? = true）・
//     SetWatcher（nil なら watchers テーブルを直接更新）・WatchersPartial（サイドバーの部分テンプレート）を持つ。
//     watchable.Type は object_type（権限名・CSS・URL）、watchable.Kind は watchers.watchable_kind
//     （enabled_module → project_module）。チケットの API（POST /issues/:id/watchers,
//     DELETE /issues/:id/watchers/:user_id）も同じアクション。
//   - 画面の watcher_link は helper の watcher_link "<object_type>" id（Watcher.any_watched? と同じく直接のウォッチのみ）。
//     JS 応答（watchers/_set_watcher）は handler の watcherLink（複数オブジェクトの bulk に対応）。
//   - サイドバーは共通の watchers/_watchers（dict "object_type" "id" "project"）。チケットだけはビューモデルを使う
//     issues/_watchers。作成時の add_author_as_watcher は repository.AddWatcher(ctx, tx, kind, id, userID)。
//
// # 規約: 通知（Mailer.deliver_* / Redmine::Notifiable）
// # 規約: リポジトリ（SCM）
//
//   - Git のみ対応（D-15）。git コマンドの実行は internal/scm（GitAdapter の移植。実行ファイルは設定 scm.git_command
//     → a.GitCommand）、チェンジセットの取り込みとコミットメッセージのチケット参照（修正キーワード・作業時間）は
//     internal/scmsync（a.scmService()）。SQL は internal/repository/scm.go、型は domain.Repository / domain.Changeset。
//   - リポジトリの URL は helper.RepositoryURL（テンプレートでは repo_url）で生成する（routes.rb の優先順位と rev の
//     制約を再現する）。:format => 'html' のルートは routeFormat("html") を付ける（ロードマップのメニューが versions.html になる）。
//   - 定期取り込みは設定 scm.fetch_interval（internal/server/scm_fetcher.go）、外部からは /sys/fetch_changesets（sys.go）。
//
// # 規約: メール通知のフック
//
// メール・Discord DM の通知は a.Notify（*notify.Service。nil でも呼べる）に、DB のコミット後に渡す
// （Redmine の after_create_commit / deliver_later と同じ。通知の失敗で操作を失敗させない）。
// Setting.notified_events の判定・受信者の計算・チャネル（メール / Discord）の決定・ジョブ投入は notify が行う。
//
//   - チケット: 書き込みは a.writeIssuesEnv(c, q)（Notifier は a.issueNotifier()。App.Notifier があればそれ、
//     メールか Discord が使えれば a.Notify、どちらも無効なら LogNotifier）で行い、コミット後に
//     a.dispatchIssueNotifications(c, res...)（issues.SaveResult.Notifications を配送。失敗はログのみ）。
//     ジャーナルを作る操作（作成・更新・一括編集・関連の追加削除・チケットの添付の削除）はすべてこれを呼ぶ。
//     CSV インポートは issueState().env（Notifier 設定済み）で保存後に env.Dispatch（Issue#notify = settings['notifications']）。
//   - ニュース・コメント・文書・ファイル・フォーラム: コミット後に a.notify(c, setting_event, mailer_action, obj)
//     （content_common.go）。Setting.notified_events に setting_event があれば a.Notify の NewsAdded /
//     NewsCommentAdded / DocumentAdded(ctx, docID, c.User) / AttachmentsAddedFor(ctx, event, ids)（files#create は
//     file_added、documents#add_attachment は document_added）/ MessagePosted を呼ぶ。
//   - Wiki の作成・本文の更新 a.Notify.WikiContentAdded / WikiContentUpdated（wiki_actions.go）。
//   - アカウント・セキュリティ系（常にメール）: AccountInformation、AccountActivationRequest（自己登録の承認待ち）、
//     AccountActivated、LostPassword(ctx, user, token, recipient)、Register(ctx, user, token)、PasswordUpdated、
//     SecurityNotification / EmailAddress*（メールアドレスの追加・変更・削除）/ AdminFlagChanged / Twofa（2FA の
//     有効化・無効化・バックアップコード）、SettingsUpdated、TestEmail（同期送信）。sender は c.User、remote_ip は c.remoteIP()。
//   - メールの本文は mailer*.go（notify.Renderer の実装）と web/templates/mailer/*.tmpl。期待値は
//     internal/server/testdata/mail（Redmine で生成。gen/regen.sh）。
//
// # 規約: 認証・sudo モード・アカウントのメール
//
//   - require_sudo_mode は、既存コントローラの宣言を sudo_mode.go の sudoModeTable にまとめてあり（runBeforeActions の最後に適用）、
//     新しく移植するコントローラでは Handle の opts の before_action の位置に RequireSudoMode(methods...) を置く。
//     既定では無効（config の [auth] sudo_mode）。
//   - パスワード再発行・登録・2 要素認証のセキュリティ通知などのメールは a.accountMailer()（AccountMailer。
//     account_mailer.go）経由で送る。メール配送が設定されていれば server が App.Mailer に
//     NotifyAccountMailer（account_mailer_notify.go。a.Notify の LostPassword / Register /
//     AccountActivationRequest / AccountActivated / PasswordUpdated / SecurityNotification に委譲）を設定する。
//     未設定（nil）ならログに記録するだけ。a.Notify を直接呼んでもよい（Twofa(ctx, user, c.User, c.remoteIP(),
//     action, "totp") など。上の「規約: 通知」）。
//   - ログイン直後にセッションへ値を入れる処理は handleActiveUser の afterLogin で行う（セッションはリダイレクトの
//     送出時に保存されるため、リダイレクトの後に Set しても保存されない）。
//   - c.Params() は呼び出しごとに作り直されるため、before_action で params[:x] ||= ... のように値を足すときは
//     httpx.BodyParams(c.R).Set を使う。
//
// # 規約: 互換テスト
//
//   - 画面のテストは internal/server のテストで、internal/testfixtures で Redmine の公式フィクスチャを投入した DB に
//     対して行う（newFixtureServer。時刻は参照環境と同じ 2026-01-15 12:00 UTC に固定し、TZ も UTC にする）。
//   - 期待値は参照 Redmine（http://127.0.0.1:3998、tools/compat/README.md）から
//     `go run ./tools/compat fetch -base http://127.0.0.1:3998 -raw -user admin /path` で取得し、CSRF・フォーム名・
//     アセットのダイジェスト・ベース URL・atom キーを伏せて testdata/*.html に保存する（normalizeFixture と同じ置換）。
//     全体一致が難しい画面は該当部分（メニュー・表など）を抜き出して比較してよい。
//   - 参照 Redmine は GET でも DB を変える（最近使ったプロジェクト・atom キー生成等）。期待値の取得順に注意し、
//     必要なら `tools/compat/redmine-ref.sh reset` で初期化する（他のエージェントと共用なので多用しない）。
//   - フィルタ・権限の振る舞いは server.Options.ExtraRoutes でテスト用ルートを足して確認できる（filters_test.go）。
//   - 意図的に参照と異なる点（プロジェクトの兄弟順など docs/schema.md に記載のもの）はテストのコメントに理由を書く。
package handler
