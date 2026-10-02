// SPDX-License-Identifier: GPL-2.0-or-later

package wikidiff

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// testdata/*.json は Redmine の Ruby 実装 (diff.rb / helpers/diff.rb / wiki_annotate.rb)
// を直接読み込んだスクリプトで生成したテストベクタである。

func loadJSON(t *testing.T, name string, v any) {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}

// rubyChange は Ruby の [sign, pos, elem] を JSON から読み込む。
type rubyChange Change

func (c *rubyChange) UnmarshalJSON(b []byte) error {
	var raw [3]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	c.Sign = raw[0].(string)[0]
	c.Pos = int(raw[1].(float64))
	c.Elem = raw[2].(string)
	return nil
}

func TestDiffStringsVectors(t *testing.T) {
	var cases []struct {
		A     []string       `json:"a"`
		B     []string       `json:"b"`
		Diffs [][]rubyChange `json:"diffs"`
	}
	loadJSON(t, "arraydiff.json", &cases)
	if len(cases) == 0 {
		t.Fatal("no cases")
	}
	for i, tc := range cases {
		var want [][]Change
		for _, h := range tc.Diffs {
			var hunk []Change
			for _, c := range h {
				hunk = append(hunk, Change(c))
			}
			want = append(want, hunk)
		}
		got := DiffStrings(tc.A, tc.B)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("case %d: DiffStrings(%q, %q)\n got  %v\n want %v", i, tc.A, tc.B, got, want)
		}
	}
}

func TestWordDiffHTMLVectors(t *testing.T) {
	var cases []struct {
		Name  string   `json:"name"`
		To    string   `json:"to"`
		From  string   `json:"from"`
		HTML  string   `json:"html"`
		Words []string `json:"words"`
	}
	loadJSON(t, "wordhtml.json", &cases)
	if len(cases) == 0 {
		t.Fatal("no cases")
	}
	for i, tc := range cases {
		// words は split(/(\s+)/) の結果 (" " の除去前)
		var wantWords []string
		for _, w := range tc.Words {
			if w != " " {
				wantWords = append(wantWords, w)
			}
		}
		if got := SplitWords(tc.To); !reflect.DeepEqual(got, wantWords) {
			t.Errorf("case %d %s: SplitWords(%q) = %q, want %q", i, tc.Name, tc.To, got, wantWords)
		}
		if got := WordDiffHTML(tc.To, tc.From); got != tc.HTML {
			t.Errorf("case %d %s: WordDiffHTML(%q, %q)\n got  %q\n want %q", i, tc.Name, tc.To, tc.From, got, tc.HTML)
		}
	}
}

// Redmine の test/unit/lib/redmine/helpers/diff_test.rb の test_dont_double_escape。
func TestWordDiffHTMLDontDoubleEscape(t *testing.T) {
	before := "<stuff> with html & special chars</danger>"
	after := "other stuff <script>alert('foo');</alert>"
	want := `<span class="diff_in">&lt;stuff&gt; with html &amp; special chars&lt;/danger&gt;</span>` +
		` <span class="diff_out">other stuff &lt;script&gt;alert(&#39;foo&#39;);&lt;/alert&gt;</span>`
	if got := WordDiffHTML(before, after); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAnnotateVectors(t *testing.T) {
	type jv struct {
		Version int    `json:"version"`
		Author  *int64 `json:"author"`
		Text    string `json:"text"`
	}
	var cases []struct {
		Versions []jv `json:"versions"`
		Lines    []jv `json:"lines"`
	}
	loadJSON(t, "annotate.json", &cases)
	if len(cases) == 0 {
		t.Fatal("no cases")
	}
	for i, tc := range cases {
		vers := make([]Version, len(tc.Versions))
		for j, v := range tc.Versions {
			vers[j] = Version{Version: v.Version, Text: v.Text}
			if v.Author != nil {
				vers[j].AuthorID = *v.Author
			}
		}
		prev := func(v Version) (Version, bool) {
			for j := range vers {
				if vers[j].Version == v.Version && j > 0 {
					return vers[j-1], true
				}
			}
			return Version{}, false
		}
		got := Annotate(vers[len(vers)-1], prev)
		want := make([]Line, len(tc.Lines))
		for j, l := range tc.Lines {
			want[j] = Line{Version: l.Version, Text: l.Text}
			if l.Author != nil {
				want[j].AuthorID = *l.Author
				want[j].HasAuthor = true
			}
		}
		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("case %d: Annotate(%+v)\n got  %+v\n want %+v", i, tc.Versions, got, want)
		}
	}
}

func TestSplitLines(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"\n", nil},
		{"a\r\n\r\nb\n\n", []string{"a", "", "b"}},
		{"a\r", []string{"a\r"}},
		{"a\r\r\nb", []string{"a\r", "b"}},
		{"\na", []string{"", "a"}},
	}
	for _, tt := range tests {
		got := SplitLines(tt.in)
		if len(got) == 0 && len(tt.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("SplitLines(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
