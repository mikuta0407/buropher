// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package helper

import "testing"

// Redmine 7.0 の IconsHelper#icon_for_mime_type（#43797 / #43805）。
func TestIconForMimeType(t *testing.T) {
	cases := map[string]string{
		"text/x-ruby":     "text-x-ruby",
		"application/pdf": "application-pdf",
		"text/plain":      "text-plain",
		"text/markdown":   "text-plain",
		"text/x-textile":  "text-plain",
		"text/x-diff":     "file",
		"image/png":       "photo",
		"image/gif":       "photo",
		"audio/mpeg":      "file-music",
		"video/mp4":       "movie",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   "file-type-docx",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         "file-type-xls",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation": "file-type-ppt",
		"application/msword":       "file",
		"application/octet-stream": "file",
		"":                         "file",
	}
	for mime, want := range cases {
		if got := IconForMimeType(mime); got != want {
			t.Errorf("IconForMimeType(%q) = %q, want %q", mime, got, want)
		}
	}
}
