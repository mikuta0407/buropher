// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package helper

import "testing"

// IconsHelper#icon_for_mime_type（Redmine 7.0 の #43797: 添付一覧にファイルの種類のアイコンを出す）。
func TestIconForMimeType(t *testing.T) {
	cases := map[string]string{
		"text/x-ruby":     "text-x-ruby",
		"application/pdf": "application-pdf",
		"application/zip": "application-zip",
		"text/plain":      "text-plain",
		"text/markdown":   "text-plain",
		"text/x-textile":  "text-plain",
		"text/csv":        "file",
		"image/png":       "photo",
		"image/svg+xml":   "photo",
		"audio/mpeg":      "file-music",
		"video/mp4":       "movie",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   "file-type-docx",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         "file-type-xls",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation": "file-type-ppt",
		"application/msword":       "file",
		"application/octet-stream": "file",
		"":                         "file",
	}
	for in, want := range cases {
		if got := iconForMimeType(in); got != want {
			t.Errorf("iconForMimeType(%q) = %q, want %q", in, got, want)
		}
	}
}
