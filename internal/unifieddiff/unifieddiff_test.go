// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

package unifieddiff

import "testing"

// test/unit/lib/redmine/unified_diff_test.rb の一部の移植。

const gitDiff = `diff --git a/test.txt b/test.txt
index 0000000..1111111 100644
--- a/test.txt
+++ b/test.txt
@@ -1,4 +1,4 @@
 context
-removed line
+added line
 context
@@ -10,2 +10,3 @@
 more
+new
 end
`

func TestParseInline(t *testing.T) {
	u := Parse(gitDiff, "inline", "", 0, nil)
	if len(u.Tables) != 1 {
		t.Fatalf("tables %d", len(u.Tables))
	}
	tb := u.Tables[0]
	if tb.FileName != "test.txt" || tb.PreviousFileName != "" {
		t.Errorf("file name %q previous %q", tb.FileName, tb.PreviousFileName)
	}
	lines := tb.Lines()
	if len(lines) != 7 {
		t.Fatalf("lines %d", len(lines))
	}
	if l := lines[1]; l.TypeDiff() != "diff_out" || l.HTMLLine() != "<span>remov</span>ed line" || l.NbLineLeft != 2 || l.NbLineRight != 0 {
		t.Errorf("removed: %+v %q", l.Diff, l.HTMLLine())
	}
	if l := lines[2]; l.TypeDiff() != "diff_in" || l.HTMLLine() != "<span>add</span>ed line" || l.NbLineRight != 2 {
		t.Errorf("added: %+v %q", l.Diff, l.HTMLLine())
	}
	if !lines[4].Spacing || lines[4].NbLineLeft != 10 {
		t.Errorf("spacing: %+v", lines[4])
	}
}

func TestParseSideBySide(t *testing.T) {
	u := Parse(gitDiff, "sbs", "", 0, nil)
	lines := u.Tables[0].Lines()
	if len(lines) != 6 {
		t.Fatalf("lines %d", len(lines))
	}
	l := lines[1]
	if l.LineLeft != "removed line" || l.LineRight != "added line" || l.TypeDiffLeft != "diff_out" || l.TypeDiffRight != "diff_in" {
		t.Errorf("sbs: %+v", l.Diff)
	}
}

func TestRenamedFileAndTruncate(t *testing.T) {
	diff := "--- a/old.txt\n+++ b/new.txt\n@@ -1 +1 @@\n-a\n+b\n"
	u := Parse(diff, "inline", "", 0, nil)
	if tb := u.Tables[0]; tb.FileName != "new.txt" || tb.PreviousFileName != "old.txt" {
		t.Errorf("rename: %q %q", tb.FileName, tb.PreviousFileName)
	}
	if u := Parse(gitDiff, "inline", "", 5, nil); !u.Truncated {
		t.Error("not truncated")
	}
}

func TestReplaceInvalidUTF8(t *testing.T) {
	if got := ReplaceInvalidUTF8("cr\xe9\xe9e"); got != "cr??e" {
		t.Errorf("got %q", got)
	}
	if got := ReplaceInvalidUTF8("テスト"); got != "テスト" {
		t.Errorf("got %q", got)
	}
}
