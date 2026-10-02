package mail_test

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	netmail "net/mail"
	"strings"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/mail"
	"github.com/mikuta0407/buropher/internal/mail/smtptest"
)

func testMessage() *mail.Message {
	return &mail.Message{
		From:       "Redmine Admin <redmine@example.net>",
		To:         []string{"jsmith@somenet.foo"},
		Bcc:        []string{"hidden@example.net"},
		Subject:    "[eCookbook - Bug #1] (New) 日本語の題名",
		MessageID:  "redmine.issue-1.20260112120000.2@example.net",
		References: "<redmine.issue-1.20260112120000.2@example.net>",
		Headers:    []mail.Header{{"X-Redmine-Project", "ecookbook"}, {"X-Mailer", "Redmine"}},
		Text:       "本文\nline 2\n",
		HTML:       "<html><body><p>本文</p></body></html>\n",
		Date:       time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC),
	}
}

func TestSMTPSenderAuthMethods(t *testing.T) {
	for _, tc := range []struct {
		name, auth, want string
		starttls         bool
	}{
		{"plain", "plain", "PLAIN", false},
		{"login", "login", "LOGIN", false},
		{"cram_md5", "cram_md5", "CRAM-MD5", false},
		{"starttls_plain", "plain", "PLAIN", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := &smtptest.Server{User: "user", Password: "secret", StartTLS: tc.starttls}
			if err := srv.Start(); err != nil {
				t.Fatal(err)
			}
			defer srv.Close()
			host, port := srv.Addr()
			s := &mail.SMTPSender{Config: mail.SMTPConfig{Address: host, Port: port, UserName: "user", Password: "secret",
				Authentication: tc.auth, EnableStartTLSAuto: true, InsecureSkipVerify: true, Domain: "buropher.test"}}
			if err := s.Send(context.Background(), testMessage()); err != nil {
				t.Fatal(err)
			}
			ms := srv.Messages()
			if len(ms) != 1 {
				t.Fatalf("got %d messages", len(ms))
			}
			m := ms[0]
			if m.Auth != tc.want || m.TLS != tc.starttls || m.HeloName != "buropher.test" {
				t.Errorf("auth=%q tls=%v helo=%q", m.Auth, m.TLS, m.HeloName)
			}
			if m.From != "redmine@example.net" || strings.Join(m.To, ",") != "jsmith@somenet.foo,hidden@example.net" {
				t.Errorf("envelope from=%q to=%v", m.From, m.To)
			}
			checkParsed(t, m.Data)
		})
	}
}

func TestSMTPSenderAuthFailure(t *testing.T) {
	srv := &smtptest.Server{User: "user", Password: "secret"}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	host, port := srv.Addr()
	s := &mail.SMTPSender{Config: mail.SMTPConfig{Address: host, Port: port, UserName: "user", Password: "wrong"}}
	if err := s.Send(context.Background(), testMessage()); err == nil || !strings.Contains(err.Error(), "AUTH") {
		t.Fatalf("err = %v", err)
	}
}

func checkParsed(t *testing.T, data string) {
	t.Helper()
	pm, err := netmail.ReadMessage(strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	dec := new(mime.WordDecoder)
	subj, _ := dec.DecodeHeader(pm.Header.Get("Subject"))
	if subj != "[eCookbook - Bug #1] (New) 日本語の題名" {
		t.Errorf("subject = %q", subj)
	}
	if pm.Header.Get("Bcc") != "" {
		t.Error("Bcc header must not be sent")
	}
	if got := pm.Header.Get("Message-ID"); got != "<redmine.issue-1.20260112120000.2@example.net>" {
		t.Errorf("Message-ID = %q", got)
	}
	if got := pm.Header.Get("X-Redmine-Project"); got != "ecookbook" {
		t.Errorf("X-Redmine-Project = %q", got)
	}
	mt, params, err := mime.ParseMediaType(pm.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/alternative" {
		t.Fatalf("content-type %q %v", mt, err)
	}
	mr := multipart.NewReader(pm.Body, params["boundary"])
	var types []string
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(p) // multipart.Reader は quoted-printable を自動で復号する
		types = append(types, p.Header.Get("Content-Type"))
		if strings.HasPrefix(p.Header.Get("Content-Type"), "text/plain") && !strings.Contains(string(b), "本文") {
			if p.Header.Get("Content-Transfer-Encoding") != "base64" {
				t.Errorf("text part = %q", b)
			}
		}
	}
	if strings.Join(types, ",") != "text/plain; charset=UTF-8,text/html; charset=UTF-8" {
		t.Errorf("parts = %v", types)
	}
}
