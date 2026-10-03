package server_test

import (
	"os"
	"testing"
	"time"
)

// TestMain は参照環境と同じく TZ=UTC にする（タイムゾーン未設定ユーザーの時刻はサーバのローカル時刻で表示される）。
// テストごとに time.Local を差し替えて戻すと、前のテストの HTTP サーバのゴルーチン（keep-alive の接続の後始末）が
// time.Now で読むのと競合する（go test -race）ため、プロセスの開始時に一度だけ設定する。
func TestMain(m *testing.M) {
	time.Local = time.UTC
	os.Exit(m.Run())
}
