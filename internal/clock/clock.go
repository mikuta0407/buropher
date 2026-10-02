// Package clock はアプリ全体の現在時刻を提供する。
//
// 通常は time.Now() を返すが、互換テスト用に環境変数 BUROPHER_FAKE_NOW（RFC3339）を設定すると
// その時刻を起点に進む時計になる（参照 Redmine の travel_to と同じ固定時刻に揃えるため）。
package clock

import (
	"os"
	"sync"
	"time"
)

var (
	once   sync.Once
	offset time.Duration
)

func load() {
	if v := os.Getenv("BUROPHER_FAKE_NOW"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			offset = time.Until(t)
		}
	}
}

// Now は現在時刻を返す。
func Now() time.Time {
	once.Do(load)
	if offset == 0 {
		return time.Now()
	}
	return time.Now().Add(offset)
}

// Set はテスト用に固定時刻を設定する（t のゼロ値で解除）。
func Set(t time.Time) {
	once.Do(load)
	if t.IsZero() {
		offset = 0
		return
	}
	offset = time.Until(t)
}
