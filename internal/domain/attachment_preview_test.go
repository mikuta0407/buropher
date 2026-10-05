// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package domain

import "testing"

// test/unit/attachment_test.rb の test_thumbnailable_should_be_true_for_images /
// test_thumbnailable_should_be_true_for_illustrator_files / test_is_pdf の移植。
func TestAttachmentThumbnailableAndIsPDF(t *testing.T) {
	for _, fn := range []string{"test.avif", "test.jpg", "test.webp", "test.pdf", "test.ai"} {
		if !(&Attachment{Filename: fn}).Thumbnailable() {
			t.Errorf("%s: thumbnailable? = false", fn)
		}
	}
	if (&Attachment{Filename: "test.txt"}).Thumbnailable() {
		t.Error("test.txt: thumbnailable? = true")
	}
	old := ThumbnailPDFAvailable
	ThumbnailPDFAvailable = false
	defer func() { ThumbnailPDFAvailable = old }()
	if (&Attachment{Filename: "test.ai"}).Thumbnailable() || (&Attachment{Filename: "test.pdf"}).Thumbnailable() {
		t.Error("pdf / ai thumbnailable without gs")
	}

	// Illustrator のファイルは PDF 互換のものがあっても PDF ではない
	for fn, want := range map[string]bool{"report.pdf": true, "logo.ai": false} {
		if got := (&Attachment{Filename: fn}).IsPDF(); got != want {
			t.Errorf("IsPDF(%s) = %v", fn, got)
		}
	}
	if !(&Attachment{Filename: "a.avif"}).IsImage() || !(&Attachment{Filename: "a.svg"}).IsImageType() {
		t.Error("avif image? / svg is_image?")
	}
}
