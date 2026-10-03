// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package httpx

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Rack の spec_utils.rb「parse nested query strings correctly」由来のケース。
// 期待値はキー順を保持した JSON（nil は null）。
func TestParseNestedQueryRackSpec(t *testing.T) {
	cases := []struct{ qs, want string }{
		{"foo", `{"foo":null}`},
		{"foo=", `{"foo":""}`},
		{"foo=bar", `{"foo":"bar"}`},
		{`foo="bar"`, `{"foo":"\"bar\""}`},
		{"foo=bar&foo=quux", `{"foo":"quux"}`},
		{"foo&foo=", `{"foo":""}`},
		{"foo=1&bar=2", `{"foo":"1","bar":"2"}`},
		{"&foo=1&&bar=2", `{"foo":"1","bar":"2"}`},
		{"foo&bar=", `{"foo":null,"bar":""}`},
		{"foo=bar&baz=", `{"foo":"bar","baz":""}`},
		{"my+weird+field=q1%212%22%27w%245%267%2Fz8%29%3F", `{"my weird field":"q1!2\"'w$5\u00267/z8)?"}`},
		{"a=b&pid%3D1234=1023", `{"a":"b","pid=1234":"1023"}`},
		{"foo[]", `{"foo":[null]}`},
		{"foo[]=", `{"foo":[""]}`},
		{"foo[]=bar", `{"foo":["bar"]}`},
		{"foo[]=bar&foo", `{"foo":null}`},
		{"foo[]=bar&foo[", `{"foo":["bar"],"foo[":null}`},
		{"foo[]=bar&foo[=baz", `{"foo":["bar"],"foo[":"baz"}`},
		{"foo[]=bar&foo[]", `{"foo":["bar",null]}`},
		{"foo[]=bar&foo[]=", `{"foo":["bar",""]}`},
		{"foo[]=1&foo[]=2", `{"foo":["1","2"]}`},
		{"foo=bar&baz[]=1&baz[]=2&baz[]=3", `{"foo":"bar","baz":["1","2","3"]}`},
		{"foo[]=bar&baz[]=1&baz[]=2&baz[]=3", `{"foo":["bar"],"baz":["1","2","3"]}`},
		{"x[y][z]", `{"x":{"y":{"z":null}}}`},
		{"x[y][z]=1", `{"x":{"y":{"z":"1"}}}`},
		{"x[y][z][]=1", `{"x":{"y":{"z":["1"]}}}`},
		{"x[y][z]=1&x[y][z]=2", `{"x":{"y":{"z":"2"}}}`},
		{"x[y][z][]=1&x[y][z][]=2", `{"x":{"y":{"z":["1","2"]}}}`},
		{"x[y][][z]=1", `{"x":{"y":[{"z":"1"}]}}`},
		{"x[y][][z][]=1", `{"x":{"y":[{"z":["1"]}]}}`},
		{"x[y][][z]=1&x[y][][w]=2", `{"x":{"y":[{"z":"1","w":"2"}]}}`},
		{"x[y][][v][w]=1", `{"x":{"y":[{"v":{"w":"1"}}]}}`},
		{"x[y][][z]=1&x[y][][v][w]=2", `{"x":{"y":[{"z":"1","v":{"w":"2"}}]}}`},
		{"x[y][][z]=1&x[y][][z]=2", `{"x":{"y":[{"z":"1"},{"z":"2"}]}}`},
		{"x[y][][z]=1&x[y][][w]=a&x[y][][z]=2&x[y][][w]=3", `{"x":{"y":[{"z":"1","w":"a"},{"z":"2","w":"3"}]}}`},
		{"x[][y]=1&x[][z][w]=a&x[][y]=2&x[][z][w]=b", `{"x":[{"y":"1","z":{"w":"a"}},{"y":"2","z":{"w":"b"}}]}`},
		{"x[][z][w]=a&x[][y]=1&x[][z][w]=b&x[][y]=2", `{"x":[{"z":{"w":"a"},"y":"1"},{"z":{"w":"b"},"y":"2"}]}`},
		{"data[books][][data][page]=1&data[books][][data][page]=2", `{"data":{"books":[{"data":{"page":"1"}},{"data":{"page":"2"}}]}}`},
		// 先頭の [ は特別扱いしない
		{"[]=1", `{"[]":"1"}`},
		{"[a]=1", `{"[a]":"1"}`},
		{"[a][b]=1", `{"[a]":{"b":"1"}}`},
		// x[][] はネスト配列
		{"x[][]=1", `{"x":[["1"]]}`},
		// エンコードされたブラケットもデコード後に解釈される
		{"foo%5B%5D=1&foo%5B%5D=2", `{"foo":["1","2"]}`},
		// 区切りは /& */（& の後の空白は無視）
		{"foo=1& bar=2", `{"foo":"1","bar":"2"}`},
		// nil は ||= で上書きされる
		{"x&x[y]=1", `{"x":{"y":"1"}}`},
		{"x&x[]=1", `{"x":["1"]}`},
		// 閉じブラケットの後ろの文字列
		{"x[y]z=1", `{"x":{"y":{"z":"1"}}}`},
		{"x[]]=1", `{"x":[{"]":"1"}]}`},
		// Redmine でよく使う形
		{"set_filter=1&f[]=status_id&op[status_id]=o&v[status_id][]=1&v[status_id][]=2&c[]=tracker&c[]=subject",
			`{"set_filter":"1","f":["status_id"],"op":{"status_id":"o"},"v":{"status_id":["1","2"]},"c":["tracker","subject"]}`},
		{"issue[subject]=Hello+World&issue[watcher_user_ids][]=2&issue[watcher_user_ids][]=3&issue[custom_field_values][1]=x",
			`{"issue":{"subject":"Hello World","watcher_user_ids":["2","3"],"custom_field_values":{"1":"x"}}}`},
		{"x[[b]]=1", `{"x":{"[b":{"]":"1"}}}`},
		{"=foo&=", `{}`},
		{"", `{}`},
	}
	for _, c := range cases {
		p, err := ParseNestedQuery(c.qs)
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.qs, err)
			continue
		}
		if got := mustJSON(t, p); got != c.want {
			t.Errorf("%q:\n got  %s\n want %s", c.qs, got, c.want)
		}
	}
}

