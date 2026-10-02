package server_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/db"
)

// チケットの参照系（issues#index / show、issue_tab、journals#index / diff、context_menus#issues、
// Atom / API / CSV）の互換テスト。
// testdata/issues_read/ の期待値は共用の参照 Redmine（3998）から
// `go run ./tools/compat fetch -base http://127.0.0.1:3998 -raw -user <user> <path>` で取得し、
// ベース URL を {{BASE}} に置換したもの（各ケースの user と path で取得する）。
//
// HTML はタグ間の空白を詰めて比較する（ERB と Go テンプレートの改行位置の差は互換テストの正規化でも無視される）。
// ヘッダーのジャンプボックス（最近使ったプロジェクト）は参照インスタンスの利用履歴に依存するため、
// <head> と <div id="main"> 以降だけを比較する。

var (
	wsBetweenTagsRe = regexp.MustCompile(`>\s+<`)
	wsRunRe         = regexp.MustCompile(`\s+`)
)

// squeezeHTML はタグ間の空白を除き、連続する空白を 1 つにする。
func squeezeHTML(s string) string {
	s = wsBetweenTagsRe.ReplaceAllString(s, "><")
	return strings.TrimSpace(wsRunRe.ReplaceAllString(s, " "))
}

// pageParts は <head>...</head> と <div id="main" ...> から <div id="footer"> の手前まで。
func pageParts(s string) string {
	var b strings.Builder
	if i, j := strings.Index(s, "<head>"), strings.Index(s, "</head>"); i >= 0 && j > i {
		b.WriteString(s[i : j+len("</head>")])
	}
	if i := strings.Index(s, `<div id="main"`); i >= 0 {
		rest := s[i:]
		if j := strings.Index(rest, `<div id="footer">`); j >= 0 {
			rest = rest[:j]
		}
		b.WriteString(rest)
	} else {
		b.WriteString(s)
	}
	return b.String()
}

var issuesImgDigestRe = regexp.MustCompile(`-[0-9a-f]{8}\.(png|gif)`)

func normalizeIssuesRead(s, base string, html bool) string {
	s = normalizeAdmin(s, base)
	s = issuesImgDigestRe.ReplaceAllString(s, "-DIGEST.$1")
	if html {
		if strings.Contains(s, "<html") {
			s = pageParts(s)
		}
		s = squeezeHTML(s)
	}
	return s
}

func compareIssuesGolden(t *testing.T, name, got, base string) {
	t.Helper()
	raw, err := os.ReadFile("testdata/issues_read/" + name)
	if err != nil {
		t.Fatal(err)
	}
	html := strings.HasSuffix(name, ".html") || strings.HasSuffix(name, ".atom")
	want := normalizeIssuesRead(string(raw), "{{BASE}}", html)
	g := normalizeIssuesRead(got, base, html)
	if g == want {
		return
	}
	// 最初の差分の前後を表示する
	n := 0
	for n < len(g) && n < len(want) && g[n] == want[n] {
		n++
	}
	from := max(0, n-200)
	t.Fatalf("%s: differs at byte %d\n got: %s\nwant: %s", name, n, g[from:min(len(g), n+300)], want[from:min(len(want), n+300)])
}

