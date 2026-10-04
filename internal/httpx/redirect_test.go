// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// testdata/validate_back_url.jsonl は Redmine の validate_back_url を addressable 2.9.0 で
// 実行した結果（リクエストは http://test.host:80）。Redmine の account_controller_test の
// back_url ケースを含む。
func TestValidateBackURLMatchesRedmine(t *testing.T) {
	data, err := os.ReadFile("testdata/validate_back_url.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "http://test.host/login", nil)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var rec struct {
			Root string          `json:"root"`
			URL  string          `json:"url"`
			R    json.RawMessage `json:"r"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatal(err)
		}
		got, ok := ValidateBackURL(req, rec.URL, rec.Root)
		var want string
		wantOK := string(rec.R) != "false"
		if wantOK {
			_ = json.Unmarshal(rec.R, &want)
		}
		// 意図的な相違: ブラウザがリンクとして別ホストへ解決する戻り先（"/\\x//..." など）は拒否する
		if wantOK && !ok && browserProtocolRelative(want) {
			continue
		}
		if ok != wantOK || got != want {
			t.Errorf("root=%q url=%q: got (%q,%v) want (%q,%v)", rec.Root, rec.URL, got, ok, want, wantOK)
		}
	}
}

// browserProtocolRelative はブラウザの規則（タブ・改行の除去、"\\" → "/"）で "//" 始まりになるか。
func browserProtocolRelative(p string) bool {
	return strings.HasPrefix(strings.NewReplacer("\t", "", "\n", "", "\r", "", `\`, "/").Replace(p), "//")
}

// back_url の検証を通った値は safe_back_url としてリンクにも使われる。ブラウザは "\\" を "/" とみなし、
// タブ・改行を除いて解決するため、これらは //evil.com（別ホスト）へのリンクになっていた（オープンリダイレクト）。
func TestValidateBackURLRejectsBrowserProtocolRelative(t *testing.T) {
	req := httptest.NewRequest("GET", "http://test.host/login", nil)
	for _, u := range []string{"/\\evil.com", "/\t/evil.com", "/\n/evil.com", "/\r/evil.com", "http://test.host/\\evil.com",
		"/%5Cevil.com" /* 復号しないので値は "/%5Cevil.com" のまま（同一ホスト）*/} {
		got, ok := ValidateBackURL(req, u, "")
		if ok && browserProtocolRelative(got) {
			t.Errorf("ValidateBackURL(%q) = %q accepted", u, got)
		}
	}
	if got, ok := ValidateBackURL(req, "/issues/1?a=\\b", ""); !ok || got != "/issues/1?a=\\b" {
		t.Errorf("ordinary path rejected: %q %v", got, ok)
	}
}

func TestRedirectBackOrDefault(t *testing.T) {
	cases := []struct {
		url, referer string
		opts         BackURLOptions
		wantLoc      string
		wantBack     bool
	}{
		{"/login?back_url=%2Fissues%2F1", "", BackURLOptions{}, "http://test.host/issues/1", true},
		{"/login?back_url=http%3A%2F%2Fevil.com%2F", "", BackURLOptions{}, "http://test.host/my/page", false},
		{"/login", "http://ref.example/x", BackURLOptions{Referer: true}, "http://ref.example/x", false},
		{"/login", "", BackURLOptions{Referer: true}, "http://test.host/my/page", false},
		{"/login?back_url=%2Fredmine%2Fissues", "", BackURLOptions{RelativeURLRoot: "/redmine", Status: 303}, "http://test.host/redmine/issues", true},
	}
	for _, c := range cases {
		req := httptest.NewRequest("POST", "http://test.host"+c.url, nil)
		if c.referer != "" {
			req.Header.Set("Referer", c.referer)
		}
		w := httptest.NewRecorder()
		back := RedirectBackOrDefault(w, req, "/my/page", c.opts)
		want := 302
		if c.opts.Status != 0 {
			want = c.opts.Status
		}
		if w.Code != want || w.Header().Get("Location") != c.wantLoc || back != c.wantBack || w.Body.Len() != 0 {
			t.Errorf("%s: %d %q back=%v", c.url, w.Code, w.Header().Get("Location"), back)
		}
		if w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
			t.Errorf("content-type %q", w.Header().Get("Content-Type"))
		}
	}
}

func TestRedirectLocation(t *testing.T) {
	req := httptest.NewRequest("GET", "http://127.0.0.1:3999/my/page", nil)
	cases := map[string]string{
		"/login?back_url=x":     "http://127.0.0.1:3999/login?back_url=x",
		"https://other/x":       "https://other/x",
		"//cdn.example/x":       "//cdn.example/x",
		"/a\r\nSet-Cookie: x=1": "http://127.0.0.1:3999/aSet-Cookie: x=1",
		"mailto:a@b":            "mailto:a@b",
	}
	for in, want := range cases {
		if got := RedirectLocation(req, in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
	w := httptest.NewRecorder()
	Redirect(w, req, "/x", http.StatusMovedPermanently)
	if w.Code != 301 {
		t.Error(w.Code)
	}
}
