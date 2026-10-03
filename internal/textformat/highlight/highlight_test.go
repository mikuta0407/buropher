// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package highlight

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/textformat/internal/fixtures"
)

// TestLanguageSupportedFixtures は language_supported? の結果が Redmine と一致することを確認する。
func TestLanguageSupportedFixtures(t *testing.T) {
	f, err := fixtures.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range f.ByMode("supported") {
		var want bool
		if err := json.Unmarshal(e.Expected, &want); err != nil {
			t.Fatal(err)
		}
		if got := LanguageSupported(e.Lang); got != want {
			t.Errorf("%s: LanguageSupported(%q) = %v, want %v", e.Name, e.Lang, got, want)
		}
	}
}

func TestLanguageSupported(t *testing.T) {
	for lang, want := range map[string]bool{
		"ruby": true, "Ruby": true, "c++": true, "delphi": true, "cplusplus": true,
		"ecmascript": true, "ecma_script": true, "java_script": true, "xhtml": true,
		"foobar": false, "c-k&r": false, "": false, "text": true, "plaintext": true,
	} {
		if got := LanguageSupported(lang); got != want {
			t.Errorf("LanguageSupported(%q) = %v, want %v", lang, got, want)
		}
	}
}

func TestFilenameSupported(t *testing.T) {
	for name, want := range map[string]bool{
		"a.rb": true, "Makefile": true, "foo.unknownext": false, "a.h": true, "README": false,
	} {
		if got := FilenameSupported(name); got != want {
			t.Errorf("FilenameSupported(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestHighlightFixtures は Rouge の出力との一致率を表示する（レキサーが別実装のため失敗にはしない）。
func TestHighlightFixtures(t *testing.T) {
	f, err := fixtures.Load()
	if err != nil {
		t.Fatal(err)
	}
	report := func(mode string, run func(fixtures.Entry) string) {
		total, pass := 0, 0
		var failed []string
		for _, e := range f.ByMode(mode) {
			total++
			if run(e) == e.ExpectedString() {
				pass++
			} else {
				failed = append(failed, e.Name)
			}
		}
		sort.Strings(failed)
		t.Logf("%s: %d/%d exact matches", mode, pass, total)
		if os.Getenv("HL_REPORT") != "" {
			t.Logf("failed: %s", strings.Join(failed, " "))
		}
	}
	report("highlight", func(e fixtures.Entry) string { return HighlightByLanguage(e.Input, e.Lang) })
	report("highlight_file", func(e fixtures.Entry) string { return HighlightByFilename(e.Input, e.Filename) })
}
