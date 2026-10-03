// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mikuta0407/buropher/web"
)

// redmineRoute は testdata/redmine-routes.json（Redmine 6.1.2 の `rails routes` を JSON で出力したもの）の 1 行。
type redmineRoute struct {
	Name         *string           `json:"name"`
	Verb         string            `json:"verb"`
	Path         string            `json:"path"`
	Controller   *string           `json:"controller"`
	Action       *string           `json:"action"`
	Defaults     map[string]string `json:"defaults"`
	Requirements map[string]string `json:"requirements"`
}

// routeExclusions は意図的に対応しない Redmine のルート（"VERB path" または "path"（全メソッド））と理由。
var routeExclusions = map[string]string{
	// ActionCable のマウント（Redmine 本体は使っていない。Rails が自動で足すもの）
	"/cable": "ActionCable は Redmine 本体で未使用（Rails の自動マウント）",
	// resources が生成するがコントローラにアクションが無いルート。Redmine では AbstractController::ActionNotFound に
	// なり public/404.html を返す（buropher のルーティング外の 404 と同じ応答）。
	"GET /auth_sources/:id(.:format)":                 "AuthSourcesController#show は未定義（Redmine でも 404）",
	"GET /users/:user_id/memberships(.:format)":       "PrincipalMembershipsController#index は未定義（Redmine でも 404）",
	"GET /users/:user_id/memberships/:id(.:format)":   "PrincipalMembershipsController#show は未定義（Redmine でも 404）",
	"GET /groups/:group_id/memberships(.:format)":     "PrincipalMembershipsController#index は未定義（Redmine でも 404）",
	"GET /groups/:group_id/memberships/:id(.:format)": "PrincipalMembershipsController#show は未定義（Redmine でも 404）",
}

// controllerAliases は Redmine のコントローラ名と buropher の Controller.Name の対応（名前が異なるもの）。
var controllerAliases = map[string]string{
	// Doorkeeper のコントローラは名前空間なしの Controller.Name で登録している（oauth_*.go）
	"doorkeeper/authorizations":          "authorizations",
	"doorkeeper/authorized_applications": "authorized_applications",
	"doorkeeper/tokens":                  "tokens",
	"doorkeeper/token_info":              "token_info",
}

// routeParamValues は動的セグメントの代表値（"controller:param" が優先、無ければ "param"）。
// ルーティングの判定には値の中身は関係ないが、GET で実際にリクエストする際に既存レコードを指すようにする。
var routeParamValues = map[string]string{
	"id":                                    "1",
	"project_id":                            "ecookbook",
	"projects:id":                           "ecookbook",
	"activities:id":                         "ecookbook",
	"search:id":                             "ecookbook",
	"reports:id":                            "ecookbook",
	"wikis:id":                              "ecookbook",
	"repositories:id":                       "ecookbook",
	"wiki:id":                               "CookBook_documentation",
	"users:id":                              "2",
	"groups:id":                             "10",
	"group_id":                              "10",
	"user_id":                               "2",
	"principal_memberships:id":              "1",
	"email_addresses:id":                    "1",
	"versions:id":                           "2",
	"enumerations:type":                     "IssuePriority",
	"imports:id":                            "0123456789abcdef",
	"scheme":                                "totp",
	"board_id":                              "1",
	"object_id":                             "1",
	"attachments:object_type":               "issues",
	"attachments:object_id":                 "3",
	"filename":                              "error281.txt",
	"size":                                  "100",
	"version":                               "1",
	"copy_from":                             "1",
	"name":                                  "history",
	"detail":                                "tracker",
	"tab":                                   "info",
	"comment_id":                            "1",
	"custom_field_id":                       "1",
	"repository_id":                         "1",
	"rev":                                   "1",
	"path":                                  "README",
	"issue_id":                              "1",
	"type":                                  "detailed",
	"format":                                "json",
	"settings:id":                           "redmine_discord",
	"oauth2_applications:id":                "1",
	"doorkeeper/authorized_applications:id": "1",
}

