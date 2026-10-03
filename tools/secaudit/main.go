// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// secaudit は参照 Redmine（公式 fixtures）と buropher（同じ fixtures を import した候補）に
// 同じリクエストを複数のユーザー・認証方式・フォーマットで送り、認可（IDOR・情報漏えい・不正な書き込み）の
// 差分を検出する差分テストハーネス。
//
// 使い方:
//
//	go run ./tools/secaudit -ref http://127.0.0.1:4051 -cand http://127.0.0.1:4151 \
//	    -routes internal/server/testdata/redmine-routes.json -out <dir> [-phase read,special,write] [-setup] \
//	    [-filter <path regexp>] [-ident <ident regexp>] [-c N] [-dry]
//
// 両サーバとも fixtures 投入直後（reset 済み）であることが前提。-setup で私的注記・最小ロール・
// インポートなどの追加データを両方に同じ手順で作る（reset 後に 1 回だけ。ID は <dir>/vars-*.json に保存）。
// read は全 GET ルート × ユーザー × フォーマット、special は API キーでの属性レベルの書き込み、
// write は全書き込みルート × 権限のないユーザー（CSRF トークン付き）。write/special は状態を変えるので
// -c 1 で直列に流し、別の phase の前には reset すること（special は権限昇格を伴うので write と混ぜない）。
// 結果は <dir>/<phase>-results.jsonl（全件。既存なら未実行分だけ追記して再開）と
// <dir>/<phase>-findings.txt（buropher の方が多く許可・露出した候補）。
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ident はリクエスト主体（ユーザー × 認証方式）。
type ident struct {
	Name   string
	Login  string
	Pass   string
	API    bool   // API キーで認証する（セッションは使わない）
	Switch string // X-Redmine-Switch-User / X-Buropher-Switch-User に入れるログイン
}

var sessionIdents = []ident{
	{Name: "anon"},
	{Name: "someone", Login: "someone", Pass: "foo"},
	{Name: "rhill", Login: "rhill", Pass: "foo"}, // setup で eCookbook に最小ロール（権限なし）で参加させる
	{Name: "dlopper", Login: "dlopper", Pass: "foo"},
	{Name: "jsmith", Login: "jsmith", Pass: "jsmith"},
	{Name: "admin", Login: "admin", Pass: "admin"},
	{Name: "dlopper+swhdr", Login: "dlopper", Pass: "foo", Switch: "admin"},
}

var apiIdents = []ident{
	{Name: "someone/api", Login: "someone", Pass: "foo", API: true},
	{Name: "dlopper/api", Login: "dlopper", Pass: "foo", API: true},
	{Name: "rhill/api", Login: "rhill", Pass: "foo", API: true},
	{Name: "dlopper/api+sw-admin", Login: "dlopper", Pass: "foo", API: true, Switch: "admin"},
	{Name: "admin/api+sw-someone", Login: "admin", Pass: "admin", API: true, Switch: "someone"},
}

// target は 1 サーバ分の状態。
type target struct {
	name    string
	base    string
	clients map[string]*http.Client // login -> セッション済みクライアント（"" は匿名）
	tokens  map[string]string       // login -> CSRF トークン
	apiKeys map[string]string       // login -> API キー
	vars    map[string]string       // setup で得た ID など
	mu      sync.Mutex
}

func newClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: 60 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

var reToken = regexp.MustCompile(`name="csrf-token" content="([^"]+)"|name="authenticity_token" value="([^"]+)"`)

func extractToken(b []byte) string {
	m := reToken.FindSubmatch(b)
	if m == nil {
		return ""
	}
	if len(m[1]) > 0 {
		return string(m[1])
	}
	return string(m[2])
}

// get は GET してステータスと本文を返す。
func (t *target) get(cl *http.Client, path string) (int, []byte, error) {
	resp, err := cl.Get(t.base + path)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, b, err
}

// login はセッションを作る（匿名もトークン取得のため GET /login する）。
func (t *target) login(login, pass string) error {
	cl := newClient()
	_, b, err := t.get(cl, "/login")
	if err != nil {
		return err
	}
	tok := extractToken(b)
	if login != "" {
		form := url.Values{"authenticity_token": {tok}, "username": {login}, "password": {pass}, "login": {"Login"}}
		resp, err := cl.PostForm(t.base+"/login", form)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode/100 != 3 || strings.Contains(resp.Header.Get("Location"), "/login") {
			return fmt.Errorf("%s: login %s failed: %d %s", t.name, login, resp.StatusCode, resp.Header.Get("Location"))
		}
		_, b, err = t.get(cl, "/my/page")
		if err != nil {
			return err
		}
		tok = extractToken(b)
		// API キー（未作成なら show_api_key で作られる）
		_, b, err = t.get(cl, "/my/api_key")
		if err != nil {
			return err
		}
		if m := regexp.MustCompile(`<pre[^>]*>\s*([0-9a-f]{40})\s*</pre>`).FindSubmatch(b); m != nil {
			t.apiKeys[login] = string(m[1])
		} else if m := regexp.MustCompile(`\b[0-9a-f]{40}\b`).Find(b); m != nil {
			t.apiKeys[login] = string(m)
		}
	}
	t.clients[login] = cl
	t.tokens[login] = tok
	return nil
}

// result は 1 リクエスト分の結果。
type result struct {
	Status   int      `json:"status"`
	Location string   `json:"loc,omitempty"`
	Len      int      `json:"len"`
	Secrets  []string `json:"secrets,omitempty"`
	Total    int      `json:"total,omitempty"`
	Err      string   `json:"err,omitempty"`
	Snippet  string   `json:"snippet,omitempty"`
}

type job struct {
	Ident  ident
	Method string
	Route  string // 由来ルート（パターン）
	Path   string
	Form   url.Values
	JSON   string
	Phase  string
}

type record struct {
	Phase  string `json:"phase"`
	Ident  string `json:"ident"`
	Method string `json:"method"`
	Route  string `json:"route"`
	Path   string `json:"path"`
	Ref    result `json:"ref"`
	Cand   result `json:"cand"`
	Flag   string `json:"flag,omitempty"`
}

// secret は「見えてはいけない」文字列。
type secret struct {
	name string
	re   *regexp.Regexp
}

func lit(s string) secret { return secret{s, regexp.MustCompile(regexp.QuoteMeta(s))} }
func word(s string) secret {
	return secret{s, regexp.MustCompile(`\b` + regexp.QuoteMeta(s) + `\b`)}
}

var commonSecrets = []secret{
	lit("Issue on project 2"), lit("Issue of a private subproject"), lit("issue of a private subproject of cookbook"),
	lit("Blocked Issue"), lit("Issue Doing the Blocking"), lit("This is an issue that blocks issue"),
	lit("Private issue on public project"), lit("This is a private issue"),
	lit("A comment with a private version"), lit("A comment on a private issue"),
	word("Alpha"), lit("Private Version of public subproject"),
	lit("News on a private project"), lit("Message on a private project"),
	lit("Parent_page"), lit("Child_page_1"), lit("Child_page_2"), lit("E-commerce web site start page"), lit("This is a parent page"),
	lit("Stock management"), lit("private.diff"), secret{"testfile.png", regexp.MustCompile(`(?i)testfile\.png`)},
	lit("root_attachment.txt"), lit("OnlineStore"), lit("Private child of eCookbook"), lit("svn://localhost/test"),
	lit("Private query for cookbook"), lit("Private query for all projects"), lit("Private query for project 2"),
	lit("Public query for project 2"),
	lit("admin@somenet.foo"), lit("jsmith@somenet.foo"),
	lit("SECRET-PRIVNOTE"), lit("SECRET-IMPORT"), lit("SECRET-PRIVPROJ-NOTE"),
	lit("DwMJ2yIxBNeAk26znMYzYmz5dAiIina0GFrPnGTM"), lit("sahYSIaoYrsZUef86sTHrLISdznW6ApF36h5WSnm"),
	lit("b5b6ff9543bf1387374cdfa27a54c96d236a7150"), lit("bfbe06043353a677d0215b26a5800d128d5413bc"),
	lit("82090c953c4a0000a7db253b0691a6b4"), lit("67eb4732624d5a7753dcea7ce0bb7d7d"),
}

var reTotal = regexp.MustCompile(`"total_count":\s*(\d+)|total_count="(\d+)"`)

