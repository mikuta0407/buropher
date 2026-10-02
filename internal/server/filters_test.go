package server_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/handler"
	"github.com/mikuta0407/buropher/internal/repository"
)

// filterRoutes は ApplicationController の before_action（handler/filters.go）を確認するためのルート。
func filterRoutes(a *handler.App, r chi.Router) {
	projects := &handler.Controller{Name: "projects", MainMenu: true}
	news := &handler.Controller{Name: "news", MainMenu: true}
	ok := func(c *handler.Req) {
		s := "ok"
		if c.Project != nil {
			s += " " + c.Project.Identifier
		}
		if u := handler.CurrentUser(c.R); u != nil && u.Logged() {
			s += " " + u.Login
		}
		c.W.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(c.W, s)
		c.Halt()
	}
	// before_action :find_project, :authorize（projects#show）。API 認証も受け付ける
	a.Handle(r, http.MethodGet, "/t/projects/{id}", projects, "show", ok,
		handler.FindProject("id"), handler.Authorize(), handler.AcceptAPIAuth())
	// before_action :find_project_by_project_id, :authorize（news#index はモジュール news が必要）
	a.Handle(r, http.MethodGet, "/t/projects/{project_id}/news", news, "index", ok,
		handler.FindProjectByProjectID(), handler.Authorize())
	// before_action :find_optional_project（news#index）
	a.Handle(r, http.MethodGet, "/t/news", news, "index", ok, handler.FindOptionalProject())
	// before_action :find_project, :check_project_privacy
	a.Handle(r, http.MethodGet, "/t/privacy/{id}", projects, "show", ok,
		handler.FindProject("id"), handler.CheckProjectPrivacy())
	// before_action :require_admin
	a.Handle(r, http.MethodGet, "/t/admin", &handler.Controller{Name: "admin"}, "index", ok, handler.RequireAdmin())
}

