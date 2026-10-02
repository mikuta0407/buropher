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
	// Mail はメール配送（Redmine の configuration.yml の email_delivery 相当）。
	Mail Mail `toml:"mail"`
	// Jobs はジョブキューのワーカーと定期実行（期日リマインダ・期限切れデータの削除）。
	Jobs Jobs `toml:"jobs"`
	// Discord は Discord API の接続先（Bot トークン等は管理画面で設定する）。
	Discord Discord `toml:"discord"`
	Auth    Auth    `toml:"auth"`
	PDF     PDF     `toml:"pdf"`
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

// Mail はメール配送の設定。
type Mail struct {
	// DeliveryMethod は "smtp" / "sendmail" / "none"（空なら配送しない。Redmine で email_delivery が
	// 未設定のとき perform_deliveries = false になるのと同じ）。
	DeliveryMethod string   `toml:"delivery_method"`
	SMTP           SMTP     `toml:"smtp"`
	Sendmail       Sendmail `toml:"sendmail"`
}

// SMTP は smtp_settings。
type SMTP struct {
	Address string `toml:"address"`
	Port    int    `toml:"port"`
	// Domain は HELO で名乗るドメイン。
	Domain   string `toml:"domain"`
	UserName string `toml:"user_name"`
	Password string `toml:"password"`
	// Authentication は "plain" / "login" / "cram_md5"。
	Authentication string `toml:"authentication"`
	// EnableStartTLSAuto はサーバが対応していれば STARTTLS を使う（既定 true）。
	EnableStartTLSAuto *bool `toml:"enable_starttls_auto"`
	// TLS は SMTPS（接続時から TLS）。
	TLS bool `toml:"tls"`
	// OpenSSLVerifyMode は "peer"（既定）/ "none"。
	OpenSSLVerifyMode string `toml:"openssl_verify_mode"`
	// Timeout は秒数（0 なら 30）。
	Timeout int `toml:"timeout"`
}

// Sendmail は sendmail_settings。
type Sendmail struct {
	Location  string   `toml:"location"`
	Arguments []string `toml:"arguments"`
}

// Jobs はジョブキューの設定。
type Jobs struct {
	// Workers はワーカー数（0 なら 2）。負なら serve でワーカーを起動しない。
	Workers int `toml:"workers"`
	// PollInterval はポーリング間隔（Go の time.ParseDuration 形式。空なら 2s）。
	PollInterval string `toml:"poll_interval"`
	// MaxAttempts はジョブの最大試行回数（0 なら 10）。
	MaxAttempts int `toml:"max_attempts"`
	// CleanupInterval は期限切れセッション・トークン等を消す間隔（空なら 1h）。
	CleanupInterval string `toml:"cleanup_interval"`
	// JobsRetentionDays は完了したジョブを残す日数（0 なら 7）。
	JobsRetentionDays int `toml:"jobs_retention_days"`
	// DeliveriesRetentionDays は通知の送信ログ（notification_deliveries）を残す日数（0 なら 30）。
	DeliveriesRetentionDays int       `toml:"deliveries_retention_days"`
	Reminders               Reminders `toml:"reminders"`
}

// Reminders は期日リマインダ（rake redmine:send_reminders 相当）の定期実行。
type Reminders struct {
	Enabled bool `toml:"enabled"`
	// At は毎日の実行時刻 "HH:MM"（サーバのローカル時刻。空なら 08:00）。
	At string `toml:"at"`
	// Days は何日先までの期日を対象にするか（0 なら 7）。
	Days int `toml:"days"`
	// Tracker はトラッカー ID（0 なら全トラッカー）。
	Tracker int64 `toml:"tracker"`
	// Project はプロジェクトの ID または識別子（空なら全プロジェクト）。
	Project string `toml:"project"`
	// Users は対象の担当者（ユーザー / グループ）ID。
	Users []int64 `toml:"users"`
	// Version は対象バージョン名。
	Version string `toml:"version"`
}

// Discord は Discord API の接続先（テストでは偽サーバの URL を指定する）。
type Discord struct {
	// Enabled は Discord 通知の機能を使えるようにする（管理画面のプラグイン一覧に設定画面を出す）。
	Enabled bool `toml:"enabled"`
	// APIBase は REST API の基底 URL（空なら https://discord.com/api/v10）。
	APIBase string `toml:"api_base"`
	// AuthorizeURL は OAuth2 の認可画面の URL（空なら https://discord.com/oauth2/authorize）。
	AuthorizeURL string `toml:"authorize_url"`
}

// Auth は認証まわりの設定（Redmine の configuration.yml の sudo_mode / sudo_mode_timeout 相当）。
type Auth struct {
	// SudoMode は sudo モード（管理操作の前にパスワードを再入力させる）を有効にする。既定 false（Redmine と同じ）。
	SudoMode bool `toml:"sudo_mode"`
	// SudoModeTimeout は sudo モードの有効時間（分）。0 なら 15。
	SudoModeTimeout int `toml:"sudo_mode_timeout"`
}

// PDF は PDF 出力のフォント（docs/pdf.md）。
type PDF struct {
	// FontDir は CJK などの追加の TrueType フォントを置くディレクトリ（空なら埋め込みの DejaVu だけ）。
	FontDir string `toml:"font_dir"`
	// Fonts はロケールごとのフォントファイル（例: ja = "ipaexg.ttf"。"regular.ttf,bold.ttf" で太字も指定できる）。
	Fonts map[string]string `toml:"fonts"`
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
		"BUROPHER_MAIL_DELIVERY":    &c.Mail.DeliveryMethod,
		"BUROPHER_SMTP_ADDRESS":     &c.Mail.SMTP.Address,
		"BUROPHER_SMTP_DOMAIN":      &c.Mail.SMTP.Domain,
		"BUROPHER_SMTP_USER_NAME":   &c.Mail.SMTP.UserName,
		"BUROPHER_SMTP_PASSWORD":    &c.Mail.SMTP.Password,
		"BUROPHER_SMTP_AUTH":        &c.Mail.SMTP.Authentication,
		"BUROPHER_DISCORD_API_BASE": &c.Discord.APIBase,
		"BUROPHER_PDF_FONT_DIR":     &c.PDF.FontDir,
	}
	for k, p := range env {
		if v, ok := os.LookupEnv(k); ok {
			*p = v
		}
	}
	if v, ok := os.LookupEnv("BUROPHER_SMTP_PORT"); ok {
		if _, err := fmt.Sscanf(v, "%d", &c.Mail.SMTP.Port); err != nil {
			return nil, fmt.Errorf("config: BUROPHER_SMTP_PORT: %w", err)
		}
	}
	if v, ok := os.LookupEnv("BUROPHER_SUDO_MODE"); ok {
		c.Auth.SudoMode = v == "1" || strings.EqualFold(v, "true")
	}
	c.Server.BaseURL = strings.TrimRight(c.Server.BaseURL, "/")
	return c, nil
}
