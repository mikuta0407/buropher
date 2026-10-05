// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package mailhandler

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// 境界だけの短いパートを大量に並べたメールでも、解析するパートの数は maxParts までに抑える。
func TestParseManyPartsBounded(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("Content-Type: multipart/mixed; boundary=b\r\n\r\n")
	for range 50000 {
		sb.WriteString("--b\r\nContent-Disposition: attachment; filename=x\r\n\r\nx\r\n")
	}
	sb.WriteString("--b--\r\n")
	p := Parse([]byte(sb.String()))
	if n := len(p.AllParts()); n > maxParts {
		t.Fatalf("parsed %d parts, want at most %d", n, maxParts)
	}
	if n := len(p.Attachments()); n > maxParts {
		t.Fatalf("%d attachments", n)
	}
	// 入れ子の multipart でも全体で数える
	sb.Reset()
	sb.WriteString("Content-Type: multipart/mixed; boundary=a\r\n\r\n")
	for range 200 {
		sb.WriteString("--a\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n")
		for range 200 {
			sb.WriteString("--b\r\nContent-Disposition: attachment; filename=x\r\n\r\nx\r\n")
		}
		sb.WriteString("--b--\r\n")
	}
	sb.WriteString("--a--\r\n")
	if n := len(Parse([]byte(sb.String())).AllParts()); n > maxParts {
		t.Fatalf("nested: parsed %d parts, want at most %d", n, maxParts)
	}
	// 通常のメールは従来どおり
	raw := "Content-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\nContent-Type: text/plain\r\n\r\nhello\r\n" +
		"--b\r\nContent-Disposition: attachment; filename=a.txt\r\n\r\na\r\n--b\r\nContent-Disposition: attachment; filename=b.txt\r\n\r\nb\r\n--b--\r\n"
	if atts := Parse([]byte(raw)).Attachments(); len(atts) != 2 || atts[1].Filename() != "b.txt" {
		t.Fatalf("normal mail: %d attachments", len(atts))
	}
}

// setMaxMessageBytes はテストの間だけ MaxMessageBytes を変える。
func setMaxMessageBytes(t *testing.T, n int64) {
	old := MaxMessageBytes
	MaxMessageBytes = n
	t.Cleanup(func() { MaxMessageBytes = old })
}

// POP3 で大きすぎるメールは読まずに「処理できなかった」ものとして扱う（delete_unprocessed なら削除する）。
func TestCheckPOP3SkipsOversizedMessage(t *testing.T) {
	setMaxMessageBytes(t, 1000)
	for _, deleteUnprocessed := range []bool{false, true} {
		srv := &fakePOP3{messages: []string{
			"Message-ID: <a@x>\r\nSubject: big\r\n\r\n" + strings.Repeat("x", 2000) + "\r\n",
			"Message-ID: <b@x>\r\nSubject: ok\r\n\r\nbody\r\n",
		}, deleted: map[int]bool{}}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go srv.serve(t, ln)
		host, port, _ := net.SplitHostPort(ln.Addr().String())
		var subjects []string
		receive := func(_ context.Context, raw []byte) bool {
			subjects = append(subjects, Parse(raw).Subject())
			return true
		}
		err = CheckPOP3(context.Background(), POP3Options{Host: host, Port: port, Username: "u", Password: "p",
			DeleteUnprocessed: deleteUnprocessed}, receive, nil)
		ln.Close()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(subjects, ",") != "ok" {
			t.Errorf("received = %v", subjects)
		}
		for _, c := range srv.commands {
			if c == "RETR 1" {
				t.Errorf("oversized message was retrieved")
			}
		}
		if srv.deleted[1] != deleteUnprocessed || !srv.deleted[2] {
			t.Errorf("delete_unprocessed=%v: deleted = %v", deleteUnprocessed, srv.deleted)
		}
	}
}

// IMAP で大きすぎるメールは読まずに「処理できなかった」ものとして move_on_failure へ移す。
func TestCheckIMAPSkipsOversizedMessage(t *testing.T) {
	setMaxMessageBytes(t, 1000)
	mem := imapmemserver.New()
	user := imapmemserver.NewUser("redmine", "secret")
	mem.AddUser(user)
	for _, name := range []string{"INBOX", "Failed"} {
		if err := user.Create(name, nil); err != nil {
			t.Fatal(err)
		}
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
	c, err := imapclient.DialInsecure(ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Login("redmine", "secret").Wait(); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"Subject: big\r\n\r\n" + strings.Repeat("x", 2000) + "\r\n", "Subject: ok\r\n\r\nbody\r\n"} {
		cmd := c.Append("INBOX", int64(len(body)), nil)
		if _, err := cmd.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	var subjects []string
	receive := func(_ context.Context, raw []byte) bool {
		subjects = append(subjects, Parse(raw).Subject())
		return true
	}
	if err := CheckIMAP(context.Background(), IMAPOptions{Host: host, Port: port, Username: "redmine", Password: "secret",
		MoveOnFailure: "Failed"}, receive, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Join(subjects, ",") != "ok" {
		t.Errorf("received = %v", subjects)
	}
	st, err := c.Status("Failed", &imap.StatusOptions{NumMessages: true}).Wait()
	if err != nil {
		t.Fatal(err)
	}
	if *st.NumMessages != 1 {
		t.Errorf("Failed = %d", *st.NumMessages)
	}
}
