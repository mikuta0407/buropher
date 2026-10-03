// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package httpx

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// 期待値は actionpack 7.2.3 の Mime::Type.parse(accept).map(&:to_s) の実行結果。
func TestParseAcceptMatchesRails(t *testing.T) {
	cases := []struct {
		accept string
		want   []string
	}{
		{"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8", []string{"text/html", "application/xml", "*/*"}},
		{"application/json", []string{"application/json"}},
		{"application/json, text/javascript, */*; q=0.01", []string{"application/json", "text/javascript", "*/*"}},
		{"text/javascript, application/javascript, application/ecmascript, application/x-ecmascript, */*; q=0.01",
			[]string{"text/javascript", "application/ecmascript", "application/x-ecmascript", "*/*"}},
		{"*/*", []string{"*/*"}},
		{"text/xml, application/xml", []string{"application/xml"}},
		{"application/xml;q=0.5, text/xml;q=0.9", []string{"application/xml"}},
		{"text/*", []string{"text/html", "text/plain", "text/javascript", "text/css", "text/calendar", "text/csv", "text/vcard", "text/vtt", "application/xml", "application/x-yaml", "application/json"}},
		{"application/*;q=0.2, text/html", []string{"text/html", "text/javascript", "application/rss+xml", "application/atom+xml", "application/xml", "application/x-yaml", "application/x-www-form-urlencoded", "application/json", "application/pdf", "application/zip", "application/gzip"}},
		{"foo/bar, application/json;q=0.5", []string{"foo/bar", "application/json"}},
		{"application/atom+xml, application/xml", []string{"application/atom+xml", "application/xml"}},
		{"application/xml, application/atom+xml, application/rss+xml;q=0.9", []string{"application/atom+xml", "application/xml", "application/rss+xml"}},
		{"text/html;level=1", []string{"text/html"}},
		{"image/png,image/*;q=0.8,*/*;q=0.5", []string{"image/png", "image/*", "*/*"}},
	}
	for _, c := range cases {
		if got := ParseAccept(c.accept); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q:\n got  %v\n want %v", c.accept, got, c.want)
		}
	}
}

