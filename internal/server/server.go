// Package server は HTTP サーバとルーティング（Redmine の routes.rb 相当）を定義する。
package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/mikuta0407/buropher/internal/config"
)

type Server struct {
	cfg    *config.Config
	router chi.Router
}

func New(cfg *config.Config) (*Server, error) {
	s := &Server{cfg: cfg}
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	s.router = r
	return s, nil
}

func (s *Server) Handler() http.Handler { return s.router }

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
