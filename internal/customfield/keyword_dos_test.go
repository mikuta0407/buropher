// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package customfield

import (
	"strings"
	"testing"
	"time"
)

// 複数値のキーワード（メール受信の "Field: x,x,x,..." や CSV インポートのセル）は、一致しない要素が
// 多くても線形時間で解析する（以前は長さの 2 乗で、数百 KB の値で数十秒以上かかった）。
func TestValueFromKeywordMultipleIsLinear(t *testing.T) {
	env := testEnv(t)
	cf := field("list", withValues("Foo", "Bar", "Baz,qux"))
	cf.Multiple = true
	kw := strings.Repeat("x,", 200000) + "foo,baz,qux"
	start := time.Now()
	got := Find("list").ValueFromKeyword(env, cf, kw, nil).([]string)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("ValueFromKeyword took %v", d)
	}
	if strings.Join(got, "|") != "Foo|Baz,qux" {
		t.Errorf("values = %q", got)
	}
}

// 上限を設けても最長一致の結果は変わらない（カンマを含むラベルが優先される）。
func TestParseKeywordLongestMatchWithLimit(t *testing.T) {
	cf := &CustomField{Multiple: true}
	labels := []string{"a", "b", "a,b", "a,b,c", "c"}
	find := func(k string) (string, bool) {
		for _, l := range labels {
			if strings.EqualFold(l, k) {
				return l, true
			}
		}
		return "", false
	}
	for kw, want := range map[string]string{
		"a,b,c,a,b": "a,b,c|a,b", "a,b,x,c": "a,b|c", "x,a , b ,c": "a|b|c", "a,b,c,c": "a,b,c|c", "": "",
	} {
		if got := strings.Join(ParseKeyword(cf, kw, MaxCommas(labels...), find).([]string), "|"); got != want {
			t.Errorf("%q = %q, want %q", kw, got, want)
		}
	}
}
