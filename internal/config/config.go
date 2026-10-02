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
	Auth     Auth     `toml:"auth"`
	// MailReceive はサーバー内でのメールの定期受信（IMAP / POP3）。
	MailReceive MailReceive `toml:"mail_receive"`
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

// Auth は認証まわりの設定（Redmine の configuration.yml の sudo_mode / sudo_mode_timeout 相当）。
type Auth struct {
	// SudoMode は sudo モード（管理操作の前にパスワードを再入力させる）を有効にする。既定 false（Redmine と同じ）。
	SudoMode bool `toml:"sudo_mode"`
	// SudoModeTimeout は sudo モードの有効時間（分）。0 なら 15。
	SudoModeTimeout int `toml:"sudo_mode_timeout"`
}

// MailReceive はサーバー内でのメールの定期受信（Redmine の rake redmine:email:receive_imap / receive_pop3 を
// cron で動かす代わり）。各項目の意味は rake の環境変数と同じ。
type MailReceive struct {
	// Protocol は "imap" または "pop3"（空なら定期受信しない）。
	Protocol string `toml:"protocol"`
	// Interval は受信の間隔（秒）。0 なら 300。
	Interval int    `toml:"interval"`
	Host     string `toml:"host"`
	Port     string `toml:"port"`
	// SSL は SSL/TLS を使うか（"" 以外なら使う。"force" ならサーバー証明書を検証しない）。
	SSL      string `toml:"ssl"`
	StartTLS bool   `toml:"starttls"`
	Username string `toml:"username"`
	Password string `toml:"password"`
	// Folder / MoveOnSuccess / MoveOnFailure は IMAP のみ。
	Folder        string `toml:"folder"`
	MoveOnSuccess string `toml:"move_on_success"`
	MoveOnFailure string `toml:"move_on_failure"`
	// APOP / DeleteUnprocessed は POP3 のみ。
	APOP              bool `toml:"apop"`
	DeleteUnprocessed bool `toml:"delete_unprocessed"`
	// Options は MailHandler のオプション（unknown_user / project / tracker / allow_override / private ... を
	// rake の環境変数と同じ名前で指定する）。
	Options map[string]string `toml:"options"`
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
		"BUROPHER_ADDR":             &c.Server.Addr,
		"BUROPHER_BASE_URL":         &c.Server.BaseURL,
		"BUROPHER_SECRET_KEY":       &c.Server.SecretKey,
		"BUROPHER_DB_DRIVER":        &c.Database.Driver,
		"BUROPHER_DB_DSN":           &c.Database.DSN,
		"BUROPHER_ATTACHMENTS_PATH": &c.Storage.AttachmentsPath,
		"BUROPHER_DEV_WEB_DIR":      &c.DevWebDir,
	}
	for k, p := range env {
		if v, ok := os.LookupEnv(k); ok {
			*p = v
		}
	}
	if v, ok := os.LookupEnv("BUROPHER_SUDO_MODE"); ok {
		c.Auth.SudoMode = v == "1" || strings.EqualFold(v, "true")
	}
	c.Server.BaseURL = strings.TrimRight(c.Server.BaseURL, "/")
	return c, nil
}
