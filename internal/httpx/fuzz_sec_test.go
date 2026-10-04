// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package httpx

import (
	"mime"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mikuta0407/buropher/internal/secoracle"
)

var backURLSeeds = []string{
	"/issues/1", "http://test.host/issues", "//evil.com", "/\\evil.com", "/\t/evil.com", "/%2f%2fevil.com",
	"http://evil.com/", "https://test.host/", "/あ", "\\\\evil.com", " /x", "/x?a=//evil#//evil",
	"http:evil.com", "/login", "/./x", "/%2e%2e/x", "http://test.host:80/x", "http://test.host@evil.com/",
	"/\n/evil.com", "/\x00/evil", "javascript:alert(1)", "/redmine/x",
}

// FuzzValidateBackURL は ValidateBackURL が受け入れた戻り先が、どの使われ方でも同一オリジンに留まることを確かめる。
//
//   - リンク（attachments/edit_all の safe_back_url）・hidden の値として相対 URL のまま使われる:
//     ブラウザの規則（"\" → "/"、タブ・改行の除去）で現在のページに対して解決してもホストが変わらない
//   - redirect_to で使われる: RedirectLocation の結果が同一オリジン
//   - 結果は "/" で始まり CR / LF / NUL を含まない
//
//	go test -run '^$' -fuzz FuzzValidateBackURL ./internal/httpx
func FuzzValidateBackURL(f *testing.F) {
	for _, s := range backURLSeeds {
		f.Add(s, "")
		f.Add(s, "/redmine")
	}
	req := httptest.NewRequest("GET", "http://test.host/projects/x/issues", nil)
	const page = "http://test.host/projects/x/issues"
	f.Fuzz(func(t *testing.T, back, root string) {
		if root != "" && root != "/redmine" {
			return
		}
		got, ok := ValidateBackURL(req, back, root)
		if !ok {
			return
		}
		if !strings.HasPrefix(got, "/") {
			t.Fatalf("ValidateBackURL(%q) = %q: not an absolute path", back, got)
		}
		if err := secoracle.SameOriginPath(page, got); err != nil {
			t.Fatalf("ValidateBackURL(%q, root=%q) = %q used as href: %v", back, root, got, err)
		}
		loc := RedirectLocation(req, got)
		if strings.ContainsAny(loc, "\r\n\x00") {
			t.Fatalf("Location %q contains CR/LF/NUL", loc)
		}
		if err := secoracle.SameOriginPath(page, loc); err != nil {
			t.Fatalf("ValidateBackURL(%q) = %q, Location %q: %v", back, got, loc, err)
		}
	})
}

// FuzzContentDisposition は ContentDisposition の結果を mime.ParseMediaType で読み戻し、
// 種別と filename 以外の引数が注入されないこと、filename*（RFC 5987）が元の名前に戻ることを確かめる。
//
//	go test -run '^$' -fuzz FuzzContentDisposition ./internal/httpx
func FuzzContentDisposition(f *testing.F) {
	for _, s := range []string{"a.txt", "あ.txt", `x"; filename*=UTF-8''evil.html; a="`, "a\r\nX-Injected: 1", "a;b=c", `a\"b`, "%41.txt", "é ß.pdf"} {
		f.Add(s, false)
		f.Add(s, true)
	}
	f.Fuzz(func(t *testing.T, name string, inline bool) {
		disp := "attachment"
		if inline {
			disp = "inline"
		}
		h := ContentDisposition(disp, name)
		if strings.ContainsAny(h, "\r\n\x00") {
			t.Fatalf("ContentDisposition(%q) = %q contains CR/LF/NUL", name, h)
		}
		for i := 0; i < len(h); i++ {
			if h[i] < 0x20 || h[i] >= 0x7f {
				t.Fatalf("ContentDisposition(%q) = %q contains non-token byte %#x", name, h, h[i])
			}
		}
		if name == "" {
			return
		}
		mt, params, err := mime.ParseMediaType(h)
		if err != nil {
			t.Fatalf("ContentDisposition(%q) = %q: unparsable: %v", name, h, err)
		}
		if mt != disp {
			t.Fatalf("ContentDisposition(%q) = %q: type %q", name, h, mt)
		}
		for k := range params {
			if k != "filename" {
				t.Fatalf("ContentDisposition(%q) = %q: injected parameter %q", name, h, k)
			}
		}
		// Go は filename* を優先して filename に入れる（不正な UTF-8 は復元できないので比較しない）
		if utf8.ValidString(name) && params["filename"] != name {
			t.Fatalf("ContentDisposition(%q) = %q: filename round-trips to %q", name, h, params["filename"])
		}
	})
}