func TestControllerFilters(t *testing.T) {
	ts, d := newFixtureServer(t, filterRoutes)
	ctx := context.Background()
	anon := newClient(t)
	jsmith := login(t, ts, "jsmith", "jsmith")
	dlopper := login(t, ts, "dlopper", "foo")
	admin := login(t, ts, "admin", "admin")
	loginURL := func(path string) string {
		return ts.URL + "/login?back_url=" + url.QueryEscape(ts.URL+path)
	}
	type tc struct {
		name   string
		c      *http.Client
		path   string
		status int
		body   string // 200 のときの本文 / 302 のときの Location / それ以外は本文に含まれる文字列
	}
	run := func(t *testing.T, cases []tc) {
		t.Helper()
		for _, x := range cases {
			res, body := get(t, x.c, ts.URL+x.path)
			if res.StatusCode != x.status {
				t.Errorf("%s %s: status %d, want %d", x.name, x.path, res.StatusCode, x.status)
				continue
			}
			switch {
			case x.status == 200 && body != x.body:
				t.Errorf("%s %s: body %q, want %q", x.name, x.path, body, x.body)
			case x.status == 302 && res.Header.Get("Location") != x.body:
				t.Errorf("%s %s: location %q, want %q", x.name, x.path, res.Header.Get("Location"), x.body)
			case x.status != 200 && x.status != 302 && !strings.Contains(body, x.body):
				t.Errorf("%s %s: body lacks %q", x.name, x.path, x.body)
			}
		}
	}

	t.Run("authorize", func(t *testing.T) {
		run(t, []tc{
			{"anonymous public", anon, "/t/projects/ecookbook", 200, "ok ecookbook"},
			{"anonymous by id", anon, "/t/projects/1", 200, "ok ecookbook"},
			{"anonymous private", anon, "/t/projects/onlinestore", 302, loginURL("/t/projects/onlinestore")},
			{"member private", jsmith, "/t/projects/onlinestore", 200, "ok onlinestore jsmith"},
			{"non member private", dlopper, "/t/projects/onlinestore", 403, "You are not authorized to access this page."},
			{"admin private", admin, "/t/projects/onlinestore", 200, "ok onlinestore admin"},
			{"missing", jsmith, "/t/projects/nosuchproject", 404, "The page you were trying to access doesn&#39;t exist or has been removed."},
			{"news module", jsmith, "/t/projects/ecookbook/news", 200, "ok ecookbook jsmith"},
		})
	})

	t.Run("module disabled", func(t *testing.T) {
		if err := repository.DisableModule(ctx, d, 1, "news"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repository.EnableModule(ctx, d, 1, "news") })
		run(t, []tc{
			// モジュール無効は未ログインでもログイン画面ではなく 403
			{"anonymous", anon, "/t/projects/ecookbook/news", 403, "You are not authorized to access this page."},
			{"member", jsmith, "/t/projects/ecookbook/news", 403, "You are not authorized to access this page."},
			{"admin", admin, "/t/projects/ecookbook/news", 403, "You are not authorized to access this page."},
		})
	})

	t.Run("archived", func(t *testing.T) {
		setStatus(t, d, 2, 9)
		t.Cleanup(func() { setStatus(t, d, 2, 1) })
		run(t, []tc{
			{"member", jsmith, "/t/projects/onlinestore", 403, "The project you&#39;re trying to access has been archived."},
			{"admin", admin, "/t/projects/onlinestore", 403, `href="/projects/onlinestore/unarchive"`},
			{"privacy", jsmith, "/t/privacy/onlinestore", 404, "The page you were trying to access"},
		})
		// 管理者以外には解除リンクを出さない
		if _, body := get(t, jsmith, ts.URL+"/t/projects/onlinestore"); strings.Contains(body, "unarchive") {
			t.Error("unarchive link shown to non admin")
		}
	})

	t.Run("find_optional_project", func(t *testing.T) {
		run(t, []tc{
			{"anonymous global", anon, "/t/news", 200, "ok"},
			{"anonymous project", anon, "/t/news?project_id=ecookbook", 200, "ok ecookbook"},
			{"anonymous missing", anon, "/t/news?project_id=nosuch", 302, loginURL("/t/news?project_id=nosuch")},
			{"logged missing", jsmith, "/t/news?project_id=nosuch", 404, "The page you were trying to access"},
			{"anonymous private", anon, "/t/news?project_id=onlinestore", 302, loginURL("/t/news?project_id=onlinestore")},
		})
	})

	t.Run("check_project_privacy", func(t *testing.T) {
		run(t, []tc{
			{"anonymous public", anon, "/t/privacy/ecookbook", 200, "ok ecookbook"},
			{"anonymous private", anon, "/t/privacy/onlinestore", 302, loginURL("/t/privacy/onlinestore")},
			{"non member", dlopper, "/t/privacy/onlinestore", 403, "You are not authorized to access this page."},
			{"member", jsmith, "/t/privacy/onlinestore", 200, "ok onlinestore jsmith"},
		})
	})

	t.Run("require_admin", func(t *testing.T) {
		run(t, []tc{
			{"anonymous", anon, "/t/admin", 302, loginURL("/t/admin")},
			{"user", jsmith, "/t/admin", 403, "You are not authorized to access this page."},
			{"admin", admin, "/t/admin", 200, "ok admin"},
		})
	})

	t.Run("api auth", func(t *testing.T) {
		key, err := repository.APIKey(ctx, d, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(key) != 40 {
			t.Fatalf("api key %q", key)
		}
		c := newClient(t)
		res, body := get(t, c, ts.URL+"/t/projects/onlinestore.json?key="+key)
		if res.StatusCode != 200 || body != "ok onlinestore jsmith" {
			t.Errorf("key param: %d %q", res.StatusCode, body)
		}
		req, _ := http.NewRequest("GET", ts.URL+"/t/projects/onlinestore.json", nil)
		req.Header.Set("X-Redmine-API-Key", key)
		if res := do(t, c, req); res.StatusCode != 200 {
			t.Errorf("header key: %d", res.StatusCode)
		}
		req, _ = http.NewRequest("GET", ts.URL+"/t/projects/onlinestore.json", nil)
		req.SetBasicAuth("jsmith", "jsmith")
		if res := do(t, c, req); res.StatusCode != 200 {
			t.Errorf("basic auth: %d", res.StatusCode)
		}
		req, _ = http.NewRequest("GET", ts.URL+"/t/projects/onlinestore.json", nil)
		req.SetBasicAuth(key, "x")
		if res := do(t, c, req); res.StatusCode != 200 {
			t.Errorf("basic auth with key: %d", res.StatusCode)
		}
		// 認証なし: REST API 有効かつ accept_api_auth なので 401 + WWW-Authenticate
		res, _ = get(t, c, ts.URL+"/t/projects/onlinestore.json")
		if res.StatusCode != 401 || res.Header.Get("WWW-Authenticate") != `Basic realm="Redmine API"` {
			t.Errorf("no auth: %d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
		}
		// accept_api_auth でないアクションではキーを無視して 403
		res, _ = get(t, c, ts.URL+"/t/projects/onlinestore/news.json?key="+key)
		if res.StatusCode != 403 {
			t.Errorf("api key on non api action: %d", res.StatusCode)
		}
		// 管理者の X-Redmine-Switch-User
		akey, _ := repository.APIKey(ctx, d, 1)
		req, _ = http.NewRequest("GET", ts.URL+"/t/projects/onlinestore.json?key="+akey, nil)
		req.Header.Set("X-Redmine-Switch-User", "dlopper")
		if res := do(t, c, req); res.StatusCode != 403 {
			t.Errorf("switch user: %d", res.StatusCode)
		}
		req.Header.Set("X-Redmine-Switch-User", "nosuchuser")
		if res := do(t, c, req); res.StatusCode != 412 {
			t.Errorf("invalid switch user: %d", res.StatusCode)
		}
	})

	t.Run("record_project_usage", func(t *testing.T) {
		recents := func(uid int64) []int64 {
			ids, err := repository.RecentProjectIDs(ctx, d, uid)
			if err != nil {
				t.Fatal(err)
			}
			return ids
		}
		if err := repository.SetRecentProjectIDs(ctx, d, 2, nil); err != nil {
			t.Fatal(err)
		}
		get(t, jsmith, ts.URL+"/t/projects/ecookbook")
		get(t, jsmith, ts.URL+"/t/projects/onlinestore")
		get(t, jsmith, ts.URL+"/t/projects/ecookbook")
		if got := recents(2); !slices.Equal(got, []int64{1, 2}) {
			t.Errorf("jsmith recents = %v", got)
		}
		// 拒否された（before_action で止まった）アクションは記録しない
		get(t, dlopper, ts.URL+"/t/projects/onlinestore")
		if got := recents(3); len(got) != 0 {
			t.Errorf("dlopper recents = %v", got)
		}
		// ブックマーク済みのプロジェクトは最近使ったプロジェクトに入れない（admin のブックマークは 1, 5）
		get(t, admin, ts.URL+"/t/projects/ecookbook")
		get(t, admin, ts.URL+"/t/projects/onlinestore")
		if got := recents(1); !slices.Equal(got, []int64{2}) {
			t.Errorf("admin recents = %v", got)
		}
		// ジャンプボックスに「最近使ったプロジェクト」として使用順に出る
		_, body := get(t, jsmith, ts.URL+"/")
		want := `<strong>Recently used</strong>` +
			`<a title="eCookbook" href="/projects/ecookbook?jump=welcome"><span style="padding-inline-start:0px;">eCookbook</span></a>` +
			`<a title="OnlineStore" href="/projects/onlinestore?jump=welcome"><span style="padding-inline-start:0px;">OnlineStore</span></a>`
		if !strings.Contains(body, want) {
			t.Errorf("recents not in jump box:\n%s", extract(body, `<div class="drdn-items projects selection">`, `</div>`))
		}
	})
}

func setStatus(t *testing.T, d *db.DB, id int64, status int) {
	t.Helper()
	if _, err := d.Exec(context.Background(), `UPDATE projects SET status = ? WHERE id = ?`, status, id); err != nil {
		t.Fatal(err)
	}
}

func do(t *testing.T, c *http.Client, req *http.Request) *http.Response {
	t.Helper()
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return res
}
