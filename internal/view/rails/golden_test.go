package rails

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path"
	"reflect"
	"strings"
	"testing"
	"unicode"
)

// ゴールデンテスト: testdata/cases.json の各ケースを Go で実行し、
// tools/golden/rails_helpers.rb が実際の Redmine 6.1.2 で生成した testdata/golden.json と比較する。

type goldenCase struct {
	ID      string            `json:"id"`
	Fn      string            `json:"fn"`
	Args    json.RawMessage   `json:"args"`
	Seq     []goldenCase      `json:"seq"`
	Builder *goldenBuilderDef `json:"builder"`
	// Discard は seq の中で出力を捨てるステップ（ERB の <% %> で呼ばれるもの）。
	Discard bool `json:"discard"`
}

type goldenBuilderDef struct {
	Name  string `json:"name"`
	Model string `json:"model"`
	Plain bool   `json:"plain"`
}

type goldenResult struct {
	ID     string  `json:"id"`
	Output *string `json:"output"`
	Safe   bool    `json:"safe"`
}

type modelSpec struct {
	Name      string              `json:"name"`
	Persisted bool                `json:"persisted"`
	ID        *int                `json:"id"`
	Attrs     json.RawMessage     `json:"attrs"`
	Errors    map[string][]string `json:"errors"`
}

// testModel は Ruby 側の ActiveModel 製ダミーモデルに対応する。
type testModel struct {
	spec  modelSpec
	attrs *Hash
}

func (m *testModel) ParamKey() string { return m.spec.Name }
func (m *testModel) Persisted() bool  { return m.spec.Persisted }
func (m *testModel) ToParam() string {
	if m.spec.ID == nil {
		return ""
	}
	return ToS(*m.spec.ID)
}
func (m *testModel) ErrorsOn(attr string) []string {
	return m.spec.Errors[attr]
}
func (m *testModel) Send(method string) (any, bool) { return m.attrs.Lookup(method) }

// decodeOrdered は JSON を順序付き Hash を使って Go の値に変換する。
func decodeOrdered(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			h := &Hash{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				v, err := decodeOrdered(dec)
				if err != nil {
					return nil, err
				}
				h.Set(kt.(string), v)
			}
			_, err := dec.Token()
			return h, err
		case '[':
			out := []any{}
			for dec.More() {
				v, err := decodeOrdered(dec)
				if err != nil {
					return nil, err
				}
				out = append(out, v)
			}
			_, err := dec.Token()
			return out, err
		}
	case json.Number:
		if i, err := t.Int64(); err == nil && !strings.ContainsAny(t.String(), ".eE") {
			return int(i), nil
		}
		f, err := t.Float64()
		return f, err
	}
	return tok, nil
}

func parseOrdered(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return decodeOrdered(dec)
}

type goldenEnv struct {
	t      *testing.T
	models map[string]modelSpec
}

func (e *goldenEnv) model(name string) *testModel {
	spec, ok := e.models[name]
	if !ok {
		e.t.Fatalf("unknown model %s", name)
	}
	attrs, err := parseOrdered(spec.Attrs)
	if err != nil {
		e.t.Fatal(err)
	}
	return &testModel{spec: spec, attrs: attrs.(*Hash)}
}

// conv はケース中の特殊マーカー（$safe, $sym, $obj, $model, $call）を解決する。
func (e *goldenEnv) conv(v *View, x any) any {
	switch t := x.(type) {
	case []any:
		out := make([]any, len(t))
		for i, el := range t {
			out[i] = e.conv(v, el)
		}
		return out
	case *Hash:
		if s, ok := t.Lookup("$safe"); ok {
			return template.HTML(s.(string))
		}
		if s, ok := t.Lookup("$sym"); ok {
			return Symbol(s.(string))
		}
		if o, ok := t.Lookup("$obj"); ok {
			return o
		}
		if m, ok := t.Lookup("$model"); ok {
			return e.model(m.(string))
		}
		if c, ok := t.Lookup("$call"); ok {
			ch := c.(*Hash)
			return e.callFn(v, ch.Get("fn").(string), e.conv(v, ch.Get("args")).([]any))
		}
		n := &Hash{}
		for _, kv := range t.Entries() {
			n.Set(kv.Key, e.conv(v, kv.Value))
		}
		return n
	}
	return x
}

func newTestView() *View {
	v := NewView("TOKEN")
	v.FormName = func(base string) string { return base + "-abcd1234" }
	v.AssetPath = func(kind, source string) string {
		if strings.HasPrefix(source, "/") {
			return source
		}
		ext := path.Ext(source)
		return "/assets/" + strings.TrimSuffix(source, ext) + "-DIGEST" + ext
	}
	v.Translate = func(key string) string {
		return map[string]string{"field_subject": "Subject"}[key]
	}
	return v
}

