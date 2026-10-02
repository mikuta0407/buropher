// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

package textile

import (
	"encoding/json"
	"html"
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
	_ = html.EscapeString
}
