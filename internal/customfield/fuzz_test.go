// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package customfield

import (
	"testing"

	"github.com/mikuta0407/buropher/internal/secoracle"
	"github.com/mikuta0407/buropher/internal/view/rails"
)

// FuzzSanitizedLink はリンク形式のカスタムフィールドの値（利用者が入力する URL）から作るリンクを
// ブラウザと同じ規則で解析し、href が危険なスキーム（文字参照・タブ・改行・大小文字の変化を含む）に
// ならないこと、属性から脱出しないことを確かめる。
//
//	go test -run '^$' -fuzz FuzzSanitizedLink ./internal/customfield
func FuzzSanitizedLink(f *testing.F) {
	for _, s := range []string{
		"javascript:alert(1)", "java\tscript:alert(1)", "javascript&colon;alert(1)", "&#106;avascript:alert(1)",
		" JaVaScRiPt:alert(1)", "\x01javascript:alert(1)", "data:text/html,<script>", "vbscript:x", "http://x/\"onmouseover=\"alert(1)",
		"//evil.com", "/\\evil.com", "mailto:a@b?subject=<script>", "jav&#x0A;ascript:x", "javascript&#0000058alert(1)",
		"javascript&#x3A;x", "javas&Tab;cript:x", "&NewLine;javascript:x",
	} {
		f.Add(s)
	}
	policy := secoracle.HTMLPolicy{}
	f.Fuzz(func(t *testing.T, u string) {
		out := string(sanitizedLink(rails.H("text"), u))
		if err := secoracle.CheckHTML(out, policy); err != nil {
			t.Fatalf("sanitizedLink(%q) = %q\n%v", u, out, err)
		}
		out = string(LinkValueHTML(u))
		if err := secoracle.CheckHTML(out, policy); err != nil {
			t.Fatalf("LinkValueHTML(%q) = %q\n%v", u, out, err)
		}
	})
}
