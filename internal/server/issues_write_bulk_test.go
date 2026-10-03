// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"net/http"
	"os"
	"testing"
)

// チケットの一括編集・一括更新・削除と、ジャーナルの引用・編集（IssuesController#bulk_edit / bulk_update / destroy、
// JournalsController#new / edit / update）のテスト。
// testdata/issues_write/ の期待値は共用の参照 Redmine（3998）から
// `go run ./tools/compat fetch -base http://127.0.0.1:3998 -raw -user <user> <path>` で取得し、
// ベース URL を {{BASE}} に置換したもの（GET のみ。比較の正規化は issues_read_test.go と同じ）。

// compareIssuesWriteGolden は testdata/issues_write/name と比較する。
func compareIssuesWriteGolden(t *testing.T, name, got, base string) {
	t.Helper()
	raw, err := os.ReadFile("testdata/issues_write/" + name)
	if err != nil {
		t.Fatal(err)
	}
	want := normalizeIssuesRead(string(raw), "{{BASE}}", true)
	g := normalizeIssuesRead(got, base, true)
	if g == want {
		return
	}
	n := 0
	for n < len(g) && n < len(want) && g[n] == want[n] {
		n++
	}
	from := max(0, n-300)
	t.Fatalf("%s: differs at byte %d\n got: %s\nwant: %s", name, n, g[from:min(len(g), n+300)], want[from:min(len(want), n+300)])
}

// TestIssuesBulkEditPagesMatchRedmine は一括編集フォーム（コピー・移動を含む）が参照 Redmine と一致することを確認する。
func TestIssuesBulkEditPagesMatchRedmine(t *testing.T) {
	ts, d := newFixtureServer(t)
	alignIssuesFixtures(t, d)
	cases := []struct {
		name, user, pw, path string
	}{
		{"bulk_edit_12_admin.html", "admin", "admin", "/issues/bulk_edit?ids[]=1&ids[]=2"},
		{"bulk_edit_13_admin.html", "admin", "admin", "/issues/bulk_edit?ids[]=1&ids[]=3"},
		{"bulk_edit_copy123_admin.html", "admin", "admin", "/issues/bulk_edit?copy=1&ids[]=1&ids[]=2&ids[]=3"},
		{"bulk_edit_14_move_admin.html", "admin", "admin", "/issues/bulk_edit?ids%5B%5D=1&ids%5B%5D=4&issue%5Bproject_id%5D=2&issue%5Btracker_id%5D=2&issue%5Bstatus_id%5D=3&issue%5Bassigned_to_id%5D=none&issue%5Bstart_date%5D=none"},
		{"bulk_edit_123_jsmith.html", "jsmith", "jsmith", "/issues/bulk_edit?ids[]=1&ids[]=2&ids[]=3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := login(t, ts, tc.user, tc.pw)
			res, body := get(t, c, ts.URL+tc.path)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("GET %s: status %d", tc.path, res.StatusCode)
			}
			compareIssuesWriteGolden(t, tc.name, body, ts.URL)
		})
	}
}
