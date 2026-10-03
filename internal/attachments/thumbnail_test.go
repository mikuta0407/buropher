// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package attachments

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 寸法だけが巨大な画像（展開するとメモリを使い尽くす）はサムネイルを作らない。
func TestGenerateThumbnailRejectsHugeDimensions(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	// IHDR（シグネチャ 8 バイト + 長さ 4 + "IHDR" 4 の後）の幅・高さを 40000x40000 にし、CRC を付け直す
	binary.BigEndian.PutUint32(b[16:], 40000)
	binary.BigEndian.PutUint32(b[20:], 40000)
	binary.BigEndian.PutUint32(b[29:], crc32.ChecksumIEEE(b[12:29]))
	dir := t.TempDir()
	src := filepath.Join(dir, "bomb.png")
	if err := os.WriteFile(src, b, 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Store{Root: dir}
	err := s.generateThumbnail(src, filepath.Join(dir, "t", "bomb.thumb"), 100)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v, want too large", err)
	}

	// 通常の画像は作れる（一時ファイルは残らない）
	buf.Reset()
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 300, 200))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "t", "ok.thumb")
	if err := s.generateThumbnail(src, target, 100); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "t"))
	if len(entries) != 1 || entries[0].Name() != "ok.thumb" {
		t.Errorf("thumbnail dir = %v", entries)
	}
}
