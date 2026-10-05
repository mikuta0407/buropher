// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Command buropher は Redmine 互換のプロジェクト管理システム Buropher の単一バイナリ。
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"syscall"

	"github.com/mikuta0407/buropher/internal/bootstrap"
	"github.com/mikuta0407/buropher/internal/brand"
	"github.com/mikuta0407/buropher/internal/config"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/redmineimport/export"
	"github.com/mikuta0407/buropher/internal/redmineimport/importer"
	"github.com/mikuta0407/buropher/internal/redmineimport/verify"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/server"
	"github.com/mikuta0407/buropher/internal/settings"
)

// ビルド時に -ldflags "-X main.version=... -X main.commit=... -X main.date=..." で埋め込む。
var (
	version = "dev"
	commit  = ""
	date    = ""
)

// printVersion はバージョン・コミット・ビルド日時・Go のバージョンを表示する。
// ldflags で埋め込まれていなければ、go build が記録した VCS 情報で補う。
func printVersion(w io.Writer) {
	c, d, modified := commit, date, false
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if c == "" {
					c = s.Value
				}
			case "vcs.time":
				if d == "" {
					d = s.Value
				}
			case "vcs.modified":
				modified = s.Value == "true" && commit == ""
			}
		}
	}
	if c == "" {
		c = "unknown"
	} else if modified {
		c += "-dirty"
	}
	if d == "" {
		d = "unknown"
	}
	fmt.Fprintf(w, "%s %s (Redmine %s compatible)\ncommit: %s\nbuilt: %s\ngo: %s %s/%s\n", brand.Name, version, brand.UpstreamVersion, c, d, runtime.Version(), runtime.GOOS, runtime.GOARCH)
	fmt.Fprintln(w, brand.LicenseLine())
}

