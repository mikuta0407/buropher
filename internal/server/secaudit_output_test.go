// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"net/url"
	"strings"
	"testing"
)

// TestEditAllCancelBackURLSanitized は attachments#edit_all の Cancel リンクが
// back_url の危険なスキーム（javascript: 等）を拒否し、正当な相対 URL は保持することを確認する。
func TestEditAllCancelBackURLSanitized(t *testing.T) {
	ts, _ := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")

	// javascript: back_url → Cancel リンクは出力されない
	_, body := get(t, c, ts.URL+"/attachments/issues/2/edit?back_url="+url.QueryEscape("javascript:alert(1)"))
	if strings.Contains(body, "javascript:alert(1)") {
		t.Errorf("javascript: back_url leaked into edit_all cancel link")
	}

	// 正当な相対 back_url → Cancel リンクが出る
	_, body = get(t, c, ts.URL+"/attachments/issues/2/edit?back_url="+url.QueryEscape("/issues/2"))
	if !strings.Contains(body, `href="/issues/2"`) {
		t.Errorf("valid back_url not rendered as cancel link")
	}
}

// TestUsersCSVDispositionSanitized は /users.csv の Content-Disposition が
// query_name の " やセミコロンで細工されないことを確認する。
func TestUsersCSVDispositionSanitized(t *testing.T) {
	ts, _ := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")

	res, _ := get(t, c, ts.URL+"/users.csv?query_name="+url.QueryEscape(`x"; filename*=UTF-8''evil.html; a="`))
	cd := res.Header.Get("Content-Disposition")
	if strings.Contains(cd, "evil.html") {
		t.Errorf("injected filename leaked into Content-Disposition: %q", cd)
	}
	// 二重引用符がそのまま値に入っていない（細工による属性追加がない）
	if strings.Count(cd, `filename="`) != 1 {
		t.Errorf("unexpected Content-Disposition structure: %q", cd)
	}
}
