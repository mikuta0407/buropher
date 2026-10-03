// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package mailhandler

import (
	"context"
	"fmt"
	"time"
)

// PollConfig はサーバー内での定期受信の設定（config の [mail_receive]）。
type PollConfig struct {
	// Protocol は "imap" または "pop3"。
	Protocol string
	// Interval は受信の間隔（0 なら 5 分）。
	Interval time.Duration
	IMAP     IMAPOptions
	POP3     POP3Options
	// Options は MailHandler のオプション。
	Options Options
}

// CheckOnce は設定のプロトコルで 1 回受信する（rake redmine:email:receive_imap / receive_pop3）。
func (h *Handler) CheckOnce(ctx context.Context, cfg PollConfig) error {
	receive := func(ctx context.Context, raw []byte) bool { return h.SafeReceive(ctx, raw, cfg.Options) != nil }
	switch cfg.Protocol {
	case "imap":
		return CheckIMAP(ctx, cfg.IMAP, receive, h.logger())
	case "pop3":
		return CheckPOP3(ctx, cfg.POP3, receive, h.logger())
	}
	return fmt.Errorf("mail receive: unknown protocol %q (imap or pop3)", cfg.Protocol)
}

// Poll は ctx が終わるまで Interval ごとに受信する（起動直後にも 1 回受信する）。
// 受信のエラーはログに記録して次の回に再試行する。
func (h *Handler) Poll(ctx context.Context, cfg PollConfig) {
	interval := cfg.Interval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	h.logger().Info("mail receive: polling", "protocol", cfg.Protocol, "interval", interval.String())
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := h.CheckOnce(ctx, cfg); err != nil {
			h.logger().Error("mail receive failed", "protocol", cfg.Protocol, "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// OptionsFromMap は rake の環境変数と同じ名前のキーからオプションを作る（MailHandler.extract_options_from_env）。
func OptionsFromMap(m map[string]string) Options {
	return ExtractOptionsFromEnv(func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	})
}
