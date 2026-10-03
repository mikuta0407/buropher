// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestVersionsXHR は status_by.js（XHR）の応答を確認する。
func TestVersionsXHR(t *testing.T) {
	ts, _ := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")
	_, body := get(t, c, ts.URL+"/versions/2")
	form := url.Values{"status_by": {"assigned_to"}, "authenticity_token": {csrfToken(t, body)}}
	req, _ := http.NewRequest("POST", ts.URL+"/versions/2/status_by.js", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b := new(strings.Builder)
	_, _ = io.Copy(b, res.Body)
	if res.StatusCode != 200 || !strings.HasPrefix(b.String(), "$('#status_by').html('<form id=\\\"status_by_form\\\"") ||
		!strings.Contains(b.String(), "Dave Lopper") {
		t.Fatalf("status_by.js: %d %s", res.StatusCode, b.String())
	}
}
