// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package mailhandler

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikuta0407/buropher/internal/attachments"
	"github.com/mikuta0407/buropher/internal/secoracle"
)

// FuzzParse は受信メールの解析（MIME・ヘッダの復号・文字コード・本文の取り出し）が任意の入力で panic せず、
// 入力長に対して時間・割り当てが極端に増えないこと、添付ファイル名が保存時の無害化
// （attachments.SanitizeFilename）後にパス区切り・NUL を含まないことを確かめる。
//
//	go test -run '^$' -fuzz FuzzParse ./internal/mailhandler
func FuzzParse(f *testing.F) {
	f.Add([]byte("From: John <john@example.net>\r\nTo: redmine@example.net\r\nSubject: =?UTF-8?B?44GC?= [ecookbook - Bug #1]\r\nContent-Type: multipart/mixed; boundary=xx\r\n\r\n--xx\r\nContent-Type: text/plain; charset=ISO-2022-JP\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nProject: ecookbook\r\n=E3=81=82\r\n--xx\r\nContent-Type: application/octet-stream; name=a.txt\r\nContent-Disposition: attachment; filename*=UTF-8''%E3%81%82.txt\r\nContent-Transfer-Encoding: base64\r\n\r\nYWJj\r\n--xx--\r\n"))
	f.Add([]byte("Subject: x\nContent-Type: text/html; charset=utf-8\n\n<p>hi<br>there</p>"))
	f.Add([]byte("Content-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\nContent-Disposition: attachment; filename*=UTF-8''..%2F..%2Fetc%2Fpasswd%00.txt\r\n\r\nx\r\n--b\r\nContent-Type: text/plain; name=\"=?UTF-8?B?Li5cLi5cd2luLmluaQ==?=\"\r\nContent-Disposition: attachment\r\n\r\ny\r\n--b\r\nContent-Location: /etc/x\\y\r\nContent-Disposition: attachment\r\n\r\nz\r\n--b--\r\n"))
	f.Add([]byte("Content-Type: message/rfc822\r\n\r\nContent-Type: multipart/mixed; boundary=c\r\n\r\n--c\r\nContent-Disposition: attachment; filename*0*=UTF-8''a%2F; filename*1=b\r\n\r\nq\r\n--c--\r\n"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		secoracle.Bounded(t, len(raw), 64, func() {
			p := Parse(raw)
			for _, a := range p.Attachments() {
				name := a.Filename()
				san := attachments.SanitizeFilename(name)
				if strings.ContainsAny(san, "/\\\x00") {
					t.Fatalf("attachment filename %q sanitizes to unsafe %q", name, san)
				}
				// 保存先のファイル名（createDiskfile: "<タイムスタンプ>_" + DiskFilenameBase）はパスの 1 要素に収まる
				// （表示名が "." や ".." でも、前置により保存先の外は指さない）
				if disk := "260101000000_" + attachments.DiskFilenameBase(san); !filepath.IsLocal(disk) || strings.ContainsAny(disk, "/\\") {
					t.Fatalf("attachment filename %q gives disk name %q", name, disk)
				}
				_ = a.DecodedBody()
			}
			_ = p.Subject()
			_ = p.From()
			_ = HTMLToText(string(raw), "textile")
			_ = ParseAddressList(string(raw))
			_ = PlainTextBodyToText(string(raw))
		})
	})
}
