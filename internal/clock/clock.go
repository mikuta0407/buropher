// Package clock はアプリ全体の現在時刻を提供する。
//
// 通常は time.Now() を返すが、互換テスト用に環境変数 BUROPHER_FAKE_NOW（RFC3339）を設定すると
// その時刻で固定される（参照 Redmine の travel_to と同じく時刻が進まない）。
package clock

import (
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
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			fixed = t
		}
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
