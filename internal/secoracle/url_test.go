// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package secoracle

import "testing"

// オラクル自体の確認: ブラウザが別オリジンへ解決するものは検出し、同一オリジンに留まるものは許す。
func TestSameOriginPath(t *testing.T) {
	const page = "http://test.host/projects/x"
	for _, bad := range []string{"//evil.com", "/\\evil.com", "/\t/evil.com", "/\n/evil.com", "\\\\evil.com",
		"http://evil.com/", "https://test.host/", "javascript:alert(1)", " //evil.com", "\x01//evil.com", "http://test.host@evil.com/"} {
		if err := SameOriginPath(page, bad); err == nil {
			t.Errorf("SameOriginPath(%q) = nil, want error", bad)
		}
	}
	for _, ok := range []string{"/issues", "/%", "/\x7f", "/a b", "/%2f%2fevil.com", "?x=//evil", "#//evil", "http://test.host/x", "/x\\y"} {
		if err := SameOriginPath(page, ok); err != nil {
			t.Errorf("SameOriginPath(%q) = %v, want nil", ok, err)
		}
	}
}

func TestURLScheme(t *testing.T) {
	for in, want := range map[string]string{
		"javascript:x": "javascript", " JaVa\tScript:x": "javascript", "\x00\x01data:x": "data",
		"/x:y": "", "x/y:z": "", "mailto:a": "mailto", "1abc:x": "",
	} {
		if got := URLScheme(in); got != want {
			t.Errorf("URLScheme(%q) = %q, want %q", in, got, want)
		}
	}
}
