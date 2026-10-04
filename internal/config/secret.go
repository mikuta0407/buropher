// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
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

// minSecretKeyLen はこれより短い server.secret_key に警告を出す長さ（バイト）。
const minSecretKeyLen = 32

// SecretKey は server.secret_key を返す。未設定ならデータディレクトリの secret_key を読み、
// なければ生成して保存する（serve と redmine import が同じ鍵を使うようにするため共通化している）。
func SecretKey(c *Config) ([]byte, error) {
	if c.Server.SecretKey != "" {
		if len(c.Server.SecretKey) < minSecretKeyLen {
			// 短い鍵はクッキー（署名・暗号化された未ログインセッション）からオフラインで総当たりされ、
			// セッションデータ（オンザフライ登録の情報等）を偽造されるおそれがある
			slog.Warn("server.secret_key is too short; use at least 32 random characters (e.g. `openssl rand -hex 64`)", "length", len(c.Server.SecretKey))
		}
		return []byte(c.Server.SecretKey), nil
	}
	path := filepath.Join(DataDir(c), "secret_key")
	if key := readSecretKeyFile(path); key != nil {
		return key, nil
	}
	var buf [64]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return nil, err
	}
	key := hex.EncodeToString(buf[:])
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	// 一時ファイルに書き切ってから link で置く（既にあれば失敗する）。serve と redmine import などが
	// 同時に初回起動しても、後から来た側は先に置かれた鍵を読んで使い、別々の鍵で動くことがない。
	tmp, err := os.CreateTemp(dir, ".secret_key-*")
	if err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.WriteString(key + "\n")
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return nil, fmt.Errorf("write %s: %w", path, werr)
	}
	if err := os.Link(tmp.Name(), path); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("write %s: %w", path, err)
		}
		if k := readSecretKeyFile(path); k != nil {
			return k, nil
		}
		// 空の secret_key が置かれている: 生成した鍵で置き換える
		if err := os.Rename(tmp.Name(), path); err != nil {
			return nil, fmt.Errorf("write %s: %w", path, err)
		}
	}
	slog.Info("generated session secret", "path", path)
	return []byte(key), nil
}

// readSecretKeyFile は secret_key ファイルの鍵（無い・空なら nil）。グループ・他者が読めるモードなら警告する。
func readSecretKeyFile(path string) []byte {
	b, err := os.ReadFile(path)
	if err != nil || len(strings.TrimSpace(string(b))) == 0 {
		return nil
	}
	if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 {
		slog.Warn("secret key file is accessible by other users; run chmod 600", "path", path, "mode", fmt.Sprintf("%o", fi.Mode().Perm()))
	}
	return []byte(strings.TrimSpace(string(b)))
}
