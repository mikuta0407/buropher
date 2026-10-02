package mailhandler

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// TestCheckIMAP は Redmine::IMAP.check（受信できたものは削除、できなかったものは既読にして move_on_failure へ移す）。
func TestCheckIMAP(t *testing.T) {
	mem := imapmemserver.New()
	user := imapmemserver.NewUser("redmine", "secret")
	mem.AddUser(user)
	for _, name := range []string{"INBOX", "Failed", "Done"} {
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

	// メールを 3 通入れる（2 通目は既読なので対象外）
	c, err := imapclient.DialInsecure(ln.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Login("redmine", "secret").Wait(); err != nil {
		t.Fatal(err)
	}
	for i, body := range []string{"Subject: ok\r\n\r\nbody\r\n", "Subject: seen\r\n\r\nbody\r\n", "Subject: ng\r\n\r\nbody\r\n"} {
		var opts *imap.AppendOptions
		if i == 1 {
			opts = &imap.AppendOptions{Flags: []imap.Flag{imap.FlagSeen}}
		}
		cmd := c.Append("INBOX", int64(len(body)), opts)
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
		s := Parse(raw).Subject()
		subjects = append(subjects, s)
		return s == "ok"
	}
	err = CheckIMAP(context.Background(), IMAPOptions{Host: host, Port: port, Username: "redmine", Password: "secret",
		MoveOnSuccess: "Done", MoveOnFailure: "Failed"}, receive, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(subjects, ",") != "ok,ng" {
		t.Errorf("received = %v", subjects)
	}
	count := func(mbox string) uint32 {
		st, err := c.Status(mbox, &imap.StatusOptions{NumMessages: true}).Wait()
		if err != nil {
			t.Fatal(err)
		}
		return *st.NumMessages
	}
	if n := count("INBOX"); n != 1 {
		t.Errorf("INBOX = %d", n)
	}
	if n := count("Done"); n != 1 {
		t.Errorf("Done = %d", n)
	}
	if n := count("Failed"); n != 1 {
		t.Errorf("Failed = %d", n)
	}
	c.Logout().Wait()
	c.Close()
}

// fakePOP3 はテスト用の POP3 サーバー。
type fakePOP3 struct {
	mu       sync.Mutex
	messages []string
	deleted  map[int]bool
	apop     bool
	commands []string
}

func (s *fakePOP3) serve(t *testing.T, ln net.Listener) {
	conn, err := ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := func(format string, args ...any) { fmt.Fprintf(conn, format+"\r\n", args...) }
	w("+OK POP3 ready <1896.697170952@dbc.mtview.ca.us>")
	pending := map[int]bool{}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		s.mu.Lock()
		s.commands = append(s.commands, line)
		s.mu.Unlock()
		f := strings.Fields(line)
		switch strings.ToUpper(f[0]) {
		case "USER", "PASS":
			w("+OK")
		case "APOP":
			// RFC 1939 の例: パスワード "tanstaaf"
			if f[2] == "c4c9334bac560ecc979e58001b3e22fb" {
				w("+OK")
			} else {
				w("-ERR bad digest")
			}
		case "LIST":
			w("+OK")
			for i, m := range s.messages {
				w("%d %d", i+1, len(m))
			}
			w(".")
		case "RETR":
			var n int
			fmt.Sscan(f[1], &n)
			w("+OK")
			for _, l := range strings.Split(strings.TrimRight(s.messages[n-1], "\r\n"), "\r\n") {
				if strings.HasPrefix(l, ".") {
					l = "." + l
				}
				w("%s", l)
			}
			w(".")
		case "DELE":
			var n int
			fmt.Sscan(f[1], &n)
			pending[n] = true
			w("+OK")
		case "QUIT":
			s.mu.Lock()
			for k := range pending {
				s.deleted[k] = true
			}
			s.mu.Unlock()
			w("+OK bye")
			return
		default:
			w("-ERR")
		}
	}
}

func TestCheckPOP3(t *testing.T) {
	for _, deleteUnprocessed := range []bool{false, true} {
		srv := &fakePOP3{messages: []string{
			"Message-ID: <a@x>\r\nSubject: ok\r\n\r\n.leading dot\r\n",
			"Message-ID: <b@x>\r\nSubject: ng\r\n\r\nbody\r\n",
		}, deleted: map[int]bool{}}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go srv.serve(t, ln)
		host, port, _ := net.SplitHostPort(ln.Addr().String())
		var bodies []string
		receive := func(_ context.Context, raw []byte) bool {
			p := Parse(raw)
			bodies = append(bodies, string(p.DecodedBody()))
			return p.Subject() == "ok"
		}
		err = CheckPOP3(context.Background(), POP3Options{Host: host, Port: port, Username: "u", Password: "p",
			DeleteUnprocessed: deleteUnprocessed}, receive, nil)
		ln.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(bodies) != 2 || bodies[0] != ".leading dot\n" {
			t.Errorf("bodies = %q", bodies)
		}
		if !srv.deleted[1] || srv.deleted[2] != deleteUnprocessed {
			t.Errorf("delete_unprocessed=%v: deleted = %v", deleteUnprocessed, srv.deleted)
		}
	}
}

func TestCheckPOP3APOP(t *testing.T) {
	srv := &fakePOP3{deleted: map[int]bool{}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go srv.serve(t, ln)
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	err = CheckPOP3(context.Background(), POP3Options{Host: host, Port: port, Username: "mrose", Password: "tanstaaf", APOP: true},
		func(context.Context, []byte) bool { return true }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(srv.commands) == 0 || srv.commands[0] != "APOP mrose c4c9334bac560ecc979e58001b3e22fb" {
		t.Errorf("commands = %v", srv.commands)
	}
}
