// Package server は HTTP サーバとルーティング（Redmine の routes.rb 相当）を定義する。
package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/mikuta0407/buropher/internal/assets"
	"github.com/mikuta0407/buropher/internal/config"
	"github.com/mikuta0407/buropher/web"
)

type Server struct {
	cfg    *config.Config
	router chi.Router
	assets *assets.Pipeline
}

func New(cfg *config.Config) (*Server, error) {
	s := &Server{cfg: cfg}
	ap, err := newAssets(cfg)
	if err != nil {
		return nil, err
	}
	s.assets = ap
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	r.Handle(assets.DefaultPrefix+"/*", ap.Handler())
	s.router = r
	return s, nil
}

func (s *Server) Handler() http.Handler { return s.router }

// Assets はアセットパイプライン（テンプレートのヘルパー用）を返す。
func (s *Server) Assets() *assets.Pipeline { return s.assets }

// newAssets は embed（開発時は DevWebDir/assets）からアセットパイプラインを作る。
func newAssets(cfg *config.Config) (*assets.Pipeline, error) {
	if cfg.DevWebDir != "" {
		return assets.New(os.DirFS(filepath.Join(cfg.DevWebDir, "assets")), assets.Options{Dev: true})
	}
	return assets.New(web.Assets(), assets.Options{})
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