// alignIssuesFixtures は testfixtures で投入されないか id が異なる行を参照 Redmine の DB に合わせる
// （reactions は未対応のフィクスチャ、watchers は Rails が名前から生成する id で並びが変わる）。
func alignIssuesFixtures(t *testing.T, d *db.DB) {
	t.Helper()
	ctx := context.Background()
	now := "2026-01-15T12:00:00.000000Z"
	stmts := []string{
		`DELETE FROM watchers WHERE watchable_kind = 'issue'`,
		`INSERT INTO watchers (id, watchable_kind, watchable_id, principal_id) VALUES (362429562, 'issue', 2, 1), (999578971, 'issue', 2, 3)`,
	}
	for _, r := range [][4]any{{1, "issue", 1, 1}, {2, "issue", 1, 2}, {3, "issue", 1, 3}, {4, "journal", 1, 2}, {5, "issue", 6, 2}, {6, "journal", 4, 2}} {
		stmts = append(stmts, fmt.Sprintf(`INSERT INTO reactions (id, reactable_kind, reactable_id, user_id, created_at, updated_at) VALUES (%d, '%s', %d, %d, '%s', '%s')`,
			r[0], r[1], r[2], r[3], now, now))
	}
	for _, q := range stmts {
		if _, err := d.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
}

// TestIssuesReadPagesMatchRedmine はチケットの参照系の画面・フィードが参照 Redmine と一致することを確認する。
func TestIssuesReadPagesMatchRedmine(t *testing.T) {
	ts, d := newFixtureServer(t)
	alignIssuesFixtures(t, d)
	clients := map[string]*http.Client{
		"admin":     login(t, ts, "admin", "admin"),
		"jsmith":    login(t, ts, "jsmith", "jsmith"),
		"dlopper":   login(t, ts, "dlopper", "foo"),
		"anonymous": newClient(t),
	}
	cases := []struct {
		name, user, path string
		status           int
	}{
		{"index_admin.html", "admin", "/issues", 200},
		{"index_all_sorted_admin.html", "admin", "/issues?set_filter=1&f[]=status_id&op[status_id]=*&sort=priority:desc,id", 200},
		{"index_grouped_totals_admin.html", "admin", "/issues?set_filter=1&group_by=status&t[]=estimated_hours&t[]=spent_hours&f[]=status_id&op[status_id]=*", 200},
		{"index_columns_admin.html", "admin", "/issues?set_filter=1&c[]=project&c[]=tracker&c[]=subject&c[]=author&c[]=category&c[]=fixed_version&c[]=start_date&c[]=due_date&c[]=estimated_hours&c[]=done_ratio&c[]=created_on&c[]=closed_on&c[]=relations&c[]=attachments&c[]=watcher_users&c[]=cf_1&c[]=cf_2&c[]=cf_6&c[]=description&c[]=last_notes&f[]=status_id&op[status_id]=*", 200},
		{"index_project_jsmith.html", "jsmith", "/projects/ecookbook/issues?set_filter=1&f[]=status_id&op[status_id]=*&group_by=fixed_version&per_page=2&page=2", 200},
		{"index_anonymous.html", "anonymous", "/issues", 200},
		{"index_invalid_admin.html", "admin", "/issues?set_filter=1&f[]=due_date&op[due_date]=%3E%3C&v[due_date][]=foo", 200},
		{"show_1_admin.html", "admin", "/issues/1", 200},
		{"show_2_jsmith.html", "jsmith", "/issues/2", 200},
		{"show_3_admin.html", "admin", "/issues/3", 200},
		{"show_14_jsmith.html", "jsmith", "/issues/14", 200},
		{"show_6_dlopper.html", "dlopper", "/issues/6", 403},
		{"show_1_anonymous.html", "anonymous", "/issues/1", 200},
		{"context_menu_1_admin.html", "admin", "/issues/context_menu?ids[]=1&back_url=/issues", 200},
		{"context_menu_bulk_jsmith.html", "jsmith", "/issues/context_menu?ids[]=1&ids[]=2&back_url=/issues", 200},
		{"journal_diff_admin.html", "admin", "/journals/3/diff", 200},
		{"show_1_admin.atom", "admin", "/issues/1.atom", 200},
		{"index_admin.atom", "admin", "/issues.atom?set_filter=1&f[]=status_id&op[status_id]=*&sort=id", 200},
		{"show_2_admin.json", "admin", "/issues/2.json?include=children,attachments,relations,changesets,journals,allowed_statuses", 200},
		{"index_admin.xml", "admin", "/issues.xml?limit=3&offset=2", 200},
		{"index_admin.csv", "admin", "/issues.csv?set_filter=1&f[]=status_id&op[status_id]=*&c[]=project&c[]=tracker&c[]=parent&c[]=status&c[]=priority&c[]=subject&c[]=author&c[]=assigned_to&c[]=updated_on&c[]=category&c[]=fixed_version&c[]=start_date&c[]=due_date&c[]=estimated_hours&c[]=estimated_remaining_hours&c[]=total_estimated_hours&c[]=spent_hours&c[]=total_spent_hours&c[]=done_ratio&c[]=created_on&c[]=closed_on&c[]=last_updated_by&c[]=relations&c[]=attachments&c[]=cf_2&c[]=cf_1&c[]=cf_6&c[]=cf_8&c[]=is_private&c[]=description&c[]=last_notes", 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 別のケースで保存されたセッションのクエリ（前後のチケットのリンク）の影響を避けるため、ログインし直す
			c := clients[tc.user]
			switch tc.user {
			case "admin":
				c = login(t, ts, "admin", "admin")
			case "jsmith":
				c = login(t, ts, "jsmith", "jsmith")
			case "dlopper":
				c = login(t, ts, "dlopper", "foo")
			default:
				c = newClient(t)
			}
			var res *http.Response
			var body string
			if strings.Contains(tc.path, ".json") || strings.Contains(tc.path, ".xml") {
				// API はセッションを使わない（HTTP Basic で認証する。参照の取得も同じ）
				res, body = getBasic(t, ts.URL+tc.path, tc.user)
			} else {
				res, body = get(t, c, ts.URL+tc.path)
			}
			if res.StatusCode != tc.status {
				t.Fatalf("GET %s: status %d", tc.path, res.StatusCode)
			}
			compareIssuesGolden(t, tc.name, body, ts.URL)
		})
	}
}

