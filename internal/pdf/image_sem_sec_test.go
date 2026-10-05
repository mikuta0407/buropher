// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package pdf

import (
	"bytes"
	"image"
	"image/png"
	"testing"
	"time"
)

// TestRegisterImageConcurrencyLimited は画像の展開・登録が同時実行数の上限を守ること
// （上限まで使われている間は待ち、空いたら進む）。
func TestRegisterImageConcurrencyLimited(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	for range cap(pdfImageSlots) {
		pdfImageSlots <- struct{}{}
	}
	done := make(chan bool, 1)
	go func() {
		d := New(nil, Options{Locale: "en"})
		_, _, _, ok := d.registerImage(buf.Bytes())
		done <- ok
	}()
	select {
	case <-done:
		t.Fatal("registerImage ran while all image slots were in use")
	case <-time.After(200 * time.Millisecond):
	}
	for range cap(pdfImageSlots) {
		<-pdfImageSlots
	}
	select {
	case ok := <-done:
		if !ok {
			t.Error("registerImage failed")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("registerImage did not proceed after slots were released")
	}
}