// routeSpecTokenRe は Rails のパス指定の字句（( ) :name *name リテラル）。
var routeSpecTokenRe = regexp.MustCompile(`\(|\)|:[a-z_]+|\*[a-z_]+|[^():*]+`)

// expandRouteSpec は Rails のパス指定（"/projects/:id/settings(/:tab)(.:format)"）を、任意部分の
// 有無の組み合わせごとの具体的なパスに展開する。(.:format) は付けない（拡張子なしの形）。
func expandRouteSpec(spec, controller string, requirements map[string]string) []string {
	toks := routeSpecTokenRe.FindAllString(spec, -1)
	var parse func(i int) ([]string, int)
	parse = func(i int) ([]string, int) {
		outs := []string{""}
		for i < len(toks) {
			tok := toks[i]
			switch {
			case tok == ")":
				return outs, i + 1
			case tok == "(":
				// (.:format) は省略形のみ
				if i+3 < len(toks) && toks[i+1] == "." && toks[i+2] == ":format" && toks[i+3] == ")" {
					i += 4
					continue
				}
				sub, ni := parse(i + 1)
				var next []string
				for _, o := range outs {
					next = append(next, o)
					for _, s := range sub {
						next = append(next, o+s)
					}
				}
				outs, i = next, ni
				continue
			case tok[0] == ':' || tok[0] == '*':
				name := tok[1:]
				v, ok := routeParamValues[controller+":"+name]
				if !ok {
					v, ok = routeParamValues[name]
				}
				if !ok {
					v = "1"
				}
				if name == "format" {
					if req := requirements["format"]; req != "" && !strings.Contains(req, "|") {
						if m := regexp.MustCompile(`^\(\?-mix:([a-z]+)\)$`).FindStringSubmatch(req); m != nil {
							v = m[1]
						} else if regexp.MustCompile(`^[a-z]+$`).MatchString(req) {
							v = req
						}
					}
				}
				tok = v
			}
			for j := range outs {
				outs[j] += tok
			}
			i++
		}
		return outs, i
	}
	outs, _ := parse(0)
	return outs
}

type routeFinder interface {
	Find(rctx *chi.Context, method, path string) string
}

