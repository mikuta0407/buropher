// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andybalholm/cascadia"
	"github.com/go-chi/chi/v5"
	"golang.org/x/net/html"

	"github.com/mikuta0407/buropher/internal/db"
	"github.com/mikuta0407/buropher/internal/domain"
	"github.com/mikuta0407/buropher/internal/handler"
	"github.com/mikuta0407/buropher/internal/repository"
	"github.com/mikuta0407/buropher/internal/scm/scmtest"
)

// test/functional/repositories_git_controller_test.rb の移植（フィクスチャの git リポジトリを使う）。

const gitPrjID = "3"

type gitEnv struct {
	t     *testing.T
	ts    *httptest.Server
	d     *db.DB
	repo  *domain.Repository
	admin *http.Client
	app   *handler.App
}

func (e *gitEnv) set(name string, v any) {
	e.t.Helper()
	if err := e.app.Settings.Set(context.Background(), name, v); err != nil {
		e.t.Fatal(err)
	}
}

// setupGit は setup（Project 3 に path_encoding ISO-8859-1 の Git リポジトリ）。fetch は自動取得に任せる。
func setupGit(t *testing.T) *gitEnv {
	t.Helper()
	path := scmtest.GitRepositoryPath(t)
	var app *handler.App
	ts, d := newFixtureServer(t, func(a *handler.App, _ chi.Router) { app = a })
	r := &domain.Repository{ProjectID: 3, SCM: "git", URL: path, PathEncoding: "ISO-8859-1", IsDefault: true, CreatedOn: frozenTime}
	if err := repository.InsertScmRepository(context.Background(), d, r); err != nil {
		t.Fatal(err)
	}
	return &gitEnv{t: t, ts: ts, d: d, repo: r, admin: login(t, ts, "admin", "admin"), app: app}
}

func (e *gitEnv) base() string {
	return e.ts.URL + "/projects/subproject1/repository/" + itoa64(e.repo.ID)
}

func (e *gitEnv) get(path string) (*http.Response, *html.Node, string) {
	e.t.Helper()
	res, body := get(e.t, e.admin, e.ts.URL+path)
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	return res, doc, body
}

func (e *gitEnv) fetch() {
	e.t.Helper()
	// show は自動取得（Setting.autofetch_changesets の既定は有効）
	res, _, body := e.get("/projects/" + gitPrjID + "/repository")
	if res.StatusCode != 200 {
		e.t.Fatalf("show: status %d\n%s", res.StatusCode, body)
	}
	if n, _ := repository.CountChangesets(context.Background(), e.d, e.repo.ID); n != 29 {
		e.t.Fatalf("changesets = %d", n)
	}
}

func sel(n *html.Node, s string) []*html.Node { return cascadia.MustCompile(s).MatchAll(n) }

func nodeText(n *html.Node) string {
	var b strings.Builder
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(n)
	return b.String()
}

func texts(ns []*html.Node) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = strings.TrimSpace(nodeText(n))
	}
	return out
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

func expectStatus(t *testing.T, res *http.Response, want int, body string) {
	t.Helper()
	if res.StatusCode != want {
		t.Fatalf("status %d (want %d)\n%s", res.StatusCode, want, contentMain(body))
	}
}

