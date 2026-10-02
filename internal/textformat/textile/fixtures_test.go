// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

package textile

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// fakeHighlighter は tools/gen-textile-fixtures.rb のフェイクハイライタと同じ挙動をする。
func fakeHighlighter(lang, code string) (string, bool) {
	if !slices.Contains([]string{"ruby", "c", "python", "javascript", "sql", "bash", "xml", "html", "java", "go"}, lang) {
		return "", false
	}
	return `<span class="hl-` + lang + `">` + htmlEscapeERB(code) + `</span>`, true
}

var fakeOpts = &Options{Highlight: fakeHighlighter}

type fixtureCase struct {
	Name  string `json:"name"`
	Input string `json:"input"`
	HTML  string `json:"html"`
}

func loadFixtures(t *testing.T) []fixtureCase {
	t.Helper()
	b, err := os.ReadFile("testdata/fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []fixtureCase
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

// TestFixtures は実際の Redmine で生成した期待値とバイト単位で比較する。
func TestFixtures(t *testing.T) {
	cases := loadFixtures(t)
	pass := 0
	for _, c := range cases {
		got := Format(c.Input, fakeOpts)
		if got == c.HTML {
			pass++
			continue
		}
		t.Errorf("%s\n--- input:\n%s\n--- want:\n%s\n--- got:\n%s", c.Name, c.Input, c.HTML, got)
	}
	t.Logf("exact match: %d/%d", pass, len(cases))
}

// TestFuzzFile は tools/gen-textile-fuzz.rb が生成したランダム入力で差分テストを行う
// (環境変数 TEXTILE_FUZZ_FILE が設定されているときのみ)。
func TestFuzzFile(t *testing.T) {
	path := os.Getenv("TEXTILE_FUZZ_FILE")
	if path == "" {
		t.Skip("TEXTILE_FUZZ_FILE not set")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cases []fixtureCase
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	pass, shown := 0, 0
	for _, c := range cases {
		got := Format(c.Input, fakeOpts)
		if got == c.HTML {
			pass++
			continue
		}
		if shown < 15 {
			shown++
			t.Errorf("%s\n--- input:\n%q\n--- want:\n%q\n--- got:\n%q", c.Name, c.Input, c.HTML, got)
		}
	}
	t.Logf("fuzz exact match: %d/%d", pass, len(cases))
}
