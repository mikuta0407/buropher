// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mikuta0407/buropher/tools/compat/internal/report"
)

// リポジトリに含まれるシナリオ・許容リスト・ゴールデンファイルの整合性を確認する（サーバ不要）。
func TestRepoScenariosHaveGoldens(t *testing.T) {
	root := filepath.Join("..", "..")
	files, err := filepath.Glob(filepath.Join(root, defaultScenarioGlob))
	if err != nil || len(files) == 0 {
		t.Fatalf("no scenarios: %v", err)
	}
	cases, err := loadCases(files, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if c.Skip != "" {
			continue
		}
		if _, ok := goldenPath(filepath.Join(root, defaultGoldenDir), c); !ok {
			t.Errorf("golden missing for %s/%s（snapshot を実行してください）", c.Scenario, c.ID)
		}
	}
	if _, err := report.LoadAllowlist(filepath.Join(root, defaultAllowlist)); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tools", "compat", "redmine-ref.sh")); err != nil {
		t.Error(err)
	}
}
