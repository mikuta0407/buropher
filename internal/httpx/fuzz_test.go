// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package httpx

import "testing"

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
		for _, parse := range []func(string) (*Params, error){ParseRailsQuery, ParseNestedQuery} {
			p, err := parse(qs)
			if err != nil || p == nil {
				continue
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
	})
}

func FuzzBodyDecoders(f *testing.F) {
	f.Add([]byte(`{"issue":{"subject":"a","custom_fields":[{"id":1,"value":["x"]}]}}`))
	f.Add([]byte(`<?xml version="1.0"?><issue><subject>a</subject><custom_fields type="array"><custom_field id="1"><value>x</value></custom_field></custom_fields></issue>`))
	f.Fuzz(func(t *testing.T, b []byte) {
		if v, err := DecodeJSON(b); err == nil {
			_ = DeepMunge(v)
		}
		if p, err := HashFromXML(b); err == nil && p != nil {
			_ = p.Keys()
		}
	})
}
