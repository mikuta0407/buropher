package rubyyaml

import "testing"

func FuzzDecode(f *testing.F) {
	for _, s := range []string{
		"---\n- 1\n- :sym\n- !ruby/object:ActiveSupport::HashWithIndifferentAccess\n  a: b\n",
		"--- !ruby/hash:ActiveSupport::HashWithIndifferentAccess\nkey: \"v\\u3042\"\nlist:\n- - x\n  - y\nn: ~\n",
		"--- &a\nx: *a\ny: |-\n  text\n  more\nz: 2026-01-01 00:00:00 Z\n",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		if v, err := Decode(src); err == nil {
			_ = Plain(v)
		}
	})
}
