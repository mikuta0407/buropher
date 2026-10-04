// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package server は HTTP サーバとルーティング（Redmine の routes.rb 相当）を定義する。
//
// ミドルウェアの順序（httpx の推奨に従う）:
//
//	Recover → RequestID → RemoteIP → 既定ヘッダ →（/healthz, /assets はここまで）
//	→ Params（一時ファイルは data/tmp）→ MethodOverride → Session → ルーティング
//	→ CSRF 検証（ルート単位, handler.App.Handle）→ ApplicationController の before_action → アクション
package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"text/template"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/assets"
	"github.com/mikuta0407/buropher/internal/attachments"
	"github.com/mikuta0407/buropher/internal/config"
	"github.com/mikuta0407/buropher/internal/crypto/secretbox"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/handler"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/issues"
	"github.com/mikuta0407/buropher/internal/jobs"
	"github.com/mikuta0407/buropher/internal/mail"
	"github.com/mikuta0407/buropher/internal/notify"
	"github.com/mikuta0407/buropher/internal/pdf"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/urlroot"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/web"
)

// Server は HTTP サーバ。
type Server struct {
	cfg      *config.Config
	router   chi.Router
	handler  http.Handler
	assets   *assets.Pipeline
	app      *handler.App
	sessions *httpx.SessionManager
	queue    *jobs.Queue
	notify   *notify.Service
	// bg は Run が起動したバックグラウンド処理（ジョブワーカー・定期実行・SCM 取り込み・DB の統計更新）。
	bg sync.WaitGroup
	// extraBG は RunInBackground で登録した処理（Run が bg として起動する）。
	extraBG []func(ctx context.Context)
}

// RunInBackground は Run の間だけ動かす処理を登録する（Run より前に呼ぶ）。fn にはサーバの停止時に
// HTTP の処理が終わってからキャンセルされる ctx が渡され、Run は fn の終了を待ってから戻る（メールの定期受信など）。
func (s *Server) RunInBackground(fn func(ctx context.Context)) {
	s.extraBG = append(s.extraBG, fn)
}

// goBG は fn をバックグラウンドで実行し、Run の終了時に完了を待つ対象にする。
func (s *Server) goBG(fn func()) {
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		fn()
	}()
}

// Options は New の追加設定（主にテスト用）。
type Options struct {
	// Now は現在時刻（nil なら time.Now）。
	Now func() time.Time
	// FormNameSuffix は form の name 属性の乱数部（nil なら乱数）。
	FormNameSuffix func() string
	// Logger はログ出力先（nil なら slog.Default()）。
	Logger *slog.Logger
	// TempDir はアップロードの一時ファイル置き場（空なら data/tmp）。
	TempDir string
	// ExtraRoutes はテスト用の追加ルート（App.Routes の後に同じミドルウェアの下で登録する）。
	ExtraRoutes func(a *handler.App, r chi.Router)
	// Notifier はチケット通知の配送先（nil ならログ出力のみ）。
	Notifier issues.Notifier
	// Version は buropher のバージョン（admin/info に表示する）。
	Version string
	// MailSender はメールの配送先の上書き（テスト用。nil なら config の mail から作る）。
	MailSender mail.Sender
}

// ErrNotInitialized は DB が未初期化（buropher init 未実行）。
var ErrNotInitialized = errors.New("database is not initialized: run `buropher init` first (it applies migrations and creates the administrator)")

