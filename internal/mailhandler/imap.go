// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package mailhandler

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// IMAPOptions は Redmine::IMAP.check の imap_options（rake redmine:email:receive_imap の環境変数）。
type IMAPOptions struct {
	// Host は IMAP サーバー（既定 127.0.0.1）。
	Host string
	// Port はポート（既定 143）。
	Port string
	// SSL は SSL/TLS を使うか（"" なら使わない。"force" ならサーバー証明書を検証しない）。
	SSL string
	// StartTLS は STARTTLS を使うか。
	StartTLS bool
	// Username / Password はアカウント（Username が空ならログインしない）。
	Username string
	Password string
	// Folder は読むフォルダ（既定 INBOX）。
	Folder string
	// MoveOnSuccess は受信できたメールを削除せずに移すメールボックス。
	MoveOnSuccess string
	// MoveOnFailure は受け付けなかったメールを移すメールボックス。
	MoveOnFailure string
}

// ReceiveFunc は 1 通のメールを受信する（MailHandler.safe_receive。受け付けたら true）。
type ReceiveFunc func(ctx context.Context, raw []byte) bool

// CheckIMAP は Redmine::IMAP.check（未読のメールを受信し、成功したものは既読・削除（または移動）、
// 失敗したものは既読（move_on_failure があれば移動）にする）。
func CheckIMAP(ctx context.Context, o IMAPOptions, receive ReceiveFunc, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	host := o.Host
	if host == "" {
		host = "127.0.0.1"
	}
	port := o.Port
	if port == "" {
		port = "143"
	}
	folder := o.Folder
	if folder == "" {
		folder = "INBOX"
	}
	addr := net.JoinHostPort(host, port)
	opts := &imapclient.Options{TLSConfig: &tls.Config{ServerName: host, InsecureSkipVerify: o.SSL == "force"}}
	var c *imapclient.Client
	var err error
	switch {
	case o.SSL != "":
		c, err = imapclient.DialTLS(addr, opts)
	case o.StartTLS:
		c, err = imapclient.DialStartTLS(addr, opts)
	default:
		c, err = imapclient.DialInsecure(addr, opts)
	}
	if err != nil {
		return fmt.Errorf("imap: connect %s: %w", addr, err)
	}
	defer c.Close()
	if o.Username != "" {
		if err := c.Login(o.Username, o.Password).Wait(); err != nil {
			return fmt.Errorf("imap: login: %w", err)
		}
	}
	if _, err := c.Select(folder, nil).Wait(); err != nil {
		return fmt.Errorf("imap: select %s: %w", folder, err)
	}
	data, err := c.UIDSearch(&imap.SearchCriteria{NotFlag: []imap.Flag{imap.FlagSeen}}, nil).Wait()
	if err != nil {
		return fmt.Errorf("imap: search: %w", err)
	}
	section := &imap.FetchItemBodySection{}
	for _, uid := range data.AllUIDs() {
		if ctx.Err() != nil {
			// 停止中: 残りは未読のまま次回に回す（処理済みの分は下の EXPUNGE で確定する）
			break
		}
		set := imap.UIDSetNum(uid)
		msgs, err := c.Fetch(set, &imap.FetchOptions{BodySection: []*imap.FetchItemBodySection{section}}).Collect()
		if err != nil {
			return fmt.Errorf("imap: fetch %d: %w", uid, err)
		}
		if len(msgs) == 0 {
			continue
		}
		raw := msgs[0].FindBodySection(section)
		logger.Debug(fmt.Sprintf("Receiving message %d", uid))
		if receive(ctx, raw) {
			logger.Debug(fmt.Sprintf("Message %d successfully received", uid))
			if o.MoveOnSuccess != "" {
				if _, err := c.Copy(set, o.MoveOnSuccess).Wait(); err != nil {
					return fmt.Errorf("imap: copy %d: %w", uid, err)
				}
			}
			if err := c.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagSeen, imap.FlagDeleted}}, nil).Close(); err != nil {
				return fmt.Errorf("imap: store %d: %w", uid, err)
			}
		} else if ctx.Err() != nil {
			// 停止（ctx のキャンセル）で受信が中断された: 処理できなかったとはみなさず、未読のまま残す
			break
		} else {
			logger.Debug(fmt.Sprintf("Message %d can not be processed", uid))
			if err := c.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagSeen}}, nil).Close(); err != nil {
				return fmt.Errorf("imap: store %d: %w", uid, err)
			}
			if o.MoveOnFailure != "" {
				if _, err := c.Copy(set, o.MoveOnFailure).Wait(); err != nil {
					return fmt.Errorf("imap: copy %d: %w", uid, err)
				}
				if err := c.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
					return fmt.Errorf("imap: store %d: %w", uid, err)
				}
			}
		}
	}
	if err := c.Expunge().Close(); err != nil {
		return fmt.Errorf("imap: expunge: %w", err)
	}
	if err := c.Logout().Wait(); err != nil {
		logger.Debug("imap: logout", "err", err)
	}
	return nil
}
