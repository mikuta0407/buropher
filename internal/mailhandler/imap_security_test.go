// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package mailhandler

import (
	"bufio"
	"context"
	"crypto/tls"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// scriptedIMAP は 1 接続だけ受け、挨拶の後は handle に任せる IMAP サーバ（異常系のテスト用）。
func scriptedIMAP(t *testing.T, handle func(c net.Conn, r *bufio.Reader)) (host, port string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = c.Write([]byte("* OK [CAPABILITY IMAP4rev1 STARTTLS] ready\r\n"))
		handle(c, bufio.NewReader(c))
	}()
	host, port, _ = net.SplitHostPort(ln.Addr().String())
	return host, port
}

// TestCheckIMAPStalledServerTimesOut は応答しなくなった IMAP サーバで受信が止まり続けないこと
// （go-imap はコマンドに期限を設けないため、以前は LOGIN の応答待ちで永久に止まっていた）。
func TestCheckIMAPStalledServerTimesOut(t *testing.T) {
	old := imapIdleTimeout
	imapIdleTimeout = 200 * time.Millisecond
	t.Cleanup(func() { imapIdleTimeout = old })
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	host, port := scriptedIMAP(t, func(c net.Conn, r *bufio.Reader) {
		// コマンドを読むだけで応答しない
		_, _ = r.ReadString('\n')
		<-stop
	})
	done := make(chan error, 1)
	go func() {
		done <- CheckIMAP(context.Background(), IMAPOptions{Host: host, Port: port, Username: "u", Password: "p"},
			func(context.Context, []byte) bool { return true }, nil)
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("CheckIMAP succeeded against a stalled server")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("CheckIMAP did not time out against a stalled server")
	}
}

// TestCheckIMAPStartTLSRefusedDoesNotDowngrade は STARTTLS が拒否されたら平文でログインしないこと。
func TestCheckIMAPStartTLSRefusedDoesNotDowngrade(t *testing.T) {
	got := make(chan string, 16)
	host, port := scriptedIMAP(t, func(c net.Conn, r *bufio.Reader) {
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			got <- line
			tag, rest, _ := strings.Cut(strings.TrimSpace(line), " ")
			if strings.HasPrefix(strings.ToUpper(rest), "STARTTLS") {
				_, _ = c.Write([]byte(tag + " NO STARTTLS unavailable\r\n"))
				continue
			}
			_, _ = c.Write([]byte(tag + " OK done\r\n"))
		}
	})
	err := CheckIMAP(context.Background(), IMAPOptions{Host: host, Port: port, StartTLS: true, Username: "u", Password: "secret-pw"},
		func(context.Context, []byte) bool { return true }, nil)
	if err == nil {
		t.Fatal("CheckIMAP succeeded although STARTTLS was refused")
	}
	time.Sleep(100 * time.Millisecond)
	for {
		select {
		case line := <-got:
			if strings.Contains(line, "secret-pw") {
				t.Fatalf("password sent in cleartext after refused STARTTLS: %q", line)
			}
			continue
		default:
		}
		break
	}
}

// TestCheckIMAPSlowReceiveKeepsConnection は受信処理（MailHandler）が長くても無通信として切らないこと。
func TestCheckIMAPSlowReceiveKeepsConnection(t *testing.T) {
	old := imapIdleTimeout
	imapIdleTimeout = 100 * time.Millisecond
	t.Cleanup(func() { imapIdleTimeout = old })
	mem := imapmemserver.New()
	user := imapmemserver.NewUser("redmine", "secret")
	mem.AddUser(user)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	body := "Subject: slow\r\n\r\nbody\r\n"
	if _, err := user.Append("INBOX", literal{strings.NewReader(body)}, &imap.AppendOptions{}); err != nil {
		t.Fatal(err)
	}
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}},
		InsecureAuth: true,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	n := 0
	err = CheckIMAP(context.Background(), IMAPOptions{Host: host, Port: port, Username: "redmine", Password: "secret"},
		func(context.Context, []byte) bool { n++; time.Sleep(400 * time.Millisecond); return true }, nil)
	if err != nil || n != 1 {
		t.Fatalf("err = %v, received = %d", err, n)
	}
}

// TestCheckIMAPSSLVerifiesCertificate は ssl を指定したらサーバ証明書を検証し、ssl=force のときだけ検証しないこと。
func TestCheckIMAPSSLVerifiesCertificate(t *testing.T) {
	// httptest の自己署名証明書を IMAP サーバに使う
	hs := httptest.NewUnstartedServer(nil)
	hs.StartTLS()
	cert := hs.TLS.Certificates
	hs.Close()

	mem := imapmemserver.New()
	user := imapmemserver.NewUser("redmine", "secret")
	mem.AddUser(user)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}},
		InsecureAuth: true,
	})
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: cert})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	recv := func(context.Context, []byte) bool { return true }

	err = CheckIMAP(context.Background(), IMAPOptions{Host: host, Port: port, SSL: "1", Username: "redmine", Password: "secret"}, recv, nil)
	if err == nil {
		t.Fatal("untrusted certificate was accepted with ssl=1")
	}
	if err := CheckIMAP(context.Background(), IMAPOptions{Host: host, Port: port, SSL: "force", Username: "redmine", Password: "secret"}, recv, nil); err != nil {
		t.Fatalf("ssl=force: %v", err)
	}
}

// literal は imap.LiteralReader（strings.Reader に Size がある）。
type literal struct{ *strings.Reader }
