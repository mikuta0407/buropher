// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package clock

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestFakeNowWarns は BUROPHER_FAKE_NOW（互換テスト専用）が設定されているとき、
// 時刻を固定したうえで警告をログに出すことを確認する（本番で誤って有効にしたことに気付けるように）。
func TestFakeNowWarns(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(old)
		once = sync.Once{}
		fixed = time.Time{}
	})
	t.Setenv("BUROPHER_FAKE_NOW", "2026-01-15T12:00:00Z")
	once = sync.Once{}
	fixed = time.Time{}
	if got := Now(); !got.Equal(time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("Now() = %v", got)
	}
	if !strings.Contains(buf.String(), "BUROPHER_FAKE_NOW") || !strings.Contains(buf.String(), "level=WARN") {
		t.Errorf("no warning logged: %q", buf.String())
	}
}
