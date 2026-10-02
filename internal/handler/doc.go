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
//     HTTP Basic、管理者の X-Redmine-Switch-User を find_current_user が処理する）。
//   - 形式の判定は httpx.Format(c.R) / httpx.IsAPIRequest(c.R) / httpx.Negotiate。API のエラーは
//     a.Errors.RenderAPIErrors（422）・c.RenderError（ステータスのみ）。
//   - JSON / XML の本文は Redmine::Views::Builders と同じキー順・型で出す必要がある（未実装。最初に必要になったときに
//     builder を移植し、この節を更新すること）。
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
