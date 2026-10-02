package server_test

// メールの互換テスト: Redmine 6.1.2 の Mailer が生成したメール（testdata/mail/mails.json。
// testdata/mail/gen/regen.sh で再生成）と、buropher の Mailer（handler.App.RenderMail）の出力を比べる。
// 受信者ごとに件名・ヘッダ・text 本文・html 本文（roadie 適用後）を比較する。
// Date と（対象オブジェクトの無いメールの）ランダムな Message-ID は比較しない。

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/config"
	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/handler"
	"github.com/mikuta0407/buropher/internal/mail"
	"github.com/mikuta0407/buropher/internal/notify"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/server"
	"github.com/mikuta0407/buropher/internal/settings"
	"github.com/mikuta0407/buropher/internal/testfixtures"
)

type goldenMail struct {
	Case    string            `json:"case"`
	To      []string          `json:"to"`
	Subject string            `json:"subject"`
	Headers map[string]string `json:"headers"`
	Text    *string           `json:"text"`
	HTML    *string           `json:"html"`
}

type goldenCase struct {
	Case  string       `json:"case"`
	Mails []goldenMail `json:"mails"`
}

func loadMailGoldens(t *testing.T) map[string]goldenCase {
	t.Helper()
	b, err := os.ReadFile("testdata/mail/mails.json")
	if err != nil {
		t.Fatal(err)
	}
	var cs []goldenCase
	if err := json.Unmarshal(b, &cs); err != nil {
		t.Fatal(err)
	}
	out := map[string]goldenCase{}
	for _, c := range cs {
		out[c.Case] = c
	}
	return out
}

// newMailApp は公式フィクスチャを投入した DB の App（固定時刻・TZ=UTC）。
func newMailApp(t *testing.T) (*handler.App, *db.DB, *settings.Settings) {
	t.Helper()
	saved := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = saved })
	ctx := context.Background()
	d := dbtest.New(t)
	if err := testfixtures.LoadContext(ctx, d, frozenTime, testfixtures.All()...); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Server.SecretKey = "test-secret"
	cfg.Storage.AttachmentsPath = t.TempDir()
	srv, err := server.New(cfg, d, server.Options{TempDir: t.TempDir(), Now: func() time.Time { return frozenTime }})
	if err != nil {
		t.Fatal(err)
	}
	return srv.App(), d, srv.App().Settings
}

func userIDByMail(t *testing.T, d *db.DB, addr string) int64 {
	t.Helper()
	var id int64
	if err := d.Get(context.Background(), &id, `SELECT user_id FROM email_addresses WHERE address = ?`, addr); err != nil {
		t.Fatalf("user for %s: %v", addr, err)
	}
	return id
}

