// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package httpx

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func withRemoteIP(cfg *ProxyConfig, req *http.Request) *http.Request {
	var out *http.Request
	RemoteIPMiddleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { out = r })).ServeHTTP(httptest.NewRecorder(), req)
	return out
}

func TestRemoteIP(t *testing.T) {
	cases := []struct {
		remote, xff, clientIP string
		cfg                   *ProxyConfig
		want                  string
	}{
		{"203.0.113.5:1234", "", "", nil, "203.0.113.5"},
		// 信頼プロキシ経由
		{"127.0.0.1:1234", "198.51.100.7", "", nil, "198.51.100.7"},
		{"10.0.0.1:1234", "198.51.100.7, 10.0.0.2", "", nil, "198.51.100.7"},
		{"10.0.0.1:1234", "1.1.1.1, 198.51.100.7, 10.0.0.2", "", nil, "198.51.100.7"},
		// すべて信頼プロキシなら最も遠いもの
		{"10.0.0.1:1234", "192.168.1.1, 10.0.0.2", "", nil, "192.168.1.1"},
		// 不正な値は無視
		{"127.0.0.1:1234", "garbage, 198.51.100.7/24", "", nil, "127.0.0.1"},
		// 信頼しない接続元の XFF は既定で無視
		{"203.0.113.5:1234", "198.51.100.7", "", nil, "203.0.113.5"},
		// Rails 互換モード
		{"203.0.113.5:1234", "198.51.100.7", "", &ProxyConfig{TrustAllForwarded: true}, "198.51.100.7"},
		// Client-IP
		{"127.0.0.1:1", "", "198.51.100.9", nil, "198.51.100.9"},
		// Client-IP が XFF に含まれない（なりすまし）なら Client-IP を無視
		{"127.0.0.1:1", "198.51.100.7", "198.51.100.9", nil, "198.51.100.7"},
		// カスタム信頼範囲
		{"203.0.113.5:1234", "198.51.100.7", "", &ProxyConfig{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}}, "198.51.100.7"},
		{"[::1]:1234", "2001:db8::1", "", nil, "2001:db8::1"},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = c.remote
		if c.xff != "" {
			req.Header.Set("X-Forwarded-For", c.xff)
		}
		if c.clientIP != "" {
			req.Header.Set("Client-Ip", c.clientIP)
		}
		if got := RemoteIP(withRemoteIP(c.cfg, req)); got != c.want {
			t.Errorf("%+v: got %s", c, got)
		}
	}
}

func TestRequestURLInfo(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com:8080/x", nil)
	if RequestBaseURL(req) != "http://example.com:8080" || RequestHost(req) != "example.com" || RequestPort(req) != 8080 {
		t.Errorf("plain: %s", RequestBaseURL(req))
	}
	req = httptest.NewRequest("GET", "http://example.com/x", nil)
	req.TLS = &tls.ConnectionState{}
	if RequestBaseURL(req) != "https://example.com" || RequestPort(req) != 443 {
		t.Errorf("tls: %s", RequestBaseURL(req))
	}
	// 信頼プロキシのヘッダ
	req = httptest.NewRequest("GET", "http://internal:3000/x", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "a.example, redmine.example.org")
	req = withRemoteIP(nil, req)
	if RequestBaseURL(req) != "https://redmine.example.org" {
		t.Errorf("proxy: %s", RequestBaseURL(req))
	}
	// 信頼しない接続元のヘッダは無視
	req = httptest.NewRequest("GET", "http://internal:3000/x", nil)
	req.RemoteAddr = "203.0.113.1:5555"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "evil.example")
	if RequestBaseURL(withRemoteIP(nil, req)) != "http://internal:3000" {
		t.Errorf("untrusted proxy headers used")
	}
	req = httptest.NewRequest("GET", "http://[::1]:8080/", nil)
	if RequestHost(req) != "[::1]" || RequestPort(req) != 8080 {
		t.Errorf("ipv6 host %s %d", RequestHost(req), RequestPort(req))
	}
}

func TestURLOptionsFromSettings(t *testing.T) {
	cases := []struct {
		proto, host string
		want        URLOptions
		base        string
	}{
		{"http", "localhost:3000", URLOptions{"http", "localhost", "3000", ""}, "http://localhost:3000"},
		{"https", "redmine.example.org", URLOptions{"https", "redmine.example.org", "", ""}, "https://redmine.example.org"},
		{"https", "example.org/redmine", URLOptions{"https", "example.org", "", "/redmine"}, "https://example.org/redmine"},
		{"http", "http://example.org:8080/sub/dir", URLOptions{"http", "example.org", "8080", "/sub/dir"}, "http://example.org:8080/sub/dir"},
	}
	for _, c := range cases {
		got := URLOptionsFromSettings(c.proto, c.host)
		if got != c.want || got.BaseURL() != c.base {
			t.Errorf("%s: %+v %s", c.host, got, got.BaseURL())
		}
	}
}

func TestRequestIDMiddleware(t *testing.T) {
	var id string
	h := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { id = RequestID(r) }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(id) || w.Header().Get("X-Request-Id") != id {
		t.Errorf("generated id %q", id)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-Id", "abc-123@x <script>"+strings.Repeat("z", 300))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if !strings.HasPrefix(id, "abc-123@xscript") || len(id) != 255 {
		t.Errorf("sanitized id %q", id)
	}
}

