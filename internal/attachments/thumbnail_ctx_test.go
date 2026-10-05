// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package attachments

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mikuta0407/buropher/internal/domain"
)

// サムネイルの生成の枠が埋まっている間に要求の ctx が終わったら（クライアントの切断など）、
// 枠を待ち続けずに諦める。待ち続けると切断済みの要求のゴルーチンが溜まり続けた。
func TestThumbnailWaitHonoursContext(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "files")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 300, 200))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "x.png"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Store{Root: root, ThumbnailsRoot: filepath.Join(dir, "thumbs")}
	a := &domain.Attachment{Filename: "x.png", DiskFilename: "x.png", Filesize: int64(buf.Len()), Digest: strings.Repeat("cd", 32)}
	// 生成の枠をすべて埋める
	for range cap(thumbnailGenSem) {
		thumbnailGenSem <- struct{}{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() {
		_, ok := s.Thumbnail(ctx, a, 100)
		done <- ok
	}()
	cancel()
	select {
	case ok := <-done:
		if ok {
			t.Error("thumbnail generated after cancellation")
		}
	case <-time.After(5 * time.Second):
		t.Error("Thumbnail kept waiting for a slot after the context was canceled")
	}
	for range cap(thumbnailGenSem) {
		<-thumbnailGenSem
	}
	// 枠が空いていれば従来どおり作る
	if _, ok := s.Thumbnail(context.Background(), a, 100); !ok {
		t.Error("thumbnail not generated")
	}
}
