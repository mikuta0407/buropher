// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package mailhandler

import (
	"slices"
	"strings"
	"testing"
)

func TestHTMLToText(t *testing.T) {
	src := `<html><head><title>T</title><style>p{}</style></head><body>Hello <b>bold</b> <i>it</i> <a href="http://x/">link</a> <a href="http://y/"></a><br><h2>Head</h2><p>para</p><table><tr><th>A</th><td>1</td></tr></table><!-- c --></body></html>`
	cases := map[string]string{
		"textile":     "Hello *bold* _it_ \"link\":http://x/ http://y/ \n\nh2. Head\n\npara\n\n*A*\n1",
		"common_mark": "Hello **bold** *it* [link](http://x/) http://y/ \n\n## Head\n\npara\n\n*A*\n1",
		"":            "Hello bold it link \n\nHead\n\npara\n\nA\n\n1",
	}
	for f, want := range cases {
		if got := HTMLToText(src, f); got != want {
			t.Errorf("%q:\n got %q\nwant %q", f, got, want)
		}
	}
}

func TestPlainTextBodyToText(t *testing.T) {
	got := PlainTextBodyToText("  indented\n   * item\n # head\nnormal\n\t tab")
	want := "indented\n * item\n # head\nnormal\n\t tab"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestDecodeWords(t *testing.T) {
	cases := map[string]string{
		"=?utf-8?b?w4TDpCDDlsO2?=":                   "Ää Öö",
		"Re: =?ISO-2022-JP?B?GyRCJUYlOSVIGyhC?=":     "Re: テスト",
		"=?utf-8?q?a_b?= =?utf-8?q?c?= plain":        "a bc plain",
		"=?UTF-8?B?44OG?= =?UTF-8?B?44K544OI?=":      "テスト",
		"=?utf-8?B?4oCZ?=x =?unknown-cs?Q?abc?= end": "’x abc end",
	}
	for in, want := range cases {
		if got := decodeWords(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestParseAddressList(t *testing.T) {
	as := ParseAddressList(`"John Smith" <JSmith@somenet.foo>, jdoe@example.net (John Doe), <redmine@somenet.foo>, undisclosed-recipients:;, "a, b" <ab@x.org>`)
	var got []string
	for _, a := range as {
		got = append(got, a.Address+"|"+a.DisplayName+"|"+strings.Join(a.Comments, ","))
	}
	want := []string{"JSmith@somenet.foo|John Smith|", "jdoe@example.net||John Doe", "redmine@somenet.foo||", "ab@x.org|a, b|"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q", got)
	}
	if as[0].Local != "JSmith" || as[0].Domain != "somenet.foo" {
		t.Errorf("local/domain = %q %q", as[0].Local, as[0].Domain)
	}
}

func TestParseMultipartWithRFC2231Filename(t *testing.T) {
	raw := "From: a@b.c\r\nContent-Type: multipart/mixed; boundary=XX\r\n\r\npreamble\r\n--XX\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nbody\r\n--XX\r\n" +
		"Content-Type: application/octet-stream\r\nContent-Disposition: attachment;\r\n filename*0*=utf-8''%E3%83%86;\r\n filename*1=st.bin\r\nContent-Transfer-Encoding: base64\r\n\r\nAAEC\r\n--XX--\r\nepilogue\r\n"
	p := Parse([]byte(raw))
	if len(p.Parts) != 2 {
		t.Fatalf("parts = %d", len(p.Parts))
	}
	atts := p.Attachments()
	if len(atts) != 1 || atts[0].Filename() != "テst.bin" || string(atts[0].DecodedBody()) != "\x00\x01\x02" {
		t.Errorf("attachment = %d %q", len(atts), atts[0].Filename())
	}
	if string(p.Parts[0].DecodedBody()) != "body" {
		t.Errorf("text = %q", p.Parts[0].DecodedBody())
	}
}

func TestDispatchByMessageID(t *testing.T) {
	c := setup(t)
	// Redmine 6.1 の Message-ID（末尾に .<uid> が付く）でも返信先が分かる
	raw := "From: JSmith@somenet.foo\nTo: redmine@somenet.foo\nSubject: hello\nIn-Reply-To: <redmine.issue-2.20260115120000.0a1b2c@example.net>\n\nreply body\n"
	obj, err := c.h.Receive(c.ctx, []byte(raw), Options{})
	c.must(err)
	j := c.assertJournal(obj)
	if j.IssueID != 2 {
		t.Errorf("issue = %d", j.IssueID)
	}
	// References だけでもよい
	raw = "From: JSmith@somenet.foo\nSubject: hello\nReferences: <foo@bar> <redmine.message-2.20260115120000@example.net>\n\nreply body\n"
	obj, err = c.h.Receive(c.ctx, []byte(raw), Options{})
	c.must(err)
	if m := c.assertMessage(obj); m.ParentID == nil || *m.ParentID != 1 {
		t.Errorf("message parent = %v", m.ParentID)
	}
	// 受信処理の無い種類は無視する
	raw = "From: JSmith@somenet.foo\nSubject: hello\nIn-Reply-To: <redmine.wiki_page-1.20260115120000@example.net>\n\nreply body\n"
	before := c.count(`SELECT COUNT(*) FROM issues`)
	obj, err = c.h.Receive(c.ctx, []byte(raw), Options{Issue: map[string]string{"project": "ecookbook"}})
	c.must(err)
	if obj != nil || c.count(`SELECT COUNT(*) FROM issues`) != before {
		t.Errorf("got %T", obj)
	}
}
