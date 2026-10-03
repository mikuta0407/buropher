// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

package server

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/mikuta0407/buropher/internal/repository"
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
				func() {
					// 任意のリポジトリの出力を解析するので、panic してもサーバ全体を落とさず次の回に再試行する
					defer func() {
						if r := recover(); r != nil {
							slog.Error("scm fetch: panic", "err", r, "stack", string(debug.Stack()))
						}
					}()
					// 通知先（メール・Discord の有効・無効）は管理画面で変わるので毎回作る
					if err := app.SCMService().FetchAll(ctx, anon); err != nil {
						slog.Error("scm fetch", "err", err)
					}
				}()
			}
		}
	})
}
