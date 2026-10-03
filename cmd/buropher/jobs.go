// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package main

import (
	"context"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/mikuta0407/buropher/internal/config"
	"github.com/mikuta0407/buropher/internal/notify"
	"github.com/mikuta0407/buropher/internal/server"
)

// openServer は CLI 用にサーバ（設定・通知・ジョブキュー）を組み立てる（HTTP は起動しない）。
func openServer(ctx context.Context, cfgPath string) (*server.Server, func(), error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, nil, err
	}
	d, err := openDB(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	if err := server.CheckInitialized(ctx, d); err != nil {
		d.Close()
		return nil, nil, err
	}
	srv, err := server.New(cfg, d, server.Options{Version: version})
	if err != nil {
		d.Close()
		return nil, nil, err
	}
	return srv, func() { d.Close() }, nil
}

// remindersCmd は rake redmine:send_reminders（Mailer.reminders を実行し、積んだ通知をその場で送る）。
func remindersCmd(args []string) error {
	fs := flag.NewFlagSet("reminders", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	days := fs.Int("days", 7, "number of days to remind about")
	tracker := fs.Int64("tracker", 0, "id of tracker (default: all trackers)")
	project := fs.String("project", "", "id or identifier of project (default: all projects)")
	users := fs.String("users", "", "comma separated list of user/group ids who should be reminded")
	ver := fs.String("version", "", "name of target version for filtering issues")
	fs.Parse(args)
	ctx := context.Background()
	srv, closeFn, err := openServer(ctx, *cfgPath)
	if err != nil {
		return err
	}
	defer closeFn()
	o := notify.ReminderOptions{Days: *days, Tracker: *tracker, Project: *project, Version: *ver}
	for _, s := range strings.Split(*users, ",") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid user id %q", s)
		}
		o.Users = append(o.Users, id)
	}
	n, err := srv.Notify().Reminders(ctx, o)
	if err != nil {
		return err
	}
	// Mailer.with_synched_deliveries（rake タスクでは積んだメールをその場で送る）
	done, err := srv.Jobs().RunPending(ctx)
	fmt.Printf("reminders: %d recipient(s), %d job(s) processed\n", n, done)
	return err
}

// jobsCmd は jobs run（実行可能なジョブを一度だけ処理する。cron からの実行やワーカーを止めた構成向け）。
func jobsCmd(args []string) error {
	fs := flag.NewFlagSet("jobs", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml")
	fs.Parse(args)
	if fs.Arg(0) != "run" {
		return fmt.Errorf("usage: buropher jobs run [-config path]")
	}
	ctx := context.Background()
	srv, closeFn, err := openServer(ctx, *cfgPath)
	if err != nil {
		return err
	}
	defer closeFn()
	if err := srv.Jobs().RecoverStale(ctx); err != nil {
		return err
	}
	n, err := srv.Jobs().RunPending(ctx)
	fmt.Printf("jobs: %d processed\n", n)
	return err
}
