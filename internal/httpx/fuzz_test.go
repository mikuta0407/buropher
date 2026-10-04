// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package httpx

import (
	"encoding/json"
	"testing"

	"github.com/mikuta0407/buropher/internal/secoracle"
)

// jsonDepth は JSON 値の入れ子の深さ（スカラーは 0）。
func jsonDepth(v any) int {
	d := 0
	switch x := v.(type) {
	case map[string]any:
		for _, c := range x {
			d = max(d, jsonDepth(c))
		}
		return d + 1
	case []any:
		for _, c := range x {
			d = max(d, jsonDepth(c))
		}
		return d + 1
	}
	return 0
}

// paramsDepth は解析結果の入れ子の深さ（JSON に直列化して数える）。
func paramsDepth(t *testing.T, v any) int {
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var x any
	if err := json.Unmarshal(b, &x); err != nil {
		t.Fatalf("unmarshal %q: %v", b, err)
	}
	return jsonDepth(x)
}

// FuzzParseRailsQuery は Rails 形式のクエリ・本文の解析と値の取り出し（params[:issue][:x] が文字列・配列・ハッシュの
// いずれでも）が panic しないことを確かめる。
func FuzzParseRailsQuery(f *testing.F) {
	for _, s := range []string{
		"issue[subject]=a&issue[custom_field_values][1][]=x&f[]=status_id&op[status_id]=o&v[status_id][]=1",
		"issue=x&issue[]=y&issue[a][b][c]=1&a[]=1&a[][b]=2&a[0]=z",
		"x[=1&]=2&%zz=3&a[b]c=4&&=&a=%E3%81%82",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, qs string) {
		secoracle.Bounded(t, len(qs), 256, func() { fuzzRailsQuery(t, qs) })
	})
}

func fuzzRailsQuery(t *testing.T, qs string) {
	{
		for _, parse := range []func(string) (*Params, error){ParseRailsQuery, ParseNestedQuery} {
			p, err := parse(qs)
			if err != nil || p == nil {
				continue
			}
			// Rack の param_depth_limit（32）を超える入れ子は ParamsTooDeepError になること。
			// 配列の要素のハッシュ（a[][b]）で 1 段増えるので余裕を見る
			if d := paramsDepth(t, p); d > DefaultParamDepthLimit+2 {
				t.Fatalf("%q parsed to depth %d (limit %d)", qs, d, DefaultParamDepthLimit)
			}
			_ = CheckParamEncoding(p)
			if m, ok := DeepMunge(p).(*Params); ok && m != nil {
				p = m
			}
			for _, k := range p.Keys() {
				_ = p.String(k)
				_ = p.Int(k)
				_, _ = p.IntStrict(k)
				_ = p.Bool(k)
				_ = p.Strings(k)
				_ = p.Ints(k)
				_ = p.Slice(k)
				_ = p.File(k)
				if m := p.Map(k); m != nil {
					for _, k2 := range m.Keys() {
						_ = p.String(k, k2)
						_ = p.Strings(k, k2)
						_ = p.Map(k, k2)
					}
				}
			}
			_ = p.Permit(Scalar("a"), ScalarArray("b"), Nested("issue", Scalar("subject"), AnyHash("custom_field_values")))
			_, _ = p.MarshalJSON()
		}
	}
}

func FuzzBodyDecoders(f *testing.F) {
	f.Add([]byte(`{"issue":{"subject":"a","custom_fields":[{"id":1,"value":["x"]}]}}`))
	f.Add([]byte(`<?xml version="1.0"?><issue><subject>a</subject><custom_fields type="array"><custom_field id="1"><value>x</value></custom_field></custom_fields></issue>`))
	f.Add([]byte(`<a><b><c><d><e><f><g><h><i><j>x</j></i></h></g></f></e></d></c></b></a>`))
	f.Add([]byte(`[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[[1]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]]`))
	f.Fuzz(func(t *testing.T, b []byte) {
		secoracle.Bounded(t, len(b), 256, func() {
			if v, err := DecodeJSON(b); err == nil {
				_ = DeepMunge(v)
			}
			if p, err := HashFromXML(b); err == nil && p != nil {
				_ = p.Keys()
				// 要素の入れ子は 100 段まで（xmlparams.go）。type="array" などで 1 段に 2 層を作り得るので倍まで
				if d := paramsDepth(t, p); d > 2*100+2 {
					t.Fatalf("XML parsed to depth %d", d)
				}
			}
		})
	})
}
