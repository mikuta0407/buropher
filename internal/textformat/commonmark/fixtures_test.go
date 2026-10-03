// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package commonmark

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/textformat/internal/fixtures"
)

// knownDiffs は Redmine 本体と出力が一致しないことが分かっているエントリ
// （理由は testdata/known_diffs.txt を参照）。
var knownDiffs = loadKnownDiffs()

func loadKnownDiffs() map[string]bool {
	m := map[string]bool{}
	b, err := os.ReadFile("testdata/known_diffs.txt")
	if err != nil {
		return m
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m[strings.Fields(line)[0]] = true
	}
	return m
}

// TestFixtures は Redmine 本体で生成した正解データ（testdata/fixtures.json）と比較する。
// 一致率を表示し、既知の差分（testdata/known_diffs.txt）以外の不一致を失敗とする。
func TestFixtures(t *testing.T) {
	f, err := fixtures.Load()
	if err != nil {
		t.Fatal(err)
	}
	total, pass := 0, 0
	var failed []string
	for _, e := range f.ByMode("commonmark") {
		total++
		opts := Options{IconsPath: f.IconsPath}
		if e.Hardbreaks != nil && !*e.Hardbreaks {
			opts.DisableHardBreaks = true
		}
		got := Format(e.Input, opts)
		want := e.ExpectedString()
		if got == want {
			pass++
			if knownDiffs[e.Name] && os.Getenv("CM_REPORT") != "" {
				t.Logf("known diff now passes: %s", e.Name)
			}
			continue
		}
		failed = append(failed, e.Name)
		if !knownDiffs[e.Name] {
			if os.Getenv("CM_SHORT") != "" {
				i := 0
				for i < len(want) && i < len(got) && want[i] == got[i] {
					i++
				}
				lo := max(0, i-60)
				t.Errorf("%s:\ninput: %q\nwant: …%q\ngot:  …%q", e.Name, e.Input, want[lo:min(len(want), i+80)], got[lo:min(len(got), i+80)])
			} else {
				t.Errorf("%s:\ninput: %q\nwant:  %q\ngot:   %q", e.Name, e.Input, want, got)
			}
		}
	}
	sort.Strings(failed)
	t.Logf("commonmark fixtures: %d/%d exact matches (%.1f%%)", pass, total, float64(pass)*100/float64(total))
	if path := os.Getenv("CM_FAILED_OUT"); path != "" {
		_ = os.WriteFile(path, []byte(strings.Join(failed, "\n")+"\n"), 0o644)
	}
}

// TestFixtureSections は section_get / section_update の正解データと比較する。
func TestFixtureSections(t *testing.T) {
	f, err := fixtures.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range f.ByMode("section_get") {
		var want []string
		if err := json.Unmarshal(e.Expected, &want); err != nil {
			t.Fatal(err)
		}
		text, hash := GetSection(e.Input, e.Index)
		if text != want[0] || hash != want[1] {
			t.Errorf("%s: want %q %q, got %q %q", e.Name, want[0], want[1], text, hash)
		}
	}
	for _, e := range f.ByMode("section_update") {
		got, err := UpdateSection(e.Input, e.Index, e.Replacement, e.Hash)
		var wantErr struct{ Error string }
		if json.Unmarshal(e.Expected, &wantErr) == nil && wantErr.Error != "" {
			if err != ErrStaleSection {
				t.Errorf("%s: want stale error, got %q %v", e.Name, got, err)
			}
			continue
		}
		want := e.ExpectedString()
		if err != nil || got != want {
			t.Errorf("%s: want %q, got %q (%v)", e.Name, want, got, err)
		}
	}
}

var _ = fmt.Sprint
