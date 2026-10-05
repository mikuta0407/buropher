// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package webhook

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeResolver は名前解決を固定する（テストでは DNS を使わない）。
func fakeResolver(m map[string][]string) func(context.Context, string) ([]netip.Addr, error) {
	return func(_ context.Context, host string) ([]netip.Addr, error) {
		ss, ok := m[strings.ToLower(host)]
		if !ok {
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
		var out []netip.Addr
		for _, s := range ss {
			out = append(out, netip.MustParseAddr(s))
		}
		return out, nil
	}
}

var testHosts = map[string][]string{
	"example.com":   {"93.184.215.14", "2606:2800:21f:cb07:6820:80da:af6b:8b2c"},
	"example.org":   {"93.184.215.15"},
	"x.example.org": {"93.184.215.16"},
	"localhost":     {"127.0.0.1", "::1"},
}

func newTestValidator(blocklist ...string) *Validator {
	v := NewValidator(blocklist)
	v.Resolver = fakeResolver(testHosts)
	return v
}

var redmineBlocklist = []string{"*.example.org", "10.0.0.0/8", "192.168.0.0/16"}

// webhook_endpoint_validator_test.rb "should validate url"
func TestValidateURL(t *testing.T) {
	ctx := context.Background()
	v := newTestValidator(redmineBlocklist...)
	for _, u := range []string{
		"mailto:user@example.com", "foobar", "example.com", "file://example.com",
		"https://x.example.org/", "http://x.example.org/", "http://missinghost.invalid",
		// webhook_test.rb "should validate url"
		"https://example.org/", "https://x.example.org/foo/bar?a=b", "https://10.1.0.12/",
		// buropher: 末尾に "." の付いたホスト名も同じく拒否する
		"https://x.example.org./",
	} {
		if v.SafeURL(ctx, u) {
			t.Errorf("%s should be invalid", u)
		}
		if errs, _ := Validate(ctx, v, u, "", nil); len(errs) == 0 || errs[0].Attr != "url" {
			t.Errorf("%s: Validate = %v", u, errs)
		}
	}
	if !v.SafeURL(ctx, "https://example.com/some/webhook?foo=bar") {
		t.Error("https://example.com/some/webhook?foo=bar should be valid")
	}
	// *.example.org は example.org 自身にも当たる（Redmine の (?:.*\.)?(?:example\.org)）
	if v.SafeURL(ctx, "https://EXAMPLE.org/") {
		t.Error("example.org should be blocked by *.example.org")
	}
}

// "should validate IPs"
func TestValidIPs(t *testing.T) {
	ctx := context.Background()
	v := newTestValidator()
	if len(v.IPsForURL(ctx, "https://example.com")) == 0 {
		t.Error("example.com should have IPs")
	}
	for _, h := range []string{"127.0.0.1", "::ffff:127.0.0.1", "localhost", "missinghost.invalid"} {
		host := h
		if strings.Contains(h, ":") {
			host = "[" + h + "]"
		}
		if ips := v.IPsForURL(ctx, "https://"+host); len(ips) != 0 {
			t.Errorf("%s: IPs = %v", h, ips)
		}
		if _, err := v.ValidIPs(ctx, h); err == nil {
			t.Errorf("%s should be invalid", h)
		}
	}
}

// "should validate ports"
func TestValidatePorts(t *testing.T) {
	ctx := context.Background()
	v := newTestValidator()
	for _, u := range []string{"http://example.com:22", "http://example.com:1", "http://example.com:0", "http://example.com:70000"} {
		if v.SafeURL(ctx, u) {
			t.Errorf("%s should be invalid", u)
		}
	}
	for _, u := range []string{"http://example.com", "http://example.com:80", "http://example.com:443", "http://example.com:8080"} {
		if !v.SafeURL(ctx, u) {
			t.Errorf("%s should be valid", u)
		}
	}
}

// "should validate ip addresses" と webhook_test.rb "should check ip address at run time"
func TestValidateIPAddresses(t *testing.T) {
	ctx := context.Background()
	v := newTestValidator(redmineBlocklist...)
	for _, ip := range []string{
		"127.0.0.0", "127.0.0.1", "2130706433", "0177.0.1", "0x7f000001", "127.0.0.01", "127.1",
		"10.0.0.0", "10.0.1.0", "169.254.1.9", "192.168.2.1", "224.0.0.1", "::1/128", "[::1]",
		"fe80::123", "[fe80::123]", "0.0.0.0", "::", "[::]", "[::ffff:127.0.0.1]", "[::ffff:7f00:1]",
		"fe80::/10",
		// buropher: マルチキャスト全域・0.0.0.0/8・ブロードキャスト
		"239.1.2.3", "[ff02::1]", "[ff05::1]", "0.1.2.3", "255.255.255.255",
	} {
		if v.SafeURL(ctx, ip) {
			t.Errorf("%s should be invalid without scheme", ip)
		}
		if v.SafeURL(ctx, "http://"+ip) {
			t.Errorf("IP %s should be invalid", ip)
		}
	}
	for _, h := range []string{"[2001:0db8:85a3:0000:0000:8a2e:0370:7334]", "8.8.8.8"} {
		if !v.SafeURL(ctx, "http://"+h) {
			t.Errorf("URI host %s should be valid", h)
		}
	}
	// IPv6 のブロックリスト（IPv4 射影アドレスは IPv4 として照合する）
	v6 := newTestValidator("fc00::/7", "203.0.113.5")
	for _, u := range []string{"http://[fd00::1]/", "http://203.0.113.5/", "http://[::ffff:203.0.113.5]/"} {
		if v6.SafeURL(ctx, u) {
			t.Errorf("%s should be blocked", u)
		}
	}
}

// 1 つでも禁止されたアドレスに解決されるホストは拒否する（DNS で内部アドレスを混ぜる攻撃）
func TestValidIPsRejectsMixedAddresses(t *testing.T) {
	v := NewValidator(nil)
	v.Resolver = fakeResolver(map[string][]string{"mixed.test": {"93.184.215.14", "127.0.0.1"}})
	if _, err := v.ValidIPs(context.Background(), "mixed.test"); err == nil {
		t.Error("mixed.test should be invalid")
	}
}

func TestParseInetAton(t *testing.T) {
	for in, want := range map[string]string{
		"2130706433": "127.0.0.1", "0177.0.1": "127.0.0.1", "0x7f000001": "127.0.0.1", "127.1": "127.0.0.1",
		"127.0.0.01": "127.0.0.1", "10.258": "10.0.1.2",
	} {
		got, ok := parseInetAton(in)
		if !ok || got.String() != want {
			t.Errorf("parseInetAton(%q) = %v, %v; want %s", in, got, ok, want)
		}
	}
	for _, in := range []string{"cafe.be", "example.com", "1.2.3.4.5", "256.1.1.1", "1..2", ""} {
		if _, ok := parseInetAton(in); ok {
			t.Errorf("parseInetAton(%q) should fail", in)
		}
	}
}

// webhook_test.rb "should validate secret length" / "should validate events"
func TestValidateModel(t *testing.T) {
	ctx := context.Background()
	v := newTestValidator()
	errs, _ := Validate(ctx, v, "https://example.com/", strings.Repeat("abdc", 100), nil)
	if len(errs) != 1 || errs[0].Attr != "secret" || errs[0].Key != "too_long" {
		t.Errorf("secret: %v", errs)
	}
	for _, e := range EventNames() {
		if errs, ev := Validate(ctx, v, "https://example.com/", "", []string{"", e}); len(errs) != 0 || len(ev) != 1 {
			t.Errorf("%s: %v %v", e, errs, ev)
		}
	}
	errs, _ = Validate(ctx, v, "https://example.com/", "", []string{"issue.created", "invalid.event"})
	if len(errs) != 1 || errs[0].Attr != "events" {
		t.Errorf("events: %v", errs)
	}
	errs, _ = Validate(ctx, v, "", "", nil)
	if len(errs) != 1 || errs[0].Key != "blank" {
		t.Errorf("blank url: %v", errs)
	}
	errs, _ = Validate(ctx, v, "https://example.com/"+strings.Repeat("a", 2000), "", nil)
	if len(errs) != 1 || errs[0].Key != "too_long" {
		t.Errorf("long url: %v", errs)
	}
}

func TestEvents(t *testing.T) {
	want := []string{
		"issue.created", "issue.updated", "issue.deleted", "news.created", "news.updated", "news.deleted",
		"time_entry.created", "time_entry.updated", "time_entry.deleted", "version.created", "version.updated",
		"version.deleted", "wiki_page.created", "wiki_page.updated", "wiki_page.deleted",
	}
	if got := strings.Join(EventNames(), ","); got != strings.Join(want, ",") {
		t.Errorf("EventNames = %s", got)
	}
	if ValidEvent("issue") || ValidEvent("project.created") || !ValidEvent("wiki_page.deleted") {
		t.Error("ValidEvent")
	}
}

// "should compute correct signature"（GitHub のドキュメントの例）
func TestSignature(t *testing.T) {
	got := Signature("It's a Secret to Everybody", []byte("Hello, World!"))
	if got != "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17" {
		t.Errorf("Signature = %s", got)
	}
}

type received struct {
	mu      sync.Mutex
	method  string
	path    string
	body    string
	headers http.Header
	count   int
}

func newWebhookServer(t *testing.T, status int) (*httptest.Server, *received) {
	t.Helper()
	rec := &received{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.method, rec.path, rec.body, rec.headers = r.Method, r.URL.Path, string(b), r.Header.Clone()
		rec.count++
		rec.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte("OK"))
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func serverPort(t *testing.T, srv *httptest.Server) int {
	_, p, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	n, err := strconv.Atoi(p)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func fixedIPs(ips ...string) func(context.Context, string) []netip.Addr {
	return func(context.Context, string) []netip.Addr {
		var out []netip.Addr
		for _, s := range ips {
			out = append(out, netip.MustParseAddr(s))
		}
		return out
	}
}

// "executor posts the payload and returns the response on success"
func TestExecutorPosts(t *testing.T) {
	srv, rec := newWebhookServer(t, 200)
	x := &Executor{ValidIPs: fixedIPs("127.0.0.1")}
	code, err := x.Call(context.Background(), "http://127.0.0.1:"+strconv.Itoa(serverPort(t, srv))+"/hook", []byte(`{"a":1}`), "")
	if err != nil || code != 200 {
		t.Fatalf("Call = %d, %v", code, err)
	}
	if rec.method != "POST" || rec.path != "/hook" || rec.body != `{"a":1}` {
		t.Errorf("received %+v", rec)
	}
	if rec.headers.Get("Content-Type") != "application/json" || rec.headers.Get("User-Agent") != "Buropher" ||
		rec.headers.Get("Accept") != "*/*" {
		t.Errorf("headers %v", rec.headers)
	}
	if rec.headers.Get(SignatureHeader) != "" {
		t.Error("signature should be empty without secret")
	}
}

// "executor sends the signature header when a secret is present" / "executor passes URL credentials as Basic Auth"
func TestExecutorSignatureAndBasicAuth(t *testing.T) {
	srv, rec := newWebhookServer(t, 200)
	x := &Executor{ValidIPs: fixedIPs("127.0.0.1")}
	u := "http://user:pass@127.0.0.1:" + strconv.Itoa(serverPort(t, srv)) + "/hook"
	if _, err := x.Call(context.Background(), u, []byte("payload"), "topsecret"); err != nil {
		t.Fatal(err)
	}
	want := Signature("topsecret", []byte("payload"))
	if rec.headers.Get(SignatureHeader) != want || rec.headers.Get(SignatureHeaderAlias) != want {
		t.Errorf("signature %v", rec.headers)
	}
	if rec.headers.Get("Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("user:pass")) {
		t.Errorf("authorization %q", rec.headers.Get("Authorization"))
	}
}

// "executor tries the next IP when a connection fails"
func TestExecutorTriesNextIP(t *testing.T) {
	srv, rec := newWebhookServer(t, 200)
	x := &Executor{ValidIPs: fixedIPs("127.0.0.2", "127.0.0.1")}
	code, err := x.Call(context.Background(), "http://127.0.0.1:"+strconv.Itoa(serverPort(t, srv))+"/hook", []byte("payload"), "")
	if err != nil || code != 200 || rec.method != "POST" {
		t.Fatalf("Call = %d, %v, %+v", code, err, rec)
	}
}

// "executor raises a SocketError when no IP can be connected to" / "... when there are no valid IPs"
func TestExecutorNoConnection(t *testing.T) {
	srv, rec := newWebhookServer(t, 200)
	x := &Executor{ValidIPs: fixedIPs("127.0.0.2")}
	_, err := x.Call(context.Background(), "http://127.0.0.1:"+strconv.Itoa(serverPort(t, srv))+"/hook", []byte("payload"), "")
	if !errors.Is(err, ErrNoConnection) || rec.count != 0 {
		t.Errorf("err = %v, count = %d", err, rec.count)
	}
	x = &Executor{ValidIPs: fixedIPs()}
	if _, err := x.Call(context.Background(), "http://127.0.0.1/hook", []byte("payload"), ""); !errors.Is(err, ErrNoConnection) {
		t.Errorf("err = %v", err)
	}
	// 既定の検証ではループバックへは送らない
	x = &Executor{}
	if _, err := x.Call(context.Background(), srv.URL+"/hook", []byte("payload"), ""); !errors.Is(err, ErrNoConnection) || rec.count != 0 {
		t.Errorf("loopback: err = %v", err)
	}
}

// "executor raises on a non-success response without trying other IPs"
func TestExecutorNonSuccess(t *testing.T) {
	srv, rec := newWebhookServer(t, 500)
	x := &Executor{ValidIPs: fixedIPs("127.0.0.1", "127.0.0.1")}
	_, err := x.Call(context.Background(), "http://127.0.0.1:"+strconv.Itoa(serverPort(t, srv))+"/hook", []byte("payload"), "")
	var se *StatusError
	if !errors.As(err, &se) || se.StatusCode != 500 || rec.count != 1 {
		t.Errorf("err = %v, count = %d", err, rec.count)
	}
	// リダイレクトは追わずに失敗にする
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/x", http.StatusFound)
	}))
	defer srv2.Close()
	_, err = x.Call(context.Background(), "http://127.0.0.1:"+strconv.Itoa(serverPort(t, srv2))+"/", []byte("p"), "")
	if !errors.As(err, &se) || se.StatusCode != 302 {
		t.Errorf("redirect: err = %v", err)
	}
}
