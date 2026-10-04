// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package clock はアプリ全体の現在時刻を提供する。
//
// 通常は time.Now() を返すが、互換テスト用に環境変数 BUROPHER_FAKE_NOW（RFC3339）を設定すると
// その時刻で固定される（参照 Redmine の travel_to と同じく時刻が進まない）。
package clock

import (
	"log/slog"
	"os"
	"sync"
	"time"
)

var (
	mu    sync.RWMutex
	once  sync.Once
	fixed time.Time
)

func load() {
	if v := os.Getenv("BUROPHER_FAKE_NOW"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			slog.Warn("BUROPHER_FAKE_NOW is set but is not RFC3339; ignored", "value", v)
			return
		}
		fixed = t
		// 時刻が進まないとセッション・トークン・sudo モードの有効期限が切れなくなる。
		// 互換テスト専用の設定が本番で誤って有効になっていることに気付けるよう、目立つ警告を出す。
		slog.Warn("BUROPHER_FAKE_NOW is set: the clock is frozen (sessions and tokens never expire). "+
			"This is for compatibility testing only; unset it in production", "now", t)
	}
}

// Now は現在時刻を返す。
func Now() time.Time {
	once.Do(load)
	mu.RLock()
	defer mu.RUnlock()
	if fixed.IsZero() {
		return time.Now()
	}
	return fixed
}

// Set はテスト用に固定時刻を設定する（t のゼロ値で解除）。
func Set(t time.Time) {
	once.Do(load)
	mu.Lock()
	fixed = t
	mu.Unlock()
}
