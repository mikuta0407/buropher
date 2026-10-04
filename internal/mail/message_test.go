// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package mail

import (
	"bytes"
	"strings"
	"testing"
)

// TestBytesNoBareCR は本文の単独の CR / LF を CRLF にそろえること（"\r.\r\n" のような並びを中継 MTA が
// 本文の終わりと解釈する SMTP smuggling を利用者の入力から作れないように）。
func TestBytesNoBareCR(t *testing.T) {
	for _, html := range []string{"", "<p>x</p>"} {
		m := &Message{From: "from@example.com", To: []string{"to@example.com"}, Subject: "s",
			Text: "line1\r.\r\nMAIL FROM:<evil@example.com>\rline3\nline4", HTML: html}
		raw, err := m.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		for i, c := range raw {
			if c == '\r' && (i+1 >= len(raw) || raw[i+1] != '\n') {
				t.Fatalf("html=%q: bare CR at %d in %q", html, i, raw)
			}
			if c == '\n' && (i == 0 || raw[i-1] != '\r') {
				t.Fatalf("html=%q: bare LF at %d in %q", html, i, raw)
			}
		}
	}
}

// TestBytesHeaderInjection は Subject・From・追加ヘッダに CR/LF を混ぜても
// 新しいヘッダが挿入されない（ヘッダインジェクションにならない）ことを確認する。
func TestBytesHeaderInjection(t *testing.T) {
	m := &Message{
		From:    FormatAddress("Evil\r\nBcc: victim@example.com", "from@example.com"),
		To:      []string{"to@example.com"},
		Subject: "Hello\r\nReply-To: attacker@example.com\r\n\r\n<injected body>",
		Text:    "body",
		Headers: []Header{{Name: "X-Test", Value: "a\r\nX-Injected: 1"}},
	}
	raw, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	// ヘッダ部（最初の空行まで）を取り出す
	headerPart := raw
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		headerPart = raw[:i]
	}
	// 生の CR/LF が値に残っていないこと（= 新しいヘッダ行が増えていないこと）を、
	// 各行の先頭のヘッダ名で確認する（継続行は WSP 始まり）。
	allowed := map[string]bool{
		"Date": true, "From": true, "To": true, "Cc": true, "Message-ID": true,
		"In-Reply-To": true, "References": true, "Subject": true, "Mime-Version": true,
		"Content-Type": true, "Content-Transfer-Encoding": true, "X-Test": true,
	}
	for _, line := range strings.Split(string(headerPart), "\r\n") {
		if line == "" || line[0] == ' ' || line[0] == '\t' {
			continue // 継続行
		}
		name, _, ok := strings.Cut(line, ":")
		if !ok || !allowed[name] {
			t.Errorf("unexpected header line (injection?): %q\nfull:\n%s", line, headerPart)
		}
	}
	// 本来の Subject の可読部分は残る
	if !bytes.Contains(headerPart, []byte("Subject: Hello")) {
		t.Errorf("Subject lost:\n%s", headerPart)
	}
}

// TestBytesHeaderControlChars は CR/LF 以外の制御文字（NUL・VT・FF など）もヘッダ値に生のまま出ないことを確認する。
// 生の NUL 等を含むヘッダは RFC 5322 違反で、MTA によっては拒否・切り詰められる（ファジングで発見）。
func TestBytesHeaderControlChars(t *testing.T) {
	m := &Message{From: "from@example.com", To: []string{"to@example.com"}, Subject: "a\x00b\x0bc\x0cd\x1be\x7ff\tg", Text: "body"}
	m.SetHeader("X-Redmine-Project", "p\x00\x0b\x0c")
	raw, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	head := raw[:bytes.Index(raw, []byte("\r\n\r\n"))]
	for _, c := range head {
		if (c < 0x20 && c != '\r' && c != '\n' && c != '\t') || c == 0x7f {
			t.Fatalf("raw control byte %#x in header section:\n%q", c, head)
		}
	}
}
