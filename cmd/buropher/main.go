// Command buropher は Redmine 互換チケットシステムの単一バイナリ。
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/mikuta0407/buropher/internal/config"
	"github.com/mikuta0407/buropher/internal/server"
)

var version = "dev"

func usage() {
	fmt.Fprintf(os.Stderr, `usage: buropher <command> [options]

commands:
  serve     start the web server
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
	srv, err := server.New(cfg)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return srv.Run(ctx)
}
