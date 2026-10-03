// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package highlight

import (
	"os"
	"testing"

	"github.com/mikuta0407/buropher/internal/textformat/internal/fixtures"
)

// TestDebugFixture は HL_DEBUG に指定したフィクスチャの差分を表示する（開発用）。
func TestDebugFixture(t *testing.T) {
	name := os.Getenv("HL_DEBUG")
	if name == "" {
		t.Skip("HL_DEBUG not set")
	}
	f, err := fixtures.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range f.Entries {
		if e.Name != name {
			continue
		}
		var got string
		if e.Mode == "highlight_file" {
			got = HighlightByFilename(e.Input, e.Filename)
		} else {
			got = HighlightByLanguage(e.Input, e.Lang)
		}
		want := e.ExpectedString()
		if want == got {
			t.Logf("IDENTICAL")
			continue
		}
		// 最初の相違箇所を表示する
		i := 0
		for i < len(want) && i < len(got) && want[i] == got[i] {
			i++
		}
		lo := max(0, i-200)
		t.Logf("len want=%d got=%d tail want=%q got=%q", len(want), len(got), want[max(0, len(want)-80):], got[max(0, len(got)-80):])
		t.Logf("first diff at %d\nwant: %q\n got: %q", i, want[lo:min(len(want), i+300)], got[lo:min(len(got), i+300)])
	}
}
