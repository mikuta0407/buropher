package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mikuta0407/buropher/internal/config"
	"github.com/mikuta0407/buropher/internal/mailhandler"
	"github.com/mikuta0407/buropher/internal/server"
)

// このファイルは buropher mail（rake redmine:email:read / receive_imap / receive_pop3 相当）。

const mailUsage = `usage: buropher mail receive [-config path] (-stdin | -imap | -pop3) [options] [name=value ...]

Read emails and create issues / replies (MailHandler).

  -stdin                   read one email from standard input (redmine:email:read)
  -imap                    read unseen emails from an IMAP server (redmine:email:receive_imap)
  -pop3                    read emails from a POP3 server (redmine:email:receive_pop3)

IMAP / POP3 options:
  -host=HOST               server host (default: 127.0.0.1)
  -port=PORT               server port (default: IMAP 143, POP3 110 / 995 with ssl)
  -ssl=SSL                 use SSL/TLS (ssl=force disables certificate verification)
  -starttls=1              use STARTTLS (IMAP)
  -username=USERNAME       account
  -password=PASSWORD       password
  -folder=FOLDER           IMAP folder to read (default: INBOX)
  -move_on_success=MAILBOX move emails that were successfully received to MAILBOX instead of deleting them (IMAP)
  -move_on_failure=MAILBOX move emails that were ignored to MAILBOX (IMAP)
  -apop=1                  use APOP authentication (POP3)
  -delete_unprocessed=1    delete messages that could not be processed from the server (POP3)

User and permissions options:
  -unknown_user=ACTION     how to handle emails from an unknown user (ignore | accept | create)
  -no_permission_check=1   disable permission checking when receiving the email
  -no_account_notice=1     disable new user account notification
  -no_notification=1       disable email notification to new user
  -default_group=foo,bar   adds created user to foo and bar groups

Issue attributes control options:
  -project=PROJECT         identifier of the target project
  -project_from_subaddress=ADDR  select project from subaddress of ADDR found in To, Cc, Bcc headers
  -status=STATUS -tracker=TRACKER -category=CATEGORY -priority=PRIORITY
  -assigned_to=ASSIGNEE -fixed_version=VERSION
  -private                 create new issues as private
  -allow_override=ATTRS    allow email content to set attributes values (comma separated list or 'all')

Options can also be given in the rake form name=value (e.g. unknown_user=create project=foo).
`

// mailOptionNames は rake の環境変数として受け付ける名前。
var mailOptionNames = []string{"host", "port", "ssl", "starttls", "username", "password", "folder", "move_on_success",
	"move_on_failure", "apop", "delete_unprocessed", "unknown_user", "no_permission_check", "no_account_notice",
	"no_notification", "default_group", "project", "project_from_subaddress", "status", "tracker", "category",
	"priority", "assigned_to", "fixed_version", "allow_override"}

func mailCmd(args []string) error {
	if len(args) == 0 || args[0] != "receive" {
		fmt.Fprint(os.Stderr, mailUsage)
		return fmt.Errorf("unknown mail command")
	}
	fs := flag.NewFlagSet("mail receive", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, mailUsage) }
	cfgPath := fs.String("config", "", "path to config.toml")
	stdin := fs.Bool("stdin", false, "read one email from standard input")
	useIMAP := fs.Bool("imap", false, "read emails from an IMAP server")
	usePOP3 := fs.Bool("pop3", false, "read emails from a POP3 server")
	private := fs.Bool("private", false, "create new issues as private")
	vals := map[string]*string{}
	for _, n := range mailOptionNames {
		vals[n] = fs.String(n, "", n)
	}
	fs.Parse(args[1:])
	env := map[string]string{}
	fs.Visit(func(f *flag.Flag) {
		if p, ok := vals[f.Name]; ok {
			env[f.Name] = *p
		}
	})
	if *private {
		env["private"] = "1"
	}
	// rake 形式の name=value
	for _, a := range fs.Args() {
		k, v, ok := strings.Cut(a, "=")
		if !ok {
			if a == "private" {
				env["private"] = "1"
				continue
			}
			return fmt.Errorf("unexpected argument %q", a)
		}
		env[k] = v
	}
	modes := 0
	for _, b := range []bool{*stdin, *useIMAP, *usePOP3} {
		if b {
			modes++
		}
	}
	if modes != 1 {
		fs.Usage()
		return fmt.Errorf("specify exactly one of -stdin, -imap, -pop3")
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	ctx := context.Background()
	d, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := server.CheckInitialized(ctx, d); err != nil {
		return err
	}
	srv, err := server.New(cfg, d, server.Options{Version: version})
	if err != nil {
		return err
	}
	h := srv.App().MailHandler()
	opts := mailhandler.OptionsFromMap(env)
	switch {
	case *stdin:
		raw, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		// rake redmine:email:read は結果を出力しない（失敗はログのみ）
		h.SafeReceive(ctx, raw, opts)
		return nil
	case *useIMAP:
		return h.CheckOnce(ctx, mailhandler.PollConfig{Protocol: "imap", IMAP: imapOptionsFromMap(env), Options: opts})
	default:
		return h.CheckOnce(ctx, mailhandler.PollConfig{Protocol: "pop3", POP3: pop3OptionsFromMap(env), Options: opts})
	}
}

func imapOptionsFromMap(m map[string]string) mailhandler.IMAPOptions {
	_, starttls := m["starttls"]
	return mailhandler.IMAPOptions{Host: m["host"], Port: m["port"], SSL: m["ssl"], StartTLS: starttls,
		Username: m["username"], Password: m["password"], Folder: m["folder"],
		MoveOnSuccess: m["move_on_success"], MoveOnFailure: m["move_on_failure"]}
}

func pop3OptionsFromMap(m map[string]string) mailhandler.POP3Options {
	return mailhandler.POP3Options{Host: m["host"], Port: m["port"], SSL: m["ssl"], APOP: m["apop"] == "1",
		Username: m["username"], Password: m["password"], DeleteUnprocessed: m["delete_unprocessed"] == "1"}
}

// mailPollConfig は config の [mail_receive] を定期受信の設定にする（Protocol が空なら ok = false）。
func mailPollConfig(c config.MailReceive) (mailhandler.PollConfig, bool) {
	if c.Protocol == "" {
		return mailhandler.PollConfig{}, false
	}
	return mailhandler.PollConfig{
		Protocol: strings.ToLower(c.Protocol),
		Interval: time.Duration(c.Interval) * time.Second,
		IMAP: mailhandler.IMAPOptions{Host: c.Host, Port: c.Port, SSL: c.SSL, StartTLS: c.StartTLS, Username: c.Username,
			Password: c.Password, Folder: c.Folder, MoveOnSuccess: c.MoveOnSuccess, MoveOnFailure: c.MoveOnFailure},
		POP3: mailhandler.POP3Options{Host: c.Host, Port: c.Port, SSL: c.SSL, APOP: c.APOP, Username: c.Username,
			Password: c.Password, DeleteUnprocessed: c.DeleteUnprocessed},
		Options: mailhandler.OptionsFromMap(c.Options),
	}, true
}
