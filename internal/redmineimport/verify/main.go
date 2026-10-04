// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package verify

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mikuta0407/buropher/internal/db"
)

// Main は `buropher redmine verify` サブコマンドの実装。args はサブコマンド以降の引数。
func Main(args []string) error {
	return mainWith(args, os.Stdout, os.Stderr)
}

// passwordFlags は --password login:password(複数指定可)。
type passwordFlags map[string]string

func (p passwordFlags) String() string { return "" }
func (p passwordFlags) Set(s string) error {
	login, pw, ok := strings.Cut(s, ":")
	if !ok || login == "" {
		return errors.New("want login:password")
	}
	p[login] = pw
	return nil
}

func mainWith(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("redmine verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var opt Options
	var driver, dsn, archivePath, reportJSON string
	pws := passwordFlags{}
	fs.StringVar(&driver, "driver", envOr("BUROPHER_DB_DRIVER", "sqlite"), "target database driver (sqlite / postgres)")
	// DSN はパスワードを含みうるので環境変数をフラグの既定値にしない（-h の使い方に表示される）
	fs.StringVar(&dsn, "dsn", "", "target database DSN (default: $BUROPHER_DB_DSN or data/buropher.db)")
	fs.StringVar(&archivePath, "archive", "", "export archive (or give it as the argument)")
	fs.StringVar(&opt.FilesDir, "files-dir", "", "buropher attachments directory (checks file existence and size)")
	fs.BoolVar(&opt.Digests, "digests", false, "also verify attachment digests (slow)")
	fs.BoolVar(&opt.StrictCounts, "strict", false, "treat rows dropped during import as failures")
	fs.Var(pws, "password", "login:password to verify against the imported hash (repeatable)")
	fs.StringVar(&reportJSON, "report-json", "", "write the report as JSON to this file ('-' = stdout)")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: buropher redmine verify [options] ARCHIVE\n\n")
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
	if dsn == "" {
		dsn = envOr("BUROPHER_DB_DSN", "data/buropher.db")
	}
	opt.Passwords = pws
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	d, err := db.Open(ctx, driver, dsn)
	if err != nil {
		return err
	}
	defer d.Close()
	start := time.Now()
	rep, err := Verify(ctx, d, archivePath, opt)
	if err != nil {
		return err
	}
	if reportJSON == "-" {
		if err := rep.WriteJSON(stdout); err != nil {
			return err
		}
	} else {
		rep.WriteText(stdout)
		fmt.Fprintf(stdout, "\n(%s)\n", elapsed(start))
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
	if !rep.OK() {
		return errors.New("verification failed")
	}
	return nil
}

func envOr(k, def string) string {
	if v, ok := os.LookupEnv(k); ok {
		return v
	}
	return def
}
