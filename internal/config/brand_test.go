// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package config

import "testing"

func TestMailBrandOptions(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Mail.SendRedmineHeaders() || c.Mail.MessageIDPrefix != "" || c.Server.AuthRealm != "" {
		t.Errorf("defaults: %+v %+v", c.Mail, c.Server)
	}
	t.Setenv("BUROPHER_MAIL_REDMINE_HEADERS", "false")
	t.Setenv("BUROPHER_MAIL_MESSAGE_ID_PREFIX", "buropher")
	t.Setenv("BUROPHER_AUTH_REALM", "Buropher")
	c, err = Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Mail.SendRedmineHeaders() || c.Mail.MessageIDPrefix != "buropher" || c.Server.AuthRealm != "Buropher" {
		t.Errorf("env: %+v %+v", c.Mail, c.Server)
	}
	t.Setenv("BUROPHER_MAIL_MESSAGE_ID_PREFIX", "other")
	if _, err := Load(""); err == nil {
		t.Error("invalid message_id_prefix accepted")
	}
}
