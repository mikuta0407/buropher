// Package config は buropher の設定（TOML + 環境変数）を読み込む。
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Server   Server   `toml:"server"`
	Database Database `toml:"database"`
	Storage  Storage  `toml:"storage"`
	SCM      SCM      `toml:"scm"`
	// DevAssetsDir が空でなければ embed ではなくディスクからアセット/テンプレートを読む（開発用）。
	DevWebDir string `toml:"dev_web_dir"`
}

type Server struct {
	Addr    string `toml:"addr"`
	BaseURL string `toml:"base_url"`
	// SecretKey はセッション・CSRF・暗号化に使う。
	SecretKey string `toml:"secret_key"`
}

type Database struct {
	// Driver は "sqlite" または "postgres"。
	Driver string `toml:"driver"`
	DSN    string `toml:"dsn"`
}

type Storage struct {
	// AttachmentsPath は添付ファイルの保存先（Redmine の attachments_storage_path 相当）。
	AttachmentsPath string `toml:"attachments_path"`
}

// SCM はリポジトリ（Git）の設定。
type SCM struct {
	// GitCommand は git の実行ファイル（Redmine の scm_git_command。空なら "git"）。
	GitCommand string `toml:"git_command"`
	// FetchInterval はチェンジセットを定期的に取り込む間隔（"15m" など。空なら定期取り込みしない。
	// Redmine の cron による Repository.fetch_changesets 相当）。
	FetchInterval string `toml:"fetch_interval"`
}

func Default() *Config {
	return &Config{
		Server:   Server{Addr: ":3000"},
		Database: Database{Driver: "sqlite", DSN: "data/buropher.db"},
		Storage:  Storage{AttachmentsPath: "data/files"},
	}
}

// Load は path（空なら省略）の TOML を読み、BUROPHER_* 環境変数で上書きする。
func Load(path string) (*Config, error) {
	c := Default()
	if path != "" {
		if _, err := toml.DecodeFile(path, c); err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
	}
	env := map[string]*string{
		"BUROPHER_ADDR":               &c.Server.Addr,
		"BUROPHER_BASE_URL":           &c.Server.BaseURL,
		"BUROPHER_SECRET_KEY":         &c.Server.SecretKey,
		"BUROPHER_DB_DRIVER":          &c.Database.Driver,
		"BUROPHER_DB_DSN":             &c.Database.DSN,
		"BUROPHER_ATTACHMENTS_PATH":   &c.Storage.AttachmentsPath,
		"BUROPHER_DEV_WEB_DIR":        &c.DevWebDir,
		"BUROPHER_SCM_GIT_COMMAND":    &c.SCM.GitCommand,
		"BUROPHER_SCM_FETCH_INTERVAL": &c.SCM.FetchInterval,
	}
	for k, p := range env {
		if v, ok := os.LookupEnv(k); ok {
			*p = v
		}
	}
	c.Server.BaseURL = strings.TrimRight(c.Server.BaseURL, "/")
	return c, nil
}
