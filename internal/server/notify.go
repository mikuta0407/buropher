// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server

// 通知（メール・Discord DM）とジョブキューの組み立て。serve でワーカーと定期実行（期日リマインダ・
// 期限切れデータの削除）を起動する。

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/config"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/discord"
	"github.com/mikuta0407/buropher/internal/handler"
	"github.com/mikuta0407/buropher/internal/jobs"
	"github.com/mikuta0407/buropher/internal/mail"
	"github.com/mikuta0407/buropher/internal/notify"
)

// MailSender は config の mail から配送先を作る（delivery_method が空・none なら nil = 送らない）。
func MailSender(c config.Mail) (mail.Sender, error) {
	switch strings.ToLower(strings.TrimPrefix(c.DeliveryMethod, ":")) {
	case "", "none":
		return nil, nil
	case "smtp", "async_smtp":
		s := c.SMTP
		starttls := true
		if s.EnableStartTLSAuto != nil {
			starttls = *s.EnableStartTLSAuto
		}
		addr := s.Address
		if addr == "" {
			addr = "localhost"
		}
		return &mail.SMTPSender{Config: mail.SMTPConfig{
			Address: addr, Port: s.Port, Domain: s.Domain, UserName: s.UserName, Password: s.Password,
			Authentication: s.Authentication, EnableStartTLSAuto: starttls, TLS: s.TLS,
			InsecureSkipVerify: strings.EqualFold(s.OpenSSLVerifyMode, "none"), Timeout: time.Duration(s.Timeout) * time.Second,
		}}, nil
	case "sendmail", "async_sendmail":
		return &mail.SendmailSender{Location: c.Sendmail.Location, Arguments: c.Sendmail.Arguments}, nil
	case "test":
		return &mail.TestSender{}, nil
	}
	return nil, fmt.Errorf("config: unknown mail.delivery_method %q", c.DeliveryMethod)
}

func parseDuration(s string, def time.Duration) (time.Duration, error) {
	if s == "" {
		return def, nil
	}
	return time.ParseDuration(s)
}

// setupNotify はジョブキューと通知の配送層を作り、App に設定する。
func setupNotify(cfg *config.Config, d *db.DB, app *handler.App, o Options) (*jobs.Queue, *notify.Service, error) {
	poll, err := parseDuration(cfg.Jobs.PollInterval, 2*time.Second)
	if err != nil {
		return nil, nil, fmt.Errorf("config: jobs.poll_interval: %w", err)
	}
	q := jobs.New(d, jobs.Options{Workers: cfg.Jobs.Workers, PollInterval: poll, MaxAttempts: cfg.Jobs.MaxAttempts})
	q.Logger = o.Logger
	q.Now = o.Now
	sender := o.MailSender
	if sender == nil {
		sender, err = MailSender(cfg.Mail)
		if err != nil {
			return nil, nil, err
		}
	}
	dc := &discord.Client{BaseURL: cfg.Discord.APIBase, HTTP: &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, Proxy: http.ProxyFromEnvironment}}}
	svc := &notify.Service{DB: d, Settings: app.Settings, Queue: q, Renderer: app, Mail: sender, Discord: dc,
		Secrets: app.Secrets, Logger: o.Logger, Now: o.Now}
	svc.RegisterJobs()
	// buropher 拡張: LDAP の定期同期
	app.RegisterLDAPSyncJob(q)
	// Webhook の送信（WebhookJob）
	app.RegisterWebhookJob(q)
	app.Notify = svc
	if svc.MailEnabled() {
		// パスワード再発行・登録・2 要素認証のセキュリティ通知などのアカウント系メール（未設定ならログのみ）
		app.Mailer = handler.NotifyAccountMailer{Service: svc}
	}
	app.DiscordAuthorizeURL = cfg.Discord.AuthorizeURL
	app.DiscordFeature = cfg.Discord.Enabled
	app.RegisterDiscordHooks()
	return q, svc, nil
}

// schedule は定期実行するジョブ（期限切れデータの削除・期日リマインダ）。
func schedule(cfg *config.Config, q *jobs.Queue) (*jobs.Scheduler, error) {
	cleanup, err := parseDuration(cfg.Jobs.CleanupInterval, time.Hour)
	if err != nil {
		return nil, fmt.Errorf("config: jobs.cleanup_interval: %w", err)
	}
	jr := time.Duration(cfg.Jobs.JobsRetentionDays) * 24 * time.Hour
	dr := time.Duration(cfg.Jobs.DeliveriesRetentionDays) * 24 * time.Hour
	s := &jobs.Scheduler{Queue: q, Jobs: []jobs.Periodic{{
		Name: "cleanup", Kind: notify.JobCleanup, Schedule: jobs.Every(cleanup), RunAtStart: true,
		Payload: notify.CleanupOptions{JobsRetention: jr, DeliveriesRetention: dr},
	}}}
	// LDAP の定期同期（各認証方式の sync_enabled / sync_interval はジョブの中で判定する）
	s.Jobs = append(s.Jobs, jobs.Periodic{Name: "ldap_sync", Kind: handler.JobLDAPSync, Schedule: jobs.Every(handler.LDAPSyncCheckInterval)})
	if r := cfg.Jobs.Reminders; r.Enabled {
		at := r.At
		if at == "" {
			at = "08:00"
		}
		daily, err := jobs.ParseDaily(at, time.Local)
		if err != nil {
			return nil, fmt.Errorf("config: jobs.reminders.at: %w", err)
		}
		s.Jobs = append(s.Jobs, jobs.Periodic{Name: "reminders", Kind: notify.JobReminders, Schedule: daily,
			Payload: notify.ReminderOptions{Days: r.Days, Tracker: r.Tracker, Project: r.Project, Users: r.Users, Version: r.Version}})
	}
	return s, nil
}

// runWorkers はワーカーと定期実行を ctx が終わるまで動かす（jobs.workers が負なら起動しない）。
func (s *Server) runWorkers(ctx context.Context) {
	if s.queue == nil || s.cfg.Jobs.Workers < 0 {
		return
	}
	sch, err := schedule(s.cfg, s.queue)
	if err != nil {
		slog.Error("jobs: scheduler disabled", "err", err)
	} else {
		s.goBG(func() { sch.Run(ctx) })
	}
	s.goBG(func() { s.queue.Run(ctx) })
}

// Jobs はジョブキュー（テスト・CLI 用）。
func (s *Server) Jobs() *jobs.Queue { return s.queue }

// Notify は通知の配送層（テスト・CLI 用）。
func (s *Server) Notify() *notify.Service { return s.notify }
