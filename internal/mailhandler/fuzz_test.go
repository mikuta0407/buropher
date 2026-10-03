package mailhandler

import "testing"

func FuzzParse(f *testing.F) {
	f.Add([]byte("From: John <john@example.net>\r\nTo: redmine@example.net\r\nSubject: =?UTF-8?B?44GC?= [ecookbook - Bug #1]\r\nContent-Type: multipart/mixed; boundary=xx\r\n\r\n--xx\r\nContent-Type: text/plain; charset=ISO-2022-JP\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nProject: ecookbook\r\n=E3=81=82\r\n--xx\r\nContent-Type: application/octet-stream; name=a.txt\r\nContent-Disposition: attachment; filename*=UTF-8''%E3%81%82.txt\r\nContent-Transfer-Encoding: base64\r\n\r\nYWJj\r\n--xx--\r\n"))
	f.Add([]byte("Subject: x\nContent-Type: text/html; charset=utf-8\n\n<p>hi<br>there</p>"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_ = Parse(raw)
		_ = ParseAddressList(string(raw))
		_ = PlainTextBodyToText(string(raw))
	})
}