func TestParseNestedQueryErrors(t *testing.T) {
	cases := []struct {
		qs   string
		kind ParamErrorKind
		msg  string
	}{
		{"x[y]=1&x[y]z=2", ParamTypeError, "expected Hash (got String) for param `y'"},
		{"x[y]=1&x[]=1", ParamTypeError, "expected Array (got Rack::QueryParser::Params) for param `x'"},
		{"x[y]=1&x[y][][w]=2", ParamTypeError, "expected Array (got String) for param `y'"},
		{"x[]=1&x[y]=2", ParamTypeError, "expected Hash (got Array) for param `x'"},
		{"foo=%", ParamInvalid, ""},
		{"foo%=1", ParamInvalid, ""},
		{"foo=%zz", ParamInvalid, ""},
		{"a" + strings.Repeat("[a]", 40) + "=1", ParamLimit, ""},
	}
	for _, c := range cases {
		_, err := ParseNestedQuery(c.qs)
		pe, ok := err.(*ParamError)
		if !ok {
			t.Errorf("%q: expected ParamError, got %v", c.qs, err)
			continue
		}
		if pe.Kind != c.kind || (c.msg != "" && pe.Msg != c.msg) {
			t.Errorf("%q: got (%v, %q), want (%v, %q)", c.qs, pe.Kind, pe.Msg, c.kind, c.msg)
		}
		if pe.Status() != 400 {
			t.Errorf("%q: status %d", c.qs, pe.Status())
		}
	}
}

func TestParseNestedQueryLimits(t *testing.T) {
	qp := &QueryParser{DepthLimit: 32, BytesizeLimit: 10, ParamsLimit: 3}
	if _, err := qp.ParseNestedQuery("a=1&b=2&c=3&d=4"); err == nil {
		t.Error("bytesize limit not enforced")
	}
	qp.BytesizeLimit = 1000
	if _, err := qp.ParseNestedQuery("a&b&c&d"); err == nil || !strings.Contains(err.Error(), "exceeds limit (3)") {
		t.Errorf("params limit: %v", err)
	}
	if _, err := qp.ParseNestedQuery("a&b&c"); err != nil {
		t.Errorf("params at limit: %v", err)
	}
	// 深さ 32 未満は OK
	if _, err := ParseNestedQuery("a" + strings.Repeat("[a]", 30) + "=1"); err != nil {
		t.Errorf("depth 31: %v", err)
	}
}

func TestParseRailsQueryDeepMunge(t *testing.T) {
	cases := []struct{ qs, want string }{
		{"foo[]", `{"foo":[]}`},
		{"foo[]=bar&foo[]", `{"foo":["bar"]}`},
		{"x[y][][z]=1", `{"x":{"y":[{"z":"1"}]}}`},
		{"foo", `{"foo":null}`},
	}
	for _, c := range cases {
		p, err := ParseRailsQuery(c.qs)
		if err != nil {
			t.Fatal(err)
		}
		if got := mustJSON(t, p); got != c.want {
			t.Errorf("%q: got %s want %s", c.qs, got, c.want)
		}
	}
	// 不正な UTF-8 は 400
	if _, err := ParseRailsQuery("a=%FF"); err == nil {
		t.Error("invalid utf-8 accepted")
	}
}
