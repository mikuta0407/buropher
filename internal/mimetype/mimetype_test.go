// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package mimetype

import "testing"

func TestOf(t *testing.T) {
	cases := map[string]string{
		"test.txt":      "text/plain",
		"TEST.TXT":      "text/plain",
		"a.tar.gz":      "application/gzip",
		"image.JPG":     "image/jpeg",
		"doc.pdf":       "application/pdf",
		"README.md":     "text/markdown",
		"x.textile":     "text/x-textile",
		"noext":         "",
		".bashrc":       "",
		"trailing.":     "",
		"dir.d/noext":   "",
		"unknown.zzzzz": "",
	}
	for in, want := range cases {
		if got := Of(in); got != want {
			t.Errorf("Of(%q) = %q, want %q", in, got, want)
		}
	}
	if !IsType("image", "a.png") || IsType("image", "a.txt") {
		t.Error("IsType")
	}
	if CSSClassOf("a.txt") != "text-plain" || MainMimetypeOf("a.mp4") != "video" {
		t.Error("CSSClassOf / MainMimetypeOf")
	}
	for in, want := range map[string]string{"a.tar.gz": ".gz", ".bashrc": "", "a.": ".", "..a.b": ".b", "dir.d/x": "", "noext": ""} {
		if got := Extname(in); got != want {
			t.Errorf("Extname(%q) = %q, want %q", in, got, want)
		}
	}
}
