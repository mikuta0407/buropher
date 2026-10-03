// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package urlroot

import "testing"

func TestPath(t *testing.T) {
	t.Cleanup(func() { Set("") })

	Set("")
	if got := Path("/issues/1"); got != "/issues/1" {
		t.Fatalf("ルート配置では変更しない: %q", got)
	}

	Set("redmine/")
	if Get() != "/redmine" {
		t.Fatalf("正規化: %q", Get())
	}
	cases := map[string]string{
		"/issues/1":          "/redmine/issues/1",
		"/":                  "/redmine/",
		"/redmine/issues/1":  "/redmine/issues/1", // 冪等
		"/redmine":           "/redmine",
		"/redmine?x=1":       "/redmine?x=1",
		"/redmineX/issues":   "/redmine/redmineX/issues",
		"//example.com/x":    "//example.com/x",
		"http://example.com": "http://example.com",
		"#note-1":            "#note-1",
		"issues/1":           "issues/1",
		"":                   "",
	}
	for in, want := range cases {
		if got := Path(in); got != want {
			t.Errorf("Path(%q) = %q, want %q", in, got, want)
		}
	}

	for in, want := range map[string]string{"/redmine": "/", "/redmine/issues": "/issues"} {
		if got, ok := Strip(in); !ok || got != want {
			t.Errorf("Strip(%q) = %q, %v", in, got, ok)
		}
	}
	if _, ok := Strip("/issues"); ok {
		t.Error("ルート外のパスは ok=false")
	}
}
