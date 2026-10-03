// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package importer

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/mikuta0407/buropher/internal/redmineimport/archive"
)

// マニフェストのテーブル名は一時ディレクトリのファイル名に使うので、"../" などを含む名前は拒否する
// （細工したアーカイブで一時ディレクトリの外にファイルを書けないように）。
func TestOpenSourceRejectsUnsafeTableName(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"../../escaped", "/abs", "Issues", "a.b"} {
		body := []byte("{}\n")
		sum := sha256.Sum256(body)
		m := archive.Manifest{Format: archive.FormatName, FormatVersion: archive.FormatVersion,
			Tables: []archive.Table{{Name: name, Path: "tables/x.ndjson", Rows: 1, Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}}}
		mb, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "a.tar.zst")
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		zw, err := zstd.NewWriter(f)
		if err != nil {
			t.Fatal(err)
		}
		tw := tar.NewWriter(zw)
		for _, e := range []struct {
			name string
			data []byte
		}{{archive.ManifestPath, mb}, {"tables/x.ndjson", body}} {
			if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.data)), Typeflag: tar.TypeReg}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write(e.data); err != nil {
				t.Fatal(err)
			}
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		tmp := filepath.Join(dir, "work", "tmp")
		if err := os.MkdirAll(tmp, 0o755); err != nil {
			t.Fatal(err)
		}
		s, err := openSource(context.Background(), p, tmp, "")
		if err == nil {
			s.close()
			t.Errorf("table name %q: accepted", name)
		} else if !strings.Contains(err.Error(), "invalid table name") {
			t.Errorf("table name %q: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "work", "escaped.ndjson")); err == nil {
			t.Errorf("table name %q: file written outside the temp dir", name)
		}
	}
}