func (t *target) secretsFor() []secret {
	s := append([]secret{}, commonSecrets...)
	keys := make([]string, 0, len(t.apiKeys))
	for login, k := range t.apiKeys {
		keys = append(keys, login)
		_ = k
	}
	sort.Strings(keys)
	for _, login := range keys {
		s = append(s, secret{"apikey:" + login, regexp.MustCompile(regexp.QuoteMeta(t.apiKeys[login]))})
	}
	return s
}

func (t *target) do(j job, secrets []secret) result {
	var cl *http.Client
	var body io.Reader
	path := t.expand(j.Path)
	req := func() (*http.Request, error) {
		if j.JSON != "" {
			body = strings.NewReader(t.expand(j.JSON))
		} else if j.Form != nil {
			f := url.Values{}
			for k, v := range j.Form {
				for _, x := range v {
					f.Add(k, t.expand(x))
				}
			}
			if !j.Ident.API {
				f.Set("authenticity_token", t.tokens[j.Ident.Login])
			}
			body = strings.NewReader(f.Encode())
		}
		r, err := http.NewRequest(j.Method, t.base+path, body)
		if err != nil {
			return nil, err
		}
		if j.JSON != "" {
			r.Header.Set("Content-Type", "application/json")
		} else if j.Form != nil {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if strings.Contains(path, ".js") && !strings.Contains(path, ".json") {
			r.Header.Set("X-Requested-With", "XMLHttpRequest")
		}
		return r, nil
	}
	r, err := req()
	if err != nil {
		return result{Err: err.Error()}
	}
	if j.Ident.API {
		cl = newClient()
		k := t.apiKeys[j.Ident.Login]
		r.Header.Set("X-Redmine-API-Key", k)
	} else {
		cl = t.clients[j.Ident.Login]
	}
	if j.Ident.Switch != "" {
		r.Header.Set("X-Redmine-Switch-User", j.Ident.Switch)
		r.Header.Set("X-Buropher-Switch-User", j.Ident.Switch)
	}
	resp, err := cl.Do(r)
	if err != nil {
		return result{Err: err.Error()}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	res := result{Status: resp.StatusCode, Location: resp.Header.Get("Location"), Len: len(b)}
	if j.Phase == "special" {
		res.Snippet = string(b[:min(len(b), 700)])
	}
	for _, s := range secrets {
		if s.re.Match(b) {
			res.Secrets = append(res.Secrets, s.name)
		}
	}
	if m := reTotal.FindSubmatch(b); m != nil {
		fmt.Sscan(string(append(m[1], m[2]...)), &res.Total)
	}
	return res
}

func (t *target) expand(s string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k, v := range t.vars {
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	return s
}

// denied はリクエストが拒否された（権限なし・ログインへのリダイレクト等）とみなせるか。
func denied(r result) bool {
	switch {
	case r.Err != "":
		return true
	case r.Status == 401 || r.Status == 403 || r.Status == 404 || r.Status == 406 || r.Status == 422 || r.Status == 400 || r.Status == 409 || r.Status == 405:
		return true
	case r.Status/100 == 3 && strings.Contains(r.Location, "/login"):
		return true
	case r.Status >= 500:
		return true
	}
	return false
}

func classify(j job, ref, cand result) string {
	var flags []string
	if denied(ref) && !denied(cand) {
		flags = append(flags, "ALLOW")
	}
	if !denied(ref) && denied(cand) && cand.Status < 500 {
		flags = append(flags, "deny")
	}
	if cand.Status >= 500 {
		flags = append(flags, "cand5xx")
	}
	var extra []string
	for _, s := range cand.Secrets {
		found := false
		for _, x := range ref.Secrets {
			if x == s {
				found = true
			}
		}
		if !found {
			extra = append(extra, s)
		}
	}
	if len(extra) > 0 {
		flags = append(flags, "LEAK:"+strings.Join(extra, "|"))
	}
	if cand.Total > ref.Total && ref.Status == 200 && cand.Status == 200 {
		flags = append(flags, fmt.Sprintf("COUNT:%d>%d", cand.Total, ref.Total))
	}
	return strings.Join(flags, " ")
}

// ---- ルート展開 ----

type route struct {
	Verb         string            `json:"verb"`
	Path         string            `json:"path"`
	Controller   *string           `json:"controller"`
	Action       *string           `json:"action"`
	Requirements map[string]string `json:"requirements"`
	Defaults     map[string]string `json:"defaults"`
}

var projects = []string{"ecookbook", "onlinestore", "private-child", "subproject1", "project6", "2"}
var projectsShort = []string{"ecookbook", "onlinestore", "private-child"}

var attachmentNames = map[string]string{
	"1": "error281.txt", "2": "document.txt", "3": "logo.gif", "7": "archive.zip", "8": "project_file.zip",
	"9": "version_file.zip", "10": "picture.jpg", "13": "foo.zip", "15": "private.diff", "16": "testfile.png",
	"20": "root_attachment.txt", "21": "archive.zip", "22": "redmine_logo.ai.unknown",
}

// values はルート・パラメータ名ごとの代入候補。
func values(ctrl, action, param string, multi bool) []string {
	pj := projects
	if multi {
		pj = projectsShort
	}
	switch param {
	case "project_id":
		return pj
	case "id":
		switch ctrl {
		case "projects", "activities", "search", "reports", "repositories_proj", "wikis":
			return pj
		case "issues", "journals_new":
			return []string{"1", "4", "6", "14"}
		case "journals":
			return []string{"1", "4", "5", "{privnote}", "{privprojnote}"}
		case "versions":
			return []string{"1", "5", "6", "7"}
		case "news":
			return []string{"1", "3"}
		case "documents":
			return []string{"1"}
		case "boards":
			return []string{"1", "3"}
		case "messages":
			return []string{"1", "7"}
		case "timelog":
			return []string{"1", "4", "5"}
		case "queries":
			return []string{"1", "2", "3", "7", "8"}
		case "attachments":
			return []string{"1", "2", "3", "7", "8", "9", "13", "15", "16", "20", "21", "22"}
		case "issue_relations":
			return []string{"1", "2"}
		case "issue_categories":
			return []string{"1", "3"}
		case "members":
			return []string{"1", "3", "7"}
		case "principal_memberships":
			return []string{"3", "6"}
		case "users":
			return []string{"1", "2", "5", "7", "current"}
		case "email_addresses":
			return []string{"2", "7"}
		case "groups":
			return []string{"10", "11", "12"}
		case "repositories":
			return []string{"10", "11"}
		case "imports":
			return []string{"{import}"}
		case "oauth2_applications", "doorkeeper/authorized_applications":
			return []string{"1"}
		case "settings":
			return []string{"redmine_foo"}
		case "reactions":
			return []string{"1"}
		default:
			return []string{"1"}
		}
	case "issue_id", "object_id", "copy_from":
		if ctrl == "repositories" {
			return []string{"1", "4"}
		}
		return []string{"1", "4", "6", "14"}
	case "board_id":
		return []string{"1", "3"}
	case "user_id":
		if ctrl == "groups" {
			return []string{"8"}
		}
		if ctrl == "watchers" {
			return []string{"3"}
		}
		return []string{"2", "5", "7"}
	case "group_id":
		return []string{"10", "11"}
	case "repository_id":
		return []string{"10", "11"}
	case "rev":
		return []string{"1", "2"}
	case "comment_id":
		return []string{"1"}
	case "custom_field_id":
		return []string{"1"}
	case "type":
		return []string{"IssuePriority"}
	case "scheme":
		return []string{"totp"}
	case "detail":
		return []string{"tracker", "assigned_to", "version", "category"}
	case "name":
		return []string{"time_entries", "changesets"}
	case "version":
		return []string{"1", "2"}
	case "filename":
		return []string{"x"}
	case "object_type":
		return []string{"issues", "documents", "wiki_pages", "versions", "projects", "messages", "news"}
	case "path", "*path":
		return []string{"subversion_test"}
	case "tab":
		return []string{"members"}
	case "size":
		return []string{"100"}
	}
	return []string{"1"}
}

var reParam = regexp.MustCompile(`[:*][a-z_]+`)

// expandRoute は 1 ルートを具体的なパス（フォーマットなし）と「フォーマット可」かどうかに展開する。
func expandRoute(r route) (paths []string, fmtOpt bool, fmtRequired bool) {
	ctrl, action := "", ""
	if r.Controller != nil {
		ctrl = *r.Controller
	}
	if r.Action != nil {
		action = *r.Action
	}
	p := r.Path
	if strings.HasSuffix(p, "(.:format)") {
		p = strings.TrimSuffix(p, "(.:format)")
		fmtOpt = r.Requirements["format"] == ""
	} else if strings.HasSuffix(p, ".:format") {
		p = strings.TrimSuffix(p, ".:format")
		fmtRequired = true
	}
	// 任意グループ: 付けない版と付けた版
	variants := []string{p}
	reOpt := regexp.MustCompile(`\(([^()]*)\)`)
	for {
		var next []string
		changed := false
		for _, v := range variants {
			loc := reOpt.FindStringIndex(v)
			if loc == nil {
				next = append(next, v)
				continue
			}
			changed = true
			inner := v[loc[0]+1 : loc[1]-1]
			next = append(next, v[:loc[0]]+v[loc[1]:])
			if inner != "/:tab" && inner != "/:size" && inner != "/:type" {
				next = append(next, v[:loc[0]]+inner+v[loc[1]:])
			}
		}
		variants = next
		if !changed {
			break
		}
	}
	for _, v := range variants {
		params := reParam.FindAllString(v, -1)
		cctx := ctrl
		if ctrl == "repositories" && strings.HasPrefix(v, "/projects/:id/repository") {
			cctx = "repositories_proj"
		}
		if ctrl == "journals" && action == "new" {
			cctx = "journals_new"
		}
		if ctrl == "watchers" && strings.HasPrefix(v, "/issues/") {
			cctx = "watchers"
		}
		multi := len(params) > 1
		combos := []string{v}
		for _, prm := range params {
			name := strings.TrimLeft(prm, ":*")
			if prm[0] == '*' {
				name = "*path"
			}
			var vals []string
			switch {
			case ctrl == "wiki" && name == "id":
				vals = []string{"CookBook_documentation", "Start_page", "Child_page_1"}
			default:
				vals = values(cctx, action, name, multi)
			}
			if ctrl == "attachments" && name == "id" && strings.Contains(v, "thumbnail") {
				vals = []string{"16", "10", "7", "3"}
			}
			var next []string
			for _, c := range combos {
				for _, val := range vals {
					next = append(next, strings.Replace(c, prm, val, 1))
				}
			}
			combos = next
		}
		// attachments/:id/:filename は実ファイル名を使う
		if ctrl == "attachments" && strings.Contains(v, ":filename") {
			for i, c := range combos {
				parts := strings.Split(c, "/")
				for k := range parts {
					if parts[k] == "x" && k > 0 {
						if n, ok := attachmentNames[parts[k-1]]; ok {
							parts[k] = url.PathEscape(n)
						}
					}
				}
				combos[i] = strings.Join(parts, "/")
			}
		}
		paths = append(paths, combos...)
	}
	return
}

var allFormats = []string{"", ".json", ".xml", ".atom", ".csv", ".pdf", ".js"}

// extraReads はクエリ文字列付きの追加 GET（{fmt} はフォーマットで置換）。
var extraReads = []string{
	"/issues{fmt}?project_id=2", "/issues{fmt}?project_id=onlinestore&set_filter=1&status_id=*",
	"/issues{fmt}?set_filter=1&status_id=*", "/issues{fmt}?query_id=8", "/issues{fmt}?query_id=2", "/issues{fmt}?query_id=3",
	"/issues{fmt}?issue_id=4,6,14", "/issues{fmt}?set_filter=1&f[]=issue_id&op[issue_id]==&v[issue_id][]=4,14",
	"/issues{fmt}?set_filter=1&f[]=subject&op[subject]=~&v[subject][]=private&status_id=*",
	"/issues{fmt}?set_filter=1&c[]=subject&c[]=last_notes&c[]=project&status_id=*",
	"/issues{fmt}?set_filter=1&f[]=fixed_version_id&op[fixed_version_id]==&v[fixed_version_id][]=5",
	"/issues{fmt}?set_filter=1&f[]=project_id&op[project_id]==&v[project_id][]=2",
	"/issues{fmt}?set_filter=1&f[]=assigned_to_id&op[assigned_to_id]==&v[assigned_to_id][]=2&status_id=*",
	"/issues{fmt}?include=attachments,relations,journals&status_id=*",
	"/issues{fmt}?set_filter=1&f[]=notes&op[notes]=~&v[notes][]=SECRET&status_id=*",
	"/issues{fmt}?set_filter=1&f[]=attachment&op[attachment]=~&v[attachment][]=private&status_id=*",
	"/issues{fmt}?set_filter=1&f[]=parent_id&op[parent_id]==&v[parent_id][]=4&status_id=*",
	"/issues{fmt}?set_filter=1&f[]=relates&op[relates]=*&status_id=*",
	"/issues{fmt}?set_filter=1&group_by=project&status_id=*",
	"/issues{fmt}?set_filter=1&group_by=fixed_version&status_id=*",
	"/issues{fmt}?set_filter=1&c[]=relations&status_id=*",
	"/issues/1{fmt}?include=journals,attachments,relations,watchers,changesets,children,allowed_statuses",
	"/issues/2{fmt}?include=journals,attachments,relations,watchers,changesets,children,allowed_statuses",
	"/issues/9{fmt}?include=relations,children", "/issues/14{fmt}?include=journals,attachments",
	"/issues/4{fmt}?include=journals,attachments",
	"/projects/1{fmt}?include=trackers,issue_categories,enabled_modules,time_entry_activities,issue_custom_fields",
	"/projects/2{fmt}?include=trackers,issue_categories",
	"/projects{fmt}?include=trackers&status=", "/projects{fmt}?display_type=list&set_filter=1&status=*",
	"/projects{fmt}?query_id=12",
	"/users/2{fmt}?include=memberships,groups", "/users/7{fmt}?include=memberships,groups", "/users/3{fmt}?include=memberships",
	"/users{fmt}?group_id=10", "/users{fmt}?status=3", "/users{fmt}?name=jsmith",
	"/groups/10{fmt}?include=users,memberships",
	"/time_entries{fmt}?project_id=onlinestore", "/time_entries{fmt}?issue_id=4", "/time_entries{fmt}?set_filter=1&f[]=spent_on&op[spent_on]=*",
	"/time_entries{fmt}?project_id=private-child", "/time_entries{fmt}?user_id=2",
	"/time_entries/report{fmt}?criteria[]=project&criteria[]=issue&columns=year",
	"/projects/ecookbook/time_entries/report{fmt}?criteria[]=project&criteria[]=issue&columns=year",
	"/time_entries{fmt}?query_id=10",
	"/news{fmt}?project_id=2", "/versions/7{fmt}",
	"/projects/ecookbook/versions{fmt}?include=shared", "/projects/ecookbook/roadmap?completed=1&with_subprojects=1",
	"/projects/ecookbook/memberships{fmt}?limit=100", "/projects/onlinestore/memberships{fmt}",
	"/search{fmt}?q=private&all_words=&titles_only=&scope=all",
	"/search{fmt}?q=project+2&scope=all&issues=1&news=1&documents=1&changesets=1&wiki_pages=1&messages=1&projects=1",
	"/search{fmt}?q=page&scope=all&wiki_pages=1", "/search{fmt}?q=SECRET&scope=all", "/search{fmt}?q=Alpha&scope=all",
	"/search{fmt}?q=%234", "/search{fmt}?q=%2314", "/search{fmt}?q=4&issues=1",
	"/search{fmt}?q=Blocked", "/search{fmt}?q=Stock",
	"/projects/ecookbook/search{fmt}?q=private&scope=subprojects",
	"/activity{fmt}?user_id=2", "/activity{fmt}?show_issues=1&show_changesets=1&show_news=1&show_documents=1&show_files=1&show_wiki_edits=1&show_messages=1&show_time_entries=1&from=2026-01-15",
	"/activity{fmt}?from=2006-07-20&with_subprojects=1", "/activity{fmt}?from=2007-01-01",
	"/projects/ecookbook/activity{fmt}?with_subprojects=1&from=2026-01-15", "/projects/ecookbook/activity{fmt}?with_subprojects=1&from=2007-01-01",
	"/issues/auto_complete?term=Issue", "/issues/auto_complete?q=4", "/issues/auto_complete?q=14", "/issues/auto_complete?q=private",
	"/issues/auto_complete?project_id=ecookbook&q=Issue", "/issues/auto_complete?project_id=onlinestore&q=Issue",
	"/issues/auto_complete?q=Blocked&scope=all", "/issues/auto_complete?q=Issue&scope=all&status=o",
	"/wiki_pages/auto_complete?project_id=onlinestore&q=page", "/wiki_pages/auto_complete?project_id=ecookbook&q=page",
	"/wiki_pages/auto_complete?project_id=2&q=Start",
	"/watchers/new?object_type=issue&object_id=4", "/watchers/new?object_type=issue&object_id=14", "/watchers/new?project_id=onlinestore",
	"/watchers/new?object_type=wiki_page&object_id=3", "/watchers/new?object_type=message&object_id=7",
	"/watchers/autocomplete_for_user?project_id=onlinestore&q=", "/watchers/autocomplete_for_user?object_type=issue&object_id=4&q=",
	"/watchers/autocomplete_for_user?project_id=ecookbook&q=j",
	"/watchers/autocomplete_for_mention?q=&object_type=issue&object_id=4&project_id=onlinestore",
	"/watchers/autocomplete_for_mention?q=&object_type=issue&object_id=14&project_id=subproject1",
	"/watchers/autocomplete_for_mention?q=&object_type=issue&project_id=onlinestore",
	"/issues/context_menu?ids[]=4", "/issues/context_menu?ids[]=14", "/issues/context_menu?ids[]=1&ids[]=4", "/issues/context_menu?ids[]=6",
	"/issues/bulk_edit?ids[]=4", "/issues/bulk_edit?ids[]=14", "/issues/bulk_edit?ids[]=1&ids[]=4", "/issues/bulk_edit?ids[]=1",
	"/time_entries/context_menu?ids[]=5", "/time_entries/context_menu?ids[]=1", "/time_entries/bulk_edit?ids[]=5", "/time_entries/bulk_edit?ids[]=1",
	"/admin/projects_context_menu?ids[]=2", "/users/context_menu?ids[]=2",
	"/issues/changes{fmt}?project_id=onlinestore", "/issues/changes{fmt}", "/issues/changes{fmt}?project_id=ecookbook",
	"/queries/filter?project_id=onlinestore&type=IssueQuery&name=fixed_version_id",
	"/queries/filter?type=IssueQuery&name=fixed_version_id", "/queries/filter?type=IssueQuery&name=issue_id",
	"/queries/filter?type=IssueQuery&name=project_id", "/queries/filter?type=IssueQuery&name=assigned_to_id",
	"/queries/filter?project_id=ecookbook&type=IssueQuery&name=category_id",
	"/queries/filter?type=TimeEntryQuery&name=project_id", "/queries/filter?type=IssueQuery&name=author_id",
	"/queries/filter?type=IssueQuery&name=watcher_id", "/queries/filter?type=IssueQuery&name=member_of_group",
	"/queries/filter?type=ProjectQuery&name=parent_id", "/queries/filter?type=IssueQuery&name=relates",
	"/queries{fmt}?limit=100", "/queries/new?project_id=onlinestore",
	"/projects/autocomplete?q=Online", "/projects/autocomplete?q=Private", "/projects/autocomplete?q=",
	"/issues/calendar?project_id=onlinestore", "/issues/calendar?set_filter=1&status_id=*&month=1&year=2026",
	"/issues/calendar?year=2006&month=7", "/issues/gantt?project_id=2", "/issues/gantt{fmt}?year=2006&month=7&months=24",
	"/issues/gantt{fmt}?set_filter=1&status_id=*&months=24&year=2025",
	"/issues/preview?issue_id=4&project_id=onlinestore&text=x", "/issues/preview?issue_id=14&text=x",
	"/issues/preview?project_id=onlinestore&text=x", "/issues/preview?project_id=onlinestore&issue[description]=x",
	"/news/preview?project_id=onlinestore&text=x", "/news/preview?id=3&text=x",
	"/preview/text?text=%234+%2314+%236+version%3A5+version%3AAlpha+news%3A3+document%3A1+r1+commit%3A1+message%3A7+%5B%5Bonlinestore%3AStart_page%5D%5D+onlinestore%3Aversion%3AAlpha+project%3Aonlinestore+attachment%3Aprivate.diff+%40jsmith+user%3Ajsmith",
	"/preview/text?project_id=onlinestore&text=%5B%5BParent_page%5D%5D+%7B%7Bchild_pages(Parent_page)%7D%7D+%7B%7Binclude(onlinestore%3AParent_page)%7D%7D",
	"/preview/text?text=%7B%7Binclude(onlinestore%3AParent_page)%7D%7D+%7B%7Bchild_pages(onlinestore%3AParent_page)%7D%7D+%7B%7Bissue(4)%7D%7D",
	"/preview/text?text=%234%3Anote-1+%2314%23note-1+%7B%7Bthumbnail(16)%7D%7D+attachment%3A16",
	"/projects/ecookbook/memberships/autocomplete?q=", "/projects/onlinestore/memberships/autocomplete?q=",
	"/groups/10/autocomplete_for_user?q=", "/auth_sources/autocomplete_for_new_user?term=a",
	"/projects/onlinestore/issues/report/tracker", "/projects/ecookbook/issues/report/version",
	"/projects/ecookbook/issues/report{fmt}?with_subprojects=1",
	"/my/page?blocks[]=issuesassignedtome", "/my/page?block=issuequery&query_id=8",
	"/issues/new?project_id=onlinestore", "/issues/new?copy_from=4", "/projects/ecookbook/issues/new?copy_from=4",
	"/projects/ecookbook/issues/new?copy_from=14", "/projects/ecookbook/issues/new?issue[parent_issue_id]=4",
	"/projects/ecookbook/issues/4/copy", "/projects/onlinestore/issues/1/copy",
	"/issues/1/tab/time_entries", "/issues/4/tab/changesets", "/issues/14/tab/time_entries",
	"/time_entries/new?issue_id=4", "/time_entries/new?project_id=onlinestore", "/time_entries/new?issue_id=14",
	"/time_entries{fmt}?issue_id=14",
	"/issues{fmt}?set_filter=1&f[]=status_id&op[status_id]=*&c[]=spent_hours&c[]=total_spent_hours&t[]=spent_hours",
	"/projects/ecookbook/issues{fmt}?set_filter=1&status_id=*&subproject_id=*",
	"/projects/ecookbook/issues{fmt}?set_filter=1&status_id=*&f[]=subproject_id&op[subproject_id]=*",
	"/projects/ecookbook/issues{fmt}?query_id=8", "/projects/ecookbook/issues{fmt}?query_id=7",
	"/projects/subproject1/issues{fmt}?set_filter=1&status_id=*",
	"/issues/2/relations{fmt}", "/issues/9/relations{fmt}", "/relations/1{fmt}",
	"/attachments/download/15", "/attachments/download/7", "/attachments/download/16/testfile.png", "/attachments/thumbnail/16/200",
	"/attachments/7{fmt}", "/attachments/15{fmt}",
	"/projects/onlinestore/wiki/Start_page{fmt}", "/projects/onlinestore/wiki/index{fmt}", "/projects/onlinestore/wiki/export{fmt}",
	"/projects/2/wiki/Start_page{fmt}?version=1",
	"/sys/projects{fmt}", "/sys/projects{fmt}?key=x",
	"/mail_handler",
	"/issues{fmt}?key={apikey_someone}", "/projects/ecookbook/issues.atom?key={atomkey_dlopper}",
	"/my/page{fmt}", "/my/account{fmt}",
	"/users/2.json?key={apikey_dlopper}", "/users/current.json?key={apikey_dlopper}",
	"/issues/4.json?key={apikey_dlopper}", "/issues/14.json?key={apikey_dlopper}",
	"/projects/onlinestore/news{fmt}", "/projects/onlinestore/boards/3{fmt}", "/boards/3/topics/7{fmt}", "/boards/1/topics/7{fmt}",
	"/projects/ecookbook/boards/3{fmt}", "/projects/ecookbook/boards/1{fmt}",
	"/projects/ecookbook/files{fmt}", "/projects/onlinestore/files{fmt}",
	"/projects/ecookbook/issues/gantt{fmt}?with_subprojects=1&months=24&year=2025",
	"/projects/ecookbook/issues/calendar?with_subprojects=1&year=2026&month=1",
	"/imports/{import}", "/imports/{import}/settings", "/imports/{import}/mapping", "/imports/{import}/run",
}

func buildReadJobs(routes []route) []job {
	var jobs []job
	seen := map[string]bool{}
	add := func(id ident, rpat, p string) {
		key := id.Name + " " + p
		if seen[key] {
			return
		}
		seen[key] = true
		jobs = append(jobs, job{Ident: id, Method: "GET", Route: rpat, Path: p, Phase: "read"})
	}
	addPath := func(rpat, p string, fmtOpt, fmtReq bool) {
		fmts := []string{""}
		if fmtOpt {
			fmts = allFormats
		}
		if fmtReq {
			fmts = []string{".json", ".xml"}
		}
		for _, f := range fmts {
			full := p + f
			if i := strings.Index(p, "?"); i >= 0 {
				full = p[:i] + f + p[i:]
			}
			for _, id := range sessionIdents {
				add(id, rpat, full)
			}
			if f == ".json" || f == ".xml" {
				for _, id := range apiIdents {
					add(id, rpat, full)
				}
			}
		}
	}
	for _, r := range routes {
		if r.Controller == nil {
			continue
		}
		verbs := strings.Split(r.Verb, "|")
		isGet := false
		for _, v := range verbs {
			if v == "GET" {
				isGet = true
			}
		}
		if !isGet || r.Path == "/logout(.:format)" || strings.HasPrefix(r.Path, "/oauth/authorize") {
			continue
		}
		paths, fo, fr := expandRoute(r)
		for _, p := range paths {
			addPath(r.Path, p, fo, fr)
		}
	}
	for _, e := range extraReads {
		if strings.Contains(e, "{fmt}") {
			for _, f := range []string{"", ".json", ".xml", ".atom", ".csv", ".pdf"} {
				full := strings.Replace(e, "{fmt}", f, 1)
				for _, id := range sessionIdents {
					add(id, "extra:"+e, full)
				}
				if f == ".json" || f == ".xml" {
					for _, id := range apiIdents {
						add(id, "extra:"+e, full)
					}
				}
			}
		} else {
			for _, id := range sessionIdents {
				add(id, "extra:"+e, e)
			}
		}
	}
	// 重要度の高いフォーマット（HTML・JSON）を先に流す
	prio := func(p string) int {
		if i := strings.Index(p, "?"); i >= 0 {
			p = p[:i]
		}
		switch filepath.Ext(p) {
		case ".json":
			return 1
		case ".xml":
			return 2
		case ".atom", ".csv", ".pdf":
			return 3
		case ".js":
			return 4
		}
		return 0
	}
	sort.SliceStable(jobs, func(i, k int) bool { return prio(jobs[i].Path) < prio(jobs[k].Path) })
	return jobs
}

// universalForm は書き込み系ルートに送る共通フォーム（strong params が無関係なキーを捨てる前提）。
func universalForm(path string) url.Values {
	f := url.Values{
		"issue[subject]": {"PWN"}, "issue[notes]": {"PWN"}, "notes": {"PWN"},
		"journal[notes]": {"PWN"}, "version[name]": {"PWN"}, "news[title]": {"PWN"}, "news[description]": {"PWN"},
		"comment[comments]": {"PWN"}, "document[title]": {"PWN"}, "board[name]": {"PWN"}, "board[description]": {"PWN"},
		"message[subject]": {"PWN"}, "message[content]": {"PWN"}, "reply[subject]": {"PWN"}, "reply[content]": {"PWN"},
		"time_entry[hours]": {"1"}, "time_entry[comments]": {"PWN"}, "time_entry[activity_id]": {"9"},
		"query[name]": {"PWN"}, "issue_category[name]": {"PWN"}, "content[text]": {"PWN"},
		"wiki_page[title]": {"PWN"}, "project[name]": {"PWN"}, "membership[role_ids][]": {"1"},
		"membership[user_id]": {"7"}, "user[firstname]": {"PWN"}, "group[name]": {"PWN"}, "user_ids[]": {"7"},
		"user_id": {"7"}, "watcher[user_ids][]": {"7"}, "relation[issue_to_id]": {"1"}, "relation[relation_type]": {"relates"},
		"attachment[filename]": {"pwn.txt"}, "attachments[1][filename]": {"pwn.txt"},
		"ids[]": {"4", "6", "14"}, "text": {"PWN"}, "object_type": {"issue"}, "object_id": {"4"},
		"tracker[name]": {"PWN"}, "issue_status[name]": {"PWN"}, "role[name]": {"PWN"}, "enumeration[name]": {"PWN"},
		"custom_field[name]": {"PWN"}, "auth_source[name]": {"PWN"}, "settings[app_title]": {"PWN"},
		"email_address[address]": {"pwn@example.net"}, "repository[url]": {"/tmp/pwn"}, "repository[identifier]": {"pwn"},
		"repository_scm": {"Subversion"}, "reaction[reactable_type]": {"Issue"}, "reactable_type": {"Issue"}, "reactable_id": {"4"},
		"name": {"PWN"}, "block": {"news"}, "blocks[]": {"news"}, "confirm": {"1"}, "issue_id": {"4"}, "rev": {"1"},
		"enumerations[9][custom_field_values][7]": {"1"}, "enumerations[9][active]": {"0"},
		"doorkeeper_application[name]": {"PWN"}, "doorkeeper_application[redirect_uri]": {"https://x.example/cb"},
	}
	_ = path
	return f
}

var extraWrites = [][2]string{
	{"POST", "/watchers/watch?object_type=issue&object_id=4"},
	{"POST", "/watchers/watch?object_type=issue&object_id=14"},
	{"POST", "/watchers/watch?object_type=message&object_id=7"},
	{"POST", "/watchers/watch?object_type=board&object_id=3"},
	{"POST", "/watchers/watch?object_type=wiki_page&object_id=3"},
	{"POST", "/watchers/watch?object_type=news&object_id=3"},
	{"POST", "/watchers?object_type=issue&object_id=4&watcher[user_ids][]=7"},
	{"POST", "/watchers?object_type=issue&object_id=14&watcher[user_ids][]=7"},
	{"POST", "/watchers/append?object_type=issue&object_id=4&watcher[user_ids][]=7"},
	{"DELETE", "/watchers?object_type=issue&object_id=2&user_id=3"},
	{"DELETE", "/watchers/watch?object_type=issue&object_id=2"},
	{"POST", "/issues.json"}, {"POST", "/projects/onlinestore/issues"}, {"POST", "/projects/ecookbook/issues"},
	{"POST", "/issues?issue[project_id]=2"},
	{"POST", "/time_entries?time_entry[issue_id]=4"}, {"POST", "/time_entries?time_entry[project_id]=2"},
	{"POST", "/time_entries?time_entry[issue_id]=14"},
	{"POST", "/issues/1/relations?relation[issue_to_id]=4"}, {"POST", "/issues/4/relations?relation[issue_to_id]=1"},
	{"POST", "/issues/14/relations?relation[issue_to_id]=1"},
	{"POST", "/issues/bulk_update?ids[]=4"}, {"POST", "/issues/bulk_update?ids[]=14"}, {"POST", "/issues/bulk_update?ids[]=1"},
	{"POST", "/issues/bulk_update?ids[]=1&issue[project_id]=2"},
	{"POST", "/time_entries/bulk_update?ids[]=5"}, {"POST", "/time_entries/bulk_update?ids[]=1"},
	{"POST", "/reactions?object_type=Issue&object_id=4"}, {"POST", "/reactions?object_type=Journal&object_id=5"},
	{"POST", "/reactions?object_type=Message&object_id=7"}, {"POST", "/reactions?object_type=News&object_id=3"},
	{"POST", "/uploads.json?filename=a.txt"},
	{"POST", "/issues/4/quoted?journal_id=4"}, {"POST", "/issues/14/quoted?journal_id=5"},
	{"POST", "/issues/1/quoted?journal_id={privnote}"},
	{"POST", "/issues/new?project_id=onlinestore"}, {"POST", "/projects/onlinestore/issues/new"},
	{"POST", "/time_entries/new?time_entry[issue_id]=4"},
	{"POST", "/queries?type=IssueQuery&project_id=onlinestore"},
	{"PATCH", "/projects/onlinestore/enumerations"},
	{"POST", "/projects/onlinestore/news"},
	{"POST", "/news/3/comments"},
	{"POST", "/boards/3/topics/new"}, {"POST", "/boards/3/topics/7/replies"}, {"POST", "/boards/1/topics/7/replies"},
	{"POST", "/projects/onlinestore/wiki/Start_page/protect?protected=1"},
	{"POST", "/projects/onlinestore/files?version_id=5"},
	{"POST", "/documents/1/add_attachment"},
	{"POST", "/imports?type=IssueImport&project_id=onlinestore"},
	{"PATCH", "/attachments/issues/4"}, {"PATCH", "/attachments/issues/14"},
	{"DELETE", "/attachments/7"}, {"DELETE", "/attachments/15"},
}

func buildWriteJobs(routes []route) []job {
	writeIdents := []ident{sessionIdents[0], sessionIdents[1], sessionIdents[2], sessionIdents[3]}
	apiW := []ident{apiIdents[0], apiIdents[1]}
	var jobs, dels []job
	seen := map[string]bool{}
	add := func(id ident, method, rpat, p string) {
		key := id.Name + " " + method + " " + p
		if seen[key] {
			return
		}
		seen[key] = true
		j := job{Ident: id, Method: method, Route: rpat, Path: p, Phase: "write", Form: universalForm(p)}
		if id.API {
			j.Form = nil
			j.JSON = `{"issue":{"subject":"PWN","notes":"PWN"},"time_entry":{"hours":1,"comments":"PWN","activity_id":9},"version":{"name":"PWN"},"news":{"title":"PWN","description":"PWN"},"membership":{"user_id":7,"role_ids":[1]},"user":{"firstname":"PWN"},"group":{"name":"PWN"},"relation":{"issue_to_id":1,"relation_type":"relates"},"wiki_page":{"text":"PWN"},"project":{"name":"PWN"},"issue_category":{"name":"PWN"},"attachment":{"filename":"pwn.txt"},"user_id":7}`
		}
		if method == "DELETE" {
			dels = append(dels, j)
		} else {
			jobs = append(jobs, j)
		}
	}
	for _, r := range routes {
		if r.Controller == nil {
			continue
		}
		ctrl := *r.Controller
		if r.Path == "/login(.:format)" || r.Path == "/logout(.:format)" || ctrl == "account" || ctrl == "my" ||
			ctrl == "twofa" || ctrl == "twofa_backup_codes" || strings.HasPrefix(ctrl, "doorkeeper") || ctrl == "previews" ||
			ctrl == "mail_handler" || ctrl == "sys" || ctrl == "rails/health" {
			continue
		}
		var method string
		for _, v := range strings.Split(r.Verb, "|") {
			if v != "GET" && v != "" {
				method = v
				break
			}
		}
		if method == "" {
			continue
		}
		paths, _, _ := expandRoute(r)
		for _, p := range paths {
			for _, id := range writeIdents {
				add(id, method, r.Path, p)
			}
			for _, id := range apiW {
				add(id, method, r.Path, p+".json")
			}
		}
	}
	for _, w := range extraWrites {
		for _, id := range writeIdents {
			add(id, w[0], "extraw:"+w[1], w[1])
		}
		for _, id := range apiW {
			p := w[1]
			if !strings.Contains(p, ".json") {
				if i := strings.Index(p, "?"); i >= 0 {
					p = p[:i] + ".json" + p[i:]
				} else {
					p += ".json"
				}
			}
			add(id, w[0], "extraw:"+w[1], p)
		}
	}
	return append(jobs, dels...)
}

// specialWrites は API キーで送る、属性レベルの認可（他プロジェクトへの移動・見えない親や版の指定・
// 他人のアップロードの添付など）を試す書き込み。[method, path, json]。
var specialWrites = [][3]string{
	{"PUT", "/issues/1.json", `{"issue":{"project_id":2}}`},
	{"PUT", "/issues/1.json", `{"issue":{"notes":"SP-privnote","private_notes":true}}`},
	{"PUT", "/issues/1.json", `{"issue":{"parent_issue_id":4}}`},
	{"PUT", "/issues/1.json", `{"issue":{"parent_issue_id":14}}`},
	{"PUT", "/issues/1.json", `{"issue":{"fixed_version_id":5}}`},
	{"PUT", "/issues/1.json", `{"issue":{"category_id":3}}`},
	{"PUT", "/issues/1.json", `{"issue":{"assigned_to_id":1}}`},
	{"PUT", "/issues/1.json", `{"issue":{"is_private":true}}`},
	{"PUT", "/issues/1.json", `{"issue":{"status_id":5}}`},
	{"PUT", "/issues/1.json", `{"issue":{"watcher_user_ids":[8]}}`},
	{"PUT", "/issues/1.json", `{"issue":{"notes":"SP-upload","uploads":[{"token":"{upload_jsmith}","filename":"stolen.txt"}]}}`},
	{"PUT", "/issues/4.json", `{"issue":{"project_id":1}}`},
	{"PUT", "/issues/4.json", `{"issue":{"notes":"SP-x"}}`},
	{"PUT", "/issues/14.json", `{"issue":{"notes":"SP-x"}}`},
	{"PUT", "/issues/6.json", `{"issue":{"notes":"SP-x"}}`},
	{"POST", "/issues.json", `{"issue":{"project_id":1,"subject":"SP-parent4","parent_issue_id":4}}`},
	{"POST", "/issues.json", `{"issue":{"project_id":1,"subject":"SP-parent14","parent_issue_id":14}}`},
	{"POST", "/issues.json", `{"issue":{"project_id":1,"subject":"SP-ver5","fixed_version_id":5}}`},
	{"POST", "/issues.json", `{"issue":{"project_id":1,"subject":"SP-ver6","fixed_version_id":6}}`},
	{"POST", "/issues.json", `{"issue":{"project_id":1,"subject":"SP-cat3","category_id":3}}`},
	{"POST", "/issues.json", `{"issue":{"project_id":1,"subject":"SP-assign","assigned_to_id":1}}`},
	{"POST", "/issues.json", `{"issue":{"project_id":1,"subject":"SP-watch","watcher_user_ids":[8,7]}}`},
	{"POST", "/issues.json", `{"issue":{"project_id":1,"subject":"SP-private","is_private":true}}`},
	{"POST", "/issues.json", `{"issue":{"project_id":1,"subject":"SP-author","author_id":1}}`},
	{"POST", "/issues.json", `{"issue":{"project_id":1,"subject":"SP-upload","uploads":[{"token":"{upload_jsmith}","filename":"stolen.txt"}]}}`},
	{"POST", "/issues.json", `{"issue":{"project_id":1,"subject":"SP-copy","copy_from":4}}`},
	{"POST", "/issues.json", `{"issue":{"project_id":2,"subject":"SP-p2"}}`},
	{"POST", "/issues.json", `{"issue":{"project_id":5,"subject":"SP-p5"}}`},
	{"POST", "/projects/1/issues.json", `{"issue":{"subject":"SP-cf","custom_fields":[{"id":9,"value":"x"}]}}`},
	{"POST", "/issues/1/relations.json", `{"relation":{"issue_to_id":4,"relation_type":"relates"}}`},
	{"POST", "/issues/1/relations.json", `{"relation":{"issue_to_id":14,"relation_type":"relates"}}`},
	{"POST", "/issues/4/relations.json", `{"relation":{"issue_to_id":1,"relation_type":"relates"}}`},
	{"POST", "/time_entries.json", `{"time_entry":{"issue_id":4,"hours":1,"activity_id":9}}`},
	{"POST", "/time_entries.json", `{"time_entry":{"issue_id":14,"hours":1,"activity_id":9}}`},
	{"POST", "/time_entries.json", `{"time_entry":{"project_id":2,"hours":1,"activity_id":9}}`},
	{"POST", "/time_entries.json", `{"time_entry":{"project_id":1,"hours":1,"activity_id":9,"user_id":2}}`},
	{"POST", "/time_entries.json", `{"time_entry":{"project_id":1,"issue_id":4,"hours":1,"activity_id":9}}`},
	{"PUT", "/time_entries/1.json", `{"time_entry":{"issue_id":4}}`},
	{"PUT", "/time_entries/1.json", `{"time_entry":{"comments":"SP"}}`},
	{"PUT", "/time_entries/5.json", `{"time_entry":{"comments":"SP"}}`},
	{"POST", "/issues/1/watchers.json", `{"user_id":8}`},
	{"POST", "/issues/4/watchers.json", `{"user_id":7}`},
	{"POST", "/issues/14/watchers.json", `{"user_id":7}`},
	{"DELETE", "/issues/2/watchers/3.json", ``},
	{"POST", "/projects.json", `{"project":{"name":"SP proj","identifier":"spproj","parent_id":2}}`},
	{"POST", "/projects.json", `{"project":{"name":"SP proj1","identifier":"spproj1","parent_id":1}}`},
	{"PUT", "/projects/1.json", `{"project":{"description":"SP"}}`},
	{"PUT", "/projects/1.json", `{"project":{"parent_id":2}}`},
	{"PUT", "/projects/2.json", `{"project":{"description":"SP"}}`},
	{"POST", "/projects/2/memberships.json", `{"membership":{"user_id":7,"role_ids":[1]}}`},
	{"POST", "/projects/1/memberships.json", `{"membership":{"user_id":7,"role_ids":[1]}}`},
	{"PUT", "/memberships/2.json", `{"membership":{"role_ids":[1]}}`},
	{"PUT", "/memberships/3.json", `{"membership":{"role_ids":[1]}}`},
	{"POST", "/projects/1/versions.json", `{"version":{"name":"SP-v"}}`},
	{"PUT", "/versions/7.json", `{"version":{"description":"SP"}}`},
	{"PUT", "/versions/5.json", `{"version":{"description":"SP"}}`},
	{"PUT", "/versions/1.json", `{"version":{"sharing":"system"}}`},
	{"POST", "/projects/2/issue_categories.json", `{"issue_category":{"name":"SP-c"}}`},
	{"PUT", "/issue_categories/3.json", `{"issue_category":{"name":"SP-c"}}`},
	{"PUT", "/projects/onlinestore/wiki/Start_page.json", `{"wiki_page":{"text":"SP"}}`},
	{"PUT", "/projects/ecookbook/wiki/CookBook_documentation.json", `{"wiki_page":{"text":"SP"}}`},
	{"PUT", "/projects/ecookbook/wiki/SPnew.json", `{"wiki_page":{"text":"SP","parent_title":"CookBook_documentation"}}`},
	{"POST", "/projects/2/news.json", `{"news":{"title":"SP","description":"SP"}}`},
	{"POST", "/projects/1/news.json", `{"news":{"title":"SP","description":"SP"}}`},
	{"PUT", "/news/3.json", `{"news":{"title":"SP"}}`},
	{"PUT", "/news/1.json", `{"news":{"title":"SP"}}`},
	{"PUT", "/users/2.json", `{"user":{"firstname":"SP"}}`},
	{"PUT", "/users/current.json", `{"user":{"admin":true}}`},
	{"PUT", "/my/account.json", `{"user":{"admin":true,"login":"pwned"}}`},
	{"POST", "/groups/10/users.json", `{"user_id":7}`},
	{"PUT", "/attachments/7.json", `{"attachment":{"filename":"x.zip"}}`},
	{"PUT", "/attachments/1.json", `{"attachment":{"description":"SP"}}`},
	{"PUT", "/attachments/15.json", `{"attachment":{"description":"SP"}}`},
	{"PUT", "/journals/1.json", `{"journal":{"notes":"SP"}}`},
	{"PUT", "/journals/5.json", `{"journal":{"notes":"SP"}}`},
	{"PUT", "/journals/{privnote}.json", `{"journal":{"notes":"SP","private_notes":false}}`},
	{"DELETE", "/time_entries/1.json", ``},
	{"DELETE", "/relations/2.json", ``},
	{"DELETE", "/issues/4.json", ``},
	{"DELETE", "/issues/14.json", ``},
	{"DELETE", "/attachments/{upload_jsmith_id}.json", ``},
}

func buildSpecialJobs() []job {
	ids := []ident{apiIdents[0], apiIdents[1], apiIdents[2], {Name: "jsmith/api", Login: "jsmith", Pass: "jsmith", API: true}}
	var jobs, dels []job
	for _, w := range specialWrites {
		for _, id := range ids {
			j := job{Ident: id, Method: w[0], Route: "special:" + w[1], Path: w[1], JSON: w[2], Phase: "special"}
			if j.JSON == "" {
				j.JSON = "{}"
			}
			if w[0] == "DELETE" {
				dels = append(dels, j)
			} else {
				jobs = append(jobs, j)
			}
		}
	}
	return append(jobs, dels...)
}

// ---- setup ----

func (t *target) apiReq(login, pass, method, path, body string) (int, []byte, http.Header) {
	r, _ := http.NewRequest(method, t.base+path, strings.NewReader(body))
	r.SetBasicAuth(login, pass)
	if strings.HasPrefix(path, "/uploads") {
		r.Header.Set("Content-Type", "application/octet-stream")
	} else {
		r.Header.Set("Content-Type", "application/json")
	}
	resp, err := newClient().Do(r)
	if err != nil {
		log.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, resp.Header
}

func (t *target) setup() {
	// 1. 私的注記（公開プロジェクトの issue 1 と、非公開プロジェクトの issue 4）
	st, _, _ := t.apiReq("admin", "admin", "PUT", "/issues/1.json", `{"issue":{"notes":"SECRET-PRIVNOTE on issue 1","private_notes":true}}`)
	log.Printf("%s: private note issue1 -> %d", t.name, st)
	st, _, _ = t.apiReq("admin", "admin", "PUT", "/issues/4.json", `{"issue":{"notes":"SECRET-PRIVPROJ-NOTE on issue 4"}}`)
	log.Printf("%s: note issue4 -> %d", t.name, st)
	// 2. 最小ロール（権限なし）を作り rhill を eCookbook に参加させる
	cl := t.clients["admin"]
	_, b, _ := t.get(cl, "/roles/new")
	tok := extractToken(b)
	form := url.Values{"authenticity_token": {tok}, "role[name]": {"Minimal"}, "role[assignable]": {"0"},
		"role[permissions][]": {""}, "role[issues_visibility]": {"default"}, "role[users_visibility]": {"members_of_visible_projects"},
		"role[time_entries_visibility]": {"own"}}
	resp, err := cl.PostForm(t.base+"/roles", form)
	if err != nil {
		log.Fatal(err)
	}
	resp.Body.Close()
	log.Printf("%s: create role -> %d %s", t.name, resp.StatusCode, resp.Header.Get("Location"))
	_, b, _ = t.apiReq("admin", "admin", "GET", "/roles.json", "")
	var roles struct {
		Roles []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"roles"`
	}
	_ = json.Unmarshal(b, &roles)
	rid := 0
	for _, r := range roles.Roles {
		if r.Name == "Minimal" {
			rid = r.ID
		}
	}
	st, b, _ = t.apiReq("admin", "admin", "POST", "/projects/1/memberships.json", fmt.Sprintf(`{"membership":{"user_id":4,"role_ids":[%d]}}`, rid))
	log.Printf("%s: role id %d, membership -> %d %s", t.name, rid, st, b)
	// 3. jsmith のインポート
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("authenticity_token", t.tokens["jsmith"])
	_ = mw.WriteField("type", "IssueImport")
	_ = mw.WriteField("project_id", "onlinestore")
	fw, _ := mw.CreateFormFile("file", "import.csv")
	_, _ = fw.Write([]byte("subject,description\nSECRET-IMPORT-ROW,SECRET-IMPORT-DESC\n"))
	mw.Close()
	req, _ := http.NewRequest("POST", t.base+"/imports", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err = t.clients["jsmith"].Do(req)
	if err != nil {
		log.Fatal(err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	log.Printf("%s: import -> %d %s", t.name, resp.StatusCode, loc)
	if m := regexp.MustCompile(`/imports/([^/]+)/settings`).FindStringSubmatch(loc); m != nil {
		t.vars["import"] = m[1]
	}
}

// discoverVars は setup 後の ID（私的注記の journal id など）を取得する。
func (t *target) discoverVars() {
	_, b, _ := t.apiReq("admin", "admin", "GET", "/issues/1.json?include=journals", "")
	var is struct {
		Issue struct {
			Journals []struct {
				ID    int    `json:"id"`
				Notes string `json:"notes"`
			} `json:"journals"`
		} `json:"issue"`
	}
	_ = json.Unmarshal(b, &is)
	for _, j := range is.Issue.Journals {
		if strings.HasPrefix(j.Notes, "SECRET-PRIVNOTE") {
			t.vars["privnote"] = fmt.Sprint(j.ID)
		}
	}
	_, b, _ = t.apiReq("admin", "admin", "GET", "/issues/4.json?include=journals", "")
	is.Issue.Journals = nil
	_ = json.Unmarshal(b, &is)
	for _, j := range is.Issue.Journals {
		if strings.HasPrefix(j.Notes, "SECRET-PRIVPROJ") {
			t.vars["privprojnote"] = fmt.Sprint(j.ID)
		}
	}
	if t.vars["import"] == "" {
		// 既存のインポート（setup 済みの場合）は jsmith の settings ページ等から分からないので固定値を試す
		t.vars["import"] = "1"
	}
	for login, k := range t.apiKeys {
		t.vars["apikey_"+login] = k
	}
	// フィードキー
	for _, login := range []string{"dlopper"} {
		_, b, _ := t.get(t.clients[login], "/my/account")
		if m := regexp.MustCompile(`key=([0-9a-f]{40})`).FindSubmatch(b); m != nil {
			t.vars["atomkey_"+login] = string(m[1])
		} else {
			_, b, _ := t.get(t.clients[login], "/projects/ecookbook/issues")
			if m := regexp.MustCompile(`key=([0-9a-f]{40})`).FindSubmatch(b); m != nil {
				t.vars["atomkey_"+login] = string(m[1])
			}
		}
	}
	// jsmith のアップロード（他人のトークンでの添付を試すため）
	st, b, _ := t.apiReq("jsmith", "jsmith", "POST", "/uploads.json?filename=jsmith.txt", "SECRET-UPLOAD-CONTENT")
	var up struct {
		Upload struct {
			ID    int    `json:"id"`
			Token string `json:"token"`
		} `json:"upload"`
	}
	_ = json.Unmarshal(b, &up)
	t.vars["upload_jsmith"] = up.Upload.Token
	t.vars["upload_jsmith_id"] = fmt.Sprint(up.Upload.ID)
	log.Printf("%s: upload -> %d", t.name, st)
	log.Printf("%s vars: %v", t.name, t.vars)
}

func main() {
	refURL := flag.String("ref", "http://127.0.0.1:4051", "reference Redmine")
	candURL := flag.String("cand", "http://127.0.0.1:4151", "buropher candidate")
	routesFile := flag.String("routes", "internal/server/testdata/redmine-routes.json", "routes json")
	outDir := flag.String("out", "secaudit-out", "output dir")
	phases := flag.String("phase", "read", "comma separated phases: read,write")
	doSetup := flag.Bool("setup", false, "create extra fixtures on both servers (once after reset)")
	conc := flag.Int("c", 6, "concurrency")
	filter := flag.String("filter", "", "only paths matching this regexp")
	identFilter := flag.String("ident", "", "only idents matching this regexp")
	dry := flag.Bool("dry", false, "print jobs and exit")
	flag.Parse()

	var routes []route
	rb, err := os.ReadFile(*routesFile)
	if err != nil {
		log.Fatal(err)
	}
	if err := json.Unmarshal(rb, &routes); err != nil {
		log.Fatal(err)
	}
	targets := []*target{
		{name: "ref", base: strings.TrimRight(*refURL, "/")},
		{name: "cand", base: strings.TrimRight(*candURL, "/")},
	}
	for _, t := range targets {
		t.clients, t.tokens, t.apiKeys, t.vars = map[string]*http.Client{}, map[string]string{}, map[string]string{}, map[string]string{}
		for _, l := range [][2]string{{"", ""}, {"someone", "foo"}, {"rhill", "foo"}, {"dlopper", "foo"}, {"jsmith", "jsmith"}, {"admin", "admin"}} {
			if err := t.login(l[0], l[1]); err != nil {
				log.Fatal(err)
			}
		}
		varsFile := filepath.Join(*outDir, "vars-"+t.name+".json")
		if *doSetup {
			t.setup()
			_ = os.MkdirAll(*outDir, 0o755)
			b, _ := json.Marshal(map[string]string{"import": t.vars["import"]})
			_ = os.WriteFile(varsFile, b, 0o644)
		} else if b, err := os.ReadFile(varsFile); err == nil {
			_ = json.Unmarshal(b, &t.vars)
		}
		t.discoverVars()
	}
	_ = os.MkdirAll(*outDir, 0o755)
	var fre, ire *regexp.Regexp
	if *filter != "" {
		fre = regexp.MustCompile(*filter)
	}
	if *identFilter != "" {
		ire = regexp.MustCompile(*identFilter)
	}
	for _, ph := range strings.Split(*phases, ",") {
		var jobs []job
		switch ph {
		case "read":
			jobs = buildReadJobs(routes)
		case "special":
			jobs = buildSpecialJobs()
		case "write":
			jobs = buildWriteJobs(routes)
		default:
			log.Fatalf("unknown phase %s", ph)
		}
		var fj []job
		for _, j := range jobs {
			if (fre == nil || fre.MatchString(j.Path)) && (ire == nil || ire.MatchString(j.Ident.Name)) {
				fj = append(fj, j)
			}
		}
		jobs = fj
		log.Printf("phase %s: %d jobs", ph, len(jobs))
		if *dry {
			for _, j := range jobs {
				fmt.Println(j.Ident.Name, j.Method, j.Path)
			}
			continue
		}
		run(targets, jobs, *conc, filepath.Join(*outDir, ph))
	}
}

func run(targets []*target, jobs []job, conc int, prefix string) {
	ref, cand := targets[0], targets[1]
	rs, cs := ref.secretsFor(), cand.secretsFor()
	// 既存の結果があれば実行済みのジョブを飛ばして追記する（中断からの再開）
	if b, err := os.ReadFile(prefix + "-results.jsonl"); err == nil {
		seen := map[string]bool{}
		for _, l := range bytes.Split(b, []byte("\n")) {
			var r record
			if json.Unmarshal(l, &r) == nil {
				seen[r.Ident+" "+r.Method+" "+r.Path] = true
			}
		}
		var rest []job
		for _, j := range jobs {
			if !seen[j.Ident.Name+" "+j.Method+" "+j.Path] {
				rest = append(rest, j)
			}
		}
		log.Printf("resume: %d done, %d remaining", len(jobs)-len(rest), len(rest))
		jobs = rest
	}
	out, err := os.OpenFile(prefix+"-results.jsonl", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Fatal(err)
	}
	defer out.Close()
	fout, _ := os.OpenFile(prefix+"-findings.txt", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	defer fout.Close()
	var mu sync.Mutex
	done := 0
	perIdent := map[string]int{}
	// 書き込みは DELETE を最後にまとめ、その前で全件の完了を待つ（削除で後続の対象が消えないように）
	var batches [][]job
	var cur []job
	for i, j := range jobs {
		if i > 0 && j.Method == "DELETE" && jobs[i-1].Method != "DELETE" {
			batches = append(batches, cur)
			cur = nil
		}
		cur = append(cur, j)
	}
	batches = append(batches, cur)
	for _, batch := range batches {
		ch := make(chan job)
		var wg sync.WaitGroup
		for range conc {
			wg.Go(func() {
				for j := range ch {
					r := ref.do(j, rs)
					c := cand.do(j, cs)
					rec := record{Phase: j.Phase, Ident: j.Ident.Name, Method: j.Method, Route: j.Route, Path: j.Path, Ref: r, Cand: c}
					rec.Flag = classify(j, r, c)
					b, _ := json.Marshal(rec)
					mu.Lock()
					out.Write(append(b, '\n'))
					if strings.Contains(rec.Flag, "ALLOW") || strings.Contains(rec.Flag, "LEAK") || strings.Contains(rec.Flag, "COUNT") {
						fmt.Fprintf(fout, "%-22s %-6s %-70s ref=%d cand=%d %s\n", rec.Ident, rec.Method, rec.Path, r.Status, c.Status, rec.Flag)
					}
					done++
					perIdent[j.Ident.Name]++
					if done%1000 == 0 {
						log.Printf("%d/%d", done, len(jobs))
					}
					mu.Unlock()
				}
			})
		}
		for _, j := range batch {
			ch <- j
		}
		close(ch)
		wg.Wait()
	}
	log.Printf("per ident: %v", perIdent)
}