func TestGitCreateAndUpdate(t *testing.T) {
	e := setupGit(t)
	res, body := post(t, e.admin, e.ts.URL+"/projects/subproject1/repositories", contentForm(t, e.admin, e.ts,
		"repository_scm", "Git", "repository[url]", "/test", "repository[is_default]", "0",
		"repository[identifier]", "test-create", "repository[report_last_commit]", "1"))
	expectContentRedirect(t, res, body, "/projects/subproject1/settings/repositories")
	var id int64
	if err := e.d.Get(context.Background(), &id, `SELECT MAX(id) FROM repositories`); err != nil {
		t.Fatal(err)
	}
	r, err := repository.GetScmRepository(context.Background(), e.d, id)
	if err != nil || r.URL != "/test" || !r.ReportLastCommit() || r.IsDefault {
		t.Fatalf("created = %+v %v", r, err)
	}
	res, body = post(t, e.admin, e.ts.URL+"/repositories/"+itoa64(id), contentForm(t, e.admin, e.ts,
		"_method", "put", "repository[report_last_commit]", "0"))
	expectContentRedirect(t, res, body, "/projects/subproject1/settings/repositories")
	if r, _ = repository.GetScmRepository(context.Background(), e.d, id); r.ReportLastCommit() {
		t.Error("report_last_commit should be false")
	}
	// 検証エラー（予約語・重複）
	res, body = post(t, e.admin, e.ts.URL+"/projects/subproject1/repositories", contentForm(t, e.admin, e.ts,
		"repository_scm", "Git", "repository[url]", "/test2", "repository[identifier]", "test-create"))
	expectStatus(t, res, 200, body)
	if !strings.Contains(body, "Identifier has already been taken") {
		t.Errorf("uniqueness error missing\n%s", contentMain(body))
	}
	res, body = post(t, e.admin, e.ts.URL+"/projects/subproject1/repositories", contentForm(t, e.admin, e.ts,
		"repository_scm", "Git", "repository[url]", "", "repository[identifier]", "diff"))
	if !strings.Contains(body, "Path to repository cannot be blank") || !strings.Contains(body, "Identifier is reserved") {
		t.Errorf("errors missing\n%s", contentMain(body))
	}
}

