// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package highlight

import (
	"strings"
	"testing"
)

func BenchmarkHighlightRuby(b *testing.B) {
	src := strings.Repeat("class Foo < Bar\n  def initialize(x = nil)\n    @x = x || \"default #{1 + 2}\"\n  end\nend\n", 200)
	b.SetBytes(int64(len(src)))
	for b.Loop() {
		HighlightByLanguage(src, "ruby")
	}
}

func BenchmarkHighlightC(b *testing.B) {
	src := strings.Repeat("static int counter = 0; /* comment */\nint add(int a, int b) { return a + b; }\nchar *s = \"text\";\n", 200)
	b.SetBytes(int64(len(src)))
	for b.Loop() {
		HighlightByLanguage(src, "c")
	}
}

func BenchmarkHighlightJS(b *testing.B) {
	src := strings.Repeat("function f(a, b) { return a + b; } // comment\nvar x = [1, 2, 3].map(function (v) { return v * 2; });\n", 200)
	b.SetBytes(int64(len(src)))
	for b.Loop() {
		HighlightByLanguage(src, "javascript")
	}
}