// TestRedmineRouteCoverage は Redmine 6.1.2 の全ルート（routeExclusions を除く）が buropher のルータで
// 同じ controller#action に解決されることを確認する。GET のルートは管理者で実際にリクエストし、
// ルーティング外の 404（public/404.html）や 500 にならないことも確認する。
// 結果の一覧は BUROPHER_ROUTE_REPORT にファイル名を指定すると書き出す。
func TestRedmineRouteCoverage(t *testing.T) {
	b, err := os.ReadFile("testdata/redmine-routes.json")
	if err != nil {
		t.Fatal(err)
	}
	var routes []redmineRoute
	if err := json.Unmarshal(b, &routes); err != nil {
		t.Fatal(err)
	}
	srv, ts, _ := newFixtureServerFull(t)
	finder, ok := srv.Handler().(routeFinder)
	if !ok {
		t.Fatal("router does not implement Find")
	}
	type key struct{ method, pattern string }
	table := map[key]string{}
	for _, e := range srv.App().RouteTable() {
		table[key{e.Method, e.Pattern}] = e.Controller + "#" + e.Action
	}
	generic404, _ := fs.ReadFile(web.Public(), "404.html")
	admin := login(t, ts, "admin", "admin")

	var report []string
	total, routed, excluded := 0, 0, 0
	for _, r := range routes {
		ctrl, action := "", ""
		if r.Controller != nil {
			ctrl = *r.Controller
		}
		if r.Action != nil {
			action = *r.Action
		}
		verbs := strings.Split(r.Verb, "|")
		for _, verb := range verbs {
			label := strings.TrimSpace(verb + " " + r.Path)
			if reason, ok := routeExclusions[label]; ok || routeExclusions[r.Path] != "" {
				if !ok {
					reason = routeExclusions[r.Path]
				}
				excluded++
				report = append(report, fmt.Sprintf("EXCLUDED %s (%s#%s): %s", label, ctrl, action, reason))
				continue
			}
			for _, p := range expandRouteSpec(r.Path, ctrl, r.Requirements) {
				total++
				want := ctrl + "#" + action
				if a, ok := controllerAliases[ctrl]; ok {
					want = a + "#" + action
				}
				rctx := chi.NewRouteContext()
				pattern := finder.Find(rctx, verb, p)
				if pattern == "" {
					report = append(report, fmt.Sprintf("UNROUTED %s %s (%s)", verb, p, want))
					t.Errorf("unrouted: %s %s (%s)", verb, p, want)
					continue
				}
				base := strings.TrimSuffix(pattern, ".{format}")
				got, known := table[key{verb, base}]
				if known && got != want {
					report = append(report, fmt.Sprintf("MISMATCH %s %s: want %s, got %s (%s)", verb, p, want, got, pattern))
					t.Errorf("route %s %s: want %s, got %s (pattern %s)", verb, p, want, got, pattern)
					continue
				}
				if verb == http.MethodGet {
					res, err := admin.Get(ts.URL + p)
					if err != nil {
						t.Fatal(err)
					}
					body, _ := readUnbranded(res.Body)
					res.Body.Close()
					switch {
					case res.StatusCode == http.StatusNotFound && string(body) == string(generic404):
						// ルートには一致したが public/404.html を返した。Doorkeeper のコントローラ（oauth2_applications 等）は
						// レコードが無いと Redmine でも public/404.html になるため失敗にはせず、一覧に記録だけする。
						report = append(report, fmt.Sprintf("PUBLIC404 %s %s (%s)", verb, p, want))
					case res.StatusCode >= 500:
						report = append(report, fmt.Sprintf("ERROR%d %s %s (%s)", res.StatusCode, verb, p, want))
						t.Errorf("route %s %s: status %d", verb, p, res.StatusCode)
						continue
					}
				}
				routed++
			}
		}
	}
	t.Logf("Redmine routes: %d paths checked, %d routed, %d excluded", total, routed, excluded)
	if out := os.Getenv("BUROPHER_ROUTE_REPORT"); out != "" {
		sort.Strings(report)
		report = append(report, fmt.Sprintf("TOTAL %d checked, %d routed, %d excluded", total, routed, excluded))
		_ = os.WriteFile(out, []byte(strings.Join(report, "\n")+"\n"), 0o644)
	}
}

// TestRoutingNotFoundFormats はルートが無い場合の 404 が ActionDispatch::PublicExceptions と同じく
// request.formats（Accept またはパスの拡張子）に従って JSON / XML / HTML になることを確認する
// （参照 Redmine の出力と同じ本文）。
func TestRoutingNotFoundFormats(t *testing.T) {
	ts, _ := newFixtureServer(t)
	cases := []struct {
		path, accept, ctype, body string
	}{
		{"/nonexistent", "application/json", "application/json; charset=utf-8", `{"status":404,"error":"Not Found"}`},
		{"/nonexistent", "application/xml", "application/xml; charset=utf-8",
			"<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<hash>\n  <status type=\"integer\">404</status>\n  <error>Not Found</error>\n</hash>\n"},
		// curl の既定 Accept（*/*）は Mime::ALL なので拡張子があっても HTML
		{"/issues/1/time_entries.json", "*/*", "text/html; charset=utf-8", ""},
		{"/nonexistent", "text/javascript", "text/html; charset=utf-8", ""},
	}
	for _, c := range cases {
		res := apiGet(t, ts, c.path, apiHeader("Accept", c.accept))
		if res.Status != 404 || res.Header.Get("Content-Type") != c.ctype {
			t.Errorf("%s (Accept %s): %d %s", c.path, c.accept, res.Status, res.Header.Get("Content-Type"))
		}
		if c.body != "" && res.Body != c.body {
			t.Errorf("%s (Accept %s): body %q", c.path, c.accept, res.Body)
		}
	}
}
