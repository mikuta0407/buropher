// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package htmldom

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/textformat/internal/fixtures"
)

type html5Case struct {
	Input    string `json:"input"`
	Expected string `json:"expected"`
	Error    string `json:"error"`
}

// TestHTML5Fixtures は Loofah.html5_fragment(input).to_s（Redmine 7.0.1 同梱の Nokogiri 1.19）で
// 生成した正解データ（testdata/html5.json）と比較する。
func TestHTML5Fixtures(t *testing.T) {
	path := os.Getenv("HTML5_FIXTURES")
	if path == "" {
		path = "testdata/html5.json"
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skip(err)
	}
	var cases []html5Case
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	pass := 0
	for i, c := range cases {
		frag, err := ParseHTML5Fragment(c.Input)
		if c.Error != "" {
			if err == nil {
				t.Errorf("#%d: want error %q, got none: %q", i, c.Error, trunc(c.Input))
			} else {
				pass++
			}
			continue
		}
		if err != nil {
			t.Errorf("#%d: unexpected error %v: %q", i, err, trunc(c.Input))
			continue
		}
		got := RenderHTML5(frag)
		c.Expected = fixtures.EscapeAttrAngles(c.Expected)
		if got != c.Expected {
			j := 0
			for j < len(got) && j < len(c.Expected) && got[j] == c.Expected[j] {
				j++
			}
			lo := max(0, j-80)
			t.Errorf("#%d:\ninput: %q\nwant: …%q\ngot:  …%q", i, trunc(c.Input), c.Expected[lo:min(len(c.Expected), j+80)], got[lo:min(len(got), j+80)])
			continue
		}
		pass++
	}
	t.Logf("html5: %d/%d", pass, len(cases))
}

func trunc(s string) string {
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

func TestHTML5Serialize(t *testing.T) {
	frag, err := ParseHTML5Fragment("<p title='a\"&b'>x&nbsp;<br/>&lt;</p><svg><path d=1 /></svg>")
	if err != nil {
		t.Fatal(err)
	}
	want := `<p title="a&quot;&amp;b">x&nbsp;<br>&lt;</p><svg><path d="1"></path></svg>`
	if got := RenderHTML5(frag); got != want {
		t.Errorf("got %q", got)
	}
	if _, err := ParseHTML5Fragment(strings.Repeat("<div>", 401)); err != ErrTreeTooDeep { //nolint:errorlint // 番兵値
		t.Errorf("want ErrTreeTooDeep, got %v", err)
	}
}
