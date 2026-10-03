package mailhandler

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// POP3Options は Redmine::POP3.check の pop_options（rake redmine:email:receive_pop3 の環境変数）。
type POP3Options struct {
	// Host は POP3 サーバー（既定 127.0.0.1）。
	Host string
	// Port はポート（既定 110、SSL なら 995）。
	Port string
	// APOP は APOP 認証を使うか。
	APOP bool
	// SSL は SSL を使うか（"" なら使わない。"force" ならサーバー証明書を検証しない）。
	SSL string
	// Username / Password はアカウント。
	Username string
	Password string
	// DeleteUnprocessed は受け付けなかったメールもサーバーから削除するか（既定は残す）。
	DeleteUnprocessed bool
}

var popMessageIDRe = regexp.MustCompile(`(?m)^Message-I[dD]: (.*)`)

// CheckPOP3 は Redmine::POP3.check（すべてのメールを受信し、受け付けたものはサーバーから削除する）。
func CheckPOP3(ctx context.Context, o POP3Options, receive ReceiveFunc, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	host := o.Host
	if host == "" {
		host = "127.0.0.1"
	}
	port := o.Port
	if port == "" {
		if o.SSL != "" {
			port = "995"
		} else {
			port = "110"
		}
	}
	addr := net.JoinHostPort(host, port)
	logger.Debug("Connecting to " + host + "...")
	c, err := dialPOP3(addr, host, o.SSL)
	if err != nil {
		return err
	}
	defer c.close()
	if err := c.auth(o.Username, o.Password, o.APOP); err != nil {
		return err
	}
	ids, err := c.list()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		logger.Debug("No email to process")
	} else {
		logger.Debug(fmt.Sprintf("%d email(s) to process...", len(ids)))
		for _, id := range ids {
			if ctx.Err() != nil {
				// 停止中: 残りはサーバに残す（QUIT で処理済みの分の削除を確定する）
				break
			}
			msg, err := c.retr(id)
			if err != nil {
				return err
			}
			messageID := ""
			if m := popMessageIDRe.FindSubmatch(msg); m != nil {
				messageID = strings.TrimSpace(string(m[1]))
			}
			ok := receive(ctx, msg)
			if !ok && ctx.Err() != nil {
				// 停止（ctx のキャンセル）で受信が中断された: 処理できなかったとはみなさず、削除しない
				break
			}
			if ok {
				if err := c.dele(id); err != nil {
					return err
				}
				logger.Debug("--> Message " + messageID + " processed and deleted from the server")
			} else if o.DeleteUnprocessed {
				if err := c.dele(id); err != nil {
					return err
				}
				logger.Debug("--> Message " + messageID + " NOT processed and deleted from the server")
			} else {
				logger.Debug("--> Message " + messageID + " NOT processed and left on the server")
			}
		}
	}
	return c.quit()
}

// pop3Conn は最小限の POP3 クライアント（RFC 1939）。
type pop3Conn struct {
	conn     net.Conn
	r        *bufio.Reader
	greeting string
}

func dialPOP3(addr, host, ssl string) (*pop3Conn, error) {
	d := &net.Dialer{Timeout: 30 * time.Second}
	var conn net.Conn
	var err error
	if ssl != "" {
		conn, err = tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: host, InsecureSkipVerify: ssl == "force"})
	} else {
		conn, err = d.Dial("tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("pop3: connect %s: %w", addr, err)
	}
	c := &pop3Conn{conn: conn, r: bufio.NewReader(conn)}
	line, err := c.readOK()
	if err != nil {
		conn.Close()
		return nil, err
	}
	c.greeting = line
	return c, nil
}

func (c *pop3Conn) close() { _ = c.conn.Close() }

func (c *pop3Conn) readLine() (string, error) {
	line, err := c.r.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("pop3: read: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (c *pop3Conn) readOK() (string, error) {
	line, err := c.readLine()
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(line, "+OK") {
		return "", errors.New("pop3: " + line)
	}
	return line, nil
}

func (c *pop3Conn) cmd(format string, args ...any) (string, error) {
	if _, err := fmt.Fprintf(c.conn, format+"\r\n", args...); err != nil {
		return "", fmt.Errorf("pop3: write: %w", err)
	}
	return c.readOK()
}

// readMulti は複数行の応答（"." で終わる。行頭の ".." は "." に戻す）。
func (c *pop3Conn) readMulti() ([]byte, error) {
	var buf bytes.Buffer
	for {
		line, err := c.r.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil, errors.New("pop3: unexpected EOF")
			}
			return nil, fmt.Errorf("pop3: read: %w", err)
		}
		trimmed := bytes.TrimRight(line, "\r\n")
		if bytes.Equal(trimmed, []byte(".")) {
			return buf.Bytes(), nil
		}
		if bytes.HasPrefix(line, []byte("..")) {
			line = line[1:]
		}
		buf.Write(line)
	}
}

var apopTimestampRe = regexp.MustCompile(`<[^>]+>`)

func (c *pop3Conn) auth(user, pass string, apop bool) error {
	if apop {
		ts := apopTimestampRe.FindString(c.greeting)
		if ts == "" {
			return errors.New("pop3: server does not support APOP")
		}
		sum := md5.Sum([]byte(ts + pass))
		_, err := c.cmd("APOP %s %s", user, hex.EncodeToString(sum[:]))
		return err
	}
	if _, err := c.cmd("USER %s", user); err != nil {
		return err
	}
	_, err := c.cmd("PASS %s", pass)
	return err
}

func (c *pop3Conn) list() ([]int, error) {
	if _, err := c.cmd("LIST"); err != nil {
		return nil, err
	}
	body, err := c.readMulti()
	if err != nil {
		return nil, err
	}
	var ids []int
	for _, line := range strings.Split(string(body), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if n, err := strconv.Atoi(f[0]); err == nil {
			ids = append(ids, n)
		}
	}
	return ids, nil
}

func (c *pop3Conn) retr(id int) ([]byte, error) {
	if _, err := c.cmd("RETR %d", id); err != nil {
		return nil, err
	}
	return c.readMulti()
}

func (c *pop3Conn) dele(id int) error {
	_, err := c.cmd("DELE %d", id)
	return err
}

func (c *pop3Conn) quit() error {
	_, err := c.cmd("QUIT")
	return err
}
