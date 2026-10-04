// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package archive

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/mikuta0407/buropher/internal/secoracle"
)

// fuzzArchive はマニフェスト（JSON の本文そのもの）と 1 つのエントリから zstd 圧縮の tar を作る。
// ファジングで zstd の圧縮列そのものを当てるのは難しいため、tar・マニフェスト・ndjson の層を対象にする。
func fuzzArchive(t testing.TB, manifest []byte, name string, body []byte) []byte {
	var out bytes.Buffer
	zw, err := zstd.NewWriter(&out, zstd.WithEncoderLevel(zstd.SpeedFastest))
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(zw)
	for _, e := range []struct {
		name string
		body []byte
	}{{ManifestPath, manifest}, {name, body}} {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(e.body))}); err != nil {
			return nil // tar に書けない名前
		}
		_, _ = tw.Write(e.body)
	}
	_ = tw.Close()
	_ = zw.Close()
	return out.Bytes()
}

func manifestFor(kind, name string, body []byte) []byte {
	sum := sha256.Sum256(body)
	h := hex.EncodeToString(sum[:])
	if kind == "file" {
		return []byte(fmt.Sprintf(`{"format":%q,"format_version":%d,"tables":[],"files":[{"path":%q,"size":%d,"sha256":%q}]}`,
			FormatName, FormatVersion, strings.TrimPrefix(name, FilesDir), len(body), h))
	}
	return []byte(fmt.Sprintf(`{"format":%q,"format_version":%d,"tables":[{"name":"issues","path":%q,"columns":[{"name":"id","type":"integer"}],"rows":1,"size":%d,"sha256":%q}],"files":[]}`,
		FormatName, FormatVersion, name, len(body), h))
}

// FuzzReader は悪意のあるアーカイブ（任意のマニフェスト・エントリ名・ndjson）を読んでも panic せず、
// 時間・割り当てが入力長に比例する範囲に収まること、ファイルエントリの名前が CleanFilePath を通ったなら
// 保存先の内側に留まることを確かめる。
//
//	go test -run '^$' -fuzz FuzzReader ./internal/redmineimport/archive
func FuzzReader(f *testing.F) {
	row := []byte("{\"id\":1,\"subject\":\"x\",\"t\":\"2026-01-01T00:00:00Z\",\"b\":{\"$b64\":\"AA==\"}}\n")
	f.Add(manifestFor("table", "tables/issues.ndjson", row), "tables/issues.ndjson", row)
	f.Add(manifestFor("file", "files/2026/01/a.txt", []byte("abc")), "files/2026/01/a.txt", []byte("abc"))
	f.Add(manifestFor("file", "files/../../etc/passwd", []byte("x")), "files/../../etc/passwd", []byte("x"))
	f.Add([]byte(`{"format":"x"}`), "tables/x.ndjson", []byte("{\"a\":[[[[[[1]]]]]]}\n\n"))
	f.Add(manifestFor("table", "tables/issues.ndjson", []byte(`{"id":1e999999999}`+"\n")), "tables/issues.ndjson", []byte(`{"id":1e999999999}`+"\n"))
	f.Fuzz(func(t *testing.T, manifest []byte, name string, body []byte) {
		data := fuzzArchive(t, manifest, name, body)
		if data == nil {
			return
		}
		secoracle.Bounded(t, len(manifest)+len(name)+len(body), 128, func() {
			r, err := NewReader(bytes.NewReader(data))
			if err != nil {
				return
			}
			defer r.Close()
			for range 4 {
				e, err := r.Next()
				if err != nil {
					if !errors.Is(err, io.EOF) && strings.Contains(err.Error(), "panic") {
						t.Fatal(err)
					}
					return
				}
				switch e.Kind {
				case KindTable:
					rows := e.Rows()
					for rows.Next() {
						_ = rows.Row()
					}
				case KindFile:
					if c, err := CleanFilePath(e.Name); err == nil {
						root := "/srv/files"
						j := filepath.Join(root, filepath.FromSlash(c))
						if rel, err := filepath.Rel(root, j); err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
							t.Fatalf("file entry %q cleans to %q outside the root (%q)", e.Name, c, j)
						}
					}
					_, _ = io.Copy(io.Discard, e.Reader())
				}
			}
		})
	})
}

// FuzzDecodeRow は ndjson の 1 行のデコードが panic せず、割り当てが行の長さに比例する範囲に収まることを確かめる。
func FuzzDecodeRow(f *testing.F) {
	for _, s := range []string{`{"id":1}`, `{"b":{"$b64":"AAAA"},"n":null,"f":1.5,"s":"あ"}`, `{"a":{"$b64":"!!"}}`, `{"x":1e400}`, `[1]`, `{"a":{"$t":1}}`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, line []byte) {
		secoracle.Bounded(t, len(line), 64, func() { _, _ = DecodeRow(line) })
	})
}
