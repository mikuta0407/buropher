// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

package textile

import (
	"encoding/json"
	"math/rand/v2"
	"os"
	"reflect"
	"strings"
	"testing"
)

type sectionCase struct {
	Name    string `json:"name"`
	Input   string `json:"input"`
	Index   int    `json:"index"`
	Section string `json:"section"`
	Hash    string `json:"hash"`
	Updated string `json:"updated"`
}

// TestSectionFixtures は get_section / update_section の結果を Redmine と比較する。
func TestSectionFixtures(t *testing.T) {
	b, err := os.ReadFile("testdata/sections.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []sectionCase
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	pass := 0
	for _, c := range cases {
		sec, hash := GetSection(c.Input, c.Index)
		upd, err := UpdateSection(c.Input, c.Index, "UPDATED", "")
		if sec == c.Section && hash == c.Hash && upd == c.Updated && err == nil {
			pass++
			continue
		}
		t.Errorf("%s index=%d\n--- input:\n%q\n--- want section:\n%q\n--- got:\n%q\n--- want updated:\n%q\n--- got:\n%q",
			c.Name, c.Index, c.Input, c.Section, sec, c.Updated, upd)
	}
	t.Logf("section exact match: %d/%d", pass, len(cases))
}

// TestScanSectionsEquivalence は scanSections が原典の正規表現 (reSections) の scan と
// 同じ結果になることをランダム入力で確認する。
func TestScanSectionsEquivalence(t *testing.T) {
	alphabet := []string{"h1. ", "h2(c). ", "h3.\t", "h", "1", ".", " ", "  ", "\n", "\n", "\r\n", "\r", "\t", "\v",
		"a", "x", "(", ")", "{color:red}", "h1.", "h10. ", "h1.:cite ", "\n\n", "\n \n", "日本"}
	rng := rand.New(rand.NewPCG(3, 4))
	for i := 0; i < 30000; i++ {
		var b strings.Builder
		k := 1 + rng.IntN(25)
		for j := 0; j < k; j++ {
			b.WriteString(alphabet[rng.IntN(len(alphabet))])
		}
		s := b.String()
		got := scanSections(s)
		var want []sectionMatch
		for _, m := range scan(reSections, s) {
			want = append(want, sectionMatch{all: m.s(1), content: m.s(2), heading: m.s(4), level: m.s(5), isHeading: m.ok(4)})
			if !m.ok(4) {
				break
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("mismatch for %q\nwant %#v\ngot  %#v", s, want, got)
		}
	}
}
