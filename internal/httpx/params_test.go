package httpx

import (
	"reflect"
	"testing"
)

func mustParse(t *testing.T, qs string) *Params {
	t.Helper()
	p, err := ParseRailsQuery(qs)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParamsAccessors(t *testing.T) {
	p := mustParse(t, "issue[subject]=Hi&issue[project_id]=12abc&issue[is_private]=0&issue[done]=1&ids[]=3&ids[]=x&ids[]=5&per_page=25&empty=&blank=+++&nilv&h[a]=1")
	if p.String("issue", "subject") != "Hi" || p.String("issue", "missing") != "" || p.String("ids") != "" {
		t.Error("String")
	}
	if p.Int("issue", "project_id") != 12 || p.Int("per_page") != 25 || p.Int("missing") != 0 || p.Int("issue", "subject") != 0 {
		t.Error("Int (to_i semantics)")
	}
	if _, ok := p.IntStrict("issue", "project_id"); ok {
		t.Error("IntStrict accepted 12abc")
	}
	if n, ok := p.IntStrict("per_page"); !ok || n != 25 {
		t.Error("IntStrict")
	}
	if p.Bool("issue", "is_private") || !p.Bool("issue", "done") || p.Bool("empty") || p.Bool("missing") || p.Bool("nilv") {
		t.Error("Bool")
	}
	if !reflect.DeepEqual(p.Strings("ids"), []string{"3", "x", "5"}) || !reflect.DeepEqual(p.Strings("per_page"), []string{"25"}) || p.Strings("missing") != nil {
		t.Error("Strings")
	}
	if !reflect.DeepEqual(p.Ints("ids"), []int64{3, 0, 5}) {
		t.Error("Ints")
	}
	if len(p.Slice("ids")) != 3 || p.Slice("per_page") != nil {
		t.Error("Slice")
	}
	if p.Map("issue").String("subject") != "Hi" || p.Map("per_page") != nil || p.Map("missing").String("x") != "" {
		t.Error("Map (nil-safe)")
	}
	if !p.Has("nilv") || !p.Has("issue", "subject") || p.Has("issue", "nope") || !p.Has("ids", "1") {
		t.Error("Has")
	}
	if p.Present("nilv") || p.Present("empty") || p.Present("blank") || !p.Present("per_page") {
		t.Error("Present")
	}
	if got := mustJSON(t, p.Only("per_page", "nope")); got != `{"per_page":"25"}` {
		t.Error("Only", got)
	}
	if p.Except("issue", "ids").Has("issue") {
		t.Error("Except")
	}
	if p.String("ids", "0") != "3" {
		t.Error("array index lookup")
	}
}

func TestParamsMutationAndOrder(t *testing.T) {
	p := NewParams()
	p.Set("b", "1")
	p.Set("a", "2")
	p.Set("b", "3")
	if !reflect.DeepEqual(p.Keys(), []string{"b", "a"}) || p.String("b") != "3" {
		t.Error("order")
	}
	if p.Delete("b") != "3" || p.Len() != 1 || p.Delete("zz") != nil {
		t.Error("Delete")
	}
	q := NewParams()
	q.Set("a", "x")
	q.Set("c", "y")
	m := p.Merge(q)
	if mustJSON(t, m) != `{"a":"x","c":"y"}` || p.String("a") != "2" {
		t.Error("Merge")
	}
	c := mustParse(t, "x[y][]=1").Clone()
	c.Map("x").Set("y", "changed")
	if c.String("x", "y") != "changed" {
		t.Error("Clone")
	}
	if got := c.ToMap(); got["x"].(map[string]any)["y"] != "changed" {
		t.Error("ToMap")
	}
	var nilp *Params
	if nilp.Len() != 0 || nilp.String("a") != "" || nilp.Has("a") {
		t.Error("nil receiver")
	}
}

func TestParamsTypedValuesFromJSON(t *testing.T) {
	v, err := DecodeJSON([]byte(`{"i":3,"f":1.5,"f1":2.0,"b":true,"s":"7"}`))
	if err != nil {
		t.Fatal(err)
	}
	p := v.(*Params)
	if p.Int("i") != 3 || p.String("i") != "3" || p.String("f") != "1.5" || p.String("f1") != "2.0" || p.Int("f") != 1 {
		t.Error("numeric conversions")
	}
	if !p.Bool("b") || p.String("b") != "true" || p.Int("s") != 7 {
		t.Error("bool/string")
	}
	if s, ok := p.StringOK("i"); !ok || s != "3" {
		t.Error("StringOK")
	}
}

func TestPermit(t *testing.T) {
	p := mustParse(t, "issue[subject]=S&issue[admin]=1&issue[watcher_user_ids][]=1&issue[watcher_user_ids][]=2"+
		"&issue[custom_field_values][1]=a&issue[custom_field_values][2][]=b"+
		"&issue[tags][x]=1&issue[uploads][][token]=t1&issue[uploads][][filename]=f1&issue[uploads][][evil]=e"+
		"&issue[nested][0][name]=n0&issue[nested][0][bad]=x&issue[nested][1][name]=n1&issue[subject_arr][]=x")
	got := p.Map("issue").Permit(
		Scalar("subject"),
		Scalar("subject_arr"), // 配列は Scalar では許可されない
		ScalarArray("watcher_user_ids"),
		AnyHash("custom_field_values"),
		Nested("uploads", Scalar("token"), Scalar("filename")),
		Nested("nested", Scalar("name")),
		Scalar("missing"),
	)
	want := `{"subject":"S","watcher_user_ids":["1","2"],"custom_field_values":{"1":"a","2":["b"]},` +
		`"uploads":[{"token":"t1","filename":"f1"}],"nested":{"0":{"name":"n0"},"1":{"name":"n1"}}}`
	if g := mustJSON(t, got); g != want {
		t.Errorf("permit:\n got  %s\n want %s", g, want)
	}
	// スカラー配列にハッシュが混じれば不許可
	q := mustParse(t, "a[][x]=1")
	if q.Permit(ScalarArray("a")).Has("a") {
		t.Error("array of hashes permitted as scalar array")
	}
}

func TestRubyHelpers(t *testing.T) {
	cases := map[string]int64{"12": 12, " 34x": 34, "-5": -5, "+7": 7, "abc": 0, "": 0, "1_000": 1000, "99999999999999999999": 9223372036854775807}
	for in, want := range cases {
		if got := RubyToI(in); got != want {
			t.Errorf("RubyToI(%q)=%d", in, got)
		}
	}
	if rubyToF("1.5abc") != 1.5 || rubyToF("x") != 0 || rubyToF("2e3") != 2000 {
		t.Error("rubyToF")
	}
	if !IsBlank(" \t") || IsBlank("a") || !IsBlank([]any{}) || !IsBlank(NewParams()) || IsBlank(int64(0)) {
		t.Error("IsBlank")
	}
}