func usage() {
	fmt.Fprintf(os.Stderr, `Buropher - Redmine-compatible project management (`+brand.License+`)

usage: buropher <command> [options]

commands:
  serve     start the web server
  migrate   apply database migrations (up|down|status)
  init      load default data and create the administrator
  redmine   migrate data from Redmine (export | import | verify)
  setting   get or set a setting (setting get <name> / setting set <name> <value>)
  reminders send due date reminders (rake redmine:send_reminders: -days -tracker -project -users -version)
  jobs      run pending background jobs once (jobs run)
  mail      receive emails (mail receive -stdin | -imap | -pop3 [options])
  version   print version, commit, build date, Go version and license
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "serve":
		err = serve(args)
	case "migrate":
		err = migrate(args)
	case "init":
		err = initCmd(args)
	case "redmine":
		err = redmineCmd(args)
	case "setting":
		err = settingCmd(args)
	case "reminders":
		err = remindersCmd(args)
	case "jobs":
		err = jobsCmd(args)
	case "mail":
		err = mailCmd(args)
	case "version":
		printVersion(os.Stdout)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		slog.Error(cmd+" failed", "err", err)
		os.Exit(1)
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	fs.Parse(args)
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	d, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := server.CheckInitialized(ctx, d); err != nil {
		return err
	}
	srv, err := server.New(cfg, d, server.Options{Version: version})
	if err != nil {
		return err
	}
	// [mail_receive] があればメールを定期受信する
	if pc, ok := mailPollConfig(cfg.MailReceive); ok {
		srv.RunInBackground(func(ctx context.Context) { srv.App().MailHandler().Poll(ctx, pc) })
	}
	return srv.Run(ctx)
}

// openDB は設定の DB を開く。SQLite の場合は親ディレクトリと DB ファイルを本人だけが読める権限で作成する。
func openDB(ctx context.Context, cfg *config.Config) (*db.DB, error) {
	if err := config.PrepareSQLite(cfg.Database.Driver, cfg.Database.DSN); err != nil {
		return nil, err
	}
	d, err := db.Open(ctx, cfg.Database.Driver, cfg.Database.DSN)
	if err != nil {
		return nil, err
	}
	d.SetMaxConns(cfg.Database.MaxOpenConns)
	return d, nil
}

func migrate(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	fs.Parse(args)
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	ctx := context.Background()
	d, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer d.Close()
	switch cmd := fs.Arg(0); cmd {
	case "", "up", "down":
		dir := db.Up
		if cmd == "down" {
			dir = db.Down
		}
		res, err := db.Migrate(ctx, d, dir)
		for _, r := range res {
			fmt.Println(r)
		}
		if err == nil && len(res) == 0 {
			fmt.Println("no migrations to apply")
		}
		return err
	case "status":
		st, err := db.Status(ctx, d)
		if err != nil {
			return err
		}
		for _, s := range st {
			state := "pending"
			if s.Applied {
				state = "applied " + s.AppliedAt.Format("2006-01-02 15:04:05")
			}
			fmt.Printf("%-6d %-40s %s\n", s.Version, filepath.Base(s.Path), state)
		}
		return nil
	default:
		return fmt.Errorf("unknown migrate command %q", cmd)
	}
}

func initCmd(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	lang := fs.String("lang", "en", "language of the default data")
	login := fs.String("admin-login", "admin", "administrator login")
	email := fs.String("admin-email", "admin@dummy.invalid", "administrator email")
	// 環境変数のパスワードをフラグの既定値にすると -h の使い方に表示されるため、解析後に補う
	pw := fs.String("admin-password", "", "administrator password (default: $BUROPHER_ADMIN_PASSWORD; random if empty)")
	noData := fs.Bool("no-default-data", false, "do not load default roles/trackers/statuses")
	fs.Parse(args)
	if *pw == "" {
		*pw = os.Getenv("BUROPHER_ADMIN_PASSWORD")
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	ctx := context.Background()
	d, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer d.Close()
	if _, err := db.Migrate(ctx, d, db.Up); err != nil {
		return err
	}
	generated := *pw == ""
	if generated {
		*pw = rand.Text()[:16]
	}
	if err := bootstrap.Init(ctx, d, bootstrap.Options{
		Language: *lang, AdminLogin: *login, AdminPassword: *pw, AdminEmail: *email, SkipDefaultData: *noData,
	}); err != nil {
		return err
	}
	fmt.Printf("initialized. administrator login: %s\n", *login)
	if generated {
		fmt.Printf("generated password: %s\n", *pw)
	}
	return nil
}

func redmineCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: buropher redmine export|import|verify [options]")
	}
	switch args[0] {
	case "export":
		export.ToolVersion = version
		return export.Main(args[1:])
	case "import":
		return importer.Main(args[1:])
	case "verify":
		return verify.Main(args[1:])
	default:
		return fmt.Errorf("unknown redmine command %q", args[0])
	}
}

// settingCmd は設定値の参照・変更（buropher setting get|set）。
func settingCmd(args []string) error {
	fs := flag.NewFlagSet("setting", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	fs.Parse(args)
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	ctx := context.Background()
	d, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := server.CheckInitialized(ctx, d); err != nil {
		return err
	}
	st, err := settings.New(ctx, repository.SettingsStore{DB: d})
	if err != nil {
		return err
	}
	switch fs.Arg(0) {
	case "get":
		if fs.NArg() != 2 {
			return fmt.Errorf("usage: buropher setting get <name>")
		}
		if settings.Lookup(fs.Arg(1)) == nil {
			return fmt.Errorf("unknown setting %q", fs.Arg(1))
		}
		b, _ := json.Marshal(st.Get(fs.Arg(1)))
		fmt.Println(string(b))
		return nil
	case "set":
		if fs.NArg() != 3 {
			return fmt.Errorf("usage: buropher setting set <name> <value>  (JSON for serialized settings)")
		}
		name, raw := fs.Arg(1), fs.Arg(2)
		def := settings.Lookup(name)
		if def == nil {
			return fmt.Errorf("unknown setting %q", name)
		}
		var v any = raw
		if def.Serialized {
			if err := json.Unmarshal([]byte(raw), &v); err != nil {
				return fmt.Errorf("%s is serialized; value must be JSON: %w", name, err)
			}
		}
		return st.Set(ctx, name, v)
	default:
		return fmt.Errorf("usage: buropher setting get|set ...")
	}
}