// CheckInitialized は DB のマイグレーションと初期データの有無を確認する。
func CheckInitialized(ctx context.Context, d *db.DB) error {
	st, err := db.Status(ctx, d)
	if err != nil {
		return fmt.Errorf("%w (%w)", ErrNotInitialized, err)
	}
	for _, s := range st {
		if !s.Applied {
			return fmt.Errorf("%w (pending migration: %s; `buropher migrate` applies it)", ErrNotInitialized, filepath.Base(s.Path))
		}
	}
	var n int
	if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM principals WHERE kind = 'user'`); err != nil {
		return fmt.Errorf("%w (%w)", ErrNotInitialized, err)
	}
	if n == 0 {
		return ErrNotInitialized
	}
	return nil
}

// New はサーバを組み立てる（設定の読み込み・アセット・テンプレート・ルーティング）。
func New(cfg *config.Config, d *db.DB, opts ...Options) (*Server, error) {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	ctx := context.Background()
	// サブパス配置（relative_url_root）はプロセス全体の設定（Rails と同じ）。パス生成はすべて urlroot.Path を通る。
	urlroot.Set(cfg.Server.RelativeURLRoot)
	st, err := settings.New(ctx, repository.SettingsStore{DB: d})
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	ap, err := newAssets(cfg)
	if err != nil {
		return nil, err
	}
	helpers := &helper.Deps{Assets: ap}
	views, err := newViews(cfg, helpers)
	if err != nil {
		return nil, err
	}
	secret, err := secretKey(cfg)
	if err != nil {
		return nil, err
	}
	tempDir := o.TempDir
	if tempDir == "" {
		tempDir = filepath.Join(dataDir(cfg), "tmp")
	}
	if err := os.MkdirAll(tempDir, 0o700); err != nil {
		return nil, err
	}

	errs := &httpx.ErrorRenderer{}
	app := &handler.App{
		DB: d, Settings: st, Bundle: i18n.Default(), Assets: ap, Views: views, Helpers: helpers,
		Errors: errs, Logger: o.Logger, Now: o.Now, FormNameSuffix: o.FormNameSuffix, Version: o.Version, Notifier: o.Notifier,
	}
	app.SudoMode = cfg.Auth.SudoMode
	app.SudoModeTimeout = time.Duration(cfg.Auth.SudoModeTimeout) * time.Minute
	app.BaseURL = cfg.Server.BaseURL
	app.PDFFonts = pdf.NewFontSet(pdf.Config{Dir: cfg.PDF.FontDir, Fonts: cfg.PDF.Fonts, Logger: o.Logger})
	app.GitCommand = cfg.SCM.GitCommand
	app.GitPathRegexp = cfg.SCM.GitPathRegexp
	app.AuthRealm = cfg.Server.AuthRealm
	app.MailOmitRedmineHeaders = !cfg.Mail.SendRedmineHeaders()
	app.MessageIDPrefix = cfg.Mail.MessageIDPrefix
	if box, err := secretbox.New(string(secret)); err == nil {
		app.Secrets = box
	}
	app.AttachmentStore = &attachments.Store{Root: cfg.Storage.AttachmentsPath, Settings: st, Now: o.Now, Logger: o.Logger}
	errs.Page = app.ErrorPage()
	if b, err := fs.ReadFile(web.Public(), "500.html"); err == nil {
		handler.InternalErrorPage = b
	}
	if root := urlroot.Get(); root != "" {
		// Redmine の autologin_cookie_path の既定（relative_url_root || '/'）
		app.AutologinCookiePath = root
	}
	sessions := &httpx.SessionManager{
		// session_store の :path => config.relative_url_root || '/'
		CookiePath: urlroot.Get(),
		Store:      repository.SessionStore{DB: d},
		Secret:     secret,
		Policy:     app.SessionPolicy,
		Now:        o.Now,
		Logger:     o.Logger,
	}
	s := &Server{cfg: cfg, assets: ap, app: app, sessions: sessions}
	if s.queue, s.notify, err = setupNotify(cfg, d, app, o); err != nil {
		return nil, err
	}

	r := chi.NewRouter()
	// Params と MethodOverride はルーティングより前に適用する（chi は Group の middleware より先に
	// メソッドとパスでルートを決めるため、Group 内で _method を反映してもルートが変わらない）。
	r.Use(recoverer(o.Logger), httpx.RequestIDMiddleware, httpx.RemoteIPMiddleware(nil), defaultHeaders,
		httpx.ParamsMiddleware(&httpx.ParseOptions{TempDir: tempDir, MaxUploadBytes: cfg.Server.MaxRequestBodyMB << 20}, nil),
		httpx.MethodOverride)
	r.NotFound(notFound)
	r.MethodNotAllowed(notFound)
	r.Get("/healthz", healthz(d, o.Logger))
	r.Handle(assets.DefaultPrefix+"/*", ap.Handler())
	r.Group(func(r chi.Router) {
		r.Use(sessions.Middleware)
		app.Routes(r)
		if o.ExtraRoutes != nil {
			o.ExtraRoutes(app, r)
		}
	})
	s.router = r
	s.handler = mountRelativeURLRoot(r)
	return s, nil
}

// Handler は http.Handler を返す。
func (s *Server) Handler() http.Handler { return s.handler }

// mountRelativeURLRoot はサブパス配置（relative_url_root）のとき、ルートで始まるリクエストだけを
// ルートを除いたパスでルーティングする（Redmine の config.ru の map relative_url_root 相当）。
// r.URL.Path はルートを含んだまま（Rails の request.path = script_name + path_info と同じ）にし、
// chi のルーティングに使うパス（RoutePath）だけを差し替える。ルート外のパスは Rack::URLMap と同じ
// 404（text/plain "Not Found: <path>"）を返す。
func mountRelativeURLRoot(next chi.Router) http.Handler {
	if urlroot.Get() == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if r.URL.RawPath != "" {
			path = r.URL.RawPath
		}
		rest, ok := urlroot.Strip(path)
		if !ok {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("X-Cascade", "pass")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("Not Found: " + r.URL.Path))
			return
		}
		rctx := chi.NewRouteContext()
		rctx.Routes = next
		rctx.RoutePath = rest
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx)))
	})
}

// Assets はアセットパイプライン（テンプレートのヘルパー用）を返す。
func (s *Server) Assets() *assets.Pipeline { return s.assets }

// App はコントローラ共通の状態を返す（テスト用）。
func (s *Server) App() *handler.App { return s.app }

// newAssets は embed（開発時は DevWebDir/assets）からアセットパイプラインを作る。
// 外部テーマ（config の web.themes_dir）は ExtraThemes として読み込み、同梱テーマと同じくダイジェスト付き URL で配信する。
func newAssets(cfg *config.Config) (*assets.Pipeline, error) {
	opts := assets.Options{RelativeURLRoot: cfg.Server.RelativeURLRoot}
	if cfg.Web.ThemesDir != "" {
		if fi, err := os.Stat(cfg.Web.ThemesDir); err != nil || !fi.IsDir() {
			return nil, fmt.Errorf("config: web.themes_dir %q is not a directory", cfg.Web.ThemesDir)
		}
		// 起動時に読み込む（Redmine の本番環境と同じく、テーマの追加・変更は再起動で反映する）
		opts.ExtraThemes = os.DirFS(cfg.Web.ThemesDir)
	}
	if cfg.DevWebDir != "" {
		opts.Dev = true
		return assets.New(os.DirFS(filepath.Join(cfg.DevWebDir, "assets")), opts)
	}
	return assets.New(web.Assets(), opts)
}

// newViews はテンプレートエンジンを作る（開発時は DevWebDir/templates を都度再読み込み）。
func newViews(cfg *config.Config, helpers *helper.Deps) (*view.Engine, error) {
	opts := view.Options{
		FS:           web.Templates(),
		Funcs:        helpers.Funcs(),
		RequestFuncs: []func(r *view.Render) template.FuncMap{helpers.RequestFuncs, helpers.AdminRequestFuncs},
		FlashIcon:    helpers.FlashIcon,
	}
	if cfg.DevWebDir != "" {
		opts.FS = os.DirFS(filepath.Join(cfg.DevWebDir, "templates"))
		opts.Reload = true
	}
	return view.New(opts)
}

// dataDir は実行時データの置き場（config.DataDir）。
func dataDir(cfg *config.Config) string { return config.DataDir(cfg) }

// secretKey は config.SecretKey。
func secretKey(cfg *config.Config) ([]byte, error) { return config.SecretKey(cfg) }

// defaultHeaders は Redmine（Rails）の既定のレスポンスヘッダ。
func defaultHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("X-XSS-Protection", "1; mode=block")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Download-Options", "noopen")
		h.Set("X-Permitted-Cross-Domain-Policies", "none")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		if h.Get("Cache-Control") == "" {
			h.Set("Cache-Control", "max-age=0, private, must-revalidate")
		}
		next.ServeHTTP(w, r)
	})
}

// notFound はルートが無い場合の public/404.html。
// ActionDispatch::PublicExceptions と同じく、request.formats の先頭が json / xml なら
// {status: 404, error: "Not Found"} を to_json / to_xml した本文を返す（Accept: application/json 等）。
func notFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Del("Cache-Control")
	// ルートに一致していないので params[:format] は無い（chi がメソッド違いで残したパスパラメータは使わない）
	// （ルーティング済みでパスパラメータが空の文脈にして、拡張子を params[:format] とみなさないようにする）
	rctx := chi.NewRouteContext()
	rctx.RoutePatterns = []string{"/*"}
	nr := r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	b, err := fs.ReadFile(web.Public(), "404.html")
	if err != nil {
		b = []byte("Not Found")
	}
	httpx.WritePublicException(w, nr, http.StatusNotFound, b)
}

// recoverer は panic を記録して public/500.html を返す。
func recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					if rec == http.ErrAbortHandler { //nolint:errorlint // recover() の値（error とは限らない）との比較
						panic(rec)
					}
					logger.Error("panic", "err", rec, "path", r.URL.Path, "stack", string(debug.Stack()))
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write(handler.InternalErrorPage)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// healthz は DB への疎通を確認する。
// 認証なしで到達できるため、DB のエラー文（ホスト名・ファイルパス・ドライバの詳細）は応答に含めずログにだけ残す。
func healthz(d *db.DB, logger *slog.Logger) http.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if err := d.Ping(ctx); err != nil {
			logger.Error("healthz: database ping failed", "err", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("db: unavailable"))
			return
		}
		_, _ = w.Write([]byte("ok"))
	}
}

// Run は ctx がキャンセルされるまでサーバを動かす。
//
// 停止の順序: HTTP サーバを Shutdown して処理中のリクエストを終わらせてから（リクエストが積んだジョブや
// トランザクションを途中で切らない）、バックグラウンド処理を止め、実行中のジョブが結果を記録し終えるのを待つ
// （呼び出し側は Run の後に DB を閉じるため）。
func (s *Server) Run(ctx context.Context) error {
	if err := s.startPprof(ctx); err != nil {
		return err
	}
	bgCtx, bgCancel := context.WithCancel(context.WithoutCancel(ctx))
	defer func() {
		bgCancel()
		done := make(chan struct{})
		go func() { s.bg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			slog.Warn("shutdown: background tasks did not stop in time")
		}
	}()
	s.startDBOptimizer(bgCtx)
	s.runWorkers(bgCtx)
	// IdleTimeout: keep-alive の接続を放置し続けない（ReadTimeout は大きな添付のアップロードを切るので設けない）
	srv := &http.Server{Addr: s.cfg.Server.Addr, Handler: s.handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	errc := make(chan error, 1)
	s.startSCMFetcher(bgCtx)
	for _, fn := range s.extraBG {
		s.goBG(func() { fn(bgCtx) })
	}
	go func() {
		slog.Info("listening", "addr", s.cfg.Server.Addr)
		errc <- srv.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(sctx)
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
