package mail

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sender はメールの配送先。
type Sender interface {
	Send(ctx context.Context, m *Message) error
}

// SenderFunc は関数を Sender にする。
type SenderFunc func(ctx context.Context, m *Message) error

// Send は f(ctx, m)。
func (f SenderFunc) Send(ctx context.Context, m *Message) error { return f(ctx, m) }

// SMTPConfig は SMTP 配送の設定（Redmine の configuration.yml の email_delivery.smtp_settings と同じ項目）。
type SMTPConfig struct {
	Address string
	Port    int
	// Domain は HELO/EHLO で名乗るドメイン（空なら localhost）。
	Domain   string
	UserName string
	Password string
	// Authentication は "plain" / "login" / "cram_md5"（空なら UserName があれば plain）。
	Authentication string
	// EnableStartTLSAuto はサーバが STARTTLS を広告していれば使う（既定 true）。
	EnableStartTLSAuto bool
	// TLS は接続時から TLS を使う（SMTPS、通常 465 番）。
	TLS bool
	// InsecureSkipVerify は証明書を検証しない（openssl_verify_mode: none）。
	InsecureSkipVerify bool
	// Timeout は接続・送受信のタイムアウト（0 なら 30 秒）。
	Timeout time.Duration
}

// SMTPSender は SMTP でメールを送る。
type SMTPSender struct {
	Config SMTPConfig
}

// ErrNoRecipients は宛先が無いメール（Redmine の deliver_mail は送らずに false を返す）。
var ErrNoRecipients = errors.New("mail: no recipients")

// Send は 1 通を SMTP で送る。
func (s *SMTPSender) Send(ctx context.Context, m *Message) error {
	rcpts := m.Recipients()
	if len(rcpts) == 0 {
		return ErrNoRecipients
	}
	data, err := m.Bytes()
	if err != nil {
		return err
	}
	c := s.Config
	port := c.Port
	if port == 0 {
		port = 25
		if c.TLS {
			port = 465
		}
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	addr := net.JoinHostPort(c.Address, strconv.Itoa(port))
	tlsConf := &tls.Config{ServerName: c.Address, InsecureSkipVerify: c.InsecureSkipVerify} //nolint:gosec // 設定で明示した場合のみ
	d := &net.Dialer{Timeout: timeout}
	var conn net.Conn
	if c.TLS {
		conn, err = (&tls.Dialer{NetDialer: d, Config: tlsConf}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp: connect %s: %w", addr, err)
	}
	deadline := time.Now().Add(timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)
	cl, err := smtp.NewClient(conn, c.Address)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp: %w", err)
	}
	defer cl.Close()
	helo := c.Domain
	if helo == "" {
		helo = "localhost"
	}
	if err := cl.Hello(helo); err != nil {
		return fmt.Errorf("smtp: EHLO: %w", err)
	}
	if !c.TLS && c.EnableStartTLSAuto {
		if ok, _ := cl.Extension("STARTTLS"); ok {
			if err := cl.StartTLS(tlsConf); err != nil {
				return fmt.Errorf("smtp: STARTTLS: %w", err)
			}
		}
	}
	if c.UserName != "" {
		var auth smtp.Auth
		switch strings.ToLower(c.Authentication) {
		case "login":
			auth = &loginAuth{user: c.UserName, pass: c.Password}
		case "cram_md5", "cram-md5":
			auth = &cramMD5Auth{user: c.UserName, secret: c.Password}
		default:
			auth = &plainAuth{user: c.UserName, pass: c.Password}
		}
		if err := cl.Auth(auth); err != nil {
			return fmt.Errorf("smtp: AUTH: %w", err)
		}
	}
	if err := cl.Mail(m.EnvelopeFrom()); err != nil {
		return fmt.Errorf("smtp: MAIL FROM: %w", err)
	}
	for _, r := range rcpts {
		if err := cl.Rcpt(r); err != nil {
			return fmt.Errorf("smtp: RCPT TO %s: %w", r, err)
		}
	}
	wc, err := cl.Data()
	if err != nil {
		return fmt.Errorf("smtp: DATA: %w", err)
	}
	if _, err := wc.Write(data); err != nil {
		return fmt.Errorf("smtp: DATA: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("smtp: DATA: %w", err)
	}
	return cl.Quit()
}

// plainAuth は AUTH PLAIN（net/smtp.PlainAuth は非 TLS の非 localhost を拒否するが、
// Redmine（Net::SMTP）は設定どおりに送るので同じ挙動にする）。
type plainAuth struct{ user, pass string }

func (a *plainAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.pass), nil
}

func (a *plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("smtp: unexpected server challenge")
	}
	return nil, nil
}

// loginAuth は AUTH LOGIN。
type loginAuth struct{ user, pass string }

func (a *loginAuth) Start(*smtp.ServerInfo) (string, []byte, error) { return "LOGIN", nil, nil }

func (a *loginAuth) Next(from []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	p := strings.ToLower(strings.TrimSpace(string(from)))
	switch {
	case strings.Contains(p, "username"), strings.Contains(p, "user"):
		return []byte(a.user), nil
	case strings.Contains(p, "password"), strings.Contains(p, "pass"):
		return []byte(a.pass), nil
	}
	return nil, fmt.Errorf("smtp: unexpected LOGIN challenge %q", from)
}

// cramMD5Auth は AUTH CRAM-MD5。
type cramMD5Auth struct{ user, secret string }

func (a *cramMD5Auth) Start(*smtp.ServerInfo) (string, []byte, error) { return "CRAM-MD5", nil, nil }

func (a *cramMD5Auth) Next(from []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	h := hmac.New(md5.New, []byte(a.secret))
	h.Write(from)
	return []byte(a.user + " " + hex.EncodeToString(h.Sum(nil))), nil
}

// SendmailSender は sendmail コマンドに渡す（delivery_method: :sendmail）。
type SendmailSender struct {
	// Location はコマンドのパス（空なら /usr/sbin/sendmail）。
	Location string
	// Arguments は引数（空なら "-i"）。宛先は末尾に付ける。
	Arguments []string
}

// Send は sendmail を実行する。
func (s *SendmailSender) Send(ctx context.Context, m *Message) error {
	rcpts := m.Recipients()
	if len(rcpts) == 0 {
		return ErrNoRecipients
	}
	data, err := m.Bytes()
	if err != nil {
		return err
	}
	loc := s.Location
	if loc == "" {
		loc = "/usr/sbin/sendmail"
	}
	args := s.Arguments
	if len(args) == 0 {
		args = []string{"-i"}
	}
	args = append(append(append([]string{}, args...), "-f", m.EnvelopeFrom(), "--"), rcpts...)
	cmd := exec.CommandContext(ctx, loc, args...)
	cmd.Stdin = bytes.NewReader(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sendmail: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// NullSender は送らずに捨てる（delivery_method 未設定）。
type NullSender struct{}

// Send は何もしない。
func (NullSender) Send(context.Context, *Message) error { return nil }

// TestSender は送ったメールを保持する（ActionMailer の :test 配送。テスト用）。
type TestSender struct {
	mu         sync.Mutex
	Deliveries []*Message
	// Err が非 nil なら Send はこのエラーを返す。
	Err error
}

// Send はメールを保持する。
func (t *TestSender) Send(_ context.Context, m *Message) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.Err != nil {
		return t.Err
	}
	t.Deliveries = append(t.Deliveries, m)
	return nil
}

// Messages は保持しているメールのコピー。
func (t *TestSender) Messages() []*Message {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]*Message(nil), t.Deliveries...)
}

// Clear は保持しているメールを消す。
func (t *TestSender) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Deliveries = nil
}
