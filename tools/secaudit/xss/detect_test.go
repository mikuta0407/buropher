// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package main

import "testing"

func kinds(fs []finding) map[string]bool {
	m := map[string]bool{}
	for _, f := range fs {
		m[f.key()] = true
	}
	return m
}

func TestCheckHTML(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`<p>&lt;img src=x onerror=xss(1)&gt;</p>`, nil},
		{`<p><img src=x onerror=xss(1)></p>`, []string{"attr-event#1"}},
		{`<a href=" javascript:xss(2)">x</a>`, []string{"url-scheme#2"}},
		{`<a href="http://e/?q=javascript:xss(2)">x</a>`, nil},
		{`<input value="a"-xss(3)-"<img src=x onerror=xss(3)>">`, []string{"attr-event#3"}},
		{`<script>var a = "x'-xss(4)-'";</script>`, nil},
		{`<script>var a = 'x'-xss(4)-'';</script>`, []string{"script-code#4"}},
		{`<script>$('#a').html('<img src=x onerror=xss(5)>')</script>`, []string{"script-string-attr-event#5"}},
		{`<script>$('#a').html('&lt;img src=x onerror=xss(5)&gt;')</script>`, nil},
	}
	for _, c := range cases {
		got := kinds(checkHTML(c.in, ""))
		if len(got) != len(c.want) {
			t.Errorf("%s: got %v want %v", c.in, got, c.want)
			continue
		}
		for _, w := range c.want {
			if !got[w] {
				t.Errorf("%s: got %v want %v", c.in, got, c.want)
			}
		}
	}
}

func TestCheckJS(t *testing.T) {
	if fs := checkJS(`$('#x').html('<p>\'-xss(1)-\'<\/p>');`); len(fs) != 0 {
		t.Errorf("escaped: %v", fs)
	}
	if fs := checkJS(`$('#x').html('<p>'-xss(1)-'</p>');`); !kinds(fs)["js-code#1"] {
		t.Errorf("breakout: %v", fs)
	}
	if fs := checkJS(`var r = /a'b/; x('xss(1)')`); len(fs) != 0 {
		t.Errorf("regex: %v", fs)
	}
}

func TestCheckCSV(t *testing.T) {
	fs := checkCSV([]byte("a,b\n1,\"=cmd|' /C xss(7)'!A0\"\n"))
	if !kinds(fs)["csv-formula#7"] {
		t.Errorf("csv: %v", fs)
	}
}