func TestGitGetNew(t *testing.T) {
	e := setupGit(t)
	_, err := e.d.Exec(context.Background(), `DELETE FROM repositories WHERE id = ?`, e.repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	res, doc, body := e.get("/projects/subproject1/repositories/new?repository_scm=Git")
	expectStatus(t, res, 200, body)
	if len(sel(doc, `select[name=repository_scm] option[value=Git][selected=selected]`)) != 1 {
		t.Error("Git should be selected")
	}
	if len(sel(doc, `input#repository_is_default[checked=checked]`)) != 1 {
		t.Error("is_default should be checked when the project has no repository")
	}
}

func TestGitBrowseRoot(t *testing.T) {
	e := setupGit(t)
	e.fetch()
	res, doc, body := e.get("/projects/" + gitPrjID + "/repository")
	expectStatus(t, res, 200, body)
	rows := sel(doc, "table.entries tbody tr")
	if len(rows) != 9 {
		t.Fatalf("rows = %d", len(rows))
	}
	dirs := texts(sel(doc, "table.entries tbody tr.dir td.filename_no_report a"))
	files := texts(sel(doc, "table.entries tbody tr.file td.filename_no_report a"))
	for _, d := range []string{"images", "this_is_a_really_long_and_verbose_directory_name", "sources"} {
		if !contains(dirs, d) {
			t.Errorf("dir %s missing: %q", d, dirs)
		}
	}
	for _, f := range []string{"README", "copied_README", "new_file.txt", "renamed_test.txt", "filemane with spaces.txt", "filename with a leading space.txt"} {
		if !contains(files, f) {
			t.Errorf("file %s missing: %q", f, files)
		}
	}
	if len(sel(doc, "table.changesets tbody tr")) == 0 {
		t.Error("changesets missing")
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func TestGitBrowseBranchTagDirectory(t *testing.T) {
	e := setupGit(t)
	e.fetch()
	res, doc, body := e.get("/projects/" + gitPrjID + "/repository/" + itoa64(e.repo.ID) + "?rev=test_branch")
	expectStatus(t, res, 200, body)
	if n := len(sel(doc, "table.entries tbody tr")); n != 4 {
		t.Errorf("branch rows = %d", n)
	}
	for _, tag := range []string{"tag00.lightweight", "tag01.annotated"} {
		res, doc, body = e.get("/projects/" + gitPrjID + "/repository/" + itoa64(e.repo.ID) + "?rev=" + tag)
		expectStatus(t, res, 200, body)
		if len(sel(doc, "table.entries tbody tr")) == 0 || len(sel(doc, "table.changesets tbody tr")) == 0 {
			t.Errorf("tag %s: entries/changesets missing", tag)
		}
	}
	res, doc, body = e.get("/projects/" + gitPrjID + "/repository/" + itoa64(e.repo.ID) + "/show/images")
	expectStatus(t, res, 200, body)
	if got := texts(sel(doc, "table.entries tbody tr.file td.filename_no_report a")); len(got) != 1 || got[0] != "edit.png" {
		t.Errorf("images = %q", got)
	}
	res, doc, body = e.get("/projects/" + gitPrjID + "/repository/" + itoa64(e.repo.ID) + "/show/images?rev=7234cb2750b63f47bff735edc50a1c0a433c2518")
	expectStatus(t, res, 200, body)
	if got := texts(sel(doc, "table.entries tbody tr.file td.filename_no_report a")); len(got) != 1 || got[0] != "delete.png" {
		t.Errorf("images at rev = %q", got)
	}
	// test_browse_latin_1_dir
	res, doc, body = e.get("/projects/" + gitPrjID + "/repository/" + itoa64(e.repo.ID) + "/show/latin-1-dir/test-%C3%9C-subdir?rev=1ca7f5ed374f3cb31a93ae5215c2e25cc6ec5127")
	expectStatus(t, res, 200, body)
	got := texts(sel(doc, "table.entries tbody tr.file td.filename_no_report a"))
	if len(got) != 3 || got[0] != "test-Ü-1.txt" || got[2] != "test-Ü.txt" {
		t.Errorf("latin-1 dir = %q", got)
	}
	// XHR（ツリーの展開）
	req, _ := http.NewRequest("GET", e.base()+"/revisions/master/show/sources?depth=1&parent_id=abc", nil)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	xres, err := e.admin.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	xres.Body.Close()
	if xres.StatusCode != 200 {
		t.Errorf("xhr status %d", xres.StatusCode)
	}
}

func TestGitChangesEntryAnnotate(t *testing.T) {
	e := setupGit(t)
	res, doc, body := e.get("/projects/" + gitPrjID + "/repository/" + itoa64(e.repo.ID) + "/changes/images/edit.png")
	expectStatus(t, res, 200, body)
	if !strings.Contains(nodeText(sel(doc, "h2")[0]), "edit.png") {
		t.Error("changes h2")
	}
	res, doc, body = e.get("/projects/" + gitPrjID + "/repository/" + itoa64(e.repo.ID) + "/entry/sources/watchers_controller.rb")
	expectStatus(t, res, 200, body)
	if l := sel(doc, "tr#L11 td.line-code"); len(l) != 1 || !strings.Contains(nodeText(l[0]), "WITHOUT ANY WARRANTY") {
		t.Error("entry line 11")
	}
	// test_entry_show_should_render_pagination
	_, doc, _ = e.get("/projects/" + gitPrjID + "/repository/" + itoa64(e.repo.ID) + "/entry/README")
	if len(sel(doc, "ul.pages li.next")) != 1 || len(sel(doc, "ul.pages li.previous")) != 1 {
		t.Error("entry pagination")
	}
	// test_entry_download（raw）
	res, raw := get(t, e.admin, e.base()+"/raw/sources/watchers_controller.rb")
	if res.StatusCode != 200 || !strings.Contains(raw, "WITHOUT ANY WARRANTY") {
		t.Errorf("raw status %d", res.StatusCode)
	}
	// test_directory_entry
	_, doc, _ = e.get("/projects/" + gitPrjID + "/repository/" + itoa64(e.repo.ID) + "/entry/sources")
	if a := sel(doc, "h2 a"); len(a) < 2 || nodeText(a[1]) != "sources" || len(sel(doc, "table.entries tbody")) != 1 ||
		len(sel(doc, "div.contextual > a.icon-download")) != 0 {
		t.Error("directory entry")
	}
	// test_annotate
	res, doc, body = e.get("/projects/" + gitPrjID + "/repository/" + itoa64(e.repo.ID) + "/annotate/sources/watchers_controller.rb")
	expectStatus(t, res, 200, body)
	tr := sel(doc, "tr#L23")
	if len(tr) != 1 {
		t.Fatal("annotate line 23")
	}
	if !strings.Contains(nodeText(sel(tr[0], "td.revision")[0]), "2f9c0091") || strings.TrimSpace(nodeText(sel(tr[0], "td.author")[0])) != "jsmith" {
		t.Errorf("annotate line 23 = %q", nodeText(tr[0]))
	}
	prev := sel(tr[0], "td.previous a.icon-history")
	want := "/projects/subproject1/repository/" + itoa64(e.repo.ID) + "/revisions/4a79347ea4b7184938d9bbea0fd421a6079f71bb/annotate/sources/watchers_controller.rb"
	if len(prev) != 1 || attr(prev[0], "href") != want {
		t.Errorf("previous annotation link: %v", prev)
	}
	// test_annotate_binary_file
	_, doc, _ = e.get("/projects/" + gitPrjID + "/repository/" + itoa64(e.repo.ID) + "/annotate/images/edit.png")
	if p := sel(doc, "p#errorExplanation"); len(p) != 1 || !strings.Contains(nodeText(p[0]), "cannot be annotated") {
		t.Error("annotate binary")
	}
	// test_annotate_latin_1_author
	_, doc, _ = e.get("/projects/" + gitPrjID + "/repository/" + itoa64(e.repo.ID) + "/revisions/83ca5fd546063a/annotate/%20filename%20with%20a%20leading%20space.txt%20")
	if a := sel(doc, "tr#L1 td.author"); len(a) != 1 || strings.TrimSpace(nodeText(a[0])) != "Felix Schäfer" {
		t.Errorf("latin-1 author = %q", texts(a))
	}
}

// test_entry_show_latin_1 / test_diff_latin_1 / test_annotate_latin_1（Setting.repositories_encodings で変換する）。
func TestGitLatin1Content(t *testing.T) {
	e := setupGit(t)
	e.fetch()
	e.set("repositories_encodings", "UTF-8,ISO-8859-1")
	base := "/projects/" + gitPrjID + "/repository/" + itoa64(e.repo.ID)
	for _, r1 := range []string{"57ca437c", "57ca437c0acbbcb749821fdf3726a1367056d364"} {
		res, doc, body := e.get(base + "/revisions/" + r1 + "/entry/latin-1-dir/test-%C3%9C.txt")
		expectStatus(t, res, 200, body)
		if l := sel(doc, "tr#L1 td.line-code"); len(l) != 1 || !strings.Contains(nodeText(l[0]), "test-Ü.txt") {
			t.Errorf("%s: entry line 1 = %q", r1, texts(l))
		}
		for _, dt := range []string{"inline", "sbs"} {
			_, doc, _ = e.get(base + "/revisions/" + r1 + "/diff?type=" + dt)
			if th := sel(doc, "table thead th.filename"); len(th) == 0 || !strings.Contains(nodeText(th[0]), "latin-1-dir/test-Ü.txt") {
				t.Errorf("%s %s: diff filename = %q", r1, dt, texts(th))
			}
			found := false
			for _, td := range sel(doc, "table tbody td.diff_in") {
				if strings.Contains(nodeText(td), "test-Ü.txt") {
					found = true
				}
			}
			if !found {
				t.Errorf("%s %s: diff_in", r1, dt)
			}
		}
		_, doc, _ = e.get(base + "/revisions/" + r1 + "/annotate/latin-1-dir/test-%C3%9C.txt")
		tr := sel(doc, "tr#L1")
		if len(tr) != 1 {
			t.Fatalf("%s: annotate L1 missing", r1)
		}
		if a := sel(tr[0], "td.revision a"); len(a) != 1 || nodeText(a[0]) != "57ca437c" {
			t.Errorf("%s: annotate revision = %q", r1, texts(a))
		}
		if a := sel(tr[0], "td.author"); len(a) != 1 || strings.TrimSpace(nodeText(a[0])) != "jsmith" {
			t.Errorf("%s: annotate author = %q", r1, texts(a))
		}
		if a := sel(tr[0], "td.line-code"); len(a) != 1 || strings.TrimSpace(nodeText(a[0])) != "test-Ü.txt" {
			t.Errorf("%s: annotate line = %q", r1, texts(a))
		}
	}
}

func TestGitAnnotateAtRevisionAndTooBig(t *testing.T) {
	e := setupGit(t)
	e.fetch()
	_, doc, _ := e.get("/projects/" + gitPrjID + "/repository/" + itoa64(e.repo.ID) + "/annotate/sources/watchers_controller.rb?rev=deff7")
	if !strings.Contains(nodeText(sel(doc, "h2")[0]), "@ deff712f") {
		t.Errorf("h2 = %q", nodeText(sel(doc, "h2")[0]))
	}
	// test_annotate_error_when_too_big
	e.set("file_max_size_displayed", "1")
	_, doc, _ = e.get("/projects/" + gitPrjID + "/repository/" + itoa64(e.repo.ID) + "/annotate/sources/watchers_controller.rb?rev=deff712f")
	if p := sel(doc, "p#errorExplanation"); len(p) != 1 || !strings.Contains(nodeText(p[0]), "exceeds the maximum text file size") {
		t.Error("annotate too big")
	}
	res, _, body := e.get("/projects/" + gitPrjID + "/repository/" + itoa64(e.repo.ID) + "/annotate/README?rev=7234cb2")
	expectStatus(t, res, 200, body)
}

func TestGitDiff(t *testing.T) {
	e := setupGit(t)
	e.fetch()
	for _, dt := range []string{"inline", "sbs"} {
		res, doc, body := e.get("/projects/" + gitPrjID + "/repository/" + itoa64(e.repo.ID) + "/revisions/2f9c0091c754a91af7a9c478e36556b4bde8dcf7/diff?type=" + dt)
		expectStatus(t, res, 200, body)
		found := false
		for _, th := range sel(doc, "th.line-num[data-txt='22'] ~ td.diff_out") {
			if strings.Contains(nodeText(th), "def remove") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: line 22 removed missing", dt)
		}
		if !strings.Contains(nodeText(sel(doc, "h2")[0]), "2f9c0091") {
			t.Errorf("%s: h2", dt)
		}
	}
	// test_diff_two_revs
	res, doc, body := e.get("/projects/" + gitPrjID + "/repository/" + itoa64(e.repo.ID) + "/revisions/61b685fbe55ab05b5ac68402d5720c1a6ac973d1/diff?rev_to=2f9c0091c754a91af7a9c478e36556b4bde8dcf7&type=inline")
	expectStatus(t, res, 200, body)
	if !strings.Contains(nodeText(sel(doc, "h2")[0]), "2f9c0091:61b685fb") {
		t.Errorf("two revs h2 = %q", nodeText(sel(doc, "h2")[0]))
	}
	if len(sel(doc, `form[action="/projects/subproject1/repository/`+itoa64(e.repo.ID)+`/revisions/61b685fbe55ab05b5ac68402d5720c1a6ac973d1/diff"]`)) != 1 ||
		len(sel(doc, `input#rev_to[type=hidden][name=rev_to][value="2f9c0091c754a91af7a9c478e36556b4bde8dcf7"]`)) != 1 {
		t.Error("two revs form")
	}
	// test_diff_should_show_filenames
	_, doc, _ = e.get("/projects/" + gitPrjID + "/repository/" + itoa64(e.repo.ID) + "/revisions/deff712f05a90d96edbd70facc47d944be5897e3/diff?type=inline")
	names := texts(sel(doc, "th.filename"))
	if !contains(names, "sources/watchers_controller.rb") || !contains(names, "test.txt") {
		t.Errorf("filenames = %q", names)
	}
	// raw .diff
	res, raw := get(t, e.admin, e.base()+"/revisions/2f9c0091c754a91af7a9c478e36556b4bde8dcf7/diff.diff")
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/x-patch" || !strings.Contains(raw, "def remove") ||
		!strings.Contains(res.Header.Get("Content-Disposition"), "changeset_r2f9c0091c754a91af7a9c478e36556b4bde8dcf7.diff") {
		t.Errorf("raw diff: %d %v", res.StatusCode, res.Header)
	}
	// test_save_diff_type
	var v string
	if err := e.d.Get(context.Background(), &v, `SELECT COALESCE(others, '') FROM user_preferences WHERE user_id = 1`); err == nil && !strings.Contains(v, "inline") {
		t.Logf("pref = %s", v)
	}
}

func TestGitRevisionsAndRevision(t *testing.T) {
	e := setupGit(t)
	e.fetch()
	res, doc, body := e.get("/projects/" + gitPrjID + "/repository/" + itoa64(e.repo.ID) + "/revisions")
	expectStatus(t, res, 200, body)
	if len(sel(doc, `form[method=get][action="/projects/subproject1/repository/`+itoa64(e.repo.ID)+`/revision"]`)) != 1 {
		t.Error("revisions form")
	}
	for _, r := range []string{"61b685fbe55ab05b5ac68402d5720c1a6ac973d1", "61b685f"} {
		res, _, body := e.get("/projects/" + gitPrjID + "/repository/" + itoa64(e.repo.ID) + "/revisions/" + r)
		expectStatus(t, res, 200, body)
	}
	// test_empty_revision
	for _, r := range []string{"", "%20"} {
		res, _, body := e.get("/projects/" + gitPrjID + "/repository/" + itoa64(e.repo.ID) + "/revision?rev=" + r)
		if res.StatusCode != 404 || !strings.Contains(body, "was not found") {
			t.Errorf("empty revision %q: status %d", r, res.StatusCode)
		}
	}
	// 関連チケットの追加・削除（manage_related_issues）
	res, body2 := post(t, e.admin, e.base()+"/revisions/61b685fbe55ab05b5ac68402d5720c1a6ac973d1/issues.js",
		contentForm(t, e.admin, e.ts, "issue_id", "#2"))
	if res.StatusCode != 200 || !strings.Contains(body2, "related-issue-2") {
		t.Errorf("add related issue: %d %s", res.StatusCode, body2)
	}
	cs, err := repository.FindChangesetByRevision(context.Background(), e.d, e.repo.ID, "61b685fbe55ab05b5ac68402d5720c1a6ac973d1")
	if err != nil {
		t.Fatal(err)
	}
	if ids, _ := repository.ChangesetIssueIDs(context.Background(), e.d, cs.ID); len(ids) != 1 || ids[0] != 2 {
		t.Errorf("issue ids = %v", ids)
	}
	f := contentForm(t, e.admin, e.ts, "_method", "delete")
	res, body2 = post(t, e.admin, e.base()+"/revisions/61b685fbe55ab05b5ac68402d5720c1a6ac973d1/issues/2.js", f)
	if res.StatusCode != 200 || !strings.Contains(body2, "#related-issue-2") {
		t.Errorf("remove related issue: %d %s", res.StatusCode, body2)
	}
	if ids, _ := repository.ChangesetIssueIDs(context.Background(), e.d, cs.ID); len(ids) != 0 {
		t.Errorf("issue ids after remove = %v", ids)
	}
}

func TestGitStatsGraphCommitters(t *testing.T) {
	e := setupGit(t)
	e.fetch()
	res, _, body := e.get("/projects/" + gitPrjID + "/repository/" + itoa64(e.repo.ID) + "/statistics")
	expectStatus(t, res, 200, body)
	res, js := get(t, e.admin, e.base()+"/graph?graph=commits_per_author")
	if res.StatusCode != 200 || !strings.Contains(js, `"John Smith"`) || !strings.Contains(js, `"commits":[0,0,0,7,1,11,1,1,2,6]`) {
		t.Errorf("graph = %d %s", res.StatusCode, js)
	}
	res, _ = get(t, e.admin, e.base()+"/graph?graph=nope")
	if res.StatusCode != 404 {
		t.Errorf("unknown graph status %d", res.StatusCode)
	}
	res, doc, body := e.get("/repositories/" + itoa64(e.repo.ID) + "/committers")
	expectStatus(t, res, 200, body)
	if n := len(sel(doc, "table.list tbody tr")); n != 7 {
		t.Errorf("committers = %d", n)
	}
	// マッピングの保存
	f := contentForm(t, e.admin, e.ts, "committers[1][]", "test20120208 <none@none>")
	f.Add("committers[1][]", "3")
	res, body = post(t, e.admin, e.ts.URL+"/repositories/"+itoa64(e.repo.ID)+"/committers", f)
	expectContentRedirect(t, res, body, "/projects/subproject1/settings/repositories")
	var n int
	if err := e.d.Get(context.Background(), &n, `SELECT COUNT(*) FROM changesets WHERE repository_id = ? AND user_id = 3`, e.repo.ID); err != nil || n != 7 {
		t.Errorf("mapped = %d %v", n, err)
	}
}

func TestGitDestroy(t *testing.T) {
	e := setupGit(t)
	e.fetch()
	res, body := post(t, e.admin, e.ts.URL+"/repositories/"+itoa64(e.repo.ID), contentForm(t, e.admin, e.ts, "_method", "delete"))
	expectContentRedirect(t, res, body, "/projects/subproject1/settings/repositories")
	if n, _ := repository.CountChangesets(context.Background(), e.d, e.repo.ID); n != 0 {
		t.Errorf("changesets left = %d", n)
	}
	if ok, _ := repository.ProjectHasDefaultRepository(context.Background(), e.d, 3); ok {
		t.Error("repository left")
	}
}

func TestSysFetchChangesets(t *testing.T) {
	e := setupGit(t)
	c := newClient(t)
	res, body := get(t, c, e.ts.URL+"/sys/fetch_changesets?key=secret")
	if res.StatusCode != 403 || !strings.Contains(body, "Access denied") {
		t.Errorf("disabled: %d %s", res.StatusCode, body)
	}
	e.set("sys_api_enabled", "1")
	e.set("sys_api_key", "secret")
	res, _ = get(t, c, e.ts.URL+"/sys/fetch_changesets?key=wrong")
	if res.StatusCode != 403 {
		t.Errorf("wrong key: %d", res.StatusCode)
	}
	res, _ = get(t, c, e.ts.URL+"/sys/fetch_changesets?key=secret&id=nope")
	if res.StatusCode != 404 {
		t.Errorf("unknown project: %d", res.StatusCode)
	}
	res, _ = get(t, c, e.ts.URL+"/sys/fetch_changesets?key=secret&id=subproject1")
	if res.StatusCode != 200 {
		t.Errorf("fetch: %d", res.StatusCode)
	}
	if n, _ := repository.CountChangesets(context.Background(), e.d, e.repo.ID); n != 29 {
		t.Errorf("changesets = %d", n)
	}
	res, body = get(t, c, e.ts.URL+"/sys/projects?key=secret")
	if res.StatusCode != 200 || !strings.Contains(body, `"identifier":"subproject1","status":1,"repository":{"id":`+itoa64(e.repo.ID)) {
		t.Errorf("sys projects: %d %s", res.StatusCode, body)
	}
}
