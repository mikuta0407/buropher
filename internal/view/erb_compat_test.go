package view

import (
	"encoding/json"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testdata/erb/*.tmpl を描画し、tools/golden/erb_whitespace.rb が実際の Rails（Erubi）で
// 同名の *.erb を描画した結果 *.out とバイト単位で比較する。
func TestERBCompat(t *testing.T) {
	e, err := New(Options{FS: os.DirFS("testdata/erb")})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/erb/data.json")
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	data["html_safe"] = template.HTML(data["html_safe"].(string))
	data["num"] = 42 // JSON の数値は float64 になるため整数に戻す

	outs, _ := filepath.Glob("testdata/erb/*.out")
	if len(outs) == 0 {
		t.Fatal("testdata/erb/*.out がない")
	}
	for _, out := range outs {
		name := strings.TrimSuffix(filepath.Base(out), ".out")
		t.Run(name, func(t *testing.T) {
			want, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			ctx := &Context{CSRFToken: "TOKEN", FormNameSuffix: func() string { return "abcd1234" }, AppTitle: "Redmine"}
			got, err := e.Render(ctx, name, data, RenderOptions{Layout: NoLayout})
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Errorf("ERB 出力と不一致\n--- want\n%s\n--- got\n%s\n--- diff\n%s", want, got, lineDiff(string(want), string(got)))
			}
		})
	}
}

// lineDiff は最初に異なる行を示す簡易差分。
func lineDiff(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(al) || i < len(bl); i++ {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			return "line " + itoa(i+1) + ":\n want: " + quote(x) + "\n  got: " + quote(y)
		}
	}
	return ""
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