// getBasic は HTTP Basic 認証で GET する。
func getBasic(t *testing.T, u, user string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		t.Fatal(err)
	}
	pw := user
	if user == "dlopper" {
		pw = "foo"
	}
	req.SetBasicAuth(user, pw)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

// TestIssuesReadStatuses は権限・存在しないレコード・形式の扱い（ステータスコード）を確認する。
func TestIssuesReadStatuses(t *testing.T) {
	ts, _ := newFixtureServer(t)
	admin := login(t, ts, "admin", "admin")
	anon := newClient(t)
	cases := []struct {
		c      *http.Client
		path   string
		status int
	}{
		{admin, "/issues/999", 404},
		{admin, "/projects/nonexistent/issues", 404},
		{admin, "/issues?query_id=999", 404},
		{anon, "/issues/6", 302},                      // 非公開プロジェクトのチケット → ログインへ
		{anon, "/projects/private-child/issues", 302}, // 非公開プロジェクト
		{admin, "/issues/1/tab/time_entries", 422},    // XHR 以外
		{admin, "/journals/999/diff", 404},
		{admin, "/issues/context_menu?ids[]=999", 404},
		{admin, "/issues.atom?set_filter=1&f[]=due_date&op[due_date]=%3E%3C&v[due_date][]=foo", 422},
		{admin, "/issues.json?set_filter=1&f[]=due_date&op[due_date]=%3E%3C&v[due_date][]=foo", 422},
	}
	for _, tc := range cases {
		res, _ := get(t, tc.c, ts.URL+tc.path)
		if res.StatusCode != tc.status {
			t.Errorf("GET %s: status %d, want %d", tc.path, res.StatusCode, tc.status)
		}
	}
}

// TestIssuesIndexSessionQuery は一覧のクエリがセッションに保存され、詳細の前後リンクに使われることを確認する。
func TestIssuesIndexSessionQuery(t *testing.T) {
	ts, _ := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")
	get(t, c, ts.URL+"/issues?set_filter=1&f[]=status_id&op[status_id]=*&sort=id")
	// set_filter なしの一覧はセッションの条件（全ステータス・id 昇順）を使う
	_, body := get(t, c, ts.URL+"/issues")
	if !strings.Contains(body, `<input type="hidden" name="sort" value="id" autocomplete="off" />`) {
		t.Error("session sort not restored")
	}
	_, body = get(t, c, ts.URL+"/issues/2")
	for _, want := range []string{`title="#1"`, `title="#3"`, `<span class="position">`, `2 of 14`} {
		if !strings.Contains(body, want) {
			t.Errorf("issue 2 prev/next: %q not found", want)
		}
	}
}
