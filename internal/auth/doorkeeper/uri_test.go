// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package doorkeeper

import (
	"slices"
	"testing"
)

// 期待値は Ruby 3.3 の URI.parse と Doorkeeper 5.8.2 の RedirectUriValidator / URIChecker（Redmine の
// force_ssl_in_redirect_uri / forbid_redirect_uri の設定）を同じ入力で実行した結果。

func TestValidateRedirectURI(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"http://example.com/cb", []string{"secured_uri"}},
		{"https://example.com/cb", nil},
		{"http://127.0.0.1:12345/cb", nil},
		{"http://localhost:3000/cb", nil},
		{"http://LOCALHOST/cb", []string{"secured_uri"}},
		{"foo", []string{"relative_uri"}},
		{"/relative", []string{"relative_uri"}},
		{"javascript:alert(1)", []string{"forbidden_uri", "unspecified_scheme"}},
		{"JavaScript:x", []string{"forbidden_uri", "unspecified_scheme"}},
		{"data:text/html,x", []string{"forbidden_uri", "unspecified_scheme"}},
		{"http://x.com/#frag", []string{"fragment_present", "secured_uri"}},
		{"https://x.com/#", []string{"fragment_present"}},
		{"localhost:3000", []string{"unspecified_scheme"}},
		{"http://", []string{"secured_uri", "invalid_uri"}},
		{"https://", []string{"invalid_uri"}},
		{"http:foo", []string{"unspecified_scheme", "secured_uri", "invalid_uri"}},
		{"myapp://callback", nil},
		{"myapp:callback", []string{"unspecified_scheme"}},
		{"urn:ietf:wg:oauth:2.0:oob", nil},
		{"urn:ietf:wg:oauth:2.0:oob:auto", nil},
		{"urn:x:y", []string{"unspecified_scheme"}},
		{"http://exa mple.com", []string{"secured_uri", "relative_uri"}},
		{"https://ok.example.com/a?b=c", nil},
		{"http://web/cb", nil},
		{"http://[::1]:8080/cb", []string{"secured_uri"}},
		{"https://例え.jp/", []string{"invalid_uri"}},
		{"https://ok.example.com/\nhttp://bad.example.com/", []string{"secured_uri"}},
		{"  ", []string{"blank"}},
		{"", []string{"blank"}},
		{"com.example.app:/oauth2redirect", nil},
		{"HTTPS://Example.COM/cb", nil},
		{"http://user:pw@127.0.0.1/cb", nil},
		{"https://a.example/%zz", []string{"invalid_uri"}},
		{"https://a.example/%41", nil},
		{"ftp://files.example.com/", nil},
	}
	for _, c := range cases {
		if got := ValidateRedirectURI(c.in); !slices.Equal(got, c.want) {
			t.Errorf("ValidateRedirectURI(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestValidForAuthorization(t *testing.T) {
	cases := []struct {
		url, client string
		want        bool
	}{
		{"http://127.0.0.1:9999/cb", "http://127.0.0.1:12345/cb", true},
		{"http://127.0.0.1/cb", "http://127.0.0.1:12345/cb", true},
		{"http://localhost:9999/cb", "http://localhost:12345/cb", false},
		{"https://client.example.com/cb?x=1", "https://client.example.com/cb?x=1", true},
		{"https://client.example.com/cb?x=2", "https://client.example.com/cb?x=1", false},
		{"https://client.example.com/cb?a=1&b=2", "https://client.example.com/cb?b=2&a=1", true},
		{"https://client.example.com/cb?x=1", "https://client.example.com/cb", true},
		{"https://client.example.com/cb", "https://client.example.com/cb?x=1", false},
		{"https://CLIENT.example.com/cb", "https://client.example.com/cb", true},
		{"https://client.example.com", "https://client.example.com/", true},
		{"https://client.example.com:443/cb", "https://client.example.com/cb", true},
		{"http://evil.example/cb", "http://127.0.0.1:12345/cb", false},
		{"urn:ietf:wg:oauth:2.0:oob", "http://a.example/\nurn:ietf:wg:oauth:2.0:oob", true},
		{"urn:ietf:wg:oauth:2.0:oob", "http://a.example/", false},
		{"https://client.example.com/cb#x", "https://client.example.com/cb#x", false},
		{"myapp://callback", "myapp://callback", true},
		{"myapp:callback", "myapp:callback", false},
		{"http://[::1]:1/cb", "http://[::1]:2/cb", true},
		{"HTTPS://client.example.com/cb", "https://client.example.com/cb", true},
		{"https://client.example.com/CB", "https://client.example.com/cb", false},
		{"", "https://client.example.com/cb", false},
	}
	for _, c := range cases {
		if got := ValidForAuthorization(c.url, c.client); got != c.want {
			t.Errorf("ValidForAuthorization(%q, %q) = %v, want %v", c.url, c.client, got, c.want)
		}
	}
}
