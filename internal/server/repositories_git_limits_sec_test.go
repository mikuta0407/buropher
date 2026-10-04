// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package server_test

import (
	"context"
	"strings"
	"testing"
)

// TestGitDiffAndAnnotateSizeLimits は、差分の画面が diff_max_lines_displayed 行より先を読まずに
// 切り詰めの表示になること、表示上限を大きく超えるファイルの注釈は blame せずに大きすぎる旨を出すこと、
// .diff のダウンロードは（流して送っても）差分全体を返すこと。
func TestGitDiffAndAnnotateSizeLimits(t *testing.T) {
	e := setupGit(t)
	ctx := context.Background()
	set := func(k, v string) {
		t.Helper()
		if err := e.app.Settings.Set(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	path := strings.TrimPrefix(e.base(), e.ts.URL)
	rev := "/revisions/deff712f05a90d96edbd70facc47d944be5897e3"
	_, full := get(t, e.admin, e.base()+rev+"/diff.diff")
	if n := strings.Count(full, "\n"); n < 20 {
		t.Fatalf("raw diff has %d lines", n)
	}
	set("diff_max_lines_displayed", "5")
	res, doc, body := e.get(path + rev + "/diff?type=inline")
	expectStatus(t, res, 200, body)
	if !strings.Contains(body, "This diff was truncated") {
		t.Errorf("diff should be truncated\n%s", contentMain(body))
	}
	if names := texts(sel(doc, "th.filename")); contains(names, "test.txt") {
		t.Errorf("lines beyond the limit should not be shown: %q", names)
	}
	// .diff は上限に関係なく全体
	res, raw := get(t, e.admin, e.base()+rev+"/diff.diff")
	if res.StatusCode != 200 || raw != full || res.Header.Get("Content-Type") != "text/x-patch" {
		t.Errorf(".diff should be the whole diff (status %d, %d bytes, want %d)", res.StatusCode, len(raw), len(full))
	}
	res, _ = get(t, e.admin, e.base()+"/revisions/0123456789abcdef0123456789abcdef01234567/diff.diff")
	if res.StatusCode != 404 {
		t.Errorf("unknown revision .diff: status %d", res.StatusCode)
	}

	set("file_max_size_displayed", "0")
	_, doc, _ = e.get(path + "/annotate/sources/watchers_controller.rb")
	if p := sel(doc, "p#errorExplanation"); len(p) != 1 || !strings.Contains(nodeText(p[0]), "exceeds the maximum text file size") {
		t.Errorf("annotate of a file over the limit: %v", texts(p))
	}
	if len(sel(doc, "tr#L1")) != 0 {
		t.Error("annotation should not be shown")
	}
}
