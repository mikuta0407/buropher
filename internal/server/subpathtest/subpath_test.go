// Package subpathtest はサブパス配置（server.relative_url_root）のサーバを確認する。
// relative_url_root はプロセス全体の設定（urlroot）なので、ほかのサーバテストと同じテストバイナリで
// 並列に動かさないよう独立したパッケージにしている。
package subpathtest

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/config"
	"github.com/mikuta0407/buropher/internal/db/dbtest"
	"github.com/mikuta0407/buropher/internal/server"
	"github.com/mikuta0407/buropher/internal/testfixtures"
	"github.com/mikuta0407/buropher/internal/urlroot"
)

var frozenTime = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

func newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func do(t *testing.T, c *http.Client, method, u string, form url.Values) (*http.Response, string) {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Accept", "text/html")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

var csrfRe = regexp.MustCompile(`name="authenticity_token" value="([^"]+)"`)

// unprefixedRe はルートを含まないアプリ内の絶対パス（href="/issues" 等）。
var urlAttrRe = regexp.MustCompile(`(?:href|src|action|data-[a-z-]*url[a-z-]*)="(/[^"]*)"`)

func unprefixed(body string) []string {
	var out []string
	for _, m := range urlAttrRe.FindAllStringSubmatch(body, -1) {
		if m[1] != "/redmine" && !strings.HasPrefix(m[1], "/redmine/") && !strings.HasPrefix(m[1], "//") {
			out = append(out, m[0])
		}
	}
	return out
}

func TestRelativeURLRoot(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	if err := testfixtures.LoadContext(ctx, d, frozenTime, testfixtures.All()...); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Server.SecretKey = "test-secret"
	cfg.Server.RelativeURLRoot = "/redmine"
	cfg.Storage.AttachmentsPath = t.TempDir()
	t.Cleanup(func() { urlroot.Set("") })
	srv, err := server.New(cfg, d, server.Options{TempDir: t.TempDir(), Now: func() time.Time { return frozenTime }})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// ルート外は Rack::URLMap と同じ 404
	res, body := do(t, newClient(t), "GET", ts.URL+"/projects", nil)
	if res.StatusCode != 404 || body != "Not Found: /projects" {
		t.Fatalf("outside root: %d %q", res.StatusCode, body)
	}

	c := newClient(t)
	res, body = do(t, c, "GET", ts.URL+"/redmine/login", nil)
	if res.StatusCode != 200 {
		t.Fatalf("login page: %d", res.StatusCode)
	}
	for _, ck := range res.Cookies() {
		if ck.Name == "_redmine_session" && ck.Path != "/redmine" {
			t.Errorf("session cookie path = %q", ck.Path)
		}
	}
	if !strings.Contains(body, `action="/redmine/login"`) || !strings.Contains(body, `href="/redmine/assets/application-`) {
		t.Errorf("login form / assets are not prefixed")
	}
	m := csrfRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no csrf token")
	}
	res, _ = do(t, c, "POST", ts.URL+"/redmine/login", url.Values{"authenticity_token": {m[1]}, "username": {"admin"}, "password": {"admin"}})
	if res.StatusCode != 302 || !strings.HasPrefix(res.Header.Get("Location"), ts.URL+"/redmine/") {
		t.Fatalf("login: %d %q", res.StatusCode, res.Header.Get("Location"))
	}

	for _, p := range []string{"/redmine", "/redmine/projects", "/redmine/projects/ecookbook", "/redmine/issues", "/redmine/issues/1",
		"/redmine/projects/ecookbook/wiki", "/redmine/admin", "/redmine/my/page", "/redmine/issues/new?project_id=1"} {
		res, body := do(t, c, "GET", ts.URL+p, nil)
		if res.StatusCode != 200 {
			t.Errorf("%s: status %d", p, res.StatusCode)
			continue
		}
		if l := unprefixed(body); len(l) > 0 {
			t.Errorf("%s: unprefixed paths: %v", p, l)
		}
	}

	// Atom の URL は request.base_url + script_name
	_, body = do(t, c, "GET", ts.URL+"/redmine/projects.atom", nil)
	if !strings.Contains(body, `href="`+ts.URL+`/redmine/projects.atom"`) {
		t.Errorf("atom self link is not prefixed:\n%s", body[:min(len(body), 600)])
	}
}
