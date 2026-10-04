// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package mail

import (
	"bufio"
	"bytes"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

// FuzzMessageHeaders は利用者由来の値（件名・差出人表示名・宛先・X-Redmine-* の値）を入れたメッセージを直列化し、
//
//   - ヘッダ部の改行は CRLF のみで、継続行（畳み込み）以外の行は既知のヘッダ名で始まる（ヘッダの注入が無い）
//   - 本文との区切り（空行）がヘッダ部の途中に現れない
//   - textproto で読み戻したヘッダ名の集合が期待どおり
//
// を確かめる。
//
//	go test -run '^$' -fuzz FuzzMessageHeaders ./internal/mail
func FuzzMessageHeaders(f *testing.F) {
	f.Add("subject", "John <john@example.net>", "a@example.net", "<x@y>", "Bug")
	f.Add("a\r\nBcc: evil@example.net", "x\r\n\r\n<script>", "a@example.net\r\nX-Evil: 1", "<a>\nX: y", "\r\nX-Injected: 1")
	f.Add(strings.Repeat("あ", 100)+"\n.\r\n", "\"q\\\"\" <a@b>", "b@c, d@e", "", "\x00\x0b\x0c")
	f.Fuzz(func(t *testing.T, subject, from, to, ref, val string) {
		m := &Message{
			From:       from,
			To:         []string{to},
			Cc:         []string{to + "x"},
			Subject:    subject,
			MessageID:  "fixed@example.net",
			InReplyTo:  ref,
			References: ref,
			Text:       "body",
			Date:       time.Unix(0, 0),
		}
		m.SetHeader("X-Redmine-Project", val)
		m.SetHeader("X-Redmine-Issue-Assignee", subject)
		raw, err := m.Bytes()
		if err != nil {
			return
		}
		end := bytes.Index(raw, []byte("\r\n\r\n"))
		if end < 0 {
			t.Fatalf("no header/body separator in %q", raw)
		}
		head := string(raw[:end+2])
		allowed := map[string]bool{
			"Date": true, "From": true, "To": true, "Cc": true, "Message-ID": true, "In-Reply-To": true,
			"References": true, "Subject": true, "Mime-Version": true, "Content-Type": true,
			"Content-Transfer-Encoding": true, "X-Redmine-Project": true, "X-Redmine-Issue-Assignee": true,
		}
		if strings.Contains(strings.ReplaceAll(head, "\r\n", ""), "\r") || strings.Contains(strings.ReplaceAll(head, "\r\n", ""), "\n") {
			t.Fatalf("bare CR or LF in header section: %q", head)
		}
		for _, line := range strings.Split(strings.TrimSuffix(head, "\r\n"), "\r\n") {
			if line == "" {
				t.Fatalf("empty line inside header section: %q", head)
			}
			if line[0] == ' ' || line[0] == '\t' {
				continue // 畳み込みの継続行
			}
			name, _, ok := strings.Cut(line, ":")
			if !ok || !allowed[name] {
				t.Fatalf("unexpected header line %q (injected?)\n%q", line, head)
			}
		}
		h, err := textproto.NewReader(bufio.NewReader(bytes.NewReader(raw))).ReadMIMEHeader()
		if err != nil {
			t.Fatalf("headers do not parse: %v\n%q", err, head)
		}
		for k := range h {
			if !allowed[k] && !allowed[strings.ReplaceAll(k, "-Id", "-ID")] {
				t.Fatalf("unexpected header %q after parsing\n%q", k, head)
			}
		}
	})
}
