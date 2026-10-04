// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package rails

import "testing"

// Loofah は data: の URI を許可プロトコルに含めつつ、メディアタイプ（ALLOWED_URI_DATA_MEDIATYPES）で絞り込む。
// その分割の結果、内容を持つ data: URI は実質すべて除かれる。移植は data を無条件に許していたため
// <a href="data:text/html,<script>..."> が残っていた（ファジングで発見）。
func TestSanitizeDropsDataURIs(t *testing.T) {
	for in, want := range map[string]string{
		`<a href="data:text/html,<script>alert(1)</script>">x</a>`: `<a>x</a>`,
		`<a href="DATA:text/html;base64,PHNjcmlwdD4=">x</a>`:       `<a>x</a>`,
		`<img src="data:image/svg+xml,<svg onload=alert(1)>">`:     `<img>`,
		`<a href=" data:,x">x</a>`:                                 `<a>x</a>`,
		`<a href="http://example.com/data:x">ok</a>`:               `<a href="http://example.com/data:x">ok</a>`,
		`<a href="mailto:a@example.com">m</a>`:                     `<a href="mailto:a@example.com">m</a>`,
	} {
		if got := string(Sanitize(in)); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}
