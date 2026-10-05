// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package scm

import (
	"bytes"
	"slices"
	"testing"
)

// TestDiscardHeaderOnlyDiff は Git 2.55.0 の show がパスを変更しないコミットでも出力するヘッダーだけの
// 差分を空にすること（Redmine 7.0 #44354）。
func TestDiscardHeaderOnlyDiff(t *testing.T) {
	header := []string{"commit 713f4944648826f558cf548222f813dabe7cbb04\n", "Author: jsmith\n", "\n", "    msg\n"}
	if got := discardHeaderOnlyDiff("invalid", header); got == nil || len(got) != 0 {
		t.Errorf("header only = %q", got)
	}
	if got := discardHeaderOnlyDiff("", header); !slices.Equal(got, header) {
		t.Errorf("no path = %q", got)
	}
	withDiff := append(slices.Clone(header), "diff --git a/x b/x\n", "+a\n")
	if got := discardHeaderOnlyDiff("x", withDiff); !slices.Equal(got, withDiff) {
		t.Errorf("with diff = %q", got)
	}

	// DiffTo のフィルター: 分割して書かれても "diff --" の行が出るまで溜め、出たら全部書き出す
	var buf bytes.Buffer
	f := &diffHeaderFilter{w: &buf}
	for _, p := range []string{"commit 713f\nAuthor: jsmith\n\n    msg\nd", "iff -", "-git a/x b/x\n+a\n", "+b\n"} {
		if n, err := f.Write([]byte(p)); err != nil || n != len(p) {
			t.Fatalf("write %q: n=%d err=%v", p, n, err)
		}
	}
	if want := "commit 713f\nAuthor: jsmith\n\n    msg\ndiff --git a/x b/x\n+a\n+b\n"; buf.String() != want {
		t.Errorf("filter = %q", buf.String())
	}
	buf.Reset()
	f = &diffHeaderFilter{w: &buf}
	_, _ = f.Write([]byte("commit 713f\nAuthor: jsmith\n\n    diff -- in message\n"))
	if buf.Len() != 0 {
		t.Errorf("header only filter = %q", buf.String())
	}
}
