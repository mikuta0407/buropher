// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package export

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// Main は `buropher redmine export` サブコマンドの実装。args はサブコマンド以降の引数。
func Main(args []string) error {
	return mainWith(args, os.Stdout, os.Stderr)
}

func mainWith(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("redmine export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var opt Options
	var noFiles, quiet bool
	fs.StringVar(&opt.DSN, "dsn", "", "source Redmine database: mysql://user:pass@host/db, postgres://..., sqlite:///path/redmine.sqlite3, sqlserver://...")
	fs.StringVar(&opt.Driver, "driver", "", "database kind (mysql/postgres/sqlite/sqlserver); auto-detected from --dsn scheme")
	fs.StringVar(&opt.FilesDir, "files", "", "Redmine attachments directory (default: <redmine-root>/files or attachments_storage_path)")
	fs.StringVar(&opt.SourceTimezone, "source-timezone", "", "IANA time zone of the Redmine server, e.g. Asia/Tokyo (required)")
	fs.StringVar(&opt.Output, "output", "", "output archive path (default: redmine-export-YYYYMMDD-HHMMSS.tar.zst)")
	fs.StringVar(&opt.Output, "o", "", "shorthand for --output")
	fs.BoolVar(&noFiles, "no-files", false, "do not include attachment files in the archive (missing files are still checked when --files is given)")
	fs.StringVar(&opt.RedmineRoot, "redmine-root", "", "Redmine installation directory (reads lib/redmine/version.rb and config/configuration.yml)")
	fs.StringVar(&opt.RedmineEnv, "redmine-env", "production", "section of config/configuration.yml to use")
	fs.BoolVar(&opt.Force, "force", false, "continue even if the acceptance check fails (development only)")
	fs.BoolVar(&opt.Overwrite, "overwrite", false, "overwrite the output file if it exists")
	fs.StringVar(&opt.TempDir, "temp-dir", "", "directory for temporary files (default: next to the output)")
	fs.BoolVar(&quiet, "quiet", false, "suppress progress logs")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: buropher redmine export --dsn DSN --source-timezone TZ [--files DIR] [options]\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if opt.DSN == "" || opt.SourceTimezone == "" {
		fs.Usage()
		return fmt.Errorf("--dsn and --source-timezone are required")
	}
	opt.IncludeFiles = !noFiles
	if opt.FilesDir == "" && opt.RedmineRoot == "" && opt.IncludeFiles {
		return fmt.Errorf("--files (or --redmine-root) is required unless --no-files is given")
	}
	level := slog.LevelInfo
	if quiet {
		level = slog.LevelError
	}
	opt.Logger = slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	m, err := Run(ctx, opt)
	if err != nil {
		return err
	}
	var rows int64
	for _, t := range m.Tables {
		rows += t.Rows
	}
	fmt.Fprintf(stdout, "exported %d tables (%d rows), %d files, %d missing files, %d warnings\n",
		len(m.Tables), rows, len(m.Files), len(m.MissingFiles), len(m.Warnings))
	for _, w := range m.Warnings {
		fmt.Fprintf(stdout, "warning: %s\n", w)
	}
	return nil
}
