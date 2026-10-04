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
		if err := secoracle.CheckHTML(out, secoracle.HTMLPolicy{}); err != nil {
			t.Errorf("Format(%q) = %q\n%v", src, out, err)
		}
		if strings.Contains(out, "<redpre") {
			t.Errorf("Format(%q) = %q: raw redpre tag", src, out)
		}
	}
	// 属性なしは従来どおり（Redmine と同じ）
	if got := Format("<redpre#0>0", nil); got != "<redpre#0>0" {
		t.Errorf("Format(<redpre#0>0) = %q", got)
	}
}
