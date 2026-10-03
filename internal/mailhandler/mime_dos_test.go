// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package mailhandler

import (
	"strings"
	"testing"
	"time"
)

// 大量の継続行を持つヘッダの解析が線形時間で終わる（文字列の += による二乗時間の連結をしない）。
func TestParseHugeFoldedHeaderIsLinear(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("From: a@example.net\r\nSubject: x\r\n")
	line := " " + strings.Repeat("a", 60) + "\r\n"
	for range 150000 { // 約 9MB
		sb.WriteString(line)
	}
	sb.WriteString("\r\nbody\r\n")
	start := time.Now()
	p := Parse([]byte(sb.String()))
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("parse took %v", d)
	}
	if v, _ := p.HeaderValue("Subject"); len(v) < 150000*60 {
		t.Fatalf("subject truncated: %d", len(v))
	}
}

// message/rfc822 の入れ子を無制限にたどらない（各段で再解析するため二乗時間・深い再帰になる）。
func TestAttachmentsNestedRFC822Bounded(t *testing.T) {
	const levels = 20000
	var sb strings.Builder
	for range levels {
		sb.WriteString("Content-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\nContent-Type: message/rfc822\r\n\r\n")
	}
	sb.WriteString("Content-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\nContent-Disposition: attachment; filename=x.txt\r\n\r\nhello\r\n--b--\r\n")
	start := time.Now()
	_ = Parse([]byte(sb.String())).Attachments()
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("attachments took %v", d)
	}
}

// 浅い message/rfc822 の中の添付は従来どおり取り出す。
func TestAttachmentsShallowRFC822(t *testing.T) {
	raw := "Content-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\nContent-Type: message/rfc822\r\n\r\n" +
		"Content-Type: multipart/mixed; boundary=c\r\n\r\n--c\r\nContent-Disposition: attachment; filename=x.txt\r\n\r\nhello\r\n--c--\r\n--b--\r\n"
	atts := Parse([]byte(raw)).Attachments()
	if len(atts) != 1 || atts[0].Filename() != "x.txt" {
		t.Fatalf("got %d", len(atts))
	}
}
