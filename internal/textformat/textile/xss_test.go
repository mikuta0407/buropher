// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package textile

import (
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/secoracle"
)

// 利用者が書いた <redpre#N ...> は内部の pre 退避用プレースホルダと同じ名前を持つ。
// 属性付きのものはエスケープする。属性なしの <redpre#N> は Redmine と同じ扱いのまま。
func TestRedpreTagWithAttributesIsEscaped(t *testing.T) {
	for _, src := range []string{
		"<redpre#0 onmouseover=alert(1) style=display:block;position:fixed;width:100%;height:100%>0",
		"hello\n\n<redpre#5 onmouseover=alert(1)>\n\n*bold*",
		"a <redpre#1\tonclick=alert(1)> b",
		"<redpre#0/onclick=alert(1)>x",
	} {
		out := Format(src, nil)
		if err := secoracle.CheckHTML(out, secoracle.HTMLPolicy{SVG: true}); err != nil {
			t.Errorf("Format(%q) = %q\n%v", src, out, err)
		}
		if strings.Contains(out, "<redpre") {
			t.Errorf("Format(%q) = %q: raw redpre tag", src, out)
		}
	}
	// 属性なしは従来どおり（Redmine と同じ。HTML5 として解析し直すため閉じタグが付く）
	if got := Format("<redpre#0>0", nil); got != "<redpre#0>0</redpre#0>" {
		t.Errorf("Format(<redpre#0>0) = %q", got)
	}
}

// 利用者が書いた ":redsh#N:"（shelve の目印と同じ形）は retrieve で展開せず、そのまま出力する。
func TestUserWrittenShelfMarkerIsNotRetrieved(t *testing.T) {
	for _, src := range []string{
		`"x":http://a/onmouseover=alert(1)// ABC(q :redsh#1:)`,
		`"x":http://a/onmouseover=alert(1)// <pre><code class="q :redsh#1:">z</code></pre>`,
		"\"x\":http://a/onmouseover=alert(1)// ABC(q :redsh#1:) ABC(q :redsh#0:) ABC(q :redsh#-1:)",
		"p(c). \"x\":http://a/onmouseover=alert(1)//\n\n<code> :redsh#1:</code> @ :redsh#2:@",
	} {
		out := Format(src, nil)
		if err := secoracle.CheckHTML(out, secoracle.HTMLPolicy{SVG: true}); err != nil {
			t.Errorf("Format(%q) = %q\n%v", src, out, err)
		}
		if !strings.Contains(out, ":redsh#") {
			t.Errorf("Format(%q) = %q: user text :redsh# lost", src, out)
		}
		if strings.Contains(out, "\uFFFF") {
			t.Errorf("Format(%q) = %q: internal marker leaked", src, out)
		}
	}
	if got := Format("a :redsh#1: b", nil); got != "<p>a :redsh#1: b</p>" {
		t.Errorf("Format(a :redsh#1: b) = %q", got)
	}
}
