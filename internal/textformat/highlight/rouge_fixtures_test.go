// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package highlight

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
)

// rougeFixture は tools/gen-rouge-fixtures.rb が Redmine（Rouge）で生成した正解データの 1 件。
type rougeFixture struct {
	Name     string          `json:"name"`
	Mode     string          `json:"mode"`
	Lang     string          `json:"lang"`
	Filename string          `json:"filename"`
	Input    string          `json:"input"`
	Expected json.RawMessage `json:"expected"`
}

func loadRougeFixtures(t *testing.T) (string, []rougeFixture) {
	t.Helper()
	b, err := os.ReadFile("testdata/rouge.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Version string         `json:"rouge_version"`
		Entries []rougeFixture `json:"entries"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f.Version, f.Entries
}

// ported は移植済みのレキサーか（chroma で代用する言語は Rouge と一致しないことがある）。
func ported(l *rougeLexer) bool {
	if l == nil {
		return true // PlainText
	}
	rougeImplsMu.Lock()
	defer rougeImplsMu.Unlock()
	_, ok := rougeImpls[l.Tag]
	return ok
}

// rougeKnownDiffs は移植済みのレキサーでも一致しないもの（理由付き）。
var rougeKnownDiffs = map[string]string{
	// コードフェンス内の latex（tex）は未移植で chroma で代用している。
	// フェンスを除いたものは sample-markdown-ported-fences で比較する。
	"rouge-sample-markdown": "fenced latex is not ported",
}

// TestRougeFixtures は Redmine（Rouge）の出力と比較する。移植済みのレキサー・対応言語の判定・
// ファイル名の判定は完全一致を求め、chroma で代用する言語は一致率だけを表示する。
func TestRougeFixtures(t *testing.T) {
	version, entries := loadRougeFixtures(t)
	if !strings.HasPrefix(version, "5.1.") {
		t.Fatalf("rouge.json was generated from Rouge %s", version)
	}
	type stat struct{ total, pass int }
	stats := map[string]*stat{}
	var others []string
	count := func(key string, ok bool) {
		s := stats[key]
		if s == nil {
			s = &stat{}
			stats[key] = s
		}
		s.total++
		if ok {
			s.pass++
		}
	}
	for _, e := range entries {
		switch e.Mode {
		case "supported", "filename":
			var want bool
			if err := json.Unmarshal(e.Expected, &want); err != nil {
				t.Fatal(err)
			}
			var got bool
			if e.Mode == "supported" {
				got = LanguageSupported(e.Lang)
			} else {
				got = FilenameSupported(e.Filename)
			}
			count(e.Mode, got == want)
			if got != want {
				t.Errorf("%s: got %v, want %v", e.Name, got, want)
			}
		case "highlight", "highlight_file":
			var want string
			if err := json.Unmarshal(e.Expected, &want); err != nil {
				t.Fatal(err)
			}
			var got string
			var strict bool
			if e.Mode == "highlight" {
				got = HighlightByLanguage(e.Input, e.Lang)
				strict = ported(findLexer(strings.ToLower(e.Lang)))
			} else {
				got = HighlightByFilename(e.Input, e.Filename)
				l, ambiguous := guessLexer(strings.ReplaceAll(e.Filename, "\r", ""), normalizeNewlines(e.Input))
				strict = ambiguous || ported(l)
			}
			if _, ok := rougeKnownDiffs[e.Name]; ok {
				strict = false
			}
			key := e.Mode
			if strict {
				key += " (ported)"
			} else {
				key += " (chroma)"
			}
			count(key, got == want)
			switch {
			case got == want:
			case strict:
				g, w := diffContext(got, want)
				t.Errorf("%s: mismatch\n got: %q\nwant: %q", e.Name, g, w)
			default:
				others = append(others, e.Name)
			}
		default:
			t.Fatalf("%s: unknown mode %q", e.Name, e.Mode)
		}
	}
	keys := make([]string, 0, len(stats))
	for k := range stats {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("%s: %d/%d exact matches", k, stats[k].pass, stats[k].total)
	}
	if os.Getenv("HL_REPORT") != "" {
		t.Logf("chroma mismatches: %s", strings.Join(others, " "))
	}
}

func normalizeNewlines(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// diffContext は最初に異なる位置の前後を返す（失敗時の表示用）。
func diffContext(got, want string) (string, string) {
	i := 0
	for i < len(got) && i < len(want) && got[i] == want[i] {
		i++
	}
	from := max(i-200, 0)
	cut := func(s string) string { return s[min(from, len(s)):min(i+300, len(s))] }
	return cut(got), cut(want)
}
