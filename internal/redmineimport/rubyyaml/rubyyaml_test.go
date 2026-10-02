package rubyyaml

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"
)

// testdata/fixtures.json は testdata/gen.rb で Ruby(Psych 5 / Rails 7.2)から生成した期待値。
// 型注釈: {"$sym": s} Symbol, {"$float": s} Float, {"$time": iso} Time, {"$date": s} Date,
// {"$hash": [[k, v], ...]} Hash(挿入順)。
type fixture struct {
	Name     string `json:"name"`
	YAML     string `json:"yaml"`
	Expected any    `json:"expected"`
}

func loadFixtures(t *testing.T) []fixture {
	t.Helper()
	b, err := os.ReadFile("testdata/fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var fs []fixture
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&fs); err != nil {
		t.Fatal(err)
	}
	return fs
}

// match は Ruby 側の注釈付き期待値と Go のデコード結果を比較する。
func match(path string, exp, got any) error {
	switch e := exp.(type) {
	case nil:
		if got != nil {
			return fmt.Errorf("%s: want nil, got %#v", path, got)
		}
		return nil
	case bool:
		if got != e {
			return fmt.Errorf("%s: want %v, got %#v", path, e, got)
		}
		return nil
	case json.Number:
		want, err := e.Int64()
		if err != nil {
			return fmt.Errorf("%s: bad fixture number %s", path, e)
		}
		if got != want {
			return fmt.Errorf("%s: want int64 %d, got %#v", path, want, got)
		}
		return nil
	case string:
		if got != e {
			return fmt.Errorf("%s: want %q, got %#v", path, e, got)
		}
		return nil
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(e) {
			return fmt.Errorf("%s: want array len %d, got %#v", path, len(e), got)
		}
		for i := range e {
			if err := match(fmt.Sprintf("%s[%d]", path, i), e[i], g[i]); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		if s, ok := e["$sym"]; ok {
			if got != Symbol(s.(string)) {
				return fmt.Errorf("%s: want Symbol %q, got %#v", path, s, got)
			}
			return nil
		}
		if s, ok := e["$date"]; ok {
			if got != Date(s.(string)) {
				return fmt.Errorf("%s: want Date %q, got %#v", path, s, got)
			}
			return nil
		}
		if s, ok := e["$float"]; ok {
			g, ok := got.(float64)
			if !ok {
				return fmt.Errorf("%s: want float %s, got %#v", path, s, got)
			}
			var want float64
			switch s {
			case "+Inf":
				want = math.Inf(1)
			case "-Inf":
				want = math.Inf(-1)
			case "NaN":
				if !math.IsNaN(g) {
					return fmt.Errorf("%s: want NaN, got %v", path, g)
				}
				return nil
			default:
				want, _ = strconv.ParseFloat(s.(string), 64)
			}
			if g != want {
				return fmt.Errorf("%s: want float %v, got %v", path, want, g)
			}
			return nil
		}
		if s, ok := e["$time"]; ok {
			want, err := time.Parse("2006-01-02T15:04:05.999999999-07:00", s.(string))
			if err != nil {
				return fmt.Errorf("%s: bad fixture time %v", path, s)
			}
			g, ok := got.(time.Time)
			if !ok || !g.Equal(want) {
				return fmt.Errorf("%s: want time %v, got %#v", path, want, got)
			}
			_, wo := want.Zone()
			_, gotOff := g.Zone()
			if wo != gotOff {
				return fmt.Errorf("%s: offset want %d got %d", path, wo, gotOff)
			}
			return nil
		}
		if h, ok := e["$hash"]; ok {
			pairs := h.([]any)
			g, ok := got.(OrderedMap)
			if !ok {
				return fmt.Errorf("%s: want hash, got %#v", path, got)
			}
			if len(g) != len(pairs) {
				return fmt.Errorf("%s: want %d keys, got %d (%#v)", path, len(pairs), len(g), g)
			}
			for i, p := range pairs {
				kv := p.([]any)
				if g[i].Key != kv[0].(string) {
					return fmt.Errorf("%s: key[%d] want %q got %q", path, i, kv[0], g[i].Key)
				}
				if err := match(path+"."+g[i].Key, kv[1], g[i].Value); err != nil {
					return err
				}
			}
			return nil
		}
	}
	return fmt.Errorf("%s: unsupported fixture value %#v", path, exp)
}

func TestRubyFixtures(t *testing.T) {
	fs := loadFixtures(t)
	if len(fs) < 50 {
		t.Fatalf("too few fixtures: %d", len(fs))
	}
	for _, f := range fs {
		t.Run(f.Name, func(t *testing.T) {
			got, err := DecodeWith(f.YAML, Options{Ordered: true})
			if err != nil {
				t.Fatalf("decode: %v\n%s", err, f.YAML)
			}
			if err := match("$", f.Expected, got); err != nil {
				t.Errorf("%v\nyaml:\n%s", err, f.YAML)
			}
			// 非順序モードでも同じ内容になること
			plain, err := Decode(f.YAML)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(Plain(plain), Plain(got)) && !hasNaN(got) {
				t.Errorf("ordered/unordered mismatch: %#v vs %#v", Plain(plain), Plain(got))
			}
		})
	}
}

func hasNaN(v any) bool {
	b, err := json.Marshal(Plain(v))
	return err != nil || string(b) == "" || containsNaN(Plain(v))
}

func containsNaN(v any) bool {
	switch x := v.(type) {
	case string:
		return x == "NaN"
	case []any:
		for _, e := range x {
			if containsNaN(e) {
				return true
			}
		}
	case map[string]any:
		for _, e := range x {
			if containsNaN(e) {
				return true
			}
		}
	}
	return false
}

func TestGoOnlyCases(t *testing.T) {
	cases := []struct {
		yaml string
		want any
	}{
		{"", nil},
		{"--- \n", nil},
		{"--- !ruby/struct:Point\nx: 1\ny: 2\n", map[string]any{"x": int64(1), "y": int64(2)}},
		{"--- !ruby/string:MyStr\nstr: hello\n:@ivar: 1\n", "hello"},
		{"--- !ruby/array:MyArr\ninternal:\n- 1\nivars:\n  :@x: 1\n", []any{int64(1)}},
		{"--- !ruby/object:BigDecimal 18:0.125e2\n", 12.5},
		{"--- !ruby/range 1..3\n", "1..3"},
		{"--- !ruby/regexp /ab+c/i\n", "/ab+c/i"},
		{"--- 99999999999999999999\n", "99999999999999999999"},
		{"--- !binary |-\n  Y2Fm6Q==\n", "café"},
		{"---\n:ruby_key: :ruby_val\n", map[string]any{"ruby_key": Symbol("ruby_val")}},
		{"--- !ruby/hash-with-ivars\nelements:\n  a: 1\nivars:\n  :@x: 2\n", map[string]any{"a": int64(1)}},
	}
	for _, c := range cases {
		got, err := Decode(c.yaml)
		if err != nil {
			t.Errorf("%q: %v", c.yaml, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %#v, want %#v", c.yaml, got, c.want)
		}
	}
}

func TestRecursiveAliasFails(t *testing.T) {
	// 自己参照(Psych では循環構造)は深さ制限でエラーにする
	_, err := Decode("--- &a\n- *a\n")
	if err == nil {
		t.Fatal("expected error for recursive alias")
	}
}

func TestOrderedMapJSON(t *testing.T) {
	v, err := DecodeWith("---\nz: 1\na: :sym\nm:\n  y: 2\n  b:\n  - :c\n", Options{Ordered: true})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"z":1,"a":"sym","m":{"y":2,"b":["c"]}}` {
		t.Errorf("json = %s", b)
	}
}

func TestInvalidYAML(t *testing.T) {
	if _, err := Decode("---\n- [unclosed\n"); err == nil {
		t.Error("expected error")
	}
}