// callReflect は関数値を引数列で呼び出す（nil 引数はゼロ値に変換）。
func callReflect(fn reflect.Value, args []any) any {
	ft := fn.Type()
	in := make([]reflect.Value, len(args))
	for i, a := range args {
		var pt reflect.Type
		if ft.IsVariadic() && i >= ft.NumIn()-1 {
			pt = ft.In(ft.NumIn() - 1).Elem()
		} else {
			pt = ft.In(i)
		}
		if a == nil {
			in[i] = reflect.Zero(pt)
		} else {
			av := reflect.ValueOf(a)
			if !av.Type().AssignableTo(pt) && av.Type().ConvertibleTo(pt) {
				av = av.Convert(pt)
			}
			in[i] = av
		}
	}
	out := fn.Call(in)
	if len(out) == 0 {
		return nil
	}
	return out[0].Interface()
}

func (e *goldenEnv) callFn(v *View, fn string, args []any) any {
	f, ok := v.FuncMap()[fn]
	if !ok {
		e.t.Fatalf("no template func %q", fn)
	}
	res := callReflect(reflect.ValueOf(f), args)
	if fb, ok := res.(*FormBuilder); ok {
		return fb.Open + "</form>"
	}
	return res
}

var typedFields = map[string]string{"email_field": "email", "number_field": "number", "url_field": "url", "file_field": "file", "search_field": "search", "telephone_field": "tel", "color_field": "color", "time_field": "time"}

func (e *goldenEnv) callBuilder(v *View, def *goldenBuilderDef, fn string, args []any) any {
	fb := v.NewFormBuilder(def.Name, e.model(def.Model), !def.Plain)
	if typ, ok := typedFields[fn]; ok {
		return callReflect(reflect.ValueOf(fb.TypedField), append([]any{typ}, args...))
	}
	var name strings.Builder
	for _, p := range strings.Split(fn, "_") {
		r := []rune(p)
		r[0] = unicode.ToUpper(r[0])
		name.WriteString(string(r))
	}
	m := reflect.ValueOf(fb).MethodByName(name.String())
	if !m.IsValid() {
		e.t.Fatalf("FormBuilder has no method %s", name.String())
	}
	// *Hash 型の可変長引数には ToHash 済みの値を渡す
	return callReflect(m, args)
}

func outputString(v any) (string, bool, bool) {
	switch x := v.(type) {
	case nil:
		return "", false, true
	case template.HTML:
		return string(x), true, false
	case string:
		return x, false, false
	case Symbol:
		return string(x), false, false
	}
	return fmt.Sprint(v), false, false
}

func TestGolden(t *testing.T) {
	var cases []goldenCase
	readJSON(t, "testdata/cases.json", &cases)
	var golden []goldenResult
	readJSON(t, "testdata/golden.json", &golden)
	var models map[string]modelSpec
	readJSON(t, "testdata/models.json", &models)
	want := map[string]goldenResult{}
	for _, g := range golden {
		want[g.ID] = g
	}
	env := &goldenEnv{t: t, models: models}
	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			env.t = t
			g, ok := want[c.ID]
			if !ok {
				t.Skip("golden.json にケースがない（tools/golden/rails_helpers.rb を再実行すること）")
			}
			v := newTestView()
			var got any
			if c.Seq != nil {
				var parts []string
				for _, s := range c.Seq {
					args := env.args(v, s.Args)
					out, _, _ := outputString(env.callFn(v, s.Fn, args))
					if !s.Discard {
						parts = append(parts, out)
					}
				}
				got = strings.Join(parts, "\n")
			} else if c.Builder != nil {
				got = env.callBuilder(v, c.Builder, c.Fn, env.args(v, c.Args))
			} else {
				got = env.callFn(v, c.Fn, env.args(v, c.Args))
			}
			out, safe, isNil := outputString(got)
			if g.Output == nil {
				if !isNil {
					t.Errorf("want nil, got %q", out)
				}
				return
			}
			if out != *g.Output {
				t.Errorf("出力不一致\n want: %q\n  got: %q", *g.Output, out)
			}
			if c.Seq == nil && safe != g.Safe {
				t.Errorf("html_safe 不一致: want %v got %v", g.Safe, safe)
			}
		})
	}
}

func (e *goldenEnv) args(v *View, raw json.RawMessage) []any {
	if len(raw) == 0 {
		return nil
	}
	a, err := parseOrdered(raw)
	if err != nil {
		e.t.Fatal(err)
	}
	return e.conv(v, a).([]any)
}

func readJSON(t *testing.T, file string, v any) {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
}
