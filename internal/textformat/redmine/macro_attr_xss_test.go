// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package redmine

import (
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/secoracle"
)

// includeTextStore は include で取り込むページの本文を差し替える。
type includeTextStore struct {
	*DBStore
	text string
}

func (s includeTextStore) WikiPageText(pageID int64) (string, bool, error) {
	return s.text, true, nil
}

// TestMacroInsideAttributeIsNotExecuted は画像・リンクの title や生 HTML の属性値の中に置いたマクロが
// 実行されず、出力に許可外の属性が現れないことを確かめる。
func TestMacroInsideAttributeIsNotExecuted(t *testing.T) {
	e := newTestEnv(t)
	cases := []struct {
		formatting, included string
		payloads             []string
	}{
		{"common_mark", `<span title=" onerror=alert(document.domain) x">y</span>`, []string{
			`![x](http://a/i.png "{{include(Another_page)}}")`,
			`[x](http://a "{{include(Another_page)}}")`,
			`<span title="{{include(Another_page)}}">z</span>`,
			`<span title='a>{{include(Another_page)}}'>z</span>`,
			`[x](http://a "{{collapse(a)}}")`,
		}},
		{"textile", `"y":http://x/onerror=alert(document.domain)//`, []string{
			`!http://a/i.png({{include(Another_page)}})!`,
			`"x({{include(Another_page)}})":http://a`,
			`ABC({{include(Another_page)}})`,
			`p{color:red}({{collapse(a)}}). x`,
		}},
	}
	for _, c := range cases {
		for _, p := range c.payloads {
			r, st := e.renderer(t, "admin", "ecookbook", c.formatting)
			r.Store = includeTextStore{DBStore: st, text: c.included}
			var o Options
			if obj, err := st.LoadObject("issue", 1); err == nil {
				o.Object = obj
			}
			out := string(r.Textilizable(p, o))
			if err := secoracle.CheckHTML(out, textilizablePolicy); err != nil {
				t.Errorf("[%s] %q\n=> %s\n%v", c.formatting, p, out, err)
			}
			if strings.Contains(out, `title="<p>`) {
				t.Errorf("[%s] %q: included page rendered inside an attribute: %s", c.formatting, p, out)
			}
		}
	}

	// タグの外のマクロは従来どおり実行される
	r, st := e.renderer(t, "admin", "ecookbook", "common_mark")
	r.Store = includeTextStore{DBStore: st, text: "included *text*"}
	out := string(r.Textilizable(`<span title="a>b">x</span> {{include(Another_page)}} <!-- {{include(Another_page)}} -->`, Options{}))
	if strings.Count(out, "<em>text</em>") != 1 {
		t.Errorf("include outside tags: %s", out)
	}
}
