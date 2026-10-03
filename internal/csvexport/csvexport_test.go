// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package csvexport

import "testing"

func TestWriter(t *testing.T) {
	w := New(Options{Encoding: "UTF-8"})
	w.Strings("Login", "Name")
	w.Row(S("a,b"), S(`say "hi"`))
	w.Row(S(""), Nil())
	want := "\xEF\xBB\xBFLogin,Name\n\"a,b\",\"say \"\"hi\"\"\"\n\"\",\n"
	if got := string(w.Bytes()); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestEncodingFallback(t *testing.T) {
	// 不明な文字コードは l(:general_csv_encoding)、変換できない文字は "?"
	w := New(Options{Separator: ";", Encoding: "bogus", DefaultEncoding: "ISO-8859-1"})
	w.Strings("café", "日本")
	if got := string(w.Bytes()); got != "caf\xe9;??\n" {
		t.Errorf("got %q", got)
	}
}
