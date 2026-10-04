// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package importer

import (
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// FuzzCleanRelPath は添付の相対パスの検証（アーカイブのエントリ名・attachments 行の disk_directory/disk_filename）を
// 通った値を保存先に連結した結果が、必ず保存先の内側に留まることを確かめる。
//
//	go test -run '^$' -fuzz FuzzCleanRelPath ./internal/redmineimport/importer
func FuzzCleanRelPath(f *testing.F) {
	for _, s := range []string{"2026/01/a.txt", "../etc/passwd", "/etc/passwd", "a/../../b", `..\..\win.ini`, "a\\..\\..\\b",
		"./.", "a/./b", "C:/x", "a\x00b", "files/../../x", "..", "....//....", "a/..", "\\\\server\\share"} {
		f.Add(s)
	}
	const root = "/srv/buropher/files"
	f.Fuzz(func(t *testing.T, p string) {
		for _, in := range []string{p, path.Clean(p)} { // source.go はそのまま、conv_poly.go は path.Clean してから渡す
			c, err := cleanRelPath(in)
			if err != nil {
				continue
			}
			if !filepath.IsLocal(filepath.FromSlash(c)) {
				t.Fatalf("cleanRelPath(%q) = %q is not a local path", in, c)
			}
			joined := filepath.Join(root, filepath.FromSlash(c))
			rel, err := filepath.Rel(root, joined)
			if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				t.Fatalf("cleanRelPath(%q) = %q escapes the root: %q", in, c, joined)
			}
			if strings.ContainsRune(c, 0) {
				t.Fatalf("cleanRelPath(%q) = %q contains NUL", in, c)
			}
		}
	})
}
