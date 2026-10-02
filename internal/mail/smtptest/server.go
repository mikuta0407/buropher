// Package smtptest はテスト用のプロセス内 SMTP サーバ（外部ネットワーク不要）。
//
// EHLO / STARTTLS / AUTH（PLAIN, LOGIN, CRAM-MD5）/ MAIL / RCPT / DATA / RSET / NOOP / QUIT を扱い、
// 受け取ったメッセージを保持する。
package smtptest

import (
	"bufio"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"math/big"
	"net"
	"strings"
	"sync"
	"time"
)

// Message は受信した 1 通。
type Message struct {
	From     string
	To       []string
	Data     string
	Auth     string // 認証方式（"PLAIN" 等、未認証なら ""）
	User     string
	TLS      bool
	HeloName string
}

// Server は SMTP サーバ。
type Server struct {
	// User / Password が空でなければ AUTH を要求する。
	User, Password string
	// StartTLS は STARTTLS を広告する。
	StartTLS bool
	// RejectRcpt が true を返す宛先は 550 で拒否する。
	RejectRcpt func(addr string) bool

	ln       net.Listener
	tlsConf  *tls.Config
	mu       sync.Mutex
	messages []Message
	wg       sync.WaitGroup
}

// Start は 127.0.0.1 の空きポートで待ち受ける。
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	s.ln = ln
	if s.StartTLS {
		cert, err := selfSigned()
		if err != nil {
			return err
		}
		s.tlsConf = &tls.Config{Certificates: []tls.Certificate{cert}}
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				s.handle(c)
			}()
		}
	}()
	return nil
}

// Addr は待ち受けアドレス（host, port）。
func (s *Server) Addr() (string, int) {
	a := s.ln.Addr().(*net.TCPAddr)
	return a.IP.String(), a.Port
}

// Close は停止する。
func (s *Server) Close() {
	if s.ln != nil {
		s.ln.Close()
	}
	s.wg.Wait()
}

// Messages は受信したメッセージ。
func (s *Server) Messages() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Message(nil), s.messages...)
}

func (s *Server) handle(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	r := bufio.NewReader(c)
	w := bufio.NewWriter(c)
	reply := func(lines ...string) {
		for _, l := range lines {
			w.WriteString(l + "\r\n")
		}
		w.Flush()
	}
	reply("220 smtptest ESMTP")
	var cur Message
	authed := s.User == ""
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		cmd := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			cur.HeloName = strings.TrimSpace(line[4:])
			ls := []string{"250-smtptest"}
			if s.StartTLS && !cur.TLS {
				ls = append(ls, "250-STARTTLS")
			}
			if s.User != "" {
				ls = append(ls, "250-AUTH PLAIN LOGIN CRAM-MD5")
			}
			ls = append(ls, "250 8BITMIME")
			reply(ls...)
		case cmd == "STARTTLS":
			if s.tlsConf == nil {
				reply("502 not supported")
				continue
			}
			reply("220 ready")
			tc := tls.Server(c, s.tlsConf)
			if err := tc.Handshake(); err != nil {
				return
			}
			c = tc
			r = bufio.NewReader(tc)
			w = bufio.NewWriter(tc)
			cur = Message{TLS: true}
		case strings.HasPrefix(cmd, "AUTH "):
			parts := strings.Fields(line)
			mech := strings.ToUpper(parts[1])
			var user, pass string
			ok := false
			switch mech {
			case "PLAIN":
				var b64 string
				if len(parts) > 2 {
					b64 = parts[2]
				} else {
					reply("334 ")
					b64, _ = r.ReadString('\n')
				}
				dec, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
				f := strings.Split(string(dec), "\x00")
				if len(f) == 3 {
					user, pass = f[1], f[2]
					ok = user == s.User && pass == s.Password
				}
			case "LOGIN":
				reply("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
				u, _ := r.ReadString('\n')
				reply("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
				p, _ := r.ReadString('\n')
				ub, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(u))
				pb, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(p))
				user, pass = string(ub), string(pb)
				ok = user == s.User && pass == s.Password
			case "CRAM-MD5":
				challenge := "<12345.67890@smtptest>"
				reply("334 " + base64.StdEncoding.EncodeToString([]byte(challenge)))
				resp, _ := r.ReadString('\n')
				dec, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(resp))
				f := strings.SplitN(string(dec), " ", 2)
				if len(f) == 2 {
					h := hmac.New(md5.New, []byte(s.Password))
					h.Write([]byte(challenge))
					user = f[0]
					ok = user == s.User && f[1] == hex.EncodeToString(h.Sum(nil))
				}
			}
			if ok {
				authed = true
				cur.Auth, cur.User = mech, user
				reply("235 authenticated")
			} else {
				reply("535 authentication failed")
			}
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			if !authed {
				reply("530 authentication required")
				continue
			}
			cur.From = strings.Trim(strings.TrimSpace(line[10:]), "<>")
			if i := strings.Index(cur.From, " "); i >= 0 {
				cur.From = strings.Trim(cur.From[:i], "<>")
			}
			reply("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			addr := strings.Trim(strings.TrimSpace(line[8:]), "<>")
			if s.RejectRcpt != nil && s.RejectRcpt(addr) {
				reply("550 no such user")
				continue
			}
			cur.To = append(cur.To, addr)
			reply("250 ok")
		case cmd == "DATA":
			reply("354 go ahead")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" || l == ".\n" {
					break
				}
				if strings.HasPrefix(l, "..") {
					l = l[1:]
				}
				b.WriteString(l)
			}
			cur.Data = b.String()
			s.mu.Lock()
			s.messages = append(s.messages, cur)
			s.mu.Unlock()
			cur = Message{TLS: cur.TLS, Auth: cur.Auth, User: cur.User, HeloName: cur.HeloName}
			reply("250 queued")
		case cmd == "RSET":
			cur = Message{TLS: cur.TLS, Auth: cur.Auth, User: cur.User, HeloName: cur.HeloName}
			reply("250 ok")
		case cmd == "NOOP":
			reply("250 ok")
		case cmd == "QUIT":
			reply("221 bye")
			return
		default:
			reply("502 unknown command")
		}
	}
}

func selfSigned() (tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}