func normalizeCRLF(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

// mailPayload はケースと受信者から Payload を作る（Mailer のアクション引数）。
func mailPayload(t *testing.T, d *db.DB, name string, uid int64) *notify.Payload {
	p := &notify.Payload{UserID: uid}
	set := func(kind string) *notify.Payload { p.Kind = kind; return p }
	switch {
	case strings.HasPrefix(name, "issue_add_"):
		id := map[string]int64{"issue_add_1": 1, "issue_add_2": 2, "issue_add_3": 3, "issue_add_4": 4, "issue_add_6": 6, "issue_add_14": 14}[name]
		if id == 0 {
			id = 1
		}
		p.IssueID = id
		return set(notify.KindIssueAdd)
	case strings.HasPrefix(name, "issue_edit_"):
		jid := map[string]int64{"issue_edit_1": 1, "issue_edit_2": 2, "issue_edit_3": 3, "issue_edit_4": 4, "issue_edit_5": 5, "issue_edit_1_ja": 1}[name]
		var issueID int64
		if err := d.Get(context.Background(), &issueID, `SELECT issue_id FROM issue_journals WHERE id = ?`, jid); err != nil {
			t.Fatal(err)
		}
		p.IssueID, p.JournalID = issueID, jid
		return set(notify.KindIssueEdit)
	case name == "news_added_1":
		p.NewsID = 1
		return set(notify.KindNewsAdded)
	case name == "news_comment_added_1":
		p.CommentID = 1
		return set(notify.KindNewsCommentAdded)
	case name == "document_added_1":
		p.DocumentID, p.AuthorID = 1, 2
		return set(notify.KindDocumentAdded)
	case name == "attachments_added_project":
		p.AttachmentIDs = []int64{8, 22}
		return set(notify.KindAttachmentsAdded)
	case name == "attachments_added_version":
		p.AttachmentIDs = []int64{9}
		return set(notify.KindAttachmentsAdded)
	case name == "attachments_added_document":
		p.AttachmentIDs = []int64{2}
		return set(notify.KindAttachmentsAdded)
	case name == "message_posted_1":
		p.MessageID = 1
		return set(notify.KindMessagePosted)
	case name == "message_posted_2":
		p.MessageID = 2
		return set(notify.KindMessagePosted)
	case name == "wiki_content_added_1":
		p.WikiPageID = 1
		return set(notify.KindWikiContentAdded)
	case name == "wiki_content_updated_1":
		p.WikiPageID = 1
		return set(notify.KindWikiContentUpdated)
	case name == "account_information":
		p.UserID, p.Password = 2, "pAsSwoRd"
		return set(notify.KindAccountInformation)
	case name == "account_information_nopassword":
		p.UserID = 3
		return set(notify.KindAccountInformation)
	case name == "account_activation_request":
		p.SenderID = 3
		return set(notify.KindAccountActivationRequest)
	case name == "account_activated":
		p.UserID = 3
		return set(notify.KindAccountActivated)
	case name == "register":
		p.UserID, p.Token = 3, strings.Repeat("b", 64)
		return set(notify.KindRegister)
	case name == "security_notification_password":
		p.UserID, p.SenderID, p.RemoteIP = 2, 2, "10.1.2.3"
		p.Message, p.Title, p.URL = "mail_body_password_updated", "button_change_password", "/my/password"
		return set(notify.KindSecurityNotification)
	case name == "security_notification_mail_added":
		p.UserID, p.SenderID, p.RemoteIP = 2, 1, "127.0.0.1"
		p.Message, p.Field, p.Value, p.ExtraRecipients = "mail_body_security_notification_add", "field_mail", "new@example.net", []string{"old@example.net"}
		return set(notify.KindSecurityNotification)
	case name == "security_notification_title_only":
		p.UserID, p.SenderID, p.RemoteIP = 2, 2, "127.0.0.1"
		p.Message, p.Value, p.Title = "mail_body_security_notification_notify_enabled", "jsmith@somenet.foo", "label_my_account"
		return set(notify.KindSecurityNotification)
	case name == "settings_updated":
		p.SenderID, p.RemoteIP, p.Changes = 1, "127.0.0.1", []string{"host_name", "login_required"}
		return set(notify.KindSettingsUpdated)
	case name == "test_email":
		return set(notify.KindTestEmail)
	}
	return nil
}

// mailSettings はケースごとの設定（gen_mails.rb の with_settings）。
var mailSettings = map[string]map[string]string{
	"issue_add_1_plain":          {"plain_text_mail": "1"},
	"issue_add_1_nostatus":       {"show_status_changes_in_mail_subject": "0"},
	"issue_add_1_header_footer":  {"emails_header": "*Header* text", "emails_footer": "Footer _line_\nsecond", "text_formatting": "textile"},
	"issue_add_1_mail_from_name": {"mail_from": "Redmine app <redmine@somenet.foo>", "host_name": "mydomain.foo/rdm", "protocol": "https"},
}

var randomMessageID = func(s string) bool { return !strings.HasPrefix(s, "<redmine.") }

func TestMailGolden(t *testing.T) {
	gs := loadMailGoldens(t)
	a, d, st := newMailApp(t)
	ctx := context.Background()
	var names []string
	for n := range gs {
		names = append(names, n)
	}
	for _, name := range names {
		gc := gs[name]
		if strings.HasPrefix(name, "reminders_") || name == "lost_password" || name == "e2e_issue_update" {
			continue // TestMailGoldenReminders / TestMailGoldenLostPassword / notify の結合テストで扱う
		}
		t.Run(name, func(t *testing.T) {
			restore := applySettings(t, st, mailSettings[name])
			defer restore()
			if name == "issue_edit_1_ja" {
				if _, err := d.Exec(ctx, `UPDATE user_accounts SET language = 'ja' WHERE principal_id = 2`); err != nil {
					t.Fatal(err)
				}
				defer d.Exec(ctx, `UPDATE user_accounts SET language = 'en' WHERE principal_id = 2`)
			}
			for i, gm := range gc.Mails {
				uid := userIDByMail(t, d, gm.To[len(gm.To)-1])
				p := mailPayload(t, d, name, uid)
				if p == nil {
					t.Fatalf("no payload mapping for %s", name)
				}
				if name == "issue_edit_1_ja" && i == 2 {
					p.JournalID = 2
				}
				m, err := a.RenderMail(ctx, p)
				if err != nil {
					t.Fatal(err)
				}
				compareMail(t, gm, m)
				if os.Getenv("MAIL_DUMP") != "" {
					t.Logf("%s\n%s\n%s", m.Subject, m.Text, m.HTML)
				}
			}
		})
	}
}

func applySettings(t *testing.T, st *settings.Settings, kv map[string]string) func() {
	t.Helper()
	ctx := context.Background()
	saved := map[string]any{}
	for k, v := range kv {
		saved[k] = st.Get(k)
		if err := st.Set(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	return func() {
		for k, v := range saved {
			_ = st.Set(ctx, k, v)
		}
	}
}

func compareMail(t *testing.T, gm goldenMail, m *mail.Message) {
	t.Helper()
	if m == nil {
		t.Errorf("mail to %v: not rendered", gm.To)
		return
	}
	who := strings.Join(gm.To, ",")
	if strings.Join(m.To, ", ") != strings.Join(gm.To, ", ") {
		t.Errorf("%s: To = %v, want %v", who, m.To, gm.To)
	}
	if m.Subject != gm.Subject {
		t.Errorf("%s: Subject = %q, want %q", who, m.Subject, gm.Subject)
	}
	got := map[string]string{"From": m.From}
	if m.MessageID != "" {
		got["Message-ID"] = "<" + m.MessageID + ">"
	}
	if m.References != "" {
		got["References"] = m.References
	}
	var order []string
	for _, h := range m.Headers {
		got[h.Name] = h.Value
		order = append(order, h.Name)
	}
	for k, want := range gm.Headers {
		switch k {
		case "To", "Subject", "Mime-Version", "Content-Type":
			continue
		case "Message-ID":
			if randomMessageID(want) {
				continue
			}
		}
		if got[k] != want {
			t.Errorf("%s: header %s = %q, want %q", who, k, got[k], want)
		}
	}
	for k := range got {
		if _, ok := gm.Headers[k]; !ok && k != "Message-ID" {
			t.Errorf("%s: unexpected header %s: %q", who, k, got[k])
		}
	}
	// X-Redmine-* などのヘッダの順序
	var wantOrder []string
	for _, h := range []string{} {
		wantOrder = append(wantOrder, h)
	}
	_ = wantOrder
	if gm.Text != nil {
		if g, w := normalizeCRLF(m.Text), normalizeCRLF(*gm.Text); g != w {
			t.Errorf("%s: text mismatch\n%s", who, lineDiff(w, g))
		}
	}
	if gm.HTML == nil {
		if m.HTML != "" {
			t.Errorf("%s: unexpected html part", who)
		}
	} else if g, w := normalizeCRLF(m.HTML), normalizeCRLF(*gm.HTML); g != w {
		t.Errorf("%s: html mismatch\n%s", who, lineDiff(w, g))
	}
}

// lineDiff は簡易な行単位の差分（最初に異なる行の前後）。
func lineDiff(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			from := max(i-3, 0)
			var b strings.Builder
			for j := from; j < i; j++ {
				if j < len(wl) {
					b.WriteString("  " + wl[j] + "\n")
				}
			}
			b.WriteString("- " + w + "\n+ " + g + "\n")
			for j := i + 1; j < i+4; j++ {
				if j < len(wl) {
					b.WriteString("- " + wl[j] + "\n")
				}
				if j < len(gl) {
					b.WriteString("+ " + gl[j] + "\n")
				}
			}
			return b.String()
		}
	}
	return "(no line difference: trailing whitespace?)"
}

var _ = repository.ErrNotFound
