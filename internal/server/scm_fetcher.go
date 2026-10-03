// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/scmsync"
)

// startSCMFetcher は設定 scm.fetch_interval ごとに Repository.fetch_changesets を実行する
// （Redmine では cron 等で `rails runner "Repository.fetch_changesets"` を実行する運用。
// buropher は単一バイナリのためサーバ内のゴルーチンで行う）。
func (s *Server) startSCMFetcher(ctx context.Context) {
	if s.cfg.SCM.FetchInterval == "" {
		return
	}
	d, err := time.ParseDuration(s.cfg.SCM.FetchInterval)
	if err != nil || d <= 0 {
		slog.Error("invalid scm.fetch_interval", "value", s.cfg.SCM.FetchInterval, "err", err)
		return
	}
	app := s.app
	svc := &scmsync.Service{DB: app.DB, Settings: app.Settings, GitCommand: app.GitCommand, Bundle: app.Bundle,
		Now: app.Now, Notifier: app.Notifier, Logger: app.Logger}
	s.goBG(func() {
		t := time.NewTicker(d)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				anon, err := repository.AnonymousUser(ctx, app.DB)
				if err != nil {
					slog.Error("scm fetch: anonymous user", "err", err)
					continue
				}
				if err := svc.FetchAll(ctx, anon); err != nil {
					slog.Error("scm fetch", "err", err)
				}
			}
		}
	})
}
