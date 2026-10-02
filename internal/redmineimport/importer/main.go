package importer

import (
	"context"
	"flag"
	"fmt"
	"github.com/mikuta0407/buropher/internal/config"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/mikuta0407/buropher/internal/db"
)

// Main は `buropher redmine import` サブコマンドの実装。args はサブコマンド以降の引数。
func Main(args []string) error {
	return mainWith(args, os.Stdout, os.Stderr)
}

func mainWith(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("redmine import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var opt Options
	var driver, dsn, archivePath, reportJSON string
	var quiet, migrate bool
	fs.StringVar(&driver, "driver", envOr("BUROPHER_DB_DRIVER", "sqlite"), "target database driver (sqlite / postgres)")
	fs.StringVar(&dsn, "dsn", envOr("BUROPHER_DB_DSN", "data/buropher.db"), "target database DSN (SQLite path or PostgreSQL DSN)")
	fs.StringVar(&archivePath, "archive", "", "export archive (.tar.zst) created by `buropher redmine export` (or give it as the argument)")
	fs.StringVar(&opt.CipherKey, "cipher-key", os.Getenv("REDMINE_CIPHER_KEY"), "Redmine database_cipher_key (decrypts TOTP secrets, LDAP and repository passwords)")
	fs.StringVar(&opt.NewCipherKey, "secret-key", os.Getenv("BUROPHER_SECRET_KEY"), "buropher server.secret_key used to re-encrypt secret values")
	fs.StringVar(&opt.FilesDir, "files-dir", envOr("BUROPHER_ATTACHMENTS_PATH", "data/files"), "buropher attachments directory (files are copied here; empty = do not copy)")
	fs.StringVar(&opt.SourceFilesDir, "source-files-dir", "", "Redmine files directory, used when the archive was exported with --no-files")
	fs.StringVar(&opt.TempDir, "temp-dir", "", "directory for temporary files (default: next to the archive)")
	fs.BoolVar(&opt.DryRun, "dry-run", false, "convert and check everything, then roll back")
	fs.StringVar(&reportJSON, "report-json", "", "write the report as JSON to this file ('-' = stdout)")
	fs.BoolVar(&migrate, "migrate", false, "apply pending buropher migrations to the target first")
	fs.BoolVar(&quiet, "quiet", false, "suppress progress logs")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: buropher redmine import [options] ARCHIVE\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case archivePath == "" && fs.NArg() == 1:
		archivePath = fs.Arg(0)
	case fs.NArg() > 0:
		fs.Usage()
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if archivePath == "" {
		fs.Usage()
		return fmt.Errorf("archive is required")
	}
	if opt.NewCipherKey == "" {
		// serve と同じ鍵（設定 → データディレクトリの secret_key → 生成）を使う
		key, err := config.SecretKey(&config.Config{Database: config.Database{Driver: driver, DSN: dsn}})
		if err != nil {
			return err
		}
		opt.NewCipherKey = string(key)
	}
	level := slog.LevelInfo
	if quiet {
		level = slog.LevelError
	}
	opt.Logger = slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	d, err := db.Open(ctx, driver, dsn)
	if err != nil {
		return err
	}
	defer d.Close()
	if migrate {
		if _, err := db.Migrate(ctx, d, db.Up); err != nil {
			return err
		}
	}
	rep, runErr := Run(ctx, d, archivePath, opt)
	if rep != nil {
		if reportJSON == "-" {
			if err := rep.WriteJSON(stdout); err != nil {
				return err
			}
		} else {
			rep.WriteText(stdout)
			if reportJSON != "" {
				f, err := os.Create(reportJSON)
				if err != nil {
					return err
				}
				werr := rep.WriteJSON(f)
				if cerr := f.Close(); werr == nil {
					werr = cerr
				}
				if werr != nil {
					return werr
				}
			}
		}
	}
	return runErr
}

func envOr(k, def string) string {
	if v, ok := os.LookupEnv(k); ok {
		return v
	}
	return def
}