func TestAPIErrors(t *testing.T) {
	er := &ErrorRenderer{JSONPEnabled: func(*http.Request) bool { return true }}
	r := chi.NewRouter()
	r.Use(ParamsMiddleware(nil, nil))
	Route(r, "POST", "/issues", func(w http.ResponseWriter, req *http.Request) {
		er.RenderAPIErrors(w, req, "Subject cannot be blank", "Tracker <x> & \u0080")
	})
	cases := []struct {
		path, ct, body string
	}{
		{"/issues.json", "application/json; charset=utf-8", `{"errors":["Subject cannot be blank","Tracker <x> & ` + "\u0080" + `"]}`},
		{"/issues.xml", "application/xml; charset=utf-8", `<?xml version="1.0" encoding="UTF-8"?><errors type="array"><error>Subject cannot be blank</error><error>Tracker &lt;x&gt; &amp; €</error></errors>`},
		{"/issues.json?callback=cb%28%29.x", "application/javascript; charset=utf-8", `cb.x({"errors":["Subject cannot be blank","Tracker <x> & ` + "\u0080" + `"]})`},
	}
	// JSON では <>& を \\u エスケープする（Rails の to_json と同じ）
	esc := strings.NewReplacer("<", "\\"+"u003c", ">", "\\"+"u003e", "&", "\\"+"u0026")
	cases[0].body = esc.Replace(cases[0].body)
	cases[2].body = esc.Replace(cases[2].body)
	for _, c := range cases {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", c.path, nil))
		if w.Code != 422 || w.Header().Get("Content-Type") != c.ct || w.Body.String() != c.body {
			t.Errorf("%s: %d %q\n%q\n%q", c.path, w.Code, w.Header().Get("Content-Type"), w.Body.String(), c.body)
		}
	}
	if APIErrorsJSON(nil) != `{"errors":[]}` || APIErrorsXML(nil) != `<?xml version="1.0" encoding="UTF-8"?><errors type="array"></errors>` {
		t.Error("empty errors")
	}
}

func TestRenderError(t *testing.T) {
	var gotLayout bool
	er := &ErrorRenderer{Page: ErrorPageFunc(func(w http.ResponseWriter, r *http.Request, status int, message string, layout bool) {
		gotLayout = layout
		w.WriteHeader(status)
		_, _ = w.Write([]byte(message))
	})}
	r := chi.NewRouter()
	r.Use(ParamsMiddleware(nil, nil))
	Route(r, "GET", "/issues/{id}", func(w http.ResponseWriter, req *http.Request) { er.Render404(w, req, "") })
	Route(r, "GET", "/admin", func(w http.ResponseWriter, req *http.Request) { er.Render403(w, req, "") })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/issues/9", nil))
	if w.Code != 404 || w.Body.String() != MessageFileNotFound || !gotLayout {
		t.Errorf("html 404: %d %q", w.Code, w.Body.String())
	}
	req := httptest.NewRequest("GET", "/admin", nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Accept", "text/html")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 403 || gotLayout || w.Body.String() != MessageNotAuthorized {
		t.Errorf("xhr html 403: %d layout=%v", w.Code, gotLayout)
	}
	// API は空ボディの head
	for path, ct := range map[string]string{"/issues/9.json": "application/json", "/issues/9.xml": "application/xml", "/issues/9.js": "text/javascript"} {
		w = httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 || w.Body.Len() != 0 || w.Header().Get("Content-Type") != ct {
			t.Errorf("%s: %d %q %q", path, w.Code, w.Header().Get("Content-Type"), w.Body.String())
		}
	}
	// Accept: */* は html（Vary: Accept 付き）
	req = httptest.NewRequest("GET", "/issues/9", nil)
	req.Header.Set("Accept", "*/*")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 404 || w.Header().Get("Vary") != "Accept" || w.Body.String() != MessageFileNotFound {
		t.Errorf("*/*: %d vary=%q", w.Code, w.Header().Get("Vary"))
	}
	// 組み込みの最小ページ
	w = httptest.NewRecorder()
	(&ErrorRenderer{}).RenderError(w, httptest.NewRequest("GET", "/", nil), 422, "Invalid <form>")
	if w.Code != 422 || !strings.Contains(w.Body.String(), `<p id="errorExplanation">Invalid &lt;form&gt;</p>`) {
		t.Errorf("default page: %s", w.Body.String())
	}
	// 204 は Content-Type なし
	w = httptest.NewRecorder()
	RenderAPIOK(w, httptest.NewRequest("PUT", "/issues/1.json", nil))
	if w.Code != 204 || w.Header().Get("Content-Type") != "" {
		t.Errorf("204: %q", w.Header().Get("Content-Type"))
	}
}

func TestXMLEscape(t *testing.T) {
	if got := XMLEscapeText("a<b>&\"'\x01\u0093x\U0001F600"); got != "a&lt;b&gt;&amp;\"'�“x\U0001F600" {
		t.Errorf("text: %q", got)
	}
	if got := XMLEscapeAttr("a\"b\nc"); got != "a&quot;b&#10;c" {
		t.Errorf("attr: %q", got)
	}
}
