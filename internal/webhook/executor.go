// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/mikuta0407/buropher/internal/brand"
)

// SignatureHeader は署名のヘッダ（Redmine と同じ名前。受信側の検証コードとの互換のため変えない）。
const SignatureHeader = "X-Redmine-Signature-256"

// SignatureHeaderAlias は buropher の別名ヘッダ（同じ値。docs/compatibility.md）。
const SignatureHeaderAlias = "X-Buropher-Signature-256"

// ErrNoConnection は接続できるアドレスが無かった（Redmine の SocketError "Could not connect to any IP"）。
var ErrNoConnection = errors.New("webhook: could not connect to any IP")

// StatusError は 2xx 以外の応答（Net::HTTPResponse#value の例外）。
type StatusError struct {
	StatusCode int
	Status     string
}

func (e *StatusError) Error() string { return "webhook: unexpected response " + e.Status }

// Executor は Webhook::Executor（1 回の POST）。
type Executor struct {
	// Validator は送信時の名前解決とアドレスの検証（nil なら空のブロックリスト）。
	Validator *Validator
	// ValidIPs は接続先アドレスの決定（nil なら Validator.IPsForURL）。テスト用。
	ValidIPs func(ctx context.Context, rawURL string) []netip.Addr
	// Timeout は接続・送信・応答待ちそれぞれのタイムアウト（0 なら Redmine と同じ 60 秒）。
	Timeout time.Duration
	// UserAgent は User-Agent（空なら brand.Name）。
	UserAgent string
	// TLSConfig は https の設定（nil なら既定。テスト用）。
	TLSConfig *tls.Config
}

// Signature は compute_signature（"sha256=" + HMAC-SHA256 の 16 進）。GitHub の Webhook と同じ形式。
func Signature(secret string, payload []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(payload)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func (x *Executor) timeout() time.Duration {
	if x.Timeout > 0 {
		return x.Timeout
	}
	return 60 * time.Second
}

// Call は payload を rawURL に POST する（Executor#call）。
//
// 検証済みのアドレスに順に接続し、接続できたところへ送る（リクエストを送った後の失敗では他のアドレスを
// 試さない）。2xx 以外の応答・送信の失敗はエラー。リダイレクトは追わない（Net::HTTP と同じ）。
// プロキシの環境変数は使わない（検証したアドレス以外へ接続させないため）。
func (x *Executor) Call(ctx context.Context, rawURL string, payload []byte, secret string) (int, error) {
	e, ok := ParseEndpoint(rawURL)
	if !ok {
		return 0, ErrNoConnection
	}
	var ips []netip.Addr
	switch {
	case x.ValidIPs != nil:
		ips = x.ValidIPs(ctx, rawURL)
	case x.Validator != nil:
		ips = x.Validator.IPsForURL(ctx, rawURL)
	default:
		ips = NewValidator(nil).IPsForURL(ctx, rawURL)
	}
	dialer := &net.Dialer{Timeout: x.timeout()}
	port := strconv.Itoa(e.Port)
	for _, ip := range ips {
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
		if err != nil {
			// 接続できなければ（まだ何も送っていないので）次のアドレスへ
			continue
		}
		return x.post(ctx, conn, e, payload, secret)
	}
	return 0, ErrNoConnection
}

// post は接続済みの conn で 1 回だけリクエストを送る。
func (x *Executor) post(ctx context.Context, conn net.Conn, e *Endpoint, payload []byte, secret string) (int, error) {
	var once sync.Once
	dial := func(context.Context, string, string) (net.Conn, error) {
		var c net.Conn
		once.Do(func() { c = conn })
		if c == nil {
			return nil, ErrNoConnection
		}
		return c, nil
	}
	tlsConf := x.TLSConfig
	if tlsConf == nil {
		tlsConf = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	tlsConf = tlsConf.Clone()
	if tlsConf.ServerName == "" {
		tlsConf.ServerName = e.Host
	}
	tr := &http.Transport{
		Proxy:                  nil,
		DialContext:            dial,
		TLSClientConfig:        tlsConf,
		TLSHandshakeTimeout:    x.timeout(),
		ResponseHeaderTimeout:  x.timeout(),
		DisableKeepAlives:      true,
		MaxResponseHeaderBytes: 1 << 20,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	ctx, cancel := context.WithTimeout(ctx, 3*x.timeout())
	defer cancel()
	u := *e.URL
	u.User = nil
	u.Fragment, u.RawFragment = "", ""
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		_ = conn.Close()
		return 0, err
	}
	ua := x.UserAgent
	if ua == "" {
		ua = brand.Name
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", ua)
	if secret != "" {
		sig := Signature(secret, payload)
		req.Header.Set(SignatureHeader, sig)
		req.Header.Set(SignatureHeaderAlias, sig)
	}
	if e.URL.User != nil {
		pw, _ := e.URL.User.Password()
		req.SetBasicAuth(e.URL.User.Username(), pw)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("webhook: POST failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, &StatusError{StatusCode: resp.StatusCode, Status: resp.Status}
	}
	return resp.StatusCode, nil
}
