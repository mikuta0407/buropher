// Copyright (C) 2026 buropher contributors
// SPDX-License-Identifier: GPL-2.0-or-later

package textile

import (
	"encoding/json"
	"os"
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
