package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// DataDir は実行時データの置き場（SQLite なら DB ファイルのディレクトリ、それ以外は data）。
func DataDir(c *Config) string {
	if c.Database.Driver == "sqlite" && c.Database.DSN != ":memory:" && !strings.HasPrefix(c.Database.DSN, "file:") {
		return filepath.Dir(c.Database.DSN)
	}
	return "data"
}

// SecretKey は server.secret_key を返す。未設定ならデータディレクトリの secret_key を読み、
// なければ生成して保存する（serve と redmine import が同じ鍵を使うようにするため共通化している）。
func SecretKey(c *Config) ([]byte, error) {
	if c.Server.SecretKey != "" {
		return []byte(c.Server.SecretKey), nil
	}
	path := filepath.Join(DataDir(c), "secret_key")
	if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		return []byte(strings.TrimSpace(string(b))), nil
	}
	var buf [64]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return nil, err
	}
	key := hex.EncodeToString(buf[:])
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(key+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	slog.Info("generated session secret", "path", path)
	return []byte(key), nil
}
