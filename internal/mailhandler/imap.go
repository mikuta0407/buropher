// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package mailhandler

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

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

// imapIdleTimeout は IMAP サーバとのやりとりで何も届かない状態を許す上限。go-imap はコマンドの応答を
// 期限なしで待つため（応答の合間に読み込みの期限を外す）、応答しなくなったサーバで定期受信が止まり続けない
// よう、無通信が続いたら接続を切る（POP3 のコマンドごとの期限と同じ目的）。受信処理（MailHandler）の間は数えない。
var imapIdleTimeout = 5 * time.Minute

// idleTimeoutConn は busy の間に imapIdleTimeout を超えて読み書きが無ければ接続を閉じる net.Conn。
// go-imap は自前で読み込みの期限を設定・解除するため、期限ではなく監視用のゴルーチンで切る。
type idleTimeoutConn struct {
	net.Conn
	timeout   time.Duration
	busy      atomic.Bool
	last      atomic.Int64 // 最後に読み書きした時刻（UnixNano）
	stop      chan struct{}
	closeOnce sync.Once
}

func newIdleTimeoutConn(c net.Conn, timeout time.Duration) *idleTimeoutConn {
	ic := &idleTimeoutConn{Conn: c, timeout: timeout, stop: make(chan struct{})}
	ic.touch()
	go ic.watch()
	return ic
}

func (c *idleTimeoutConn) touch() { c.last.Store(time.Now().UnixNano()) }

func (c *idleTimeoutConn) watch() {
	t := time.NewTicker(max(c.timeout/4, 10*time.Millisecond))
	defer t.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-t.C:
			if c.busy.Load() && time.Since(time.Unix(0, c.last.Load())) > c.timeout {
				_ = c.Close()
				return
			}
		}
	}
}

// begin はサーバとのやりとりを始める（無通信の監視を始める）。
func (c *idleTimeoutConn) begin() {
	c.touch()
	c.busy.Store(true)
}

// end はやりとりを終える（受信処理の間は監視しない）。
func (c *idleTimeoutConn) end() { c.busy.Store(false) }

func (c *idleTimeoutConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.touch()
	}
	return n, err
}

func (c *idleTimeoutConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	if n > 0 {
		c.touch()
	}
	return n, err
}

func (c *idleTimeoutConn) Close() error {
	c.closeOnce.Do(func() { close(c.stop) })
	return c.Conn.Close()
}

// dialIMAP は接続し（SSL / STARTTLS / 平文）、無通信を監視する IMAP クライアントを返す。
func dialIMAP(ctx context.Context, addr string, o IMAPOptions, tlsConf *tls.Config, opts *imapclient.Options) (*imapclient.Client, *idleTimeoutConn, error) {
	d := &net.Dialer{Timeout: 30 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	conn := newIdleTimeoutConn(raw, imapIdleTimeout)
	conn.begin()
	switch {
	case o.SSL != "":
		cfg := tlsConf.Clone()
		cfg.NextProtos = []string{"imap"}
		tc := tls.Client(conn, cfg)
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, nil, err
		}
		return imapclient.New(tc, opts), conn, nil
	case o.StartTLS:
		// NewStartTLS は STARTTLS が拒否されたら失敗する（平文へは落とさない）
		c, err := imapclient.NewStartTLS(conn, opts)
		return c, conn, err
	}
	return imapclient.New(conn, opts), conn, nil
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
	tlsConf := &tls.Config{ServerName: host, InsecureSkipVerify: o.SSL == "force"} //nolint:gosec // ssl=force を明示した場合のみ（Redmine と同じ）
	opts := &imapclient.Options{TLSConfig: tlsConf}
	c, conn, err := dialIMAP(ctx, addr, o, tlsConf, opts)
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
		conn.end()
		ok := receive(ctx, raw)
		conn.begin()
		if ok {
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
