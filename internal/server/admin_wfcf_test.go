// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// このファイルはワークフロー・カスタムフィールドの管理画面を参照 Redmine（フィクスチャ投入済み、
// http://127.0.0.1:3998）の出力と比較する。testdata/wfcf/*.html は `compat fetch -raw -user admin` の出力に
// normalize / normalizeFixture と同じ置換をしたもの。

var imageDigestRe = regexp.MustCompile(`-[0-9a-f]{8}\.(png|gif)`)

// comparePage は管理者でページを取得し、testdata/wfcf/<golden> と比較する。
// 環境変数 WFCF_DUMP が設定されていれば、取得した（正規化後の）本文をそのディレクトリに書き出す（差分調査用）。
func comparePage(t *testing.T, ts *httptest.Server, c *http.Client, path, golden string) string {
	t.Helper()
	res, body := get(t, c, ts.URL+path)
	if res.StatusCode != 200 {
		t.Fatalf("%s: status %d", path, res.StatusCode)
	}
	// 画像のダイジェストは buropher のアセットパイプラインと Sprockets で異なるため伏せる（tools/compat の正規化と同じ）
	got := imageDigestRe.ReplaceAllString(normalize(normalizeFixture(body, ts.URL)), "-DIGEST.$1")
	if dir := os.Getenv("WFCF_DUMP"); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, golden), []byte(got), 0o644)
	}
	want, err := os.ReadFile("testdata/wfcf/" + golden)
	if err != nil {
		t.Fatal(err)
	}
	// プロジェクトのツリー（render_project_nested_lists）は兄弟の並び順が参照と異なる（docs/schema.md 17.:
	// 参照のフィクスチャは eCookbook が先、buropher は名前のバイト順で OnlineStore が先）ため、
	// ツリー部分は項目（行）の集合として比較し、本文からは伏せる。
	gotTree, gotRest := cutProjectTree(got)
	wantTree, wantRest := cutProjectTree(string(want))
	if !sameLineSet(gotTree, wantTree) {
		t.Errorf("%s: project tree items differ\n got: %s\nwant: %s", golden, gotTree, wantTree)
	}
	if gotRest != wantRest {
		gl, wl := strings.Split(gotRest, "\n"), strings.Split(wantRest, "\n")
		for i := 0; i < len(gl) || i < len(wl); i++ {
			var a, b string
			if i < len(gl) {
				a = gl[i]
			}
			if i < len(wl) {
				b = wl[i]
			}
			if a != b {
				t.Fatalf("%s: line %d differs\n got: %q\nwant: %q", golden, i+1, a, b)
			}
		}
	}
	return body
}

var projectTreeRe = regexp.MustCompile(`(?s)<ul class='projects root'>\n.*?</li></ul>\n(?:</li></ul>\n)*`)

// cutProjectTree はプロジェクトのツリー部分を取り出し、本文ではそれを PROJECT_TREE に置き換える。
func cutProjectTree(s string) (tree, rest string) {
	loc := projectTreeRe.FindStringIndex(s)
	if loc == nil {
		return "", s
	}
	return s[loc[0]:loc[1]], s[:loc[0]] + "PROJECT_TREE\n" + s[loc[1]:]
}

// sameLineSet はツリーの各項目（<label>...</label>）の集合が等しいか。
func sameLineSet(a, b string) bool {
	re := regexp.MustCompile(`<label>.*?</label>`)
	as, bs := re.FindAllString(a, -1), re.FindAllString(b, -1)
	if len(as) != len(bs) {
		return false
	}
	m := map[string]int{}
	for _, x := range as {
		m[x]++
	}
	for _, x := range bs {
		m[x]--
	}
	for _, n := range m {
		if n != 0 {
			return false
		}
	}
	return true
}

func TestWorkflowPagesWithFixtures(t *testing.T) {
	ts, _ := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")
	for _, tc := range []struct{ path, golden string }{
		{"/workflows", "workflows.html"},
		{"/workflows/edit", "wf_edit.html"},
		{"/workflows/edit?role_id=1&tracker_id=1", "wf_edit_r1t1.html"},
		{"/workflows/edit?role_id=1&tracker_id=1&used_statuses_only=0", "wf_edit_r1t1_all.html"},
		{"/workflows/edit?role_id[]=1&role_id[]=2&tracker_id[]=1", "wf_edit_multi.html"},
		{"/workflows/edit?role_id=all&tracker_id=all", "wf_edit_all.html"},
		{"/workflows/permissions", "wf_perm.html"},
		{"/workflows/permissions?role_id=1&tracker_id=1", "wf_perm_r1t1.html"},
		{"/workflows/permissions?role_id[]=1&role_id[]=2&tracker_id[]=1&tracker_id[]=2", "wf_perm_multi.html"},
		{"/workflows/copy", "wf_copy.html"},
		{"/workflows/copy?source_tracker_id=1&source_role_id=2", "wf_copy_src.html"},
	} {
		t.Run(strings.TrimSuffix(tc.golden, ".html"), func(t *testing.T) {
			comparePage(t, ts, c, tc.path, tc.golden)
		})
	}
}

type pageCase struct{ path, golden string }

func TestCustomFieldPagesWithFixtures(t *testing.T) {
	ts, _ := newFixtureServer(t)
	c := login(t, ts, "admin", "admin")
	cases := []pageCase{
		{"/custom_fields", "cf_index.html"},
		{"/custom_fields?tab=UserCustomField", "cf_index_tab.html"},
		{"/custom_fields/new", "cf_new.html"},
		{"/custom_fields/new?tab=UserCustomField", "cf_new_tab_user.html"},
		{"/custom_fields/new?type=IssueCustomField", "cf_new_issue.html"},
		{"/custom_fields/new?type=IssueCustomField&copy=1", "cf_new_copy1.html"},
		{"/custom_fields/new?type=ProjectCustomField&copy=3", "cf_new_copy3.html"},
	}
	for i := 1; i <= 10; i++ {
		cases = append(cases, pageCase{fmt.Sprintf("/custom_fields/%d/edit", i), fmt.Sprintf("cf_edit_%d.html", i)})
	}
	for _, f := range []string{"string", "text", "link", "int", "float", "date", "list", "bool", "enumeration", "user", "version", "attachment", "progressbar"} {
		cases = append(cases, pageCase{"/custom_fields/new?type=IssueCustomField&custom_field%5Bfield_format%5D=" + f, "cf_new_issue_" + f + ".html"})
	}
	for _, typ := range []string{"TimeEntryCustomField", "ProjectCustomField", "VersionCustomField", "DocumentCustomField", "UserCustomField",
		"GroupCustomField", "TimeEntryActivityCustomField", "IssuePriorityCustomField", "DocumentCategoryCustomField"} {
		cases = append(cases,
			pageCase{"/custom_fields/new?type=" + typ, "cf_new_" + typ + ".html"},
			pageCase{"/custom_fields/new?type=" + typ + "&custom_field%5Bfield_format%5D=text", "cf_new_" + typ + "_text.html"})
	}
	for _, tc := range cases {
		t.Run(strings.TrimSuffix(tc.golden, ".html"), func(t *testing.T) {
			comparePage(t, ts, c, tc.path, tc.golden)
		})
	}
}
