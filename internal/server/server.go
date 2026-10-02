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
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"text/template"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/assets"
	"github.com/mikuta0407/buropher/internal/config"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/handler"
	"github.com/mikuta0407/buropher/internal/helper"
	"github.com/mikuta0407/buropher/internal/httpx"
	"github.com/mikuta0407/buropher/internal/i18n"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/view"
	"github.com/mikuta0407/buropher/web"
)

// Server は HTTP サーバ。
type Server struct {
	cfg      *config.Config
	router   chi.Router
	assets   *assets.Pipeline
	app      *handler.App
	sessions *httpx.SessionManager
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
}

// ErrNotInitialized は DB が未初期化（buropher init 未実行）。
var ErrNotInitialized = errors.New("database is not initialized: run `buropher init` first (it applies migrations and creates the administrator)")

// CheckInitialized は DB のマイグレーションと初期データの有無を確認する。
func CheckInitialized(ctx context.Context, d *db.DB) error {
	st, err := db.Status(ctx, d)
	if err != nil {
		return fmt.Errorf("%w (%v)", ErrNotInitialized, err)
	}
	for _, s := range st {
		if !s.Applied {
			return fmt.Errorf("%w (pending migration: %s; `buropher migrate` applies it)", ErrNotInitialized, filepath.Base(s.Path))
		}
	}
	var n int
	if err := d.Get(ctx, &n, `SELECT COUNT(*) FROM principals WHERE kind = 'user'`); err != nil {
		return fmt.Errorf("%w (%v)", ErrNotInitialized, err)
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
		Errors: errs, Logger: o.Logger, Now: o.Now, FormNameSuffix: o.FormNameSuffix,
	}
	errs.Page = app.ErrorPage()
	if b, err := fs.ReadFile(web.Public(), "500.html"); err == nil {
		handler.InternalErrorPage = b
	}
	sessions := &httpx.SessionManager{
		Store:  repository.SessionStore{DB: d},
		Secret: secret,
		Policy: app.SessionPolicy,
		Now:    o.Now,
		Logger: o.Logger,
	}
	s := &Server{cfg: cfg, assets: ap, app: app, sessions: sessions}

	r := chi.NewRouter()
	r.Use(recoverer(o.Logger), httpx.RequestIDMiddleware, httpx.RemoteIPMiddleware(nil), defaultHeaders)
	r.NotFound(notFound)
	r.MethodNotAllowed(notFound)
	r.Get("/healthz", healthz(d))
	r.Handle(assets.DefaultPrefix+"/*", ap.Handler())
	r.Group(func(r chi.Router) {
		r.Use(
			httpx.ParamsMiddleware(&httpx.ParseOptions{TempDir: tempDir}, nil),
			httpx.MethodOverride,
			sessions.Middleware,
		)
		app.Routes(r)
		if o.ExtraRoutes != nil {
			o.ExtraRoutes(app, r)
		}
	})
	s.router = r
	return s, nil
}

// Handler は http.Handler を返す。
func (s *Server) Handler() http.Handler { return s.router }

// Assets はアセットパイプライン（テンプレートのヘルパー用）を返す。
func (s *Server) Assets() *assets.Pipeline { return s.assets }

// App はコントローラ共通の状態を返す（テスト用）。
func (s *Server) App() *handler.App { return s.app }

// newAssets は embed（開発時は DevWebDir/assets）からアセットパイプラインを作る。
func newAssets(cfg *config.Config) (*assets.Pipeline, error) {
	if cfg.DevWebDir != "" {
		return assets.New(os.DirFS(filepath.Join(cfg.DevWebDir, "assets")), assets.Options{Dev: true})
	}
	return assets.New(web.Assets(), assets.Options{})
}

// newViews はテンプレートエンジンを作る（開発時は DevWebDir/templates を都度再読み込み）。
func newViews(cfg *config.Config, helpers *helper.Deps) (*view.Engine, error) {
	opts := view.Options{
		FS:           web.Templates(),
		Funcs:        helpers.Funcs(),
		RequestFuncs: []func(r *view.Render) template.FuncMap{helpers.RequestFuncs},
		FlashIcon:    helpers.FlashIcon,
	}
	if cfg.DevWebDir != "" {
		opts.FS = os.DirFS(filepath.Join(cfg.DevWebDir, "templates"))
		opts.Reload = true
	}
	return view.New(opts)
}

// dataDir は実行時データの置き場（SQLite なら DB ファイルのディレクトリ、それ以外は data）。
func dataDir(cfg *config.Config) string {
	if cfg.Database.Driver == "sqlite" && cfg.Database.DSN != ":memory:" && !strings.HasPrefix(cfg.Database.DSN, "file:") {
		return filepath.Dir(cfg.Database.DSN)
	}
	return "data"
}

// secretKey は config の secret_key を返す。未設定なら data/secret_key を読み、なければ生成して保存する。
func secretKey(cfg *config.Config) ([]byte, error) {
	if cfg.Server.SecretKey != "" {
		return []byte(cfg.Server.SecretKey), nil
	}
	path := filepath.Join(dataDir(cfg), "secret_key")
	if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		return []byte(strings.TrimSpace(string(b))), nil
	}
	var buf [64]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return nil, err
	}
	key := hex.EncodeToString(buf[:])
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(key+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	slog.Info("generated session secret", "path", path)
	return []byte(key), nil
}

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
func notFound(w http.ResponseWriter, r *http.Request) {
	b, err := fs.ReadFile(web.Public(), "404.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Del("Cache-Control")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write(b)
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
					if rec == http.ErrAbortHandler {
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
func healthz(d *db.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if err := d.Ping(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("db: " + err.Error()))
			return
		}
		_, _ = w.Write([]byte("ok"))
	}
}

// Run は ctx がキャンセルされるまでサーバを動かす。
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{Addr: s.cfg.Server.Addr, Handler: s.router, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
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
