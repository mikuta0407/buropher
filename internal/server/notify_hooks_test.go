package server_test

// 画面の操作から通知が送られること（コントローラ・モデルのコールバックの移植箇所）を確かめる。

import (
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/mail"
)

func subjectsTo(ms []*mail.Message) []string {
	var out []string
	for _, m := range ms {
		out = append(out, strings.Join(m.To, ",")+" "+m.Subject)
	}
	slices.Sort(out)
	return out
}

func TestNotifyHooksFromActions(t *testing.T) {
	e := newNotifyEnv(t)
	ts := newTestHTTP(t, e.srv)

	t.Run("settings_updated", func(t *testing.T) {
		e.sender.Clear()
		c := login(t, ts, "admin", "admin")
		_, body := get(t, c, ts.URL+"/settings?tab=authentication")
		post(t, c, ts.URL+"/settings/edit?tab=authentication", url.Values{"authenticity_token": {csrfToken(t, body)},
			"settings[login_required]": {"1"}})
		e.run()
		got := subjectsTo(e.sender.Messages())
		if !slices.Equal(got, []string{"admin@somenet.foo [Buropher] Security notification"}) {
			t.Fatalf("mails = %v", got)
		}
		if !strings.Contains(e.sender.Messages()[0].Text, "* Authentication required") {
			t.Errorf("text = %s", e.sender.Messages()[0].Text)
		}
		e.set("login_required", "0")
	})

	t.Run("email_address_added", func(t *testing.T) {
		e.sender.Clear()
		c := login(t, ts, "jsmith", "jsmith")
		_, body := get(t, c, ts.URL+"/my/account")
		post(t, c, ts.URL+"/users/2/email_addresses", url.Values{"authenticity_token": {csrfToken(t, body)},
			"email_address[address]": {"jsmith-new@example.net"}})
		e.run()
		ms := e.sender.Messages()
		if len(ms) != 1 || !strings.Contains(ms[0].Text, "Email jsmith-new@example.net was added.") || ms[0].Header("X-Redmine-Url") != "http://localhost:3000/my/account" {
			t.Fatalf("mails = %v", subjectsTo(ms))
		}
	})

	t.Run("password_updated", func(t *testing.T) {
		e.sender.Clear()
		c := login(t, ts, "jsmith", "jsmith")
		_, body := get(t, c, ts.URL+"/my/password")
		post(t, c, ts.URL+"/my/password", url.Values{"authenticity_token": {csrfToken(t, body)},
			"password": {"jsmith"}, "new_password": {"newpassword1"}, "new_password_confirmation": {"newpassword1"}})
		e.run()
		ms := e.sender.Messages()
		if len(ms) == 0 || !strings.Contains(ms[0].Text, "Your password has been changed.") {
			t.Fatalf("mails = %v", subjectsTo(ms))
		}
		// 通知の宛先は jsmith のアドレス（追加したアドレスも notify なら含む）
		if !slices.Contains(ms[0].To, "jsmith@somenet.foo") {
			t.Errorf("to = %v", ms[0].To)
		}
	})

	t.Run("account_information_and_admin", func(t *testing.T) {
		e.sender.Clear()
		c := login(t, ts, "admin", "admin")
		_, body := get(t, c, ts.URL+"/users/new")
		post(t, c, ts.URL+"/users", url.Values{"authenticity_token": {csrfToken(t, body)},
			"user[login]": {"newadmin"}, "user[firstname]": {"New"}, "user[lastname]": {"Admin"},
			"user[mail]": {"newadmin@example.net"}, "user[password]": {"secret123"}, "user[password_confirmation]": {"secret123"},
			"user[admin]": {"1"}, "send_information": {"1"}})
		e.run()
		got := subjectsTo(e.sender.Messages())
		want := []string{
			"admin@somenet.foo [Buropher] Security notification",
			"newadmin@example.net Your Buropher account activation",
		}
		for _, w := range want {
			if !slices.Contains(got, w) {
				t.Errorf("missing %q in %v", w, got)
			}
		}
		for _, m := range e.sender.Messages() {
			if m.Subject == "Your Buropher account activation" && !strings.Contains(m.Text, "* Password: secret123") {
				t.Errorf("account information text = %s", m.Text)
			}
		}
	})
}
