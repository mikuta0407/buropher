// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package csvimport

import (
	"testing"

	"github.com/mikuta0407/buropher/internal/secoracle"
)

func FuzzParse(f *testing.F) {
	f.Add([]byte("a,b,c\n1,\"x\"\"y\",3\r\n"), ",", `"`, "")
	f.Add([]byte("\xef\xbb\xbfa;b\n\"1;2\";3\n"), ";", "'", "UTF-8")
	f.Add([]byte("\x82\xa0,b\n"), ",", `"`, "Shift_JIS")
	f.Fuzz(func(t *testing.T, data []byte, sep, quote, enc string) {
		o := Options{Separator: sep, Wrapper: quote, Encoding: enc}
		secoracle.Bounded(t, len(data), 64, func() {
			_ = Parse(data, o, func(Row) bool { return true })
			_, _ = FirstRows(data, o, 3)
		})
	})
}
