// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package archive

import (
	"archive/tar"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

type rawEntry struct {
	name string
	typ  byte
	body []byte
}

// rawArchive はマニフェストとエントリを任意に組んだ（悪意のある）アーカイブを作る。
func rawArchive(t *testing.T, m Manifest, entries []rawEntry, zopts ...zstd.EOption) []byte {
	t.Helper()
	m.Format, m.FormatVersion = FormatName, FormatVersion
	mj, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw, err := zstd.NewWriter(&out, zopts...)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(zw)
	all := append([]rawEntry{{name: ManifestPath, body: mj}}, entries...)
	for _, e := range all {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		h := &tar.Header{Name: e.name, Typeflag: typ, Mode: 0o644, Size: int64(len(e.body))}
		if typ == tar.TypeSymlink {
			h.Linkname, h.Size = "/etc/passwd", 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func drain(r *Reader) error {
	for {
		e, err := r.Next()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if e.Kind == KindTable {
			rows := e.Rows()
			for rows.Next() {
			}
			if err := rows.Err(); err != nil {
				return err
			}
		}
	}
}

// 1 行が上限を超える ndjson は全体を読み込まずにエラーにする（メモリを使い尽くさない）。
func TestRowsLineTooLong(t *testing.T) {
	old := MaxRowBytes
	MaxRowBytes = 1024
	defer func() { MaxRowBytes = old }()
	long := []byte(`{"id":1,"name":"` + strings.Repeat("a", 4096) + `"}` + "\n")
	data := rawArchive(t, Manifest{Tables: []Table{{Name: "issues", Path: TablesDir + "issues.ndjson", Size: int64(len(long)), SHA256: sum(long)}}},
		[]rawEntry{{name: TablesDir + "issues.ndjson", body: long}})
	r, err := NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := drain(r); err == nil || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("err = %v, want line too long", err)
	}

	// 上限以内の行は読める
	br := bufio.NewReaderSize(bytes.NewReader([]byte("abc\ndef")), 16)
	if l, err := ReadLine(br); string(l) != "abc\n" || err != nil {
		t.Errorf("ReadLine = %q, %v", l, err)
	}
	if l, err := ReadLine(br); string(l) != "def" || err != io.EOF {
		t.Errorf("ReadLine = %q, %v", l, err)
	}
}

// tar ヘッダのサイズがマニフェストと違うエントリは、展開する前に拒否する
// （小さいと宣言して巨大な内容を書き出させない）。
func TestEntrySizeMismatchRejectedEarly(t *testing.T) {
	body := []byte(strings.Repeat("x", 10000))
	data := rawArchive(t, Manifest{Files: []File{{Path: "2026/01/a.bin", Size: 10, SHA256: sum(body[:10])}}},
		[]rawEntry{{name: FilesDir + "2026/01/a.bin", body: body}})
	r, err := NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	_, err = r.Next()
	if err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("err = %v, want size mismatch at Next", err)
	}
}

// シンボリックリンク・パス走査のエントリは無視または拒否される。
func TestSymlinkAndTraversalEntries(t *testing.T) {
	data := rawArchive(t, Manifest{}, []rawEntry{
		{name: FilesDir + "link", typ: tar.TypeSymlink},
	})
	r, err := NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := drain(r); err != nil {
		t.Errorf("symlink: %v", err)
	}
	r.Close()

	evil := []byte("evil")
	data = rawArchive(t, Manifest{}, []rawEntry{{name: FilesDir + "../../evil", body: evil}})
	r, err = NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := drain(r); err == nil {
		t.Error("unlisted traversal entry accepted")
	}
	r.Close()
}

// 巨大なウィンドウを要求する zstd フレームは拒否する（伸長に大量のメモリを確保させない）。
func TestZstdHugeWindowRejected(t *testing.T) {
	// フレームヘッダを直接組む: マジック、FHD=0（内容サイズなし・単一セグメントでない）、
	// Window_Descriptor（指数 19 → 2^29 = 512MB）、空の最終 raw ブロック
	frame := []byte{0x28, 0xb5, 0x2f, 0xfd, 0x00, 19 << 3, 0x01, 0x00, 0x00}
	if _, err := NewReader(bytes.NewReader(frame)); err == nil || !strings.Contains(err.Error(), "window") {
		t.Errorf("err = %v, want window size error", err)
	}
	// 通常の Writer の出力（8MB ウィンドウ）は読める
	data := rawArchive(t, Manifest{}, nil)
	r, err := NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
}
