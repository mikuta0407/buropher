package server_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// このファイルはワークフロー・カスタムフィールドの管理画面を参照 Redmine（フィクスチャ投入済み、
// http://127.0.0.1:3998）の出力と比較する。testdata/wfcf/*.html は `compat fetch -raw -user admin` の出力に
// normalize / normalizeFixture と同じ置換をしたもの。

// comparePage は管理者でページを取得し、testdata/wfcf/<golden> と比較する。
// 環境変数 WFCF_DUMP が設定されていれば、取得した（正規化後の）本文をそのディレクトリに書き出す（差分調査用）。
func comparePage(t *testing.T, ts *httptest.Server, c *http.Client, path, golden string) string {
	t.Helper()
	res, body := get(t, c, ts.URL+path)
	if res.StatusCode != 200 {
		t.Fatalf("%s: status %d", path, res.StatusCode)
	}
	got := normalize(normalizeFixture(body, ts.URL))
	if dir := os.Getenv("WFCF_DUMP"); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, golden), []byte(got), 0o644)
	}
	compareGolden(t, "wfcf/"+golden, got)
	return body
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
		t.Run(strings.TrimPrefix(tc.golden, ".html"), func(t *testing.T) {
			comparePage(t, ts, c, tc.path, tc.golden)
		})
	}
}
