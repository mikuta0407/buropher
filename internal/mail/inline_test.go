// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package mail

import (
	"encoding/json"
	"os"
	"testing"
)

// TestInlineCSSGolden は roadie（Redmine 7.0.1 同梱版）の出力と一致することを確かめる。
// 期待値は testdata/gen_roadie.rb（入力は make_inputs.py が作る roadie_inputs.json）で生成する。
func TestInlineCSSGolden(t *testing.T) {
	head, err := os.ReadFile("testdata/layout_head.html")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("testdata/roadie_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name, Body, Expected string
	}
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			src := string(head) + "\n<body>\n" + c.Body + "</body>\n</html>\n"
			got, err := InlineCSS(src, URLOptions{Protocol: "http", Host: "localhost", Port: 3000})
			if err != nil {
				t.Fatal(err)
			}
			if got != c.Expected {
				t.Errorf("mismatch\n--- got\n%s\n--- want\n%s", got, c.Expected)
			}
		})
	}
}
