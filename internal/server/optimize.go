// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server

import (
	"context"
	"log/slog"
	"time"
)

// dbOptimizeInterval は DB の統計の更新 (SQLite の PRAGMA optimize) を繰り返す間隔。
const dbOptimizeInterval = 6 * time.Hour

// startDBOptimizer は起動直後と以後 dbOptimizeInterval ごとに DB の統計を更新する。
// SQLite は統計が無いと大きなテーブルで不適切なプランを選ぶことがあるため (db.DB.Optimize)。
// 起動を遅らせないようバックグラウンドで実行する。
func (s *Server) startDBOptimizer(ctx context.Context) {
	d := s.app.DB
	go func() {
		t := time.NewTicker(dbOptimizeInterval)
		defer t.Stop()
		// 起動時は統計の無いテーブルだけ、以後は全テーブルを更新する
		all := false
		for {
			start := time.Now()
			if err := d.Optimize(ctx, all); err != nil {
				if ctx.Err() != nil {
					return
				}
				slog.Error("db optimize", "err", err)
			} else {
				slog.Debug("db optimize", "duration", time.Since(start))
			}
			all = true
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}
