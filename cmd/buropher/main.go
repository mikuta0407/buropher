// Command buropher は Redmine 互換チケットシステムの単一バイナリ。
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mikuta0407/buropher/internal/bootstrap"
	"github.com/mikuta0407/buropher/internal/config"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/redmineimport/export"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/server"
	"github.com/mikuta0407/buropher/internal/settings"
)

var version = "dev"

func usage() {
	fmt.Fprintf(os.Stderr, `usage: buropher <command> [options]

commands:
  serve     start the web server
  migrate   apply database migrations (up|down|status)
  init      load default data and create the administrator
  redmine   migrate data from Redmine (export)
  setting   get or set a setting (setting get <name> / setting set <name> <value>)
  version   print version
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
	case "version":
		fmt.Println("buropher", version)
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
	srv, err := server.New(cfg, d)
	if err != nil {
		return err
	}
	return srv.Run(ctx)
}

// openDB は設定の DB を開く。SQLite の場合は親ディレクトリを作成する。
func openDB(ctx context.Context, cfg *config.Config) (*db.DB, error) {
	if cfg.Database.Driver == "sqlite" && !strings.HasPrefix(cfg.Database.DSN, "file:") && cfg.Database.DSN != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(cfg.Database.DSN), 0o755); err != nil {
			return nil, err
		}
	}
	return db.Open(ctx, cfg.Database.Driver, cfg.Database.DSN)
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
	email := fs.String("admin-email", "admin@example.net", "administrator email")
	pw := fs.String("admin-password", os.Getenv("BUROPHER_ADMIN_PASSWORD"), "administrator password (random if empty)")
	noData := fs.Bool("no-default-data", false, "do not load default roles/trackers/statuses")
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
		return fmt.Errorf("usage: buropher redmine export [options]")
	}
	switch args[0] {
	case "export":
		export.ToolVersion = version
		return export.Main(args[1:])
	default:
		return fmt.Errorf("unknown redmine command %q", args[0])
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
