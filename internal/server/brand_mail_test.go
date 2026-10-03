// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/handler"
	"github.com/mikuta0407/buropher/internal/notify"
)

// buropher 拡張: 送信メールの X-Buropher-* ヘッダ（mail.redmine_compat_headers）と Message-ID の接頭辞
// （mail.message_id_prefix）。既定の動作（X-Redmine-* の直後に X-Buropher-*、接頭辞 redmine）は TestMailGolden で確認する。
func TestMailBrandOptions(t *testing.T) {
	a, d, _ := newMailApp(t)
	ctx := context.Background()
	uid := userIDByMail(t, d, "jsmith@somenet.foo")
	render := func() map[string]string {
		m, err := a.RenderMail(ctx, &notify.Payload{Kind: notify.KindIssueAdd, IssueID: 1, UserID: uid})
		if err != nil || m == nil {
			t.Fatalf("render: %v", err)
		}
		h := map[string]string{"Message-ID": m.MessageID}
		for _, x := range m.Headers {
			h[x.Name] = x.Value
		}
		return h
	}

	h := render()
	if h["X-Redmine-Issue-Id"] != "1" || h["X-Buropher-Issue-Id"] != "1" || !strings.HasPrefix(h["Message-ID"], "redmine.issue-1.") {
		t.Errorf("default headers: %v", h)
	}

	a.MailOmitRedmineHeaders = true
	a.MessageIDPrefix = "buropher"
	t.Cleanup(func() { a.MailOmitRedmineHeaders, a.MessageIDPrefix = false, "" })
	h = render()
	for k := range h {
		if strings.HasPrefix(k, "X-Redmine-") {
			t.Errorf("unexpected %s", k)
		}
	}
	for _, k := range []string{"X-Buropher-Issue-Id", "X-Buropher-Project", "X-Buropher-Host", "X-Buropher-Site", "X-Buropher-Sender"} {
		if h[k] == "" {
			t.Errorf("missing %s: %v", k, h)
		}
	}
	if !strings.HasPrefix(h["Message-ID"], "buropher.issue-1.") {
		t.Errorf("Message-ID = %q", h["Message-ID"])
	}
	if h["List-Id"] != "<ecookbook.redmine.example.net>" {
		t.Errorf("List-Id = %q", h["List-Id"])
	}
}

// server.auth_realm は WWW-Authenticate の realm を変える（既定は Redmine と同じ "Redmine"）。
func TestAuthRealmOption(t *testing.T) {
	ts, _ := newFixtureServer(t)
	res := apiGet(t, ts, "/users/current.xml")
	if got := res.Header.Get("WWW-Authenticate"); got != `Basic realm="Redmine API"` {
		t.Errorf("default realm: %q", got)
	}
	ts2, _ := newFixtureServer(t, func(a *handler.App, _ chi.Router) { a.AuthRealm = "Buropher" })
	res = apiGet(t, ts2, "/users/current.xml")
	if got := res.Header.Get("WWW-Authenticate"); got != `Basic realm="Buropher API"` {
		t.Errorf("configured realm: %q (status %d)", got, res.Status)
	}
}
