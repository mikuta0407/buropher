// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package rubyyaml

import (
	"testing"

	"github.com/mikuta0407/buropher/internal/secoracle"
)

func FuzzDecode(f *testing.F) {
	for _, s := range []string{
		"---\n- 1\n- :sym\n- !ruby/object:ActiveSupport::HashWithIndifferentAccess\n  a: b\n",
		"--- !ruby/hash:ActiveSupport::HashWithIndifferentAccess\nkey: \"v\\u3042\"\nlist:\n- - x\n  - y\nn: ~\n",
		"--- &a\nx: *a\ny: |-\n  text\n  more\nz: 2026-01-01 00:00:00 Z\n",
	} {
		f.Add(s)
	}
	// エイリアスの展開による指数的な増加（billion laughs）
	f.Add("a: &a [x,x,x,x,x,x,x,x,x]\nb: &b [*a,*a,*a,*a,*a,*a,*a,*a,*a]\nc: &c [*b,*b,*b,*b,*b,*b,*b,*b,*b]\nd: &d [*c,*c,*c,*c,*c,*c,*c,*c,*c]\ne: [*d,*d,*d,*d,*d,*d,*d,*d,*d]\n")
	f.Fuzz(func(t *testing.T, src string) {
		secoracle.Bounded(t, len(src), 4096, func() {
			if v, err := Decode(src); err == nil {
				_ = Plain(v)
			}
		})
	})
}