func routed(t *testing.T, pattern, path string, hdr map[string]string, fn func(r *http.Request)) {
	t.Helper()
	r := chi.NewRouter()
	r.Use(ParamsMiddleware(nil, nil))
	called := false
	Route(r, "GET", pattern, func(w http.ResponseWriter, req *http.Request) { called = true; fn(req) })
	req := httptest.NewRequest("GET", path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if !called {
		t.Fatalf("%s: handler not called (status %d)", path, w.Code)
	}
}

func TestFormats(t *testing.T) {
	browser := "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"
	cases := []struct {
		pattern, path string
		hdr           map[string]string
		want          []string
	}{
		{"/issues", "/issues", map[string]string{"Accept": browser}, []string{"html"}},
		{"/issues", "/issues.json", nil, []string{"json"}},
		{"/issues", "/issues.xml", map[string]string{"Accept": browser}, []string{"xml"}},
		{"/issues/{id}", "/issues/1.pdf", nil, []string{"pdf"}},
		{"/projects/{id}/issues", "/projects/foo/issues.atom", nil, []string{"atom"}},
		{"/issues", "/issues.csv", nil, []string{"csv"}},
		{"/issues", "/issues.js", nil, []string{"js"}},
		{"/issues", "/issues?format=json", nil, []string{"json"}},
		// 未知の拡張子は空（respond_to なら 406）
		{"/issues", "/issues.foo", nil, []string{}},
		// Accept ヘッダ（ブラウザ風でないもの）
		{"/issues", "/issues", map[string]string{"Accept": "application/json"}, []string{"json"}},
		{"/issues", "/issues", map[string]string{"Accept": "*/*"}, []string{FormatAll}},
		// XHR は Accept を優先
		{"/issues", "/issues", map[string]string{"X-Requested-With": "XMLHttpRequest", "Accept": "text/javascript, application/javascript, application/ecmascript, application/x-ecmascript, */*; q=0.01"}, []string{"js", FormatAll}},
		// XHR で Accept なしなら js
		{"/issues", "/issues", map[string]string{"X-Requested-With": "XMLHttpRequest"}, []string{"js"}},
		// Accept なしは html
		{"/issues", "/issues", nil, []string{"html"}},
	}
	for _, c := range cases {
		routed(t, c.pattern, c.path, c.hdr, func(r *http.Request) {
			if got := Formats(r); !reflect.DeepEqual(got, c.want) {
				t.Errorf("%s %v: got %v want %v", c.path, c.hdr, got, c.want)
			}
		})
	}
}

func TestNegotiateAndAPIRequest(t *testing.T) {
	routed(t, "/issues", "/issues", map[string]string{"Accept": "*/*"}, func(r *http.Request) {
		if got := Negotiate(r, "html", "api"); got != "html" {
			t.Errorf("*/* negotiate: %q", got)
		}
		if IsAPIRequest(r) {
			t.Error("api_request? true")
		}
		if !ShouldVaryAccept(r) {
			t.Error("vary expected")
		}
	})
	routed(t, "/issues", "/issues.json", nil, func(r *http.Request) {
		if got := Negotiate(r, "html", "json", "xml"); got != "json" {
			t.Errorf("json negotiate: %q", got)
		}
		if got := Negotiate(r, "html", "atom"); got != "" {
			t.Errorf("unknown negotiate: %q", got)
		}
		if got := Negotiate(r, "html", FormatAll); got != "json" {
			t.Errorf("any negotiate: %q", got)
		}
		if !IsAPIRequest(r) {
			t.Error("api_request? false")
		}
		if ShouldVaryAccept(r) {
			t.Error("vary with explicit format")
		}
	})
	routed(t, "/issues", "/issues?format=xml", nil, func(r *http.Request) {
		if !IsAPIRequest(r) {
			t.Error("format param should be api")
		}
	})
}

func TestRouteFormatHandling(t *testing.T) {
	r := chi.NewRouter()
	var got string
	h := func(w http.ResponseWriter, req *http.Request) {
		got = chi.URLParam(req, "id") + "|" + chi.URLParam(req, "format")
	}
	Route(r, "GET", "/issues/{id}", h)
	Route(r, "GET", "/issues", h)
	Route(r, "GET", "/", h)
	Route(r, "GET", "/attachments/download/{id}/{filename}", h, NoFormat())
	Route(r, "GET", "/projects/{id}/issues/report", h, AllowedFormats("html"))
	cases := []struct {
		method, path string
		code         int
		want         string
	}{
		{"GET", "/issues/1", 200, "1|"},
		{"GET", "/issues/1.json", 200, "1|json"},
		{"HEAD", "/issues/1.json", 200, "1|json"},
		{"GET", "/issues/1.2.json", 404, ""},
		{"GET", "/issues/.json", 404, ""},
		{"GET", "/issues.xml", 200, "|xml"},
		{"GET", "/", 200, "|"},
		{"GET", "/attachments/download/3/file.pdf", 200, "3|"},
		{"GET", "/projects/foo/issues/report.json", 404, ""},
		{"GET", "/projects/foo/issues/report.html", 200, "foo|html"},
		{"POST", "/issues", 405, ""},
	}
	for _, c := range cases {
		got = ""
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(c.method, c.path, nil))
		if w.Code != c.code || (c.code == 200 && got != c.want) {
			t.Errorf("%s %s: code %d got %q, want %d %q", c.method, c.path, w.Code, got, c.code, c.want)
		}
	}
	if fn := chi.URLParam(httptest.NewRequest("GET", "/", nil), "x"); fn != "" {
		t.Error("unexpected")
	}
}

func TestContentTypes(t *testing.T) {
	if ContentTypeFor("json") != "application/json; charset=utf-8" || ContentTypeFor("js") != "text/javascript; charset=utf-8" ||
		ContentTypeFor("atom") != "application/atom+xml; charset=utf-8" || ContentTypeFor("nope") != "text/html; charset=utf-8" {
		t.Error("ContentTypeFor")
	}
	w := httptest.NewRecorder()
	SetContentType(w, "pdf", false)
	if w.Header().Get("Content-Type") != "application/pdf" {
		t.Error("SetContentType")
	}
	if LookupMimeByExtension("jpg").Symbol != "jpeg" || LookupMimeByExtension("yml").Symbol != "yaml" || LookupMimeByExtension("exe") != nil {
		t.Error("LookupMimeByExtension")
	}
	if !strings.HasPrefix(MimeFor("csv"), "text/csv") {
		t.Error("MimeFor")
	}
}

func TestFormatsBeforeRouting(t *testing.T) {
	// ルーティング前（トップレベルミドルウェア）ではパス拡張子で近似する
	req := httptest.NewRequest("POST", "/issues.json", nil)
	if !IsAPIRequest(req) || Format(req) != "json" {
		t.Errorf("pre-routing: %v %v", IsAPIRequest(req), Format(req))
	}
	if r2 := WithFormats(req, "html"); Format(r2) != "html" {
		t.Error("WithFormats")
	}
}
